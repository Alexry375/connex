package demo

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// Line est une ligne produite par le simulateur vivant.
type Line struct {
	Machine string // la machine concernée
	Value   any    // model.Snapshot, model.Patch, model.MachineStatus ou model.Reply
}

// Live fait vivre le simulateur en temps réel et répond aux requêtes (§4.3) comme
// le ferait `connex stream` sur chaque machine, avec des délais réalistes. C'est
// la source de l'affichage en mode démo.
type Live struct {
	lines chan Line
	reqs  chan liveReq
	done  chan struct{}
}

type liveReq struct {
	machine string
	req     model.Request
}

// action est un effet d'une requête, dû delay après la requête.
type action struct {
	delay time.Duration
	due   time.Time
	run   func() []any
}

// ErrBusy : trop de requêtes en attente.
var ErrBusy = errors.New("simulateur : trop de requêtes en attente")

// ErrClosed : le simulateur est arrêté.
var ErrClosed = errors.New("simulateur arrêté")

// StartLive démarre le simulateur. speed compte les secondes simulées par
// seconde réelle ; 0 fige le monde (seules les requêtes le font changer). Il
// s'arrête avec ctx, et ferme alors Lines.
func StartLive(ctx context.Context, seed int64, speed float64) *Live {
	l := &Live{lines: make(chan Line, 64), reqs: make(chan liveReq, 32), done: make(chan struct{})}
	go l.run(ctx, seed, speed)
	return l
}

// Lines renvoie le flux des lignes : d'abord un relevé par machine.
func (l *Live) Lines() <-chan Line { return l.lines }

// Send transmet une requête à une machine simulée. La réponse arrive dans Lines.
func (l *Live) Send(machine string, req model.Request) error {
	select {
	case <-l.done:
		return ErrClosed
	case l.reqs <- liveReq{machine, req}:
		return nil
	default:
		return ErrBusy
	}
}

func (l *Live) run(ctx context.Context, seed int64, speed float64) {
	defer close(l.lines)
	defer close(l.done)
	start := time.Now()
	s := newSim(rand.New(rand.NewPCG(uint64(seed), 0x636f6e6e6578)), start)
	fx := rand.New(rand.NewPCG(uint64(seed), 0x616374696f6e)) // délais des actions
	var queue []Line
	push := func(vals []any) {
		for _, v := range vals {
			if t, ok := v.(tagged); ok {
				queue = append(queue, Line{Machine: t.machine, Value: t.reply})
				continue
			}
			queue = append(queue, Line{Machine: machineOf(v), Value: v})
		}
	}
	for _, snap := range s.snapshots() {
		push([]any{snap})
	}
	var actions []action
	// advance amène l'horloge simulée au présent, avant d'appliquer une action.
	advance := func() {
		el := time.Since(start).Seconds()
		if speed > 0 {
			el *= speed
		}
		if el > s.now {
			s.now = el
		}
	}
	for {
		var due time.Time
		if speed > 0 && len(s.queue) > 0 {
			due = start.Add(time.Duration(s.peek() / speed * float64(time.Second)))
		}
		for _, a := range actions {
			if due.IsZero() || a.due.Before(due) {
				due = a.due
			}
		}
		var timer <-chan time.Time
		var t *time.Timer
		if !due.IsZero() {
			t = time.NewTimer(time.Until(due))
			timer = t.C
		}
		var out chan<- Line
		var first Line
		if len(queue) > 0 {
			out, first = l.lines, queue[0]
		}
		select {
		case <-ctx.Done():
			if t != nil {
				t.Stop()
			}
			return
		case out <- first:
			queue = queue[1:]
		case r := <-l.reqs:
			advance()
			now, more := s.handle(r.machine, r.req, fx)
			push(now)
			for _, a := range more {
				a.due = time.Now().Add(a.delay)
				actions = append(actions, a)
			}
		case <-timer:
			now := time.Now()
			kept := actions[:0]
			var ready []action
			for _, a := range actions {
				if !a.due.After(now) {
					ready = append(ready, a)
				} else {
					kept = append(kept, a)
				}
			}
			actions = kept
			for _, a := range ready {
				advance()
				push(a.run())
			}
			if speed > 0 {
				for len(s.queue) > 0 && !start.Add(time.Duration(s.peek()/speed*float64(time.Second))).After(now) {
					push(s.step())
				}
			}
		}
		if t != nil {
			t.Stop()
		}
	}
}

func machineOf(v any) string {
	switch x := v.(type) {
	case model.Snapshot:
		return x.Machine.Name
	case model.Patch:
		return x.Machine
	case model.MachineStatus:
		return x.Machine
	}
	return ""
}

// after construit une action due d après la requête.
func after(d time.Duration, run func() []any) action {
	return action{delay: d, run: run}
}

func between(r *rand.Rand, lo, hi time.Duration) time.Duration {
	return lo + time.Duration(r.Int64N(int64(hi-lo)+1))
}

// handle traite une requête : renvoie les lignes immédiates et les actions à
// venir. Les réponses portent l'id de la requête ; les effets passent par
// s.change, donc sortent en patchs comme ceux d'un vrai stream.
func (s *sim) handle(machine string, req model.Request, r *rand.Rand) ([]any, []action) {
	reply := func(ok bool, format string, args ...any) model.Reply {
		rep := model.Reply{Schema: model.Schema, Type: "reply", ID: req.ID, OK: ok}
		if !ok {
			rep.Error = fmt.Sprintf(format, args...)
		}
		return rep
	}
	out := func(vals ...any) []any { return vals }
	m := s.machineNamed(machine)
	switch {
	case m == nil:
		return out(tagged{machine, reply(false, "machine inconnue : %s", machine)}), nil
	case !m.reachable:
		return out(tagged{machine, reply(false, "%s est injoignable", machine)}), nil
	}
	fail := func(d time.Duration, format string, args ...any) []action {
		rep := reply(false, format, args...)
		return []action{after(d, func() []any { return out(tagged{machine, rep}) })}
	}
	// step applique fn à la machine et renvoie les patchs produits.
	step := func(fn func()) []any {
		s.out = nil
		s.change(m, fn)
		o := s.out
		s.out = nil
		return o
	}

	switch req.Op {
	case model.ReqRefresh:
		return nil, []action{after(between(r, 150*time.Millisecond, 400*time.Millisecond), func() []any {
			return out(tagged{machine, reply(true, "")})
		})}

	case model.ReqStart, model.ReqStop, model.ReqRestart:
		key := req.Target
		a := m.app(key)
		if a == nil {
			return nil, fail(between(r, 80*time.Millisecond, 200*time.Millisecond), "aucune appli « %s » sur %s", key, machine)
		}
		p := m.appProject(key)
		running := a.State == model.AppRunning || a.State == model.AppStarting
		switch {
		case req.Op == model.ReqStart && running:
			return nil, fail(between(r, 100*time.Millisecond, 250*time.Millisecond), "« %s » tourne déjà", key)
		case req.Op == model.ReqStop && !running:
			return nil, fail(between(r, 100*time.Millisecond, 250*time.Millisecond), "« %s » est déjà arrêtée", key)
		case req.Op == model.ReqStop:
			return nil, []action{after(between(r, 300*time.Millisecond, 900*time.Millisecond), func() []any {
				lines := step(func() {
					if p.server != "" {
						m.removeListener(p.server)
						p.server = ""
					}
					m.setApp(key, model.AppStopped, p)
				})
				return append(lines, tagged{machine, reply(true, "")})
			})}
		}
		// start, ou restart : on arrête d'abord ce qui tourne, puis on lance.
		first := between(r, 150*time.Millisecond, 350*time.Millisecond)
		up := first + between(r, 800*time.Millisecond, 2200*time.Millisecond)
		return nil, []action{
			after(first, func() []any {
				return step(func() {
					if p.server != "" {
						m.removeListener(p.server)
						p.server = ""
					}
					m.setApp(key, model.AppStarting, p)
				})
			}),
			after(up, func() []any {
				var holder *model.Listener
				lines := step(func() { holder = s.appUp(m, key) })
				if holder != nil {
					who := "un autre compte"
					if holder.Mine {
						who = fmt.Sprintf("%s (pid %d)", holder.Process, holder.PID)
					}
					return append(lines, tagged{machine, reply(false, "le port %d est déjà pris par %s", a.Port, who)})
				}
				return append(lines, tagged{machine, reply(true, "")})
			}),
		}

	case model.ReqFreePort:
		port, err := strconv.Atoi(req.Target)
		if err != nil || port < 1 || port > 65535 {
			return nil, fail(50*time.Millisecond, "port invalide : %q", req.Target)
		}
		var mine []string
		for _, l := range m.snap.Listeners {
			if l.Port != port {
				continue
			}
			if !l.Mine {
				return nil, fail(between(r, 100*time.Millisecond, 300*time.Millisecond),
					"le port %d est tenu par un autre compte (uid %d) : connex ne peut pas le libérer", port, l.UID)
			}
			mine = append(mine, l.Key)
		}
		if len(mine) == 0 {
			return nil, fail(between(r, 100*time.Millisecond, 250*time.Millisecond), "rien n'écoute sur le port %d", port)
		}
		return nil, []action{after(between(r, 400*time.Millisecond, 1200*time.Millisecond), func() []any {
			lines := step(func() {
				for _, key := range mine {
					m.removeListener(key)
					for _, p := range m.projects {
						if p.server != key {
							continue
						}
						p.server = ""
						if p.app != "" {
							m.setApp(p.app, model.AppStopped, p)
						}
					}
				}
			})
			return append(lines, tagged{machine, reply(true, "")})
		})}
	}
	return out(tagged{machine, reply(false, "opération inconnue : %q", req.Op)}), nil
}

// tagged est une réponse accompagnée de sa machine.
type tagged struct {
	machine string
	reply   model.Reply
}

func (s *sim) machineNamed(name string) *machine {
	for _, m := range s.machines {
		if m.info.Name == name {
			return m
		}
	}
	return nil
}

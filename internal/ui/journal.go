package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
)

// tone colore une nouvelle.
type tone int

const (
	toneNeutral tone = iota
	toneGood
	toneWarn // à surveiller : un port ouvert à tous
	toneCall // un agent t'attend
	toneBad
)

// entry est une ligne du journal d'observation : ce qui vient de changer.
type entry struct {
	at      time.Time // réception, horloge locale
	machine string
	text    string
	tone    tone
	item    string // élément concerné, pour y aller depuis le journal
	sign    rune   // + − ~ ou ◐ pour une machine
}

// maxJournal borne le journal gardé en mémoire.
const maxJournal = 400

// describe traduit un changement en nouvelle. prev est le relevé de la machine
// avant l'événement, next celui d'après. ok est faux pour un changement sans
// intérêt à raconter.
func describe(c fleet.Change, prev, next *model.Snapshot, m *fleet.Machine, ev *model.MachineStatus, lostFor time.Duration, home string) (entry, bool) {
	e := entry{at: c.At, machine: c.Machine, sign: '~'}
	switch c.Kind {
	case fleet.KindMachine:
		e.sign = '◐'
		e.item = ""
		switch {
		case m.Known && m.Reachable && prev == nil:
			// Premier relevé, que la machine soit nouvelle ou qu'elle n'ait
			// donné jusque-là que son état.
			s := &m.Snapshot
			n, _ := portNumbers(s)
			e.text = "premier relevé : " + plural(n, "port", "ports") + ", " +
				plural(len(s.Agents), "agent", "agents") + ", " + plural(len(s.Repos), "dépôt", "dépôts")
			e.tone = toneGood
		case c.Op == model.OpAdd && m.Reachable:
			return e, false // joignable, mais rien de relevé encore : rien à raconter
		case !m.Reachable:
			e.text = "injoignable"
			if ev != nil && ev.Reason != "" {
				e.text += " : " + ev.Reason
			}
			e.tone = toneBad
		default:
			e.text = "de nouveau joignable"
			if lostFor > 0 {
				e.text += ", après " + durFR(lostFor) + " de silence"
			}
			e.tone = toneGood
		}
		return e, true

	case model.KindListener:
		k := kindPort
		switch c.Op {
		case model.OpAdd:
			e.sign = '+'
			l := findListener(next, c.Key)
			if l == nil {
				return e, false
			}
			if !l.Mine {
				k = kindForeign
			}
			e.text = "port " + strconv.Itoa(l.Port) + " apparu · " + listenerWho(l, home)
			if l.Exposure == model.ExposureAll {
				e.text += " · " + listenerExposureFR(l)
				e.tone = toneWarn
			}
		case model.OpRemove:
			e.sign = '−'
			l := findListener(prev, c.Key)
			if l == nil {
				return e, false
			}
			if !l.Mine {
				k = kindForeign
			}
			e.text = "port " + strconv.Itoa(l.Port) + " disparu · " + listenerWho(l, home)
		default:
			old, l := findListener(prev, c.Key), findListener(next, c.Key)
			if old == nil || l == nil {
				return e, false
			}
			if !l.Mine {
				k = kindForeign
			}
			switch {
			case old.PID != l.PID && l.PID != 0:
				e.text = "port " + strconv.Itoa(l.Port) + " · " + l.Program() + " relancé (pid " + strconv.Itoa(l.PID) + ")"
			case old.Exposure != l.Exposure || old.Funnel != l.Funnel:
				e.text = "port " + strconv.Itoa(l.Port) + " : " + listenerExposureFR(old) + " → " + listenerExposureFR(l)
			default:
				e.text = "port " + strconv.Itoa(l.Port) + " modifié"
			}
		}
		e.item = itemID(c.Machine, k, c.Key)
		return e, true

	case model.KindRepo:
		e.item = itemID(c.Machine, kindRepo, c.Key)
		switch c.Op {
		case model.OpAdd:
			e.sign = '+'
			r := findRepo(next, c.Key)
			if r == nil {
				return e, false
			}
			e.text = "dépôt " + baseName(r.Path) + " trouvé · " + branchFR(r.Branch, r.Detached)
		case model.OpRemove:
			e.sign = '−'
			e.text = "dépôt " + baseName(c.Key) + " disparu"
		default:
			old, r := findRepo(prev, c.Key), findRepo(next, c.Key)
			if old == nil || r == nil {
				return e, false
			}
			var parts []string
			if dirtyFR(old.Dirty) != dirtyFR(r.Dirty) {
				switch dirtyFR(r.Dirty) {
				case "modifié":
					parts = append(parts, "modifié")
					e.tone = toneWarn
				case "propre":
					parts = append(parts, "redevenu propre")
					e.tone = toneGood
				default:
					parts = append(parts, "état devenu inconnu")
				}
			}
			if old.Branch != r.Branch || old.Detached != r.Detached {
				parts = append(parts, "passe sur "+branchFR(r.Branch, r.Detached))
			}
			for _, wt := range r.Worktrees {
				if !hasWorktree(old, wt.Path) {
					parts = append(parts, "worktree "+wt.Branch+" ouvert")
				}
			}
			for _, wt := range old.Worktrees {
				if !hasWorktree(r, wt.Path) {
					parts = append(parts, "worktree "+wt.Branch+" fermé")
				}
			}
			if len(parts) == 0 {
				return e, false
			}
			e.text = "dépôt " + baseName(r.Path) + " " + strings.Join(parts, ", ")
		}
		return e, true

	case model.KindAgent:
		e.item = itemID(c.Machine, kindAgent, c.Key)
		switch c.Op {
		case model.OpAdd:
			e.sign = '+'
			a := findAgent(next, c.Key)
			if a == nil {
				return e, false
			}
			e.text = a.Tool + " commence dans " + baseName(a.CWD)
			if a.State == model.StateWaiting {
				e.tone = toneCall
			}
		case model.OpRemove:
			e.sign = '−'
			a := findAgent(prev, c.Key)
			if a == nil {
				return e, false
			}
			e.text = a.Tool + " a fini dans " + baseName(a.CWD)
		default:
			old, a := findAgent(prev, c.Key), findAgent(next, c.Key)
			if old == nil || a == nil {
				return e, false
			}
			switch {
			case old.State != a.State && a.State == model.StateWaiting:
				e.text = a.Tool + " t’attend dans " + baseName(a.CWD)
				e.tone = toneCall
			case old.State != a.State && a.State == model.StateWorking:
				e.text = a.Tool + " se remet au travail dans " + baseName(a.CWD)
			case old.State != a.State:
				e.text = a.Tool + " " + agentStateFR(a.State) + " dans " + baseName(a.CWD)
			case old.CWD != a.CWD:
				e.text = a.Tool + " passe dans " + shortPath(a.CWD, home)
			default:
				return e, false
			}
		}
		return e, true

	case model.KindApp:
		e.item = itemID(c.Machine, kindApp, c.Key)
		switch c.Op {
		case model.OpAdd:
			e.sign = '+'
			e.text = "appli " + c.Key + " inscrite"
		case model.OpRemove:
			e.sign = '−'
			e.text = "appli " + c.Key + " désinscrite"
		default:
			old, a := findApp(prev, c.Key), findApp(next, c.Key)
			if old == nil || a == nil {
				return e, false
			}
			switch {
			case old.State != a.State:
				e.text = "appli " + a.Name + " " + appStateFR(a.State)
				switch a.State {
				case model.AppFailed:
					e.tone = toneBad
				case model.AppRunning:
					e.tone = toneGood
				}
			case old.Health.HTTP != a.Health.HTTP:
				e.text = "appli " + a.Name + " répond " + httpFR(a.Health.HTTP)
				if a.Health.HTTP >= 400 || a.Health.HTTP == 0 {
					e.tone = toneBad
				} else {
					e.tone = toneGood
				}
			default:
				return e, false
			}
		}
		return e, true
	}
	return e, false
}

// listenerWho dit qui écoute : « node · ~/Global/Projets/Clepsydre », ou
// « autre compte (uid 990) ».
func listenerWho(l *model.Listener, home string) string {
	if !l.Mine {
		return "autre compte (uid " + strconv.Itoa(l.UID) + ")"
	}
	dir := l.ProjectRoot
	if dir == "" {
		dir = l.CWD
	}
	if dir == "" {
		return l.Program()
	}
	return l.Program() + " · " + shortPath(dir, home)
}

func findListener(s *model.Snapshot, key string) *model.Listener {
	if s == nil {
		return nil
	}
	for i := range s.Listeners {
		if s.Listeners[i].Key == key {
			return &s.Listeners[i]
		}
	}
	return nil
}

func findRepo(s *model.Snapshot, key string) *model.Repo {
	if s == nil {
		return nil
	}
	for i := range s.Repos {
		if s.Repos[i].Key == key {
			return &s.Repos[i]
		}
	}
	return nil
}

func findAgent(s *model.Snapshot, key string) *model.Agent {
	if s == nil {
		return nil
	}
	for i := range s.Agents {
		if s.Agents[i].Key == key {
			return &s.Agents[i]
		}
	}
	return nil
}

func findApp(s *model.Snapshot, key string) *model.App {
	if s == nil {
		return nil
	}
	for i := range s.Apps {
		if s.Apps[i].Key == key {
			return &s.Apps[i]
		}
	}
	return nil
}

func hasWorktree(r *model.Repo, p string) bool {
	for _, wt := range r.Worktrees {
		if wt.Path == p {
			return true
		}
	}
	return false
}

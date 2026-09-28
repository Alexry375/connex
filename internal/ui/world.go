package ui

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
)

// itemID désigne un élément d'une machine d'un événement à l'autre. Pour une
// machine entière, kind vaut fleet.KindMachine et key son nom.
type itemID struct{ machine, kind, key string }

// tone colore une ligne de la chronique.
type tone int

const (
	toneNeutral tone = iota
	toneGood         // revenu, en marche, action faite
	toneWarn         // réponse HTTP en erreur, dépôt modifié
	toneAlert        // échec, injoignable, refus
	toneCall         // un agent t'attend
	toneOpen         // ouvert à tous
)

// record est une ligne de la chronique : ce qui vient de changer.
type record struct {
	at   time.Time
	id   itemID
	op   string
	text string
	tone tone
	back bool // trouvé dans le relevé d'une machine qui revient : daté « au retour »
}

// mark retient le dernier changement d'un élément.
type mark struct {
	at   time.Time
	op   string
	tone tone
	back bool
}

// ghost garde un élément disparu quelques instants, pour qu'on voie ce qui
// vient de partir, à sa place.
type ghost struct {
	at   time.Time
	data any // model.Listener, model.Agent, model.Repo ou model.App
	back bool
}

// pending est une requête envoyée, sans réponse encore.
type pending struct {
	id      string
	machine string
	op      string
	target  string
	item    itemID
	at      time.Time
}

// outcome est la réponse à une requête.
type outcome struct {
	pending
	ok   bool
	err  string
	done time.Time
}

// Durées de vie de ce qui signale un changement.
const (
	ghostTTL    = 60 * time.Second  // un élément disparu reste visible, grisé
	recentTTL   = 120 * time.Second // un élément changé garde sa marque
	resultTTL   = 15 * time.Second  // le résultat d'une action reste sur sa ligne
	silentAfter = 20 * time.Second  // une requête sans réponse cesse de tourner et peut se refaire
	chronLimit  = 400               // lignes gardées dans la chronique
)

// world est ce que l'interface sait : l'état des machines (fleet) et ce qu'elle
// a observé en plus — chronique, causes des coupures, éléments disparus,
// requêtes en cours. Tout vient des événements de la source.
type world struct {
	fleet   *fleet.Fleet
	reason  map[string]string // machine → cause de la dernière coupure, telle que la source la donne
	away    map[string]bool   // machines coupées depuis leur dernier relevé complet
	chron   []record          // du plus ancien au plus récent
	recent  map[itemID]mark
	ghosts  map[itemID]ghost
	since   map[itemID]time.Time // agents : dernier changement d'état vu par connex
	pending map[string]*pending  // par identifiant de requête
	results map[itemID]outcome   // dernière réponse, par élément visé
	last    outcome              // la dernière réponse reçue
	answers int                  // nombre de réponses reçues
	seq     int
	version int // change à chaque modification visible
	added   int // lignes de chronique écrites depuis le début
}

func newWorld() *world {
	return &world{
		fleet:   fleet.New(),
		reason:  map[string]string{},
		away:    map[string]bool{},
		recent:  map[itemID]mark{},
		ghosts:  map[itemID]ghost{},
		since:   map[itemID]time.Time{},
		pending: map[string]*pending{},
		results: map[itemID]outcome{},
	}
}

// apply intègre un événement de la source.
func (w *world) apply(ev source.Event, now time.Time) {
	if ev.Reply != nil {
		w.reply(ev, now)
		return
	}
	var before model.Snapshot
	downSince := time.Time{}
	if m := w.fleet.Machine(ev.Machine); m != nil {
		if m.Known {
			before = m.Snapshot
		}
		if !m.Reachable {
			downSince = m.Since
		}
	}
	// Ce que le relevé d'une machine qui revient montre de nouveau a pu
	// arriver à n'importe quel moment de la coupure : connex ne sait que
	// l'avoir vu au retour, et ne lui prête ni heure ni durée.
	back := ev.Snapshot != nil && w.away[ev.Machine]
	changes := w.fleet.Apply(ev, now)
	if ev.Status != nil && !ev.Status.Reachable {
		w.reason[ev.Machine] = ev.Status.Reason
		w.away[ev.Machine] = true
	}
	if ev.Snapshot != nil {
		delete(w.away, ev.Machine)
	}
	m := w.fleet.Machine(ev.Machine)
	if m == nil {
		return
	}
	home := homeOf(m.Snapshot.Machine.User)
	if home == "" {
		home = homeOf(before.Machine.User)
	}
	for _, c := range changes {
		id := itemID{c.Machine, c.Kind, c.Key}
		if c.Kind == fleet.KindMachine {
			w.machineRecord(m, c, downSince, now)
			continue
		}
		old, _ := find(before, c.Kind, c.Key)
		cur, _ := find(m.Snapshot, c.Kind, c.Key)
		text, t := describe(c.Op, c.Kind, old, cur, home)
		w.add(record{at: now, id: id, op: c.Op, text: text, tone: t, back: back})
		w.recent[id] = mark{at: now, op: c.Op, tone: t, back: back}
		switch c.Op {
		case model.OpRemove:
			if old != nil {
				w.ghosts[id] = ghost{at: now, data: old, back: back}
			}
			delete(w.since, id)
		case model.OpAdd:
			delete(w.ghosts, id)
			if c.Kind == model.KindAgent {
				w.stateSince(id, now, back)
			}
		case model.OpUpdate:
			if c.Kind == model.KindAgent {
				o, _ := old.(model.Agent)
				n, _ := cur.(model.Agent)
				if o.State != n.State {
					w.stateSince(id, now, back)
				}
			}
		}
	}
	if len(changes) > 0 {
		w.version++
	}
}

// stateSince retient quand un agent a changé d'état ; pour un changement vu
// au retour d'une coupure, l'heure est inconnue et rien n'est retenu.
func (w *world) stateSince(id itemID, now time.Time, back bool) {
	if back {
		delete(w.since, id)
		return
	}
	w.since[id] = now
}

func (w *world) machineRecord(m *fleet.Machine, c fleet.Change, downSince, now time.Time) {
	id := itemID{m.Name, fleet.KindMachine, m.Name}
	switch {
	case !m.Reachable:
		text := m.Name + " injoignable"
		if r := w.reason[m.Name]; r != "" {
			text += " — " + r
		}
		w.add(record{at: now, id: id, op: c.Op, text: text, tone: toneAlert})
	case c.Op == model.OpAdd:
		w.add(record{at: now, id: id, op: c.Op, text: m.Name + " : premier relevé", tone: toneNeutral})
	default:
		text := m.Name + " de nouveau joignable"
		if !downSince.IsZero() {
			text += ", après " + span(now.Sub(downSince))
		}
		delete(w.reason, m.Name)
		w.add(record{at: now, id: id, op: c.Op, text: text, tone: toneGood})
	}
	w.recent[id] = mark{at: now, op: c.Op, tone: w.chron[len(w.chron)-1].tone}
}

// closed dit pourquoi l'affichage s'arrête quand la source ferme son flux,
// avec la cause de la coupure si la source l'a donnée juste avant.
func (w *world) closed() error {
	for _, m := range w.fleet.Machines() {
		if r := w.reason[m.Name]; r != "" && !m.Reachable {
			return fmt.Errorf("%w — %s injoignable : %s", ErrSourceClosed, m.Name, r)
		}
	}
	return ErrSourceClosed
}

func (w *world) add(r record) {
	w.added++
	w.chron = append(w.chron, r)
	if len(w.chron) > chronLimit {
		w.chron = append(w.chron[:0], w.chron[len(w.chron)-chronLimit:]...)
	}
}

// send envoie une requête de la liste fermée. Une erreur immédiate de la
// source devient un refus, affiché comme une réponse.
func (w *world) send(src source.Source, machine, op, target string, item itemID, now time.Time) {
	w.seq++
	p := pending{id: "connex-ui-" + strconv.Itoa(w.seq), machine: machine, op: op, target: target, item: item, at: now}
	delete(w.results, item)
	w.version++
	if err := src.Send(machine, source.NewRequest(p.id, op, target)); err != nil {
		w.finish(p, false, err.Error(), now)
		return
	}
	w.pending[p.id] = &p
}

func (w *world) reply(ev source.Event, now time.Time) {
	p := w.pending[ev.Reply.ID]
	if p == nil {
		return // réponse à une requête qui n'est pas la nôtre
	}
	delete(w.pending, ev.Reply.ID)
	w.finish(*p, ev.Reply.OK, ev.Reply.Error, now)
}

func (w *world) finish(p pending, ok bool, reason string, now time.Time) {
	w.results[p.item] = outcome{pending: p, ok: ok, err: reason, done: now}
	w.last = w.results[p.item]
	w.answers++
	text := actionPhrase(p.op, p.target, p.machine)
	t := toneGood
	if ok {
		text += " — fait"
	} else {
		t = toneAlert
		text += " — refusé"
		if reason != "" {
			text += " : " + reason
		}
	}
	w.add(record{at: now, id: p.item, op: p.op, text: text, tone: t})
	w.version++
}

// pendingFor renvoie la requête en cours sur un élément, s'il y en a une.
func (w *world) pendingFor(id itemID) *pending {
	var best *pending
	for _, p := range w.pending {
		if p.item == id && (best == nil || p.at.After(best.at)) {
			best = p
		}
	}
	return best
}

// prune oublie ce qui a fait son temps ; renvoie vrai si l'affichage change.
func (w *world) prune(now time.Time) bool {
	changed := false
	for id, g := range w.ghosts {
		if now.Sub(g.at) > ghostTTL {
			delete(w.ghosts, id)
			changed = true
		}
	}
	for id, m := range w.recent {
		if now.Sub(m.at) > recentTTL {
			delete(w.recent, id)
			changed = true
		}
	}
	for id, o := range w.results {
		if now.Sub(o.done) > resultTTL {
			delete(w.results, id)
			changed = true
		}
	}
	if changed {
		w.version++
	}
	return changed
}

// find cherche un élément dans un relevé ; renvoie une copie typée.
func find(s model.Snapshot, kind, key string) (any, bool) {
	switch kind {
	case model.KindListener:
		for _, l := range s.Listeners {
			if l.Key == key {
				return l, true
			}
		}
	case model.KindRepo:
		for _, r := range s.Repos {
			if r.Key == key {
				return r, true
			}
		}
	case model.KindAgent:
		for _, a := range s.Agents {
			if a.Key == key {
				return a, true
			}
		}
	case model.KindApp:
		for _, a := range s.Apps {
			if a.Key == key {
				return a, true
			}
		}
	}
	return nil, false
}

// actionPhrase dit une action : « arrêter site », « libérer le port 5000 ».
func actionPhrase(op, target, machine string) string {
	switch op {
	case model.ReqRefresh:
		return "rafraîchir " + machine
	case model.ReqFreePort:
		return "libérer le port " + target
	}
	return opWord(op) + " " + target
}

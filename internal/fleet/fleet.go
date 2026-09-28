// Package fleet tient l'état courant de toutes les machines à partir des
// événements d'une source, et dit ce qui a changé à chaque événement. L'affichage
// n'a donc pas à réappliquer les patchs lui-même. Un Fleet n'est pas protégé
// contre les accès concurrents : un seul fil d'exécution s'en sert.
package fleet

import (
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
)

// KindMachine est la nature d'un changement qui touche une machine entière :
// elle apparaît (OpAdd), décroche ou revient (OpUpdate).
const KindMachine = "machine"

// Machine est l'état connu d'une machine.
type Machine struct {
	Name      string
	Snapshot  model.Snapshot // dernier état connu ; vide tant que Known est faux
	Known     bool           // au moins un relevé reçu
	Reachable bool           // faux entre un Status injoignable et le retour
	Since     time.Time      // dernier changement de joignabilité
	Updated   time.Time      // dernier événement reçu pour cette machine
}

// Change est un changement observé.
type Change struct {
	Machine string
	Op      string // model.OpAdd, model.OpUpdate ou model.OpRemove
	Kind    string // model.KindListener, KindRepo, KindAgent, KindApp, ou KindMachine
	Key     string // clé de l'élément ; nom de la machine pour KindMachine
	At      time.Time
}

// Fleet est l'ensemble des machines vues.
type Fleet struct {
	order  []string
	byName map[string]*Machine
}

// New renvoie un Fleet vide.
func New() *Fleet { return &Fleet{byName: map[string]*Machine{}} }

// Machines renvoie les machines dans l'ordre où elles sont apparues. Les
// pointeurs restent valables ; ne les modifie pas.
func (f *Fleet) Machines() []*Machine {
	out := make([]*Machine, 0, len(f.order))
	for _, n := range f.order {
		out = append(out, f.byName[n])
	}
	return out
}

// Machine renvoie la machine nommée, ou nil.
func (f *Fleet) Machine(name string) *Machine { return f.byName[name] }

func (f *Fleet) get(name string, now time.Time) (*Machine, bool) {
	if m, ok := f.byName[name]; ok {
		return m, false
	}
	m := &Machine{Name: name, Since: now}
	f.byName[name] = m
	f.order = append(f.order, name)
	return m, true
}

// Apply intègre un événement et renvoie ce qui a changé. now date les
// changements. Une réponse (Event.Reply) ne change rien ici. Un patch reçu avant
// tout relevé de sa machine est ignoré.
func (f *Fleet) Apply(ev source.Event, now time.Time) []Change {
	var out []Change
	add := func(op, kind, key string) {
		out = append(out, Change{Machine: ev.Machine, Op: op, Kind: kind, Key: key, At: now})
	}
	switch {
	case ev.Snapshot != nil:
		m, fresh := f.get(ev.Machine, now)
		switch {
		case fresh:
			add(model.OpAdd, KindMachine, m.Name)
		case m.Known:
			for _, c := range model.Diff(m.Snapshot, *ev.Snapshot) {
				add(c.Op, c.Kind, c.Key)
			}
		}
		if !fresh && !m.Reachable {
			m.Since = now
			add(model.OpUpdate, KindMachine, m.Name)
		}
		m.Snapshot, m.Known, m.Reachable, m.Updated = *ev.Snapshot, true, true, now
	case ev.Patch != nil:
		m := f.byName[ev.Machine]
		if m == nil || !m.Known {
			return nil
		}
		m.Snapshot = model.Apply(m.Snapshot, ev.Patch.Changes)
		m.Updated = now
		for _, c := range ev.Patch.Changes {
			add(c.Op, c.Kind, c.Key)
		}
	case ev.Status != nil:
		m, fresh := f.get(ev.Machine, now)
		m.Updated = now
		if fresh {
			m.Reachable = ev.Status.Reachable
			add(model.OpAdd, KindMachine, m.Name)
			return out
		}
		if m.Reachable != ev.Status.Reachable {
			m.Reachable = ev.Status.Reachable
			m.Since = now
			add(model.OpUpdate, KindMachine, m.Name)
		}
	}
	return out
}

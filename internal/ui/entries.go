package ui

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
)

// stratum est une marche de l'échelle : les éléments y sont rangés du plus
// pressant (les agents, ceux qui t'attendent en tête) au plus tranquille (les
// dépôts).
type stratum int

const (
	stAgents stratum = iota // sessions d'agents
	stApps                  // applis inscrites
	stOpen                  // ports ouverts à tous
	stPorts                 // les autres ports
	stRepos                 // dépôts
	stCount
)

func (s stratum) title() string {
	switch s {
	case stAgents:
		return "agents"
	case stApps:
		return "applis"
	case stOpen:
		return "ouverts à tous"
	case stPorts:
		return "ports"
	case stRepos:
		return "dépôts"
	}
	return ""
}

// entry est une ligne de l'échelle. Exactement un des pointeurs est rempli.
type entry struct {
	id       itemID
	stratum  stratum
	machine  *fleet.Machine
	order    int // rang de la machine
	agent    *model.Agent
	app      *model.App
	port     *model.Listener
	repo     *model.Repo
	ghost    bool      // disparu il y a peu
	goneAt   time.Time // quand
	goneBack bool      // disparu pendant une coupure, vu au retour
	hay      string    // texte de recherche, replié
}

// home renvoie le dossier personnel de la machine de l'entrée.
func (e *entry) home() string { return homeOf(e.machine.Snapshot.Machine.User) }

// buildEntries met à plat toutes les machines en lignes d'échelle, rangées.
func buildEntries(w *world) []entry {
	var out []entry
	for i, m := range w.fleet.Machines() {
		if !m.Known {
			continue
		}
		s := &m.Snapshot
		for j := range s.Agents {
			a := &s.Agents[j]
			out = append(out, entry{id: itemID{m.Name, model.KindAgent, a.Key}, stratum: stAgents, machine: m, order: i, agent: a})
		}
		for j := range s.Apps {
			a := &s.Apps[j]
			out = append(out, entry{id: itemID{m.Name, model.KindApp, a.Key}, stratum: stApps, machine: m, order: i, app: a})
		}
		for j := range s.Listeners {
			l := &s.Listeners[j]
			st := stPorts
			if l.Exposure == "all" {
				st = stOpen
			}
			out = append(out, entry{id: itemID{m.Name, model.KindListener, l.Key}, stratum: st, machine: m, order: i, port: l})
		}
		for j := range s.Repos {
			r := &s.Repos[j]
			out = append(out, entry{id: itemID{m.Name, model.KindRepo, r.Key}, stratum: stRepos, machine: m, order: i, repo: r})
		}
	}
	// les disparus récents, à leur place
	machines := w.fleet.Machines()
	for id, g := range w.ghosts {
		i, m := -1, (*fleet.Machine)(nil)
		for k, mm := range machines {
			if mm.Name == id.machine {
				i, m = k, mm
			}
		}
		if m == nil {
			continue
		}
		e := entry{id: id, machine: m, order: i, ghost: true, goneAt: g.at, goneBack: g.back}
		switch d := g.data.(type) {
		case model.Agent:
			e.agent, e.stratum = &d, stAgents
		case model.App:
			e.app, e.stratum = &d, stApps
		case model.Listener:
			e.port, e.stratum = &d, stPorts
			if d.Exposure == "all" {
				e.stratum = stOpen
			}
		case model.Repo:
			e.repo, e.stratum = &d, stRepos
		default:
			continue
		}
		out = append(out, e)
	}
	for i := range out {
		out[i].hay = haystack(&out[i])
	}
	sort.SliceStable(out, func(i, j int) bool { return less(&out[i], &out[j]) })
	return out
}

// less range l'échelle : par marche, puis dans chaque marche par un ordre
// stable qui ne fait pas sauter les lignes à chaque événement.
func less(a, b *entry) bool {
	if a.stratum != b.stratum {
		return a.stratum < b.stratum
	}
	switch a.stratum {
	case stAgents:
		// ceux qui t'attendent d'abord (les disparus ne t'attendent plus)
		if wa, wb := waitingEntry(a), waitingEntry(b); wa != wb {
			return wa
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return a.agent.PID < b.agent.PID
	case stApps:
		if a.order != b.order {
			return a.order < b.order
		}
		return a.app.Name < b.app.Name
	case stOpen:
		if a.port.Mine != b.port.Mine {
			return a.port.Mine
		}
		fallthrough
	case stPorts:
		if a.port.Port != b.port.Port {
			return a.port.Port < b.port.Port
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return a.port.Key < b.port.Key
	case stRepos:
		if a.order != b.order {
			return a.order < b.order
		}
		return a.repo.Path < b.repo.Path
	}
	return a.id.key < b.id.key
}

func waitingEntry(e *entry) bool {
	return !e.ghost && e.agent.State == model.StateWaiting
}

// haystack réunit les mots par lesquels on peut retrouver une entrée.
func haystack(e *entry) string {
	f := []string{e.machine.Name}
	switch {
	case e.agent != nil:
		a := e.agent
		f = append(f, a.Tool, a.CWD, projectName(a.CWD), agentWord(a.State), "agent", "pid"+strconv.Itoa(a.PID))
	case e.app != nil:
		a := e.app
		f = append(f, a.Name, a.Project, a.CWD, a.Unit, appWord(a.State), "appli")
	case e.port != nil:
		l := e.port
		f = append(f, "port", l.Addr, exposureWord(l.Exposure), l.Exposure)
		if l.Mine {
			f = append(f, l.Process, l.CWD, l.ProjectRoot, projectName(listenerDir(*l)))
		} else {
			f = append(f, "autre compte", "uid"+strconv.Itoa(l.UID))
		}
	case e.repo != nil:
		r := e.repo
		f = append(f, "dépôt", r.Path, projectName(r.Path), r.Branch, dirtyWord(r.Dirty))
		for _, wt := range r.Worktrees {
			f = append(f, wt.Path, wt.Branch, "worktree")
		}
	}
	if e.ghost {
		f = append(f, "disparu", "fermé")
	}
	return fold(strings.Join(f, " "))
}

// ports renvoie les numéros de port qu'une entrée porte (le sien, ou celui
// réservé par une appli).
func (e *entry) ports() []int {
	switch {
	case e.port != nil:
		return []int{e.port.Port}
	case e.app != nil && e.app.Port != 0:
		return []int{e.app.Port}
	}
	return nil
}

// match dit si l'entrée répond à la recherche. Chaque mot doit s'y trouver.
// Un mot fait de chiffres cherche un port : il répond aux ports qui commencent
// par ces chiffres (exact d'abord, voir rank), et au pid exact d'un agent ;
// avec loose, il répond aussi aux noms qui le contiennent (« 2025-stage »).
func match(e *entry, tokens []string, loose bool) bool {
	for _, t := range tokens {
		if isDigits(t) {
			ok := loose && strings.Contains(e.hay, t)
			for _, p := range e.ports() {
				if strings.HasPrefix(strconv.Itoa(p), t) {
					ok = true
				}
			}
			if e.agent != nil && strconv.Itoa(e.agent.PID) == t {
				ok = true
			}
			if e.port != nil && e.port.PID != 0 && strconv.Itoa(e.port.PID) == t {
				ok = true
			}
			if !ok {
				return false
			}
			continue
		}
		if !strings.Contains(e.hay, t) {
			return false
		}
	}
	return true
}

// rank classe les résultats d'une recherche : un port exact passe devant.
func rank(e *entry, tokens []string) int {
	r := 0
	for _, t := range tokens {
		if !isDigits(t) {
			continue
		}
		for _, p := range e.ports() {
			if strconv.Itoa(p) == t {
				r--
			}
		}
	}
	return r
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// tokens découpe une recherche en mots repliés.
func tokens(q string) []string { return strings.Fields(fold(q)) }

// filterEntries garde les entrées de la machine choisie (vide : toutes) qui
// répondent à la recherche ; un port exact remonte en tête de sa marche.
func filterEntries(all []entry, machine, query string) []entry {
	toks := tokens(query)
	pick := func(loose bool) []entry {
		out := make([]entry, 0, len(all))
		for i := range all {
			e := &all[i]
			if machine != "" && e.id.machine != machine {
				continue
			}
			if len(toks) > 0 && !match(e, toks, loose) {
				continue
			}
			out = append(out, *e)
		}
		return out
	}
	out := pick(false)
	if len(out) == 0 && slices.ContainsFunc(toks, isDigits) {
		// aucun port ni pid ne répond : les chiffres se cherchent dans les noms
		out = pick(true)
	}
	if len(toks) > 0 {
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].stratum != out[j].stratum {
				return out[i].stratum < out[j].stratum
			}
			return rank(&out[i], toks) < rank(&out[j], toks)
		})
	}
	return out
}

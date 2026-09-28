package ui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
)

// kind est la nature d'un élément du catalogue.
type kind int

const (
	kindError   kind = iota // erreur de relevé
	kindAgent               // session d'agent
	kindApp                 // appli inscrite
	kindPort                // port en écoute de l'utilisateur
	kindForeign             // port en écoute d'un autre compte
	kindRepo                // dépôt git
)

// item est un élément du catalogue, tiré du relevé d'une machine. Les pointeurs
// désignent le relevé courant de la machine (fleet le remplace sans le modifier).
type item struct {
	id   string
	kind kind
	m    *fleet.Machine
	home string

	agent *model.Agent
	app   *model.App
	port  *model.Listener
	repo  *model.Repo
	err   *model.Error

	appPort  *model.Listener   // l'écoute d'une appli qui tourne
	twins    []*model.Listener // mêmes port et programme sur d'autres adresses (IPv4 et IPv6…)
	holder   *model.Listener   // ce qui tient le port d'une appli qui n'écoute pas
	sharers  []*model.Listener // écoutes d'autres programmes sur le même numéro de port
	agentsIn int               // pour un dépôt : agents qui y travaillent

	name string // nom court, pour trier et chercher
	text string // tout le texte cherchable, replié
}

// machineView est une machine et son catalogue trié.
type machineView struct {
	m       *fleet.Machine
	home    string
	items   []*item
	waiting []*item
	agents  int
	unknown int // agents dont la source ne sait pas s'ils attendent
	apps    int
	ports   int
	foreign int
	repos   int
	failing int                   // applis en échec ou qui répondent en erreur
	owners  map[string]*model.App // clé d'écoute → appli qui la tient
}

// world est l'état affichable : les machines dans l'ordre d'apparition.
type world struct {
	machines []*machineView
	byID     map[string]*item
}

func itemID(machine string, k kind, key string) string {
	return machine + "\x1f" + strconv.Itoa(int(k)) + "\x1f" + key
}

// buildWorld tire le catalogue de l'état courant de la flotte.
func buildWorld(f *fleet.Fleet) *world {
	w := &world{byID: map[string]*item{}}
	for _, m := range f.Machines() {
		mv := &machineView{m: m, home: homeOf(m.Snapshot.Machine.User)}
		w.machines = append(w.machines, mv)
		if !m.Known {
			continue
		}
		s := &m.Snapshot
		add := func(it *item) {
			it.m, it.home = m, mv.home
			it.text = fold(it.text + " " + m.Name)
			mv.items = append(mv.items, it)
			w.byID[it.id] = it
			// Le journal nomme chaque écoute par son port : les jumelles et
			// l'écoute d'une appli mènent aussi à l'objet qui les montre.
			for _, l := range append([]*model.Listener{it.appPort}, it.twins...) {
				if l == nil {
					continue
				}
				k := kindForeign
				if l.Mine {
					k = kindPort
				}
				if id := itemID(m.Name, k, l.Key); w.byID[id] == nil {
					w.byID[id] = it
				}
			}
		}
		// Agents : par projet, puis par pid. Un ordre stable se parcourt mieux
		// qu'un ordre qui saute à chaque changement d'état.
		agents := make([]*item, 0, len(s.Agents))
		for i := range s.Agents {
			a := &s.Agents[i]
			it := &item{id: itemID(m.Name, kindAgent, a.Key), kind: kindAgent, agent: a, name: baseName(a.CWD)}
			it.text = strings.Join([]string{a.Tool, agentStateFR(a.State), a.State, a.CWD, "agent", strconv.Itoa(a.PID)}, " ")
			agents = append(agents, it)
			switch a.State {
			case model.StateWaiting:
				mv.waiting = append(mv.waiting, it)
			case model.StateWorking, model.StateIdle:
			default:
				mv.unknown++
			}
		}
		sort.SliceStable(agents, func(i, j int) bool {
			if agents[i].name != agents[j].name {
				return strings.ToLower(agents[i].name) < strings.ToLower(agents[j].name)
			}
			return agents[i].agent.PID < agents[j].agent.PID
		})
		for _, it := range agents {
			add(it)
		}
		mv.agents = len(agents)

		// Applis, et l'écoute qui leur appartient.
		claimed := map[string]*model.App{}
		apps := make([]*item, 0, len(s.Apps))
		for i := range s.Apps {
			a := &s.Apps[i]
			it := &item{id: itemID(m.Name, kindApp, a.Key), kind: kindApp, app: a, name: a.Name}
			if a.Port > 0 {
				for j := range s.Listeners {
					l := &s.Listeners[j]
					if l.Port == a.Port && l.Mine && (under(l.CWD, a.CWD) || under(l.CWD, a.Project)) && claimed[l.Key] == nil {
						it.appPort, claimed[l.Key] = l, a
						break
					}
				}
				for j := range s.Listeners {
					if l := &s.Listeners[j]; it.appPort != nil && claimed[l.Key] == nil && twinOf(l) == twinOf(it.appPort) {
						it.twins, claimed[l.Key] = append(it.twins, l), a
					}
				}
				if it.appPort == nil {
					for j := range s.Listeners {
						if s.Listeners[j].Port == a.Port {
							it.holder = &s.Listeners[j]
							break
						}
					}
				}
			}
			parts := []string{a.Name, "appli", appStateFR(a.State), a.State, a.Project, a.CWD, a.Unit, viewFR(a.View)}
			if a.Port > 0 {
				parts = append(parts, strconv.Itoa(a.Port))
			}
			if it.appPort != nil {
				parts = append(parts, it.appPort.Program(), it.appPort.Process, it.appPort.Exe, listenerExposureFR(it.appPort), it.appPort.Addr)
			}
			for _, l := range it.twins {
				parts = append(parts, l.Addr)
			}
			it.text = strings.Join(parts, " ")
			if a.State == model.AppFailed || (a.State == model.AppRunning && a.Health.HTTP >= 400) {
				mv.failing++
			}
			apps = append(apps, it)
		}
		sort.SliceStable(apps, func(i, j int) bool { return apps[i].app.Name < apps[j].app.Name })

		// Ports : ceux des applis sont décrits avec leur appli ; les écoutes
		// jumelles (même programme, même port, même exposition, autre adresse)
		// ne font qu'un élément.
		var ports, foreign []*item
		firstOf := map[string]*item{}
		for i := range s.Listeners {
			l := &s.Listeners[i]
			if claimed[l.Key] != nil {
				continue
			}
			if first := firstOf[twinOf(l)]; first != nil {
				first.twins = append(first.twins, l)
				first.text += " " + l.Addr
				continue
			}
			k := kindPort
			if !l.Mine {
				k = kindForeign
			}
			it := &item{id: itemID(m.Name, k, l.Key), kind: k, port: l, name: l.Program()}
			parts := []string{strconv.Itoa(l.Port), "port", l.Addr, listenerExposureFR(l), l.Exposure}
			if l.Mine {
				parts = append(parts, l.Program(), l.Process, l.Exe, l.Unit, l.CWD, l.ProjectRoot)
			} else {
				parts = append(parts, "autre compte", "uid "+strconv.Itoa(l.UID))
			}
			it.text = strings.Join(parts, " ")
			firstOf[twinOf(l)] = it
			if l.Mine {
				ports = append(ports, it)
			} else {
				foreign = append(foreign, it)
			}
		}
		// Deux programmes sur le même numéro de port : on le dit des deux côtés.
		for _, it := range append(append([]*item{}, apps...), ports...) {
			own := it.port
			if it.kind == kindApp {
				own = it.appPort
			}
			if own == nil {
				continue
			}
			for j := range s.Listeners {
				l := &s.Listeners[j]
				if l.Port == own.Port && l.Key != own.Key && l.Mine && l.PID != own.PID {
					it.sharers = append(it.sharers, l)
				}
			}
		}
		mv.owners = claimed
		for _, it := range apps {
			add(it)
		}
		for _, it := range ports {
			add(it)
		}
		for _, it := range foreign {
			add(it)
		}
		mv.apps, mv.ports, mv.foreign = len(apps), len(ports), len(foreign)

		// Dépôts, dans l'ordre des chemins.
		for i := range s.Repos {
			r := &s.Repos[i]
			it := &item{id: itemID(m.Name, kindRepo, r.Key), kind: kindRepo, repo: r, name: baseName(r.Path)}
			parts := []string{r.Path, "dépôt", r.Branch, dirtyFR(r.Dirty), r.Upstream}
			if ahead, behind := syncFR(r); ahead != "" || behind != "" {
				parts = append(parts, strings.ReplaceAll(ahead, "\u00a0", " "), strings.ReplaceAll(behind, "\u00a0", " "))
			}
			for _, wt := range r.Worktrees {
				parts = append(parts, wt.Path, wt.Branch, "worktree")
			}
			for _, a := range s.Agents {
				if repoHolds(r, a.CWD) {
					it.agentsIn++
				}
			}
			it.text = strings.Join(parts, " ")
			add(it)
		}
		mv.repos = len(s.Repos)

		// Erreurs de relevé, en dernier : le relevé est produit quand même, et
		// une erreur qui dure (un worktree cassé) ne doit pas pousser les agents
		// hors de l'écran.
		for i := range s.Errors {
			e := &s.Errors[i]
			name := "erreur"
			if path, _ := errorParts(e.Message); path != "" {
				name = baseName(path)
			}
			add(&item{id: itemID(m.Name, kindError, strconv.Itoa(i)), kind: kindError, err: e, name: name,
				text: "erreur " + errorSourceFR(e.Source) + " " + e.Source + " " + e.Message})
		}
	}
	return w
}

// twinOf résume ce qui fait deux écoutes jumelles : même port, même compte,
// même programme, même dossier, même exposition. Seule l'adresse diffère.
func twinOf(l *model.Listener) string {
	return strings.Join([]string{strconv.Itoa(l.Port), strconv.FormatBool(l.Mine), strconv.Itoa(l.UID),
		strconv.Itoa(l.PID), l.Process, l.CWD, l.Exposure, strconv.FormatBool(l.Funnel)}, "\x1f")
}

// portNumbers compte les numéros de port distincts en écoute, et ceux qui sont
// ouverts à tous : un port écouté en IPv4 et en IPv6 compte une fois.
func portNumbers(s *model.Snapshot) (n, open int) {
	seen, exposed := map[int]bool{}, map[int]bool{}
	for _, l := range s.Listeners {
		seen[l.Port] = true
		if l.Exposure == "all" {
			exposed[l.Port] = true
		}
	}
	return len(seen), len(exposed)
}

// under dit si dir est root ou un de ses sous-dossiers.
func under(dir, root string) bool {
	if root == "" || dir == "" {
		return false
	}
	return dir == root || strings.HasPrefix(dir, strings.TrimSuffix(root, "/")+"/")
}

// repoHolds dit si dir est dans le dépôt ou dans un de ses worktrees.
func repoHolds(r *model.Repo, dir string) bool {
	if under(dir, r.Path) {
		return true
	}
	for _, wt := range r.Worktrees {
		if under(dir, wt.Path) {
			return true
		}
	}
	return false
}

// repoOf renvoie le dépôt de la machine qui contient dir.
func repoOf(s *model.Snapshot, dir string) *model.Repo {
	var best *model.Repo
	for i := range s.Repos {
		r := &s.Repos[i]
		if repoHolds(r, dir) && (best == nil || len(r.Path) > len(best.Path)) {
			best = r
		}
	}
	return best
}

// machine renvoie la vue de la machine nommée.
func (w *world) machine(name string) *machineView {
	for _, mv := range w.machines {
		if mv.m.Name == name {
			return mv
		}
	}
	return nil
}

// waiting renvoie tous les agents qui attendent, machine par machine.
func (w *world) waiting() []*item {
	var out []*item
	for _, mv := range w.machines {
		out = append(out, mv.waiting...)
	}
	return out
}

// search renvoie les éléments de toutes les machines qui répondent à q. Des
// chiffres seuls cherchent un port : exact d'abord, puis ceux qui commencent
// ainsi ; si aucun port ne répond, ils sont cherchés comme un mot (« 2025 »
// trouve un dépôt « 2025-stage »). Sinon chaque mot doit apparaître quelque
// part, sans souci des accents ni des majuscules ; les noms qui commencent par
// la requête passent devant. Un nom d'appli tapé en entier désigne cette
// appli : elle passe devant tout le reste, même un agent, un dépôt ou un port
// du même nom.
func (w *world) search(q string) []*item {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	digits := strings.Trim(q, "0123456789") == ""
	out := w.find(q, digits)
	if len(out) == 0 && digits {
		out = w.find(q, false)
	}
	return w.appsFirst(q, out)
}

// appsFirst met en tête les applis dont le nom est q, dans l'ordre des machines.
func (w *world) appsFirst(q string, out []*item) []*item {
	var named []*item
	seen := map[*item]bool{}
	for _, mv := range w.machines {
		for _, it := range mv.items {
			if it.kind == kindApp && fold(it.app.Name) == fold(q) {
				named = append(named, it)
				seen[it] = true
			}
		}
	}
	if len(named) == 0 {
		return out
	}
	for _, it := range out {
		if !seen[it] {
			named = append(named, it)
		}
	}
	return named
}

// find cherche q comme un port (digits) ou comme des mots.
func (w *world) find(q string, digits bool) []*item {
	type hit struct {
		it    *item
		score int
		order int
	}
	var hits []hit
	n := 0
	words := strings.Fields(fold(q))
	for _, mv := range w.machines {
		for _, it := range mv.items {
			n++
			score := -1
			if digits {
				p := itemPort(it)
				switch ps := strconv.Itoa(p); {
				case p == 0:
				case ps == q:
					score = 0
				case strings.HasPrefix(ps, q):
					score = 1
				}
			} else {
				all := true
				for _, wd := range words {
					if !strings.Contains(it.text, wd) {
						all = false
						break
					}
				}
				if all {
					score = 2
					if strings.HasPrefix(fold(it.name), words[0]) {
						score = 0
					} else if strings.Contains(fold(it.name), words[0]) {
						score = 1
					}
				}
			}
			if score >= 0 {
				hits = append(hits, hit{it, score, n})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		return hits[i].order < hits[j].order
	})
	out := make([]*item, len(hits))
	for i, h := range hits {
		out[i] = h.it
	}
	return out
}

// itemPort renvoie le port d'un élément (0 s'il n'en a pas).
func itemPort(it *item) int {
	switch {
	case it.port != nil:
		return it.port.Port
	case it.app != nil:
		return it.app.Port
	}
	return 0
}

// Package demo est le simulateur de connex (cahier des charges §8) : trois
// machines inventées mais crédibles, puis un flux d'événements tiré d'une
// simulation (agents qui travaillent, serveurs de dev qui démarrent, dépôts
// qui changent, machine qui décroche). Rien n'est rejoué : chaque patch est la
// différence entre deux états successifs du monde simulé.
package demo

import (
	"math/rand/v2"
	"net/netip"
	"path"
	"strconv"
	"time"

	"github.com/Alexry375/connex/internal/collect"
	"github.com/Alexry375/connex/internal/model"
)

// stack est la façon dont un projet sert en développement.
type stack struct {
	process string // nom de l'exécutable, comme /proc/<pid>/comm
	port    int    // port préféré
	addr    string // adresse d'écoute
	subdir  string // dossier du processus, relatif au dépôt (monorepo)
}

// project est un dépôt d'une machine simulée, avec éventuellement un serveur.
type project struct {
	name   string
	path   string
	branch string
	stack  *stack
	server string // clé du port de son serveur, "" s'il est arrêté
	app    string // clé de l'appli inscrite qui le lance, "" s'il est lancé à la main
	local  bool   // dépôt sans branche distante suivie
}

// machine est une machine simulée : son relevé courant et de quoi le faire vivre.
type machine struct {
	info      model.Machine
	home      string
	uid       int
	snap      model.Snapshot
	projects  []*project
	pid       int // compteur de pids
	reachable bool
	remote    bool // le laptop ne peut pas se perdre lui-même
	maxAgents int
	virtual   map[netip.Addr]bool // adresses des interfaces virtuelles (docker0…)
	// legacy : la machine fait tourner un connex d'avant L7, qui montre une
	// session Claude au repos en idle et ne sait rien de l'état de codex.
	legacy    bool
	incidents map[string]float64 // date simulée du dernier incident, par sorte et par appli
}

func (m *machine) newPID(r *rand.Rand) int {
	m.pid += 1 + r.IntN(900)
	return m.pid
}

// foreign est un port d'un autre compte : seuls port, adresse et uid sont connus.
type foreign struct {
	addr string
	port int
	uid  int
}

func (m *machine) addForeign(f foreign) {
	m.snap.Listeners = append(m.snap.Listeners, listener(f.addr, f.port, false, f.uid, 0, "", "", ""))
}

// listener construit un port comme le ferait collect, clé et exposition comprises.
// root est la racine de projet que collect trouverait en remontant depuis cwd.
func listener(addr string, port int, mine bool, uid, pid int, process, cwd, root string) model.Listener {
	a := mustAddr(addr)
	proto := "tcp4"
	if a.Is6() {
		proto = "tcp6"
	}
	l := model.Listener{
		Key:      proto + ":" + addrPort(a, port),
		Port:     port,
		Addr:     a.String(),
		Exposure: collect.Exposure(a),
		Mine:     mine,
		UID:      uid,
	}
	if mine {
		l.PID, l.Process, l.CWD, l.ProjectRoot = pid, process, cwd, root
		l.Exe = exeOf(process)
	}
	return l
}

// exeOf donne le nom de l'exécutable (/proc/<pid>/exe) d'un processus au nom
// court process : un serveur Next ou Node-RED est un node, un uvicorn un python.
func exeOf(process string) string {
	switch process {
	case "next-server", "node-red", "homebridge":
		return "node"
	case "uvicorn", "jupyter-lab", "python3", "MainThread":
		return "python3.12"
	}
	return process
}

var (
	next    = &stack{process: "next-server", port: 3000, addr: "::"}
	vite    = &stack{process: "node", port: 5173, addr: "::1"}
	vite4   = &stack{process: "node", port: 5173, addr: "127.0.0.1", subdir: "apps/web"}
	uvicorn = &stack{process: "uvicorn", port: 8000, addr: "127.0.0.1"}
	flask   = &stack{process: "python3", port: 5000, addr: "127.0.0.1"}
	goAPI   = &stack{process: "tango-api", port: 8080, addr: "127.0.0.1"}
	hugo    = &stack{process: "hugo", port: 1313, addr: "127.0.0.1"}
	jupyter = &stack{process: "jupyter-lab", port: 8888, addr: "127.0.0.1"}
	story   = &stack{process: "node", port: 6006, addr: "127.0.0.1"}
	bun     = &stack{process: "bun", port: 3000, addr: "::"}
)

// buildWorld construit les trois machines et leur état de départ. Les détails
// (pids, états, dépôts modifiés, serveurs lancés) varient avec le hasard ; les
// minimums du §8 sont tenus par construction.
func buildWorld(r *rand.Rand, at time.Time) []*machine {
	laptop := &machine{
		info: model.Machine{Name: "alfred-laptop", OS: "linux", Arch: "amd64", User: "alfred", TailscaleIP: "100.101.12.34"},
		home: "/home/alfred", uid: 1000, pid: 180000 + r.IntN(90000), reachable: true, maxAgents: 7,
		virtual: map[netip.Addr]bool{netip.MustParseAddr("172.17.0.1"): true},
	}
	vps := &machine{
		info: model.Machine{Name: "vps-40517391", OS: "linux", Arch: "amd64", User: "alfred", TailscaleIP: "100.71.85.119"},
		home: "/home/alfred", uid: 1001, pid: 20000 + r.IntN(40000), reachable: true, remote: true, maxAgents: 3,
	}
	pi := &machine{
		info: model.Machine{Name: "abc-desktop", OS: "linux", Arch: "arm64", User: "abc", TailscaleIP: "100.88.3.21"},
		home: "/home/abc", uid: 1000, pid: 3000 + r.IntN(20000), reachable: true, remote: true, maxAgents: 2,
		legacy: true, // abc n'a pas encore reçu le connex du lot L7
	}
	for _, m := range []*machine{laptop, vps, pi} {
		m.snap = model.NewSnapshot(m.info, at)
	}

	g := "/home/alfred/Global/"
	laptop.projects = []*project{
		{name: "site", path: g + "Projets/site", branch: "main", stack: next},
		{name: "Clepsydre", path: g + "Projets/Clepsydre", branch: "main", stack: vite},
		{name: "Tango", path: g + "Agents_Projects/Tango", branch: "dev", stack: goAPI},
		{name: "connex", path: g + "Agents_Projects/connex-lab/repo", branch: "main"},
		{name: "radio-z", path: g + "Projets/radio-z", branch: "main", stack: uvicorn},
		{name: "planetaire", path: g + "Projets/planetaire-agents", branch: "main", stack: flask},
		{name: "nuage", path: g + "Projets/nuage", branch: "main", stack: vite4},
		{name: "blog", path: g + "Perso/blog", branch: "main", stack: hugo},
		{name: "ml-notebooks", path: g + "Cours/ml-notebooks", branch: "master", stack: jupyter},
		{name: "ui-kit", path: g + "Projets/ui-kit", branch: "main", stack: story},
		{name: "landing", path: g + "Perso/landing-asso-escalade", branch: "main", stack: bun},
		{name: "dotfiles", path: g + "dotfiles", branch: "main"},
		{name: "tp-reseaux", path: g + "Cours/M2-reseaux-TP3-routage", branch: "master"},
		{name: "archive-stage", path: g + "Archives/2025-stage-M2-analyse-comparative-des-outils-de-supervision-multi-machines", branch: "main"},
	}
	vps.projects = []*project{
		{name: "site", path: "/home/alfred/apps/site", branch: "main", stack: &stack{process: "next-server", port: 3000, addr: "0.0.0.0"}},
		{name: "tango", path: "/home/alfred/apps/tango", branch: "main", stack: goAPI},
		{name: "n8n-flows", path: "/home/alfred/n8n-flows", branch: "main", stack: &stack{process: "node", port: 5678, addr: "127.0.0.1"}},
		{name: "atelier", path: "/home/alfred/ateliers-isoles/cartes-mentales/atelier", branch: "main", stack: &stack{process: "python3", port: 8765, addr: "100.71.85.119"}},
		{name: "clepsydre", path: "/home/alfred/apps/clepsydre", branch: "staging", stack: &stack{process: "node", port: 4173, addr: "127.0.0.1"}},
		{name: "backups", path: "/home/alfred/scripts/backups", branch: "main"},
	}
	pi.projects = []*project{
		{name: "domotique", path: "/home/abc/domotique", branch: "main", stack: &stack{process: "node-red", port: 1880, addr: "0.0.0.0"}},
		{name: "homebridge", path: "/home/abc/homebridge-config", branch: "main", stack: &stack{process: "homebridge", port: 8581, addr: "0.0.0.0"}},
		{name: "cadre-photo", path: "/home/abc/projets/cadre-photo", branch: "main", stack: flask},
		{name: "scripts", path: "/home/abc/scripts", branch: "main"},
	}

	for _, m := range []*machine{laptop, vps, pi} {
		for _, p := range m.projects {
			dirty := r.IntN(3) == 0
			repo := model.Repo{
				Key: p.path, Path: p.path, Branch: p.branch, Dirty: &dirty, Worktrees: []model.Worktree{},
			}
			// La plupart suivent une branche distante ; quelques-uns sont
			// locaux (pas de upstream, ahead et behind inconnus).
			p.local = r.IntN(5) == 0
			if !p.local {
				repo.Upstream = "origin/" + p.branch
				repo.Ahead, repo.Behind = intp(pickInt(r, []int{0, 0, 0, 1, 3})), intp(pickInt(r, []int{0, 0, 0, 0, 2}))
			}
			m.snap.Repos = append(m.snap.Repos, repo)
		}
	}
	// À pousser, en retard, et une branche locale sans suivi, à coup sûr.
	setSync(laptop, laptop.projects[0].path, "origin/main", 2, 0)
	setSync(laptop, laptop.projects[2].path, "origin/dev", 1, 4)
	setSync(laptop, laptop.projects[12].path, "", 0, 0)
	laptop.projects[0].local, laptop.projects[2].local, laptop.projects[12].local = false, false, true
	// Propres et modifiés, à coup sûr.
	setDirty(laptop, laptop.projects[3].path, true)
	setDirty(laptop, laptop.projects[11].path, false)
	// Des worktrees.
	addWorktree(laptop, laptop.projects[3].path, model.Worktree{Path: g + "Agents_Projects/connex-lab/workspaces/l1-moteur-local/repo", Branch: "feat/l1-moteur-local"})
	addWorktree(laptop, laptop.projects[2].path, model.Worktree{Path: g + "Agents_Projects/Tango-" + slug(topic(r)), Branch: topic(r)})
	addWorktree(vps, vps.projects[0].path, model.Worktree{Path: "/home/alfred/apps/site-hotfix", Branch: "hotfix/" + pick(r, nouns)})

	// Serveurs de dev : la plupart tournent au départ.
	for _, m := range []*machine{laptop, vps, pi} {
		for _, p := range m.projects {
			if p.stack != nil && (r.IntN(5) != 0 || m != laptop) {
				startServer(m, p, r)
			}
		}
	}
	// Deux applis qui veulent le même port : nuage (127.0.0.1:5173) à côté de
	// Clepsydre ([::1]:5173) ; Node résout localhost vers ::1, l'autre vers IPv4.
	for _, i := range []int{1, 6} {
		if laptop.projects[i].server == "" {
			startServer(laptop, laptop.projects[i], r)
		}
	}
	// Port de l'utilisateur ouvert à tous sur le VPS : site en production.
	if vps.projects[0].server == "" {
		startServer(vps, vps.projects[0], r)
	}
	// Syncthing sur le Pi, lancé par abc hors de tout dépôt : ~ n'a aucun
	// marqueur de projet, donc pas de racine de projet.
	syncPID := pi.newPID(r)
	pi.snap.Listeners = append(pi.snap.Listeners,
		listener("127.0.0.1", 8384, true, pi.uid, syncPID, "syncthing", pi.home, ""),
		listener("::", 22000, true, pi.uid, syncPID, "syncthing", pi.home, ""))

	// Un relais que l'utilisateur lance pour ses conteneurs : il n'écoute que sur le
	// pont Docker (docker0 porte 172.17.0.1), donc host.
	relay := listener("172.17.0.1", 4000, true, laptop.uid, laptop.newPID(r), "litellm", laptop.home, "")
	relay.Exposure = collect.ExposureOn(mustAddr(relay.Addr), laptop.virtual)
	laptop.snap.Listeners = append(laptop.snap.Listeners, relay)
	// Sur le VPS, le bot notifbot tourne dans son unité systemd : son fil
	// principal s'appelle MainThread, l'unité dit ce que c'est.
	bot := listener("127.0.0.1", 7070, true, vps.uid, vps.newPID(r), "MainThread", "/home/alfred/notifbot", "")
	bot.Unit = "notifbot.service"
	vps.snap.Listeners = append(vps.snap.Listeners, bot)

	// Ports des autres comptes.
	for _, f := range []foreign{
		{"127.0.0.53", 53, 990}, {"127.0.0.54", 53, 990}, {"127.0.0.1", 631, 0}, {"::1", 631, 0},
		{"127.0.0.1", 11434, 982}, {"100.101.12.34", 43127, 0}, {"fd7a:115c:a1e0::5a01:c22", 43127, 0},
		{"0.0.0.0", 5432, 0},
	} {
		laptop.addForeign(f)
	}
	// Tailscale Funnel publie le port 443 du laptop sur Internet : tailscaled
	// (root) y écoute à l'adresse Tailscale.
	laptop.addForeign(foreign{"100.101.12.34", 443, 0})
	collect.MarkFunnel(laptop.snap.Listeners, map[int]bool{443: true})
	for _, f := range []foreign{
		{"0.0.0.0", 22, 0}, {"::", 22, 0}, {"0.0.0.0", 80, 0}, {"0.0.0.0", 443, 0},
		{"127.0.0.1", 5432, 113}, {"127.0.0.1", 6379, 114}, {"100.71.85.119", 38211, 0},
		{"0.0.0.0", 8000, 1003},
	} {
		vps.addForeign(f)
	}
	for _, f := range []foreign{
		{"0.0.0.0", 22, 0}, {"0.0.0.0", 53, 999}, {"0.0.0.0", 80, 33}, {"0.0.0.0", 1883, 108},
	} {
		pi.addForeign(f)
	}

	// Agents : les quatre états sont là au départ. codex aussi dit s'il
	// travaille, par l'heure de son terminal ; une session claude sans terminal
	// (claude -p) reste d'état inconnu. idle ne vient plus que du Pi, dont le
	// connex est plus ancien.
	states := []string{model.StateWorking, model.StateWaiting, model.StateWaiting}
	r.Shuffle(len(states), func(i, j int) { states[i], states[j] = states[j], states[i] })
	for i, idx := range []int{3, 2, 0} {
		addAgent(laptop, "claude", laptop.projects[idx].path, states[i], r)
	}
	addAgent(laptop, "codex", laptop.projects[2].path, pick(r, []string{model.StateWorking, model.StateWaiting}), r)
	addAgent(laptop, "claude", laptop.projects[4].path, model.StateUnknown, r)
	addAgent(vps, "claude", vps.projects[0].path, pick(r, []string{model.StateWorking, model.StateWaiting}), r)
	addAgent(pi, "claude", pi.projects[2].path, model.StateIdle, r)
	if r.IntN(2) == 0 {
		addAgent(pi, "codex", pi.projects[0].path, model.StateUnknown, r)
	}
	registerApps(r, laptop, vps, pi)
	for _, m := range []*machine{laptop, vps, pi} {
		sortSnap(&m.snap)
	}
	return []*machine{laptop, vps, pi}
}

func intp(n int) *int { return &n }

// restState est l'état d'une session Claude qui a fini : waiting, ou idle
// pour un connex d'avant L7.
func (m *machine) restState() string {
	if m.legacy {
		return model.StateIdle
	}
	return model.StateWaiting
}

// setSync pose la branche suivie d'un dépôt et ses commits à pousser et en
// retard ; sans branche suivie, ahead et behind sont inconnus.
func setSync(m *machine, repo, upstream string, ahead, behind int) {
	r := m.repo(repo)
	if r == nil {
		return
	}
	r.Upstream, r.Ahead, r.Behind = upstream, nil, nil
	if upstream != "" {
		r.Ahead, r.Behind = intp(ahead), intp(behind)
	}
}

func setDirty(m *machine, repo string, dirty bool) {
	if r := m.repo(repo); r != nil {
		r.Dirty = &dirty
	}
}

func addWorktree(m *machine, repo string, wt model.Worktree) {
	if r := m.repo(repo); r != nil {
		r.Worktrees = append(append([]model.Worktree{}, r.Worktrees...), wt)
	}
}

func (m *machine) repo(p string) *model.Repo {
	for i := range m.snap.Repos {
		if m.snap.Repos[i].Path == p {
			return &m.snap.Repos[i]
		}
	}
	return nil
}

// startServer lance le serveur de dev d'un projet. Si son port préféré est pris
// à la même adresse, il prend le suivant, comme next dev ou vite.
func startServer(m *machine, p *project, r *rand.Rand) {
	port := p.stack.port
	a := mustAddr(p.stack.addr)
	for taken(m, a, port) {
		port++
	}
	// Dans un monorepo, le serveur tourne dans une appli qui a son propre
	// package.json : c'est elle, la racine de projet.
	cwd := p.path
	if p.stack.subdir != "" {
		cwd = path.Join(p.path, p.stack.subdir)
	}
	l := listener(p.stack.addr, port, true, m.uid, m.newPID(r), p.stack.process, cwd, cwd)
	m.snap.Listeners = append(m.snap.Listeners, l)
	p.server = l.Key
}

// taken dit si un socket écoute déjà sur ce port à une adresse en conflit.
func taken(m *machine, a netip.Addr, port int) bool {
	for _, l := range m.snap.Listeners {
		if l.Port != port {
			continue
		}
		la := mustAddr(l.Addr)
		if la == a || la.IsUnspecified() || a.IsUnspecified() {
			return true
		}
	}
	return false
}

func addAgent(m *machine, tool, cwd, state string, r *rand.Rand) string {
	pid := m.newPID(r)
	key := tool + ":" + strconv.Itoa(pid)
	m.snap.Agents = append(m.snap.Agents, model.Agent{Key: key, Tool: tool, PID: pid, CWD: cwd, State: state})
	return key
}

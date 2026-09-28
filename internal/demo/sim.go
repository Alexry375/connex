package demo

import (
	"container/heap"
	"math"
	"math/rand/v2"
	"net/netip"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// sim fait vivre le monde : une file d'événements datés en secondes simulées.
// Chaque événement modifie l'état d'une machine ; la différence avec l'état
// d'avant devient un patch, si la machine est joignable.
type sim struct {
	r        *rand.Rand
	t0       time.Time
	now      float64 // secondes simulées depuis t0
	machines []*machine
	queue    eventQueue
	seq      int
	out      []any // lignes produites par l'événement en cours
}

type event struct {
	at  float64
	seq int
	run func()
}

type eventQueue []event

func (q eventQueue) Len() int { return len(q) }
func (q eventQueue) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	return q[i].seq < q[j].seq
}
func (q eventQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *eventQueue) Push(x any)   { *q = append(*q, x.(event)) }
func (q *eventQueue) Pop() any {
	old := *q
	e := old[len(old)-1]
	*q = old[:len(old)-1]
	return e
}

func newSim(r *rand.Rand, t0 time.Time) *sim {
	s := &sim{r: r, t0: t0}
	s.machines = buildWorld(r, t0)
	for _, m := range s.machines {
		for _, a := range m.snap.Agents {
			s.scheduleAgent(m, a.Key)
		}
		for _, p := range m.projects {
			if p.server != "" && p.app == "" {
				s.scheduleServer(m, p, p.server)
			}
		}
		s.scheduleApps(m)
		s.after(s.exp(serverEvery(m)), func() { s.serverStart(m) })
		s.after(s.exp(spawnEvery(m)), func() { s.spawnAgent(m) })
	}
	laptop, vps := s.machines[0], s.machines[1]
	s.after(s.exp(120), func() { s.humanEdit(laptop) })
	s.after(s.exp(240), func() { s.foreignChurn(vps) })
	// Première coupure tôt, pour qu'on la voie en construisant l'affichage (§8 :
	// une machine devient injoignable puis revient) ; les suivantes sont rares.
	s.after(s.uniform(40, 150), s.outage)
	return s
}

// snapshots renvoie le relevé courant de chaque machine.
func (s *sim) snapshots() []model.Snapshot {
	out := make([]model.Snapshot, 0, len(s.machines))
	for _, m := range s.machines {
		snap := cloneSnap(m.snap)
		snap.TakenAt = model.Timestamp(s.clock())
		out = append(out, snap)
	}
	return out
}

// peek renvoie la date simulée du prochain événement.
func (s *sim) peek() float64 {
	if len(s.queue) == 0 {
		return math.Inf(1)
	}
	return s.queue[0].at
}

// step joue le prochain événement et renvoie les lignes qu'il produit.
func (s *sim) step() []any {
	e := heap.Pop(&s.queue).(event)
	s.now = math.Max(s.now, e.at) // une action demandée a pu avancer l'horloge
	s.out = nil
	e.run()
	return s.out
}

func (s *sim) clock() time.Time {
	return s.t0.Add(time.Duration(s.now * float64(time.Second)))
}

func (s *sim) after(d float64, run func()) {
	s.seq++
	heap.Push(&s.queue, event{at: s.now + d, seq: s.seq, run: run})
}

// exp tire une durée de loi exponentielle de moyenne mean, bornée pour éviter
// les rafales et les silences absurdes.
func (s *sim) exp(mean float64) float64 {
	return math.Min(math.Max(s.r.ExpFloat64()*mean, mean/10), mean*4)
}

func (s *sim) uniform(lo, hi float64) float64 { return lo + s.r.Float64()*(hi-lo) }

// change applique fn à la machine et publie la différence.
func (s *sim) change(m *machine, fn func()) {
	before := cloneSnap(m.snap)
	fn()
	sortSnap(&m.snap)
	if !m.reachable {
		return // la machine vit, mais personne ne la voit
	}
	if changes := model.Diff(before, m.snap); len(changes) > 0 {
		s.out = append(s.out, model.NewPatch(m.info.Name, s.clock(), changes))
	}
}

func serverEvery(m *machine) float64 {
	switch {
	case !m.remote:
		return 70
	case m.maxAgents > 2:
		return 220
	default:
		return 420
	}
}

func spawnEvery(m *machine) float64 {
	if !m.remote {
		return 150
	}
	return 420
}

// --- agents

func (s *sim) scheduleAgent(m *machine, key string) {
	var d float64
	switch a := m.agent(key); {
	case a == nil:
		return
	case a.State == model.StateWorking:
		d = s.uniform(40, 200)
	case a.State == model.StateWaiting, a.State == model.StateIdle:
		d = s.uniform(15, 240)
	default:
		d = s.uniform(90, 500)
	}
	s.after(d, func() { s.agentStep(m, key) })
}

// agentStep fait avancer une session : elle travaille, puis t'attend (tâche
// finie, souvent commitée, ou question), repart ou s'arrête. En travaillant,
// elle modifie son dépôt. codex et claude suivent le même cycle : depuis L7,
// l'état de codex vient de l'heure de son terminal. Une session sans terminal
// (unknown) ne dit que son travail et sa fin. Sur une machine au connex d'avant
// L7, une session qui a fini est idle.
func (s *sim) agentStep(m *machine, key string) {
	a := m.agent(key)
	if a == nil {
		return
	}
	repo := m.repoOf(a.CWD)
	gone := false
	s.change(m, func() {
		a := m.agent(key)
		p := s.r.Float64()
		switch {
		case a.State == model.StateUnknown:
			switch {
			case p < 0.25:
				gone = true
			case p < 0.6 && repo != nil:
				s.touchRepo(m, repo.Path)
			}
		case a.State == model.StateWorking:
			switch {
			case p < 0.6:
				a.State = m.restState()
				if p >= 0.35 && repo != nil && s.r.IntN(5) < 3 {
					s.commit(m, repo.Path) // tâche finie, commitée
				}
			case repo != nil && s.r.IntN(6) == 0:
				s.newWorktree(m, repo.Path, a)
			case repo != nil:
				s.touchRepo(m, repo.Path)
			}
		case a.State == model.StateWaiting, a.State == model.StateIdle:
			switch {
			case p < 0.6:
				a.State = model.StateWorking
			case p < 0.8:
				gone = true
			}
		}
		if gone {
			m.removeAgent(key)
			if repo != nil && s.r.IntN(3) == 0 {
				s.dropWorktree(m, repo.Path)
			}
		}
	})
	if !gone {
		s.scheduleAgent(m, key)
	}
}

// spawnAgent ouvre une nouvelle session dans un projet de la machine.
func (s *sim) spawnAgent(m *machine) {
	defer s.after(s.exp(spawnEvery(m)), func() { s.spawnAgent(m) })
	if len(m.snap.Agents) >= m.maxAgents {
		return
	}
	p := m.projects[s.r.IntN(len(m.projects))]
	cwd := p.path
	if repo := m.repo(p.path); repo != nil && len(repo.Worktrees) > 0 && s.r.IntN(2) == 0 {
		cwd = repo.Worktrees[s.r.IntN(len(repo.Worktrees))].Path
	}
	tool, state := "claude", model.StateWorking
	if s.r.IntN(4) == 0 {
		tool = "codex"
		if m.legacy {
			state = model.StateUnknown // un connex d'avant L7 ne sait rien de codex
		}
	}
	var key string
	s.change(m, func() { key = addAgent(m, tool, cwd, state, s.r) })
	s.scheduleAgent(m, key)
}

// --- dépôts

// touchRepo : quelqu'un modifie des fichiers suivis.
func (s *sim) touchRepo(m *machine, repo string) {
	setDirty(m, repo, true)
}

// commit : les changements du dépôt sont commités ; s'il suit une branche
// distante, il a un commit de plus à pousser.
func (s *sim) commit(m *machine, repo string) {
	setDirty(m, repo, false)
	if r := m.repo(repo); r != nil && r.Ahead != nil {
		r.Ahead = intp(*r.Ahead + 1)
	}
}

// push : l'utilisateur pousse ; plus rien à pousser. Le retard ne bouge pas : il
// ne change qu'au prochain fetch, que connex ne fait jamais.
func (s *sim) push(m *machine, repo string) {
	if r := m.repo(repo); r != nil && r.Ahead != nil {
		r.Ahead = intp(0)
	}
}

// newWorktree : l'agent ouvre une branche dans un worktree à côté du dépôt.
func (s *sim) newWorktree(m *machine, repo string, a *model.Agent) {
	r := m.repo(repo)
	if r == nil || len(r.Worktrees) >= 3 {
		s.touchRepo(m, repo)
		return
	}
	branch := topic(s.r)
	wt := model.Worktree{Path: r.Path + "-" + slug(branch), Branch: branch}
	for _, w := range r.Worktrees {
		if w.Path == wt.Path {
			return
		}
	}
	addWorktree(m, repo, wt)
	a.CWD = wt.Path
}

func (s *sim) dropWorktree(m *machine, repo string) {
	r := m.repo(repo)
	if r == nil || len(r.Worktrees) == 0 {
		return
	}
	i := s.r.IntN(len(r.Worktrees))
	for _, a := range m.snap.Agents {
		if a.CWD == r.Worktrees[i].Path {
			return // encore utilisé
		}
	}
	r.Worktrees = append(append([]model.Worktree{}, r.Worktrees[:i]...), r.Worktrees[i+1:]...)
}

// humanEdit : l'utilisateur modifie, commite, pousse ou change de branche sur le laptop.
func (s *sim) humanEdit(m *machine) {
	defer s.after(s.exp(120), func() { s.humanEdit(m) })
	p := m.projects[s.r.IntN(len(m.projects))]
	s.change(m, func() {
		r := m.repo(p.path)
		switch x := s.r.IntN(10); {
		case x < 5:
			setDirty(m, p.path, true)
		case x < 7:
			s.commit(m, p.path)
		case x < 8:
			s.push(m, p.path)
		default:
			if *r.Dirty {
				setDirty(m, p.path, true)
				return
			}
			// Une branche neuve n'a pas de branche distante suivie ; revenir
			// sur la principale la retrouve, à jour du dernier fetch.
			if r.Branch == p.branch {
				r.Branch = topic(s.r)
				setSync(m, p.path, "", 0, 0)
			} else {
				r.Branch = p.branch
				if !p.local {
					setSync(m, p.path, "origin/"+p.branch, 0, 0)
				}
			}
		}
	})
}

// --- serveurs

// scheduleServer prévoit l'arrêt d'un serveur : quelques minutes pour un serveur
// de dev du laptop, des heures pour un service du VPS ou du Pi.
func (s *sim) scheduleServer(m *machine, p *project, key string) {
	life := 420.0
	if m.remote {
		life = 3 * 3600
	}
	s.after(s.exp(life), func() { s.serverStop(m, p, key) })
	if s.r.IntN(5) < 2 {
		s.after(s.exp(150), func() { s.serverRestart(m, key) })
	}
}

// serverStart lance le serveur de dev d'un projet arrêté.
func (s *sim) serverStart(m *machine) {
	defer s.after(s.exp(serverEvery(m)), func() { s.serverStart(m) })
	var idle []*project
	for _, p := range m.projects {
		if p.stack != nil && p.server == "" && p.app == "" {
			idle = append(idle, p)
		}
	}
	if len(idle) == 0 {
		return
	}
	p := idle[s.r.IntN(len(idle))]
	s.change(m, func() { startServer(m, p, s.r) })
	s.scheduleServer(m, p, p.server)
}

func (s *sim) serverStop(m *machine, p *project, key string) {
	if p.server != key {
		return
	}
	s.change(m, func() {
		m.removeListener(key)
		p.server = ""
	})
}

// serverRestart : le serveur redémarre (watcher, crash) sur le même port.
func (s *sim) serverRestart(m *machine, key string) {
	s.change(m, func() {
		for i := range m.snap.Listeners {
			if m.snap.Listeners[i].Key == key {
				m.snap.Listeners[i].PID = m.newPID(s.r)
			}
		}
	})
}

// foreignChurn : un autre compte du VPS lance ou arrête un serveur ; on ne voit
// que son port, son adresse et son uid.
func (s *sim) foreignChurn(m *machine) {
	defer s.after(s.exp(240), func() { s.foreignChurn(m) })
	s.change(m, func() {
		for _, l := range m.snap.Listeners {
			if !l.Mine && l.UID == 1003 && s.r.IntN(2) == 0 {
				m.removeListener(l.Key)
				return
			}
		}
		addr := pick(s.r, []string{"0.0.0.0", "127.0.0.1", "::"})
		port := 8000 + s.r.IntN(100)
		if !taken(m, mustAddr(addr), port) {
			m.addForeign(foreign{addr: addr, port: port, uid: 1003})
		}
	})
}

// --- coupures

// outage : une machine distante décroche (Wi-Fi du Pi, redémarrage du VPS),
// puis revient. Au retour, le laptop relance stream : un relevé neuf arrive.
func (s *sim) outage() {
	var remotes []*machine
	for _, m := range s.machines {
		if m.remote && m.reachable {
			remotes = append(remotes, m)
		}
	}
	defer s.after(s.uniform(900, 2400), s.outage)
	if len(remotes) == 0 {
		return
	}
	m := remotes[s.r.IntN(len(remotes))]
	m.reachable = false
	// La raison ne tire pas au sort : les séquences des autres événements restent
	// celles d'avant.
	reason := strings.ReplaceAll(outageReasons[int(s.now)%len(outageReasons)], "HOST", m.info.TailscaleIP)
	s.out = append(s.out, model.MachineStatus{Schema: model.Schema, Type: "machine_status", Machine: m.info.Name, Reachable: false, Since: model.Timestamp(s.clock()), Reason: reason})
	s.after(s.uniform(35, 140), func() {
		m.reachable = true
		at := model.Timestamp(s.clock())
		snap := cloneSnap(m.snap)
		snap.TakenAt = at
		s.out = append(s.out, model.MachineStatus{Schema: model.Schema, Type: "machine_status", Machine: m.info.Name, Reachable: true, Since: at}, snap)
	})
}

// outageReasons : ce que ssh dit quand une session tombe.
var outageReasons = []string{
	"Connection timed out during banner exchange",
	"client_loop: send disconnect: Broken pipe",
	"ssh: connect to host HOST port 22: Connection timed out",
	"Connection closed by remote host",
}

// --- outils

func (m *machine) agent(key string) *model.Agent {
	for i := range m.snap.Agents {
		if m.snap.Agents[i].Key == key {
			return &m.snap.Agents[i]
		}
	}
	return nil
}

func (m *machine) removeAgent(key string) {
	for i, a := range m.snap.Agents {
		if a.Key == key {
			m.snap.Agents = append(m.snap.Agents[:i:i], m.snap.Agents[i+1:]...)
			return
		}
	}
}

func (m *machine) removeListener(key string) {
	for i, l := range m.snap.Listeners {
		if l.Key == key {
			m.snap.Listeners = append(m.snap.Listeners[:i:i], m.snap.Listeners[i+1:]...)
			return
		}
	}
}

// repoOf renvoie le dépôt qui contient dir, worktrees compris.
func (m *machine) repoOf(dir string) *model.Repo {
	for i := range m.snap.Repos {
		r := &m.snap.Repos[i]
		if dir == r.Path || strings.HasPrefix(dir, r.Path+"/") {
			return r
		}
		for _, w := range r.Worktrees {
			if dir == w.Path || strings.HasPrefix(dir, w.Path+"/") {
				return r
			}
		}
	}
	return nil
}

// cloneSnap copie un relevé en profondeur.
func cloneSnap(s model.Snapshot) model.Snapshot {
	c := s
	c.Listeners = append([]model.Listener{}, s.Listeners...)
	c.Agents = append([]model.Agent{}, s.Agents...)
	c.Apps = append([]model.App{}, s.Apps...)
	c.Errors = append([]model.Error{}, s.Errors...)
	c.Repos = make([]model.Repo, len(s.Repos))
	for i, r := range s.Repos {
		r.Worktrees = append([]model.Worktree{}, r.Worktrees...)
		if r.Dirty != nil {
			d := *r.Dirty
			r.Dirty = &d
		}
		if r.Ahead != nil {
			r.Ahead = intp(*r.Ahead)
		}
		if r.Behind != nil {
			r.Behind = intp(*r.Behind)
		}
		c.Repos[i] = r
	}
	return c
}

func sortSnap(s *model.Snapshot) {
	sort.Slice(s.Listeners, func(i, j int) bool {
		if s.Listeners[i].Port != s.Listeners[j].Port {
			return s.Listeners[i].Port < s.Listeners[j].Port
		}
		return s.Listeners[i].Key < s.Listeners[j].Key
	})
	sort.Slice(s.Repos, func(i, j int) bool { return s.Repos[i].Path < s.Repos[j].Path })
	sort.Slice(s.Agents, func(i, j int) bool { return s.Agents[i].PID < s.Agents[j].PID })
	sort.Slice(s.Apps, func(i, j int) bool { return s.Apps[i].Key < s.Apps[j].Key })
}

func mustAddr(s string) netip.Addr {
	return netip.MustParseAddr(s)
}

func addrPort(a netip.Addr, port int) string {
	return netip.AddrPortFrom(a, uint16(port)).String()
}

var (
	prefixes = []string{"feat", "fix", "refactor", "exp", "chore"}
	nouns    = []string{
		"export-csv", "auth-oidc", "sidebar", "rate-limit", "cache-redis", "i18n", "webhooks",
		"onboarding", "dark-mode", "search", "pagination", "upload-s3", "billing", "notifications",
		"audit-log", "charts", "offline", "sync", "retry", "parser", "planning", "conges", "badges",
		"import-ics", "carte", "tuiles", "streaming", "healthcheck", "migrations", "tests-e2e",
	}
)

// topic invente un nom de branche : 5 préfixes × 30 sujets.
func topic(r *rand.Rand) string {
	return pick(r, prefixes) + "/" + pick(r, nouns)
}

func slug(branch string) string {
	return path.Base(branch)
}

func pick[T any](r *rand.Rand, xs []T) T {
	return xs[r.IntN(len(xs))]
}

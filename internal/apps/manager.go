package apps

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Alexry375/connex/internal/collect"
	"github.com/Alexry375/connex/internal/config"
	"github.com/Alexry375/connex/internal/model"
)

// Réglages fixés par le contrat L3.
const (
	probeEvery   = 10 * time.Second // au plus une sonde HTTP par appli
	probeTimeout = 2 * time.Second
	// probeWaitLive borne l'attente d'une sonde dans un relevé du flux : le
	// patch qui suit une action ne doit pas attendre une appli qui ne répond pas.
	probeWaitLive = 300 * time.Millisecond
	termWait      = 5 * time.Second // free_port : SIGTERM, puis SIGKILL après ce délai
	portBase      = 3100            // ports choisis : 3100 + FNV-32a(nom) mod 900
	portSpan      = 900
)

// agentTools sont les processus que free_port ne touche jamais.
var agentTools = map[string]bool{"claude": true, "codex": true}

// Manager agit sur les applis de cette machine. C'est le même code pour les
// commandes locales (`connex stop x`) et pour les requêtes du flux.
type Manager struct {
	Registry Registry
	Machine  string // nom annoncé par la machine, pour les messages (lire par machineName)
	ProcRoot string
	Run      Runner
	// Listeners relit les ports en écoute ; collect.Listeners par défaut.
	Listeners func() []model.Listener
	Now       func() time.Time
	// Probe fait la sonde HTTP ; httpProbe par défaut.
	Probe    func(ctx context.Context, addr string, port int) int
	TermWait time.Duration

	mu       sync.Mutex
	locks    map[string]*sync.Mutex
	probes   map[string]probeResult
	inflight map[string]chan struct{} // sondes en cours, par appli
}

type probeResult struct {
	at   time.Time
	addr string
	code int
}

// NewManager prépare un Manager réel pour la machine nommée machine.
func NewManager(reg Registry, machine string) *Manager {
	m := &Manager{Registry: reg, Machine: machine, ProcRoot: "/proc", Run: ExecRunner, Now: time.Now,
		Probe: httpProbe, TermWait: termWait}
	m.Listeners = func() []model.Listener {
		l, _ := collect.Listeners(m.ProcRoot)
		return l
	}
	return m
}

// lock sérialise les actions sur une même appli.
func (m *Manager) lock(name string) func() {
	m.mu.Lock()
	if m.locks == nil {
		m.locks = map[string]*sync.Mutex{}
	}
	l, ok := m.locks[name]
	if !ok {
		l = &sync.Mutex{}
		m.locks[name] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// ---- Requêtes du flux ----

// Validate contrôle la cible d'une opération d'appli (§4.3), avant toute autre
// chose. Il renvoie un refus, vide si la requête est recevable. Les opérations
// hors de la liste fermée sont refusées.
func (m *Manager) Validate(op, target string) string {
	switch op {
	case model.ReqStart, model.ReqStop, model.ReqRestart:
		if !config.ValidName(target) {
			return fmt.Sprintf("nom d'appli invalide : %q", target)
		}
		return ""
	case model.ReqFreePort:
		if _, err := ParsePort(target); err != nil {
			return err.Error()
		}
		return ""
	}
	return "opération inconnue ; permises : start, stop, restart, free_port, refresh"
}

// Key renvoie la clé qui sérialise les requêtes : deux actions de même clé
// s'enchaînent sans se chevaucher.
func (m *Manager) Key(op, target string) string {
	if op == model.ReqFreePort {
		return "port:" + target
	}
	return "app:" + target
}

// Do exécute une opération déjà validée.
func (m *Manager) Do(ctx context.Context, op, target string) error {
	if refusal := m.Validate(op, target); refusal != "" {
		return errors.New(refusal)
	}
	switch op {
	case model.ReqStart:
		return m.Start(ctx, target)
	case model.ReqStop:
		return m.Stop(ctx, target)
	case model.ReqRestart:
		return m.Restart(ctx, target)
	case model.ReqFreePort:
		p, _ := ParsePort(target)
		return m.FreePort(ctx, p)
	}
	return errors.New("opération inconnue")
}

// ParsePort lit un numéro de port écrit en chiffres, de 1 à 65535.
func ParsePort(s string) (int, error) {
	if s == "" || len(s) > 5 || strings.Trim(s, "0123456789") != "" {
		return 0, fmt.Errorf("port invalide : %q", s)
	}
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("port invalide : %q", s)
	}
	return p, nil
}

// ---- Actions ----

func (m *Manager) find(name string) (Entry, error) {
	e, ok, err := m.Registry.Find(name)
	if err != nil {
		return Entry{}, err
	}
	if !ok {
		return Entry{}, fmt.Errorf("aucune appli « %s » sur %s", name, m.machineName())
	}
	return e, nil
}

// status lit l'état d'une seule unité.
func (m *Manager) status(ctx context.Context, unit string) (unitStatus, error) {
	out, stderr, err := m.Run(ctx, "systemctl", showArgs([]string{unit})...)
	if err != nil {
		return unitStatus{}, fmt.Errorf("systemctl show : %s", errText(stderr, err))
	}
	return parseShow(out)[unit], nil
}

// Start démarre une appli arrêtée.
func (m *Manager) Start(ctx context.Context, name string) error {
	defer m.lock(name)()
	e, err := m.find(name)
	if err != nil {
		return err
	}
	st, err := m.status(ctx, e.Unit())
	if err != nil {
		return err
	}
	if st.running() {
		return fmt.Errorf("« %s » tourne déjà", name)
	}
	return m.launch(ctx, e)
}

// Stop arrête une appli qui tourne.
func (m *Manager) Stop(ctx context.Context, name string) error {
	defer m.lock(name)()
	e, err := m.find(name)
	if err != nil {
		return err
	}
	st, err := m.status(ctx, e.Unit())
	if err != nil {
		return err
	}
	if !st.running() {
		return fmt.Errorf("« %s » est déjà arrêtée", name)
	}
	return m.stopUnit(ctx, e.Unit())
}

// Restart relance une unité qui tourne, et démarre l'appli sinon.
func (m *Manager) Restart(ctx context.Context, name string) error {
	defer m.lock(name)()
	e, err := m.find(name)
	if err != nil {
		return err
	}
	st, err := m.status(ctx, e.Unit())
	if err != nil {
		return err
	}
	if !st.running() {
		return m.launch(ctx, e)
	}
	if _, stderr, err := m.Run(ctx, "systemctl", "--user", "restart", e.Unit()); err != nil {
		return fmt.Errorf("relance de « %s » : %s", name, scrub(errText(stderr, err), e))
	}
	return nil
}

func (m *Manager) stopUnit(ctx context.Context, unit string) error {
	if _, stderr, err := m.Run(ctx, "systemctl", "--user", "stop", unit); err != nil {
		return fmt.Errorf("arrêt de %s : %s", unit, errText(stderr, err))
	}
	return nil
}

// launch lance l'unité de l'appli, après avoir vérifié que son port est libre.
func (m *Manager) launch(ctx context.Context, e Entry) error {
	if who := m.portHolder(e.Port, e.Unit()); who != "" {
		return fmt.Errorf("le port %d est déjà pris par %s", e.Port, who)
	}
	_, _, _ = m.Run(ctx, "systemctl", "--user", "reset-failed", e.Unit()) // échoue si l'unité n'existe pas : sans importance
	if _, stderr, err := m.Run(ctx, "systemd-run", startArgs(e)...); err != nil {
		return fmt.Errorf("systemd-run n'a pas pu lancer « %s » : %s", e.Name, scrub(errText(stderr, err), e))
	}
	return nil
}

// portHolder décrit qui tient le port, hors de l'unité donnée ; vide si personne.
func (m *Manager) portHolder(port int, unit string) string {
	for _, l := range m.Listeners() {
		if l.Port != port {
			continue
		}
		switch {
		case !l.Mine:
			return fmt.Sprintf("un autre compte (uid %d)", l.UID)
		case l.PID == 0:
			return "un processus que connex ne peut pas identifier"
		case inUnit(m.ProcRoot, l.PID, unit):
			continue
		default:
			return fmt.Sprintf("%s (pid %d)", l.Process, l.PID)
		}
	}
	return ""
}

// scrub retire d'un message d'erreur tout ce qui ressemble à la commande de
// l'appli ou à son PATH (D11) : le message devient générique.
func scrub(msg string, e Entry) string {
	leak := e.Path != "" && strings.Contains(msg, e.Path)
	for _, a := range e.Command {
		if len(a) >= 3 && strings.Contains(msg, a) {
			leak = true
		}
	}
	if leak {
		return "échec (détail masqué : il citait la commande de l'appli)"
	}
	return msg
}

// holder est un processus qui tient le port à libérer.
type holder struct {
	pid   int
	start uint64
	app   string // appli connex dont l'unité contient le processus
}

// FreePort libère un port tenu par un processus de l'utilisateur.
func (m *Manager) FreePort(ctx context.Context, port int) error {
	defer m.lock("port:" + strconv.Itoa(port))()
	var hs []holder
	seen := map[int]bool{}
	for _, l := range m.Listeners() {
		if l.Port != port {
			continue
		}
		if !l.Mine {
			return fmt.Errorf("le port %d est tenu par un autre compte (uid %d) : connex ne peut pas le libérer", port, l.UID)
		}
		if l.PID == 0 {
			return fmt.Errorf("le port %d est tenu par un processus que connex ne peut pas identifier", port)
		}
		if seen[l.PID] {
			continue
		}
		seen[l.PID] = true
		// L'heure de démarrage est lue avant les contrôles et relue après : un
		// pid réutilisé entre-temps n'est pas pris pour le processus contrôlé.
		st, err := startTime(m.ProcRoot, l.PID)
		if err != nil {
			continue // disparu entre-temps
		}
		uids, err := procUIDs(m.ProcRoot, l.PID)
		if err != nil {
			continue // disparu entre-temps
		}
		for _, uid := range uids {
			if uid != os.Getuid() {
				return fmt.Errorf("le port %d est tenu par un autre compte (uid %d) : connex ne peut pas le libérer", port, uid)
			}
		}
		tool := comm(m.ProcRoot, l.PID)
		if agentTools[tool] {
			return fmt.Errorf("le port %d est tenu par un agent (%s, pid %d) : connex ne le libère pas", port, tool, l.PID)
		}
		if tool == "systemd" {
			// Le gestionnaire systemd --user (activation par socket) : le
			// signaler arrêterait toutes les unités du compte.
			return fmt.Errorf("le port %d est tenu par systemd (pid %d) : connex ne le libère pas", port, l.PID)
		}
		if l.PID == os.Getpid() {
			return fmt.Errorf("le port %d est tenu par connex lui-même", port)
		}
		if again, err := startTime(m.ProcRoot, l.PID); err != nil || again != st {
			continue // disparu ou remplacé pendant les contrôles
		}
		hs = append(hs, holder{pid: l.PID, start: st, app: connexApp(m.ProcRoot, l.PID)})
	}
	if len(seen) == 0 {
		return fmt.Errorf("rien n'écoute sur le port %d", port)
	}
	stopped := map[string]bool{}
	for _, h := range hs {
		if h.app != "" {
			// Une appli connex : on arrête son unité, sinon systemd la relancerait.
			if stopped[h.app] {
				continue
			}
			stopped[h.app] = true
			unlock := m.lock(h.app)
			err := m.stopUnit(ctx, UnitName(h.app))
			unlock()
			if err != nil {
				return err
			}
			continue
		}
		if err := m.terminate(ctx, h, port); err != nil {
			return err
		}
	}
	// Le port doit être libre à la fin (un arrêt d'unité peut prendre un instant).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if !m.portHeld(port, 0) {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("le port %d est toujours pris", port)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// portHeld dit si le port est en écoute ; pid non nul : tenu par ce processus.
func (m *Manager) portHeld(port, pid int) bool {
	for _, l := range m.Listeners() {
		if l.Port == port && (pid == 0 || l.PID == pid) {
			return true
		}
	}
	return false
}

// terminate envoie SIGTERM, puis SIGKILL si le processus tient encore le port
// après TermWait. Avant chaque signal, il vérifie que le pid désigne toujours
// le même processus (même heure de démarrage).
func (m *Manager) terminate(ctx context.Context, h holder, port int) error {
	same := func() bool {
		st, err := startTime(m.ProcRoot, h.pid)
		return err == nil && st == h.start
	}
	if !same() {
		return nil // déjà parti
	}
	if err := syscall.Kill(h.pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("port %d : signal au pid %d : %v", port, h.pid, err)
	}
	deadline := time.Now().Add(m.TermWait)
	for time.Now().Before(deadline) {
		if !m.portHeld(port, h.pid) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if !same() || !m.portHeld(port, h.pid) {
		return nil
	}
	if err := syscall.Kill(h.pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("port %d : signal au pid %d : %v", port, h.pid, err)
	}
	return nil
}

// ---- Inscription ----

// RunOptions décrit une appli à inscrire.
type RunOptions struct {
	Name    string
	Port    int    // 0 : port stable choisi par connex
	View    string // "" : remote
	Command []string
	Dir     string // dossier de l'appli
	PathEnv string // PATH courant
}

// Register inscrit une appli sans la lancer.
func (m *Manager) Register(o RunOptions) (Entry, error) {
	if !config.ValidName(o.Name) {
		return Entry{}, fmt.Errorf("nom d'appli invalide : %q (attendu : [a-z0-9][a-z0-9-]*, 41 caractères au plus)", o.Name)
	}
	switch o.View {
	case "":
		o.View = model.ViewRemote
	case model.ViewRemote, model.ViewTrain:
	default:
		return Entry{}, fmt.Errorf("--view : %q inconnu (remote ou train)", o.View)
	}
	if o.Port < 0 || o.Port > 65535 {
		return Entry{}, fmt.Errorf("--port : %d hors de 1…65535", o.Port)
	}
	if len(o.Command) == 0 {
		return Entry{}, errors.New("commande manquante après --")
	}
	exe, err := lookCommand(o.Command[0], o.Dir, o.PathEnv)
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	err = m.Registry.Update(func(list []Entry) ([]Entry, error) {
		reserved := map[int]string{}
		for _, x := range list {
			if x.Name == o.Name {
				return nil, fmt.Errorf("« %s » est déjà inscrite", o.Name)
			}
			reserved[x.Port] = x.Name
		}
		port := o.Port
		if port != 0 {
			if other, ok := reserved[port]; ok {
				return nil, fmt.Errorf("le port %d est déjà réservé par « %s »", port, other)
			}
		} else {
			listening := map[int]bool{}
			for _, l := range m.Listeners() {
				listening[l.Port] = true
			}
			port = choosePort(o.Name, func(p int) bool { _, r := reserved[p]; return r || listening[p] })
			if port == 0 {
				return nil, fmt.Errorf("aucun port libre entre %d et %d", portBase, portBase+portSpan-1)
			}
		}
		cmd := append([]string{exe}, o.Command[1:]...)
		for i := 1; i < len(cmd); i++ {
			if cmd[i] == "{port}" {
				cmd[i] = strconv.Itoa(port)
			}
		}
		e = Entry{Name: o.Name, Project: collect.ProjectRoot(o.Dir, collect.NetworkMounts(m.ProcRoot)), Dir: o.Dir, Command: cmd,
			Path: o.PathEnv, Port: port, View: o.View, RegisteredAt: model.Timestamp(m.Now())}
		return append(list, e), nil
	})
	return e, err
}

// lookCommand trouve la commande dans le PATH donné, et rend son chemin absolu.
// Le message d'erreur ne cite pas la commande (D11).
func lookCommand(name, dir, pathEnv string) (string, error) {
	notFound := errors.New("commande introuvable : le premier mot après -- n'est ni dans le PATH ni un exécutable")
	if name == "" {
		return "", notFound
	}
	if strings.Contains(name, "/") {
		p := name
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			return "", notFound
		}
		return filepath.Clean(p), nil
	}
	for _, d := range filepath.SplitList(pathEnv) {
		if d == "" || !filepath.IsAbs(d) {
			continue // un dossier relatif du PATH ne sert pas : l'unité tourne ailleurs
		}
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", notFound
}

// choosePort renvoie le port stable de l'appli : 3100 + FNV-32a(nom) mod 900,
// ou le suivant qui n'est pas pris, en revenant à 3100 après 3999. 0 si tout
// est pris.
func choosePort(name string, taken func(int) bool) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	first := int(h.Sum32() % portSpan)
	for i := range portSpan {
		p := portBase + (first+i)%portSpan
		if !taken(p) {
			return p
		}
	}
	return 0
}

// Remove arrête l'unité si elle tourne, puis désinscrit l'appli.
func (m *Manager) Remove(ctx context.Context, name string) error {
	if !config.ValidName(name) {
		return fmt.Errorf("nom d'appli invalide : %q", name)
	}
	defer m.lock(name)()
	e, err := m.find(name)
	if err != nil {
		return err
	}
	st, err := m.status(ctx, e.Unit())
	if err != nil {
		return err
	}
	if st.running() {
		if err := m.stopUnit(ctx, e.Unit()); err != nil {
			return err
		}
	}
	_, _, _ = m.Run(ctx, "systemctl", "--user", "reset-failed", e.Unit()) // une unité en échec disparaît avec l'appli
	return m.Registry.Update(func(list []Entry) ([]Entry, error) {
		return slices.DeleteFunc(list, func(x Entry) bool { return x.Name == name }), nil
	})
}

// ---- État et santé ----

// Apps renvoie les applis inscrites pour le relevé : état de l'unité et santé
// mesurée. listeners sont les ports en écoute du même relevé. Un seul appel à
// systemctl pour toutes les applis.
func (m *Manager) Apps(ctx context.Context, listeners []model.Listener) ([]model.App, []model.Error) {
	return m.AppsWait(ctx, listeners, probeTimeout)
}

// AppsWait est Apps, en attendant au plus wait une sonde HTTP due ; au-delà, le
// code de la sonde précédente est rendu et la sonde finit en arrière-plan.
func (m *Manager) AppsWait(ctx context.Context, listeners []model.Listener, wait time.Duration) ([]model.App, []model.Error) {
	out := []model.App{}
	list, err := m.Registry.Load()
	if err != nil {
		return out, []model.Error{{Source: "apps", Message: err.Error()}}
	}
	if len(list) == 0 {
		return out, nil
	}
	var errs []model.Error
	units := make([]string, len(list))
	for i, e := range list {
		units[i] = e.Unit()
	}
	stdout, stderr, err := m.Run(ctx, "systemctl", showArgs(units)...)
	if err != nil {
		errs = append(errs, model.Error{Source: "apps", Message: "systemctl show : " + errText(stderr, err)})
	}
	statuses := parseShow(stdout)
	now := m.Now()

	type probeJob struct {
		i    int
		addr string
	}
	var jobs []probeJob
	for i, e := range list {
		u := statuses[e.Unit()]
		a := model.App{Key: e.Name, Name: e.Name, Project: e.Project, CWD: e.Dir, Port: e.Port,
			Unit: e.Unit(), View: e.View}
		a.Health.PIDAlive = pidAlive(m.ProcRoot, u.MainPID)
		addr := ""
		for _, l := range listeners {
			if l.Port == e.Port && l.PID > 0 && inUnit(m.ProcRoot, l.PID, e.Unit()) {
				addr = l.Addr
				break
			}
		}
		a.Health.PortListening = addr != ""
		a.State = appState(u, a.Health.PortListening, now)
		if a.Health.PortListening {
			jobs = append(jobs, probeJob{i, addr})
		}
		out = append(out, a)
	}
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[j.i].Health.HTTP = m.httpCode(ctx, list[j.i].Name, j.addr, list[j.i].Port, now, wait)
		}()
	}
	wg.Wait()
	return out, errs
}

// machineName lit Machine, que Collector met à jour à chaque relevé.
func (m *Manager) machineName() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Machine
}

// httpCode renvoie le code de la dernière sonde, et sonde à nouveau si elle a
// plus de 10 s. La sonde tourne à part (une seule à la fois par appli) ; on
// l'attend au plus wait.
func (m *Manager) httpCode(ctx context.Context, name, addr string, port int, now time.Time, wait time.Duration) int {
	m.mu.Lock()
	if m.probes == nil {
		m.probes, m.inflight = map[string]probeResult{}, map[string]chan struct{}{}
	}
	p, ok := m.probes[name]
	if ok && p.addr == addr && now.Sub(p.at) < probeEvery {
		m.mu.Unlock()
		return p.code
	}
	done, running := m.inflight[name]
	if !running {
		done = make(chan struct{})
		m.inflight[name] = done
		go func() {
			// La sonde a son propre délai (2 s) : elle ne dépend pas du relevé.
			code := m.Probe(context.Background(), addr, port)
			m.mu.Lock()
			m.probes[name] = probeResult{at: now, addr: addr, code: code}
			delete(m.inflight, name)
			m.mu.Unlock()
			close(done)
		}()
	}
	m.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	case <-ctx.Done():
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.probes[name]; ok && p.addr == addr {
		return p.code
	}
	return 0
}

// probeHost choisit l'adresse à sonder pour une écoute : une écoute sur toutes
// les interfaces se sonde sur la boucle locale.
func probeHost(addr string) string {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return addr
	}
	a = a.Unmap()
	switch {
	case a == netip.IPv4Unspecified():
		return "127.0.0.1"
	case a == netip.IPv6Unspecified():
		return "::1"
	}
	return a.String()
}

// httpProbe fait GET / sur l'adresse d'écoute : délai 2 s, sans suivre les
// redirections ni lire le corps, sans mandataire. 0 s'il n'y a pas de réponse
// HTTP.
func httpProbe(ctx context.Context, addr string, port int) int {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	u := "http://" + net.JoinHostPort(probeHost(addr), strconv.Itoa(port)) + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("User-Agent", "connex-sante")
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Collector ajoute les applis au relevé d'un autre collecteur.
type Collector struct {
	Inner interface {
		Collect(ctx context.Context, full bool) model.Snapshot
	}
	Manager *Manager
}

// Collect implémente stream.Collector.
func (c *Collector) Collect(ctx context.Context, full bool) model.Snapshot {
	s := c.Inner.Collect(ctx, full)
	if ctx.Err() != nil {
		return s
	}
	wait := probeTimeout // un relevé complet attend ses sondes
	if !full {
		wait = probeWaitLive
	}
	// La machine s'identifie au premier relevé (L1) : le Manager en prend le nom.
	c.Manager.mu.Lock()
	c.Manager.Machine = s.Machine.Name
	c.Manager.mu.Unlock()
	apps, errs := c.Manager.AppsWait(ctx, s.Listeners, wait)
	s.Apps = apps
	s.Errors = append(s.Errors, errs...)
	return s
}

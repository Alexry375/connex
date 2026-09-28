package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// fakeSystemd simule systemctl --user et systemd-run : il note chaque appel et
// tient l'état des unités.
type fakeSystemd struct {
	mu    sync.Mutex
	calls [][]string
	units map[string]*unitStatus
	pid   int
	// runFails fait échouer systemd-run avec ce message d'erreur.
	runFails string
}

func newFakeSystemd() *fakeSystemd { return &fakeSystemd{units: map[string]*unitStatus{}, pid: 1000} }

func (f *fakeSystemd) run(_ context.Context, name string, args ...string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	fail := func(msg string) ([]byte, string, error) { return nil, msg + "\n", errors.New("exit status 1") }
	if name == "systemd-run" {
		if f.runFails != "" {
			return fail(f.runFails)
		}
		unit := strings.TrimPrefix(args[1], "--unit=") + ".service"
		if u, ok := f.units[unit]; ok && u.running() {
			return fail("Unit " + unit + " was already loaded")
		}
		f.pid++
		f.units[unit] = &unitStatus{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: f.pid, Started: time.Now()}
		return nil, "", nil
	}
	if name != "systemctl" || args[0] != "--user" {
		return fail("appel inattendu")
	}
	switch args[1] {
	case "show":
		var b strings.Builder
		for _, unit := range args[slices.Index(args, "--")+1:] {
			u, ok := f.units[unit]
			if !ok {
				u = &unitStatus{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}
			}
			started := ""
			if !u.Started.IsZero() {
				started = "@" + strconv.FormatInt(u.Started.Unix(), 10)
			}
			fmt.Fprintf(&b, "MainPID=%d\nExecMainStartTimestamp=%s\nId=%s\nLoadState=%s\nActiveState=%s\nSubState=%s\n\n",
				u.MainPID, started, unit, u.LoadState, u.ActiveState, u.SubState)
		}
		return []byte(b.String()), "", nil
	case "stop":
		delete(f.units, args[2])
		return nil, "", nil
	case "restart":
		u, ok := f.units[args[2]]
		if !ok {
			return fail("Unit " + args[2] + " not found.")
		}
		f.pid++
		u.MainPID, u.ActiveState = f.pid, "active"
		return nil, "", nil
	case "reset-failed":
		if u, ok := f.units[args[2]]; ok && u.ActiveState == "failed" {
			delete(f.units, args[2])
			return nil, "", nil
		}
		return fail("Unit " + args[2] + " not loaded.")
	}
	return fail("appel inattendu")
}

func (f *fakeSystemd) set(unit, active, sub string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pid++
	f.units[unit] = &unitStatus{LoadState: "loaded", ActiveState: active, SubState: sub, MainPID: f.pid, Started: time.Now()}
}

// mutating renvoie les appels qui changent quelque chose (hors show).
func (f *fakeSystemd) mutating() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if !slices.Contains(c, "show") {
			out = append(out, c)
		}
	}
	return out
}

// testManager renvoie un Manager sur un registre temporaire, un faux systemd et
// un faux /proc, avec des ports en écoute choisis par le test.
func testManager(t *testing.T) (*Manager, *fakeSystemd, *[]model.Listener) {
	t.Helper()
	dir := t.TempDir()
	fs := newFakeSystemd()
	var listeners []model.Listener
	m := &Manager{Registry: Registry{Path: filepath.Join(dir, "state", "connex", "apps.json")}, Machine: "m1",
		ProcRoot: filepath.Join(dir, "proc"), Run: fs.run, Now: time.Now, TermWait: time.Second,
		Listeners: func() []model.Listener { return listeners },
		Probe:     func(context.Context, string, int) int { return 0 },
	}
	os.MkdirAll(m.ProcRoot, 0o755)
	return m, fs, &listeners
}

// fakeProc crée /proc/<pid> dans le faux /proc : cgroup, comm et stat.
func fakeProc(t *testing.T, procRoot string, pid int, comm, cgroup string, start uint64) {
	t.Helper()
	d := filepath.Join(procRoot, strconv.Itoa(pid))
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644)
	os.WriteFile(filepath.Join(d, "cgroup"), []byte("0::"+cgroup+"\n"), 0o644)
	stat := fmt.Sprintf("%d (%s) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 %d 1000 100", pid, comm, start)
	os.WriteFile(filepath.Join(d, "stat"), []byte(stat), 0o644)
	u := os.Getuid()
	os.WriteFile(filepath.Join(d, "status"), []byte(fmt.Sprintf("Name:\t%s\nUid:\t%d\t%d\t%d\t%d\n", comm, u, u, u, u)), 0o644)
}

func register(t *testing.T, m *Manager, name string, port int, cmd ...string) Entry {
	t.Helper()
	if len(cmd) == 0 {
		cmd = []string{"true", "--secret-token=abc123", "{port}"}
	}
	e, err := m.Register(RunOptions{Name: name, Port: port, Command: cmd, Dir: t.TempDir(), PathEnv: os.Getenv("PATH")})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestStartArgsExact : l'appel à systemd-run est exactement celui du contrat,
// sans --collect, avec PORT et PATH seuls repris.
func TestStartArgsExact(t *testing.T) {
	e := Entry{Name: "site", Dir: "/home/a/site", Command: []string{"/usr/bin/python3", "-m", "http.server", "3100"},
		Path: "/usr/bin:/bin", Port: 3100}
	want := []string{"--user", "--unit=connex-site", "--description=connex : site", "-p", "Restart=on-failure",
		"-p", "RestartSec=2", "-p", "TimeoutStopSec=10", "--working-directory=/home/a/site",
		"--setenv=PORT=3100", "--setenv=PATH=/usr/bin:/bin", "--", "/usr/bin/python3", "-m", "http.server", "3100"}
	if got := startArgs(e); !reflect.DeepEqual(got, want) {
		t.Fatalf("obtenu %q\nattendu %q", got, want)
	}
	if slices.Contains(startArgs(e), "--collect") {
		t.Fatal("--collect présent")
	}
}

// TestParseShowAndAppState : la sortie de systemctl show et la table d'états.
func TestParseShowAndAppState(t *testing.T) {
	out := "MainPID=0\nNRestarts=0\nExecMainStartTimestamp=\nId=connex-a.service\nLoadState=not-found\nActiveState=inactive\nSubState=dead\n\n" +
		"MainPID=42\nExecMainStartTimestamp=@1790511884\nId=connex-b.service\nLoadState=loaded\nActiveState=active\nSubState=running\n"
	st := parseShow([]byte(out))
	if a := st["connex-a.service"]; a.LoadState != "not-found" || a.MainPID != 0 || !a.Started.IsZero() {
		t.Fatalf("a : %+v", a)
	}
	if b := st["connex-b.service"]; b.MainPID != 42 || b.Started.Unix() != 1790511884 || !b.running() {
		t.Fatalf("b : %+v", b)
	}
	now := time.Now()
	fresh, old := now.Add(-5*time.Second), now.Add(-31*time.Second)
	cases := []struct {
		u    unitStatus
		held bool
		want string
	}{
		{unitStatus{}, false, model.AppStopped},
		{unitStatus{LoadState: "not-found", ActiveState: "inactive"}, false, model.AppStopped},
		{unitStatus{ActiveState: "activating", SubState: "start"}, false, model.AppStarting},
		{unitStatus{ActiveState: "activating", SubState: "auto-restart"}, false, model.AppStarting},
		{unitStatus{ActiveState: "inactive", SubState: "auto-restart"}, false, model.AppStarting},
		{unitStatus{ActiveState: "active", Started: fresh}, true, model.AppRunning},
		{unitStatus{ActiveState: "active", Started: fresh}, false, model.AppStarting},
		{unitStatus{ActiveState: "active", Started: old}, false, model.AppRunning},
		{unitStatus{ActiveState: "failed"}, false, model.AppFailed},
	}
	for _, c := range cases {
		if got := appState(c.u, c.held, now); got != c.want {
			t.Errorf("%+v port tenu=%v : %s, attendu %s", c.u, c.held, got, c.want)
		}
	}
}

// TestChoosePortStableAndSkips : 3100 + FNV-32a mod 900, puis le suivant libre,
// en revenant au début de la plage.
func TestChoosePortStableAndSkips(t *testing.T) {
	// FNV-32a("site") calculé à part (python3 : l'algorithme de référence).
	none := func(int) bool { return false }
	p := choosePort("site", none)
	if p != choosePort("site", none) || p < 3100 || p > 3999 {
		t.Fatalf("port %d instable ou hors plage", p)
	}
	if want := 3100 + int(fnv32a("site")%900); p != want {
		t.Fatalf("port %d, attendu %d", p, want)
	}
	if got := choosePort("site", func(q int) bool { return q == p || q == p+1 }); got != p+2 && !(p >= 3998 && got < p) {
		t.Fatalf("ports pris non sautés : %d (base %d)", got, p)
	}
	if got := choosePort("site", func(q int) bool { return q >= p }); got != 3100 && p != 3100 {
		t.Fatalf("pas de retour au début de la plage : %d", got)
	}
	if got := choosePort("site", func(int) bool { return true }); got != 0 {
		t.Fatalf("plage pleine : %d au lieu de 0", got)
	}
}

// fnv32a est l'algorithme FNV-1a 32 bits écrit à la main, référence
// indépendante de hash/fnv.
func fnv32a(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// TestRegistryPermissionsAndAtomic : dossier 0700 (corrigé s'il était ouvert),
// fichier 0600, aucun fichier temporaire laissé.
func TestRegistryPermissionsAndAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connex")
	os.MkdirAll(dir, 0o755)
	r := Registry{Path: filepath.Join(dir, "apps.json")}
	err := r.Update(func(l []Entry) ([]Entry, error) { return append(l, Entry{Name: "b"}, Entry{Name: "a"}), nil })
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dossier en %o", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(r.Path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("registre en %o", fi.Mode().Perm())
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("fichier temporaire laissé : %s", e.Name())
		}
		if fi, _ := e.Info(); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s en %o", e.Name(), fi.Mode().Perm())
		}
	}
	list, err := r.Load()
	if err != nil || len(list) != 2 || list[0].Name != "a" {
		t.Fatalf("relecture : %+v %v", list, err)
	}
	// Une erreur dans fn ne touche pas au fichier.
	before, _ := os.ReadFile(r.Path)
	if err := r.Update(func([]Entry) ([]Entry, error) { return nil, errors.New("non") }); err == nil {
		t.Fatal("erreur perdue")
	}
	if after, _ := os.ReadFile(r.Path); string(after) != string(before) {
		t.Fatal("registre modifié malgré l'erreur")
	}
}

// TestRegisterAndRefusals : commande trouvée dans le PATH et gardée en chemin
// absolu, {port} remplacé, refus avec message ; le message de commande
// introuvable ne cite pas la commande.
func TestRegisterAndRefusals(t *testing.T) {
	m, _, listeners := testManager(t)
	project := t.TempDir()
	os.WriteFile(filepath.Join(project, "go.mod"), []byte("module x\n"), 0o644)
	sub := filepath.Join(project, "web")
	os.Mkdir(sub, 0o755)
	e, err := m.Register(RunOptions{Name: "test-a", Command: []string{"true", "--port", "{port}", "{port}x"},
		Dir: sub, PathEnv: os.Getenv("PATH")})
	if err != nil {
		t.Fatal(err)
	}
	truePath, _ := exec.LookPath("true")
	want := []string{truePath, "--port", strconv.Itoa(e.Port), "{port}x"}
	if !reflect.DeepEqual(e.Command, want) || e.Project != project || e.Dir != sub || e.View != model.ViewRemote {
		t.Fatalf("entrée %+v", e)
	}
	if e.Port != choosePort("test-a", func(int) bool { return false }) {
		t.Fatalf("port %d non stable", e.Port)
	}
	// Le port stable suivant saute un port en écoute et un port réservé.
	*listeners = []model.Listener{{Port: choosePort("test-b", func(int) bool { return false }), Mine: false, UID: 0}}
	e2 := register(t, m, "test-b", 0)
	if e2.Port == (*listeners)[0].Port || e2.Port == e.Port {
		t.Fatalf("port %d déjà pris", e2.Port)
	}
	refusals := []struct {
		o    RunOptions
		want string
	}{
		{RunOptions{Name: "Mauvais-Nom", Command: []string{"true"}}, "nom d'appli invalide"},
		{RunOptions{Name: "test-a", Command: []string{"true"}}, "déjà inscrite"},
		{RunOptions{Name: "test-c", Port: e.Port, Command: []string{"true"}}, "déjà réservé par « test-a »"},
		{RunOptions{Name: "test-d", Command: []string{"commande-absente-xyz"}}, "commande introuvable"},
		{RunOptions{Name: "test-e", View: "tv", Command: []string{"true"}}, "--view"},
	}
	for _, r := range refusals {
		r.o.Dir, r.o.PathEnv = t.TempDir(), os.Getenv("PATH")
		_, err := m.Register(r.o)
		if err == nil || !strings.Contains(err.Error(), r.want) {
			t.Errorf("%+v : %v, attendu %q", r.o, err, r.want)
		}
		if err != nil && strings.Contains(err.Error(), "commande-absente-xyz") {
			t.Errorf("le message cite la commande : %v", err)
		}
	}
	list, _ := m.Registry.Load()
	if len(list) != 2 {
		t.Fatalf("%d applis inscrites, 2 attendues", len(list))
	}
}

// TestActionsWithFakeSystemd : start, stop, restart et leurs refus, avec les
// messages du simulateur ; start remet l'unité à zéro avant systemd-run.
func TestActionsWithFakeSystemd(t *testing.T) {
	ctx := context.Background()
	m, fs, listeners := testManager(t)
	e := register(t, m, "test-a", 0)
	unit := e.Unit()

	if err := m.Stop(ctx, "test-a"); err == nil || err.Error() != "« test-a » est déjà arrêtée" {
		t.Fatalf("stop d'une appli arrêtée : %v", err)
	}
	if err := m.Start(ctx, "test-inconnue"); err == nil || err.Error() != "aucune appli « test-inconnue » sur m1" {
		t.Fatalf("appli inconnue : %v", err)
	}
	if err := m.Start(ctx, "test-a"); err != nil {
		t.Fatal(err)
	}
	calls := fs.mutating()
	if len(calls) != 2 || !reflect.DeepEqual(calls[0], []string{"systemctl", "--user", "reset-failed", unit}) ||
		!reflect.DeepEqual(calls[1], append([]string{"systemd-run"}, startArgs(e)...)) {
		t.Fatalf("appels : %q", calls)
	}
	if err := m.Start(ctx, "test-a"); err == nil || err.Error() != "« test-a » tourne déjà" {
		t.Fatalf("start d'une appli qui tourne : %v", err)
	}
	fs.set(unit, "activating", "auto-restart")
	if err := m.Start(ctx, "test-a"); err == nil || !strings.Contains(err.Error(), "tourne déjà") {
		t.Fatalf("start pendant une relance : %v", err)
	}
	fs.set(unit, "active", "running")
	pid := fs.units[unit].MainPID
	if err := m.Restart(ctx, "test-a"); err != nil {
		t.Fatal(err)
	}
	if fs.units[unit].MainPID == pid || !slices.Contains(fs.mutating()[len(fs.mutating())-1], "restart") {
		t.Fatal("restart d'une unité qui tourne : pas de systemctl restart")
	}
	if err := m.Stop(ctx, "test-a"); err != nil {
		t.Fatal(err)
	}
	if last := fs.mutating()[len(fs.mutating())-1]; !reflect.DeepEqual(last, []string{"systemctl", "--user", "stop", unit}) {
		t.Fatalf("arrêt : %q", last)
	}
	// restart d'une appli arrêtée : elle démarre.
	if err := m.Restart(ctx, "test-a"); err != nil {
		t.Fatal(err)
	}
	if last := fs.mutating()[len(fs.mutating())-1]; last[0] != "systemd-run" {
		t.Fatalf("restart d'une appli arrêtée : %q", last)
	}
	// Port pris par un autre processus : refus avant systemd-run, comme le simulateur.
	fs.set(unit, "failed", "failed")
	fakeProc(t, m.ProcRoot, 812, "python3", "/user.slice/app.slice/vte-spawn.scope", 1)
	*listeners = []model.Listener{{Port: e.Port, Mine: true, PID: 812, Process: "python3"}}
	n := len(fs.mutating())
	if err := m.Start(ctx, "test-a"); err == nil || err.Error() != fmt.Sprintf("le port %d est déjà pris par python3 (pid 812)", e.Port) {
		t.Fatalf("port pris : %v", err)
	}
	if len(fs.mutating()) != n {
		t.Fatalf("systemd appelé malgré le refus : %q", fs.mutating()[n:])
	}
	// Une erreur de systemd-run qui citerait la commande est masquée.
	*listeners = nil
	fs.runFails = "Failed to start: --secret-token=abc123 refused"
	err := m.Start(ctx, "test-a")
	if err == nil || strings.Contains(err.Error(), "abc123") {
		t.Fatalf("erreur de systemd-run non masquée : %v", err)
	}
}

// TestValidateRejectsHostileTargets : la cible est validée avant tout ; une
// requête refusée n'appelle jamais systemd.
func TestValidateRejectsHostileTargets(t *testing.T) {
	m, fs, _ := testManager(t)
	bad := []struct{ op, target string }{
		{"exec", "touch /tmp/x"}, {"start", "x; touch /tmp/x"}, {"start", "../../x"}, {"stop", "$(touch /tmp/x)"},
		{"restart", ""}, {"start", "-rf"}, {"start", "A"}, {"free_port", "abc"}, {"free_port", "70000"},
		{"free_port", "0"}, {"free_port", "+80"}, {"free_port", " 80"}, {"free_port", "80 "}, {"free_port", ""},
		{"refresh", ""}, {"shell", ""},
	}
	for _, b := range bad {
		if m.Validate(b.op, b.target) == "" {
			t.Errorf("%s %q accepté", b.op, b.target)
		}
		if err := m.Do(context.Background(), b.op, b.target); err == nil {
			t.Errorf("Do %s %q sans erreur", b.op, b.target)
		}
	}
	if len(fs.calls) != 0 {
		t.Fatalf("systemd appelé : %q", fs.calls)
	}
	for _, ok := range []struct{ op, target string }{{"start", "site"}, {"stop", "a-b-1"}, {"free_port", "5000"}, {"free_port", "65535"}} {
		if r := m.Validate(ok.op, ok.target); r != "" {
			t.Errorf("%s %q refusé : %s", ok.op, ok.target, r)
		}
	}
}

// TestAppsHealthMeasured : port_listening vient du cgroup du processus qui tient
// le port, http d'une vraie sonde, au plus une toutes les 10 s ; la commande et
// le PATH ne sortent jamais dans le relevé.
func TestAppsHealthMeasured(t *testing.T) {
	ctx := context.Background()
	m, fs, listeners := testManager(t)
	e := register(t, m, "test-a", 0)
	if err := m.Start(ctx, "test-a"); err != nil {
		t.Fatal(err)
	}
	mainPID := fs.units[e.Unit()].MainPID
	fakeProc(t, m.ProcRoot, mainPID, "python3", "/user.slice/user@1000.service/app.slice/"+e.Unit(), 1)
	fakeProc(t, m.ProcRoot, 999, "python3", "/user.slice/user@1000.service/app.slice/autre.scope", 1)
	var probes []string
	m.Probe = func(_ context.Context, addr string, port int) int {
		probes = append(probes, addr+":"+strconv.Itoa(port))
		return 200
	}
	now := time.Now()
	m.Now = func() time.Time { return now }

	// Le port est tenu par un processus hors de l'unité : pas d'écoute, pas de sonde.
	*listeners = []model.Listener{{Port: e.Port, Addr: "0.0.0.0", Mine: true, PID: 999}}
	apps, errs := m.Apps(ctx, *listeners)
	if len(errs) != 0 || len(apps) != 1 {
		t.Fatalf("%+v %+v", apps, errs)
	}
	a := apps[0]
	if !a.Health.PIDAlive || a.Health.PortListening || a.Health.HTTP != 0 || a.State != model.AppStarting || len(probes) != 0 {
		t.Fatalf("port tenu par un autre : %+v, sondes %v", a, probes)
	}
	// Tenu par l'unité : running, sonde sur 127.0.0.1 pour une écoute sur 0.0.0.0.
	*listeners = []model.Listener{{Port: e.Port, Addr: "0.0.0.0", Mine: true, PID: mainPID}}
	a = must(m.Apps(ctx, *listeners))[0]
	if a.State != model.AppRunning || !a.Health.PortListening || a.Health.HTTP != 200 || a.Unit != e.Unit() || a.Key != "test-a" {
		t.Fatalf("appli : %+v", a)
	}
	if len(probes) != 1 || probes[0] != "0.0.0.0:"+strconv.Itoa(e.Port) || probeHost("0.0.0.0") != "127.0.0.1" || probeHost("::") != "::1" {
		t.Fatalf("sondes : %v", probes)
	}
	now = now.Add(9 * time.Second)
	must(m.Apps(ctx, *listeners))
	if len(probes) != 1 {
		t.Fatalf("sonde refaite avant 10 s : %v", probes)
	}
	now = now.Add(2 * time.Second)
	must(m.Apps(ctx, *listeners))
	if len(probes) != 2 {
		t.Fatalf("sonde pas refaite après 10 s : %v", probes)
	}
	b, _ := json.Marshal(must(m.Apps(ctx, *listeners)))
	for _, secret := range []string{"secret-token", "abc123", os.Getenv("PATH")} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("le relevé contient %q : %s", secret, b)
		}
	}
	// Un seul appel à systemctl show par passe, pour toutes les applis.
	register(t, m, "test-b", 0)
	n := len(fs.calls)
	must(m.Apps(ctx, *listeners))
	if len(fs.calls) != n+1 || !slices.Contains(fs.calls[n], "connex-test-b.service") || !slices.Contains(fs.calls[n], e.Unit()) {
		t.Fatalf("appels à show : %q", fs.calls[n:])
	}
}

func must(a []model.App, _ []model.Error) []model.App { return a }

// TestSlowProbeDoesNotBlockLiveSnapshot : une appli qui tient son port sans
// répondre ne retient un relevé du flux que probeWaitLive ; sa sonde finit à
// part, sans être relancée entre-temps, et son code apparaît au relevé suivant.
func TestSlowProbeDoesNotBlockLiveSnapshot(t *testing.T) {
	ctx := context.Background()
	m, fs, listeners := testManager(t)
	e := register(t, m, "test-a", 0)
	m.Start(ctx, "test-a")
	pid := fs.units[e.Unit()].MainPID
	fakeProc(t, m.ProcRoot, pid, "python3", "/user.slice/user@1000.service/app.slice/"+e.Unit(), 1)
	*listeners = []model.Listener{{Port: e.Port, Addr: "127.0.0.1", Mine: true, PID: pid}}
	gate := make(chan struct{})
	var calls atomic.Int32
	m.Probe = func(context.Context, string, int) int {
		calls.Add(1)
		<-gate
		return 200
	}
	col := &Collector{Inner: staticCollector{*listeners}, Manager: m}
	for range 2 {
		t0 := time.Now()
		s := col.Collect(ctx, false)
		if d := time.Since(t0); d > time.Second || s.Apps[0].Health.HTTP != 0 {
			t.Fatalf("relevé retenu %s, http %d", d, s.Apps[0].Health.HTTP)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d sondes lancées", calls.Load())
	}
	close(gate)
	for end := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if a := col.Collect(ctx, false).Apps[0]; a.Health.HTTP == 200 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("le code de la sonde n'apparaît pas")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d sondes lancées", calls.Load())
	}
}

type staticCollector struct{ listeners []model.Listener }

func (c staticCollector) Collect(context.Context, bool) model.Snapshot {
	s := model.NewSnapshot(model.Machine{Name: "ici"}, time.Now())
	s.Listeners = c.listeners
	return s
}

// TestFreePortRefusals : autre compte, agent, rien en écoute ; aucun signal ni
// arrêt dans ces cas.
func TestFreePortRefusals(t *testing.T) {
	ctx := context.Background()
	m, fs, listeners := testManager(t)
	if err := m.FreePort(ctx, 5000); err == nil || err.Error() != "rien n'écoute sur le port 5000" {
		t.Fatalf("rien : %v", err)
	}
	*listeners = []model.Listener{{Port: 5000, Mine: false, UID: 1003}}
	if err := m.FreePort(ctx, 5000); err == nil ||
		err.Error() != "le port 5000 est tenu par un autre compte (uid 1003) : connex ne peut pas le libérer" {
		t.Fatalf("autre compte : %v", err)
	}
	for _, tool := range []string{"claude", "codex"} {
		fakeProc(t, m.ProcRoot, 4242, tool, "/user.slice/app.slice/x.scope", 7)
		*listeners = []model.Listener{{Port: 5000, Mine: true, PID: 4242, Process: tool}}
		if err := m.FreePort(ctx, 5000); err == nil || !strings.Contains(err.Error(), "agent") {
			t.Fatalf("%s : %v", tool, err)
		}
	}
	// Le gestionnaire systemd --user, qui tient un port par activation.
	fakeProc(t, m.ProcRoot, 4243, "systemd", "/user.slice/user@1000.service/init.scope", 7)
	*listeners = []model.Listener{{Port: 5000, Mine: true, PID: 4243, Process: "systemd"}}
	if err := m.FreePort(ctx, 5000); err == nil || err.Error() != "le port 5000 est tenu par systemd (pid 4243) : connex ne le libère pas" {
		t.Fatalf("systemd : %v", err)
	}
	// Vu comme « à moi » par le relevé, mais un compte effectif différent
	// (programme setuid) : c'est un autre compte.
	fakeProc(t, m.ProcRoot, 4244, "passwd", "/user.slice/app.slice/y.scope", 7)
	u := os.Getuid()
	os.WriteFile(filepath.Join(m.ProcRoot, "4244", "status"), []byte(fmt.Sprintf("Uid:\t%d\t0\t0\t0\n", u)), 0o644)
	*listeners = []model.Listener{{Port: 5000, Mine: true, PID: 4244, Process: "passwd"}}
	if err := m.FreePort(ctx, 5000); err == nil ||
		err.Error() != "le port 5000 est tenu par un autre compte (uid 0) : connex ne peut pas le libérer" {
		t.Fatalf("setuid : %v", err)
	}
	if len(fs.mutating()) != 0 {
		t.Fatalf("systemd appelé : %q", fs.mutating())
	}
}

// TestFreePortStopsConnexUnit : un port tenu par une unité connex-<nom> est
// libéré en arrêtant l'unité, pas en tuant le processus (systemd le relancerait).
func TestFreePortStopsConnexUnit(t *testing.T) {
	ctx := context.Background()
	m, fs, listeners := testManager(t)
	e := register(t, m, "test-a", 0)
	m.Start(ctx, "test-a")
	pid := fs.units[e.Unit()].MainPID
	fakeProc(t, m.ProcRoot, pid, "python3", "/user.slice/user@1000.service/app.slice/"+e.Unit(), 1)
	*listeners = []model.Listener{{Port: e.Port, Mine: true, PID: pid, Process: "python3"}}
	// Le faux systemd libère le port quand l'unité s'arrête.
	m.Listeners = func() []model.Listener {
		if _, ok := fs.units[e.Unit()]; ok {
			return *listeners
		}
		return nil
	}
	if err := m.FreePort(ctx, e.Port); err != nil {
		t.Fatal(err)
	}
	if last := fs.mutating()[len(fs.mutating())-1]; !reflect.DeepEqual(last, []string{"systemctl", "--user", "stop", e.Unit()}) {
		t.Fatalf("appel : %q", last)
	}
}

// TestFreePortSignalsOwnProcess : SIGTERM à un processus de l'utilisateur qui
// tient le port, après vérification de son heure de démarrage. Le processus
// est un sleep lancé par le test ; le « port » est simulé (aucune écoute).
func TestFreePortSignalsOwnProcess(t *testing.T) {
	ctx := context.Background()
	m, _, _ := testManager(t)
	m.ProcRoot = "/proc"
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	defer cmd.Process.Kill()
	alive := func() bool {
		select {
		case <-exited:
			return false
		default:
			return true
		}
	}
	m.Listeners = func() []model.Listener {
		if alive() {
			return []model.Listener{{Port: 5999, Mine: true, PID: cmd.Process.Pid, Process: "sleep"}}
		}
		return nil
	}
	if err := m.FreePort(ctx, 5999); err != nil {
		t.Fatal(err)
	}
	if alive() {
		t.Fatal("le processus tient toujours le port")
	}
	if st := cmd.ProcessState; st == nil || st.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Fatalf("pas arrêté par SIGTERM : %v", cmd.ProcessState)
	}
}

// TestTerminateChecksStartTime : un pid dont l'heure de démarrage a changé
// (pid réutilisé) ne reçoit aucun signal.
func TestTerminateChecksStartTime(t *testing.T) {
	m, _, _ := testManager(t)
	m.ProcRoot = "/proc"
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	st, err := startTime("/proc", cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	m.Listeners = func() []model.Listener { return nil }
	if err := m.terminate(context.Background(), holder{pid: cmd.Process.Pid, start: st + 1}, 5999); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid))); err != nil {
		t.Fatal("processus tué malgré une heure de démarrage différente")
	}
	if cmd.ProcessState != nil {
		t.Fatal("processus arrêté")
	}
}

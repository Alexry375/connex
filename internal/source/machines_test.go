package source

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// Le binaire de test sert aussi de faux ssh et de faux flux local : TestMain
// reconnaît ces rôles avant de lancer les tests.
//
// Faux ssh (variable fakeSSHRoot définie) : il exécute les trois commandes
// distantes constantes dans un HOME par destination, $root/<destination> :
//   - « mort… » refuse la connexion (code 255), comme toute destination dont
//     le HOME contient un fichier refuse ;
//   - l'envoi du binaire échoue si le HOME contient un fichier disque-plein ;
//   - le fichier arch du HOME donne la sortie de uname -m (x86_64 sinon) ;
//   - le flux écrit son pid dans stream.pid, une ligne de .bashrc bavard, un
//     relevé, puis répond aux requêtes ; SIGTERM le coupe comme une coupure
//     réseau (code 255, « Broken pipe »).
const fakeSSHRoot = "CONNEX_TEST_FAKE_SSH_ROOT"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "fake-local-stream" {
		fakeStream("annonce-locale", os.Stdin, os.Stdout)
		os.Exit(0)
	}
	if root := os.Getenv(fakeSSHRoot); root != "" && len(os.Args) > 1 && os.Args[1] == "-T" {
		os.Exit(fakeSSH(root, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeSSH(root string, args []string) int {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if args[i] == "-o" {
			i++
		}
		i++
	}
	if i+1 >= len(args) {
		fmt.Fprintln(os.Stderr, "faux ssh : arguments illisibles")
		return 255
	}
	dest, remote := args[i], strings.Join(args[i+1:], " ")
	j, _ := os.OpenFile(filepath.Join(root, "journal"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	fmt.Fprintf(j, "%s\t%s\n", dest, remote)
	j.Close()
	if strings.HasPrefix(dest, "mort") {
		fmt.Fprintf(os.Stderr, "ssh: connect to host %s port 22: Connection refused\n", dest)
		return 255
	}
	home := filepath.Join(root, dest)
	bin := filepath.Join(home, ".local", "bin", "connex")
	if _, err := os.Stat(filepath.Join(home, "refuse")); err == nil {
		fmt.Fprintf(os.Stderr, "ssh: connect to host %s port 22: Connection refused\n", dest)
		return 255
	}
	switch remote {
	case remoteProbe:
		fmt.Println("Bienvenue sur " + dest) // .bashrc bavard
		arch := "x86_64"
		if b, err := os.ReadFile(filepath.Join(home, "arch")); err == nil {
			arch = strings.TrimSpace(string(b))
		}
		fmt.Println(arch)
		if b, err := os.ReadFile(bin); err == nil {
			sum := sha256.Sum256(b)
			fmt.Printf("%s  %s\n", hex.EncodeToString(sum[:]), bin)
		}
		return 0
	case remoteUpload:
		b, _ := io.ReadAll(os.Stdin)
		if _, err := os.Stat(filepath.Join(home, "disque-plein")); err == nil {
			fmt.Fprintln(os.Stderr, "cat: write error: No space left on device")
			return 1
		}
		os.MkdirAll(filepath.Dir(bin), 0o755)
		if err := os.WriteFile(bin+".tmp", b, 0o755); err != nil {
			return 1
		}
		return map[bool]int{true: 0, false: 1}[os.Rename(bin+".tmp", bin) == nil]
	case remoteStream:
		if _, err := os.Stat(bin); err != nil {
			fmt.Fprintln(os.Stderr, "bash: ~/.local/bin/connex: No such file or directory")
			return 127
		}
		os.WriteFile(filepath.Join(home, "stream.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)
		go func() {
			<-sig
			fmt.Fprintln(os.Stderr, "client_loop: send disconnect: Broken pipe")
			os.Exit(255)
		}()
		fmt.Println("ligne de .bashrc bavard")
		fakeStream("annonce-"+dest, os.Stdin, os.Stdout)
		return 0
	}
	fmt.Fprintln(os.Stderr, "faux ssh : commande distante inattendue : "+remote)
	return 127
}

// fakeStream parle comme `connex stream` : un relevé, puis une réponse par
// requête ; il s'arrête quand son entrée se ferme.
func fakeStream(name string, in io.Reader, out io.Writer) {
	w := bufio.NewWriter(out)
	snap := model.NewSnapshot(model.Machine{Name: name, OS: "linux", Arch: "amd64", User: "u"}, time.Now())
	b, _ := json.Marshal(snap)
	w.Write(append(b, '\n'))
	w.Flush()
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		var req model.Request
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		rep := model.Reply{Schema: 1, Type: "reply", ID: req.ID, OK: req.Target != "x"}
		if !rep.OK {
			rep.Error = "« x » est déjà arrêtée"
		}
		b, _ := json.Marshal(rep)
		w.Write(append(b, '\n'))
		// Un patch pour vérifier le renommage.
		p := model.NewPatch(name, time.Now(), []model.Change{{Op: model.OpAdd, Kind: model.KindRepo, Key: "/r",
			Data: model.Repo{Key: "/r", Path: "/r", Worktrees: []model.Worktree{}}}})
		b, _ = json.Marshal(p)
		w.Write(append(b, '\n'))
		w.Flush()
	}
}

// fakeWorld prépare un faux ssh et un dossier dist.
type fakeWorld struct {
	root, dist string
	binary     []byte
}

func newFakeWorld(t *testing.T) *fakeWorld {
	t.Helper()
	w := &fakeWorld{root: t.TempDir(), dist: t.TempDir(), binary: []byte("binaire connex v1 " + t.Name())}
	os.WriteFile(filepath.Join(w.dist, "connex-linux-amd64"), w.binary, 0o755)
	t.Setenv(fakeSSHRoot, w.root)
	return w
}

func (w *fakeWorld) options(targets ...Target) ConnectOptions {
	return ConnectOptions{Targets: targets, LocalArgv: []string{os.Args[0], "fake-local-stream"}, Dist: w.dist,
		SSH: os.Args[0], MinBackoff: 50 * time.Millisecond, MaxBackoff: 200 * time.Millisecond}
}

func (w *fakeWorld) journal(t *testing.T) []string {
	b, _ := os.ReadFile(filepath.Join(w.root, "journal"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (w *fakeWorld) count(t *testing.T, dest, remote string) int {
	n := 0
	for _, l := range w.journal(t) {
		if l == dest+"\t"+remote {
			n++
		}
	}
	return n
}

// collector lit les événements d'une source.
type collector struct {
	t    *testing.T
	src  Source
	all  []Event
	used map[int]bool
}

// until rend le premier événement, déjà reçu ou à venir, qui vérifie ok et
// n'a pas encore été rendu.
func (c *collector) until(what string, timeout time.Duration, ok func(Event) bool) Event {
	c.t.Helper()
	if c.used == nil {
		c.used = map[int]bool{}
	}
	for i, ev := range c.all {
		if !c.used[i] && ok(ev) {
			c.used[i] = true
			return ev
		}
	}
	deadline := time.After(timeout)
	for {
		select {
		case ev, open := <-c.src.Events():
			if !open {
				c.t.Fatalf("%s : source fermée", what)
			}
			c.all = append(c.all, ev)
			if ok(ev) {
				c.used[len(c.all)-1] = true
				return ev
			}
		case <-deadline:
			c.t.Fatalf("%s : rien en %s (reçu : %s)", what, timeout, c.summary())
		}
	}
}

func (c *collector) summary() string {
	var s []string
	for _, ev := range c.all {
		switch {
		case ev.Snapshot != nil:
			s = append(s, ev.Machine+":snapshot("+ev.Snapshot.Machine.Name+")")
		case ev.Status != nil:
			s = append(s, fmt.Sprintf("%s:status(%v,%s)", ev.Machine, ev.Status.Reachable, ev.Status.Reason))
		case ev.Patch != nil:
			s = append(s, ev.Machine+":patch("+ev.Patch.Machine+")")
		case ev.Reply != nil:
			s = append(s, ev.Machine+":reply("+ev.Reply.ID+")")
		}
	}
	return strings.Join(s, " ")
}

func (c *collector) statuses(machine string, reachable bool) int {
	n := 0
	for _, ev := range c.all {
		if ev.Status != nil && ev.Machine == machine && ev.Status.Reachable == reachable {
			n++
		}
	}
	return n
}

func snapshotOf(machine string) func(Event) bool {
	return func(ev Event) bool { return ev.Snapshot != nil && ev.Machine == machine }
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func readPID(t *testing.T, path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(b))
	return pid
}

// TestSSHArgvExact : les options du contrat, dans l'ordre, puis la destination
// et la commande distante constante, chacune en argument à part.
func TestSSHArgvExact(t *testing.T) {
	want := []string{"ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=6", "-o", "ControlPath=none", "abc@pi5", "~/.local/bin/connex stream"}
	if got := sshArgv("ssh", "abc@pi5", remoteStream); !reflect.DeepEqual(got, want) {
		t.Fatalf("obtenu %q", got)
	}
	if remoteProbe != "uname -m; sha256sum ~/.local/bin/connex 2>/dev/null || true" ||
		remoteUpload != "mkdir -p ~/.local/bin && cat > ~/.local/bin/.connex.tmp && chmod 755 ~/.local/bin/.connex.tmp && mv -f ~/.local/bin/.connex.tmp ~/.local/bin/connex" {
		t.Fatal("commandes distantes différentes du contrat")
	}
}

// TestParseProbe : architecture et empreinte, malgré un .bashrc bavard.
func TestParseProbe(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	cases := []struct{ out, arch, sum string }{
		{"x86_64\n" + sum + "  /home/a/.local/bin/connex\n", "x86_64", sum},
		{"aarch64\n", "aarch64", ""},
		{"Bienvenue !\nMessage du jour : sauvegarde ce soir\naarch64\n" + sum + "  /home/abc/.local/bin/connex\n", "aarch64", sum},
		{"", "", ""},
	}
	for _, c := range cases {
		if a, s := parseProbe(c.out); a != c.arch || s != c.sum {
			t.Errorf("%q : %q %q", c.out, a, s)
		}
	}
}

// TestNextBackoff : 1, 2, 4, 8, 16, puis 30 s au plus.
func TestNextBackoff(t *testing.T) {
	d := time.Second
	var got []time.Duration
	for range 7 {
		got = append(got, d)
		d = nextBackoff(d, 30*time.Second)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for i := range want {
		want[i] *= time.Second
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

// TestConnectDeploysAndRenames : binaire posé une fois avec la bonne empreinte,
// événements au nom de la configuration, requêtes routées, machine refusée
// signalée une seule fois, rien ne reste après Close.
func TestConnectDeploysAndRenames(t *testing.T) {
	w := newFakeWorld(t)
	src, err := Connect(context.Background(), w.options(Target{Name: "local"}, Target{Name: "f1", SSH: "f1"}, Target{Name: "mort1", SSH: "mort1"}))
	if err != nil {
		t.Fatal(err)
	}
	c := &collector{t: t, src: src}
	c.until("relevé local", 10*time.Second, snapshotOf("local"))
	snap := c.until("relevé f1", 10*time.Second, snapshotOf("f1"))
	if snap.Snapshot.Machine.Name != "f1" {
		t.Fatalf("nom annoncé gardé : %q", snap.Snapshot.Machine.Name)
	}
	if got, _ := os.ReadFile(filepath.Join(w.root, "f1", ".local", "bin", "connex")); string(got) != string(w.binary) {
		t.Fatalf("binaire posé : %q", got)
	}
	if n := w.count(t, "f1", remoteUpload); n != 1 {
		t.Fatalf("%d envois", n)
	}
	st := c.until("mort1 injoignable", 5*time.Second, func(ev Event) bool { return ev.Status != nil && ev.Machine == "mort1" })
	if st.Status.Reachable || st.Status.Reason != "ssh: connect to host mort1 port 22: Connection refused" {
		t.Fatalf("mort1 : %+v", st.Status)
	}
	// Requêtes : routées vers f1, réponse et patch au nom de la configuration.
	if err := src.Send("f1", NewRequest("q1", model.ReqStop, "x")); err != nil {
		t.Fatal(err)
	}
	rep := c.until("réponse", 5*time.Second, func(ev Event) bool { return ev.Reply != nil })
	if rep.Machine != "f1" || rep.Reply.ID != "q1" || rep.Reply.OK || rep.Reply.Error != "« x » est déjà arrêtée" {
		t.Fatalf("réponse : %+v %+v", rep, rep.Reply)
	}
	p := c.until("patch", 5*time.Second, func(ev Event) bool { return ev.Patch != nil })
	if p.Machine != "f1" || p.Patch.Machine != "f1" {
		t.Fatalf("patch : %s / %s", p.Machine, p.Patch.Machine)
	}
	if err := src.Send("mort1", NewRequest("q2", model.ReqStart, "x")); err == nil || !strings.Contains(err.Error(), "injoignable") {
		t.Fatalf("envoi vers une machine injoignable : %v", err)
	}
	if err := src.Send("inconnue", NewRequest("q3", model.ReqStart, "x")); err == nil {
		t.Fatal("envoi vers une machine inconnue accepté")
	}
	// mort1 a été réessayée, mais signalée une seule fois.
	time.Sleep(400 * time.Millisecond)
	if w.count(t, "mort1", remoteProbe) < 2 {
		t.Fatalf("mort1 pas réessayée : %v", w.journal(t))
	}
	pid := readPID(t, filepath.Join(w.root, "f1", "stream.pid"))
	src.Close()
	for range src.Events() {
	}
	if n := c.statuses("mort1", false); n != 1 {
		t.Fatalf("%d machine_status injoignable pour mort1 (%s)", n, c.summary())
	}
	if alive(pid) {
		t.Fatal("le flux de f1 vit encore après Close")
	}
	// Deuxième source : le binaire est à jour, il n'est pas reposé.
	src2, err := Connect(context.Background(), w.options(Target{Name: "f1", SSH: "f1"}))
	if err != nil {
		t.Fatal(err)
	}
	(&collector{t: t, src: src2}).until("relevé f1", 10*time.Second, snapshotOf("f1"))
	src2.Close()
	if n := w.count(t, "f1", remoteUpload); n != 1 {
		t.Fatalf("binaire reposé : %d envois", n)
	}
	// Un binaire différent en place est remplacé.
	os.WriteFile(filepath.Join(w.root, "f1", ".local", "bin", "connex"), []byte("ancien"), 0o755)
	src3, _ := Connect(context.Background(), w.options(Target{Name: "f1", SSH: "f1"}))
	(&collector{t: t, src: src3}).until("relevé f1", 10*time.Second, snapshotOf("f1"))
	src3.Close()
	if got, _ := os.ReadFile(filepath.Join(w.root, "f1", ".local", "bin", "connex")); string(got) != string(w.binary) {
		t.Fatalf("binaire non remplacé : %q", got)
	}
}

// TestConnectReconnectsAfterCut : une coupure donne un seul machine_status
// injoignable avec la raison de ssh, puis le retour et un relevé neuf.
func TestConnectReconnectsAfterCut(t *testing.T) {
	w := newFakeWorld(t)
	src, _ := Connect(context.Background(), w.options(Target{Name: "f1", SSH: "f1"}, Target{Name: "f2", SSH: "f2"}))
	defer src.Close()
	c := &collector{t: t, src: src}
	c.until("relevé f1", 10*time.Second, snapshotOf("f1"))
	c.until("relevé f2", 10*time.Second, snapshotOf("f2"))
	syscall.Kill(readPID(t, filepath.Join(w.root, "f1", "stream.pid")), syscall.SIGTERM)
	st := c.until("coupure", 5*time.Second, func(ev Event) bool { return ev.Status != nil })
	if st.Machine != "f1" || st.Status.Reachable || st.Status.Reason != "client_loop: send disconnect: Broken pipe" {
		t.Fatalf("coupure : %+v", st.Status)
	}
	back := c.until("retour", 5*time.Second, func(ev Event) bool { return ev.Status != nil || ev.Snapshot != nil })
	if back.Status == nil || !back.Status.Reachable || back.Machine != "f1" {
		t.Fatalf("le retour n'est pas annoncé avant le relevé : %s", c.summary())
	}
	c.until("relevé neuf", 5*time.Second, snapshotOf("f1"))
	if c.statuses("f1", false) != 1 || c.statuses("f2", false) != 0 {
		t.Fatalf("événements : %s", c.summary())
	}
	if err := src.Send("f1", NewRequest("q", model.ReqRefresh, "")); err != nil {
		t.Fatalf("envoi après le retour : %v", err)
	}
}

// TestConnectMissingDistNoFlap : sans binaire pour l'architecture de la
// machine, elle est injoignable avec la raison du contrat, signalée une fois,
// et rien n'est envoyé ; les essais suivants ne font pas alterner son état.
func TestConnectMissingDistNoFlap(t *testing.T) {
	w := newFakeWorld(t)
	os.MkdirAll(filepath.Join(w.root, "pi"), 0o755)
	os.WriteFile(filepath.Join(w.root, "pi", "arch"), []byte("aarch64\n"), 0o644)
	src, _ := Connect(context.Background(), w.options(Target{Name: "pi", SSH: "pi"}))
	c := &collector{t: t, src: src}
	st := c.until("pi injoignable", 5*time.Second, func(ev Event) bool { return ev.Status != nil })
	want := "binaire linux-arm64 absent de " + w.dist + " : lance scripts/install.sh"
	if st.Status.Reachable || st.Status.Reason != want {
		t.Fatalf("raison %q, attendu %q", st.Status.Reason, want)
	}
	time.Sleep(500 * time.Millisecond)
	src.Close()
	for ev := range src.Events() {
		c.all = append(c.all, ev)
	}
	if w.count(t, "pi", remoteProbe) < 2 || w.count(t, "pi", remoteUpload) != 0 {
		t.Fatalf("journal : %v", w.journal(t))
	}
	if c.statuses("pi", true) != 0 || c.statuses("pi", false) != 1 {
		t.Fatalf("état alterné : %s", c.summary())
	}
}

// TestConnectRefusesDashDestination : une destination qui commence par « - »
// serait lue par ssh comme une option.
func TestConnectRefusesDashDestination(t *testing.T) {
	_, err := Connect(context.Background(), ConnectOptions{Targets: []Target{{Name: "x", SSH: "-oProxyCommand=touch /tmp/x"}}})
	if err == nil {
		t.Fatal("destination acceptée")
	}
}

// TestConnectUploadFailsAfterCut : après une coupure, si l'envoi d'un nouveau
// binaire échoue, la machine reste injoignable : pas de retour annoncé, pas de
// deuxième machine_status injoignable.
func TestConnectUploadFailsAfterCut(t *testing.T) {
	w := newFakeWorld(t)
	src, _ := Connect(context.Background(), w.options(Target{Name: "f1", SSH: "f1"}))
	c := &collector{t: t, src: src}
	c.until("relevé f1", 10*time.Second, snapshotOf("f1"))
	os.WriteFile(filepath.Join(w.dist, "connex-linux-amd64"), []byte("binaire connex v2"), 0o755)
	os.WriteFile(filepath.Join(w.root, "f1", "disque-plein"), nil, 0o600)
	syscall.Kill(readPID(t, filepath.Join(w.root, "f1", "stream.pid")), syscall.SIGTERM)
	c.until("coupure", 5*time.Second, func(ev Event) bool { return ev.Status != nil })
	for w.count(t, "f1", remoteUpload) < 3 { // trois essais d'envoi (le premier a réussi)
		time.Sleep(50 * time.Millisecond)
	}
	src.Close()
	for ev := range src.Events() {
		c.all = append(c.all, ev)
	}
	if c.statuses("f1", true) != 0 || c.statuses("f1", false) != 1 {
		t.Fatalf("événements : %s", c.summary())
	}
}

// TestConnectBackoffResetsAfterStableSession : après une série d'échecs,
// l'attente a grandi ; une session qui a tenu plus de StableAfter la ramène au
// minimum, et la coupure suivante est réessayée vite.
func TestConnectBackoffResetsAfterStableSession(t *testing.T) {
	w := newFakeWorld(t)
	refuse := filepath.Join(w.root, "f1", "refuse")
	os.MkdirAll(filepath.Dir(refuse), 0o755)
	os.WriteFile(refuse, nil, 0o600)
	o := w.options(Target{Name: "f1", SSH: "f1"})
	o.MinBackoff, o.MaxBackoff, o.StableAfter = 100*time.Millisecond, 3*time.Second, 300*time.Millisecond
	src, _ := Connect(context.Background(), o)
	defer src.Close()
	c := &collector{t: t, src: src}
	for w.count(t, "f1", remoteProbe) < 5 { // attentes 100, 200, 400, 800 ms : la suivante vaut 1,6 s
		time.Sleep(20 * time.Millisecond)
	}
	os.Remove(refuse)
	c.until("premier retour", 10*time.Second, func(ev Event) bool { return ev.Status != nil && ev.Status.Reachable })
	c.until("relevé f1", 10*time.Second, snapshotOf("f1"))
	time.Sleep(500 * time.Millisecond) // la session tient plus de StableAfter
	syscall.Kill(readPID(t, filepath.Join(w.root, "f1", "stream.pid")), syscall.SIGTERM)
	c.until("coupure", 5*time.Second, func(ev Event) bool { return ev.Status != nil && !ev.Status.Reachable })
	t0 := time.Now()
	c.until("retour", 5*time.Second, func(ev Event) bool { return ev.Status != nil && ev.Status.Reachable })
	// Sans remise à zéro, l'attente serait de 3 s.
	if d := time.Since(t0); d > 1500*time.Millisecond {
		t.Fatalf("retour en %s : l'attente n'est pas revenue au minimum", d)
	}
}

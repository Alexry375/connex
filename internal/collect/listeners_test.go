package collect

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeProcAddr(t *testing.T) {
	cases := map[string]string{
		"0100007F":                         "127.0.0.1",
		"00000000":                         "0.0.0.0",
		"48917A64":                         "100.122.145.72",
		"00000000000000000000000001000000": "::1",
		"00000000000000000000000000000000": "::",
		"5C117AFD0000E0A1000000009B9A01B7": "fd7a:115c:a1e0::b701:9a9b",
		"0000000000000000FFFF00000100007F": "::ffff:127.0.0.1",
	}
	for in, want := range cases {
		got, err := decodeProcAddr(in)
		if err != nil || got.String() != want {
			t.Errorf("decodeProcAddr(%s) = %v, %v ; attendu %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0100007", "ZZ00007F", "0100007F00"} {
		if _, err := decodeProcAddr(bad); err == nil {
			t.Errorf("decodeProcAddr(%q) devrait échouer", bad)
		}
	}
}

const procNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F40 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1111 1 0000000000000000 100 0 0 10 0
   1: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2222 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1F40 0100007F:A2C4 01 00000000:00000000 00:00000000 00000000  1000        0 3333 1 0000000000000000 20 4 30 10 -1
   3: 48917A64:9FA6 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 4444 1 0000000000000000 100 0 0 10 0
`

const procNetTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:0438 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 5555 1 0000000000000000 100 0 0 10 0
   1: 0000000000000000FFFF00000100007F:1F41 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 6666 1 0000000000000000 100 0 0 10 0
`

func TestParseProcNet(t *testing.T) {
	socks, err := parseProcNet(strings.NewReader(procNetTCP), "tcp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(socks) != 3 {
		t.Fatalf("%d sockets en écoute, 3 attendus (la connexion établie est ignorée) : %+v", len(socks), socks)
	}
	s := socks[0]
	if s.key() != "tcp4:127.0.0.1:8000" || s.uid != 1000 || s.inode != 1111 {
		t.Errorf("premier socket : %+v, clé %s", s, s.key())
	}
	socks6, err := parseProcNet(strings.NewReader(procNetTCP6), "tcp6")
	if err != nil {
		t.Fatal(err)
	}
	if len(socks6) != 2 || socks6[0].key() != "tcp6:[::1]:1080" || socks6[1].key() != "tcp6:[::ffff:127.0.0.1]:8001" {
		t.Errorf("tcp6 : %+v", socks6)
	}
	if _, err := parseProcNet(strings.NewReader("entete\n 0: XYZ:0016 0:0 0A a b c d 0 0 1\n"), "tcp4"); err == nil {
		t.Error("une adresse illisible doit être signalée")
	}
}

func TestExposure(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1": "loopback", "127.8.9.10": "loopback", "::1": "loopback", "::ffff:127.0.0.1": "loopback",
		"100.64.0.1": "tailnet", "100.127.255.254": "tailnet", "100.122.145.72": "tailnet",
		"fd7a:115c:a1e0::1": "tailnet", "fd7a:115c:a1e0:ffff::9": "tailnet", "::ffff:100.100.1.1": "tailnet",
		"100.63.255.255": "all", "100.128.0.0": "all", "fd7a:115c:a1e1::1": "all",
		"0.0.0.0": "all", "::": "all", "192.168.1.20": "all", "::ffff:0.0.0.0": "all", "2001:db8::1": "all",
	}
	for in, want := range cases {
		if got := Exposure(netip.MustParseAddr(in)); got != want {
			t.Errorf("Exposure(%s) = %s ; attendu %s", in, got, want)
		}
	}
}

// fakeProc fabrique un /proc minimal : net/tcp, net/tcp6 et des processus.
type fakeProc struct {
	t    *testing.T
	root string
}

func newFakeProc(t *testing.T, tcp, tcp6 string) *fakeProc {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "net"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "net", "tcp"), []byte(tcp), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "net", "tcp6"), []byte(tcp6), 0o644))
	return &fakeProc{t: t, root: root}
}

// addProc ajoute un processus : comm, cwd et des descripteurs vers des sockets.
// Il pose aussi une ligne de commande et un environnement pleins de secrets, qui
// ne doivent jamais ressortir.
func (f *fakeProc) addProc(pid int, comm, cwd string, inodes ...uint64) {
	dir := filepath.Join(f.root, strconv.Itoa(pid))
	must(f.t, os.MkdirAll(filepath.Join(dir, "fd"), 0o755))
	must(f.t, os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644))
	must(f.t, os.WriteFile(filepath.Join(dir, "cmdline"), []byte("serve\x00--token=SECRET-CMDLINE"), 0o644))
	must(f.t, os.WriteFile(filepath.Join(dir, "environ"), []byte("API_KEY=SECRET-ENVIRON"), 0o644))
	if cwd != "" {
		must(f.t, os.Symlink(cwd, filepath.Join(dir, "cwd")))
	}
	must(f.t, os.Symlink("/dev/null", filepath.Join(dir, "fd", "0")))
	for i, ino := range inodes {
		must(f.t, os.Symlink(socketLink(ino), filepath.Join(dir, "fd", strconv.Itoa(i+3))))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func testCollector(procRoot, home string, roots []string) *Collector {
	uid := os.Getuid()
	return &Collector{
		procRoot: procRoot, home: home, uid: uid, roots: roots,
		owners: newSocketOwners(procRoot, uid),
		repos:  newRepoTracker(newWalker(home, map[string]bool{}), 8),
		now:    timeNowForTests,
		// Hermétique : aucune interface réelle, jamais de tailscale lancé.
		ifaces:     func() ([]netIface, error) { return nil, nil },
		readFunnel: func(context.Context, string) map[int]bool { return map[int]bool{} },
	}
}

func TestCollectListenersFromProc(t *testing.T) {
	uid := strconv.Itoa(os.Getuid())
	tcp := strings.ReplaceAll(procNetTCP, "  1000 ", " "+uid+" ")
	tcp6 := strings.ReplaceAll(procNetTCP6, "  1000 ", " "+uid+" ")
	fp := newFakeProc(t, tcp, tcp6)

	proj := t.TempDir()
	must(t, os.WriteFile(filepath.Join(proj, "package.json"), []byte("{}"), 0o644))
	srv := filepath.Join(proj, "apps", "api")
	must(t, os.MkdirAll(srv, 0o755))
	fp.addProc(4242, "python3", srv, 1111)
	fp.addProc(4300, "python3", srv, 1111) // enfant qui a hérité du socket
	fp.addProc(5000, "ssh", "/", 5555, 6666)

	c := testCollector(fp.root, t.TempDir(), nil)
	socks, errs := c.readSockets()
	procs, err := listProcs(fp.root)
	must(t, err)
	ls := c.collectListeners(socks, procs, map[string]bool{}, nil)
	if len(errs) != 0 {
		t.Fatalf("erreurs : %+v", errs)
	}
	byKey := map[string]int{}
	for i, l := range ls {
		byKey[l.Key] = i
	}
	if len(ls) != 5 {
		t.Fatalf("%d ports, 5 attendus : %+v", len(ls), ls)
	}
	py := ls[byKey["tcp4:127.0.0.1:8000"]]
	if !py.Mine || py.PID != 4242 || py.Process != "python3" || py.CWD != srv || py.ProjectRoot != proj || py.Exposure != "loopback" {
		t.Errorf("port du témoin : %+v", py)
	}
	ssh := ls[byKey["tcp4:0.0.0.0:22"]]
	if ssh.Mine || ssh.PID != 0 || ssh.Process != "" || ssh.CWD != "" || ssh.UID != 0 || ssh.Exposure != "all" {
		t.Errorf("port d'un autre compte : %+v", ssh)
	}
	if ts := ls[byKey["tcp4:100.122.145.72:40870"]]; ts.Exposure != "tailnet" || ts.Mine {
		t.Errorf("port tailnet : %+v", ts)
	}
	if m := ls[byKey["tcp6:[::ffff:127.0.0.1]:8001"]]; m.Exposure != "loopback" || m.PID != 5000 || m.Process != "ssh" {
		t.Errorf("IPv4 mappée : %+v", m)
	}
	b, _ := json.Marshal(ls)
	for _, secret := range []string{"SECRET-CMDLINE", "SECRET-ENVIRON", "--token"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("%s sort dans le relevé : %s", secret, b)
		}
	}
}

func TestSocketOwnersFollowsFdChanges(t *testing.T) {
	fp := newFakeProc(t, "", "")
	fp.addProc(10, "a", "/", 1111)
	fp.addProc(20, "b", "/", 2222)
	o := newSocketOwners(fp.root, os.Getuid())
	procs, _ := listProcs(fp.root)

	got := o.resolve([]uint64{1111, 2222, 9999}, procs)
	if got[1111].pid != 10 || got[2222].pid != 20 {
		t.Fatalf("premier balayage : %+v", got)
	}
	if _, ok := got[9999]; ok {
		t.Error("un inode sans propriétaire lisible ne doit pas en recevoir un")
	}
	// Le processus 10 ferme son socket et le 20 récupère 1111 : le cache,
	// vérifié par readlink, doit s'en apercevoir.
	must(t, os.Remove(filepath.Join(fp.root, "10", "fd", "3")))
	must(t, os.Symlink(socketLink(1111), filepath.Join(fp.root, "20", "fd", "9")))
	got = o.resolve([]uint64{1111, 2222, 9999}, procs)
	if got[1111].pid != 20 || got[1111].fd != 9 {
		t.Errorf("après changement : %+v", got[1111])
	}
}

// Un serveur qui démarre entre la lecture de /proc/net et celle des processus :
// son socket n'a pas de propriétaire au premier passage, mais le retrouve au
// suivant, sans relire les processus déjà lus.
func TestSocketOwnersAdoptsLateProcess(t *testing.T) {
	fp := newFakeProc(t, "", "")
	fp.addProc(10, "a", "/", 1111)
	o := newSocketOwners(fp.root, os.Getuid())
	stale, _ := listProcs(fp.root)
	fp.addProc(30, "vite", "/", 3333)

	got := o.resolve([]uint64{1111, 3333}, stale)
	if _, ok := got[3333]; ok || got[1111].pid != 10 {
		t.Fatalf("premier passage : %+v", got)
	}
	// Le dossier fd du processus 10 ne se liste plus (ses entrées restent
	// lisibles) : un balayage complet ferait perdre à 1111 son propriétaire.
	// Seul le nouveau venu doit être lu.
	fd10 := filepath.Join(fp.root, "10", "fd")
	must(t, os.Chmod(fd10, 0o100))
	t.Cleanup(func() { os.Chmod(fd10, 0o755) })
	if _, err := os.ReadDir(fd10); err == nil {
		t.Skip("droits sans effet (root ?)")
	}
	procs, _ := listProcs(fp.root)
	got = o.resolve([]uint64{1111, 3333}, procs)
	if got[3333].pid != 30 || got[1111].pid != 10 {
		t.Errorf("second passage : %+v", got)
	}
}

func TestProjectRoot(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	must(t, os.MkdirAll(deep, 0o755))
	must(t, os.WriteFile(filepath.Join(root, "a", "go.mod"), nil, 0o644))
	if got := ProjectRoot(deep, nil); got != filepath.Join(root, "a") {
		t.Errorf("go.mod : %s", got)
	}
	must(t, os.Mkdir(filepath.Join(root, "a", "b", ".git"), 0o755))
	if got := ProjectRoot(deep, nil); got != filepath.Join(root, "a", "b") {
		t.Errorf("le marqueur le plus proche gagne : %s", got)
	}
	for _, m := range []string{"pyproject.toml", "Cargo.toml", "package.json"} {
		d := filepath.Join(root, m+"-proj")
		must(t, os.MkdirAll(d, 0o755))
		must(t, os.WriteFile(filepath.Join(d, m), nil, 0o644))
		if got := ProjectRoot(d, nil); got != d {
			t.Errorf("%s : %s", m, got)
		}
	}
	// Sous un montage réseau, rien n'est examiné.
	if got := ProjectRoot(deep, map[string]bool{filepath.Join(root, "a"): true}); got != "" {
		t.Errorf("montage réseau : %q", got)
	}
	if got := ProjectRoot("relatif/x", nil); got != "" {
		t.Errorf("chemin relatif : %q", got)
	}
}

package collect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

var timeNowForTests = time.Now

func TestMapClaudeStatus(t *testing.T) {
	cases := map[string]string{
		"busy": model.StateWorking, "BUSY ": model.StateWorking,
		"idle":                   model.StateWaiting, // réponse finie : la session attend l'utilisateur
		"waiting":                model.StateWaiting,
		"waiting_for_permission": model.StateWaiting,
		"needs_permission":       model.StateWaiting,
		"awaiting_approval":      model.StateWaiting,
		"shell":                  model.StateUnknown,
		"working":                model.StateUnknown, // non observé : on ne devine pas
		"":                       model.StateUnknown,
	}
	for in, want := range cases {
		if got := MapClaudeStatus(in); got != want {
			t.Errorf("MapClaudeStatus(%q) = %s ; attendu %s", in, got, want)
		}
	}
}

func TestCollectAgents(t *testing.T) {
	fp := newFakeProc(t, "", "")
	home := t.TempDir()
	sessions := filepath.Join(home, ".claude", "sessions")
	must(t, os.MkdirAll(sessions, 0o700))
	write := func(pid, body string) {
		must(t, os.WriteFile(filepath.Join(sessions, pid+".json"), []byte(body), 0o600))
	}
	write("100", `{"pid":100,"sessionId":"s1","cwd":"/x","status":"busy","name":"TITRE-SECRET de la conversation"}`)
	write("102", `{"pid":999,"status":"busy"}`) // fichier d'un ancien processus
	write("104", `{"pid":104,"status":"waiting_for_permission","name":"autre titre"}`)
	write("105", `{"pid":105,"status":{"etrange":true}}`)
	write("106", `pas du json`)
	write("107", `{"pid":107,"status":"idle"}`)
	// Un fichier .key à côté ne doit jamais être ouvert : on le rend illisible.
	must(t, os.WriteFile(filepath.Join(sessions, "100.abc.key"), []byte("CLE"), 0o000))

	work := t.TempDir()
	fp.addProc(100, "claude", work)
	fp.addProc(101, "codex", work)
	fp.addProc(102, "claude", work)
	fp.addProc(103, "bash", work)
	fp.addProc(104, "claude", work)
	fp.addProc(105, "claude", work)
	fp.addProc(106, "claude", work)
	fp.addProc(107, "claude", work)
	fp.addProc(108, "claude-helper", work)

	c := testCollector(fp.root, home, nil)
	procs, _ := listProcs(fp.root)
	agents := c.collectAgents(procs)
	want := map[string]string{
		"claude:100": model.StateWorking, "codex:101": model.StateUnknown, "claude:102": model.StateUnknown,
		"claude:104": model.StateWaiting, "claude:105": model.StateUnknown, "claude:106": model.StateUnknown,
		"claude:107": model.StateWaiting,
	}
	if len(agents) != len(want) {
		t.Fatalf("%d agents, %d attendus : %+v", len(agents), len(want), agents)
	}
	for _, a := range agents {
		if want[a.Key] != a.State || a.CWD != work {
			t.Errorf("%s : état %s, cwd %s", a.Key, a.State, a.CWD)
		}
	}
	b, _ := json.Marshal(agents)
	for _, secret := range []string{"TITRE-SECRET", "autre titre", "SECRET-CMDLINE", "SECRET-ENVIRON"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("%q sort dans le relevé", secret)
		}
	}
}

// statLine fabrique une ligne /proc/<pid>/stat au terminal de contrôle tty.
func statLine(pid int, comm string, tty int64) string {
	return strconv.Itoa(pid) + " (" + comm + ") S 1 " + strconv.Itoa(pid) + " " + strconv.Itoa(pid) + " " +
		strconv.FormatInt(tty, 10) + " -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0\n"
}

// ptsDev est le tty_nr de /dev/pts/n : majeur 136 et suivants, mineur sur 20 bits.
func ptsDev(n int64) int64 {
	major, minor := 136+n>>20, n&0xfffff
	return (minor & 0xff) | major<<8 | (minor&^0xff)<<12
}

func TestPtsFromStat(t *testing.T) {
	cases := []struct {
		stat string
		n    int
		ok   bool
	}{
		{statLine(1, "codex", ptsDev(3)), 3, true},
		{statLine(1, "codex", ptsDev(300)), 300, true},           // mineur au-delà de 255
		{statLine(1, "codex", ptsDev(1<<20+7)), 1<<20 + 7, true}, // majeur 137
		{statLine(1, "un nom ) (bizarre", ptsDev(5)), 5, true},   // parenthèses et espaces dans comm
		{statLine(1, "codex", 0), 0, false},                      // pas de terminal
		{statLine(1, "codex", 4<<8|1), 0, false},                 // console /dev/tty1 : pas un pts
		{"12 (codex) S 1", 0, false},
		{"illisible", 0, false},
	}
	for _, c := range cases {
		n, ok := ptsFromStat(c.stat)
		if ok != c.ok || (ok && n != c.n) {
			t.Errorf("ptsFromStat(%q) = %d, %v ; attendu %d, %v", c.stat, n, ok, c.n, c.ok)
		}
	}
	// Le vrai terminal de ce test, s'il en a un, se relit comme ps le lit.
	if b, err := os.ReadFile("/proc/self/stat"); err == nil {
		if n, ok := ptsFromStat(string(b)); ok {
			if link, err := os.Readlink("/proc/self/fd/0"); err == nil && strings.HasPrefix(link, "/dev/pts/") && link != "/dev/pts/"+strconv.Itoa(n) {
				t.Errorf("tty_nr donne pts/%d, stdin est %s", n, link)
			}
		}
	}
}

// La règle des 15 s, à ses bornes.
func TestTTYStateAt(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for age, want := range map[time.Duration]string{
		0: model.StateWorking, 8 * time.Second: model.StateWorking, 15 * time.Second: model.StateWorking,
		15*time.Second + time.Millisecond: model.StateWaiting, time.Hour: model.StateWaiting,
		-2 * time.Second: model.StateWorking, // horloge qui recule : écrit à l'instant
	} {
		if got := ttyStateAt(now.Add(-age), now); got != want {
			t.Errorf("âge %v : %s ; attendu %s", age, got, want)
		}
	}
}

// Codex, et Claude sans fichier de session lisible : l'état vient de l'heure du
// terminal, par tty_nr ou par un descripteur 0-2. Un fichier de session lisible
// garde le dessus. Le contenu du terminal n'est jamais lu : on le rend illisible.
func TestAgentStateFromTerminal(t *testing.T) {
	fp := newFakeProc(t, "", "")
	home := t.TempDir()
	sessions := filepath.Join(home, ".claude", "sessions")
	must(t, os.MkdirAll(sessions, 0o700))
	must(t, os.WriteFile(filepath.Join(sessions, "205.json"), []byte(`{"pid":205,"status":"busy"}`), 0o600))
	must(t, os.WriteFile(filepath.Join(sessions, "206.json"), []byte(`{"pid":999,"status":"busy"}`), 0o600))
	must(t, os.WriteFile(filepath.Join(sessions, "209.json"), []byte(`{"pid":209}`), 0o600))

	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	dev := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dev, "pts"), 0o755))
	pts := func(n int, age time.Duration) {
		p := filepath.Join(dev, "pts", strconv.Itoa(n))
		must(t, os.WriteFile(p, []byte("CONTENU-DU-TERMINAL"), 0o000))
		must(t, os.Chtimes(p, now.Add(-age), now.Add(-age)))
	}
	pts(1, 3*time.Second)  // écrit à l'instant
	pts(2, 40*time.Second) // silencieux
	pts(3, 16*time.Second) // juste au-delà
	pts(4, 20*time.Second) // silencieux, mais le fichier de session dit busy

	work := t.TempDir()
	add := func(pid int, comm string, tty int64) {
		fp.addProc(pid, comm, work)
		must(t, os.WriteFile(filepath.Join(fp.root, strconv.Itoa(pid), "stat"), []byte(statLine(pid, comm, tty)), 0o644))
	}
	add(201, "codex", ptsDev(1))
	add(202, "codex", ptsDev(2))
	add(203, "codex", 0) // sans terminal, stdin sur /dev/null
	add(204, "claude", ptsDev(3))
	add(205, "claude", ptsDev(4))
	add(206, "claude", ptsDev(2)) // fichier d'un ancien pid : repli sur le terminal
	add(207, "codex", ptsDev(9))  // pts disparu : heure illisible
	add(208, "codex", 0)          // pas de terminal de contrôle, mais stdout sur pts/1
	must(t, os.Symlink("/dev/pts/1", filepath.Join(fp.root, "208", "fd", "1")))
	add(209, "claude", ptsDev(1)) // fichier sans statut : repli sur le terminal

	c := testCollector(fp.root, home, nil)
	c.devRoot = dev
	c.now = func() time.Time { return now }
	procs, _ := listProcs(fp.root)
	want := map[string]string{
		"codex:201": model.StateWorking, "codex:202": model.StateWaiting, "codex:203": model.StateUnknown,
		"claude:204": model.StateWaiting, "claude:205": model.StateWorking, "claude:206": model.StateWaiting,
		"codex:207": model.StateUnknown, "codex:208": model.StateWorking, "claude:209": model.StateWorking,
	}
	got := c.collectAgents(procs)
	if len(got) != len(want) {
		t.Fatalf("%d agents, %d attendus : %+v", len(got), len(want), got)
	}
	for _, a := range got {
		if a.State != want[a.Key] {
			t.Errorf("%s : %s ; attendu %s", a.Key, a.State, want[a.Key])
		}
	}
}

func TestParseTailscale(t *testing.T) {
	out := []byte(`{"Version":"1.90","BackendState":"Running","Self":{"DNSName":"alfred-laptop.tail1234.ts.net.","HostName":"Alfred-Laptop",
		"TailscaleIPs":["fd7a:115c:a1e0::b701:9a9b","100.122.145.72"]},"Peer":{"x":{"DNSName":"vps.tail1234.ts.net."}}}`)
	name, ip, err := parseTailscale(out)
	if err != nil || name != "alfred-laptop" || ip != "100.122.145.72" {
		t.Errorf("parseTailscale = %q, %q, %v", name, ip, err)
	}
	name, ip, err = parseTailscale([]byte(`{"Self":{"DNSName":"","TailscaleIPs":[]}}`))
	if err != nil || name != "" || ip != "" {
		t.Errorf("sans MagicDNS : %q, %q, %v", name, ip, err)
	}
	if _, _, err := parseTailscale([]byte("pas du json")); err == nil {
		t.Error("une sortie illisible doit être signalée")
	}
}

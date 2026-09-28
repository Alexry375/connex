package collect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

var timeNowForTests = time.Now

func TestMapClaudeStatus(t *testing.T) {
	cases := map[string]string{
		"busy": model.StateWorking, "BUSY ": model.StateWorking,
		"idle":                   model.StateIdle,
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
		"claude:107": model.StateIdle,
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

package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

func TestRenderSnapshot(t *testing.T) {
	yes, no := true, false
	s := model.NewSnapshot(model.Machine{Name: "vps-1", OS: "linux", Arch: "amd64", User: "alfred", TailscaleIP: "100.71.85.119"}, time.Now())
	s.Listeners = []model.Listener{
		{Key: "tcp4:0.0.0.0:3000", Port: 3000, Addr: "0.0.0.0", Exposure: "all", Mine: true, PID: 4242, Process: "next-server", CWD: "/home/alfred/x/web", ProjectRoot: "/home/alfred/x"},
		{Key: "tcp4:127.0.0.1:5432", Port: 5432, Addr: "127.0.0.1", Exposure: "loopback", UID: 113},
	}
	s.Repos = []model.Repo{
		{Path: "/home/alfred/x", Branch: "main", Dirty: &yes, Worktrees: []model.Worktree{
			{Path: "/home/alfred/x-a", Branch: "feat/a"}, {Path: "/home/alfred/x/.wt/b", Branch: "feat/b"},
			{Path: "/srv/c", Detached: true}, {Path: "/home/alfred/x-d", Branch: "feat/d"},
		}},
		{Path: "/home/alfred/y", Branch: "dev", Dirty: &no},
		{Path: "/home/alfred/z", Detached: true},
	}
	s.Agents = []model.Agent{{Tool: "claude", PID: 77, CWD: "/home/alfred/x", State: "waiting"}}
	s.Errors = []model.Error{{Source: "repos", Message: "racine absente : /home/alfred/old"}}

	var b bytes.Buffer
	if err := Snapshot(&b, s, "/home/alfred"); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"vps-1 · linux/amd64 · alfred · 100.71.85.119",
		"PORTS (2)", "3000", "all ⚠", "next-server", "~/x",
		"5432", "uid 113",
		"DÉPÔTS (3)", "modifié", "propre", "HEAD détachée",
		"../x-a (feat/a)", ".wt/b (feat/b)", "/srv/c (HEAD détachée)", "+1",
		"AGENTS (1)", "claude", "t’attend",
		"ERREURS (1)", "[repos] racine absente",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("il manque %q dans :\n%s", want, out)
		}
	}
	// Le port d'un autre compte n'a ni pid ni dossier.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "5432") && (strings.Contains(line, "~") || strings.Contains(line, "4242")) {
			t.Errorf("ligne du port d'un autre compte : %q", line)
		}
	}
	if strings.HasPrefix(out, "{") || strings.HasPrefix(out, "[") {
		t.Error("le tableau ne doit pas commencer comme du JSON")
	}
}

func intp(n int) *int { return &n }

// Lot L7 : le programme nommé par son unité ou son exécutable, host et Funnel
// dits en clair, les commits à pousser et en retard, « t’attend ».
func TestRenderL7(t *testing.T) {
	yes, no := true, false
	s := model.NewSnapshot(model.Machine{Name: "laptop", OS: "linux", Arch: "amd64", User: "alfred"}, time.Now())
	s.Listeners = []model.Listener{
		{Key: "a", Port: 3001, Addr: "127.0.0.1", Exposure: "loopback", Mine: true, PID: 10, Process: "MainThread", Exe: "node", Unit: "notifbot-v2-fc558c59.service"},
		{Key: "b", Port: 3002, Addr: "127.0.0.1", Exposure: "loopback", Mine: true, PID: 11, Process: "MainThread", Exe: "node"},
		{Key: "c", Port: 3003, Addr: "127.0.0.1", Exposure: "loopback", Mine: true, PID: 12, Process: "next-server", Exe: "node"},
		{Key: "d", Port: 8080, Addr: "172.17.0.1", Exposure: "host", Mine: true, PID: 13, Process: "python3"},
		{Key: "e", Port: 8443, Addr: "100.122.145.72", Exposure: "all", Funnel: true, Mine: true, PID: 14, Process: "caddy"},
	}
	s.Repos = []model.Repo{
		{Path: "/home/alfred/avance", Branch: "main", Dirty: &no, Upstream: "origin/main", Ahead: intp(2), Behind: intp(0)},
		{Path: "/home/alfred/retard", Branch: "main", Dirty: &yes, Upstream: "origin/main", Ahead: intp(0), Behind: intp(1)},
		{Path: "/home/alfred/seul", Branch: "main", Dirty: &no},
	}
	s.Agents = []model.Agent{
		{Tool: "claude", PID: 77, State: "waiting"}, {Tool: "codex", PID: 78, State: "working"}, {Tool: "codex", PID: 79, State: "unknown"},
	}
	var b bytes.Buffer
	if err := Snapshot(&b, s, "/home/alfred"); err != nil {
		t.Fatal(err)
	}
	line := func(sub string) string {
		for _, l := range strings.Split(b.String(), "\n") {
			if strings.Contains(l, sub) {
				return l
			}
		}
		t.Fatalf("pas de ligne %q dans :\n%s", sub, b.String())
		return ""
	}
	for sub, want := range map[string]string{
		"3001": "notifbot-v2-fc558c59", "3002": "node", "3003": "next-server",
		"8080": "host (cette machine seulement)", "8443": "Internet (Funnel) ⚠",
		"avance": "propre, 2 à pousser", "retard": "modifié, 1 en retard",
		"claude": "t’attend", " 78 ": "travaille", " 79 ": "inconnu",
	} {
		if l := line(sub); !strings.Contains(l, want) {
			t.Errorf("ligne %q : %q attendu dans %q", sub, want, l)
		}
	}
	if l := line("3001"); strings.Contains(l, ".service") || strings.Contains(l, "MainThread") {
		t.Errorf("unité mal nommée : %q", l)
	}
	if l := line("seul"); strings.Contains(l, "pousser") || strings.Contains(l, "retard") {
		t.Errorf("dépôt sans branche suivie : %q", l)
	}
	if !strings.Contains(b.String(), "dernier fetch") {
		t.Error("« en retard » sans dire qu'il date du dernier fetch")
	}
}

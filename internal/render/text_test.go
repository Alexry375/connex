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
		"AGENTS (1)", "claude", "waiting",
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

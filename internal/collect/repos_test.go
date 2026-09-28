package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Alexry375/connex/internal/model"
)

func TestParseStatus(t *testing.T) {
	clean := "# branch.oid 1234\n# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -0\n"
	if st := parseStatus([]byte(clean)); st.branch != "main" || st.dirty || st.detached {
		t.Errorf("propre : %+v", st)
	}
	for name, line := range map[string]string{
		"modifié":   "1 .M N... 100644 100644 100644 aaa bbb lisez-moi.txt",
		"renommé":   "2 R. N... 100644 100644 100644 aaa bbb R100 neuf\tvieux",
		"conflit":   "u UU N... 100644 100644 100644 100644 a b c conflit.txt",
		"non suivi": "? brouillon.txt",
	} {
		if st := parseStatus([]byte(clean + line + "\n")); !st.dirty {
			t.Errorf("%s : dirty attendu", name)
		}
	}
	if st := parseStatus([]byte("# branch.oid (initial)\n# branch.head (detached)\n")); !st.detached || st.branch != "" {
		t.Errorf("HEAD détachée : %+v", st)
	}
}

// Les en-têtes branch.upstream et branch.ab donnent la branche suivie et les
// commits à pousser et en retard ; sans eux, rien (nil).
func TestParseStatusUpstream(t *testing.T) {
	head := "# branch.oid 1234\n# branch.head main\n"
	cases := []struct {
		name, out, upstream string
		ahead, behind       int // -1 : nil
	}{
		{"en avance", head + "# branch.upstream origin/main\n# branch.ab +2 -0\n", "origin/main", 2, 0},
		{"en retard", head + "# branch.upstream origin/dev\n# branch.ab +0 -13\n", "origin/dev", 0, 13},
		{"divergé et modifié", head + "# branch.upstream up/x\n# branch.ab +1 -4\n? neuf.txt\n", "up/x", 1, 4},
		{"branche suivie disparue", head + "# branch.upstream origin/vieux\n", "origin/vieux", -1, -1},
		{"sans branche suivie", head, "", -1, -1},
		{"ab illisible", head + "# branch.upstream origin/main\n# branch.ab 2 0\n", "origin/main", -1, -1},
	}
	for _, c := range cases {
		st := parseStatus([]byte(c.out))
		if st.upstream != c.upstream || !intIs(st.ahead, c.ahead) || !intIs(st.behind, c.behind) {
			t.Errorf("%s : upstream %q, ahead %v, behind %v", c.name, st.upstream, deref(st.ahead), deref(st.behind))
		}
	}
}

func intIs(p *int, want int) bool {
	if want < 0 {
		return p == nil
	}
	return p != nil && *p == want
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestParseWorktrees(t *testing.T) {
	out := `worktree /d/projet
HEAD 1111
branch refs/heads/main

worktree /d/projet-wt
HEAD 2222
branch refs/heads/feat/x

worktree /d/projet-detache
HEAD 3333
detached

worktree /d/disparu
HEAD 4444
branch refs/heads/vieux
prunable gitdir file points to non-existent location

`
	got := parseWorktrees([]byte(out))
	if len(got) != 4 {
		t.Fatalf("%d entrées : %+v", len(got), got)
	}
	if got[1].path != "/d/projet-wt" || got[1].branch != "feat/x" {
		t.Errorf("worktree lié : %+v", got[1])
	}
	if !got[2].detached || !got[3].prunable {
		t.Errorf("détaché / prunable : %+v %+v", got[2], got[3])
	}
}

// gitT lance git pour préparer un dépôt de test (avec --no-optional-locks lui
// aussi, par principe).
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"--no-optional-locks", "-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v : %v\n%s", args, err, out)
	}
}

// newRepo crée un dépôt avec un commit.
func newRepo(t *testing.T, dir string) {
	t.Helper()
	must(t, os.MkdirAll(dir, 0o755))
	gitT(t, dir, "init", "-q")
	must(t, os.WriteFile(filepath.Join(dir, "suivi.txt"), []byte("un\n"), 0o644))
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")
}

func realDir(t *testing.T) string {
	d, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	return d
}

func TestDiscoverAndInspectRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	home := realDir(t)
	root := home // on parcourt ~ : les dossiers d'agents y sont
	main := filepath.Join(root, "projet")
	newRepo(t, main)
	wt := filepath.Join(root, "projet-wt")
	gitT(t, main, "worktree", "add", "-q", wt, "-b", "feat/x")
	newRepo(t, filepath.Join(root, "a", "b", "c", "profond"))        // niveau 4 : trouvé
	newRepo(t, filepath.Join(root, "a", "b", "c", "d", "tropbas"))   // niveau 5 : non
	newRepo(t, filepath.Join(root, "web", "node_modules", "paquet")) // sauté
	newRepo(t, filepath.Join(root, ".venv", "lib"))                  // sauté
	newRepo(t, filepath.Join(main, "imbrique"))                      // dépôt dans un dépôt
	must(t, os.WriteFile(filepath.Join(main, ".git", "info", "exclude"), []byte("imbrique/\n"), 0o644))
	must(t, os.Symlink(main, filepath.Join(root, "lien-vers-projet")))
	newRepo(t, filepath.Join(home, ".claude", "plugins", "x")) // dossier d'agent : jamais ouvert
	newRepo(t, filepath.Join(home, ".codex", "y"))

	tr := newRepoTracker(newWalker(home, map[string]bool{}), 8)
	ctx := context.Background()
	repos, errs := tr.collect(ctx, []string{home, filepath.Join(home, "absente")}, true, nil)

	var paths []string
	for _, r := range repos {
		paths = append(paths, strings.TrimPrefix(r.Path, home+"/"))
	}
	want := "a/b/c/profond projet projet/imbrique"
	if strings.Join(paths, " ") != want {
		t.Errorf("dépôts : %v ; attendu %s", paths, want)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "racine absente") {
		t.Errorf("erreurs : %+v", errs)
	}
	var p model.Repo
	for _, r := range repos {
		if r.Path == main {
			p = r
		}
	}
	if p.Branch != "main" || p.Dirty == nil || *p.Dirty {
		t.Errorf("dépôt propre : %+v", p)
	}
	if len(p.Worktrees) != 1 || p.Worktrees[0].Path != wt || p.Worktrees[0].Branch != "feat/x" {
		t.Errorf("worktrees : %+v", p.Worktrees)
	}

	// Un fichier suivi modifié rend le dépôt dirty ; un fichier non suivi aussi.
	must(t, os.WriteFile(filepath.Join(main, "suivi.txt"), []byte("deux\n"), 0o644))
	repo, _, _ := inspectRepo(ctx, main, nil)
	if repo.Dirty == nil || !*repo.Dirty {
		t.Errorf("après modification : %+v", repo)
	}
	gitT(t, main, "checkout", "-q", "--", "suivi.txt")
	must(t, os.WriteFile(filepath.Join(main, "brouillon.txt"), []byte("x"), 0o644))
	if repo, _, _ := inspectRepo(ctx, main, nil); repo.Dirty == nil || !*repo.Dirty {
		t.Errorf("fichier non suivi : %+v", repo)
	}

	// Deux racines imbriquées : chacune garde ses 4 niveaux, sans doublon.
	cands, _ := tr.walker.discover(ctx, []string{home, filepath.Join(home, "a"), home})
	var got []string
	for _, c := range cands {
		got = append(got, strings.TrimPrefix(c.path, home+"/"))
	}
	if strings.Join(got, " ") != "a/b/c/profond projet projet/imbrique projet-wt a/b/c/d/tropbas" {
		t.Errorf("racines imbriquées : %v", got)
	}

	// Un dossier qui se dit dépôt mais ne l'est pas : erreur, dirty inconnu.
	broken := filepath.Join(root, "casse")
	must(t, os.MkdirAll(filepath.Join(broken, ".git"), 0o755))
	repo, _, errs = inspectRepo(ctx, broken, nil)
	if repo.Dirty != nil || len(errs) != 1 {
		t.Errorf("dépôt cassé : %+v %+v", repo, errs)
	}
}

// Les dossiers d'agents restent fermés quand $HOME, ou ~/.codex lui-même, est
// un lien symbolique : le parcours voit alors leurs chemins résolus.
func TestAgentDirsBehindSymlinks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	base := realDir(t)
	realHome, linkHome := filepath.Join(base, "vrai-home"), filepath.Join(base, "home")
	newRepo(t, filepath.Join(realHome, ".claude", "projects", "p"))
	newRepo(t, filepath.Join(realHome, "ok"))
	must(t, os.Symlink(realHome, linkHome))
	elsewhere := filepath.Join(base, "ailleurs")
	newRepo(t, filepath.Join(elsewhere, "codex", "sessions", "s"))
	newRepo(t, filepath.Join(elsewhere, "autre"))
	must(t, os.Symlink(filepath.Join(elsewhere, "codex"), filepath.Join(realHome, ".codex")))

	w := newWalker(linkHome, map[string]bool{})
	for roots, want := range map[string]string{
		linkHome:  "vrai-home/ok",
		elsewhere: "ailleurs/autre",
	} {
		cands, errs := w.discover(context.Background(), []string{roots})
		var got []string
		for _, c := range cands {
			got = append(got, strings.TrimPrefix(c.path, base+"/"))
		}
		if strings.Join(got, " ") != want || len(errs) != 0 {
			t.Errorf("racine %s : %v %v ; attendu %s", roots, got, errs, want)
		}
	}
	if _, errs := w.discover(context.Background(), []string{filepath.Join(linkHome, ".claude")}); len(errs) != 1 || !strings.Contains(errs[0].Message, "ignorée") {
		t.Errorf("racine dans ~/.claude : %v", errs)
	}
}

// Une racine située sous un montage réseau n'est ni résolue ni parcourue.
func TestRootUnderNetworkMount(t *testing.T) {
	base := realDir(t)
	mnt := filepath.Join(base, "vps")
	must(t, os.MkdirAll(filepath.Join(mnt, "sub", "app", ".git"), 0o755))
	w := newWalker(base, map[string]bool{mnt: true})
	for _, root := range []string{mnt, filepath.Join(mnt, "sub")} {
		cands, errs := w.discover(context.Background(), []string{root})
		if len(cands) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Message, "montage réseau") {
			t.Errorf("racine %s : %v %v", root, cands, errs)
		}
	}
}

// Un ~/.codex ou un ~/.agents qui apparaît en cours de session reste fermé : la
// liste des dossiers interdits est refaite à chaque relevé.
func TestCollectKeepsNewAgentDirsClosed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	base := realDir(t)
	realHome, home, root := filepath.Join(base, "vrai-home"), filepath.Join(base, "home"), filepath.Join(base, "Global")
	newRepo(t, filepath.Join(realHome, "ok"))
	newRepo(t, filepath.Join(root, "app"))
	must(t, os.Symlink(realHome, home))
	c := testCollector(newFakeProc(t, "", "").root, home, []string{home, root})
	c.known = true // pas d'appel à tailscale dans ce test
	paths := func() string {
		var got []string
		for _, r := range c.Collect(context.Background(), true).Repos {
			got = append(got, strings.TrimPrefix(r.Path, base+"/"))
		}
		return strings.Join(got, " ")
	}
	if got := paths(); got != "Global/app vrai-home/ok" {
		t.Fatalf("départ : %s", got)
	}
	newRepo(t, filepath.Join(realHome, ".agents", "a"))
	newRepo(t, filepath.Join(root, "dotfiles", "codex", "sessions", "s"))
	must(t, os.Symlink(filepath.Join(root, "dotfiles", "codex"), filepath.Join(realHome, ".codex")))
	if got := paths(); got != "Global/app vrai-home/ok" {
		t.Errorf("après création de ~/.agents et du lien ~/.codex : %s", got)
	}
}

// Un sous-module est un dépôt à part entière, sans faux worktree dans .git/modules ;
// de même pour un dépôt créé avec --separate-git-dir. Un worktree verrouillé
// dont le dossier a disparu reste listé, comme le fait git.
func TestSubmoduleHasNoFakeWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	root := realDir(t)
	sub, main := filepath.Join(root, "lib"), filepath.Join(root, "app")
	newRepo(t, sub)
	newRepo(t, main)
	gitT(t, main, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "vendor/lib")
	gitT(t, main, "commit", "-q", "-m", "sous-module")
	// Dépôt à gitdir séparé, rangé dans son dossier de travail : git annonce
	// le gitdir comme worktree principal.
	sep := filepath.Join(root, "app2")
	must(t, os.MkdirAll(sep, 0o755))
	gitT(t, sep, "init", "-q", "--separate-git-dir", filepath.Join(sep, ".bare"))
	must(t, os.WriteFile(filepath.Join(sep, ".bare", "info", "exclude"), []byte(".bare/\n"), 0o644))
	gitT(t, sep, "commit", "-q", "--allow-empty", "-m", "init")
	// Worktree verrouillé dont le dossier a disparu : git le liste encore.
	gone := filepath.Join(root, "lib-verrou")
	gitT(t, sub, "worktree", "add", "-q", gone, "-b", "verrou")
	gitT(t, sub, "worktree", "lock", gone)
	must(t, os.RemoveAll(gone))

	repos, errs := newRepoTracker(newWalker(root, map[string]bool{}), 8).collect(context.Background(), []string{root}, true, nil)
	var got []string
	for _, r := range repos {
		got = append(got, fmt.Sprintf("%s:%d", strings.TrimPrefix(r.Path, root+"/"), len(r.Worktrees)))
		if r.Dirty == nil || *r.Dirty {
			t.Errorf("%s devrait être propre : %+v", r.Path, r)
		}
	}
	if strings.Join(got, " ") != "app:0 app/vendor/lib:0 app2:0 lib:1" || len(errs) != 0 {
		t.Errorf("dépôts : %v %v", got, errs)
	}
}

// core.fsmonitor, réglé dans un dépôt, ferait lancer un programme par git status.
func TestGitNeverRunsFsmonitor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	root := realDir(t)
	repo, marker := filepath.Join(root, "r"), filepath.Join(root, "HOOK-LANCE")
	newRepo(t, repo)
	hook := filepath.Join(root, "hook.sh")
	must(t, os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755))
	gitT(t, repo, "config", "core.fsmonitor", hook)
	must(t, os.WriteFile(filepath.Join(repo, "suivi.txt"), []byte("deux\n"), 0o644))

	r, _, errs := inspectRepo(context.Background(), repo, nil)
	if r.Dirty == nil || !*r.Dirty || len(errs) != 0 {
		t.Errorf("état : %+v %v", r, errs)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("git a lancé le programme de core.fsmonitor")
	}
}

func TestRepoTrackerRefreshesInSlices(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	home := realDir(t)
	var dirs []string
	for _, n := range []string{"r1", "r2", "r3", "r4", "r5", "r6", "r7"} {
		d := filepath.Join(home, n)
		newRepo(t, d)
		dirs = append(dirs, d)
	}
	tr := newRepoTracker(newWalker(home, map[string]bool{}), 2)
	ctx := context.Background()
	tr.collect(ctx, []string{home}, true, nil)
	for _, d := range dirs {
		must(t, os.WriteFile(filepath.Join(d, "suivi.txt"), []byte("modifié\n"), 0o644))
	}
	hot := []string{filepath.Join(dirs[6], "src")} // un agent travaille dans r7

	countDirty := func(repos []model.Repo) (n int, r7 bool) {
		for _, r := range repos {
			if r.Dirty != nil && *r.Dirty {
				n++
				if r.Path == dirs[6] {
					r7 = true
				}
			}
		}
		return n, r7
	}
	repos, _ := tr.collect(ctx, []string{home}, false, hot)
	if n, r7 := countDirty(repos); n != 3 || !r7 {
		t.Errorf("1er passage partiel : %d dépôts relus modifiés (3 attendus : 2 + celui de l'agent), r7=%v", n, r7)
	}
	repos, _ = tr.collect(ctx, []string{home}, false, hot)
	if n, _ := countDirty(repos); n != 5 {
		t.Errorf("2e passage : %d modifiés, 5 attendus", n)
	}
	repos, _ = tr.collect(ctx, []string{home}, false, nil)
	if n, _ := countDirty(repos); n != 7 {
		t.Errorf("3e passage : %d modifiés, 7 attendus", n)
	}
	// Un nouveau dépôt est relu tout de suite, budget ou pas.
	newRepo(t, filepath.Join(home, "r8"))
	repos, _ = tr.collect(ctx, []string{home}, false, nil)
	if len(repos) != 8 || repos[7].Dirty == nil || *repos[7].Dirty {
		t.Errorf("nouveau dépôt : %+v", repos[len(repos)-1])
	}
	// Un worktree tout neuf n'est jamais compté comme un dépôt de plus.
	gitT(t, dirs[0], "worktree", "add", "-q", filepath.Join(home, "r1-wt"), "-b", "wt")
	repos, _ = tr.collect(ctx, []string{home}, false, nil)
	if len(repos) != 8 || len(repos[0].Worktrees) != 1 {
		t.Errorf("worktree neuf : %d dépôts, worktrees de r1 %+v", len(repos), repos[0].Worktrees)
	}
	// Un refresh complet relit tout.
	for _, d := range dirs {
		gitT(t, d, "checkout", "-q", "--", "suivi.txt")
	}
	repos, _ = tr.collect(ctx, []string{home}, true, nil)
	if n, _ := countDirty(repos); n != 0 {
		t.Errorf("après refresh complet : %d modifiés", n)
	}
}

func TestNetworkMounts(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "self"), 0o755))
	mi := `22 1 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw
100 22 0:50 / /home/alfred/vps rw,nosuid shared:60 - fuse.sshfs alfred@100.71.85.119:/ rw
101 22 0:51 / /home/alfred/One\040Drive rw shared:61 - fuse.rclone onedrive: rw
102 22 0:52 / /mnt/nas rw shared:62 - nfs4 nas:/export rw
103 22 0:53 / /mnt/win rw shared:63 - cifs //pc/c rw
104 22 0:54 / /tmp rw shared:64 - tmpfs tmpfs rw
`
	must(t, os.WriteFile(filepath.Join(root, "self", "mountinfo"), []byte(mi), 0o644))
	got := networkMounts(root)
	for _, p := range []string{"/home/alfred/vps", "/home/alfred/One Drive", "/mnt/nas", "/mnt/win"} {
		if !got[p] {
			t.Errorf("%s devrait être sauté", p)
		}
	}
	if got["/"] || got["/tmp"] || len(got) != 4 {
		t.Errorf("montages locaux sautés à tort : %v", got)
	}
	// Le parcours n'entre pas dans un montage sauté.
	home := realDir(t)
	newRepo(t, filepath.Join(home, "monte", "depot"))
	newRepo(t, filepath.Join(home, "local"))
	w := newWalker(home, map[string]bool{filepath.Join(home, "monte"): true})
	cands, _ := w.discover(context.Background(), []string{home})
	if len(cands) != 1 || cands[0].path != filepath.Join(home, "local") {
		t.Errorf("candidats : %+v", cands)
	}
}

// Sur de vrais dépôts : en avance, en retard, sans branche suivie, tête
// détachée. Le dépôt distant devient injoignable, et tout appel à ssh est
// noté : connex ne doit jamais le contacter, ni toucher à FETCH_HEAD.
func TestInspectRepoAheadBehindOffline(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	d := realDir(t)
	remote := filepath.Join(d, "distant.git")
	gitT(t, d, "init", "-q", "--bare", remote)
	gitT(t, d, "clone", "-q", remote, "avance")
	av := filepath.Join(d, "avance")
	commit := func(dir, name string) {
		must(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644))
		gitT(t, dir, "add", name)
		gitT(t, dir, "commit", "-q", "-m", name)
	}
	commit(av, "un")
	gitT(t, av, "push", "-q", "origin", "HEAD:main")
	gitT(t, av, "branch", "-q", "-u", "origin/main")
	gitT(t, d, "clone", "-q", remote, "retard")
	gitT(t, d, "clone", "-q", remote, "autre")
	commit(filepath.Join(d, "autre"), "deux")
	gitT(t, filepath.Join(d, "autre"), "push", "-q", "origin", "HEAD:main")
	gitT(t, filepath.Join(d, "retard"), "fetch", "-q")
	commit(av, "trois")
	commit(av, "quatre")
	commit(av, "cinq")
	newRepo(t, filepath.Join(d, "seul"))
	gitT(t, d, "clone", "-q", remote, "detache")
	gitT(t, filepath.Join(d, "detache"), "checkout", "-q", "--detach")

	log := filepath.Join(d, "ssh.log")
	fake := filepath.Join(d, "faux-ssh")
	must(t, os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\" >> "+log+"\nexit 1\n"), 0o755))
	t.Setenv("GIT_SSH_COMMAND", fake)
	t.Setenv("GIT_SSH", fake)
	for _, r := range []string{"avance", "retard", "detache"} {
		gitT(t, filepath.Join(d, r), "remote", "set-url", "origin", "ssh://git@depot.invalid/x.git")
	}
	fetchHead := filepath.Join(d, "retard", ".git", "FETCH_HEAD")
	before, err := os.Stat(fetchHead)
	must(t, err)

	cases := []struct {
		dir, upstream string
		ahead, behind int
	}{
		{"avance", "origin/main", 3, 0},
		{"retard", "origin/main", 0, 1},
		{"seul", "", -1, -1},
		{"detache", "", -1, -1},
	}
	for _, c := range cases {
		r, _, errs := inspectRepo(context.Background(), filepath.Join(d, c.dir), nil)
		if len(errs) > 0 {
			t.Fatalf("%s : %v", c.dir, errs)
		}
		if r.Upstream != c.upstream || !intIs(r.Ahead, c.ahead) || !intIs(r.Behind, c.behind) {
			t.Errorf("%s : upstream %q, ahead %v, behind %v", c.dir, r.Upstream, deref(r.Ahead), deref(r.Behind))
		}
		b, _ := json.Marshal(r)
		if c.ahead < 0 && (!strings.Contains(string(b), `"ahead":null`) || strings.Contains(string(b), `"upstream"`)) {
			t.Errorf("%s : JSON %s", c.dir, b)
		}
	}
	if b, err := os.ReadFile(log); err == nil {
		t.Errorf("le dépôt distant a été contacté : %s", b)
	}
	if after, err := os.Stat(fetchHead); err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Error("FETCH_HEAD a changé : un fetch a eu lieu")
	}
}

package collect

import (
	"context"
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
	repo, _, _ := inspectRepo(ctx, main)
	if repo.Dirty == nil || !*repo.Dirty {
		t.Errorf("après modification : %+v", repo)
	}
	gitT(t, main, "checkout", "-q", "--", "suivi.txt")
	must(t, os.WriteFile(filepath.Join(main, "brouillon.txt"), []byte("x"), 0o644))
	if repo, _, _ := inspectRepo(ctx, main); repo.Dirty == nil || !*repo.Dirty {
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
	repo, _, errs = inspectRepo(ctx, broken)
	if repo.Dirty != nil || len(errs) != 1 {
		t.Errorf("dépôt cassé : %+v %+v", repo, errs)
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

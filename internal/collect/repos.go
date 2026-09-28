package collect

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// maxDepth est la profondeur de recherche des dépôts : la racine est au niveau 0.
const maxDepth = 4

// skipNames sont les dossiers jamais parcourus.
var skipNames = map[string]bool{"node_modules": true, ".venv": true, ".cache": true, ".git": true}

// candidate est un dossier qui contient une entrée .git.
type candidate struct {
	path    string
	gitFile bool // .git est un fichier : worktree lié ou sous-module
}

// walker parcourt les racines à la recherche de dépôts.
type walker struct {
	forbidden []string        // dossiers d'agents : jamais ouverts
	mounts    map[string]bool // montages réseau ou FUSE : jamais parcourus
}

// newWalker prépare un parcours. Le parcours compare des chemins résolus : si
// $HOME ou ~/.claude est un lien, on interdit aussi sa cible. Collect en refait
// un à chaque relevé, pour suivre un dossier créé, ou changé en lien, en cours
// de session.
func newWalker(home string, mounts map[string]bool) *walker {
	w := &walker{mounts: mounts}
	realHome := canonical(home)
	seen := map[string]bool{}
	for _, name := range []string{".claude", ".codex", ".agents"} {
		for _, p := range []string{filepath.Join(home, name), filepath.Join(realHome, name), canonical(filepath.Join(home, name))} {
			if !seen[p] {
				seen[p] = true
				w.forbidden = append(w.forbidden, p)
			}
		}
	}
	return w
}

func (w *walker) skipDir(path string) bool {
	if w.mounts[path] {
		return true
	}
	for _, f := range w.forbidden {
		if path == f || strings.HasPrefix(path, f+"/") {
			return true
		}
	}
	return false
}

// discover renvoie les dossiers qui contiennent .git sous les racines, jusqu'à
// maxDepth niveaux, sans suivre les liens symboliques.
func (w *walker) discover(ctx context.Context, roots []string) ([]candidate, []model.Error) {
	var out []candidate
	var errs []model.Error
	// seen retient la plus faible profondeur de visite de chaque dossier : deux
	// racines imbriquées ne font pas deux fois le travail, mais la plus profonde
	// garde ses 4 niveaux.
	seen := map[string]int{}
	for _, root := range roots {
		// Sous un montage réseau, même résoudre la racine peut bloquer.
		if onMount(root, w.mounts) {
			errs = append(errs, model.Error{Source: "repos", Message: "racine ignorée (dossier d'agent ou montage réseau) : " + root})
			continue
		}
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, model.Error{Source: "repos", Message: "racine absente : " + root})
			} else {
				errs = append(errs, model.Error{Source: "repos", Message: "racine illisible : " + err.Error()})
			}
			continue
		}
		if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
			errs = append(errs, model.Error{Source: "repos", Message: "racine qui n'est pas un dossier : " + root})
			continue
		}
		if w.skipDir(real) || onMount(real, w.mounts) {
			errs = append(errs, model.Error{Source: "repos", Message: "racine ignorée (dossier d'agent ou montage réseau) : " + root})
			continue
		}
		w.walk(ctx, real, 0, seen, &out, &errs)
	}
	uniq := map[string]bool{}
	cands := out[:0]
	for _, c := range out {
		if !uniq[c.path] {
			uniq[c.path] = true
			cands = append(cands, c)
		}
	}
	return cands, errs
}

func (w *walker) walk(ctx context.Context, dir string, depth int, seen map[string]int, out *[]candidate, errs *[]model.Error) {
	if d, ok := seen[dir]; ctx.Err() != nil || (ok && d <= depth) {
		return
	}
	seen[dir] = depth
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			*errs = append(*errs, model.Error{Source: "repos", Message: "permission refusée : " + dir})
		}
		return
	}
	for _, e := range ents {
		if e.Name() == ".git" {
			*out = append(*out, candidate{path: dir, gitFile: !e.IsDir()})
			break
		}
	}
	if depth >= maxDepth {
		return
	}
	for _, e := range ents {
		if !e.IsDir() || skipNames[e.Name()] { // IsDir est faux pour un lien symbolique
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if w.skipDir(sub) {
			continue
		}
		w.walk(ctx, sub, depth+1, seen, out, errs)
	}
}

// networkMounts lit /proc/self/mountinfo et renvoie les points de montage FUSE,
// NFS ou CIFS : un sshfs vers une autre machine n'est pas la machine locale, et
// un montage mort bloquerait le parcours.
func networkMounts(procRoot string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(procRoot, "self", "mountinfo"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		pre, post, ok := strings.Cut(line, " - ")
		f, g := strings.Fields(pre), strings.Fields(post)
		if !ok || len(f) < 5 || len(g) < 1 {
			continue
		}
		fstype := g[0]
		if fstype == "fuse" || strings.HasPrefix(fstype, "fuse.") || strings.HasPrefix(fstype, "nfs") ||
			fstype == "cifs" || strings.HasPrefix(fstype, "smb") {
			out[unescapeMount(f[4])] = true
		}
	}
	return out
}

// unescapeMount décode les \040 (espace) et autres octets en octal de mountinfo.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, ok := octal3(s[i+1 : i+4]); ok {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func octal3(s string) (byte, bool) {
	if len(s) != 3 {
		return 0, false
	}
	v := 0
	for _, c := range []byte(s) {
		if c < '0' || c > '7' {
			return 0, false
		}
		v = v*8 + int(c-'0')
	}
	if v > 255 {
		return 0, false
	}
	return byte(v), true
}

// gitTimeout borne chaque appel à git.
const gitTimeout = 10 * time.Second

// git lance `git --no-optional-locks -C dir args…` et renvoie sa sortie. Toute
// lecture d'état d'un dépôt passe par ici : jamais de verrou optionnel, donc
// jamais d'écriture de l'index. core.fsmonitor est coupé : réglé dans un dépôt,
// il ferait lancer par `git status` un programme, ou un démon qui écoute sur un
// socket.
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-C", dir}, args...)...)
	cmd.Env = gitEnv()
	cmd.WaitDelay = 200 * time.Millisecond // un git tué ne retient pas l'arrêt
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return stdout.Bytes(), nil
}

// gitEnv est l'environnement des appels à git : sortie non traduite, jamais de
// question interactive, et aucune variable GIT_DIR héritée qui ferait lire un
// autre dépôt que celui demandé.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY",
			"LC_ALL", "LANG", "LANGUAGE":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
}

// statusInfo est ce que `git status --porcelain=v2 --branch` dit d'un dépôt.
type statusInfo struct {
	branch   string
	detached bool
	dirty    bool
	upstream string // branche distante suivie, vide sans elle
	// ahead et behind viennent de « # branch.ab +A -B » : absents sans branche
	// suivie, ou si elle a disparu du dépôt local depuis le dernier fetch.
	ahead, behind *int
}

// parseStatus lit la sortie de `git status --porcelain=v2 --branch`. Toute ligne
// qui n'est pas un en-tête « # » est un changement : modifié, indexé, en conflit
// ou non suivi. Le dépôt est alors dirty.
func parseStatus(out []byte) statusInfo {
	var st statusInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if v, ok := strings.CutPrefix(line, "# branch.upstream "); ok {
			st.upstream = v
			continue
		}
		if v, ok := strings.CutPrefix(line, "# branch.ab "); ok {
			st.ahead, st.behind = parseAB(v)
			continue
		}
		if v, ok := strings.CutPrefix(line, "# branch.head "); ok {
			if v == "(detached)" {
				st.detached = true
			} else {
				st.branch = v
			}
			continue
		}
		if !strings.HasPrefix(line, "#") {
			st.dirty = true
		}
	}
	return st
}

// parseAB lit « +A -B » ; nil, nil si la forme n'est pas celle-là.
func parseAB(v string) (ahead, behind *int) {
	f := strings.Fields(v)
	if len(f) != 2 || !strings.HasPrefix(f[0], "+") || !strings.HasPrefix(f[1], "-") {
		return nil, nil
	}
	a, err1 := strconv.Atoi(f[0][1:])
	b, err2 := strconv.Atoi(f[1][1:])
	if err1 != nil || err2 != nil || a < 0 || b < 0 {
		return nil, nil
	}
	return &a, &b
}

// worktreeEntry est une entrée de `git worktree list --porcelain`.
type worktreeEntry struct {
	path     string
	branch   string
	detached bool
	bare     bool
	prunable bool
}

func parseWorktrees(out []byte) []worktreeEntry {
	var list []worktreeEntry
	var cur *worktreeEntry
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, worktreeEntry{path: strings.TrimPrefix(line, "worktree ")})
			cur = &list[len(list)-1]
		case cur == nil:
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.detached = true
		case line == "bare":
			cur.bare = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			cur.prunable = true
		}
	}
	return list
}

// canonical résout les liens d'un chemin quand c'est possible.
func canonical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// inspectRepo interroge git sur un dépôt : branche et état par `status`, autres
// worktrees par `worktree list`. listed reçoit tous les dossiers de worktree
// connus de ce dépôt, lui compris. Les chemins situés sous un montage réseau
// sont gardés tels que git les donne, sans y toucher.
func inspectRepo(ctx context.Context, path string, mounts map[string]bool) (repo model.Repo, listed []string, errs []model.Error) {
	repo = model.Repo{Key: path, Path: path, Worktrees: []model.Worktree{}}
	if out, err := git(ctx, path, "status", "--porcelain=v2", "--branch"); err != nil {
		errs = append(errs, model.Error{Source: "repos", Message: path + " : " + err.Error()})
	} else {
		st := parseStatus(out)
		dirty := st.dirty
		repo.Branch, repo.Detached, repo.Dirty = st.branch, st.detached, &dirty
		// Tout vient de l'état local : la branche distante telle que le dernier
		// fetch l'a laissée. connex ne contacte jamais le dépôt distant.
		if !st.detached {
			repo.Upstream, repo.Ahead, repo.Behind = st.upstream, st.ahead, st.behind
		}
	}
	out, err := git(ctx, path, "worktree", "list", "--porcelain")
	if err != nil {
		e := model.Error{Source: "repos", Message: path + " : " + err.Error()}
		if len(errs) == 0 || errs[0] != e { // un dépôt cassé fait échouer les deux appels
			errs = append(errs, e)
		}
		return repo, nil, errs
	}
	self := canonical(path)
	for _, wt := range parseWorktrees(out) {
		p := filepath.Clean(wt.path)
		if !onMount(p, mounts) {
			p = canonical(p)
		}
		listed = append(listed, p)
		if p == self || wt.bare || wt.prunable || !isWorkDir(p, mounts) {
			continue
		}
		repo.Worktrees = append(repo.Worktrees, model.Worktree{Path: p, Branch: wt.branch, Detached: wt.detached})
	}
	return repo, listed, errs
}

// isWorkDir dit si un worktree annoncé par git en est bien un. Un dossier qui
// existe sans entrée .git est un gitdir : c'est ainsi que `worktree list`
// désigne le worktree principal d'un sous-module (.git/modules/x) ou d'un dépôt
// créé avec --separate-git-dir. Un dossier absent reste listé : git ne le garde
// que verrouillé (sinon il est prunable). Sous un montage réseau, on ne va pas
// voir : on croit git.
func isWorkDir(p string, mounts map[string]bool) bool {
	if onMount(p, mounts) {
		return true
	}
	if _, err := os.Lstat(p); err != nil {
		return true
	}
	_, err := os.Lstat(filepath.Join(p, ".git"))
	return err == nil
}

// repoState est le dernier état connu d'un dépôt, pour le rafraîchissement par tranches.
type repoState struct {
	repo    model.Repo
	listed  []string
	errs    []model.Error
	checked int64 // numéro du passage qui l'a interrogé
}

// repoTracker garde l'état des dépôts entre deux passages de stream. À chaque
// passage, les racines sont reparcourues ; git n'interroge que les dépôts
// nouveaux, ceux où travaille un agent ou tourne un port, et une tranche des
// autres, du plus ancien au plus récent. Un passage complet les interroge tous.
type repoTracker struct {
	walker *walker
	budget int // dépôts interrogés par passage, en plus des prioritaires
	pass   int64
	state  map[string]*repoState
}

func newRepoTracker(w *walker, budget int) *repoTracker {
	return &repoTracker{walker: w, budget: budget, state: map[string]*repoState{}}
}

// collect relève les dépôts. hot contient des dossiers actifs (cwd d'agents,
// racines de projets des ports) : un dépôt qui en contient un est toujours relu.
func (t *repoTracker) collect(ctx context.Context, roots []string, full bool, hot []string) ([]model.Repo, []model.Error) {
	t.pass++
	cands, errs := t.walker.discover(ctx, roots)
	var mains, files []candidate
	for _, c := range cands {
		if c.gitFile {
			files = append(files, c)
		} else {
			mains = append(mains, c)
		}
	}

	// Un dossier à .git fichier inconnu est peut-être un worktree tout neuf :
	// on relit alors tous les dépôts principaux pour savoir à qui il appartient.
	forceMains := full
	for _, c := range files {
		if _, ok := t.state[c.path]; !ok && !t.listedByMain(mains, c.path) {
			forceMains = true
			break
		}
	}
	t.refresh(ctx, t.pick(mains, forceMains, hot))

	listed := map[string]bool{}
	for _, c := range mains {
		for _, p := range t.state[c.path].listedOrEmpty() {
			listed[p] = true
		}
	}
	var standalone []candidate
	for _, c := range files {
		if !listed[canonical(c.path)] {
			standalone = append(standalone, c)
		}
	}
	t.refresh(ctx, t.pick(standalone, full, hot))

	present := map[string]bool{}
	repos := []model.Repo{}
	for _, c := range append(mains, standalone...) {
		st := t.state[c.path]
		if st == nil { // contexte annulé avant l'interrogation
			continue
		}
		present[c.path] = true
		repos = append(repos, st.repo)
		errs = append(errs, st.errs...)
	}
	for p := range t.state {
		if !present[p] {
			delete(t.state, p)
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Path < repos[j].Path })
	return repos, errs
}

func (s *repoState) listedOrEmpty() []string {
	if s == nil {
		return nil
	}
	return s.listed
}

func (t *repoTracker) listedByMain(mains []candidate, path string) bool {
	p := canonical(path)
	for _, c := range mains {
		for _, l := range t.state[c.path].listedOrEmpty() {
			if l == p {
				return true
			}
		}
	}
	return false
}

// pick choisit les dépôts à interroger ce passage.
func (t *repoTracker) pick(cands []candidate, all bool, hot []string) []string {
	var out, rest []string
	for _, c := range cands {
		st := t.state[c.path]
		if all || st == nil || containsAny(c.path, hot) {
			out = append(out, c.path)
		} else {
			rest = append(rest, c.path)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return t.state[rest[i]].checked < t.state[rest[j]].checked })
	if len(rest) > t.budget {
		rest = rest[:t.budget]
	}
	return append(out, rest...)
}

// containsAny dit si l'un des dossiers de hot est dir ou se trouve sous dir.
func containsAny(dir string, hot []string) bool {
	for _, h := range hot {
		if h == dir || strings.HasPrefix(h, dir+"/") {
			return true
		}
	}
	return false
}

// gitWorkers est le nombre d'appels à git menés en parallèle.
const gitWorkers = 8

func (t *repoTracker) refresh(ctx context.Context, paths []string) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, gitWorkers)
	for _, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			repo, listed, errs := inspectRepo(ctx, p, t.walker.mounts)
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			t.state[p] = &repoState{repo: repo, listed: listed, errs: errs, checked: t.pass}
			mu.Unlock()
		}()
	}
	wg.Wait()
}

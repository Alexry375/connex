package ui

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// Un petit monde écrit à la main pour les tests : deux machines, les cas que
// personne ne dessine (deux programmes sur un port, un port d'un autre compte,
// une appli arrêtée dont le port est pris, un chemin très long).

var t0 = time.Date(2026, 9, 27, 21, 40, 0, 0, time.UTC)

const longDir = "/home/alfred/Global/Agents_Projects/un-projet-au-nom-vraiment-très-long/sous-dossier/encore-un-sous-dossier/et-le-dernier-dossier-qui-est-long-lui-aussi"

func laptopSnap(at time.Time) model.Snapshot {
	s := model.NewSnapshot(model.Machine{Name: "laptop", OS: "linux", Arch: "amd64", User: "alfred"}, at)
	s.Listeners = []model.Listener{
		{Key: "tcp4:127.0.0.1:5173", Port: 5173, Addr: "127.0.0.1", Exposure: "loopback", Mine: true, UID: 1000, PID: 101, Process: "node", CWD: "/home/alfred/p/clepsydre", ProjectRoot: "/home/alfred/p/clepsydre"},
		{Key: "tcp6:[::1]:5173", Port: 5173, Addr: "::1", Exposure: "loopback", Mine: true, UID: 1000, PID: 102, Process: "node", CWD: "/home/alfred/p/nuage/apps/web", ProjectRoot: "/home/alfred/p/nuage"},
		{Key: "tcp4:0.0.0.0:22", Port: 22, Addr: "0.0.0.0", Exposure: "all", Mine: false, UID: 0},
		{Key: "tcp4:127.0.0.1:5000", Port: 5000, Addr: "127.0.0.1", Exposure: "loopback", Mine: true, UID: 1000, PID: 103, Process: "python3", CWD: "/home/alfred/p/ml-notebooks", ProjectRoot: "/home/alfred/p/ml-notebooks"},
		{Key: "tcp4:100.64.0.1:8384", Port: 8384, Addr: "100.64.0.1", Exposure: "tailnet", Mine: true, UID: 1000, PID: 104, Process: "syncthing", CWD: longDir},
	}
	dirty := true
	s.Repos = []model.Repo{
		{Key: "/home/alfred/p/nuage", Path: "/home/alfred/p/nuage", Branch: "main", Dirty: &dirty, Worktrees: []model.Worktree{}},
		{Key: "/home/alfred/p/planetaire", Path: "/home/alfred/p/planetaire", Branch: "dev", Worktrees: []model.Worktree{}},
	}
	s.Agents = []model.Agent{
		{Key: "claude:201", Tool: "claude", PID: 201, CWD: "/home/alfred/p/nuage", State: model.StateWaiting},
		{Key: "codex:202", Tool: "codex", PID: 202, CWD: "/home/alfred/p/planetaire", State: model.StateWorking},
	}
	s.Apps = []model.App{
		{Key: "planetaire", Name: "planetaire", Project: "/home/alfred/p/planetaire", CWD: "/home/alfred/p/planetaire", Port: 5000, Unit: "connex-planetaire.service", State: model.AppStopped, View: model.ViewRemote},
	}
	return s
}

func vpsSnap(at time.Time) model.Snapshot {
	s := model.NewSnapshot(model.Machine{Name: "vps", OS: "linux", Arch: "amd64", User: "alfred"}, at)
	s.Listeners = []model.Listener{
		{Key: "tcp4:0.0.0.0:3000", Port: 3000, Addr: "0.0.0.0", Exposure: "all", Mine: true, UID: 1000, PID: 301, Process: "node", CWD: "/home/alfred/p/site", ProjectRoot: "/home/alfred/p/site"},
	}
	s.Apps = []model.App{
		{Key: "site", Name: "site", Project: "/home/alfred/p/site", CWD: "/home/alfred/p/site", Port: 3000, Unit: "connex-site.service", State: model.AppRunning,
			Health: model.AppHealth{PIDAlive: true, PortListening: true, HTTP: 200}, View: model.ViewRemote},
	}
	return s
}

// fakeSrc retient les requêtes envoyées.
type fakeSrc struct {
	sent []model.Request
	to   []string
	err  error
}

func (f *fakeSrc) Send(machine string, r model.Request) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, r)
	f.to = append(f.to, machine)
	return nil
}

var errRefused = errors.New("la file des requêtes est pleine")

// newTestApp ouvre l'interface sur le petit monde, ouverture passée.
func newTestApp(t *testing.T, w, h int) (*App, *fakeSrc) {
	t.Helper()
	src := &fakeSrc{}
	a := newApp(src, t0)
	a.resize(w, h)
	l, v := laptopSnap(t0), vpsSnap(t0)
	a.event(source.Event{Machine: "laptop", Snapshot: &l}, t0)
	a.event(source.Event{Machine: "vps", Snapshot: &v}, t0)
	a.introOver = true
	return a, src
}

// screenText renvoie les caractères de l'image, une ligne par rangée.
func screenText(cv *canvas.Canvas) string {
	var b strings.Builder
	for y := 0; y < cv.H; y++ {
		b.WriteString(cv.Row(y))
		b.WriteByte('\n')
	}
	return b.String()
}

// vt est un terminal minimal : il suit le curseur et les caractères, pour
// vérifier que le rendu différentiel amène bien l'écran à l'image voulue.
type vt struct {
	W, H   int
	cells  []rune
	cx, cy int
}

func newVT(w, h int) *vt {
	v := &vt{W: w, H: h, cells: make([]rune, w*h)}
	for i := range v.cells {
		v.cells[i] = ' '
	}
	return v
}

func (v *vt) feed(b []byte) {
	s := string(b)
	for len(s) > 0 {
		if s[0] == 0x1b && len(s) > 1 && s[1] == '[' {
			j := 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'H' {
				parts := strings.Split(s[2:j], ";")
				y, _ := strconv.Atoi(parts[0])
				x, _ := strconv.Atoi(parts[1])
				v.cx, v.cy = x-1, y-1
			}
			s = s[j+1:]
			continue
		}
		r := []rune(s[:min(len(s), 4)])[0]
		n := len(string(r))
		if v.cx < v.W && v.cy < v.H {
			v.cells[v.cy*v.W+v.cx] = r
			if canvas.RuneWidth(r) == 2 && v.cx+1 < v.W {
				v.cells[v.cy*v.W+v.cx+1] = 0
			}
		}
		v.cx += canvas.RuneWidth(r)
		s = s[n:]
	}
}

func (v *vt) text() string {
	var b strings.Builder
	for y := 0; y < v.H; y++ {
		for x := 0; x < v.W; x++ {
			if r := v.cells[y*v.W+x]; r != 0 {
				b.WriteRune(r)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

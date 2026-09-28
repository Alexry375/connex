package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/canvas"
	"github.com/Alexry375/connex/internal/ui/term"
)

// oneMachineApp : la source locale, une seule machine, beaucoup de dépôts.
func oneMachineApp(t *testing.T, w, h, repos int) *App {
	t.Helper()
	a := newApp(&fakeSrc{}, t0)
	a.resize(w, h)
	l := laptopSnap(t0)
	for i := 0; i < repos; i++ {
		p := fmt.Sprintf("/home/alfred/p/r%02d", i)
		l.Repos = append(l.Repos, model.Repo{Key: p, Path: p, Branch: "main", Worktrees: []model.Worktree{}})
	}
	a.event(source.Event{Machine: "laptop", Snapshot: &l}, t0)
	a.introOver = true
	return a
}

// Une seule machine en plein écran : le catalogue se lit en deux colonnes, et
// l'élément choisi reste en vue quand on descend.
func TestWideCatalogueFlowsInTwoColumns(t *testing.T) {
	now := t0.Add(5 * time.Second)
	a := oneMachineApp(t, 160, 45, 60)
	rows := rowsOf(t, a, now)
	two := 0
	for _, r := range rows {
		if strings.Count(r, "◆ r") == 2 {
			two++
		}
	}
	if two < 10 {
		t.Fatalf("à 160 × 45, %d rangées portent deux dépôts : le catalogue ne se partage pas\n%s", two, strings.Join(rows, "\n"))
	}
	// Le groupe se redit en tête de la seconde colonne.
	for _, r := range rows {
		if strings.Contains(r, "agents") && strings.Contains(r, "dépôts") {
			goto grouped
		}
	}
	t.Errorf("« dépôts » ne se redit pas en tête de la seconde colonne\n%s", strings.Join(rows, "\n"))
grouped:
	for i := 0; i < 80; i++ {
		a.key(code(term.Down), now)
		it := a.selected()
		if it == nil {
			t.Fatal("plus rien de choisi")
		}
		if all := strings.Join(rowsOf(t, a, now), "\n"); !strings.Contains(all, " "+it.name+" ") {
			t.Fatalf("après %d fois ↓, « %s » n'est plus en vue\n%s", i+1, it.name, all)
		}
	}
	// À 120 colonnes, une seule colonne.
	a = oneMachineApp(t, 120, 36, 60)
	for _, r := range rowsOf(t, a, now) {
		if strings.Count(r, "◆ r") > 1 {
			t.Fatalf("à 120 colonnes, deux colonnes : %q", r)
		}
	}
}

// Toutes les tailles, toutes les vues : ni panique, ni rangée qui déborde ou
// qui manque de largeur.
func TestEverySizeEveryView(t *testing.T) {
	var sizes [][2]int
	for w := 0; w <= 60; w += 3 {
		for _, h := range []int{0, 1, 2, 5, 11, 12, 13, 24} {
			sizes = append(sizes, [2]int{w, h})
		}
	}
	for _, w := range []int{79, 80, 81, 109, 110, 119, 120, 149, 150, 151, 159, 160, 161, 200, 300} {
		for _, h := range []int{1, 11, 12, 13, 24, 31, 32, 36, 45, 90} {
			sizes = append(sizes, [2]int{w, h})
		}
	}
	type setup struct {
		name string
		prep func(t *testing.T) *App
	}
	now := t0.Add(5 * time.Second)
	multi := func(f func(a *App)) func(t *testing.T) *App {
		return func(t *testing.T) *App {
			a, _ := newTestApp(t, 80, 24)
			f(a)
			return a
		}
	}
	setups := []setup{
		{"ciel", multi(func(a *App) {})},
		{"une-machine", func(t *testing.T) *App { return oneMachineApp(t, 80, 24, 60) }},
		{"une-machine-recherche", func(t *testing.T) *App {
			a := oneMachineApp(t, 80, 24, 60)
			typeText(a, "main", now)
			return a
		}},
		{"erreurs", func(t *testing.T) *App { return errorApp(t, 80, 24) }},
		{"recherche", multi(func(a *App) { typeText(a, "p", now) })},
		{"recherche-port", multi(func(a *App) { typeText(a, "51", now) })},
		{"question", multi(func(a *App) {
			a.plate, a.view = appSite, viewPlate
			a.key(term.R('a'), now)
		})},
		{"planche-appli", multi(func(a *App) { a.plate, a.view = appPoly, viewPlate })},
		{"planche-port-long", multi(func(a *App) { a.plate, a.view = port8384, viewPlate })},
		{"planche-agent", multi(func(a *App) { a.plate, a.view = agentWait, viewPlate })},
		{"journal", multi(func(a *App) { a.key(code(term.Tab), now) })},
		{"legende", multi(func(a *App) { a.key(term.R('?'), now) })},
		{"machine-perdue", multi(func(a *App) {
			a.event(statusEv("vps", false, strings.Repeat("ssh: connect to host 100.88.3.21 port 22: Connection timed out ", 3), now), now)
		})},
		{"attente", multi(func(a *App) {
			a.plate, a.view = appPoly, viewPlate
			a.key(term.R('d'), now)
			a.key(code(term.Enter), now.Add(confirmArm))
		})},
	}
	for _, s := range setups {
		for _, sz := range sizes {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s à %d × %d : panique %v", s.name, sz[0], sz[1], r)
					}
				}()
				a := s.prep(t)
				a.resize(sz[0], sz[1])
				for _, dt := range []time.Duration{0, 130 * time.Millisecond, 3 * time.Second, 95 * time.Second} {
					cv := a.frame(now.Add(dt))
					for y := 0; y < cv.H; y++ {
						if w := canvas.StringWidth(cv.Row(y)); w != cv.W {
							t.Errorf("%s à %d × %d : rangée %d de %d colonnes", s.name, sz[0], sz[1], y, w)
							return
						}
					}
				}
			}()
		}
	}
}

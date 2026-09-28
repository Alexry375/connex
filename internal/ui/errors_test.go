package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// Les erreurs de relevé, telles que la vraie machine les donne (worktrees
// cassés, dossier qui n'est pas un dépôt) : chaque ligne dit son dossier, et
// elles passent après les agents, les applis, les ports et les dépôts.

var realErrors = []model.Error{
	{Source: "repos", Message: "/home/alfred/Global/SAND/Castle-of-Doom : fatal: not a git repository (or any of the parent directories): .git"},
	{Source: "repos", Message: "/home/alfred/Global/Agents_Projects/games/.worktrees/alpha-sprint : fatal: not a git repository: /home/alfred/Global/Claude_Projects/games/.git/worktrees/alpha-sprint"},
	{Source: "repos", Message: "/home/alfred/Global/Agents_Projects/games/.worktrees/alpha-sprint-v1 : fatal: not a git repository: /home/alfred/Global/Claude_Projects/games/.git/worktrees/alpha-sprint-v1"},
	{Source: "repos", Message: "permission refusée : /home/alfred/prive"},
	{Source: "machine", Message: "tailscale status : exec: \"tailscale\": executable file not found in $PATH"},
}

func errorApp(t *testing.T, w, h int) *App {
	t.Helper()
	a := newApp(&fakeSrc{}, t0)
	a.resize(w, h)
	l := laptopSnap(t0)
	l.Errors = append([]model.Error{}, realErrors...)
	a.event(source.Event{Machine: "laptop", Snapshot: &l}, t0)
	a.introOver = true
	return a
}

func TestErrorParts(t *testing.T) {
	cases := []struct{ msg, path, reason string }{
		{realErrors[0].Message, "/home/alfred/Global/SAND/Castle-of-Doom", "fatal: not a git repository (or any of the parent directories): .git"},
		{"permission refusée : /home/alfred/prive", "/home/alfred/prive", "permission refusée"},
		{"racine absente : /srv/x", "/srv/x", "racine absente"},
		{realErrors[4].Message, "", realErrors[4].Message},
		{"sans deux-points", "", "sans deux-points"},
	}
	for _, c := range cases {
		if p, r := errorParts(c.msg); p != c.path || r != c.reason {
			t.Errorf("errorParts(%q) = %q, %q ; attendu %q, %q", c.msg, p, r, c.path, c.reason)
		}
	}
}

// Constat sur la vraie machine à 80 × 24 : six erreurs en tête du catalogue,
// six lignes identiques « ✕ relevé repos », prenaient 6 rangées sur 11.
func TestCollectErrorsLastAndNamed(t *testing.T) {
	a := errorApp(t, 80, 24)
	now := t0.Add(5 * time.Second)
	items := a.w.machines[0].items
	seenErr := false
	for _, it := range items {
		if it.kind == kindError {
			seenErr = true
		} else if seenErr {
			t.Fatalf("un élément %q suit une erreur de relevé", it.name)
		}
	}
	if !seenErr {
		t.Fatal("aucune erreur de relevé dans le catalogue")
	}
	// Planche I : les agents d'abord, l'erreur en bas.
	rows := rowsOf(t, a, now)
	all := strings.Join(rows, "\n")
	agentRow, errRow := -1, -1
	for i, r := range rows {
		if agentRow < 0 && strings.Contains(r, "agents") && strings.Contains(r, "nuage") {
			agentRow = i
		}
		if errRow < 0 && strings.Contains(r, "✕ Castle-of-Doom") {
			errRow = i
		}
	}
	if agentRow < 0 || errRow >= 0 && errRow < agentRow {
		t.Errorf("à 80 × 24, les erreurs passent avant les agents (agents rangée %d, erreur rangée %d) :\n%s", agentRow, errRow, all)
	}

	// Chaque ligne se distingue par son dossier ; la raison se coupe s'il faut.
	want := []string{"Castle-of-Doom", "alpha-sprint", "alpha-sprint-v1", "prive", "relevé de la machine"}
	for i, it := range items[len(items)-len(realErrors):] {
		for _, w := range []int{76, 150} {
			cv := canvas.New(w, 1)
			drawLine(cv, 0, 0, w, a.lineFor(it, now), false, 0)
			row := cv.Row(0)
			if !strings.Contains(row, "✕ "+want[i]) {
				t.Errorf("largeur %d : la ligne de l'erreur %d ne dit pas « %s » : %q", w, i, want[i], row)
			}
			if i == 0 && !strings.Contains(row, "fatal: not a git") {
				t.Errorf("largeur %d : la raison manque : %q", w, row)
			}
			if i == 0 && w == 150 && !strings.Contains(row, "~/Global/SAND/Castle-of-Doom") {
				t.Errorf("largeur %d : le dossier manque : %q", w, row)
			}
		}
	}

	// La planche d'une erreur dit tout : la partie du relevé, le dossier, la raison entière.
	it := items[len(items)-len(realErrors)]
	a.plate, a.view = it.id, viewPlate
	all = strings.Join(rowsOf(t, a, now), "\n")
	for _, w := range []string{"Castle-of-Doom", "erreur · relevé des dépôts · laptop", "relevé des dépôts", "dossier", "raison", "(or any of the parent", "directories): .git"} {
		if !strings.Contains(all, w) {
			t.Errorf("planche d'une erreur : « %s » manque\n%s", w, all)
		}
	}

	// On les retrouve en cherchant « erreur », ou le nom du dossier.
	a.view = viewSky
	typeText(a, "erreur", now)
	if got := len(a.results); got != len(realErrors) {
		t.Errorf("« erreur » trouve %d objets, attendu %d", got, len(realErrors))
	}
}

package ui

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/canvas"
	"github.com/Alexry375/connex/internal/ui/term"
)

// longRepoName vient du simulateur (graine 7) : un nom de dossier plus large
// que la colonne de texte d'une planche.
const longRepoName = "2025-stage-M2-analyse-comparative-des-outils-de-supervision-multi-machines"

// Un mot trop long pour la ligne se coupe après un « / », sinon après un « - »
// ou un « _ » ; net seulement s'il n'a aucun de ces séparateurs. Rien ne se perd.
func TestWrapBreaksLongNamesCleanly(t *testing.T) {
	path := "/home/alfred/Global/Archives/" + longRepoName
	for w := 12; w <= 110; w++ {
		lines := wrap(path, w)
		if got := strings.Join(lines, ""); got != path {
			t.Fatalf("largeur %d : %q ne redonne pas le chemin", w, lines)
		}
		for i, l := range lines {
			if canvas.StringWidth(l) > w {
				t.Fatalf("largeur %d : ligne %q trop large", w, l)
			}
			if i < len(lines)-1 && !strings.ContainsAny(l[len(l)-1:], "/-_") {
				t.Errorf("largeur %d : %q coupé au milieu d'un mot", w, l)
			}
		}
	}
	// Sans séparateur, on coupe net.
	if got := wrap(strings.Repeat("x", 25), 10); len(got) != 3 || got[0] != strings.Repeat("x", 10) {
		t.Errorf("mot sans séparateur : %q", got)
	}
	// Un trait d'union en tête ne fait pas une ligne à lui seul.
	for _, l := range wrap("-"+strings.Repeat("y", 20), 8) {
		if l == "-" {
			t.Errorf("ligne réduite à un trait d'union : %q", wrap("-"+strings.Repeat("y", 20), 8))
		}
	}
}

// « à l’instant » et « il y a 12 s » ne se coupent pas en fin de ligne : sur
// la planche, le résultat d'une action finit par l'un ou l'autre.
func TestAgoNeverSplits(t *testing.T) {
	for _, d := range []time.Duration{0, 12 * time.Second, 3 * time.Minute} {
		ago := agoFR(d)
		s := "✕ refusé : le port 5000 est déjà pris par python3 (pid 262025) · " + ago
		for w := canvas.StringWidth(ago); w <= 80; w++ {
			lines := wrap(s, w)
			if last := lines[len(lines)-1]; !strings.HasSuffix(last, ago) {
				t.Errorf("largeur %d : « %s » coupé : %q", w, ago, lines)
			}
		}
	}
}

// wrapRows garde au plus n lignes, jamais plus larges que w ; s'il en manque,
// la dernière finit par « … ».
func TestWrapRows(t *testing.T) {
	s := "ssh: Could not resolve hostname abc-desktop.tail5e2c1.ts.net: Temporary failure in name resolution"
	for w := 3; w <= 110; w++ {
		all := wrap(s, w)
		for n := 0; n <= 4; n++ {
			got := wrapRows(s, w, n)
			if len(got) > n {
				t.Fatalf("largeur %d, %d lignes : %q", w, n, got)
			}
			for _, l := range got {
				if canvas.StringWidth(l) > w {
					t.Fatalf("largeur %d : ligne %q trop large", w, l)
				}
			}
			switch {
			case n == 0:
			case len(all) <= n && strings.Join(got, "\n") != strings.Join(all, "\n"):
				t.Errorf("largeur %d, %d lignes : %q au lieu de %q", w, n, got, all)
			case len(all) > n && !strings.HasSuffix(got[n-1], "…"):
				t.Errorf("largeur %d, %d lignes : %q ne finit pas par « … »", w, n, got)
			}
		}
	}
}

// Une machine injoignable avant tout relevé : sous « Injoignable avant tout
// relevé. », la raison de ssh se lit en entier si le catalogue en a la place,
// et finit par « … » sinon ; jamais tranchée net.
func TestUnreachableBeforeSnapshotReason(t *testing.T) {
	const timeout = "ssh: connect to host 100.88.3.21 port 22: Connection timed out"
	const resolve = "ssh: Could not resolve hostname abc-desktop.tail5e2c1.ts.net: Temporary failure in name resolution"
	for _, c := range []struct {
		w, h   int
		reason string
		full   bool // la place suffit : la raison doit se lire en entier
	}{
		{80, 24, timeout, true}, {120, 36, timeout, true}, {160, 45, timeout, true},
		{120, 12, timeout, true}, {120, 12, resolve, false}, {40, 12, resolve, false},
	} {
		a, _ := newTestApp(t, c.w, c.h)
		now := t0.Add(5 * time.Second)
		a.event(statusEv("pi", false, c.reason, now), now)
		for i := 0; i < 4 && a.w.machines[a.focus].m.Name != "pi"; i++ { // à 80 colonnes, un catalogue à la fois
			a.key(code(term.Right), now)
		}
		rows := rowsOf(t, a, now)
		// Sous l'en-tête, on lit les lignes tant qu'elles prolongent la raison.
		got := ""
		for y, row := range rows {
			i := strings.Index(row, "Injoignable avant tout relevé.")
			if i < 0 {
				continue
			}
			col := len([]rune(row[:i]))
			for _, next := range rows[y+1:] {
				rs := []rune(next)
				if col >= len(rs) || strings.TrimSpace(string(rs[col:])) == "" {
					break
				}
				cand := strings.TrimSpace(string(rs[col:]))
				if got != "" {
					cand = got + " " + cand
				}
				stem, cut := strings.CutSuffix(cand, "…")
				if !strings.HasPrefix(c.reason, strings.TrimSpace(stem)) {
					break
				}
				got = cand
				if cut {
					break
				}
			}
			break
		}
		switch {
		case got == c.reason:
		case strings.HasSuffix(got, "…") && !c.full:
		default:
			t.Errorf("%d × %d : raison lue %q\n%s", c.w, c.h, got, strings.Join(rows, "\n"))
		}
		if !c.full && got == c.reason {
			t.Errorf("%d × %d : le cas doit manquer de place, il ne teste pas « … »", c.w, c.h)
		}
	}
}

// Sur la planche d'un dépôt au nom très long, à toutes les tailles : le chemin
// se lit en entier, et le nom n'est jamais coupé net. Là où il ne tient pas, il
// finit par « … » (en tête de la planche) ou par un séparateur (dans le chemin).
func TestLongNamePlate(t *testing.T) {
	path := "/home/alfred/Global/Archives/" + longRepoName
	isName := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_/.…", r)
	}
	for _, sz := range [][2]int{{80, 24}, {100, 30}, {110, 32}, {120, 36}, {160, 45}} {
		a, _ := newTestApp(t, sz[0], sz[1])
		now := t0.Add(5 * time.Second)
		r := model.Repo{Key: path, Path: path, Branch: "main", Worktrees: []model.Worktree{}}
		a.event(patchEv("laptop", now, model.Change{Op: model.OpAdd, Kind: model.KindRepo, Key: path, Data: r}), now)
		a.plate, a.view = itemID("laptop", kindRepo, path), viewPlate
		rows := rowsOf(t, a, now)
		all := strings.Join(rows, "\n")
		for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '-' }) {
			if !strings.Contains(all, part) {
				t.Errorf("%d × %d : « %s » manque au chemin\n%s", sz[0], sz[1], part, all)
			}
		}
		for _, row := range rows {
			for off := 0; ; {
				i := strings.Index(row[off:], "2025-stage")
				if i < 0 {
					break
				}
				rs := []rune(row[off+i:])
				j := 0
				for j < len(rs) && isName(rs[j]) {
					j++
				}
				run := string(rs[:j])
				ok := run == longRepoName || strings.HasSuffix(run, "…") ||
					strings.HasPrefix(longRepoName, run) && strings.HasSuffix(run, "-")
				if !ok {
					t.Errorf("%d × %d : nom coupé net : %q\n%s", sz[0], sz[1], run, all)
				}
				off += i + len("2025-stage")
			}
		}
	}
}

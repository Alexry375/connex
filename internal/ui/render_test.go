package ui

import (
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/ui/canvas"
	"github.com/Alexry375/connex/internal/ui/term"
)

// Le rendu différentiel doit amener l'écran exactement à l'image voulue, d'une
// planche à l'autre et d'un instant à l'autre.
func TestDiffRenderMatchesFrames(t *testing.T) {
	a, _ := newTestApp(t, 80, 24)
	r := canvas.NewRenderer()
	v := newVT(80, 24)
	now := t0.Add(2 * time.Second)
	check := func(step string) {
		t.Helper()
		cv := a.frame(now)
		v.feed(r.Frame(cv))
		if got, want := v.text(), screenText(cv); got != want {
			t.Fatalf("%s : l'écran ne correspond pas à l'image\n--- écran\n%s--- image\n%s", step, got, want)
		}
		now = now.Add(300 * time.Millisecond)
	}
	check("planche I")
	check("planche I, un pas plus tard")
	a.key(term.Key{Code: term.Enter}, now)
	check("planche II")
	a.key(term.Key{Code: term.Esc}, now)
	check("retour")
	a.key(term.Key{Code: term.Tab}, now)
	check("journal")
	a.key(term.R('?'), now)
	check("légende")
	a.key(term.Key{Code: term.Esc}, now)
	a.key(term.R('5'), now)
	check("recherche")
}

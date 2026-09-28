package canvas

import (
	"strings"
	"testing"
)

func TestWidthAndClean(t *testing.T) {
	for s, want := range map[string]int{
		"site":           4,
		"dépôt":          5,
		"~/Global/名前":    13,
		"e\xcc\x81":      1, // e + accent combinant
		"a\xe2\x80\xaeb": 3, // contrôle bidi remplacé par « ? »
		"\x1b]0;titre\a": 10,
	} {
		if got := StringWidth(s); got != want {
			t.Errorf("StringWidth(%q) = %d, attendu %d", s, got, want)
		}
	}
	c := New(20, 1)
	c.Text(0, 0, 20, "a\x1b[31mb", Hex(0xffffff), 0)
	if row := c.Row(0); strings.ContainsRune(row, 0x1b) || !strings.HasPrefix(row, "a?[31mb") {
		t.Fatalf("séquence d'échappement non neutralisée : %q", row)
	}
}

func TestTextClipsAndTruncates(t *testing.T) {
	c := New(10, 2)
	end := c.Text(6, 0, 10, "abcdef", Hex(0xffffff), 0)
	if end != 10 || c.Row(0) != "      abcd" {
		t.Fatalf("coupe à droite : fin %d, %q", end, c.Row(0))
	}
	// Un caractère large qui ne tient pas n'est pas écrit à moitié.
	c.Text(8, 1, 10, "a名", Hex(0xffffff), 0)
	if c.Row(1) != "        a " {
		t.Fatalf("large coupé : %q", c.Row(1))
	}
	if got := Truncate("~/Global/Projets/site", 10); got != "~/Global/…" || StringWidth(got) != 10 {
		t.Fatalf("Truncate : %q", got)
	}
	if got := TruncateLeft("~/Global/Projets/site", 10); got != "…jets/site" {
		t.Fatalf("TruncateLeft : %q", got)
	}
}

func TestOverwriteHalfOfWide(t *testing.T) {
	c := New(6, 1)
	c.Text(0, 0, 6, "名前", Hex(0xffffff), 0)
	c.Text(1, 0, 6, "x", Hex(0xffffff), 0) // coupe la moitié droite de 名
	if c.Row(0) != " x前  " {
		t.Fatalf("après recouvrement : %q", c.Row(0))
	}
}

func TestRendererWritesOnlyChanges(t *testing.T) {
	r := NewRenderer()
	c := New(8, 3)
	c.Text(0, 0, 8, "étoile", Hex(0xd0e0d0), 0)
	first := r.Frame(c)
	if !strings.Contains(string(first), "étoile") {
		t.Fatalf("première image incomplète : %q", first)
	}
	if again := r.Frame(c); len(again) != 0 {
		t.Fatalf("rien n'a changé mais %d octets écrits : %q", len(again), again)
	}
	c.SetRune(3, 2, '✦', Hex(0xf0c46e), Bold)
	diff := string(r.Frame(c))
	if !strings.Contains(diff, "\x1b[3;4H") || !strings.Contains(diff, "✦") || strings.Contains(diff, "étoile") {
		t.Fatalf("différence mal écrite : %q", diff)
	}
	if len(diff) > 60 {
		t.Fatalf("une case changée coûte %d octets", len(diff))
	}
	r.Invalidate()
	if again := r.Frame(c); !strings.Contains(string(again), "étoile") {
		t.Fatal("Invalidate ne repeint pas tout")
	}
}

func TestBlitAndBackdrop(t *testing.T) {
	p := NewPixels(2, 2)
	p.Set(0, 0, RGB{1, 0, 0})
	p.Set(0, 1, RGB{0, 0, 1})
	p.Set(1, 0, RGB{0, 1, 0})
	p.Set(1, 1, RGB{0, 1, 0})
	c := New(2, 1)
	c.Blit(p, 0, 0)
	if cell := c.At(0, 0); cell.R != HalfTop || cell.FG != Hex(0xff0000) || cell.BG != Hex(0x0000ff) {
		t.Fatalf("demi-bloc : %+v", cell)
	}
	if cell := c.At(1, 0); cell.R != ' ' || cell.BG != Hex(0x00ff00) {
		t.Fatalf("case unie : %+v", cell)
	}
	// Du texte posé sur un demi-bloc prend la moyenne des deux pixels en fond.
	c.SetRune(0, 0, 'a', Hex(0xffffff), 0)
	if bg := c.At(0, 0).BG; bg != RGB8(128, 0, 128) {
		t.Fatalf("fond sous le texte : %06x", uint32(bg)&0xffffff)
	}
}

func TestBrailleDots(t *testing.T) {
	c := New(2, 1)
	c.DotAt(0, 0, Hex(0xffffff))
	c.DotAt(1, 3, Hex(0xffffff))
	if r := c.At(0, 0).R; r != 0x2800|0x01|0x80 {
		t.Fatalf("braille : %U", r)
	}
	c.SetRune(1, 0, 'x', Hex(0xffffff), 0)
	c.DotAt(2, 0, Hex(0xffffff))
	if r := c.At(1, 0).R; r != 'x' {
		t.Fatalf("un point a recouvert du texte : %q", r)
	}
}

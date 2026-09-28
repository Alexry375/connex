package canvas

import (
	"bytes"
	"strings"
	"testing"
)

func rowText(c *Canvas, y int) string {
	var sb strings.Builder
	for x := 0; x < c.W; x++ {
		if r := c.Cell(x, y).R; r != 0 {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func TestTextClipsAndHandlesWideRunes(t *testing.T) {
	c := New(10, 2)
	n := c.Text(7, 0, "abcdef", Style{Fg: Hex(0xffffff)})
	if n != 6 {
		t.Errorf("colonnes occupées : %d, attendu 6", n)
	}
	if got := rowText(c, 0); got != "       abc" {
		t.Errorf("ligne coupée : %q", got)
	}
	c.Text(0, 1, "日本x", Style{})
	if got := rowText(c, 1); !strings.HasPrefix(got, "日本x") {
		t.Errorf("caractères larges : %q", got)
	}
	if c.Cell(1, 1).R != 0 || c.Cell(3, 1).R != 0 {
		t.Errorf("la moitié droite d'un caractère large doit valoir 0")
	}
	// écraser la moitié droite efface la gauche
	c.Set(1, 1, 'z', Style{})
	if c.Cell(0, 1).R != ' ' {
		t.Errorf("moitié gauche orpheline : %q", c.Cell(0, 1).R)
	}
}

// Un caractère large écrit hors de la grille, ou d'une vue, ne touche à rien :
// ni panique, ni case d'une autre ligne.
func TestWideRuneOutsideTouchesNothing(t *testing.T) {
	c := New(6, 3)
	c.Text(0, 5, "日本", Style{})
	c.Text(0, -1, "日本", Style{})
	s := c.Sub(0, 0, 6, 1)
	s.Text(0, 1, "日本", Style{})
	for y := 0; y < c.H; y++ {
		if got := rowText(c, y); got != "      " {
			t.Errorf("ligne %d touchée : %q", y, got)
		}
	}
}

func TestTextReplacesControlCharacters(t *testing.T) {
	c := New(5, 1)
	c.Text(0, 0, "a\x1b[b", Style{})
	if strings.ContainsRune(rowText(c, 0), 0x1b) {
		t.Fatal("un caractère d'échappement est arrivé dans la grille")
	}
}

func TestSubClipsToParent(t *testing.T) {
	c := New(10, 5)
	s := c.Sub(8, 3, 5, 5)
	if s.W != 2 || s.H != 2 {
		t.Fatalf("vue coupée : %d×%d, attendu 2×2", s.W, s.H)
	}
	s.Text(0, 0, "xyz", Style{})
	if rowText(c, 3) != "        xy" {
		t.Errorf("écriture dans la vue : %q", rowText(c, 3))
	}
}

func TestPixelsComposeHalfBlocks(t *testing.T) {
	c := New(2, 1)
	c.Fill(' ', Style{Bg: Hex(0x000000)})
	red, blue := Hex(0xff0000), Hex(0x0000ff)
	c.Pixel(0, 0, red)
	c.Pixel(0, 1, blue)
	cell := c.Cell(0, 0)
	if cell.R != '▀' || cell.Fg != red || cell.Bg != blue {
		t.Errorf("demi-bloc : %+v", cell)
	}
	if c.PixelAt(0, 0) != red || c.PixelAt(0, 1) != blue || c.PixelAt(1, 1) != Hex(0) {
		t.Errorf("lecture des pixels incohérente")
	}
}

func TestDotsComposeBraille(t *testing.T) {
	c := New(1, 1)
	c.Dot(0, 0, Hex(0xffffff))
	c.Dot(1, 3, Hex(0xffffff))
	if r := c.Cell(0, 0).R; r != 0x2800|0x01|0x80 {
		t.Errorf("braille : %U", r)
	}
}

func TestRendererSendsOnlyChanges(t *testing.T) {
	var out bytes.Buffer
	r := NewRenderer(&out)
	c := New(20, 5)
	c.Text(0, 0, "bonjour", Style{Fg: Hex(0xd0cfcc)})
	if _, err := r.Render(c); err != nil {
		t.Fatal(err)
	}
	first := out.Len()
	if first == 0 || !strings.Contains(out.String(), "bonjour") {
		t.Fatalf("première image vide ou incomplète : %q", out.String())
	}
	out.Reset()
	n, _ := r.Render(c)
	if n != 0 || out.Len() != 0 {
		t.Errorf("image identique : %d octets envoyés, attendu 0", out.Len())
	}
	c.Set(10, 3, 'x', Style{Fg: Hex(0xff0000), Bg: Default})
	out.Reset()
	r.Render(c)
	s := out.String()
	if !strings.Contains(s, "\x1b[4;11H") || !strings.Contains(s, "x") {
		t.Errorf("changement mal placé : %q", s)
	}
	if len(s) > 60 {
		t.Errorf("une seule case changée mais %d octets envoyés : %q", len(s), s)
	}
	r.Invalidate()
	out.Reset()
	r.Render(c)
	if out.Len() < first/2 {
		t.Errorf("Invalidate doit tout repeindre (%d octets)", out.Len())
	}
}

func TestRendererUsesTrueColor(t *testing.T) {
	var out bytes.Buffer
	c := New(1, 1)
	c.Set(0, 0, 'a', Style{Fg: RGB(1, 2, 3), Bg: RGB(4, 5, 6), A: Bold})
	NewRenderer(&out).Render(c)
	for _, want := range []string{"38;2;1;2;3", "48;2;4;5;6", "\x1b[?2026h", "\x1b[?2026l"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%q absent de %q", want, out.String())
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"abcdef", 10, "abcdef"},
		{"abcdef", 4, "abc…"},
		{"abcdef", 1, "…"},
		{"abcdef", 0, ""},
		{"éèàùç", 3, "éè…"},
	}
	for _, c := range cases {
		if got := Truncate(c.in, c.w); got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, attendu %q", c.in, c.w, got, c.want)
		}
	}
	if got := TruncateLeft("/home/alfred/projets", 8); got != "…projets" {
		t.Errorf("TruncateLeft : %q", got)
	}
}

func TestMix(t *testing.T) {
	if got := Mix(Hex(0x000000), Hex(0xffffff), 0.5); got != Hex(0x808080) {
		t.Errorf("Mix : %06x", uint32(got))
	}
	if got := Hex(0x808080).Scale(2); got != Hex(0xffffff) {
		t.Errorf("Scale borne mal : %06x", uint32(got))
	}
}

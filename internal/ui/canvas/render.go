package canvas

import (
	"io"
	"strconv"
	"unicode/utf8"
)

// Renderer écrit des images successives dans un terminal en n'envoyant que les
// cases qui ont changé depuis l'image précédente. Chaque image part en un seul
// Write, encadrée par la mise à jour synchronisée (mode 2026), que les
// terminaux qui ne la connaissent pas ignorent.
type Renderer struct {
	out  io.Writer
	prev []Cell
	w, h int
	full bool
	buf  []byte

	// état du terminal pendant l'écriture d'une image
	curX, curY int
	cur        Style
}

// NewRenderer écrit dans out.
func NewRenderer(out io.Writer) *Renderer { return &Renderer{out: out, full: true} }

// Invalidate force l'image suivante à tout repeindre (après un
// redimensionnement, ou si l'écran a pu être abîmé).
func (r *Renderer) Invalidate() { r.full = true }

// Render envoie l'image c, qui doit être une grille entière (pas une vue).
// Il renvoie le nombre d'octets écrits.
func (r *Renderer) Render(c *Canvas) (int, error) {
	if c.W != r.w || c.H != r.h || len(r.prev) != len(c.cells) {
		r.w, r.h = c.W, c.H
		r.prev = make([]Cell, len(c.cells))
		r.full = true
	}
	b := r.buf[:0]
	b = append(b, "\x1b[?2026h\x1b[0m"...)
	full := r.full
	if full {
		// après un redimensionnement, le terminal a pu garder des restes
		b = append(b, "\x1b[H\x1b[2J"...)
	}
	r.cur = Style{Fg: Default, Bg: Default}
	r.curX, r.curY = -1, -1
	changed := false
	for y := 0; y < c.H; y++ {
		row := y * c.W
		for x := 0; x < c.W; x++ {
			cell := c.cells[row+x]
			if !r.full && cell == r.prev[row+x] {
				// la moitié droite d'un caractère large a pu changer seule
				if !(isWideLead(cell.R) && x+1 < c.W && c.cells[row+x+1] != r.prev[row+x+1]) {
					continue
				}
			}
			if cell.R == 0 {
				continue // moitié droite : écrite avec la gauche
			}
			changed = true
			b = r.moveTo(b, x, y)
			ch := cell.R
			if ch == halfTop && cell.Fg == cell.Bg {
				ch = ' '
			}
			st := Style{Fg: cell.Fg, Bg: cell.Bg, A: cell.A}
			if ch == ' ' && st.A&Underline == 0 {
				st.Fg = r.cur.Fg // la couleur du texte ne compte pas pour une espace
			}
			b = r.style(b, st)
			b = utf8.AppendRune(b, ch)
			r.curX += RuneWidth(ch)
		}
	}
	b = append(b, "\x1b[0m\x1b[?2026l"...)
	r.buf = b
	copy(r.prev, c.cells)
	r.full = false
	if !changed && !full {
		return 0, nil
	}
	return r.out.Write(b)
}

func (r *Renderer) moveTo(b []byte, x, y int) []byte {
	switch {
	case r.curY == y && r.curX == x:
		return b
	case r.curY == y && x > r.curX && r.curX >= 0:
		b = append(b, "\x1b["...)
		b = strconv.AppendInt(b, int64(x-r.curX), 10)
		b = append(b, 'C')
	default:
		b = append(b, "\x1b["...)
		b = strconv.AppendInt(b, int64(y+1), 10)
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(x+1), 10)
		b = append(b, 'H')
	}
	r.curX, r.curY = x, y
	return b
}

func (r *Renderer) style(b []byte, st Style) []byte {
	if st == r.cur {
		return b
	}
	b = append(b, "\x1b["...)
	first := true
	sep := func() {
		if !first {
			b = append(b, ';')
		}
		first = false
	}
	for _, a := range []struct {
		bit     Attr
		on, off string
	}{{Bold, "1", "22"}, {Underline, "4", "24"}, {Italic, "3", "23"}} {
		was, want := r.cur.A&a.bit != 0, st.A&a.bit != 0
		switch {
		case want && !was:
			sep()
			b = append(b, a.on...)
		case was && !want:
			sep()
			b = append(b, a.off...)
		}
	}
	if st.Fg != r.cur.Fg {
		sep()
		b = appendColor(b, st.Fg, "38", "39")
	}
	if st.Bg != r.cur.Bg {
		sep()
		b = appendColor(b, st.Bg, "48", "49")
	}
	b = append(b, 'm')
	r.cur = st
	return b
}

func appendColor(b []byte, c Color, set, reset string) []byte {
	if c == Default || c == Transparent {
		return append(b, reset...)
	}
	cr, cg, cb := c.RGB()
	b = append(b, set...)
	b = append(b, ";2;"...)
	b = strconv.AppendInt(b, int64(cr), 10)
	b = append(b, ';')
	b = strconv.AppendInt(b, int64(cg), 10)
	b = append(b, ';')
	b = strconv.AppendInt(b, int64(cb), 10)
	return b
}

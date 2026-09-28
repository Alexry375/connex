package canvas

import "strconv"

// Renderer envoie les toiles successives au terminal en n'écrivant que les cases
// qui ont changé depuis l'image précédente. Au repos, une animation discrète ne
// coûte ainsi que quelques dizaines d'octets par image.
type Renderer struct {
	front *Canvas
	out   []byte
	full  bool
}

// NewRenderer renvoie un Renderer dont la première image repeindra tout.
func NewRenderer() *Renderer { return &Renderer{full: true} }

// Invalidate force l'image suivante à tout repeindre (redimensionnement, Ctrl-L).
func (r *Renderer) Invalidate() { r.full = true }

// Frame renvoie les octets qui amènent l'écran de l'image précédente à back ; une
// tranche vide s'il n'y a rien à écrire. La tranche est réutilisée à l'appel
// suivant.
func (r *Renderer) Frame(back *Canvas) []byte {
	if r.full || r.front == nil || r.front.W != back.W || r.front.H != back.H {
		r.front = &Canvas{W: back.W, H: back.H, Cells: make([]Cell, len(back.Cells))}
		for i := range r.front.Cells {
			r.front.Cells[i] = Cell{R: -1} // aucune case ne lui ressemble
		}
		r.full = false
	}
	// Pas de mise à jour synchronisée (mode 2026) : le terminal de VHS
	// (xterm.js) laisse alors des restes de l'image précédente.
	w := &writer{out: r.out[:0], cx: -1, cy: -1, W: back.W}
	for y := 0; y < back.H; y++ {
		row := back.Cells[y*back.W : (y+1)*back.W]
		front := r.front.Cells[y*back.W : (y+1)*back.W]
		for x := 0; x < back.W; x++ {
			b := row[x]
			if b == front[x] {
				continue
			}
			switch {
			case b.R == 0 && x > 0 && RuneWidth(row[x-1].R) == 2:
				// moitié droite d'un large : on réécrit le caractère entier
				w.cell(x-1, y, row[x-1])
				front[x-1], front[x] = row[x-1], b
				continue
			case b.R == 0:
				b.R = ' ' // moitié droite orpheline
			}
			w.cell(x, y, b)
			front[x] = row[x]
			if RuneWidth(b.R) == 2 && x+1 < back.W {
				front[x+1] = row[x+1]
				x++
			}
		}
	}
	r.out = w.out
	if len(w.out) == 0 {
		return nil
	}
	return r.out
}

type writer struct {
	out    []byte
	cx, cy int
	W      int
	pen    Cell
	penOK  bool
}

func (w *writer) cell(x, y int, c Cell) {
	if w.cx != x || w.cy != y {
		w.out = append(w.out, "\x1b["...)
		w.out = strconv.AppendInt(w.out, int64(y+1), 10)
		w.out = append(w.out, ';')
		w.out = strconv.AppendInt(w.out, int64(x+1), 10)
		w.out = append(w.out, 'H')
	}
	w.style(c)
	w.out = appendRune(w.out, c.R)
	w.cx, w.cy = x+RuneWidth(c.R), y
	if w.cx >= w.W {
		w.cx = -1 // dernière colonne : position du curseur incertaine
	}
}

// style règle le stylo pour la case c. Pour une espace, seule la couleur du fond
// compte.
func (w *writer) style(c Cell) {
	space := c.R == ' ' && c.A&Underline == 0
	if w.penOK && w.pen.A == c.A && w.pen.BG == c.BG && (space || w.pen.FG == c.FG) {
		return
	}
	if !w.penOK || w.pen.A != c.A {
		w.out = append(w.out, "\x1b[0"...)
		if c.A&Bold != 0 {
			w.out = append(w.out, ";1"...)
		}
		if c.A&Dim != 0 {
			w.out = append(w.out, ";2"...)
		}
		if c.A&Italic != 0 {
			w.out = append(w.out, ";3"...)
		}
		if c.A&Underline != 0 {
			w.out = append(w.out, ";4"...)
		}
		w.out = appendColor(w.out, 38, c.FG)
		w.out = appendColor(w.out, 48, c.BG)
		w.out = append(w.out, 'm')
		w.pen, w.penOK = c, true
		return
	}
	w.out = append(w.out, "\x1b["...)
	first := true
	if !space && w.pen.FG != c.FG {
		w.out = appendColorParam(w.out, 38, c.FG, first)
		first = false
		w.pen.FG = c.FG
	}
	if w.pen.BG != c.BG {
		w.out = appendColorParam(w.out, 48, c.BG, first)
		w.pen.BG = c.BG
	}
	w.out = append(w.out, 'm')
}

// appendColor ajoute « ;38;2;r;g;b » (ou 48 pour le fond) ; rien pour la couleur
// par défaut, déjà remise par le 0 qui précède.
func appendColor(out []byte, base int, c Color) []byte {
	if !c.IsSet() {
		return out
	}
	return appendColorParam(out, base, c, false)
}

func appendColorParam(out []byte, base int, c Color, first bool) []byte {
	if !first {
		out = append(out, ';')
	}
	if !c.IsSet() {
		return strconv.AppendInt(out, int64(base+1), 10) // 39 ou 49
	}
	r, g, b := c.Parts()
	out = strconv.AppendInt(out, int64(base), 10)
	out = append(out, ";2;"...)
	out = strconv.AppendInt(out, int64(r), 10)
	out = append(out, ';')
	out = strconv.AppendInt(out, int64(g), 10)
	out = append(out, ';')
	return strconv.AppendInt(out, int64(b), 10)
}

func appendRune(out []byte, r rune) []byte {
	if r < 0x80 {
		return append(out, byte(r))
	}
	return append(out, string(r)...)
}

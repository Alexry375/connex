package canvas

// HalfTop est le demi-bloc haut : son encre est le pixel du haut de la case, son
// fond le pixel du bas. Une case porte ainsi deux pixels presque carrés.
const HalfTop = '▀'

// RGB est une couleur en flottants, composantes entre 0 et 1.
type RGB struct{ R, G, B float32 }

func clamp8(v float32) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 1:
		return 255
	}
	return uint8(v*255 + 0.5)
}

// Color convertit en couleur 24 bits.
func (c RGB) Color() Color { return RGB8(clamp8(c.R), clamp8(c.G), clamp8(c.B)) }

// Scale multiplie la luminosité.
func (c RGB) Scale(k float32) RGB { return RGB{c.R * k, c.G * k, c.B * k} }

// Plus ajoute une lumière.
func (c RGB) Plus(d RGB) RGB { return RGB{c.R + d.R, c.G + d.G, c.B + d.B} }

// Luma renvoie la luminance perçue.
func (c RGB) Luma() float32 { return 0.2126*c.R + 0.7152*c.G + 0.0722*c.B }

// Lerp va de a (t = 0) à b (t = 1).
func Lerp(a, b RGB, t float32) RGB {
	return RGB{a.R + (b.R-a.R)*t, a.G + (b.G-a.G)*t, a.B + (b.B-a.B)*t}
}

// Mix mélange deux couleurs 24 bits (t = 0 : a).
func Mix(a, b Color, t float64) Color {
	if !a.IsSet() {
		return b
	}
	if !b.IsSet() {
		return a
	}
	return Lerp(a.RGB(), b.RGB(), float32(t)).Color()
}

// Pixels est une image en flottants, à deux pixels par case en hauteur.
type Pixels struct {
	W, H int
	P    []RGB
}

// NewPixels renvoie une image noire de w × h pixels.
func NewPixels(w, h int) *Pixels {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return &Pixels{W: w, H: h, P: make([]RGB, w*h)}
}

// At renvoie le pixel (x, y), noir hors de l'image.
func (p *Pixels) At(x, y int) RGB {
	if x < 0 || y < 0 || x >= p.W || y >= p.H {
		return RGB{}
	}
	return p.P[y*p.W+x]
}

// Set pose un pixel.
func (p *Pixels) Set(x, y int, c RGB) {
	if x < 0 || y < 0 || x >= p.W || y >= p.H {
		return
	}
	p.P[y*p.W+x] = c
}

// Add ajoute une lumière c d'intensité k au pixel (x, y).
func (p *Pixels) Add(x, y int, c RGB, k float32) {
	if x < 0 || y < 0 || x >= p.W || y >= p.H || k <= 0 {
		return
	}
	i := y*p.W + x
	p.P[i] = p.P[i].Plus(c.Scale(k))
}

// Blend rapproche le pixel (x, y) de c, de t.
func (p *Pixels) Blend(x, y int, c RGB, t float32) {
	if x < 0 || y < 0 || x >= p.W || y >= p.H || t <= 0 {
		return
	}
	i := y*p.W + x
	if t > 1 {
		t = 1
	}
	p.P[i] = Lerp(p.P[i], c, t)
}

// CopyFrom recopie q, de même taille.
func (p *Pixels) CopyFrom(q *Pixels) {
	if p.W == q.W && p.H == q.H {
		copy(p.P, q.P)
	}
}

// Blit dessine les pixels dans la toile à partir de la case (cx, cy) : les
// pixels 2j et 2j+1 forment la rangée de cases j.
func (c *Canvas) Blit(p *Pixels, cx, cy int) {
	for j := 0; j*2 < p.H; j++ {
		y := cy + j
		if y < 0 || y >= c.H {
			continue
		}
		for i := 0; i < p.W; i++ {
			x := cx + i
			if x < 0 || x >= c.W {
				continue
			}
			top := p.P[2*j*p.W+i].Color()
			bot := top
			if 2*j+1 < p.H {
				bot = p.P[(2*j+1)*p.W+i].Color()
			}
			cell := &c.Cells[y*c.W+x]
			if top == bot {
				*cell = Cell{R: ' ', BG: top}
			} else {
				*cell = Cell{R: HalfTop, FG: top, BG: bot}
			}
		}
	}
}

// Points du braille : bit du point (dx, dy) d'une case, dx de 0 à 1, dy de 0 à 3.
var brailleBit = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// Dot allume un point braille dans la case (x, y) : dx de 0 à 1, dy de 0 à 3.
// Il ne recouvre jamais un caractère déjà écrit (texte, étoile).
func (c *Canvas) Dot(x, y, dx, dy int, fg Color) {
	if !c.In(x, y) || dx < 0 || dx > 1 || dy < 0 || dy > 3 {
		return
	}
	cell := &c.Cells[y*c.W+x]
	bit := brailleBit[dy][dx]
	switch {
	case cell.R >= 0x2800 && cell.R <= 0x28ff:
		cell.R |= bit
		if fg.RGB().Luma() > cell.FG.RGB().Luma() {
			cell.FG = fg
		}
	case cell.R == ' ' || cell.R == HalfTop:
		bg := c.Backdrop(x, y)
		*cell = Cell{R: 0x2800 | bit, FG: fg, BG: bg}
	}
}

// DotAt allume le point braille le plus proche de (bx, by), en coordonnées de
// points : 2 points par case en largeur, 4 en hauteur.
func (c *Canvas) DotAt(bx, by float64, fg Color) {
	if bx < 0 || by < 0 {
		return
	}
	ix, iy := int(bx), int(by)
	c.Dot(ix/2, iy/4, ix%2, iy%4, fg)
}

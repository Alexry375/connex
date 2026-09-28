// Package canvas est la toile de l'affichage : une grille de cases (caractère,
// encre, fond, style) qu'on compose en entier à chaque image, et qu'un Renderer
// envoie au terminal en n'écrivant que les cases qui ont changé.
package canvas

// Color est une couleur 24 bits. La valeur nulle est la couleur par défaut du
// terminal.
type Color uint32

const setBit = 1 << 24

// Hex construit une couleur à partir de 0xRRGGBB.
func Hex(v uint32) Color { return Color(setBit | v&0xffffff) }

// RGB8 construit une couleur à partir de ses trois composantes.
func RGB8(r, g, b uint8) Color {
	return Color(setBit | uint32(r)<<16 | uint32(g)<<8 | uint32(b))
}

// IsSet dit si la couleur est définie (et non celle du terminal).
func (c Color) IsSet() bool { return c&setBit != 0 }

// Parts renvoie les trois composantes.
func (c Color) Parts() (r, g, b uint8) { return uint8(c >> 16), uint8(c >> 8), uint8(c) }

// RGB renvoie la couleur en flottants, entre 0 et 1.
func (c Color) RGB() RGB {
	r, g, b := c.Parts()
	return RGB{float32(r) / 255, float32(g) / 255, float32(b) / 255}
}

// Attr est un style de caractère.
type Attr uint8

// Styles.
const (
	Bold Attr = 1 << iota
	Dim
	Italic
	Underline
)

// Cell est une case de la toile. R vaut 0 pour la moitié droite d'un caractère
// large.
type Cell struct {
	R  rune
	FG Color
	BG Color
	A  Attr
}

// Canvas est une grille de W × H cases.
type Canvas struct {
	W, H  int
	Cells []Cell
}

// New renvoie une toile vide (espaces sur fond par défaut).
func New(w, h int) *Canvas {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	c := &Canvas{W: w, H: h, Cells: make([]Cell, w*h)}
	c.Clear(0)
	return c
}

// Clear remplit la toile d'espaces sur le fond bg.
func (c *Canvas) Clear(bg Color) {
	for i := range c.Cells {
		c.Cells[i] = Cell{R: ' ', BG: bg}
	}
}

// In dit si (x, y) est dans la toile.
func (c *Canvas) In(x, y int) bool { return x >= 0 && y >= 0 && x < c.W && y < c.H }

// At renvoie la case (x, y) ; hors de la toile, une case vide.
func (c *Canvas) At(x, y int) Cell {
	if !c.In(x, y) {
		return Cell{R: ' '}
	}
	return c.Cells[y*c.W+x]
}

// Fill peint le fond d'un rectangle et efface ses caractères.
func (c *Canvas) Fill(x, y, w, h int, bg Color) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			if c.In(i, j) {
				c.Cells[j*c.W+i] = Cell{R: ' ', BG: bg}
			}
		}
	}
}

// Tint peint le fond d'un rectangle sans toucher aux caractères.
func (c *Canvas) Tint(x, y, w, h int, bg Color) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			if c.In(i, j) {
				cell := &c.Cells[j*c.W+i]
				if cell.R == HalfTop {
					cell.R, cell.FG = ' ', 0
				}
				cell.BG = bg
			}
		}
	}
}

// Backdrop renvoie la couleur du fond visible en (x, y) : pour une case en
// demi-blocs, la moyenne de ses deux pixels.
func (c *Canvas) Backdrop(x, y int) Color {
	cell := c.At(x, y)
	if cell.R == HalfTop && cell.FG.IsSet() && cell.BG.IsSet() {
		return Mix(cell.FG, cell.BG, 0.5)
	}
	return cell.BG
}

// SetRune pose un caractère d'une case de large en (x, y), sur le fond visible.
func (c *Canvas) SetRune(x, y int, r rune, fg Color, a Attr) {
	if !c.In(x, y) {
		return
	}
	c.put(x, y, Clean(r), 1, fg, c.Backdrop(x, y), a)
}

// Set pose une case telle quelle.
func (c *Canvas) Set(x, y int, cell Cell) {
	if !c.In(x, y) {
		return
	}
	c.put(x, y, Clean(cell.R), 1, cell.FG, cell.BG, cell.A)
}

// put écrit un caractère de largeur w en réparant les caractères larges qu'il
// recouvre à moitié.
func (c *Canvas) put(x, y int, r rune, w int, fg, bg Color, a Attr) {
	row := c.Cells[y*c.W : (y+1)*c.W]
	for i := x; i < x+w && i < c.W; i++ {
		if row[i].R == 0 && i > 0 && i == x {
			row[i-1].R = ' ' // on coupe la moitié droite d'un large
		}
		if RuneWidth(row[i].R) == 2 && i+1 < c.W && i+1 >= x+w {
			row[i+1].R = ' ' // on coupe la moitié gauche d'un large
		}
	}
	row[x] = Cell{R: r, FG: fg, BG: bg, A: a}
	if w == 2 && x+1 < c.W {
		row[x+1] = Cell{R: 0, FG: fg, BG: bg, A: a}
	}
}

// Text écrit s à partir de (x, y), sans dépasser la colonne max (exclue), sur le
// fond visible. Il renvoie la colonne qui suit le dernier caractère écrit. Les
// caractères de contrôle venus des données sont remplacés : ils ne doivent
// jamais atteindre le terminal.
func (c *Canvas) Text(x, y, max int, s string, fg Color, a Attr) int {
	if y < 0 || y >= c.H {
		return x
	}
	if max > c.W {
		max = c.W
	}
	for _, r := range s {
		r = Clean(r)
		w := RuneWidth(r)
		if w == 0 {
			continue
		}
		if x+w > max {
			break
		}
		if x >= 0 {
			c.put(x, y, r, w, fg, c.Backdrop(x, y), a)
		}
		x += w
	}
	return x
}

// TextBG écrit comme Text, sur un fond donné.
func (c *Canvas) TextBG(x, y, max int, s string, fg, bg Color, a Attr) int {
	if y < 0 || y >= c.H {
		return x
	}
	if max > c.W {
		max = c.W
	}
	for _, r := range s {
		r = Clean(r)
		w := RuneWidth(r)
		if w == 0 {
			continue
		}
		if x+w > max {
			break
		}
		if x >= 0 {
			c.put(x, y, r, w, fg, bg, a)
		}
		x += w
	}
	return x
}

// Row renvoie les caractères de la ligne y, pour les tests.
func (c *Canvas) Row(y int) string {
	if y < 0 || y >= c.H {
		return ""
	}
	rs := make([]rune, 0, c.W)
	for _, cell := range c.Cells[y*c.W : (y+1)*c.W] {
		if cell.R != 0 {
			rs = append(rs, cell.R)
		}
	}
	return string(rs)
}

// String renvoie toute la toile, une ligne par rangée.
func (c *Canvas) String() string {
	out := make([]byte, 0, (c.W+1)*c.H)
	for y := 0; y < c.H; y++ {
		out = append(out, c.Row(y)...)
		out = append(out, '\n')
	}
	return string(out)
}

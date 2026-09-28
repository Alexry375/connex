package canvas

import "unicode/utf8"

// Transparent, donné comme couleur de texte ou de fond, garde celle de la
// cellule peinte.
const Transparent Color = 1 << 25

// Attr est un attribut de texte.
type Attr uint8

// Attributs de texte.
const (
	Bold Attr = 1 << iota
	Underline
	Italic
)

// Cell est une case de la grille. R vaut 0 dans la case de droite d'un
// caractère large.
type Cell struct {
	R      rune
	Fg, Bg Color
	A      Attr
}

// Style regroupe les couleurs et attributs d'un texte.
type Style struct {
	Fg, Bg Color
	A      Attr
}

// Canvas est une vue rectangulaire sur une grille. Sub en découpe des vues qui
// partagent la même grille ; les coordonnées sont locales à la vue et tout
// ce qui dépasse est coupé.
type Canvas struct {
	W, H   int
	ox, oy int
	stride int
	rows   int
	cells  []Cell
}

// halfTop est le demi-bloc supérieur : un pixel en haut (texte), un en bas (fond).
const halfTop = '▀'

// New crée une grille de w × h cases, fond et texte par défaut.
func New(w, h int) *Canvas {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	c := &Canvas{W: w, H: h, stride: w, rows: h, cells: make([]Cell, w*h)}
	c.Fill(' ', Style{Fg: Default, Bg: Default})
	return c
}

// Sub renvoie la vue (x, y, w, h) de c, coupée à c.
func (c *Canvas) Sub(x, y, w, h int) *Canvas {
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > c.W {
		w = c.W - x
	}
	if y+h > c.H {
		h = c.H - y
	}
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return &Canvas{W: w, H: h, ox: c.ox + x, oy: c.oy + y, stride: c.stride, rows: c.rows, cells: c.cells}
}

// In dit si (x, y) est dans la vue.
func (c *Canvas) In(x, y int) bool { return x >= 0 && y >= 0 && x < c.W && y < c.H }

func (c *Canvas) at(x, y int) *Cell { return &c.cells[(c.oy+y)*c.stride+c.ox+x] }

// Cell renvoie la case (x, y) ; hors de la vue, une case vide.
func (c *Canvas) Cell(x, y int) Cell {
	if !c.In(x, y) {
		return Cell{R: ' ', Fg: Default, Bg: Default}
	}
	return *c.at(x, y)
}

// Fill remplit la vue avec r dans le style st.
func (c *Canvas) Fill(r rune, st Style) {
	for y := 0; y < c.H; y++ {
		for x := 0; x < c.W; x++ {
			c.Set(x, y, r, st)
		}
	}
}

// Rect remplit le rectangle (x, y, w, h) avec r dans le style st.
func (c *Canvas) Rect(x, y, w, h int, r rune, st Style) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			c.Set(i, j, r, st)
		}
	}
}

// Set peint une case. Transparent garde la couleur en place ; sur un pixel en
// demi-bloc, le fond gardé est le mélange des deux pixels.
func (c *Canvas) Set(x, y int, r rune, st Style) {
	if !c.In(x, y) {
		return
	}
	cell := c.at(x, y)
	if cell.R == 0 && x > 0 {
		// on écrase la moitié droite d'un caractère large : sa moitié gauche devient une espace
		left := c.at(x-1, y)
		left.R = ' '
	}
	if isWideLead(cell.R) && x+1 < c.W {
		// on écrase la moitié gauche : la droite devient une espace
		right := c.at(x+1, y)
		if right.R == 0 {
			right.R = ' '
		}
	}
	fg, bg := st.Fg, st.Bg
	if bg == Transparent {
		bg = cell.visibleBg()
	}
	if fg == Transparent {
		fg = cell.Fg
	}
	cell.R, cell.Fg, cell.Bg, cell.A = r, fg, bg, st.A
}

// SetBg change seulement le fond d'une case (un pixel en demi-bloc devient
// uni).
func (c *Canvas) SetBg(x, y int, bg Color) {
	if !c.In(x, y) {
		return
	}
	cell := c.at(x, y)
	if cell.R == halfTop {
		cell.R = ' '
	}
	cell.Bg = bg
}

// SetFg change seulement la couleur du texte d'une case.
func (c *Canvas) SetFg(x, y int, fg Color) {
	if !c.In(x, y) {
		return
	}
	c.at(x, y).Fg = fg
}

func (cell *Cell) visibleBg() Color {
	if cell.R == halfTop {
		return Mix(cell.Fg, cell.Bg, 0.5)
	}
	return cell.Bg
}

// Text écrit s à partir de (x, y), sans retour à la ligne, et renvoie le
// nombre de colonnes occupées (y compris celles coupées par le bord).
func (c *Canvas) Text(x, y int, s string, st Style) int {
	col := x
	for _, r := range s {
		w := RuneWidth(r)
		switch w {
		case 0:
			continue
		case 2:
			switch {
			case !c.In(col, y):
			case col+1 < c.W:
				c.Set(col, y, r, st)
				c.Set(col+1, y, ' ', st)
				c.at(col+1, y).R = 0
			default:
				c.Set(col, y, '…', st) // la moitié droite tomberait hors du bord
			}
		default:
			c.Set(col, y, sanitize(r), st)
		}
		col += w
	}
	return col - x
}

// Pixel peint un pixel carré : la grille compte deux pixels par case en
// hauteur (demi-blocs). py va de 0 à 2H-1.
func (c *Canvas) Pixel(px, py int, col Color) {
	y := py >> 1
	if py < 0 || !c.In(px, y) {
		return
	}
	cell := c.at(px, y)
	if cell.R != halfTop {
		bg := cell.visibleBg()
		cell.R, cell.Fg, cell.Bg, cell.A = halfTop, bg, bg, 0
	}
	if py&1 == 0 {
		cell.Fg = col
	} else {
		cell.Bg = col
	}
}

// PixelAt renvoie la couleur visible du pixel (px, py).
func (c *Canvas) PixelAt(px, py int) Color {
	y := py >> 1
	if py < 0 || !c.In(px, y) {
		return Default
	}
	cell := c.at(px, y)
	if cell.R == halfTop {
		if py&1 == 0 {
			return cell.Fg
		}
		return cell.Bg
	}
	return cell.Bg
}

// BlendPixel mélange col au pixel (px, py) avec l'opacité a (0 à 1).
func (c *Canvas) BlendPixel(px, py int, col Color, a float64) {
	if a <= 0 {
		return
	}
	if a >= 1 {
		c.Pixel(px, py, col)
		return
	}
	c.Pixel(px, py, Mix(c.PixelAt(px, py), col, a))
}

// Dot allume un point braille : la grille compte 2 × 4 points par case. Les
// points d'une même case partagent la couleur du dernier posé ; le fond reste.
func (c *Canvas) Dot(dx, dy int, col Color) {
	if dx < 0 || dy < 0 {
		return
	}
	x, y := dx>>1, dy>>2
	if !c.In(x, y) {
		return
	}
	cell := c.at(x, y)
	if cell.R < 0x2800 || cell.R > 0x28ff {
		bg := cell.visibleBg()
		cell.R, cell.Bg, cell.A = 0x2800, bg, 0
	}
	cell.R |= brailleBit[dy&3][dx&1]
	cell.Fg = col
}

var brailleBit = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// isWideLead dit si r occupe deux colonnes.
func isWideLead(r rune) bool { return r != 0 && RuneWidth(r) == 2 }

// sanitize remplace les caractères de contrôle, qui dérègleraient le terminal.
func sanitize(r rune) rune {
	if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == utf8.RuneError {
		return '�'
	}
	return r
}

// Width renvoie le nombre de colonnes qu'occupe s.
func Width(s string) int {
	n := 0
	for _, r := range s {
		n += RuneWidth(r)
	}
	return n
}

// RuneWidth renvoie 0 (marque combinante, invisible), 1 ou 2 (caractère large,
// idéogrammes surtout).
func RuneWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 0x300:
		return 1
	case r >= 0x300 && r <= 0x36f, r >= 0x200b && r <= 0x200f, r >= 0xfe00 && r <= 0xfe0f, r == 0x2060, r == 0xfeff:
		return 0
	case r >= 0x1100 && r <= 0x115f,
		r >= 0x2e80 && r <= 0x303e, r >= 0x3041 && r <= 0x33ff,
		r >= 0x3400 && r <= 0x4dbf, r >= 0x4e00 && r <= 0x9fff,
		r >= 0xa000 && r <= 0xa4cf, r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff, r >= 0xfe30 && r <= 0xfe4f,
		r >= 0xff00 && r <= 0xff60, r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1f64f, r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

// Truncate coupe s à w colonnes au plus, en finissant par « … » s'il a fallu
// couper.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) <= w {
		return s
	}
	out := make([]rune, 0, w)
	n := 0
	for _, r := range s {
		rw := RuneWidth(r)
		if n+rw > w-1 {
			break
		}
		out = append(out, r)
		n += rw
	}
	return string(out) + "…"
}

// TruncateLeft coupe le début de s pour tenir en w colonnes : « …fin ».
func TruncateLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) <= w {
		return s
	}
	rs := []rune(s)
	n := 0
	i := len(rs)
	for i > 0 {
		rw := RuneWidth(rs[i-1])
		if n+rw > w-1 {
			break
		}
		n += rw
		i--
	}
	return "…" + string(rs[i:])
}

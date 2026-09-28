// Package canvas est la toile de l'affichage : une grille de cellules colorées
// (couleurs 24 bits), des primitives pour y peindre (texte, pixels en
// demi-blocs, points braille) et un rendu qui n'envoie au terminal que les
// cellules qui ont changé depuis l'image précédente.
package canvas

import "math"

// Color est une couleur 24 bits 0xRRGGBB, ou Default : la couleur du terminal.
type Color uint32

// Default laisse la couleur du terminal (fond ou texte).
const Default Color = 1 << 24

// RGB construit une couleur.
func RGB(r, g, b uint8) Color { return Color(uint32(r)<<16 | uint32(g)<<8 | uint32(b)) }

// Hex construit une couleur depuis 0xRRGGBB.
func Hex(v uint32) Color { return Color(v & 0xffffff) }

// RGB renvoie les composantes ; Default donne du noir.
func (c Color) RGB() (r, g, b uint8) {
	if c == Default {
		return 0, 0, 0
	}
	return uint8(c >> 16), uint8(c >> 8), uint8(c)
}

// Mix mélange a et b : t = 0 donne a, t = 1 donne b. Default est traité comme
// du noir, sauf si les deux valent Default.
func Mix(a, b Color, t float64) Color {
	if a == b {
		return a
	}
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	ar, ag, ab := a.RGB()
	br, bg, bb := b.RGB()
	return RGB(lerp8(ar, br, t), lerp8(ag, bg, t), lerp8(ab, bb, t))
}

// Scale multiplie la luminosité par f (f > 1 éclaircit, borné à 255).
func (c Color) Scale(f float64) Color {
	if c == Default {
		return c
	}
	r, g, b := c.RGB()
	return RGB(clamp8(float64(r)*f), clamp8(float64(g)*f), clamp8(float64(b)*f))
}

// Luma renvoie la luminance perçue, entre 0 et 1.
func (c Color) Luma() float64 {
	r, g, b := c.RGB()
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 255
}

func lerp8(a, b uint8, t float64) uint8 {
	return clamp8(float64(a) + (float64(b)-float64(a))*t)
}

func clamp8(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return uint8(math.Round(v))
}

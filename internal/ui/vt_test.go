package ui

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Alexry375/connex/internal/ui/canvas"
)

// vt est un terminal minimal pour les tests : il rejoue ce que l'affichage
// écrit (déplacements, effacement, texte ; couleurs et modes ignorés) et rend
// l'écran en lignes de texte, pour vérifier ce qu'un humain lirait.
type vt struct {
	w, h int
	cell [][]rune
	x, y int
}

func newVT(w, h int) *vt {
	v := &vt{w: w, h: h}
	v.clear()
	return v
}

func (v *vt) clear() {
	v.cell = make([][]rune, v.h)
	for y := range v.cell {
		v.cell[y] = []rune(strings.Repeat(" ", v.w))
	}
}

// resize change la taille, comme un terminal qu'on agrandit : le contenu est
// perdu, l'affichage doit tout repeindre.
func (v *vt) resize(w, h int) {
	v.w, v.h = w, h
	v.clear()
	v.x, v.y = 0, 0
}

func (v *vt) feed(b []byte) {
	for len(b) > 0 {
		if b[0] == 0x1b && len(b) > 1 && b[1] == '[' {
			i := 2
			for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
				i++
			}
			if i >= len(b) {
				return
			}
			v.csi(string(b[2:i]), b[i])
			b = b[i+1:]
			continue
		}
		r, n := utf8.DecodeRune(b)
		b = b[n:]
		switch {
		case r == '\r':
			v.x = 0
		case r == '\n':
			v.y = min(v.y+1, v.h-1)
		case r < 0x20:
		default:
			if v.x >= 0 && v.x < v.w && v.y >= 0 && v.y < v.h {
				v.cell[v.y][v.x] = r
				if canvas.RuneWidth(r) == 2 && v.x+1 < v.w {
					v.cell[v.y][v.x+1] = 0
				}
			}
			v.x += canvas.RuneWidth(r)
		}
	}
}

func (v *vt) csi(args string, final byte) {
	if strings.HasPrefix(args, "?") {
		return // modes : écran alternatif, curseur, mise à jour synchronisée
	}
	nums := []int{}
	for _, p := range strings.Split(args, ";") {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		nums = append(nums, n)
	}
	arg := func(i, def int) int {
		if i < len(nums) && nums[i] > 0 {
			return nums[i]
		}
		return def
	}
	switch final {
	case 'H':
		v.y, v.x = arg(0, 1)-1, arg(1, 1)-1
	case 'C':
		v.x += arg(0, 1)
	case 'J':
		if arg(0, 0) == 2 {
			v.clear()
		}
	}
}

// lines rend l'écran ; les demi-blocs (pixels) deviennent des espaces.
func (v *vt) lines() []string {
	out := make([]string, v.h)
	for y, row := range v.cell {
		var b strings.Builder
		for _, r := range row {
			switch r {
			case 0:
			case '▀':
				b.WriteRune(' ')
			default:
				b.WriteRune(r)
			}
		}
		out[y] = b.String()
	}
	return out
}

func (v *vt) text() string { return strings.Join(v.lines(), "\n") }

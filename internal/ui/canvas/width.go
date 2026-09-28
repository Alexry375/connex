package canvas

import "unicode"

// Clean remplace ce qui ne doit pas atteindre le terminal : caractères de
// contrôle (une séquence d'échappement cachée dans un nom de dossier piloterait
// le terminal) et contrôles bidirectionnels (ils réordonneraient le texte).
func Clean(r rune) rune {
	switch {
	case r < 0x20, r >= 0x7f && r < 0xa0:
		return '?'
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f, r == 0x061c:
		return '?'
	case r == 0xfffd || r > unicode.MaxRune:
		return '?'
	}
	return r
}

// RuneWidth renvoie le nombre de colonnes qu'occupe r dans un terminal : 0 pour
// une marque combinante ou un caractère de format, 2 pour un caractère large
// d'Asie orientale ou un emoji, 1 sinon.
func RuneWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 0x300:
		return 1
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		return 0
	case isWide(r):
		return 2
	}
	return 1
}

// StringWidth renvoie la largeur de s en colonnes.
func StringWidth(s string) int {
	n := 0
	for _, r := range s {
		n += RuneWidth(Clean(r))
	}
	return n
}

// Truncate coupe s pour qu'il tienne en w colonnes, avec « … » s'il est coupé.
func Truncate(s string, w int) string {
	if StringWidth(s) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	out := make([]rune, 0, w)
	n := 0
	for _, r := range s {
		rw := RuneWidth(Clean(r))
		if n+rw > w-1 {
			break
		}
		out = append(out, r)
		n += rw
	}
	return string(out) + "…"
}

// TruncateLeft coupe le début de s pour qu'il tienne en w colonnes, avec « … ».
func TruncateLeft(s string, w int) string {
	if StringWidth(s) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	rs := []rune(s)
	n := 0
	i := len(rs)
	for i > 0 {
		rw := RuneWidth(Clean(rs[i-1]))
		if n+rw > w-1 {
			break
		}
		n += rw
		i--
	}
	return "…" + string(rs[i:])
}

type span struct{ lo, hi rune }

// wide : plages larges (East Asian Wide et Fullwidth, emoji en présentation
// graphique), d'après Unicode 15. Approximation suffisante pour des noms et des
// chemins.
var wide = []span{
	{0x1100, 0x115f}, {0x231a, 0x231b}, {0x2329, 0x232a}, {0x23e9, 0x23ec}, {0x23f0, 0x23f0},
	{0x23f3, 0x23f3}, {0x25fd, 0x25fe}, {0x2614, 0x2615}, {0x2648, 0x2653}, {0x267f, 0x267f},
	{0x2693, 0x2693}, {0x26a1, 0x26a1}, {0x26aa, 0x26ab}, {0x26bd, 0x26be}, {0x26c4, 0x26c5},
	{0x26ce, 0x26ce}, {0x26d4, 0x26d4}, {0x26ea, 0x26ea}, {0x26f2, 0x26f3}, {0x26f5, 0x26f5},
	{0x26fa, 0x26fa}, {0x26fd, 0x26fd}, {0x2705, 0x2705}, {0x270a, 0x270b}, {0x2728, 0x2728},
	{0x274c, 0x274c}, {0x274e, 0x274e}, {0x2753, 0x2755}, {0x2757, 0x2757}, {0x2795, 0x2797},
	{0x27b0, 0x27b0}, {0x27bf, 0x27bf}, {0x2b1b, 0x2b1c}, {0x2b50, 0x2b50}, {0x2b55, 0x2b55},
	{0x2e80, 0x303e}, {0x3041, 0x33ff}, {0x3400, 0x4dbf}, {0x4e00, 0x9fff}, {0xa000, 0xa4cf},
	{0xa960, 0xa97f}, {0xac00, 0xd7a3}, {0xf900, 0xfaff}, {0xfe10, 0xfe19}, {0xfe30, 0xfe6f},
	{0xff00, 0xff60}, {0xffe0, 0xffe6}, {0x1f004, 0x1f004}, {0x1f0cf, 0x1f0cf}, {0x1f18e, 0x1f18e},
	{0x1f191, 0x1f19a}, {0x1f200, 0x1f251}, {0x1f300, 0x1f64f}, {0x1f680, 0x1f6ff}, {0x1f7e0, 0x1f7eb},
	{0x1f900, 0x1f9ff}, {0x1fa70, 0x1faff}, {0x20000, 0x3fffd},
}

func isWide(r rune) bool {
	lo, hi := 0, len(wide)
	for lo < hi {
		m := (lo + hi) / 2
		switch {
		case r < wide[m].lo:
			hi = m
		case r > wide[m].hi:
			lo = m + 1
		default:
			return true
		}
	}
	return false
}

// Package term parle au terminal : mode brut, taille, écran alternatif, et
// décodage des touches reçues sur l'entrée standard.
package term

import "unicode/utf8"

// Code désigne une touche spéciale ; KeyRune porte un caractère.
type Code int

// Touches reconnues.
const (
	KeyRune Code = iota
	KeyEnter
	KeyEsc
	KeyBackspace
	KeyTab
	KeyBacktab
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyDelete
	KeyCtrlC
	KeyCtrlD
	KeyCtrlL
	KeyCtrlU
	KeyCtrlW
	KeyCtrlR
	KeyUnknown
)

// Key est une touche décodée.
type Key struct {
	Code Code
	Rune rune // pour KeyRune
}

// Decode découpe buf en touches. Une séquence d'échappement coupée en fin de
// tampon est renvoyée dans rest, pour être complétée par la lecture suivante ;
// un Échap seul en fin de tampon est une vraie touche Échap.
func Decode(buf []byte) (keys []Key, rest []byte) {
	for len(buf) > 0 {
		k, n := decodeOne(buf)
		if n == 0 {
			return keys, buf
		}
		if k.Code != KeyUnknown {
			keys = append(keys, k)
		}
		buf = buf[n:]
	}
	return keys, nil
}

// decodeOne lit une touche ; n = 0 si la séquence est incomplète.
func decodeOne(b []byte) (Key, int) {
	c := b[0]
	switch {
	case c == 0x1b:
		if len(b) == 1 {
			return Key{Code: KeyEsc}, 1
		}
		switch b[1] {
		case '[':
			return decodeCSI(b)
		case 'O':
			if len(b) < 3 {
				return Key{}, 0
			}
			return ss3(b[2]), 3
		}
		// Échap suivi d'autre chose : une vraie touche Échap, puis la suite.
		// (Alt+touche envoie la même chose ; l'affichage ne s'en sert pas, et une
		// frappe rapide après Échap ne doit pas se perdre.)
		return Key{Code: KeyEsc}, 1
	case c == '\r' || c == '\n':
		return Key{Code: KeyEnter}, 1
	case c == '\t':
		return Key{Code: KeyTab}, 1
	case c == 0x7f || c == 0x08:
		return Key{Code: KeyBackspace}, 1
	case c == 0x03:
		return Key{Code: KeyCtrlC}, 1
	case c == 0x04:
		return Key{Code: KeyCtrlD}, 1
	case c == 0x0c:
		return Key{Code: KeyCtrlL}, 1
	case c == 0x15:
		return Key{Code: KeyCtrlU}, 1
	case c == 0x17:
		return Key{Code: KeyCtrlW}, 1
	case c == 0x12:
		return Key{Code: KeyCtrlR}, 1
	case c < 0x20:
		return Key{Code: KeyUnknown}, 1
	}
	if !utf8.FullRune(b) {
		return Key{}, 0
	}
	r, n := utf8.DecodeRune(b)
	if r == utf8.RuneError {
		return Key{Code: KeyUnknown}, n
	}
	return Key{Code: KeyRune, Rune: r}, n
}

func ss3(c byte) Key {
	switch c {
	case 'A':
		return Key{Code: KeyUp}
	case 'B':
		return Key{Code: KeyDown}
	case 'C':
		return Key{Code: KeyRight}
	case 'D':
		return Key{Code: KeyLeft}
	case 'H':
		return Key{Code: KeyHome}
	case 'F':
		return Key{Code: KeyEnd}
	}
	return Key{Code: KeyUnknown}
}

// decodeCSI lit « ESC [ paramètres final » ; les séquences inconnues sont
// avalées entières.
func decodeCSI(b []byte) (Key, int) {
	i := 2
	for i < len(b) && (b[i] >= 0x30 && b[i] <= 0x3f || b[i] >= 0x20 && b[i] <= 0x2f) {
		i++
	}
	if i >= len(b) {
		if len(b) > 32 {
			return Key{Code: KeyUnknown}, len(b) // séquence folle : on l'oublie
		}
		return Key{}, 0
	}
	final := b[i]
	params := string(b[2:i])
	n := i + 1
	switch final {
	case 'A', 'B', 'C', 'D', 'H', 'F':
		return ss3(final), n
	case 'Z':
		return Key{Code: KeyBacktab}, n
	case '~':
		first := params
		for j := 0; j < len(params); j++ {
			if params[j] == ';' {
				first = params[:j]
				break
			}
		}
		switch first {
		case "1", "7":
			return Key{Code: KeyHome}, n
		case "4", "8":
			return Key{Code: KeyEnd}, n
		case "3":
			return Key{Code: KeyDelete}, n
		case "5":
			return Key{Code: KeyPgUp}, n
		case "6":
			return Key{Code: KeyPgDn}, n
		}
	}
	return Key{Code: KeyUnknown}, n
}

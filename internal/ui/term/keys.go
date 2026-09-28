package term

import (
	"io"
	"unicode/utf8"
)

// Code dit quelle touche a été pressée.
type Code int

// Touches reconnues. Rune porte un caractère imprimable dans Key.Rune.
const (
	Rune Code = iota
	Enter
	Esc
	Backspace
	Tab
	BackTab
	Up
	Down
	Left
	Right
	Home
	End
	PageUp
	PageDown
	Delete
	F5
	CtrlC
	CtrlL
	CtrlU
	CtrlW
)

// Key est une touche lue au clavier.
type Key struct {
	Code Code
	Rune rune
}

// R construit la touche d'un caractère.
func R(r rune) Key { return Key{Code: Rune, Rune: r} }

// Parse découpe b en touches. rest est le début d'une séquence ou d'un caractère
// UTF-8 incomplet, à compléter par la lecture suivante. Un Échap seul en fin de
// lecture est la touche Échap : les terminaux envoient une séquence d'un bloc.
func Parse(b []byte) (keys []Key, rest []byte) {
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b:
			k, n, ok := parseEsc(b)
			if !ok {
				return keys, b
			}
			if n > 0 && k.Code >= 0 {
				keys = append(keys, k)
			}
			b = b[n:]
		case c == '\r' || c == '\n':
			keys, b = append(keys, Key{Code: Enter}), b[1:]
		case c == 0x7f || c == 0x08:
			keys, b = append(keys, Key{Code: Backspace}), b[1:]
		case c == '\t':
			keys, b = append(keys, Key{Code: Tab}), b[1:]
		case c == 0x03:
			keys, b = append(keys, Key{Code: CtrlC}), b[1:]
		case c == 0x0c:
			keys, b = append(keys, Key{Code: CtrlL}), b[1:]
		case c == 0x15:
			keys, b = append(keys, Key{Code: CtrlU}), b[1:]
		case c == 0x17:
			keys, b = append(keys, Key{Code: CtrlW}), b[1:]
		case c < 0x20:
			b = b[1:] // autre touche de contrôle : ignorée
		default:
			if !utf8.FullRune(b) {
				return keys, b
			}
			r, n := utf8.DecodeRune(b)
			if r != utf8.RuneError && r >= 0x20 && (r < 0x7f || r >= 0xa0) {
				keys = append(keys, R(r))
			}
			b = b[n:]
		}
	}
	return keys, nil
}

// ignored marque une séquence reconnue mais sans touche utile.
const ignored = Code(-1)

// parseEsc lit une séquence qui commence par Échap. ok est faux si elle est
// incomplète.
func parseEsc(b []byte) (Key, int, bool) {
	if len(b) == 1 {
		return Key{Code: Esc}, 1, true
	}
	switch b[1] {
	case '[':
		// CSI : paramètres 0x30–0x3f, intermédiaires 0x20–0x2f, final 0x40–0x7e.
		i := 2
		for i < len(b) && b[i] >= 0x20 && b[i] <= 0x3f {
			i++
		}
		if i >= len(b) {
			return Key{}, 0, false
		}
		final := b[i]
		if final < 0x40 || final > 0x7e {
			return Key{Code: ignored}, i, true // séquence cassée : on la jette
		}
		return Key{Code: csiKey(string(b[2:i]), final)}, i + 1, true
	case 'O':
		if len(b) < 3 {
			return Key{}, 0, false
		}
		code := ignored
		switch b[2] {
		case 'A':
			code = Up
		case 'B':
			code = Down
		case 'C':
			code = Right
		case 'D':
			code = Left
		case 'H':
			code = Home
		case 'F':
			code = End
		}
		return Key{Code: code}, 3, true
	case 0x1b:
		return Key{Code: Esc}, 1, true
	}
	// Alt + touche : on garde Échap, la touche suit.
	return Key{Code: Esc}, 1, true
}

func csiKey(params string, final byte) Code {
	// Les modificateurs (1;5A pour Ctrl-Haut) ne changent rien ici.
	first := params
	for i := 0; i < len(params); i++ {
		if params[i] == ';' {
			first = params[:i]
			break
		}
	}
	switch final {
	case 'A':
		return Up
	case 'B':
		return Down
	case 'C':
		return Right
	case 'D':
		return Left
	case 'H':
		return Home
	case 'F':
		return End
	case 'Z':
		return BackTab
	case '~':
		switch first {
		case "1", "7":
			return Home
		case "4", "8":
			return End
		case "3":
			return Delete
		case "5":
			return PageUp
		case "6":
			return PageDown
		case "15":
			return F5
		}
	}
	return ignored
}

// ReadKeys lit r jusqu'à sa fin et envoie les touches sur out. Il s'arrête aussi
// quand done est fermé (après la lecture en cours).
func ReadKeys(r io.Reader, out chan<- Key, done <-chan struct{}) {
	buf := make([]byte, 512)
	var pending []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			keys, rest := Parse(pending)
			for _, k := range keys {
				select {
				case out <- k:
				case <-done:
					return
				}
			}
			pending = append(pending[:0], rest...)
		}
		if err != nil {
			return
		}
	}
}

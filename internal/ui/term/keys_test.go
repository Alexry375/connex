package term

import "testing"

func TestDecode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Key
	}{
		{"lettres", "ab", []Key{{Code: KeyRune, Rune: 'a'}, {Code: KeyRune, Rune: 'b'}}},
		{"accent", "é", []Key{{Code: KeyRune, Rune: 'é'}}},
		{"flèches", "\x1b[A\x1b[B\x1b[C\x1b[D", []Key{{Code: KeyUp}, {Code: KeyDown}, {Code: KeyRight}, {Code: KeyLeft}}},
		{"flèches mode application", "\x1bOA\x1bOD", []Key{{Code: KeyUp}, {Code: KeyLeft}}},
		{"flèche avec Maj", "\x1b[1;2A", []Key{{Code: KeyUp}}},
		{"pages", "\x1b[5~\x1b[6~", []Key{{Code: KeyPgUp}, {Code: KeyPgDn}}},
		{"début fin", "\x1b[H\x1b[F\x1b[1~\x1b[4~", []Key{{Code: KeyHome}, {Code: KeyEnd}, {Code: KeyHome}, {Code: KeyEnd}}},
		{"Maj-Tab", "\x1b[Z", []Key{{Code: KeyBacktab}}},
		{"Échap seul", "\x1b", []Key{{Code: KeyEsc}}},
		{"Échap puis Échap", "\x1b\x1b", []Key{{Code: KeyEsc}, {Code: KeyEsc}}},
		{"Échap puis x", "\x1bx", []Key{{Code: KeyEsc}, {Code: KeyRune, Rune: 'x'}}},
		{"Échap puis 5173", "\x1b5173", []Key{{Code: KeyEsc}, {Code: KeyRune, Rune: '5'}, {Code: KeyRune, Rune: '1'}, {Code: KeyRune, Rune: '7'}, {Code: KeyRune, Rune: '3'}}},
		{"contrôles", "\x03\r\t\x7f", []Key{{Code: KeyCtrlC}, {Code: KeyEnter}, {Code: KeyTab}, {Code: KeyBackspace}}},
		{"séquence inconnue avalée", "\x1b[200~q", []Key{{Code: KeyRune, Rune: 'q'}}},
	}
	for _, c := range cases {
		got, rest := Decode([]byte(c.in))
		if len(rest) != 0 {
			t.Errorf("%s : reste %q", c.name, rest)
		}
		if len(got) != len(c.want) {
			t.Errorf("%s : %v, attendu %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s : touche %d = %+v, attendu %+v", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestDecodeKeepsIncompleteSequences(t *testing.T) {
	keys, rest := Decode([]byte("a\x1b[1;"))
	if len(keys) != 1 || string(rest) != "\x1b[1;" {
		t.Fatalf("touches %v, reste %q", keys, rest)
	}
	keys, rest = Decode(append(rest, 'B'))
	if len(keys) != 1 || keys[0].Code != KeyDown || len(rest) != 0 {
		t.Fatalf("séquence complétée : %v, reste %q", keys, rest)
	}
	keys, rest = Decode([]byte{0xc3})
	if len(keys) != 0 || len(rest) != 1 {
		t.Fatalf("UTF-8 coupé : %v, reste %q", keys, rest)
	}
}

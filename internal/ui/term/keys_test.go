package term

import (
	"io"
	"reflect"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Key
		rest string
	}{
		{"lettres et accents", "qé5", []Key{R('q'), R('é'), R('5')}, ""},
		{"flèches CSI", "\x1b[A\x1b[B\x1b[C\x1b[D", []Key{{Code: Up}, {Code: Down}, {Code: Right}, {Code: Left}}, ""},
		{"flèches SS3", "\x1bOA\x1bOD", []Key{{Code: Up}, {Code: Left}}, ""},
		{"Échap seul", "\x1b", []Key{{Code: Esc}}, ""},
		{"Échap puis lettre", "\x1bq", []Key{{Code: Esc}, R('q')}, ""},
		{"Entrée, retour, tab", "\r\x7f\t\x1b[Z", []Key{{Code: Enter}, {Code: Backspace}, {Code: Tab}, {Code: BackTab}}, ""},
		{"Ctrl-C", "\x03", []Key{{Code: CtrlC}}, ""},
		{"pages et fin", "\x1b[5~\x1b[6~\x1b[1~\x1b[4~\x1b[3~\x1b[15~", []Key{{Code: PageUp}, {Code: PageDown}, {Code: Home}, {Code: End}, {Code: Delete}, {Code: F5}}, ""},
		{"modificateurs ignorés", "\x1b[1;5A", []Key{{Code: Up}}, ""},
		{"séquence inconnue jetée", "\x1b[200~x", []Key{R('x')}, ""},
		{"séquence incomplète gardée", "a\x1b[1;", []Key{R('a')}, "\x1b[1;"},
		{"UTF-8 coupé gardé", "a\xc3", []Key{R('a')}, "\xc3"},
		{"contrôles ignorés", "\x01\x02b", []Key{R('b')}, ""},
	}
	for _, c := range cases {
		got, rest := Parse([]byte(c.in))
		if !reflect.DeepEqual(got, c.want) || string(rest) != c.rest {
			t.Errorf("%s : Parse(%q) = %v reste %q ; attendu %v reste %q", c.name, c.in, got, rest, c.want, c.rest)
		}
	}
}

// Une séquence coupée entre deux lectures est recollée.
func TestReadKeysAcrossReads(t *testing.T) {
	pr, pw := io.Pipe()
	out := make(chan Key, 8)
	done := make(chan struct{})
	go ReadKeys(pr, out, done)
	go func() {
		pw.Write([]byte("\x1b["))
		time.Sleep(10 * time.Millisecond)
		pw.Write([]byte("Aé"))
		pw.Close()
	}()
	var got []Key
	for k := range collect(out, 2) {
		got = append(got, k)
	}
	want := []Key{{Code: Up}, R('é')}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("touches %v ; attendu %v", got, want)
	}
	close(done)
}

func collect(ch <-chan Key, n int) <-chan Key {
	out := make(chan Key, n)
	go func() {
		defer close(out)
		for i := 0; i < n; i++ {
			select {
			case k := <-ch:
				out <- k
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()
	return out
}

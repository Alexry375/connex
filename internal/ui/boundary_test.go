package ui

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/term"
)

// L'interface ne lit rien sur la machine et n'exécute rien : ses paquets
// n'importent que ce qui sert au terminal et à l'image, et de os et syscall
// ils ne touchent que l'entrée et la sortie standard.
func TestUIReadsNothingElse(t *testing.T) {
	allowed := map[string]bool{
		"bytes": true, "context": true, "embed": true, "errors": true, "fmt": true, "image": true,
		"image/jpeg": true, "io": true, "math": true, "os": true, "os/signal": true, "path": true,
		"sort": true, "strconv": true, "strings": true, "sync": true, "syscall": true, "time": true,
		"unicode": true, "unicode/utf8": true, "unsafe": true,
		"github.com/Alexry375/connex/internal/model": true, "github.com/Alexry375/connex/internal/source": true, "github.com/Alexry375/connex/internal/fleet": true,
		"github.com/Alexry375/connex/internal/ui/art": true, "github.com/Alexry375/connex/internal/ui/canvas": true, "github.com/Alexry375/connex/internal/ui/term": true,
	}
	osOK := map[string]bool{"Stdin": true, "Stdout": true, "File": true, "Signal": true}
	sysBad := map[string]bool{"Open": true, "Openat": true, "Exec": true, "ForkExec": true, "StartProcess": true,
		"Socket": true, "Connect": true, "Bind": true, "Listen": true, "Stat": true, "Lstat": true,
		"Readlink": true, "Getdents": true, "ReadDirent": true, "Chdir": true, "Kill": true, "Setuid": true}
	fset := token.NewFileSet()
	n := 0
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		n++
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); !allowed[path] {
				t.Errorf("%s importe %q", p, path)
			}
		}
		ast.Inspect(f, func(nd ast.Node) bool {
			sel, ok := nd.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok {
				switch {
				case id.Name == "os" && !osOK[sel.Sel.Name]:
					t.Errorf("%s : os.%s", fset.Position(sel.Pos()), sel.Sel.Name)
				case id.Name == "syscall" && sysBad[sel.Sel.Name]:
					t.Errorf("%s : syscall.%s", fset.Position(sel.Pos()), sel.Sel.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil || n < 10 {
		t.Fatalf("parcours des sources : %v (%d fichiers)", err, n)
	}
}

// chanSource est une source de test branchée sur un canal.
type chanSource struct {
	ev   chan source.Event
	mu   sync.Mutex
	sent []model.Request
}

func (c *chanSource) Events() <-chan source.Event { return c.ev }
func (c *chanSource) Close() error                { return nil }
func (c *chanSource) Send(machine string, r model.Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, r)
	return nil
}

// fakeScreen retient ce que la boucle écrit ; sa taille peut changer.
type fakeScreen struct {
	mu   sync.Mutex
	w, h int
	out  []byte
}

func (s *fakeScreen) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = append(s.out, b...)
	return len(b), nil
}

func (s *fakeScreen) Size() (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h, nil
}

// La boucle : un changement de taille redessine tout à la nouvelle taille, et
// q la termine aussitôt.
func TestLoopResizeAndQuit(t *testing.T) {
	src := &chanSource{ev: make(chan source.Event)}
	scr := &fakeScreen{w: 80, h: 24}
	keys := make(chan term.Key)
	winch := make(chan os.Signal)
	done := make(chan error, 1)
	go func() { done <- loop(context.Background(), src, Options{}, scr, keys, winch) }()

	s := laptopSnap(t0)
	src.ev <- source.Event{Machine: "laptop", Snapshot: &s}
	keys <- code(term.Esc) // abrège l'ouverture
	scr.mu.Lock()
	scr.w, scr.h = 100, 30
	scr.mu.Unlock()
	winch <- syscall.SIGWINCH
	keys <- code(term.Down) // une image au moins après le changement de taille
	keys <- term.R('q')
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("q n'a pas terminé la boucle")
	}
	v := newVT(100, 30)
	v.feed(scr.out)
	text := v.text()
	if !strings.Contains(text, "UN AGENT T’ATTEND SUR LAPTOP.") || !strings.Contains(text, "q quitter") {
		t.Fatalf("l'écran à 100 × 30 n'est pas la planche I :\n%s", text)
	}
	if len(src.sent) != 0 {
		t.Fatalf("requêtes envoyées : %+v", src.sent)
	}
}

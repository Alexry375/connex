package ui

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/source"
)

// vtScreen est un terminal d'essai pour la boucle : ce qu'elle écrit passe
// dans l'émulateur vt, et sa taille se change en cours de route.
type vtScreen struct {
	mu sync.Mutex
	vt *vt
}

func newVTScreen(w, h int) *vtScreen { return &vtScreen{vt: newVT(w, h)} }

func (s *vtScreen) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vt.feed(b)
	return len(b), nil
}

func (s *vtScreen) Size() (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vt.w, s.vt.h, nil
}

func (s *vtScreen) resize(w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vt.resize(w, h)
}

func (s *vtScreen) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vt.lines()
}

// until attend que ok soit vrai pour l'écran, et renvoie le temps mis.
func (s *vtScreen) until(t *testing.T, limit time.Duration, what string, ok func(ls []string) bool) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < limit {
		if ok(s.lines()) {
			return time.Since(start)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s : toujours pas après %v ; l'écran :\n%s", what, limit, strings.Join(s.lines(), "\n"))
	return 0
}

type loopRun struct {
	scr   *vtScreen
	in    chan []byte
	winch chan os.Signal
	done  chan error
}

func startLoop(ctx context.Context, src source.Source, w, h int) *loopRun {
	r := &loopRun{scr: newVTScreen(w, h), in: make(chan []byte, 4), winch: make(chan os.Signal, 1), done: make(chan error, 1)}
	go func() { r.done <- loop(ctx, src, r.scr, r.in, r.winch) }()
	return r
}

func (r *loopRun) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("la boucle ne s'arrête pas")
		return nil
	}
}

// Sur le simulateur, à 80 × 24 : ouverture comprise, on lit en moins de 1,5 s
// qui t'attend et sur quelles machines ; q quitte.
func TestLoopReadableFastThenQuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := source.NewDemo(ctx, source.DemoOptions{Seed: 7, Speed: 0})
	defer src.Close()
	r := startLoop(ctx, src, 80, 24)
	took := r.scr.until(t, 3*time.Second, "état lisible", func(ls []string) bool {
		top := strings.Join(ls[:6], "\n")
		return strings.Contains(top, "2 agents t'attendent") &&
			strings.Contains(top, "alfred-laptop") && strings.Contains(top, "vps-40517391") &&
			strings.Contains(top, "abc-desktop") && strings.Contains(ls[23], "q quitter")
	})
	if took >= 1500*time.Millisecond {
		t.Errorf("état lisible en %v : c'est plus de 1,5 s", took)
	}
	t.Logf("état lisible en %v", took)
	r.in <- []byte("q")
	if err := r.wait(t); err != nil {
		t.Errorf("q : %v", err)
	}
}

// Un changement de taille en route repeint tout à la nouvelle taille.
func TestLoopRepaintsOnResize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := newFake()
	for _, ev := range fleetEvents(true) {
		src.ch <- ev
	}
	r := startLoop(ctx, src, 120, 36)
	r.in <- []byte(" ") // abrège l'ouverture
	r.scr.until(t, 2*time.Second, "premier dessin", func(ls []string) bool {
		return strings.Contains(ls[35], "q quitter")
	})
	r.scr.resize(60, 20)
	r.winch <- syscall.SIGWINCH
	r.scr.until(t, 2*time.Second, "dessin à 60 × 20", func(ls []string) bool {
		return len(ls) == 20 && strings.Contains(ls[19], "q quitter") && strings.Contains(ls[0], "t'attend")
	})
	cancel()
	if err := r.wait(t); err != nil {
		t.Errorf("fin du contexte : %v", err)
	}
}

// Ctrl-C quitte ; une source qui se tait pour de bon est une erreur dite.
func TestLoopEnds(t *testing.T) {
	ctx := context.Background()
	r := startLoop(ctx, newFake(), 80, 24)
	r.in <- []byte{0x03}
	if err := r.wait(t); err != nil {
		t.Errorf("Ctrl-C : %v", err)
	}

	src := newFake()
	r = startLoop(ctx, src, 80, 24)
	close(src.ch)
	if err := r.wait(t); err != ErrSourceClosed {
		t.Errorf("source fermée : %v", err)
	}

	// la source locale dit pourquoi elle s'arrête, juste avant de fermer
	src = newFake()
	r = startLoop(ctx, src, 80, 24)
	src.ch <- snap("laptop", nil)
	src.ch <- status("laptop", false, "connex stream : permission refusée")
	close(src.ch)
	if err := r.wait(t); !errors.Is(err, ErrSourceClosed) || !strings.Contains(err.Error(), "laptop injoignable : connex stream : permission refusée") {
		t.Errorf("la cause doit accompagner la fin : %v", err)
	}
	var out strings.Builder
	src = newFake()
	src.ch <- snap("laptop", nil)
	src.ch <- status("laptop", false, "connex stream : permission refusée")
	close(src.ch)
	if err := runPlain(ctx, src, &out); !errors.Is(err, ErrSourceClosed) || !strings.Contains(err.Error(), "permission refusée") {
		t.Errorf("en texte aussi : %v", err)
	}
}

// Échap seul est une touche, même si la suite n'arrive jamais ; une flèche
// coupée entre deux lectures reste une flèche.
func TestLoopEscapeTiming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := newFake()
	for _, ev := range fleetEvents(true) {
		src.ch <- ev
	}
	r := startLoop(ctx, src, 80, 24)
	r.in <- []byte(" /5173")
	r.scr.until(t, 2*time.Second, "recherche", func(ls []string) bool { return strings.Contains(strings.Join(ls, "\n"), "cherche 5173") })
	r.in <- []byte{0x1b}
	r.scr.until(t, 2*time.Second, "échap efface la recherche", func(ls []string) bool {
		return !strings.Contains(strings.Join(ls, "\n"), "cherche 5173")
	})
	r.in <- []byte("/5173\r")
	r.in <- []byte{0x1b, '['}
	r.in <- []byte{'B'}
	r.in <- []byte("\r")
	r.scr.until(t, 2*time.Second, "détail du second programme du port", func(ls []string) bool {
		return strings.Contains(strings.Join(ls, "\n"), "pid 12")
	})
}

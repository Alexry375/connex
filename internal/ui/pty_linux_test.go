//go:build linux

package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Alexry375/connex/internal/source"
)

// Ces tests lancent l'affichage dans un vrai pseudo-terminal : le binaire de
// test se relance lui-même, et l'enfant fait ce que fait connex --demo.

const ptyChildEnv = "CONNEX_UI_TEST_PTY"

func TestMain(m *testing.M) {
	if os.Getenv(ptyChildEnv) == "1" {
		os.Exit(ptyChild())
	}
	os.Exit(m.Run())
}

// ptyChild est l'enfant : Run sur le simulateur figé, arrêté comme connex par
// SIGTERM ou SIGHUP.
func ptyChild() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	src := source.NewDemo(ctx, source.DemoOptions{Seed: 7, Speed: 0})
	defer src.Close()
	if err := Run(ctx, src); err != nil {
		fmt.Fprintln(os.Stderr, "connex :", err)
		return 1
	}
	return 0
}

func ptyIoctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

type ptyRun struct {
	master, slave *os.File
	cmd           *exec.Cmd
	before        syscall.Termios // le mode du terminal avant l'affichage
	mu            sync.Mutex
	out           []byte
	done          chan struct{} // fermé quand l'enfant a fini
	werr          error
}

func setSize(t *testing.T, f *os.File, w, h int) {
	t.Helper()
	ws := struct{ Row, Col, X, Y uint16 }{uint16(h), uint16(w), 0, 0}
	if err := ptyIoctl(f, syscall.TIOCSWINSZ, unsafe.Pointer(&ws)); err != nil {
		t.Fatalf("taille du pseudo-terminal : %v", err)
	}
}

func startPTY(t *testing.T, w, h int) *ptyRun {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("pas de pseudo-terminal ici : %v", err)
	}
	var unlock int32
	var n uint32
	if err := ptyIoctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		t.Skipf("pseudo-terminal verrouillé : %v", err)
	}
	if err := ptyIoctl(master, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		t.Skipf("pseudo-terminal sans numéro : %v", err)
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("pseudo-terminal injoignable : %v", err)
	}
	setSize(t, master, w, h)
	p := &ptyRun{master: master, slave: slave, before: termios(t, slave), done: make(chan struct{})}
	p.cmd = exec.Command(os.Args[0])
	p.cmd.Env = append(os.Environ(), ptyChildEnv+"=1")
	p.cmd.Stdin, p.cmd.Stdout, p.cmd.Stderr = slave, slave, slave
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("lancement : %v", err)
	}
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, err := master.Read(buf)
			p.mu.Lock()
			p.out = append(p.out, buf[:n]...)
			p.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() { p.werr = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			p.cmd.Process.Kill()
			<-p.done
		}
		master.Close()
		slave.Close()
	})
	return p
}

func (p *ptyRun) output() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.out...)
}

// screenFrom rejoue la sortie depuis from dans un terminal w × h.
func (p *ptyRun) screenFrom(from, w, h int) []string {
	v := newVT(w, h)
	v.feed(p.output()[from:])
	return v.lines()
}

func (p *ptyRun) waitScreen(t *testing.T, from, w, h int, what string, ok func([]string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok(p.screenFrom(from, w, h)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s : l'écran n'y est pas :\n%q", what, p.screenFrom(from, w, h))
}

func (p *ptyRun) waitExit(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		if p.werr != nil {
			t.Fatalf("l'affichage finit en erreur : %v\n%q", p.werr, p.output())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("l'affichage ne s'arrête pas")
	}
	// ce qui reste en route
	for n := -1; n != len(p.output()); {
		n = len(p.output())
		time.Sleep(50 * time.Millisecond)
	}
}

func termios(t *testing.T, f *os.File) syscall.Termios {
	t.Helper()
	var tio syscall.Termios
	if err := ptyIoctl(f, syscall.TCGETS, unsafe.Pointer(&tio)); err != nil {
		t.Fatalf("lecture du mode du terminal : %v", err)
	}
	return tio
}

const (
	enterSeq   = "\x1b[?1049h"
	restoreSeq = "\x1b[?2026l\x1b[0m\x1b[?7h\x1b[?25h\x1b[?1049l"
)

func hasState(ls []string) bool {
	top := ""
	for _, l := range ls[:6] {
		top += l + "\n"
	}
	return bytes.Contains([]byte(top), []byte("2 agents t'attendent")) &&
		bytes.Contains([]byte(ls[len(ls)-1]), []byte("q quitter"))
}

// q, Ctrl-C et SIGTERM arrêtent l'affichage à tout moment ; le terminal est
// rendu comme il était : écran principal, curseur, mode normal.
func TestPTYQuitLeavesTerminalClean(t *testing.T) {
	for _, c := range []struct {
		name string
		stop func(p *ptyRun)
	}{
		{"q", func(p *ptyRun) { p.master.Write([]byte("q")) }},
		{"Ctrl-C", func(p *ptyRun) { p.master.Write([]byte{0x03}) }},
		{"q pendant une recherche", func(p *ptyRun) { p.master.Write([]byte("/node")); p.master.Write([]byte("q")) }},
		{"SIGTERM", func(p *ptyRun) { p.cmd.Process.Signal(syscall.SIGTERM) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := startPTY(t, 80, 24)
			p.waitScreen(t, 0, 80, 24, "état lisible", hasState)
			if raw := termios(t, p.slave); raw.Lflag&syscall.ICANON != 0 {
				t.Error("le terminal devait être en mode brut pendant l'affichage")
			}
			c.stop(p)
			p.waitExit(t)
			out := p.output()
			if !bytes.HasPrefix(out, []byte(enterSeq)) {
				t.Errorf("l'affichage doit commencer par l'écran alternatif : %q", out[:min(len(out), 40)])
			}
			if !bytes.HasSuffix(out, []byte(restoreSeq)) {
				t.Errorf("l'affichage doit finir en rendant le terminal : %q", out[max(len(out)-80, 0):])
			}
			if after := termios(t, p.slave); after != p.before {
				t.Errorf("le mode du terminal n'est pas rendu :\navant %+v\naprès %+v", p.before, after)
			}
		})
	}
}

// Agrandir ou rétrécir la fenêtre en route : l'affichage se recompose à la
// nouvelle taille.
func TestPTYResize(t *testing.T) {
	p := startPTY(t, 120, 36)
	p.waitScreen(t, 0, 120, 36, "état lisible à 120 × 36", hasState)
	mark := len(p.output())
	setSize(t, p.master, 60, 20)
	deadline := time.Now().Add(5 * time.Second)
	from := -1
	for from < 0 && time.Now().Before(deadline) {
		if i := bytes.Index(p.output()[mark:], []byte("\x1b[H\x1b[2J")); i >= 0 {
			from = mark + i
		}
		time.Sleep(10 * time.Millisecond)
	}
	if from < 0 {
		t.Fatal("pas de nouvelle image après le changement de taille")
	}
	p.waitScreen(t, from, 60, 20, "état lisible à 60 × 20", func(ls []string) bool {
		return bytes.Contains([]byte(ls[0]), []byte("t'attend")) && bytes.Contains([]byte(ls[19]), []byte("q quitter"))
	})
	p.master.Write([]byte("q"))
	p.waitExit(t)
}

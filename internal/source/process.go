package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// maxLine borne une ligne lue (un relevé de machine chargée tient en quelques
// centaines de kilo-octets).
const maxLine = 16 << 20

// ErrBusy : trop de requêtes en attente d'envoi.
var ErrBusy = errors.New("trop de requêtes en attente")

// ErrClosed : la source est arrêtée.
var ErrClosed = errors.New("source arrêtée")

// Process est une source lue sur un processus qui parle comme `connex stream` :
// lignes JSON sur sa sortie standard, requêtes sur son entrée standard. Quand le
// processus s'arrête, la source émet un Status injoignable puis ferme Events.
// Elle ne relance rien : la reconnexion est l'affaire de l'appelant (lot L2).
type Process struct {
	cmd    *exec.Cmd
	events chan Event
	reqs   chan model.Request
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
	stderr *tail

	mu   sync.Mutex
	name string // nom de la machine : provisoire, puis celui du premier relevé
}

// StartProcess lance argv et en fait une source. machine est le nom provisoire
// de la machine, en attendant son premier relevé.
func StartProcess(ctx context.Context, machine string, argv ...string) (*Process, error) {
	if len(argv) == 0 {
		return nil, errors.New("StartProcess : commande vide")
	}
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	var stderr tail
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	p := &Process{cmd: cmd, events: make(chan Event, 64), reqs: make(chan model.Request, 32),
		done: make(chan struct{}), cancel: cancel, stderr: &stderr, name: machine}
	go p.write(stdin)
	go p.read(ctx, stdout)
	return p, nil
}

// NewLocal est la machine locale, pour de vrai : connex relance son propre
// binaire en `connex stream`.
func NewLocal(ctx context.Context) (*Process, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	return StartProcess(ctx, host, exe, "stream")
}

func (p *Process) machine() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.name
}

func (p *Process) read(ctx context.Context, stdout io.Reader) {
	defer close(p.events)
	defer close(p.done)
	r := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			if ev, derr := Decode(line, p.machine()); derr == nil {
				if ev.Snapshot != nil {
					p.mu.Lock()
					p.name = ev.Snapshot.Machine.Name
					p.mu.Unlock()
				}
				select {
				case p.events <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
		if err != nil {
			break
		}
	}
	_ = p.cmd.Wait() // la raison, s'il y en a une, est dans Stderr
	if ctx.Err() != nil {
		return // arrêt demandé : pas d'alerte
	}
	st := model.MachineStatus{Schema: model.Schema, Type: "machine_status", Machine: p.machine(),
		Reachable: false, Since: model.Timestamp(time.Now()), Reason: lastLine(p.stderr.String())}
	select {
	case p.events <- Event{Machine: st.Machine, Status: &st}:
	case <-ctx.Done():
	}
}

// Stderr renvoie les derniers octets (4 Kio au plus) de la sortie d'erreur du
// processus : la raison d'un arrêt, par exemple un échec de connexion SSH.
func (p *Process) Stderr() string { return p.stderr.String() }

func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > maxLine {
			return nil, fmt.Errorf("ligne de plus de %d octets", maxLine)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return bytes.TrimSpace(buf), err
	}
}

func (p *Process) write(stdin io.WriteCloser) {
	defer stdin.Close()
	for {
		select {
		case <-p.done:
			return
		case req := <-p.reqs:
			b, err := json.Marshal(req)
			if err != nil {
				continue
			}
			if _, err := stdin.Write(append(b, '\n')); err != nil {
				return
			}
		}
	}
}

// Events implémente Source.
func (p *Process) Events() <-chan Event { return p.events }

// Send implémente Source. machine doit être celle du processus.
func (p *Process) Send(machine string, req model.Request) error {
	if name := p.machine(); machine != name {
		return fmt.Errorf("machine inconnue : %s (cette source est %s)", machine, name)
	}
	select {
	case <-p.done:
		return ErrClosed
	case p.reqs <- req:
		return nil
	default:
		return ErrBusy
	}
}

// Close implémente Source : arrête le processus.
func (p *Process) Close() error {
	p.once.Do(p.cancel)
	return nil
}

// lastLine renvoie la dernière ligne non vide de s, ou « le processus s'est
// arrêté » s'il n'y en a pas.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return "le processus s'est arrêté"
}

// tail garde les derniers octets écrits (sortie d'erreur d'un processus).
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

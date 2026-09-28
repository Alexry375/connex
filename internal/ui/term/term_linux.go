//go:build linux

package term

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// Terminal est le terminal de l'utilisateur, ouvert sur l'entrée et la sortie
// standard.
type Terminal struct {
	in, out *os.File
	saved   syscall.Termios
	mu      sync.Mutex
	raw     bool
	entered bool
}

// ErrNotTerminal : l'entrée ou la sortie n'est pas un terminal.
var ErrNotTerminal = errors.New("l'entrée ou la sortie n'est pas un terminal")

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// IsTerminal dit si f est un terminal.
func IsTerminal(f *os.File) bool {
	var t syscall.Termios
	return ioctl(f.Fd(), syscall.TCGETS, unsafe.Pointer(&t)) == nil
}

// Open prend le terminal si l'entrée et la sortie en sont un.
func Open(in, out *os.File) (*Terminal, error) {
	if !IsTerminal(in) || !IsTerminal(out) {
		return nil, ErrNotTerminal
	}
	return &Terminal{in: in, out: out}, nil
}

// Raw passe le terminal en mode brut (touches reçues une à une, sans écho ni
// signaux : Ctrl-C arrive comme une touche).
func (t *Terminal) Raw() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.raw {
		return nil
	}
	fd := t.in.Fd()
	if err := ioctl(fd, syscall.TCGETS, unsafe.Pointer(&t.saved)); err != nil {
		return err
	}
	raw := t.saved
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, syscall.TCSETS, unsafe.Pointer(&raw)); err != nil {
		return err
	}
	t.raw = true
	return nil
}

// Enter passe sur l'écran alternatif, cache le curseur et coupe le retour à la
// ligne automatique (la dernière case peut s'écrire sans faire défiler).
func (t *Terminal) Enter() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entered = true
	_, err := t.out.WriteString("\x1b[?1049h\x1b[?25l\x1b[?7l\x1b[0m\x1b[H\x1b[2J")
	return err
}

// Restore remet le terminal comme il était : écran principal, curseur visible,
// retour à la ligne, mode normal. On peut l'appeler plusieurs fois.
func (t *Terminal) Restore() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var first error
	if t.entered {
		_, first = t.out.WriteString("\x1b[?2026l\x1b[0m\x1b[?7h\x1b[?25h\x1b[?1049l")
		t.entered = false
	}
	if t.raw {
		if err := ioctl(t.in.Fd(), syscall.TCSETS, unsafe.Pointer(&t.saved)); err != nil && first == nil {
			first = err
		}
		t.raw = false
	}
	return first
}

// Size renvoie la taille en colonnes et en lignes.
func (t *Terminal) Size() (w, h int, err error) {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(t.out.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0, 0, err
	}
	return int(ws.Col), int(ws.Row), nil
}

// Write écrit sur la sortie du terminal.
func (t *Terminal) Write(b []byte) (int, error) { return t.out.Write(b) }

// Read lit l'entrée du terminal.
func (t *Terminal) Read(b []byte) (int, error) { return t.in.Read(b) }

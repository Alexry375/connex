// Package term prépare le terminal pour l'affichage de connex : mode brut, écran
// secondaire, taille de la fenêtre, puis remise en état à la sortie. Il n'utilise
// que la bibliothèque standard (ioctl par syscall, Linux).
package term

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// ErrNotTerminal : l'entrée ou la sortie n'est pas un terminal.
var ErrNotTerminal = errors.New("l'affichage a besoin d'un terminal (essaie « connex ls » ou « connex demo »)")

// Séquences d'entrée et de sortie de l'affichage : écran secondaire, curseur
// caché, pas de retour à la ligne automatique (écrire la dernière case de
// l'écran ne doit pas le faire défiler).
const (
	enterSeq = "\x1b[?1049h\x1b[?25l\x1b[?7l\x1b[0m\x1b[2J"
	leaveSeq = "\x1b[0m\x1b[?7h\x1b[?25h\x1b[?1049l"
)

// Terminal est un terminal pris en main par l'affichage.
type Terminal struct {
	in, out *os.File
	saved   syscall.Termios
	mu      sync.Mutex
	active  bool
}

func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

func getTermios(fd uintptr) (syscall.Termios, error) {
	var t syscall.Termios
	err := ioctl(fd, syscall.TCGETS, unsafe.Pointer(&t))
	return t, err
}

// IsTerminal dit si f est un terminal.
func IsTerminal(f *os.File) bool {
	_, err := getTermios(f.Fd())
	return err == nil
}

// Open vérifie que in et out sont des terminaux. Il ne change encore rien.
func Open(in, out *os.File) (*Terminal, error) {
	if !IsTerminal(in) || !IsTerminal(out) {
		return nil, ErrNotTerminal
	}
	return &Terminal{in: in, out: out}, nil
}

// Enter passe en mode brut et sur l'écran secondaire. Les touches arrivent
// alors une à une, sans écho ; Ctrl-C arrive comme une touche.
func (t *Terminal) Enter() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, err := getTermios(t.in.Fd())
	if err != nil {
		return err
	}
	t.saved = st
	raw := st
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(t.in.Fd(), syscall.TCSETS, unsafe.Pointer(&raw)); err != nil {
		return err
	}
	t.active = true
	_, err = t.out.WriteString(enterSeq)
	return err
}

// Restore rend le terminal dans l'état où il était : écran principal, curseur
// visible, mode ligne. On peut l'appeler plusieurs fois.
func (t *Terminal) Restore() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return nil
	}
	t.active = false
	_, werr := t.out.WriteString(leaveSeq)
	err := ioctl(t.in.Fd(), syscall.TCSETS, unsafe.Pointer(&t.saved))
	if err != nil {
		return err
	}
	return werr
}

type winsize struct{ Row, Col, X, Y uint16 }

// Size renvoie la taille de la fenêtre, en colonnes et en lignes.
func (t *Terminal) Size() (int, int, error) {
	var ws winsize
	if err := ioctl(t.out.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0, 0, err
	}
	return int(ws.Col), int(ws.Row), nil
}

// Write écrit une image déjà composée.
func (t *Terminal) Write(b []byte) (int, error) { return t.out.Write(b) }

//go:build !linux

package term

import (
	"errors"
	"os"
)

// Terminal n'est pris en charge que sous Linux, la seule cible de connex.
type Terminal struct{}

// ErrNotTerminal : pas de terminal utilisable.
var ErrNotTerminal = errors.New("terminal non pris en charge sur ce système")

// IsTerminal renvoie toujours faux hors de Linux.
func IsTerminal(*os.File) bool { return false }

// Open échoue hors de Linux : l'affichage passe en texte simple.
func Open(in, out *os.File) (*Terminal, error) { return nil, ErrNotTerminal }

// Raw n'est pas pris en charge.
func (t *Terminal) Raw() error { return ErrNotTerminal }

// Enter n'est pas pris en charge.
func (t *Terminal) Enter() error { return ErrNotTerminal }

// Restore ne fait rien.
func (t *Terminal) Restore() error { return nil }

// Size n'est pas pris en charge.
func (t *Terminal) Size() (int, int, error) { return 0, 0, ErrNotTerminal }

// Write n'est pas pris en charge.
func (t *Terminal) Write(b []byte) (int, error) { return 0, ErrNotTerminal }

// Read n'est pas pris en charge.
func (t *Terminal) Read(b []byte) (int, error) { return 0, ErrNotTerminal }

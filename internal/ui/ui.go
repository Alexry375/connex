// Package ui est l'affichage de connex dans le terminal (lot L4) : un retable
// d'après Hilma af Klint, où le soleil dit qui t'attend, où chaque machine est
// un astre, et où tout ce qui tourne se range en marches.
//
// L'affichage ne lit que sa source : il ne regarde ni /proc, ni git, ni SSH, ni
// aucun fichier, et ses seules actions sont les requêtes de la liste fermée
// (start, stop, restart, free_port, refresh).
package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/canvas"
	"github.com/Alexry375/connex/internal/ui/term"
)

// ErrSourceClosed : la source a fermé son flux alors que l'affichage tournait.
var ErrSourceClosed = errors.New("la source des données s'est arrêtée")

// Run affiche les machines de src jusqu'à la fin de ctx, la fermeture de la
// source, ou la demande de l'utilisateur. Il ne ferme pas src.
//
// Sans terminal (sortie ou entrée redirigée), il écrit la chronique en texte,
// une ligne par changement.
func Run(ctx context.Context, src source.Source) error {
	if !term.IsTerminal(os.Stdin) || !term.IsTerminal(os.Stdout) {
		return runPlain(ctx, src, os.Stdout)
	}
	t, err := term.Open(os.Stdin, os.Stdout)
	if err != nil {
		return runPlain(ctx, src, os.Stdout)
	}
	if err := t.Raw(); err != nil {
		return err
	}
	defer t.Restore()
	if err := t.Enter(); err != nil {
		return err
	}
	in := make(chan []byte, 16)
	go readInput(t, in)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	return loop(ctx, src, t, in, winch)
}

// screen est ce que la boucle sait du terminal.
type screen interface {
	io.Writer
	Size() (w, h int, err error)
}

// readInput lit l'entrée standard et passe les octets à la boucle.
func readInput(r io.Reader, out chan<- []byte) {
	buf := make([]byte, 256)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			b := make([]byte, n)
			copy(b, buf[:n])
			out <- b
		}
		if err != nil {
			close(out)
			return
		}
	}
}

// escWait est le temps laissé à une séquence d'échappement coupée en deux
// lectures pour se compléter, avant de prendre l'Échap pour une vraie touche.
const escWait = 30 * time.Millisecond

// loop est la boucle de l'affichage : événements de la source, touches,
// changements de taille, et images au rythme de ce qui bouge.
func loop(ctx context.Context, src source.Source, scr screen, in <-chan []byte, winch <-chan os.Signal) error {
	now := time.Now()
	a := newApp(src, now)
	w, h, err := scr.Size()
	if err != nil || w <= 0 || h <= 0 {
		w, h = 80, 24
	}
	a.W, a.H = w, h
	c := canvas.New(w, h)
	r := canvas.NewRenderer(scr)
	events := src.Events()

	var held []byte // un Échap seul en fin de lecture, en attente
	var escTimer *time.Timer
	var escC <-chan time.Time
	frame := time.NewTimer(0)
	defer frame.Stop()
	next := now
	dirty := true

	feed := func(b []byte) {
		buf := append(held, b...)
		held = nil
		if escTimer != nil {
			escTimer.Stop()
			escC = nil
		}
		if n := len(buf); n > 0 && buf[n-1] == 0x1b && (n == 1 || buf[n-2] != 0x1b) {
			held = []byte{0x1b}
			buf = buf[:n-1]
			escTimer = time.NewTimer(escWait)
			escC = escTimer.C
		}
		keys, rest := term.Decode(buf)
		if len(rest) > 0 {
			held = append(rest, held...)
			if escC == nil {
				escTimer = time.NewTimer(escWait)
				escC = escTimer.C
			}
		}
		a.now = time.Now()
		for _, k := range keys {
			a.key(k)
			if a.quit {
				return
			}
		}
		dirty = true
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return a.w.closed()
			}
			t := time.Now()
			a.event(ev, t)
			// ce qui est déjà arrivé passe dans la même image
			for more := true; more; {
				select {
				case ev, ok := <-events:
					if !ok {
						more = false
						events = nil
						break
					}
					a.event(ev, t)
				default:
					more = false
				}
			}
			dirty = true
		case b, ok := <-in:
			if !ok {
				in = nil
				continue
			}
			feed(b)
		case <-escC:
			escC = nil
			if len(held) > 0 {
				keys, _ := term.Decode(held)
				held = nil
				a.now = time.Now()
				for _, k := range keys {
					a.key(k)
				}
				dirty = true
			}
		case <-winch:
			if nw, nh, err := scr.Size(); err == nil && nw > 0 && nh > 0 {
				w, h = nw, nh
			}
			a.W, a.H = w, h
			c = canvas.New(w, h)
			r.Invalidate()
			dirty = true
		case <-frame.C:
		}
		if a.quit {
			return nil
		}
		t := time.Now()
		a.tick(t)
		if dirty || !t.Before(next) {
			a.draw(c)
			if _, err := r.Render(c); err != nil {
				return fmt.Errorf("écriture dans le terminal : %w", err)
			}
			dirty = false
			next = t.Add(a.frameEvery(t))
		}
		frame.Reset(max(time.Until(next), time.Millisecond))
		if events == nil && ctx.Err() == nil {
			return a.w.closed()
		}
	}
}

// frameEvery dit dans combien de temps peindre l'image suivante : vite quand
// quelque chose bouge, une fois par seconde sinon (les « il y a 12 s »).
func (a *app) frameEvery(now time.Time) time.Duration {
	if a.opening {
		return 40 * time.Millisecond
	}
	for _, t := range a.flips {
		if now.Sub(t) < eclipseSpan+100*time.Millisecond {
			return 40 * time.Millisecond
		}
	}
	for _, r := range a.rays() {
		if r.fresh > 0 {
			return 50 * time.Millisecond
		}
	}
	for _, p := range a.w.pending {
		if now.Sub(p.at) < silentAfter {
			return 125 * time.Millisecond // le signe de l'attente tourne
		}
	}
	if a.mode == modeDetail || a.mode == modeConfirm || a.typing {
		return 125 * time.Millisecond
	}
	for i := range a.all {
		if e := &a.all[i]; e.app != nil && e.app.State == "starting" {
			return 125 * time.Millisecond
		}
	}
	if a.waitingCount() > 0 && a.mode != modeChron {
		return 125 * time.Millisecond // l'or respire
	}
	return time.Second
}

// runPlain écrit la chronique en texte : une ligne par changement, précédée
// du temps écoulé depuis le lancement.
func runPlain(ctx context.Context, src source.Source, out io.Writer) error {
	w := newWorld()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-src.Events():
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return w.closed()
			}
			before := w.added
			now := time.Now()
			w.apply(ev, now)
			n := min(w.added-before, len(w.chron))
			for _, r := range w.chron[len(w.chron)-n:] {
				where := ""
				if r.id.kind != "machine" {
					where = r.id.machine + " · "
				}
				el := int(now.Sub(start).Seconds())
				if _, err := fmt.Fprintf(out, "+%02d:%02d  %s%s\n", el/60, el%60, where, r.text); err != nil {
					return nil // la sortie est fermée (connex --demo | head)
				}
			}
		}
	}
}

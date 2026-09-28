// Package ui est l'affichage de connex dans le terminal (lot L4) : un atlas
// céleste des machines, dans la manière des planches de Trouvelot.
//
// Tout ce qui est affiché vient de la source ; l'interface ne lit rien d'autre
// sur la machine et n'exécute aucune commande. Ses seules actions sont les
// requêtes de la liste fermée (start, stop, restart, free_port, refresh),
// envoyées par Source.Send, et l'ouverture d'une appli (touche o), confiée à
// Options.Open.
package ui

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/canvas"
	"github.com/Alexry375/connex/internal/ui/term"
)

// Options règle ce que l'affichage peut faire hors de la source.
type Options struct {
	// Open ouvre une appli (voir cmd/connex/uiopen.go) ; nil : rien ne s'ouvre (démo).
	Open func(machine, app string, snaps map[string]model.Snapshot) (string, error)
}

// Run vaut RunWith(ctx, src, Options{}) : rien ne s'ouvre.
func Run(ctx context.Context, src source.Source) error {
	return RunWith(ctx, src, Options{})
}

// RunWith affiche les machines de src jusqu'à la fin de ctx ou jusqu'à ce
// que l'utilisateur quitte (q ou Ctrl-C). Si la source s'arrête, l'affichage le dit et
// reste ouvert sur le dernier état connu. RunWith ne ferme pas src. La touche o
// appelle opts.Open, et elle seule.
func RunWith(ctx context.Context, src source.Source, opts Options) error {
	if !term.IsTerminal(os.Stdin) || !term.IsTerminal(os.Stdout) {
		return term.ErrNotTerminal
	}
	t, err := term.Open(os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	if err := t.Enter(); err != nil {
		t.Restore() // le mode brut a pu passer avant l'erreur
		return err
	}
	defer t.Restore()
	// Même sur une panique, le terminal est rendu propre avant le message.
	defer func() {
		if r := recover(); r != nil {
			t.Restore()
			panic(r)
		}
	}()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	keys := make(chan term.Key, 64)
	done := make(chan struct{})
	defer close(done)
	go term.ReadKeys(os.Stdin, keys, done)

	return loop(ctx, src, opts, t, keys, winch)
}

// screen est ce que la boucle demande au terminal.
type screen interface {
	Write(b []byte) (int, error)
	Size() (int, int, error)
}

// loop est la boucle unique de l'affichage : événements de la source, touches,
// changements de taille, et minuterie réglée sur la prochaine étape visible.
// Au repos, elle dort jusqu'à la prochaine image utile.
func loop(ctx context.Context, src source.Source, opts Options, scr screen, keys <-chan term.Key, winch <-chan os.Signal) error {
	a := newApp(src, time.Now())
	a.open = opts.Open
	w, h, err := scr.Size()
	if err != nil {
		return err
	}
	a.resize(w, h)
	r := canvas.NewRenderer()
	events := src.Events()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		now := time.Now()
		if out := r.Frame(a.frame(now)); out != nil {
			if _, err := scr.Write(out); err != nil {
				return err
			}
		}
		if a.quit {
			return nil
		}
		timer.Reset(a.nextWake(now))
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				events = nil
				a.sourceClosed(time.Now())
				continue
			}
			a.event(ev, time.Now())
			// Une rafale d'événements ne coûte qu'une image.
			for i := 0; i < 256 && events != nil; i++ {
				select {
				case ev, ok := <-events:
					if !ok {
						events = nil
						a.sourceClosed(time.Now())
						break
					}
					a.event(ev, time.Now())
					continue
				default:
				}
				break
			}
		case k := <-keys:
			a.key(k, time.Now())
			for i := 0; i < 64 && !a.quit; i++ {
				select {
				case k := <-keys:
					a.key(k, time.Now())
					continue
				default:
				}
				break
			}
		case <-winch:
			if w, h, err := scr.Size(); err == nil {
				a.resize(w, h)
			}
			r.Invalidate()
		case <-timer.C:
		}
	}
}

// frame compose l'image de l'instant now.
func (a *App) frame(now time.Time) *canvas.Canvas {
	cv := a.cv
	a.expire(now)
	if a.tiny() {
		a.drawTiny(cv, now)
		return cv
	}
	switch a.view {
	case viewPlate:
		a.drawPlate(cv, now)
	case viewJournal:
		a.drawJournal(cv, now)
	case viewLegend:
		a.drawLegend(cv, now)
	default:
		a.drawMain(cv, now)
	}
	if !a.introOver {
		if now.Sub(a.opened) >= introDur {
			a.introOver = true
		} else {
			a.drawIntro(cv, now)
		}
	}
	return cv
}

// nextWake dit quand la prochaine image change. Pendant l'ouverture et le vol
// d'une météore : 25 images par seconde ; pendant une action : de quoi faire
// tourner l'indicateur ; au repos : un pas de 250 ms sur la planche I (rayons,
// respiration), sinon la seconde suivante (durées affichées). Une question
// posée se réveille quand elle s'arme, pour écrire « Entrée : oui », et quand
// elle expire, pour se retirer.
func (a *App) nextWake(now time.Time) time.Duration {
	d := a.stepWake(now)
	if c := a.confirm; c != nil {
		if !c.armed(now) {
			d = min(d, confirmArm-now.Sub(c.at))
		}
		d = min(d, confirmTTL-now.Sub(c.at))
	}
	return d
}

func (a *App) stepWake(now time.Time) time.Duration {
	if !a.introOver && now.Sub(a.opened) < introDur {
		return 40 * time.Millisecond
	}
	if a.view == viewSky {
		for _, m := range a.meteors {
			if now.Sub(m.born) < meteorFlight {
				return 40 * time.Millisecond
			}
		}
	}
	if len(a.pending) > 0 {
		return 120 * time.Millisecond
	}
	if a.view == viewSky && !a.tiny() {
		return stepDur - now.Sub(a.opened)%stepDur
	}
	return time.Second - time.Duration(now.Nanosecond())%time.Second
}

// drawTiny : la fenêtre est trop petite pour une planche ; on dit l'essentiel,
// et la dernière rangée garde le moyen de sortir.
func (a *App) drawTiny(cv *canvas.Canvas, now time.Time) {
	cv.Clear(landBG)
	_, title, tn := a.title()
	fg := ink
	if tn == toneCall {
		fg = gold
	}
	last := a.H
	if a.H >= 2 {
		last = a.H - 1
		cv.Text(1, last, a.W, "q quitte.", inkDim, canvas.Italic)
	}
	y := 0
	for _, line := range wrap(title, max(1, a.W-2)) {
		if y >= last {
			return
		}
		cv.Text(1, y, a.W, line, fg, canvas.Bold)
		y++
	}
	// Espaces insécables : « 40 × 12 » ne se coupe pas en fin de ligne.
	for _, line := range wrap("Agrandis la fenêtre : "+itoa(minW)+"\u00a0×\u00a0"+itoa(minH)+" au moins.", max(1, a.W-2)) {
		if y >= last {
			return
		}
		cv.Text(1, y, a.W, line, inkDim, canvas.Italic)
		y++
	}
}

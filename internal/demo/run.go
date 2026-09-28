package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"path"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/render"
)

// Options règle le simulateur.
type Options struct {
	Out      io.Writer
	JSON     bool          // lignes JSON (§4) plutôt que du texte
	Duration time.Duration // durée réelle du flux ; 0 = les relevés seulement
	Speed    float64       // secondes simulées par seconde réelle
	Seed     int64         // même graine, même histoire
}

// Run écrit le relevé des trois machines, puis, si Duration > 0, le flux
// d'événements en temps réel (accéléré par Speed) jusqu'au bout de Duration.
func Run(ctx context.Context, o Options) error {
	start := time.Now()
	s := newSim(rand.New(rand.NewPCG(uint64(o.Seed), 0x636f6e6e6578)), start)
	for _, snap := range s.snapshots() {
		if err := emit(o, snap); err != nil {
			return err
		}
	}
	if o.Duration <= 0 {
		return nil
	}
	end := start.Add(o.Duration)
	horizon := o.Duration.Seconds() * o.Speed
	for {
		at := s.peek()
		if at > horizon {
			sleepUntil(ctx, end)
			return nil
		}
		due := start.Add(time.Duration(at / o.Speed * float64(time.Second)))
		if !sleepUntil(ctx, due) {
			return nil // Ctrl-C ou SIGTERM : on s'arrête proprement
		}
		for _, line := range s.step() {
			if err := emit(o, line); err != nil {
				return err
			}
		}
	}
}

// sleepUntil attend t. Il renvoie false si ctx est annulé avant (Ctrl-C).
func sleepUntil(ctx context.Context, t time.Time) bool {
	timer := time.NewTimer(time.Until(t))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func emit(o Options, v any) error {
	if o.JSON {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = o.Out.Write(append(b, '\n'))
		return err
	}
	switch v := v.(type) {
	case model.Snapshot:
		if err := render.Snapshot(o.Out, v, "/home/"+v.Machine.User); err != nil {
			return err
		}
		_, err := fmt.Fprintln(o.Out)
		return err
	case model.Patch:
		for _, c := range v.Changes {
			if _, err := fmt.Fprintf(o.Out, "%s  %-16s %s\n", clockOf(v.At), v.Machine, describe(c)); err != nil {
				return err
			}
		}
	case model.MachineStatus:
		what := "⚠ injoignable"
		if v.Reachable {
			what = "de nouveau joignable"
		}
		_, err := fmt.Fprintf(o.Out, "%s  %-16s %s\n", clockOf(v.Since), v.Machine, what)
		return err
	}
	return nil
}

func clockOf(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("15:04:05")
}

// describe résume un changement en une ligne lisible.
func describe(c model.Change) string {
	sign := map[string]string{model.OpAdd: "+", model.OpUpdate: "~", model.OpRemove: "-"}[c.Op]
	switch d := c.Data.(type) {
	case model.Listener:
		who := fmt.Sprintf("uid %d", d.UID)
		if d.Mine {
			where := "hors projet"
			if d.ProjectRoot != "" {
				where = path.Base(d.ProjectRoot)
			}
			who = fmt.Sprintf("%s %d, %s", d.Process, d.PID, where)
		}
		return fmt.Sprintf("%s port %d %s (%s) %s", sign, d.Port, d.Addr, d.Exposure, who)
	case model.Repo:
		state := "propre"
		if d.Dirty != nil && *d.Dirty {
			state = "modifié"
		}
		var wts []string
		for _, w := range d.Worktrees {
			wts = append(wts, w.Branch)
		}
		s := fmt.Sprintf("%s dépôt %s : %s, %s", sign, path.Base(d.Path), d.Branch, state)
		if len(wts) > 0 {
			s += ", worktrees " + strings.Join(wts, " ")
		}
		return s
	case model.Agent:
		return fmt.Sprintf("%s agent %s %d %s dans %s", sign, d.Tool, d.PID, d.State, path.Base(d.CWD))
	}
	return fmt.Sprintf("%s %s %s", sign, c.Kind, c.Key)
}

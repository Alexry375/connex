package main

// Lot L2 : les trois machines. `connex ls --all`, `connex watch`, et la source
// des machines pour l'affichage.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/alert"
	"github.com/Alexry375/connex/internal/config"
	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/render"
	"github.com/Alexry375/connex/internal/source"
)

// lsAllWait : on n'attend pas une machine plus longtemps (contrat L2).
const lsAllWait = 12 * time.Second

const usageMachines = `  connex ls --all [--json] [--config F]
        un relevé de chaque machine de la configuration, la locale d'abord
  connex watch [--json] [--config F]
        le direct de toutes les machines
  connex run <nom> [--port N] [--view remote|train] -- <commande> [arguments…]
        inscrit une appli du dossier courant et la lance (unité systemd --user)
  connex start|stop|restart <nom> [--on MACHINE] [--config F]
  connex free <port> [--on MACHINE] [--config F]
        actions sur une appli ou un port, en local ou par le flux d'une machine
  connex rm <nom>
        arrête et désinscrit une appli locale
`

// loadConfig lit la configuration : le fichier donné, ou celui par défaut.
func loadConfig(path string) (config.Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.Config{}, err
	}
	return config.Load(path, home)
}

// sshProgram est le programme ssh : $CONNEX_SSH s'il est défini.
func sshProgram() string {
	if p := os.Getenv("CONNEX_SSH"); p != "" {
		return p
	}
	return "ssh"
}

// localStreamArgv est la commande du flux local : ce binaire, avec la même
// configuration.
func localStreamArgv(cfg config.Config) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	argv := []string{exe, "stream"}
	if cfg.Path != "" {
		argv = append(argv, "--config", cfg.Path)
	}
	return argv, nil
}

// targets renvoie les machines de la configuration, la locale d'abord.
func targets(cfg config.Config) []source.Target {
	ts := []source.Target{{Name: cfg.Name}}
	for _, m := range cfg.Machines {
		ts = append(ts, source.Target{Name: m.Name, SSH: m.SSH})
	}
	return ts
}

// connectTargets ouvre la source des machines données.
func connectTargets(ctx context.Context, cfg config.Config, ts []source.Target) (*source.Machines, error) {
	argv, err := localStreamArgv(cfg)
	if err != nil {
		return nil, err
	}
	return source.Connect(ctx, source.ConnectOptions{Targets: ts, LocalArgv: argv, Dist: cfg.Dist, SSH: sshProgram()})
}

// machinesSource est la source de l'affichage quand la configuration a des
// machines distantes.
func machinesSource(ctx context.Context, cfg config.Config) (source.Source, error) {
	return connectTargets(ctx, cfg, targets(cfg))
}

// runList est `connex ls` : la machine locale avec ses applis, ou toutes les
// machines avec --all.
func runList(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "une ligne JSON (§4.1) par machine au lieu du tableau")
	all := fs.Bool("all", false, "toutes les machines de la configuration")
	cfgPath := fs.String("config", "", "fichier de configuration (défaut : ~/.config/connex/config.toml)")
	var roots rootFlag
	fs.Var(&roots, "root", "racine où chercher les dépôts (répétable ; machine locale seule)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *all {
		if len(roots) > 0 {
			return fmt.Errorf("%w : --root ne s'emploie pas avec --all", errUsage)
		}
		return runLsAll(ctx, *cfgPath, *asJSON, out)
	}
	col, _, err := localCollector(ctx, *cfgPath, roots)
	if err != nil {
		return err
	}
	snap := col.Collect(ctx, true)
	if *asJSON {
		return writeJSON(out, snap)
	}
	home, _ := os.UserHomeDir()
	return writeSnapshot(out, snap, home)
}

func writeJSON(out io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = out.Write(append(b, '\n'))
	return err
}

// writeSnapshot écrit un relevé lisible, avec ses applis.
func writeSnapshot(out io.Writer, s model.Snapshot, home string) error {
	if err := render.Snapshot(out, s, home); err != nil {
		return err
	}
	if len(s.Apps) == 0 {
		return nil
	}
	fmt.Fprintf(out, "\nAPPLIS (%d)\n", len(s.Apps))
	for _, a := range s.Apps {
		fmt.Fprintf(out, "  %-20s %-9s port %-5d pid %-5s écoute %-3s http %-3d %s\n", a.Name, a.State, a.Port,
			yesNo(a.Health.PIDAlive), yesNo(a.Health.PortListening), a.Health.HTTP, a.Unit)
	}
	return nil
}

func yesNo(b bool) string {
	if b {
		return "oui"
	}
	return "non"
}

// runLsAll fait un relevé de chaque machine par la source des machines, et
// n'attend pas une machine plus de 12 s.
func runLsAll(ctx context.Context, cfgPath string, asJSON bool, out io.Writer) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	ts := targets(cfg)
	src, err := connectTargets(ctx, cfg, ts)
	if err != nil {
		return err
	}
	results := make([]*source.Event, len(ts))
	index := map[string]int{}
	for i, t := range ts[1:] {
		index[t.Name] = i + 1
	}
	// La machine locale peut porter le nom qu'elle annonce : ce qui n'est pas
	// une machine distante est elle.
	slot := func(machine string) int {
		if i, ok := index[machine]; ok {
			return i
		}
		return 0
	}
	missing := len(ts)
	deadline := time.NewTimer(lsAllWait)
	defer deadline.Stop()
wait:
	for missing > 0 {
		select {
		case <-ctx.Done():
			break wait
		case <-deadline.C:
			break wait
		case ev, ok := <-src.Events():
			if !ok {
				break wait
			}
			if ev.Snapshot == nil && (ev.Status == nil || ev.Status.Reachable) {
				continue
			}
			if i := slot(ev.Machine); results[i] == nil {
				results[i] = &ev
				missing--
			}
		}
	}
	src.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	home, _ := os.UserHomeDir()
	for i, t := range ts {
		ev := results[i]
		if ev == nil {
			name := t.Name
			if name == "" {
				name, _ = os.Hostname()
			}
			st := model.MachineStatus{Schema: model.Schema, Type: "machine_status", Machine: name, Reachable: false,
				Since: model.Timestamp(time.Now()), Reason: fmt.Sprintf("pas de réponse en %d s", int(lsAllWait/time.Second))}
			ev = &source.Event{Machine: name, Status: &st}
		}
		switch {
		case asJSON && ev.Snapshot != nil:
			err = writeJSON(out, ev.Snapshot)
		case asJSON:
			err = writeJSON(out, ev.Status)
		case ev.Snapshot != nil:
			h := ""
			if i == 0 {
				h = home
			}
			err = writeSnapshot(out, *ev.Snapshot, h)
			if err == nil {
				// Tout port Funnel, sur toute machine ; le reste, sur une machine publique.
				// La machine locale peut n'avoir pas de nom dans la configuration :
				// l'alerte prend celui que le flux annonce.
				name := ev.Machine
				if name == "" {
					name = ev.Snapshot.Machine.Name
				}
				for _, a := range alert.Find(name, *ev.Snapshot, i > 0 && cfg.Public(t.Name)) {
					if _, err = fmt.Fprintln(out, a.Line()); err != nil {
						return err
					}
				}
			}
			fmt.Fprintln(out)
		default:
			_, err = fmt.Fprintf(out, "%s · injoignable : %s\n\n", ev.Status.Machine, ev.Status.Reason)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// runWatch écrit le direct de toutes les machines jusqu'à SIGINT ou SIGTERM.
func runWatch(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "chaque événement en ligne JSON (§4) au lieu du texte")
	cfgPath := fs.String("config", "", "fichier de configuration (défaut : ~/.config/connex/config.toml)")
	if err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	src, err := connectTargets(ctx, cfg, targets(cfg))
	if err != nil {
		return err
	}
	defer src.Close()
	f := fleet.New()
	alerts := alert.NewTracker()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-src.Events():
			if !ok {
				return nil
			}
			if *asJSON {
				if err := writeJSON(out, eventValue(ev)); err != nil {
					return err
				}
				continue
			}
			now := time.Now()
			for _, c := range f.Apply(ev, now) {
				if _, err := fmt.Fprintf(out, "%s  %-14s %s\n", now.Format("15:04:05"), c.Machine, describe(f, ev, c)); err != nil {
					return err
				}
			}
			// Alerte d'exposition : au premier relevé, puis à chaque port qui apparaît.
			if m := f.Machine(ev.Machine); m != nil && m.Known && (ev.Snapshot != nil || ev.Patch != nil) {
				for _, a := range alerts.New(ev.Machine, m.Snapshot, cfg.Public(ev.Machine)) {
					if _, err := fmt.Fprintln(out, a.Line()); err != nil {
						return err
					}
				}
			}
		}
	}
}

// eventValue renvoie la ligne du §4 portée par l'événement.
func eventValue(ev source.Event) any {
	switch {
	case ev.Snapshot != nil:
		return ev.Snapshot
	case ev.Patch != nil:
		return ev.Patch
	case ev.Status != nil:
		return ev.Status
	}
	return ev.Reply
}

// describe dit en une ligne ce qui change.
func describe(f *fleet.Fleet, ev source.Event, c fleet.Change) string {
	m := f.Machine(c.Machine)
	if c.Kind == fleet.KindMachine {
		switch {
		case m == nil:
			return "machine"
		case !m.Reachable && ev.Status != nil:
			return "injoignable : " + ev.Status.Reason
		case !m.Reachable:
			return "injoignable"
		case c.Op == model.OpAdd:
			return "apparaît"
		}
		return "joignable"
	}
	sign := map[string]string{model.OpAdd: "+", model.OpUpdate: "~", model.OpRemove: "−"}[c.Op]
	if m == nil || c.Op == model.OpRemove {
		return fmt.Sprintf("%s %s %s", sign, kindLabel(c.Kind), c.Key)
	}
	s := m.Snapshot
	switch c.Kind {
	case model.KindListener:
		for _, l := range s.Listeners {
			if l.Key == c.Key {
				who := l.Program()
				if !l.Mine {
					who = fmt.Sprintf("uid %d", l.UID)
				}
				return fmt.Sprintf("%s port %d %s (%s)", sign, l.Port, strings.TrimSpace(who), render.Exposure(l))
			}
		}
	case model.KindApp:
		for _, a := range s.Apps {
			if a.Key == c.Key {
				return fmt.Sprintf("%s appli %s : %s, port %d, http %d", sign, a.Name, a.State, a.Port, a.Health.HTTP)
			}
		}
	case model.KindRepo:
		for _, r := range s.Repos {
			if r.Key == c.Key {
				return fmt.Sprintf("%s dépôt %s : %s, %s", sign, filepath.Base(r.Path), r.Branch, render.RepoState(r))
			}
		}
	case model.KindAgent:
		for _, a := range s.Agents {
			if a.Key == c.Key {
				return fmt.Sprintf("%s agent %s : %s", sign, a.Key, render.AgentState(a.State))
			}
		}
	}
	return fmt.Sprintf("%s %s %s", sign, kindLabel(c.Kind), c.Key)
}

func kindLabel(kind string) string {
	switch kind {
	case model.KindListener:
		return "port"
	case model.KindRepo:
		return "dépôt"
	case model.KindApp:
		return "appli"
	}
	return kind
}

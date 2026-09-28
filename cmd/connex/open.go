package main

// Lot L5 : `connex open` ouvre une appli dans le navigateur du laptop. L'adresse
// est choisie par internal/address ; ce fichier fait le relevé, lit le fichier
// du miroir et lance xdg-open.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Alexry375/connex/internal/address"
	"github.com/Alexry375/connex/internal/config"
	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
)

const usageL5 = `  connex open <nom> [--on MACHINE] [--print] [--config F]
        ouvre l'appli dans le navigateur, par Tailscale ou par le miroir du mode train
  connex train <nom> [--stop] [--config F]
        lance (ou arrête) le miroir apercu-vps de l'appli de la machine train = true
`

// openWait : délai laissé au programme d'ouverture.
const openWait = 10 * time.Second

// mirrorDir est le dossier où apercu-vps écrit <nom>.url, relatif à $HOME.
const mirrorDir = ".local/state/miroir"

// mirrorMax : un fichier .url plus long n'a pas la forme permise.
const mirrorMax = 4096

// machineRef est une machine de la configuration, résolue.
type machineRef struct {
	name   string // nom pour les messages
	local  bool
	target source.Target // pour une machine distante
}

// resolveMachine trouve la machine de --on : la locale si on est vide ou vaut
// son nom, sinon une machine de la configuration.
func resolveMachine(cfg config.Config, on string) (machineRef, error) {
	if on == "" || on == cfg.Name {
		name := cfg.Name
		if name == "" {
			name, _ = os.Hostname()
		}
		return machineRef{name: name, local: true}, nil
	}
	for _, m := range cfg.Machines {
		if m.Name == on {
			return machineRef{name: m.Name, target: source.Target{Name: m.Name, SSH: m.SSH}}, nil
		}
	}
	return machineRef{}, fmt.Errorf("machine inconnue dans la configuration : %q", on)
}

// freshSnapshot prend un relevé frais de la machine : en local comme `connex
// ls`, à distance par une session comme `ls --all`, 12 s au plus.
func freshSnapshot(ctx context.Context, cfg config.Config, cfgPath string, m machineRef) (model.Snapshot, error) {
	if m.local {
		col, _, err := localCollector(ctx, cfgPath, nil)
		if err != nil {
			return model.Snapshot{}, err
		}
		snap := col.Collect(ctx, true)
		if ctx.Err() != nil {
			return model.Snapshot{}, errors.New("relevé interrompu")
		}
		return snap, nil
	}
	src, err := connectTargets(ctx, cfg, []source.Target{m.target})
	if err != nil {
		return model.Snapshot{}, err
	}
	defer src.Close()
	deadline := time.NewTimer(lsAllWait)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return model.Snapshot{}, errors.New("relevé interrompu")
		case <-deadline.C:
			return model.Snapshot{}, fmt.Errorf("%s est injoignable : pas de réponse en %d s", m.name, int(lsAllWait/time.Second))
		case ev, ok := <-src.Events():
			switch {
			case !ok:
				return model.Snapshot{}, fmt.Errorf("%s : session fermée", m.name)
			case ev.Snapshot != nil:
				return *ev.Snapshot, nil
			case ev.Status != nil && !ev.Status.Reachable:
				return model.Snapshot{}, fmt.Errorf("%s est injoignable : %s", m.name, ev.Status.Reason)
			}
		}
	}
}

// findApp renvoie l'appli nommée du relevé, ou nil.
func findApp(s model.Snapshot, name string) *model.App {
	for i := range s.Apps {
		if s.Apps[i].Name == name {
			return &s.Apps[i]
		}
	}
	return nil
}

// runOpen est `connex open <nom> [--on M] [--print] [--config F]`.
func runOpen(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	on := fs.String("on", "", "machine de la configuration où tourne l'appli (défaut : la machine locale)")
	printOnly := fs.Bool("print", false, "écrire l'adresse seule, sans rien lancer")
	cfgPath := fs.String("config", "", "fichier de configuration (défaut : ~/.config/connex/config.toml)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("%w : connex open <nom> [--on MACHINE] [--print] [--config F]", errUsage)
	}
	name := pos[0]
	if !config.ValidName(name) {
		return fmt.Errorf("nom d'appli invalide : %q", name)
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	m, err := resolveMachine(cfg, *on)
	if err != nil {
		return err
	}
	snap, err := freshSnapshot(ctx, cfg, *cfgPath, m)
	if err != nil {
		return err
	}
	in := address.Input{App: name, Machine: m.name, Local: m.local, Snapshot: snap}
	if a := findApp(snap, name); a != nil && a.View == model.ViewTrain {
		if in.Mirror, err = readMirror(name); err != nil {
			return err
		}
		in.LaptopListeners = snap.Listeners
		if !m.local {
			laptop, err := freshSnapshot(ctx, cfg, *cfgPath, machineRef{name: cfg.Name, local: true})
			if err != nil {
				return err
			}
			in.LaptopListeners = laptop.Listeners
		}
	}
	url, err := address.For(in)
	if err != nil {
		return err
	}
	if *printOnly {
		_, err = fmt.Fprintln(out, url)
		return err
	}
	fmt.Fprintf(out, "ouverture de %s\n", url)
	return openURL(ctx, url)
}

// readMirror lit $HOME/.local/state/miroir/<nom>.url, écrit par apercu-vps.
// Un fichier absent donne "" ; un fichier trop long, un contenu refusé.
func readMirror(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, mirrorDir, name+".url")
	// apercu-vps écrit un fichier ordinaire ; tout autre chose (lien, dossier,
	// tube qui bloquerait la lecture) vaut un miroir arrêté.
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, mirrorMax+1))
	if err != nil {
		return "", err
	}
	if len(b) > mirrorMax {
		return "", nil
	}
	return string(b), nil
}

// openerProgram est le programme d'ouverture : $CONNEX_OPEN s'il est défini.
func openerProgram() string {
	if p := os.Getenv("CONNEX_OPEN"); p != "" {
		return p
	}
	return "xdg-open"
}

// openURL lance le programme d'ouverture avec l'adresse pour seul argument,
// sans shell, entrée standard vide, 10 s au plus. Sa sortie standard n'est pas
// transmise : un navigateur qui en hériterait tiendrait ouverte celle de connex.
func openURL(ctx context.Context, url string) error {
	prog := openerProgram()
	tctx, cancel := context.WithTimeout(ctx, openWait)
	defer cancel()
	cmd := exec.CommandContext(tctx, prog, url)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, os.Stderr
	err := cmd.Run()
	switch {
	case ctx.Err() != nil:
		return errors.New("ouverture interrompue")
	case tctx.Err() != nil:
		return fmt.Errorf("%s : pas terminé en %d s", prog, int(openWait/time.Second))
	case err != nil:
		return fmt.Errorf("%s a échoué : %v", prog, err)
	}
	return nil
}

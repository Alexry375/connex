package main

// Lot L5 : `connex train` lance ou arrête le miroir apercu-vps d'une appli de la
// machine train = true (D7 : connex lance apercu-vps, il ne transporte jamais
// lui-même le contenu d'un projet).

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Alexry375/connex/internal/config"
	"github.com/Alexry375/connex/internal/source"
)

// mirrorDirRe : apercu-vps transmet le dossier à un shell distant ; aucun
// caractère de shell n'y passe (D13).
var mirrorDirRe = regexp.MustCompile(`^[A-Za-z0-9._/@+-]+$`)

// exitCodeError fait sortir connex avec le code d'un programme lancé, sans
// message de plus (le programme a déjà parlé).
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("code de sortie %d", e.code) }

// checkMirrorDir dit si le dossier d'une appli peut être passé à apercu-vps :
// absolu, sans segment . ni .., et fait des seuls caractères A-Z a-z 0-9 . _ / @ + -.
// La racine seule (« / ») est refusée aussi : apercu-vps en retire le / final et
// ferait alors le miroir de tout le HOME du VPS.
func checkMirrorDir(dir string) bool {
	if !strings.HasPrefix(dir, "/") || !mirrorDirRe.MatchString(dir) {
		return false
	}
	named := false
	for _, seg := range strings.Split(dir, "/") {
		if seg == "." || seg == ".." {
			return false
		}
		named = named || seg != ""
	}
	return named
}

// apercuProgram est le programme du miroir : $CONNEX_APERCU s'il est défini.
func apercuProgram() string {
	if p := os.Getenv("CONNEX_APERCU"); p != "" {
		return p
	}
	return "apercu-vps"
}

// runTrain est `connex train <nom> [--stop] [--config F]`.
func runTrain(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("train", flag.ContinueOnError)
	stop := fs.Bool("stop", false, "arrêter le miroir de l'appli")
	cfgPath := fs.String("config", "", "fichier de configuration (défaut : ~/.config/connex/config.toml)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("%w : connex train <nom> [--stop] [--config F]", errUsage)
	}
	name := pos[0]
	if !config.ValidName(name) {
		return fmt.Errorf("nom d'appli invalide : %q", name)
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	tm, ok := cfg.TrainMachine()
	if !ok {
		return errors.New("aucune machine en mode train dans la configuration (clé train = true)")
	}
	if *stop {
		return runApercu(ctx, out, name, "stop")
	}
	m := machineRef{name: tm.Name, target: source.Target{Name: tm.Name, SSH: tm.SSH}}
	snap, err := freshSnapshot(ctx, cfg, *cfgPath, m)
	if err != nil {
		return err
	}
	a := findApp(snap, name)
	if a == nil {
		return fmt.Errorf("aucune appli « %s » sur %s", name, tm.Name)
	}
	if !checkMirrorDir(a.CWD) {
		return fmt.Errorf("dossier de « %s » refusé pour le miroir : seuls A-Z a-z 0-9 . _ / @ + - sont permis", name)
	}
	return runApercu(ctx, out, a.CWD, "--nom", name)
}

// runApercu lance apercu-vps en tableau d'arguments, entrée standard vide,
// sorties transmises, sans délai imposé. Un Ctrl-C (annulation de ctx) lui est
// relayé ; son code de sortie devient celui de connex.
func runApercu(ctx context.Context, out io.Writer, args ...string) error {
	prog := apercuProgram()
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, out, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ee):
		if st, ok := ee.Sys().(syscall.WaitStatus); ok && st.Signaled() {
			return exitCodeError{128 + int(st.Signal())}
		}
		if code := ee.ExitCode(); code > 0 {
			return exitCodeError{code}
		}
	}
	return fmt.Errorf("%s : %v", prog, err)
}

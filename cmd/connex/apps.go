package main

// Lot L3 : les applis inscrites. `connex run`, `start`, `stop`, `restart`,
// `free`, `rm`, et le flux qui les relève et exécute leurs requêtes.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/Alexry375/connex/internal/apps"
	"github.com/Alexry375/connex/internal/collect"
	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/stream"
)

// Délais d'une action à distance : la session, puis la réponse.
const (
	remoteConnectWait = 45 * time.Second
	remoteReplyWait   = 60 * time.Second
)

// localCollector prépare le relevé local avec les applis, et le Manager qui agit
// sur elles.
func localCollector(ctx context.Context, cfgPath string, cliRoots []string) (*apps.Collector, *apps.Manager, error) {
	// roots se lit comme au L1 (collect.RootsFrom), dans le fichier de --config
	// s'il y en a un ; ce fichier doit d'abord être une configuration valide.
	if cfgPath != "" {
		if _, err := loadConfig(cfgPath); err != nil {
			return nil, nil, err
		}
	}
	c, err := collect.New(ctx, collect.Options{Roots: cliRoots, Config: cfgPath})
	if err != nil {
		return nil, nil, err
	}
	reg, err := apps.DefaultRegistry()
	if err != nil {
		return nil, nil, err
	}
	m := apps.NewManager(reg, "") // nommé au premier relevé (apps.Collector)
	return &apps.Collector{Inner: c, Manager: m}, m, nil
}

// localManager est le Manager des applis de cette machine, sans relevé.
func localManager(ctx context.Context) (*apps.Manager, error) {
	reg, err := apps.DefaultRegistry()
	if err != nil {
		return nil, err
	}
	m, _ := collect.Identify(ctx)
	return apps.NewManager(reg, m.Name), nil
}

// runStreamApps est `connex stream` : le flux de L1, avec les applis dans le
// relevé et leurs requêtes exécutées.
func runStreamApps(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	interval := fs.Duration("interval", 2*time.Second, "délai entre deux relectures de la machine")
	cfgPath := fs.String("config", "", "fichier de configuration (racines des dépôts)")
	var roots rootFlag
	fs.Var(&roots, "root", "racine où chercher les dépôts (répétable)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *interval < 100*time.Millisecond {
		return fmt.Errorf("%w : --interval doit valoir au moins 100ms", errUsage)
	}
	col, m, err := localCollector(ctx, *cfgPath, roots)
	if err != nil {
		return err
	}
	return stream.RunWith(ctx, col, in, out, *interval, m)
}

// parseMixed lit les drapeaux où qu'ils soient et renvoie les arguments
// positionnels.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(os.Stderr)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, fmt.Errorf("%w : %v", errUsage, err)
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// runRun est `connex run <nom> [--port N] [--view remote|train] -- <commande…>`.
func runRun(ctx context.Context, args []string, out io.Writer) error {
	sep := slices.Index(args, "--")
	if sep < 0 || sep == len(args)-1 {
		return fmt.Errorf("%w : connex run <nom> [--port N] [--view remote|train] -- <commande> [arguments…]", errUsage)
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	port := fs.Int("port", 0, "port de l'appli (défaut : un port stable choisi par connex)")
	view := fs.String("view", model.ViewRemote, "façon de la regarder : remote ou train")
	pos, err := parseMixed(fs, args[:sep])
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("%w : un seul nom d'appli attendu avant --", errUsage)
	}
	if *port < 0 || *port > 65535 || (*port == 0 && isSet(fs, "port")) {
		return fmt.Errorf("%w : --port doit être entre 1 et 65535", errUsage)
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	m, err := localManager(ctx)
	if err != nil {
		return err
	}
	e, err := m.Register(apps.RunOptions{Name: pos[0], Port: *port, View: *view, Command: args[sep+1:],
		Dir: dir, PathEnv: os.Getenv("PATH")})
	if err != nil {
		return err
	}
	if err := m.Start(ctx, e.Name); err != nil {
		return fmt.Errorf("« %s » inscrite sur le port %d, mais pas lancée : %w", e.Name, e.Port, err)
	}
	fmt.Fprintf(out, "« %s » inscrite sur le port %d et lancée (%s)\n", e.Name, e.Port, e.Unit())
	return nil
}

func isSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// runAction est `connex start|stop|restart <nom>` et `connex free <port>`, en
// local ou, avec --on, par une requête dans le flux de la machine.
func runAction(ctx context.Context, op string, args []string, out io.Writer) error {
	name := op
	if op == model.ReqFreePort {
		name = "free"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	on := fs.String("on", "", "machine de la configuration où agir (défaut : la machine locale)")
	cfgPath := fs.String("config", "", "fichier de configuration (défaut : ~/.config/connex/config.toml)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		what := "<nom>"
		if op == model.ReqFreePort {
			what = "<port>"
		}
		return fmt.Errorf("%w : connex %s %s [--on MACHINE] [--config F]", errUsage, name, what)
	}
	target := pos[0]
	if *on == "" {
		m, err := localManager(ctx)
		if err != nil {
			return err
		}
		if refusal := m.Validate(op, target); refusal != "" {
			return errors.New(refusal)
		}
		if err := m.Do(ctx, op, target); err != nil {
			return err
		}
		fmt.Fprintln(out, doneMessage(op, target, ""))
		return nil
	}
	return remoteAction(ctx, *cfgPath, *on, op, target, out)
}

func doneMessage(op, target, machine string) string {
	where := ""
	if machine != "" {
		where = " sur " + machine
	}
	switch op {
	case model.ReqStart:
		return fmt.Sprintf("« %s » démarrée%s", target, where)
	case model.ReqStop:
		return fmt.Sprintf("« %s » arrêtée%s", target, where)
	case model.ReqRestart:
		return fmt.Sprintf("« %s » relancée%s", target, where)
	}
	return fmt.Sprintf("port %s libéré%s", target, where)
}

// remoteAction envoie la requête dans le flux de la machine et attend sa réponse.
func remoteAction(ctx context.Context, cfgPath, machine, op, target string, out io.Writer) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	var t *source.Target
	if machine == cfg.Name {
		t = &source.Target{Name: cfg.Name}
	}
	for _, m := range cfg.Machines {
		if m.Name == machine {
			t = &source.Target{Name: m.Name, SSH: m.SSH}
		}
	}
	if t == nil {
		return fmt.Errorf("machine inconnue dans la configuration : %q", machine)
	}
	src, err := connectTargets(ctx, cfg, []source.Target{*t})
	if err != nil {
		return err
	}
	defer src.Close()
	id := "cli-" + strconv.Itoa(os.Getpid())
	connect := time.NewTimer(remoteConnectWait)
	defer connect.Stop()
	var replyWait <-chan time.Time
	sent := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-connect.C:
			if !sent {
				return fmt.Errorf("%s : pas de relevé en %d s", machine, int(remoteConnectWait/time.Second))
			}
		case <-replyWait:
			return fmt.Errorf("%s : pas de réponse en %d s", machine, int(remoteReplyWait/time.Second))
		case ev, ok := <-src.Events():
			if !ok {
				return fmt.Errorf("%s : session fermée", machine)
			}
			switch {
			case ev.Status != nil && !ev.Status.Reachable:
				return fmt.Errorf("%s est injoignable : %s", machine, ev.Status.Reason)
			case ev.Snapshot != nil && !sent:
				if err := src.Send(machine, source.NewRequest(id, op, target)); err != nil {
					return err
				}
				sent = true
				replyWait = time.After(remoteReplyWait)
			case ev.Reply != nil && ev.Reply.ID == id:
				if !ev.Reply.OK {
					return errors.New(ev.Reply.Error)
				}
				fmt.Fprintln(out, doneMessage(op, target, machine))
				return nil
			}
		}
	}
}

// runRm est `connex rm <nom>` : en local seulement.
func runRm(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("%w : connex rm <nom>", errUsage)
	}
	m, err := localManager(ctx)
	if err != nil {
		return err
	}
	if err := m.Remove(ctx, pos[0]); err != nil {
		return err
	}
	fmt.Fprintf(out, "« %s » désinscrite\n", pos[0])
	return nil
}

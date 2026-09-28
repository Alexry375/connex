// connex montre ce qui tourne sur une machine : ports en écoute, dépôts git,
// sessions d'agents. Ce lot (L1) fournit le moteur d'une machine : ls, stream,
// et le simulateur demo.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Alexry375/connex/internal/collect"
	"github.com/Alexry375/connex/internal/demo"
	"github.com/Alexry375/connex/internal/render"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/stream"
	"github.com/Alexry375/connex/internal/ui"
)

const usage = `connex — ce qui tourne sur tes machines

  connex [--demo [--speed N] [--seed N]]
        l'affichage en direct ; --demo le branche sur le simulateur
  connex ls [--json] [--root DOSSIER]…
        relevé de la machine locale : ports, dépôts, agents
  connex stream [--interval 2s] [--root DOSSIER]…
        relevé puis événements en JSON ; requêtes sur l'entrée standard
  connex demo [--json] [--for DURÉE] [--speed N] [--seed N]
        simulateur : trois machines inventées, puis un flux d'événements
  connex help agents
        le mode d'emploi de connex pour les agents (Claude Code, Codex)
`

// guideAgents est le mode d'emploi de `connex help agents`, embarqué dans le binaire.
//
//go:embed guide_agents.txt
var guideAgents string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	var err error
	if len(os.Args) < 2 || (strings.HasPrefix(os.Args[1], "-") && os.Args[1] != "-h" && os.Args[1] != "--help") {
		err = runUI(ctx, os.Args[1:])
		exit(err)
		return
	}
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "ls":
		err = runList(ctx, args, os.Stdout)
	case "stream":
		err = runStreamApps(ctx, args, os.Stdin, os.Stdout)
	case "demo":
		err = runDemo(ctx, args, os.Stdout)
	case "watch":
		err = runWatch(ctx, args, os.Stdout)
	case "run":
		err = runRun(ctx, args, os.Stdout)
	case "start", "stop", "restart":
		err = runAction(ctx, cmd, args, os.Stdout)
	case "free":
		err = runAction(ctx, "free_port", args, os.Stdout)
	case "rm":
		err = runRm(ctx, args, os.Stdout)
	case "open":
		err = runOpen(ctx, args, os.Stdout)
	case "train":
		err = runTrain(ctx, args, os.Stdout)
	case "help", "-h", "--help":
		switch {
		case len(args) == 1 && args[0] == "agents":
			fmt.Print(guideAgents)
		case len(args) == 0:
			fmt.Print(usage + usageMachines + usageL5)
		default:
			fmt.Fprintf(os.Stderr, "aide inconnue : %s (essaie connex help agents)\n", strings.Join(args, " "))
			os.Exit(2)
		}
		return
	default:
		fmt.Fprintf(os.Stderr, "commande inconnue : %s\n\n%s%s%s", cmd, usage, usageMachines, usageL5)
		os.Exit(2)
	}
	exit(err)
}

// exit termine avec le code qui correspond à err.
func exit(err error) {
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	var code exitCodeError
	if errors.As(err, &code) {
		os.Exit(code.code)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "connex :", err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

var errUsage = errors.New("usage")

// rootFlag accepte --root plusieurs fois.
type rootFlag []string

func (r *rootFlag) String() string     { return strings.Join(*r, ",") }
func (r *rootFlag) Set(v string) error { *r = append(*r, v); return nil }

func parse(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w : %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w : argument inattendu %q", errUsage, fs.Arg(0))
	}
	return nil
}

func runLs(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "une ligne JSON (§4.1) au lieu du tableau")
	var roots rootFlag
	fs.Var(&roots, "root", "racine où chercher les dépôts (répétable)")
	if err := parse(fs, args); err != nil {
		return err
	}
	c, err := collect.New(ctx, collect.Options{Roots: roots})
	if err != nil {
		return err
	}
	snap := c.Collect(ctx, true)
	if ctx.Err() != nil { // Ctrl-C en cours de relevé : il est incomplet
		return errors.New("relevé interrompu")
	}
	if *asJSON {
		b, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		_, err = out.Write(append(b, '\n'))
		return err
	}
	home, _ := os.UserHomeDir()
	return render.Snapshot(out, snap, home)
}

func runStream(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	interval := fs.Duration("interval", 2*time.Second, "délai entre deux relectures de la machine")
	var roots rootFlag
	fs.Var(&roots, "root", "racine où chercher les dépôts (répétable)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *interval < 100*time.Millisecond {
		return fmt.Errorf("%w : --interval doit valoir au moins 100ms", errUsage)
	}
	c, err := collect.New(ctx, collect.Options{Roots: roots})
	if err != nil {
		return err
	}
	return stream.Run(ctx, c, in, out, *interval)
}

func runDemo(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "lignes JSON au lieu du texte")
	dur := fs.Duration("for", 0, "durée du flux d'événements (0 : les relevés seulement)")
	speed := fs.Float64("speed", 1, "accélération du temps simulé (60 : une minute par seconde)")
	seed := fs.Int64("seed", 0, "graine du hasard (0 : au hasard)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *speed <= 0 {
		return fmt.Errorf("%w : --speed doit être positif", errUsage)
	}
	if *seed == 0 {
		*seed = time.Now().UnixNano()
	}
	return demo.Run(ctx, demo.Options{
		Out:      out,
		JSON:     *asJSON,
		Duration: *dur,
		Speed:    *speed,
		Seed:     *seed,
	})
}

// runUI lance l'affichage : sur la machine locale, ou sur le simulateur avec --demo.
func runUI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connex", flag.ContinueOnError)
	useDemo := fs.Bool("demo", false, "brancher l'affichage sur le simulateur")
	speed := fs.Float64("speed", 1, "avec --demo : accélération du temps simulé (0 : monde figé)")
	seed := fs.Int64("seed", 0, "avec --demo : graine du hasard (0 : au hasard)")
	cfgPath := fs.String("config", "", "fichier de configuration (défaut : ~/.config/connex/config.toml)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *speed < 0 {
		return fmt.Errorf("%w : --speed ne peut pas être négatif", errUsage)
	}
	var src source.Source
	var opts ui.Options // la démo n'ouvre rien
	if *useDemo {
		if *seed == 0 {
			*seed = time.Now().UnixNano()
		}
		src = source.NewDemo(ctx, source.DemoOptions{Seed: *seed, Speed: *speed})
	} else {
		cfg, err := loadConfig(*cfgPath)
		if err != nil {
			return err
		}
		if len(cfg.Machines) > 0 {
			src, err = machinesSource(ctx, cfg)
		} else {
			src, err = source.NewLocal(ctx)
		}
		if err != nil {
			return err
		}
		opts.Open = uiOpener(cfg)
	}
	defer src.Close()
	return ui.RunWith(ctx, src, opts)
}

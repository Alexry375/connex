// Package collect fait le relevé de la machine locale : ports en écoute et leurs
// processus (/proc), dépôts git (git --no-optional-locks), sessions d'agents.
// Il ne lit ni ligne de commande ni environnement de processus, ni contenu de
// fichier de projet, ni conversation d'agent.
package collect

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// Collector relève l'état de la machine. Il garde un peu de mémoire entre deux
// relevés (propriétaires des sockets, état des dépôts) pour que stream reste
// sobre ; ls n'en fait qu'un.
type Collector struct {
	procRoot  string
	devRoot   string // /dev si vide (les tests en fabriquent un)
	home      string
	uid       int
	roots     []string
	machine   model.Machine
	known     bool          // machine identifiée
	fixedErrs []model.Error // erreurs de configuration et d'identification
	owners    *socketOwners
	repos     *repoTracker
	now       func() time.Time
	mu        sync.Mutex

	// Exposition (lot L7). Les tests remplacent ces lectures ; nil ou vide :
	// net.Interfaces, /sys/devices/virtual/net et `tailscale serve status --json`.
	ifaces     func() ([]netIface, error)
	sysNet     string
	readFunnel func(ctx context.Context, program string) map[int]bool
	funnel     map[int]bool // ports publiés par Funnel au dernier appel
	funnelAt   time.Time
}

// Options règle un Collector.
type Options struct {
	Home     string   // $HOME ; os.UserHomeDir() si vide
	Roots    []string // --root ; vide = configuration ou défaut
	Config   string   // fichier de configuration (--config) ; vide = le fichier par défaut
	ProcRoot string   // /proc si vide (les tests en fabriquent un)
	// RepoBudget est le nombre de dépôts ordinaires relus par passage de stream,
	// en plus des nouveaux et de ceux où travaille un agent. 8 si nul.
	RepoBudget int
}

// New prépare un Collector. L'identité de la machine est lue au premier relevé,
// sous le contexte de celui-ci : stream peut ainsi l'interrompre.
func New(ctx context.Context, o Options) (*Collector, error) {
	if o.Home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		o.Home = h
	}
	if o.ProcRoot == "" {
		o.ProcRoot = "/proc"
	}
	if o.RepoBudget <= 0 {
		o.RepoBudget = 8
	}
	roots, errs := RootsFrom(o.Home, o.Config, o.Roots)
	uid := os.Getuid()
	return &Collector{
		procRoot:  o.ProcRoot,
		home:      o.Home,
		uid:       uid,
		roots:     roots,
		fixedErrs: errs,
		owners:    newSocketOwners(o.ProcRoot, uid),
		repos:     newRepoTracker(newWalker(o.Home, nil), o.RepoBudget),
		now:       time.Now,
	}, nil
}

// Collect fait un relevé. full relit tous les dépôts ; sinon seule une tranche
// des dépôts inchangés est relue (voir repoTracker). Si ctx est annulé en
// cours de route, le relevé est incomplet et ne doit pas être utilisé.
func (c *Collector) Collect(ctx context.Context, full bool) model.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.known {
		m, errs := Identify(ctx)
		c.machine = m // au pire le nom d'hôte, si l'arrêt a coupé tailscale
		if ctx.Err() == nil {
			c.known = true
			c.fixedErrs = append(c.fixedErrs, errs...)
		}
	}
	snap := model.NewSnapshot(c.machine, c.now())
	snap.Errors = append(snap.Errors, c.fixedErrs...)

	// Les montages et les dossiers interdits sont revus à chaque passage : un
	// sshfs monté, ou un ~/.codex créé, en cours de session reste hors du parcours.
	mounts := networkMounts(c.procRoot)
	c.repos.walker = newWalker(c.home, mounts)
	socks, lerrs := c.readSockets()
	procs, err := listProcs(c.procRoot)
	if err != nil {
		snap.Errors = append(snap.Errors, model.Error{Source: "listeners", Message: err.Error()})
	}
	listeners := c.collectListeners(socks, procs, mounts, c.localHostAddrs())
	MarkFunnel(listeners, c.funnelPorts(ctx))
	agents := c.collectAgents(procs)
	snap.Listeners = listeners
	snap.Agents = agents
	snap.Errors = append(snap.Errors, lerrs...)

	var hot []string
	for _, a := range agents {
		hot = append(hot, a.CWD)
	}
	for _, l := range listeners {
		if l.ProjectRoot != "" {
			hot = append(hot, l.ProjectRoot)
		}
	}
	repos, rerrs := c.repos.collect(ctx, c.roots, full, hot)
	snap.Repos = repos
	snap.Errors = append(snap.Errors, rerrs...)
	return snap
}

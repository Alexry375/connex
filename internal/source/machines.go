package source

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// Les options de ssh, exactement celles-ci et dans cet ordre (contrat L2).
// ControlPath=none : une connexion partagée restée bloquée après une coupure ne
// doit pas bloquer connex avec elle.
var sshOptions = []string{
	"-T",
	"-o", "BatchMode=yes",
	"-o", "ConnectTimeout=10",
	"-o", "ServerAliveInterval=5",
	"-o", "ServerAliveCountMax=6",
	"-o", "ControlPath=none",
}

// Les commandes distantes sont des constantes : rien de la configuration, d'une
// requête ni d'une appli n'y est jamais inséré. ssh les confie au shell de
// l'autre machine.
const (
	remoteStream = "~/.local/bin/connex stream"
	remoteProbe  = "uname -m; sha256sum ~/.local/bin/connex 2>/dev/null || true"
	remoteUpload = "mkdir -p ~/.local/bin && cat > ~/.local/bin/.connex.tmp && chmod 755 ~/.local/bin/.connex.tmp && mv -f ~/.local/bin/.connex.tmp ~/.local/bin/connex"
)

// Délais par défaut.
const (
	defaultMinBackoff   = time.Second
	defaultMaxBackoff   = 30 * time.Second
	defaultStableAfter  = 60 * time.Second
	defaultProbeTimeout = 20 * time.Second
	defaultUploadLimit  = 5 * time.Minute
	defaultFirstSnap    = 30 * time.Second
	stopGrace           = 3 * time.Second
)

// Target est une machine à suivre.
type Target struct {
	// Name est le nom de la configuration, porté par tous ses événements. Vide
	// pour la machine locale : elle garde le nom qu'elle annonce.
	Name string
	// SSH est la destination ssh ; vide pour la machine locale.
	SSH string
}

// ConnectOptions règle la source des machines.
type ConnectOptions struct {
	Targets   []Target // dans l'ordre de la configuration, la locale d'abord
	LocalArgv []string // flux local ; par défaut ce binaire avec « stream »
	Dist      string   // dossier des binaires connex-linux-<arch> à poser
	SSH       string   // programme ssh ; « ssh » si vide

	// Délais ; 0 : ceux du contrat.
	MinBackoff, MaxBackoff time.Duration
	StableAfter            time.Duration // une session qui a tenu plus longtemps remet l'attente au minimum
	ProbeTimeout           time.Duration // appel ssh qui lit l'architecture et l'empreinte
	UploadTimeout          time.Duration
	FirstSnapshot          time.Duration // délai pour le premier relevé d'une session
}

// Machines réunit les flux de plusieurs machines en une seule source : la
// machine locale par `connex stream`, les autres par ssh. Elle reconnecte seule
// une machine qui tombe, et pose le binaire sur une machine distante quand il y
// manque ou diffère de celui de Dist.
type Machines struct {
	o      ConnectOptions
	events chan Event
	links  []*link
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
	mu     sync.RWMutex
	closed bool
}

// Connect démarre la source. Chaque machine est suivie en parallèle : une
// machine lente ne retarde pas les autres.
func Connect(ctx context.Context, o ConnectOptions) (*Machines, error) {
	if o.SSH == "" {
		o.SSH = "ssh"
	}
	if len(o.LocalArgv) == 0 {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		o.LocalArgv = []string{exe, "stream"}
	}
	o.MinBackoff = orDefault(o.MinBackoff, defaultMinBackoff)
	o.MaxBackoff = orDefault(o.MaxBackoff, defaultMaxBackoff)
	o.StableAfter = orDefault(o.StableAfter, defaultStableAfter)
	o.ProbeTimeout = orDefault(o.ProbeTimeout, defaultProbeTimeout)
	o.UploadTimeout = orDefault(o.UploadTimeout, defaultUploadLimit)
	o.FirstSnapshot = orDefault(o.FirstSnapshot, defaultFirstSnap)
	names := map[string]bool{}
	for _, t := range o.Targets {
		if strings.HasPrefix(t.SSH, "-") {
			return nil, fmt.Errorf("destination ssh refusée pour %s : elle commence par « - »", t.Name)
		}
		if t.SSH != "" && t.Name == "" {
			return nil, fmt.Errorf("machine distante sans nom (%s)", t.SSH)
		}
		if t.Name != "" && names[t.Name] {
			return nil, fmt.Errorf("machine en double : %s", t.Name)
		}
		names[t.Name] = true
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Machines{o: o, events: make(chan Event, 64), ctx: ctx, cancel: cancel}
	for _, t := range o.Targets {
		l := &link{m: m, target: t, name: t.Name}
		if l.name == "" {
			l.name, _ = os.Hostname()
		}
		m.links = append(m.links, l)
		m.wg.Add(1)
		go l.run(ctx)
	}
	go func() {
		<-ctx.Done()
		m.finish()
	}()
	return m, nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Events implémente Source.
func (m *Machines) Events() <-chan Event { return m.events }

// Send implémente Source : la requête part dans le flux de la machine. Une
// machine injoignable donne une erreur immédiate.
func (m *Machines) Send(machine string, req model.Request) error {
	for _, l := range m.links {
		l.mu.Lock()
		name, proc, announced, ready := l.name, l.proc, l.announced, l.ready
		l.mu.Unlock()
		if name != machine {
			continue
		}
		if proc == nil || !ready {
			return fmt.Errorf("%s est injoignable", machine)
		}
		return proc.Send(announced, req)
	}
	return fmt.Errorf("machine inconnue : %s", machine)
}

// Close implémente Source : ferme toutes les sessions et attend qu'elles soient
// finies (ni ssh ni flux ne restent derrière).
func (m *Machines) Close() error {
	m.cancel()
	m.finish()
	return nil
}

// finish attend les machines puis ferme Events.
func (m *Machines) finish() {
	m.once.Do(func() {
		done := make(chan struct{})
		go func() { m.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * stopGrace):
		}
		m.mu.Lock()
		m.closed = true
		close(m.events)
		m.mu.Unlock()
	})
}

// emit envoie un événement, sauf si la source s'arrête.
func (m *Machines) emit(ctx context.Context, ev Event) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return
	}
	select {
	case m.events <- ev:
	case <-ctx.Done():
	}
}

// link suit une machine : sessions successives, avec attente entre deux essais.
type link struct {
	m      *Machines
	target Target

	mu        sync.Mutex
	name      string   // nom porté par les événements
	announced string   // nom annoncé par le flux (celui du Process)
	proc      *Process // session en cours, nil entre deux
	ready     bool     // relevé reçu dans la session en cours

	// suspect : la dernière session s'est finie sans donner de relevé (ssh
	// refusé, binaire absent, flux cassé…). La suivante n'annonce le retour
	// qu'avec son relevé, pour ne pas alterner joignable / injoignable à chaque
	// essai. Après une coupure en pleine session, le retour est annoncé dès que
	// ssh répond et que le bon binaire est en place.
	suspect bool
}

func (l *link) local() bool { return l.target.SSH == "" }

func (l *link) currentName() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.name
}

// nextBackoff double l'attente, sans dépasser max.
func nextBackoff(cur, max time.Duration) time.Duration {
	if cur*2 > max {
		return max
	}
	return cur * 2
}

func (l *link) run(ctx context.Context) {
	defer l.m.wg.Done()
	o := l.m.o
	backoff := o.MinBackoff
	down := false // un machine_status injoignable a été émis depuis la dernière session
	for {
		began := time.Now()
		reason := l.session(ctx, &down)
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) > o.StableAfter {
			backoff = o.MinBackoff
		}
		if !down {
			down = true
			st := model.MachineStatus{Schema: model.Schema, Type: "machine_status", Machine: l.currentName(),
				Reachable: false, Since: model.Timestamp(time.Now()), Reason: reason}
			l.m.emit(ctx, Event{Machine: st.Machine, Status: &st})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = nextBackoff(backoff, o.MaxBackoff)
	}
}

// session ouvre une session et relaie ses événements jusqu'à sa fin. Elle
// renvoie la raison de la fin.
func (l *link) session(ctx context.Context, down *bool) (reason string) {
	// back annonce le retour d'une machine qui était injoignable.
	gotSnapshot := false
	back := func() {
		if !*down {
			return
		}
		*down = false
		name := l.currentName()
		st := model.MachineStatus{Schema: model.Schema, Type: "machine_status", Machine: name,
			Reachable: true, Since: model.Timestamp(time.Now())}
		l.m.emit(ctx, Event{Machine: name, Status: &st})
	}
	defer func() { l.suspect = !gotSnapshot }()
	early := back // retour annoncé dès que la machine répond, binaire en place
	if l.suspect {
		early = func() {} // retour annoncé seulement avec un relevé
	}

	argv := l.m.o.LocalArgv
	if !l.local() {
		if reason := l.deploy(ctx); reason != "" {
			return reason
		}
		early()
		argv = sshArgv(l.m.o.SSH, l.target.SSH, remoteStream)
	}
	// Le processus a son propre contexte, détaché de ctx : à l'arrêt de la
	// source, stop l'arrête en douceur (SIGTERM) avant tout arrêt forcé.
	sctx, scancel := context.WithCancel(context.WithoutCancel(ctx))
	defer scancel()
	p, err := StartProcess(sctx, l.currentName(), argv...)
	if err != nil {
		if l.local() {
			return "flux local impossible à lancer : " + err.Error()
		}
		return "ssh impossible à lancer : " + err.Error()
	}
	l.mu.Lock()
	l.proc, l.ready, l.announced = p, false, l.name
	l.mu.Unlock()
	if l.local() {
		early() // le flux local est lancé
	}
	defer func() {
		l.mu.Lock()
		l.proc, l.ready = nil, false
		l.mu.Unlock()
	}()

	first := time.NewTimer(l.m.o.FirstSnapshot)
	defer first.Stop()
	for {
		select {
		case <-ctx.Done():
			p.stop(scancel)
			return ""
		case <-first.C:
			p.stop(scancel)
			return fmt.Sprintf("pas de relevé en %s", l.m.o.FirstSnapshot)
		case ev, ok := <-p.Events():
			if !ok {
				if reason == "" {
					reason = "le flux s'est arrêté"
				}
				return reason
			}
			switch {
			case ev.Status != nil:
				if !ev.Status.Reachable {
					reason = ev.Status.Reason
				}
			case ev.Snapshot != nil:
				first.Stop()
				gotSnapshot = true
				back() // sans effet si le retour est déjà annoncé
				l.mu.Lock()
				l.announced = ev.Snapshot.Machine.Name
				if l.target.Name != "" {
					ev.Snapshot.Machine.Name = l.target.Name
				} else {
					l.name = l.announced
				}
				name := l.name
				l.ready = true
				l.mu.Unlock()
				l.m.emit(ctx, Event{Machine: name, Snapshot: ev.Snapshot})
			case ev.Patch != nil:
				l.mu.Lock()
				name, ready := l.name, l.ready
				l.mu.Unlock()
				if !ready {
					continue // un patch avant le relevé n'a pas de sens
				}
				ev.Patch.Machine = name
				l.m.emit(ctx, Event{Machine: name, Patch: ev.Patch})
			case ev.Reply != nil:
				name := l.currentName()
				l.m.emit(ctx, Event{Machine: name, Reply: ev.Reply})
			}
		}
	}
}

// stop arrête le processus en douceur : SIGTERM (ssh ferme la session, le flux
// local s'arrête), puis, si rien n'a bougé après un délai, l'arrêt forcé par
// cancel. Il attend la fin du processus.
func (p *Process) stop(cancel context.CancelFunc) {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
	}
	timeout := time.After(stopGrace)
	for {
		select {
		case _, ok := <-p.events:
			if !ok {
				cancel()
				return
			}
		case <-timeout:
			cancel()
			// après l'arrêt forcé, la lecture s'arrête aussi ; on n'attend plus que brièvement
			select {
			case <-p.done:
			case <-time.After(stopGrace):
			}
			return
		}
	}
}

// sshArgv construit l'appel à ssh : les options du contrat, la destination en
// argument à part, puis la commande distante constante.
func sshArgv(prog, dest, remote string) []string {
	argv := append([]string{prog}, sshOptions...)
	return append(argv, dest, remote)
}

// goArch traduit la sortie de uname -m.
var goArch = map[string]string{"x86_64": "amd64", "aarch64": "arm64"}

var sumRe = regexp.MustCompile(`^([0-9a-f]{64})\s`)

// parseProbe lit la sortie de remoteProbe : la ligne de uname -m, puis celle de
// sha256sum si le binaire existe. Un .bashrc bavard peut écrire avant elles :
// l'architecture est la dernière ligne d'un seul mot avant l'empreinte.
func parseProbe(out string) (arch, sum string) {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := sumRe.FindStringSubmatch(line + " "); m != nil {
			return arch, m[1]
		}
		if line != "" && !strings.ContainsAny(line, " \t") {
			arch = line
		}
	}
	return arch, ""
}

// deploy vérifie le binaire en place sur la machine distante et le remplace
// s'il manque ou diffère de celui de Dist. Il renvoie une raison d'échec, vide
// si le bon binaire est en place.
func (l *link) deploy(ctx context.Context) string {
	o := l.m.o
	out, reason := l.ssh(ctx, o.ProbeTimeout, remoteProbe, nil)
	if reason != "" {
		return reason
	}
	arch, sum := parseProbe(out)
	ga, ok := goArch[arch]
	if !ok {
		if arch == "" {
			return "architecture illisible (uname -m)"
		}
		return fmt.Sprintf("architecture non prise en charge : %s", arch)
	}
	local := filepath.Join(o.Dist, "connex-linux-"+ga)
	want, err := sha256File(local)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Sprintf("binaire linux-%s absent de %s : lance scripts/install.sh", ga, o.Dist)
	}
	if err != nil {
		return fmt.Sprintf("binaire linux-%s illisible : %v", ga, err)
	}
	if sum == want {
		return ""
	}
	f, err := os.Open(local)
	if err != nil {
		return fmt.Sprintf("binaire linux-%s illisible : %v", ga, err)
	}
	_, reason = l.ssh(ctx, o.UploadTimeout, remoteUpload, f)
	f.Close()
	if reason != "" {
		return "envoi du binaire : " + reason
	}
	out, reason = l.ssh(ctx, o.ProbeTimeout, remoteProbe, nil)
	if reason != "" {
		return reason
	}
	if _, sum = parseProbe(out); sum != want {
		return fmt.Sprintf("le binaire posé n'a pas l'empreinte de %s", local)
	}
	return ""
}

// ssh lance une commande distante constante et renvoie sa sortie, ou une raison
// d'échec : la dernière ligne d'erreur de ssh, ou le délai dépassé.
func (l *link) ssh(ctx context.Context, timeout time.Duration, remote string, stdin io.Reader) (string, string) {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := sshArgv(l.m.o.SSH, l.target.SSH, remote)
	cmd := exec.CommandContext(tctx, argv[0], argv[1:]...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	var out bytes.Buffer
	var errb tail
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &out, &errb
	err := cmd.Run()
	switch {
	case ctx.Err() != nil:
		return "", "arrêt demandé"
	case tctx.Err() != nil:
		return "", fmt.Sprintf("pas de réponse de ssh en %s", timeout)
	case err != nil:
		return "", lastLine(errb.String())
	}
	return out.String(), ""
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

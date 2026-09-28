// Package alert trouve les ports ouverts à Internet, pour les signaler en clair
// dans la sortie texte de ls --all et de watch (§6.8, D13) :
//   - sur une machine joignable depuis Internet (public = true), les ports de
//     l'utilisateur en exposition all ;
//   - sur toute machine, les ports publiés par Tailscale Funnel, quel que soit
//     le compte qui les tient (lot L7).
//
// L'alerte n'est que du texte.
package alert

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Alexry375/connex/internal/model"
)

// Alert est un port ouvert à Internet. Une écoute IPv4 et une écoute IPv6 sur
// le même port font une seule alerte.
type Alert struct {
	Machine   string
	Port      int
	Processes []string // noms des programmes, triés, sans doublon
	Addrs     []string // adresses d'écoute, triées
	Funnel    bool     // publié sur Internet par Tailscale Funnel
}

// Find renvoie les alertes du relevé s de la machine nommée, par port croissant.
// public dit si la machine est joignable depuis Internet (public = true dans la
// configuration) : ses ports en exposition all sont alors signalés. Un port
// Funnel l'est toujours.
func Find(machine string, s model.Snapshot, public bool) []Alert {
	byPort := map[int]*Alert{}
	var ports []int
	for _, l := range s.Listeners {
		if !l.Funnel && (!public || !l.Mine || l.Exposure != model.ExposureAll) {
			continue
		}
		a := byPort[l.Port]
		if a == nil {
			a = &Alert{Machine: machine, Port: l.Port}
			byPort[l.Port] = a
			ports = append(ports, l.Port)
		}
		a.Funnel = a.Funnel || l.Funnel
		p := programOf(l)
		if !l.Mine {
			p = fmt.Sprintf("autre compte, uid %d", l.UID)
		}
		if p != "" && !slices.Contains(a.Processes, p) {
			a.Processes = append(a.Processes, p)
		}
		if !slices.Contains(a.Addrs, l.Addr) {
			a.Addrs = append(a.Addrs, l.Addr)
		}
	}
	slices.Sort(ports)
	out := make([]Alert, 0, len(ports))
	for _, p := range ports {
		a := byPort[p]
		slices.Sort(a.Processes)
		slices.Sort(a.Addrs)
		out = append(out, *a)
	}
	return out
}

// Line est la ligne affichée ; elle commence par ALERTE.
func (a Alert) Line() string {
	proc := "processus inconnu"
	if len(a.Processes) > 0 {
		proc = strings.Join(a.Processes, ", ")
	}
	if a.Funnel {
		return fmt.Sprintf("ALERTE %s : port %d (%s) publié sur Internet par Tailscale Funnel (%s) : joignable par n'importe qui",
			a.Machine, a.Port, proc, strings.Join(a.Addrs, ", "))
	}
	// « si le pare-feu le laisse passer » : connex voit l'écoute, pas le pare-feu. Le 28/09,
	// les serveurs du VPS en 0.0.0.0 étaient filtrés depuis Internet ; le port 22, lui, répondait.
	return fmt.Sprintf("ALERTE %s : port %d (%s) écoute sur toutes les interfaces (%s) : joignable depuis Internet si le pare-feu le laisse passer",
		a.Machine, a.Port, proc, strings.Join(a.Addrs, ", "))
}

// Tracker retient, pour watch, les ports déjà signalés de chaque machine.
type Tracker struct {
	seen map[string]map[int]bool
}

// NewTracker renvoie un Tracker vide.
func NewTracker() *Tracker { return &Tracker{seen: map[string]map[int]bool{}} }

// New renvoie les alertes du relevé courant d'une machine qui ne l'étaient pas
// au relevé précédent : toutes au premier relevé, puis chaque port qui
// apparaît. Un port qui disparaît puis revient est signalé de nouveau.
func (t *Tracker) New(machine string, s model.Snapshot, public bool) []Alert {
	cur := Find(machine, s, public)
	prev := t.seen[machine]
	next := make(map[int]bool, len(cur))
	var fresh []Alert
	for _, a := range cur {
		next[a.Port] = true
		if !prev[a.Port] {
			fresh = append(fresh, a)
		}
	}
	t.seen[machine] = next
	return fresh
}

// programOf nomme le programme d'une écoute comme l'affichage (Program), et
// ajoute entre parenthèses le programme lui-même quand c'est son unité qui le
// nomme : « notifbot (python3.12) ». Une alerte doit dire ce qui tourne.
func programOf(l model.Listener) string {
	p := strings.TrimSpace(l.Program())
	if l.Unit == "" {
		return p
	}
	inner := strings.TrimSpace(l.Process)
	if l.Exe != "" && model.IsThreadName(inner) {
		inner = l.Exe
	}
	if inner == "" || inner == p || model.IsThreadName(inner) {
		return p
	}
	return p + " (" + inner + ")"
}

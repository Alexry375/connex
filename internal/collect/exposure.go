package collect

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// Exposition « host » : une adresse portée par une interface virtuelle (pont
// Docker docker0, br-…, virbr…, veth…) n'est jointe que par la machine et ses
// conteneurs. On le sait par la bibliothèque standard et /sys, sans rien lancer.
// Deux sortes d'interfaces virtuelles mènent pourtant ailleurs et n'en sont pas :
// un lien point à point (VPN tun, WireGuard : il y a quelqu'un au bout) et un
// pont qui contient une carte physique (br0 avec eth0 : le réseau local le joint).

// sysVirtualNet liste les interfaces virtuelles de la machine.
const sysVirtualNet = "/sys/devices/virtual/net"

// netIface est une interface réseau et ses adresses.
type netIface struct {
	name  string
	addrs []netip.Addr
	p2p   bool // lien point à point (drapeau POINTOPOINT)
}

// readIfaces lit les interfaces de la machine par net.Interfaces et Addrs.
func readIfaces() ([]netIface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]netIface, 0, len(ifs))
	for _, ifc := range ifs {
		ni := netIface{name: ifc.Name, p2p: ifc.Flags&net.FlagPointToPoint != 0}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if pn, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(pn.IP); ok {
					ni.addrs = append(ni.addrs, ip.Unmap())
				}
			}
		}
		out = append(out, ni)
	}
	return out, nil
}

// hostAddrs renvoie les adresses des interfaces virtuelles autres que la boucle
// locale et Tailscale (une interface qui porte une adresse Tailscale est celle
// de Tailscale, quel que soit son nom), hors liens point à point et ponts qui
// contiennent une interface physique. virtual dit si une interface est listée
// sous /sys/devices/virtual/net/ ; members donne les interfaces d'un pont.
func hostAddrs(ifaces []netIface, virtual func(name string) bool, members func(name string) []string) map[netip.Addr]bool {
	out := map[netip.Addr]bool{}
	for _, ifc := range ifaces {
		if ifc.name == "lo" || ifc.p2p || !virtual(ifc.name) {
			continue
		}
		tail := false
		for _, a := range ifc.addrs {
			if tailnet4.Contains(a) || tailnet6.Contains(a) {
				tail = true
			}
		}
		for _, m := range members(ifc.name) {
			if !virtual(m) {
				tail = true // pont relié au réseau par une carte physique
			}
		}
		if tail {
			continue
		}
		for _, a := range ifc.addrs {
			if !a.IsLoopback() && !a.IsUnspecified() {
				out[a] = true
			}
		}
	}
	return out
}

// virtualIn dit si l'interface name est listée sous le dossier dir.
func virtualIn(dir string) func(string) bool {
	return func(name string) bool {
		if !ifaceName(name) {
			return false
		}
		_, err := os.Lstat(filepath.Join(dir, name))
		return err == nil
	}
}

// bridgeMembers lit les interfaces d'un pont dans dir/<pont>/brif/ ; rien pour
// une interface qui n'est pas un pont.
func bridgeMembers(dir string) func(string) []string {
	return func(name string) []string {
		if !ifaceName(name) {
			return nil
		}
		ents, err := os.ReadDir(filepath.Join(dir, name, "brif"))
		if err != nil {
			return nil
		}
		out := make([]string, 0, len(ents))
		for _, e := range ents {
			out = append(out, e.Name())
		}
		return out
	}
}

// ifaceName dit si name peut être un nom d'interface (pas de chemin).
func ifaceName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}

// localHostAddrs lit les adresses « host » de la machine ; une erreur de
// lecture laisse la liste vide : les écoutes gardent alors leur exposition
// ordinaire.
func (c *Collector) localHostAddrs() map[netip.Addr]bool {
	read := c.ifaces
	if read == nil {
		read = readIfaces
	}
	dir := c.sysNet
	if dir == "" {
		dir = sysVirtualNet
	}
	ifs, err := read()
	if err != nil {
		return map[netip.Addr]bool{}
	}
	return hostAddrs(ifs, virtualIn(dir), bridgeMembers(dir))
}

// ExposureOn classe une adresse d'écoute : celle d'une interface virtuelle
// (hors boucle locale et Tailscale) est host ; sinon Exposure.
func ExposureOn(a netip.Addr, host map[netip.Addr]bool) string {
	e := Exposure(a)
	if e == model.ExposureAll && host[a.Unmap()] {
		return model.ExposureHost
	}
	return e
}

// Tailscale Funnel publie sur Internet des couples hôte:port. connex les lit
// dans `tailscale serve status --json`, qui imprime la configuration ServeConfig
// de Tailscale (ipn/serve.go) : AllowFunnel est un objet {"hôte:port": true},
// et chaque session de premier plan (`tailscale funnel` sans --bg) a le sien
// sous Foreground. Sans configuration, la sortie est {} ou null.

// serveConfig est la partie utile de ipn.ServeConfig.
type serveConfig struct {
	AllowFunnel map[string]bool         `json:"AllowFunnel"`
	Foreground  map[string]*serveConfig `json:"Foreground"`
}

// parseFunnelPorts renvoie les ports publiés par Funnel. Un couple sans port
// lisible est ignoré.
func parseFunnelPorts(out []byte) (map[int]bool, error) {
	var sc *serveConfig
	if err := json.Unmarshal(out, &sc); err != nil {
		return nil, err
	}
	ports := map[int]bool{}
	var walk func(*serveConfig, int)
	walk = func(sc *serveConfig, depth int) {
		if sc == nil || depth > 1 { // Foreground ne contient pas d'autre Foreground
			return
		}
		for hp, on := range sc.AllowFunnel {
			if !on {
				continue
			}
			_, p, err := net.SplitHostPort(hp)
			if err != nil {
				continue
			}
			if n, err := strconv.ParseUint(p, 10, 16); err == nil && n > 0 {
				ports[int(n)] = true
			}
		}
		for _, fg := range sc.Foreground {
			walk(fg, depth+1)
		}
	}
	walk(sc, 0)
	return ports, nil
}

// tailscaleProgram est le programme tailscale : $CONNEX_TAILSCALE s'il est défini.
func tailscaleProgram() string {
	if p := os.Getenv("CONNEX_TAILSCALE"); p != "" {
		return p
	}
	return "tailscale"
}

// funnelTimeout borne l'appel à tailscale ; funnelTTL est la durée pendant
// laquelle stream garde la réponse avant de redemander.
const (
	funnelTimeout = 3 * time.Second
	funnelTTL     = 10 * time.Second
)

// readFunnelPorts lance `tailscale serve status --json` : tableau d'arguments,
// sans shell, entrée vide, 3 s au plus. C'est la seule commande tailscale de ce
// genre que connex lance. Absent ou en échec : aucun port, et pas d'erreur
// bloquante.
func readFunnelPorts(ctx context.Context, program string) map[int]bool {
	ctx, cancel := context.WithTimeout(ctx, funnelTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "serve", "status", "--json")
	cmd.Stdin = nil // /dev/null
	cmd.WaitDelay = 200 * time.Millisecond
	out, err := cmd.Output()
	if err != nil { // pas de tailscale, arrêté ou trop lent : pas de Funnel connu
		return map[int]bool{}
	}
	ports, err := parseFunnelPorts(out)
	if err != nil {
		return map[int]bool{}
	}
	return ports
}

// funnelPorts renvoie les ports publiés par Funnel, relus au plus toutes les
// funnelTTL.
func (c *Collector) funnelPorts(ctx context.Context) map[int]bool {
	now := c.now()
	if c.funnel != nil && now.Sub(c.funnelAt) < funnelTTL && now.Sub(c.funnelAt) >= 0 {
		return c.funnel
	}
	read := c.readFunnel
	if read == nil {
		read = readFunnelPorts
	}
	c.funnel, c.funnelAt = read(ctx, tailscaleProgram()), now
	return c.funnel
}

// MarkFunnel passe en exposition all, avec Funnel, chaque écoute à une adresse
// Tailscale sur un port publié par Funnel.
func MarkFunnel(ls []model.Listener, ports map[int]bool) {
	for i := range ls {
		if ports[ls[i].Port] && ls[i].Exposure == model.ExposureTailnet {
			ls[i].Exposure, ls[i].Funnel = model.ExposureAll, true
		}
	}
}

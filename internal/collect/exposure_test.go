package collect

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func addrs(ss ...string) []netip.Addr {
	var out []netip.Addr
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

// Les adresses host sont celles des interfaces virtuelles, reconnues par leur
// présence sous /sys/devices/virtual/net, quel que soit leur nom ou leur
// adresse : ni la boucle locale, ni Tailscale, ni une carte physique.
func TestHostAddrs(t *testing.T) {
	sys := t.TempDir()
	for _, n := range []string{"lo", "docker0", "br-3f2a", "virbr0", "veth12ab", "tailscale0", "pont-bizarre"} {
		must(t, os.Mkdir(filepath.Join(sys, n), 0o755))
	}
	ifaces := []netIface{
		{name: "lo", addrs: addrs("127.0.0.1", "::1")},
		{name: "wlp0s20f3", addrs: addrs("192.168.1.20", "fe80::1")},              // physique
		{name: "docker0", addrs: addrs("172.17.0.1", "fe80::42:acff:fe11:1")},     // pont Docker
		{name: "br-3f2a", addrs: addrs("172.18.0.1")},                             // réseau compose
		{name: "virbr0", addrs: addrs("192.168.122.1")},                           // libvirt
		{name: "veth12ab", addrs: addrs("fe80::9c3a:1ff:fe00:1")},                 // bout de conteneur
		{name: "tailscale0", addrs: addrs("100.122.145.72", "fd7a:115c:a1e0::1")}, // Tailscale
		{name: "pont-bizarre", addrs: addrs("10.99.0.1")},                         // virtuelle, nom quelconque
	}
	got := hostAddrs(ifaces, virtualIn(sys), bridgeMembers(sys))
	for _, a := range []string{"172.17.0.1", "fe80::42:acff:fe11:1", "172.18.0.1", "192.168.122.1", "fe80::9c3a:1ff:fe00:1", "10.99.0.1"} {
		if !got[netip.MustParseAddr(a)] {
			t.Errorf("%s devrait être host", a)
		}
	}
	for _, a := range []string{"127.0.0.1", "::1", "192.168.1.20", "fe80::1", "100.122.145.72", "fd7a:115c:a1e0::1"} {
		if got[netip.MustParseAddr(a)] {
			t.Errorf("%s ne devrait pas être host", a)
		}
	}
	// Pas d'interface virtuelle : rien n'est host, même 172.17.0.1.
	if got := hostAddrs([]netIface{{name: "eth0", addrs: addrs("172.17.0.1")}}, virtualIn(sys), bridgeMembers(sys)); len(got) != 0 {
		t.Errorf("carte physique en 172.17.0.1 : %v", got)
	}
	if virtualIn(sys)("../" + filepath.Base(sys)) {
		t.Error("un nom d'interface avec / sort du dossier")
	}
}

// Un VPN (tun, WireGuard : lien point à point) et un pont qui contient une
// carte physique sont virtuels, mais on les joint d'ailleurs : pas host. Un
// pont Docker dont les membres sont des veth reste host.
func TestHostAddrsNotElsewhere(t *testing.T) {
	sys := t.TempDir()
	for _, d := range []string{"gpd0", "wg0", "br0/brif/eth0", "docker0/brif/veth12ab", "veth12ab"} {
		must(t, os.MkdirAll(filepath.Join(sys, d), 0o755))
	}
	ifaces := []netIface{
		{name: "gpd0", addrs: addrs("10.212.134.7"), p2p: true},
		{name: "wg0", addrs: addrs("10.8.0.2"), p2p: true},
		{name: "br0", addrs: addrs("192.168.1.30")},
		{name: "docker0", addrs: addrs("172.17.0.1")},
	}
	got := hostAddrs(ifaces, virtualIn(sys), bridgeMembers(sys))
	for _, a := range []string{"10.212.134.7", "10.8.0.2", "192.168.1.30"} {
		if got[netip.MustParseAddr(a)] {
			t.Errorf("%s ne devrait pas être host", a)
		}
	}
	if !got[netip.MustParseAddr("172.17.0.1")] {
		t.Error("docker0 (membres veth) devrait rester host")
	}
}

func TestExposureOf(t *testing.T) {
	host := map[netip.Addr]bool{netip.MustParseAddr("172.17.0.1"): true}
	for a, want := range map[string]string{
		"172.17.0.1": "host", "::ffff:172.17.0.1": "host", "172.17.0.2": "all",
		"0.0.0.0": "all", "::": "all", "127.0.0.1": "loopback", "100.122.145.72": "tailnet",
	} {
		if got := ExposureOn(netip.MustParseAddr(a), host); got != want {
			t.Errorf("%s : %s ; attendu %s", a, got, want)
		}
	}
}

// Le format réel de ipn.ServeConfig : AllowFunnel {"hôte:port": true}, et le
// même sous Foreground pour `tailscale funnel` sans --bg.
func TestParseFunnelPorts(t *testing.T) {
	cases := map[string][]int{
		`{}`:   nil,
		`null`: nil,
		`{"TCP":{"443":{"HTTPS":true}},"Web":{"laptop.tail1.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}},"AllowFunnel":{"laptop.tail1.ts.net:443":true}}`: {443},
		`{"AllowFunnel":{"a.ts.net:8443":true,"a.ts.net:10000":false}}`:                                    {8443},
		`{"Foreground":{"sess-1":{"TCP":{"10000":{"HTTPS":true}},"AllowFunnel":{"a.ts.net:10000":true}}}}`: {10000},
		`{"AllowFunnel":{"a.ts.net":true,"[fd7a::1]:4443":true,"a.ts.net:0":true,"a.ts.net:99999":true}}`:  {4443},
		`{"Services":{"svc:web":{"TCP":{"80":{"HTTP":true}}}}}`:                                            nil,
	}
	for in, want := range cases {
		got, err := parseFunnelPorts([]byte(in))
		if err != nil {
			t.Fatalf("%s : %v", in, err)
		}
		if len(got) != len(want) {
			t.Errorf("%s : %v ; attendu %v", in, got, want)
		}
		for _, p := range want {
			if !got[p] {
				t.Errorf("%s : port %d manquant", in, p)
			}
		}
	}
	if _, err := parseFunnelPorts([]byte("No serve config")); err == nil {
		t.Error("texte non JSON accepté")
	}
}

// Le vrai lancement : le programme de CONNEX_TAILSCALE reçoit exactement
// « serve status --json », sans entrée ; absent, en échec ou trop lent, il ne
// donne aucun port.
func TestReadFunnelPorts(t *testing.T) {
	d := t.TempDir()
	log := filepath.Join(d, "args")
	prog := func(name, body string) string {
		p := filepath.Join(d, name)
		must(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755))
		return p
	}
	ok := prog("ts", `echo "$*" >> `+log+`
read x && echo "ENTREE:$x" >> `+log+`
echo '{"AllowFunnel":{"h.ts.net:8443":true}}'
`)
	ports := readFunnelPorts(context.Background(), ok)
	if !ports[8443] || len(ports) != 1 {
		t.Errorf("ports %v", ports)
	}
	b, _ := os.ReadFile(log)
	if strings.TrimSpace(string(b)) != "serve status --json" {
		t.Errorf("arguments ou entrée : %q", b)
	}
	if p := readFunnelPorts(context.Background(), prog("ko", "exit 1\n")); len(p) != 0 {
		t.Errorf("échec : %v", p)
	}
	if p := readFunnelPorts(context.Background(), filepath.Join(d, "absent")); len(p) != 0 {
		t.Errorf("absent : %v", p)
	}
	start := time.Now()
	if p := readFunnelPorts(context.Background(), prog("lent", "exec sleep 20\n")); len(p) != 0 {
		t.Errorf("lent : %v", p)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("tailscale lent attendu %v", el)
	}
	t.Setenv("CONNEX_TAILSCALE", ok)
	if tailscaleProgram() != ok {
		t.Error("CONNEX_TAILSCALE ignoré")
	}
}

// Le relevé complet : une écoute sur l'adresse d'une interface virtuelle est
// host ; une écoute Tailscale sur un port Funnel devient all avec funnel ;
// la boucle locale sur ce même port ne change pas. La réponse de tailscale
// est gardée funnelTTL.
func TestCollectExposure(t *testing.T) {
	uid := strconv.Itoa(os.Getuid())
	// 172.17.0.1:8080, 100.122.145.72:8443, 127.0.0.1:8443, 0.0.0.0:22
	tcp := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 010011AC:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  " + uid + "        0 7001 1\n" +
		"   1: 48917A64:20FB 00000000:0000 0A 00000000:00000000 00:00000000 00000000  " + uid + "        0 7002 1\n" +
		"   2: 0100007F:20FB 00000000:0000 0A 00000000:00000000 00:00000000 00000000  " + uid + "        0 7003 1\n" +
		"   3: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 7004 1\n"
	fp := newFakeProc(t, tcp, "")
	sys := t.TempDir()
	must(t, os.Mkdir(filepath.Join(sys, "docker0"), 0o755))
	must(t, os.Mkdir(filepath.Join(sys, "tailscale0"), 0o755))
	c := testCollector(fp.root, t.TempDir(), []string{t.TempDir()})
	c.known = true
	c.sysNet = sys
	c.ifaces = func() ([]netIface, error) {
		return []netIface{{name: "docker0", addrs: addrs("172.17.0.1")}, {name: "tailscale0", addrs: addrs("100.122.145.72")}, {name: "lo", addrs: addrs("127.0.0.1")}}, nil
	}
	calls := 0
	funnel := map[int]bool{8443: true}
	c.readFunnel = func(context.Context, string) map[int]bool { calls++; return funnel }
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	check := func(want map[string]string) {
		t.Helper()
		got := map[string]string{}
		for _, l := range c.Collect(context.Background(), true).Listeners {
			e := l.Exposure
			if l.Funnel {
				e += "+funnel"
			}
			got[l.Addr+":"+strconv.Itoa(l.Port)] = e
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s : %s ; attendu %s", k, got[k], v)
			}
		}
	}
	check(map[string]string{"172.17.0.1:8080": "host", "100.122.145.72:8443": "all+funnel", "127.0.0.1:8443": "loopback", "0.0.0.0:22": "all"})
	funnel = map[int]bool{}
	now = now.Add(5 * time.Second)
	check(map[string]string{"100.122.145.72:8443": "all+funnel"}) // gardé 10 s
	now = now.Add(6 * time.Second)
	check(map[string]string{"100.122.145.72:8443": "tailnet", "172.17.0.1:8080": "host"})
	if calls != 2 {
		t.Errorf("%d appels à tailscale, 2 attendus", calls)
	}
}

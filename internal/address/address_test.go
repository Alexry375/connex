package address

import (
	"errors"
	"strings"
	"testing"

	"github.com/Alexry375/connex/internal/model"
)

const laptopIP = "100.101.102.103"

func listener(port int, addr, exposure string, mine bool) model.Listener {
	return model.Listener{Key: "tcp:" + addr, Port: port, Addr: addr, Exposure: exposure, Mine: mine}
}

func running(name string, port int) model.App {
	return model.App{Key: name, Name: name, Port: port, State: model.AppRunning, View: model.ViewRemote,
		Health: model.AppHealth{PIDAlive: true, PortListening: true}}
}

// snap fabrique le relevé d'une machine d'adresse Tailscale ip.
func snap(ip string, apps []model.App, ls ...model.Listener) model.Snapshot {
	return model.Snapshot{Machine: model.Machine{Name: "m", TailscaleIP: ip}, Apps: apps, Listeners: ls}
}

func mustURL(t *testing.T, in Input, want string) {
	t.Helper()
	got, err := For(in)
	if err != nil || got != want {
		t.Fatalf("For : %q, %v ; attendu %q", got, err, want)
	}
}

func mustRefuse(t *testing.T, in Input, want string) {
	t.Helper()
	got, err := For(in)
	var r *Refusal
	if !errors.As(err, &r) || got != "" {
		t.Fatalf("For : %q, %v ; refus attendu", got, err)
	}
	if !strings.Contains(r.Msg, want) {
		t.Fatalf("refus « %s » sans « %s »", r.Msg, want)
	}
}

func TestForAbsentApp(t *testing.T) {
	in := Input{App: "x", Machine: "vps", Snapshot: snap(laptopIP, []model.App{running("y", 3000)})}
	mustRefuse(t, in, "aucune appli « x » sur vps")
}

// Sur le laptop : 127.0.0.1 pour la boucle locale et toutes les interfaces,
// l'adresse Tailscale du laptop pour une écoute Tailscale seule.
func TestForLocal(t *testing.T) {
	apps := []model.App{running("a", 3000)}
	for _, l := range []model.Listener{
		listener(3000, "127.0.0.1", "loopback", true),
		listener(3000, "0.0.0.0", "all", true),
		listener(3000, "::", "all", true),
	} {
		mustURL(t, Input{App: "a", Machine: "laptop", Local: true, Snapshot: snap(laptopIP, apps, l)}, "http://127.0.0.1:3000/")
	}
	in := Input{App: "a", Machine: "laptop", Local: true,
		Snapshot: snap(laptopIP, apps, listener(3000, laptopIP, "tailnet", true))}
	mustURL(t, in, "http://"+laptopIP+":3000/")
	in.Snapshot.Machine.TailscaleIP = ""
	mustRefuse(t, in, "adresse Tailscale inconnue pour laptop")
}

// Sur une autre machine : son adresse Tailscale, pour une écoute tailnet ou all.
func TestForRemote(t *testing.T) {
	apps := []model.App{running("a", 8080)}
	for _, l := range []model.Listener{
		listener(8080, "100.71.85.119", "tailnet", true),
		listener(8080, "0.0.0.0", "all", true),
	} {
		in := Input{App: "a", Machine: "vps", Snapshot: snap("100.71.85.119", apps, l, listener(8080, "127.0.0.1", "loopback", true))}
		mustURL(t, in, "http://100.71.85.119:8080/")
	}
}

func TestForRemoteLoopbackRefused(t *testing.T) {
	in := Input{App: "a", Machine: "vps", Snapshot: snap("100.71.85.119", []model.App{running("a", 8080)},
		listener(8080, "127.0.0.1", "loopback", true), listener(8080, "::1", "loopback", true))}
	mustRefuse(t, in, "« a » n'écoute que sur la boucle locale de vps : injoignable depuis ce laptop. "+
		"Fais-la écouter sur son adresse Tailscale, ou passe-la en mode train.")
}

// Jamais d'adresse hors de 100.64.0.0/10, jamais un nom d'hôte.
func TestForRemoteTailscaleIPChecked(t *testing.T) {
	for _, ip := range []string{"", "vps.example.com", "10.0.0.1", "100.63.255.255", "100.128.0.1", "fd7a:115c:a1e0::1", "100.71.85.119/32"} {
		in := Input{App: "a", Machine: "vps", Snapshot: snap(ip, []model.App{running("a", 8080)}, listener(8080, "0.0.0.0", "all", true))}
		mustRefuse(t, in, "adresse Tailscale inconnue pour vps")
	}
	in := Input{App: "a", Machine: "vps", Snapshot: snap("100.127.255.254", []model.App{running("a", 8080)}, listener(8080, "0.0.0.0", "all", true))}
	mustURL(t, in, "http://100.127.255.254:8080/")
}

func TestForNotRunning(t *testing.T) {
	cases := []struct {
		name string
		edit func(*model.App)
		want string
	}{
		{"arrêtée", func(a *model.App) { a.State = model.AppStopped; a.Health = model.AppHealth{} }, "(état : stopped)"},
		{"en échec", func(a *model.App) { a.State = model.AppFailed }, "(état : failed)"},
		{"démarre", func(a *model.App) { a.State = model.AppStarting }, "(état : starting)"},
		{"port muet", func(a *model.App) { a.Health.PortListening = false }, "(état : running)"},
	}
	for _, c := range cases {
		a := running("a", 3000)
		c.edit(&a)
		in := Input{App: "a", Machine: "pi", Snapshot: snap(laptopIP, []model.App{a}, listener(3000, "0.0.0.0", "all", true))}
		mustRefuse(t, in, "« a » ne tourne pas sur pi "+c.want)
	}
}

// Une écoute d'un autre compte sur le même port n'est pas l'appli.
func TestForIgnoresOtherAccounts(t *testing.T) {
	in := Input{App: "a", Machine: "vps", Snapshot: snap("100.71.85.119", []model.App{running("a", 8080)},
		listener(8080, "127.0.0.1", "loopback", true), listener(8080, "0.0.0.0", "all", false))}
	mustRefuse(t, in, "n'écoute que sur la boucle locale de vps")
}

func TestMirrorPort(t *testing.T) {
	cases := map[string]int{
		"http://127.0.0.1:8801/\n":          8801,
		"  http://127.0.0.1:3100 \n":        3100,
		"http://127.0.0.1:65535/":           65535,
		"":                                  0,
		"http://127.0.0.1:65536/":           0,
		"http://127.0.0.1:0/":               0,
		"http://127.0.0.1:08801/":           0,
		"http://127.0.0.1:8801//":           0,
		"http://127.0.0.1:8801/x":           0,
		"http://127.0.0.1:8801/../x":        0,
		"https://127.0.0.1:8801/":           0,
		"http://localhost:8801/":            0,
		"http://127.0.0.2:8801/":            0,
		"http://example.com:8801/":          0,
		"file:///etc/passwd":                0,
		"http://127.0.0.1:8801/ http://x/":  0,
		"http://127.0.0.1:8801/\nhttp://x/": 0,
		"http://user@127.0.0.1:8801/":       0,
	}
	for in, want := range cases {
		if got := MirrorPort(in); got != want {
			t.Errorf("MirrorPort(%q) = %d, attendu %d", in, got, want)
		}
	}
}

// Mode train : l'adresse du miroir, si son port répond sur 127.0.0.1 du laptop ;
// l'état de l'appli sur sa machine n'entre pas en compte.
func TestForTrain(t *testing.T) {
	a := running("site", 3000)
	a.View, a.State, a.Health = model.ViewTrain, model.AppStopped, model.AppHealth{}
	base := Input{App: "site", Machine: "vps", Snapshot: snap("100.71.85.119", []model.App{a}), Mirror: "http://127.0.0.1:8801/\n"}
	ok := [][]model.Listener{
		{listener(8801, "127.0.0.1", "loopback", true)},
		{listener(8801, "0.0.0.0", "all", true)},
		{listener(8801, "::", "all", true)},
		{listener(8801, "::ffff:127.0.0.1", "loopback", true)},
	}
	for _, ls := range ok {
		in := base
		in.LaptopListeners = ls
		mustURL(t, in, "http://127.0.0.1:8801/")
	}
	stopped := "mode train arrêté pour « site » : lance connex train site"
	bad := [][]model.Listener{
		nil,
		{listener(8802, "127.0.0.1", "loopback", true)},
		{listener(8801, "::1", "loopback", true)},
		{listener(8801, "127.0.0.2", "loopback", true)},
		{listener(8801, laptopIP, "tailnet", true)},
	}
	for _, ls := range bad {
		in := base
		in.LaptopListeners = ls
		mustRefuse(t, in, stopped)
	}
	in := base
	in.LaptopListeners = ok[0]
	for _, m := range []string{"", "http://example.com:8801/", "https://127.0.0.1:8801/"} {
		in.Mirror = m
		mustRefuse(t, in, stopped)
	}
}

// Lot L7 : une appli qui n'écoute qu'en host (pont Docker) est refusée, sur le
// laptop comme ailleurs ; avec aussi la boucle locale, le laptop l'ouvre.
func TestForHostOnlyRefused(t *testing.T) {
	apps := []model.App{running("a", 3000)}
	docker := listener(3000, "172.17.0.1", "host", true)
	for _, local := range []bool{true, false} {
		in := Input{App: "a", Machine: "m", Local: local, Snapshot: snap(laptopIP, apps, docker)}
		mustRefuse(t, in, "interface virtuelle")
	}
	in := Input{App: "a", Machine: "m", Local: true, Snapshot: snap(laptopIP, apps, docker, listener(3000, "127.0.0.1", "loopback", true))}
	mustURL(t, in, "http://127.0.0.1:3000/")
	in.Local = false
	mustRefuse(t, in, "boucle locale")
}

// Une écoute Funnel est sur l'adresse Tailscale (exposition all, funnel) : on
// l'ouvre par cette adresse, même depuis le laptop, pas par 127.0.0.1.
func TestForFunnelUsesTailscaleAddress(t *testing.T) {
	apps := []model.App{running("a", 8443)}
	f := listener(8443, laptopIP, "all", true)
	f.Funnel = true
	for _, local := range []bool{true, false} {
		mustURL(t, Input{App: "a", Machine: "m", Local: local, Snapshot: snap(laptopIP, apps, f)}, "http://"+laptopIP+":8443/")
	}
}

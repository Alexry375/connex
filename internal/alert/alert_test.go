package alert

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Alexry375/connex/internal/model"
)

func l(port int, addr, exposure string, mine bool, process string) model.Listener {
	return model.Listener{Key: addr, Port: port, Addr: addr, Exposure: exposure, Mine: mine, Process: process}
}

func ports(as []Alert) []int {
	var out []int
	for _, a := range as {
		out = append(out, a.Port)
	}
	return out
}

// Seuls les ports de l'utilisateur en exposition all ; v4 et v6 d'un même port
// font une seule alerte.
func TestFind(t *testing.T) {
	s := model.Snapshot{Listeners: []model.Listener{
		l(8080, "0.0.0.0", "all", true, "node"),
		l(8080, "::", "all", true, "node"),
		l(22, "0.0.0.0", "all", false, ""),
		l(5432, "127.0.0.1", "loopback", true, "postgres"),
		l(3000, "100.71.85.119", "tailnet", true, "next-server"),
		l(443, "::", "all", true, "caddy"),
	}}
	got := Find("vps", s, true)
	want := []Alert{
		{Machine: "vps", Port: 443, Processes: []string{"caddy"}, Addrs: []string{"::"}},
		{Machine: "vps", Port: 8080, Processes: []string{"node"}, Addrs: []string{"0.0.0.0", "::"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("obtenu %+v\nattendu %+v", got, want)
	}
	if got := Find("vps", model.Snapshot{}, true); len(got) != 0 {
		t.Fatalf("relevé vide : %+v", got)
	}
}

// La ligne commence par ALERTE et porte, en mots entiers, Internet, le port, le
// processus et la machine.
func TestLine(t *testing.T) {
	line := Alert{Machine: "vps", Port: 8080, Processes: []string{"node"}, Addrs: []string{"0.0.0.0"}}.Line()
	for _, w := range []string{`^ALERTE `, `\bInternet\b`, `\b8080\b`, `\bnode\b`, `\bvps\b`} {
		if !regexp.MustCompile(w).MatchString(line) {
			t.Errorf("« %s » ne contient pas %s", line, w)
		}
	}
	if line := (Alert{Machine: "vps", Port: 1}).Line(); !regexp.MustCompile(`processus inconnu`).MatchString(line) {
		t.Errorf("processus inconnu non dit : %s", line)
	}
}

// watch : tout au premier relevé, puis seulement ce qui apparaît ; machines
// indépendantes.
func TestTrackerNew(t *testing.T) {
	tr := NewTracker()
	s1 := model.Snapshot{Listeners: []model.Listener{l(80, "0.0.0.0", "all", true, "a")}}
	s2 := model.Snapshot{Listeners: []model.Listener{l(80, "0.0.0.0", "all", true, "a"), l(81, "::", "all", true, "b")}}
	steps := []struct {
		machine string
		s       model.Snapshot
		want    []int
	}{
		{"vps", s1, []int{80}},
		{"vps", s1, nil},               // même état (reconnexion) : rien
		{"vps", s2, []int{81}},         // nouveau port
		{"pi", s2, []int{80, 81}},      // autre machine : premier relevé
		{"vps", model.Snapshot{}, nil}, // les ports disparaissent
		{"vps", s1, []int{80}},         // et reviennent : signalés de nouveau
	}
	for i, st := range steps {
		if got := ports(tr.New(st.machine, st.s, true)); !reflect.DeepEqual(got, st.want) {
			t.Errorf("étape %d (%s) : %v, attendu %v", i, st.machine, got, st.want)
		}
	}
}

// Funnel : signalé sur toute machine, publique ou non, quel que soit le compte
// qui tient le port ; la ligne dit Internet, Funnel, le port et la machine. Une
// machine non publique ne signale rien d'autre.
func TestFindFunnel(t *testing.T) {
	funnel := l(8443, "100.122.145.72", "all", true, "MainThread")
	funnel.Funnel, funnel.Exe = true, "node"
	other := model.Listener{Key: "o", Port: 9443, Addr: "100.122.145.72", Exposure: "all", Funnel: true, UID: 1003}
	s := model.Snapshot{Listeners: []model.Listener{
		funnel, other,
		l(8080, "0.0.0.0", "all", true, "node"),
		l(8081, "172.17.0.1", "host", true, "python3"),
		l(3000, "100.122.145.72", "tailnet", true, "next-server"),
	}}
	got := Find("laptop", s, false)
	if !reflect.DeepEqual(ports(got), []int{8443, 9443}) {
		t.Fatalf("machine non publique : %+v", got)
	}
	for _, a := range got {
		line := a.Line()
		for _, w := range []string{`^ALERTE `, `\bInternet\b`, `\bFunnel\b`, `\b` + strconv.Itoa(a.Port) + `\b`, `\blaptop\b`} {
			if !regexp.MustCompile(w).MatchString(line) {
				t.Errorf("« %s » ne contient pas %s", line, w)
			}
		}
	}
	if !strings.Contains(got[0].Line(), "node") || !strings.Contains(got[1].Line(), "uid 1003") {
		t.Errorf("programmes : %q / %q", got[0].Line(), got[1].Line())
	}
	if got := Find("vps", s, true); !reflect.DeepEqual(ports(got), []int{8080, 8443, 9443}) {
		t.Errorf("machine publique : %v", ports(got))
	}
	if got := NewTracker().New("pi", s, false); !reflect.DeepEqual(ports(got), []int{8443, 9443}) {
		t.Errorf("watch : %v", ports(got))
	}
}

// Un programme nommé par son unité garde, dans l'alerte, ce qui tourne
// vraiment : l'exécutable pour un nom de fil, sinon le nom du processus.
func TestProgramOf(t *testing.T) {
	for _, c := range []struct {
		l    model.Listener
		want string
	}{
		{model.Listener{Process: "python3"}, "python3"},
		{model.Listener{Process: "MainThread", Exe: "node"}, "node"},
		{model.Listener{Process: "MainThread", Exe: "node", Unit: "notifbot.service"}, "notifbot (node)"},
		{model.Listener{Process: "python3", Exe: "python3.12", Unit: "aw-l7.service"}, "aw-l7 (python3)"},
		{model.Listener{Process: "MainThread", Unit: "bot.service"}, "bot"},
		{model.Listener{Process: "site", Unit: "site.service"}, "site"},
	} {
		if got := programOf(c.l); got != c.want {
			t.Errorf("programOf(%+v) = %q, attendu %q", c.l, got, c.want)
		}
	}
}

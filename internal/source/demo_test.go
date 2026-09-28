package source_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
)

// harness fait tourner un simulateur figé et garde l'état reconstruit.
type harness struct {
	t   *testing.T
	src *source.Demo
	f   *fleet.Fleet
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := &harness{t: t, src: source.NewDemo(ctx, source.DemoOptions{Seed: 42, Speed: 0}), f: fleet.New()}
	for len(h.f.Machines()) < 3 {
		h.next()
	}
	return h
}

func (h *harness) next() source.Event {
	h.t.Helper()
	select {
	case ev, ok := <-h.src.Events():
		if !ok {
			h.t.Fatal("source fermée")
		}
		h.f.Apply(ev, time.Now())
		return ev
	case <-time.After(5 * time.Second):
		h.t.Fatal("aucun événement en 5 s")
	}
	return source.Event{}
}

// ask envoie une requête et attend sa réponse, en appliquant les patchs reçus.
func (h *harness) ask(machine, id, op, target string) model.Reply {
	h.t.Helper()
	if err := h.src.Send(machine, source.NewRequest(id, op, target)); err != nil {
		h.t.Fatal(err)
	}
	for {
		if ev := h.next(); ev.Reply != nil && ev.Reply.ID == id {
			if ev.Machine != machine {
				h.t.Fatalf("réponse attribuée à %q, attendu %q", ev.Machine, machine)
			}
			return *ev.Reply
		}
	}
}

func (h *harness) app(machine, name string) model.App {
	h.t.Helper()
	for _, a := range h.f.Machine(machine).Snapshot.Apps {
		if a.Name == name {
			return a
		}
	}
	h.t.Fatalf("appli %s absente de %s", name, machine)
	return model.App{}
}

func (h *harness) listening(machine string, port int) bool {
	for _, l := range h.f.Machine(machine).Snapshot.Listeners {
		if l.Port == port {
			return true
		}
	}
	return false
}

func TestDemoHasAppsInEveryState(t *testing.T) {
	h := newHarness(t)
	seen := map[string]bool{}
	views := map[string]bool{}
	for _, m := range h.f.Machines() {
		for _, a := range m.Snapshot.Apps {
			seen[a.State] = true
			views[a.View] = true
			if a.Unit == "" || a.Key != a.Name {
				t.Errorf("appli mal formée : %+v", a)
			}
		}
	}
	for _, st := range []string{model.AppRunning, model.AppStopped, model.AppFailed} {
		if !seen[st] {
			t.Errorf("aucune appli dans l'état %s", st)
		}
	}
	if !views[model.ViewTrain] || !views[model.ViewRemote] {
		t.Errorf("vues : %v", views)
	}
}

func TestDemoStartRefusedThenFreePortThenStart(t *testing.T) {
	h := newHarness(t)
	const lap = "alfred-laptop"
	if a := h.app(lap, "planetaire"); a.State != model.AppStopped || a.Port != 5000 {
		t.Fatalf("départ : %+v", a)
	}
	r := h.ask(lap, "a", model.ReqStart, "planetaire")
	if r.OK || !strings.Contains(r.Error, "5000") || !strings.Contains(r.Error, "python3") {
		t.Fatalf("démarrage sur port pris : %+v", r)
	}
	if a := h.app(lap, "planetaire"); a.State != model.AppFailed {
		t.Fatalf("après refus : %+v", a)
	}
	if r := h.ask(lap, "b", model.ReqFreePort, "5000"); !r.OK {
		t.Fatalf("libérer 5000 : %+v", r)
	}
	if h.listening(lap, 5000) {
		t.Fatal("5000 toujours en écoute")
	}
	if r := h.ask(lap, "c", model.ReqStart, "planetaire"); !r.OK {
		t.Fatalf("démarrage : %+v", r)
	}
	a := h.app(lap, "planetaire")
	if a.State != model.AppRunning || !a.Health.PortListening || a.Health.HTTP != 200 || !h.listening(lap, 5000) {
		t.Fatalf("après démarrage : %+v", a)
	}
}

func TestDemoStopRestartAndRefusals(t *testing.T) {
	h := newHarness(t)
	const vps = "vps-40517391"
	if r := h.ask(vps, "1", model.ReqStop, "site"); !r.OK {
		t.Fatalf("stop : %+v", r)
	}
	if a := h.app(vps, "site"); a.State != model.AppStopped || a.Health.PIDAlive || h.listening(vps, a.Port) {
		t.Fatalf("après stop : %+v", a)
	}
	if r := h.ask(vps, "2", model.ReqStop, "site"); r.OK || r.Error == "" {
		t.Fatalf("second stop accepté : %+v", r)
	}
	if r := h.ask(vps, "3", model.ReqRestart, "site"); !r.OK {
		t.Fatalf("restart : %+v", r)
	}
	if a := h.app(vps, "site"); a.State != model.AppRunning {
		t.Fatalf("après restart : %+v", a)
	}
	for _, c := range []struct{ op, target, want string }{
		{model.ReqStart, "site", "tourne déjà"},
		{model.ReqStart, "inconnue", "aucune appli"},
		{model.ReqFreePort, "22", "autre compte"},
		{model.ReqFreePort, "9", "rien n'écoute"},
		{model.ReqFreePort, "abc", "port invalide"},
		{"exec", "rm -rf /", "opération inconnue"},
	} {
		if r := h.ask(vps, c.op+c.target, c.op, c.target); r.OK || !strings.Contains(r.Error, c.want) {
			t.Errorf("%s %s : %+v, attendu %q", c.op, c.target, r, c.want)
		}
	}
	if r := h.ask(vps, "4", model.ReqRefresh, ""); !r.OK {
		t.Fatalf("refresh : %+v", r)
	}
}

func TestDemoUnknownMachine(t *testing.T) {
	h := newHarness(t)
	if r := h.ask("nulle-part", "x", model.ReqRefresh, ""); r.OK || !strings.Contains(r.Error, "inconnue") {
		t.Fatalf("%+v", r)
	}
}

func TestDemoFlowsAtSpeed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	src := source.NewDemo(ctx, source.DemoOptions{Seed: 3, Speed: 120})
	patches := 0
	for ev := range src.Events() {
		if ev.Patch != nil {
			patches++
			for _, c := range ev.Patch.Changes {
				if c.Op != model.OpRemove && c.Data == nil {
					t.Fatalf("donnée non typée : %+v", c)
				}
			}
		}
	}
	if patches < 5 {
		t.Fatalf("%d patchs en 4 s à vitesse 120", patches)
	}
}

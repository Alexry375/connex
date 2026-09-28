package fleet

import (
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
)

func snap(name string, ports ...int) *model.Snapshot {
	s := model.NewSnapshot(model.Machine{Name: name}, time.Now())
	for _, p := range ports {
		k := "tcp4:127.0.0.1:" + string(rune('0'+p))
		s.Listeners = append(s.Listeners, model.Listener{Key: k, Port: p})
	}
	return &s
}

func kinds(cs []Change) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Op+" "+c.Kind+" "+c.Key)
	}
	return out
}

func TestFleetLifecycle(t *testing.T) {
	f := New()
	now := time.Now()
	if cs := f.Apply(source.Event{Machine: "a", Snapshot: snap("a", 1)}, now); len(cs) != 1 || cs[0].Kind != KindMachine || cs[0].Op != model.OpAdd {
		t.Fatalf("apparition : %v", kinds(cs))
	}
	p := model.NewPatch("a", now, []model.Change{{Op: model.OpAdd, Kind: model.KindListener, Key: "k2", Data: model.Listener{Key: "k2", Port: 2}}})
	if cs := f.Apply(source.Event{Machine: "a", Patch: &p}, now); len(cs) != 1 || cs[0].Key != "k2" {
		t.Fatalf("patch : %v", kinds(cs))
	}
	if n := len(f.Machine("a").Snapshot.Listeners); n != 2 {
		t.Fatalf("%d ports après patch", n)
	}
	down := model.MachineStatus{Machine: "a", Reachable: false}
	later := now.Add(time.Minute)
	if cs := f.Apply(source.Event{Machine: "a", Status: &down}, later); len(cs) != 1 || f.Machine("a").Reachable || !f.Machine("a").Since.Equal(later) {
		t.Fatalf("coupure : %v %+v", kinds(cs), f.Machine("a"))
	}
	if n := len(f.Machine("a").Snapshot.Listeners); n != 2 {
		t.Fatal("l'état d'avant la coupure doit rester")
	}
	// Retour : un relevé neuf remplace l'ancien, et la différence est signalée.
	cs := f.Apply(source.Event{Machine: "a", Snapshot: snap("a", 3)}, later)
	got := map[string]bool{}
	for _, k := range kinds(cs) {
		got[k] = true
	}
	for _, want := range []string{"add listener tcp4:127.0.0.1:3", "remove listener k2", "update machine a"} {
		if !got[want] {
			t.Errorf("retour : manque %q dans %v", want, kinds(cs))
		}
	}
	if !f.Machine("a").Reachable {
		t.Fatal("machine revenue mais injoignable")
	}
}

func TestFleetIgnoresPatchBeforeSnapshotAndKeepsOrder(t *testing.T) {
	f := New()
	p := model.NewPatch("b", time.Now(), nil)
	if cs := f.Apply(source.Event{Machine: "b", Patch: &p}, time.Now()); cs != nil || f.Machine("b") != nil {
		t.Fatal("patch sans relevé pris en compte")
	}
	for _, n := range []string{"z", "a", "m"} {
		f.Apply(source.Event{Machine: n, Snapshot: snap(n)}, time.Now())
	}
	var order []string
	for _, m := range f.Machines() {
		order = append(order, m.Name)
	}
	if len(order) != 3 || order[0] != "z" || order[1] != "a" || order[2] != "m" {
		t.Fatalf("ordre : %v", order)
	}
	if cs := f.Apply(source.Event{Machine: "z", Reply: &model.Reply{ID: "r"}}, time.Now()); cs != nil {
		t.Fatal("une réponse ne change rien")
	}
}

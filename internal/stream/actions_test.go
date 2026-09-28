package stream

import (
	"bufio"
	"context"
	"errors"
	"io"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// fakeActions note les actions, mesure leurs chevauchements, et peut en retenir
// une jusqu'à ce que le test la libère.
type fakeActions struct {
	mu      sync.Mutex
	calls   []string
	running map[string]int
	maxKey  map[string]int
	gates   map[string]chan struct{}
	delay   time.Duration
	after   func(op, target string) // effet de l'action sur la machine
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

func newFakeActions() *fakeActions {
	return &fakeActions{running: map[string]int{}, maxKey: map[string]int{}, gates: map[string]chan struct{}{}}
}

func (f *fakeActions) Validate(op, target string) string {
	if op == "free_port" {
		if !regexp.MustCompile(`^[0-9]{1,5}$`).MatchString(target) {
			return "port invalide"
		}
		return ""
	}
	if !nameRe.MatchString(target) {
		return "nom d'appli invalide"
	}
	return ""
}

func (f *fakeActions) Key(op, target string) string { return target }

func (f *fakeActions) Do(ctx context.Context, op, target string) error {
	f.mu.Lock()
	f.calls = append(f.calls, op+" "+target)
	f.running[target]++
	f.maxKey[target] = max(f.maxKey[target], f.running[target])
	gate := f.gates[target]
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	time.Sleep(f.delay)
	f.mu.Lock()
	f.running[target]--
	f.mu.Unlock()
	if f.after != nil {
		f.after(op, target)
	}
	if target == "refusee" {
		return errors.New("« refusee » tourne déjà")
	}
	return nil
}

// appsCollector rend un relevé dont les applis changent quand une action agit.
type appsCollector struct {
	mu    sync.Mutex
	state map[string]string
}

func (c *appsCollector) Collect(ctx context.Context, full bool) model.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := model.NewSnapshot(model.Machine{Name: "m"}, time.Now())
	for n, st := range c.state {
		s.Apps = append(s.Apps, model.App{Key: n, Name: n, Unit: "connex-" + n + ".service", State: st})
	}
	return s
}

func startWith(t *testing.T, c Collector, acts Actions) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{in: inW, out: bufio.NewScanner(outR), done: make(chan error, 1)}
	go func() {
		err := RunWith(context.Background(), c, inR, outW, time.Hour, acts)
		outW.Close()
		s.done <- err
	}()
	t.Cleanup(func() { inW.Close(); go io.Copy(io.Discard, outR) })
	return s
}

func (s *session) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func req(id, op, target string) string {
	return `{"schema":1,"type":"request","id":"` + id + `","op":"` + op + `","target":"` + target + `"}`
}

// TestRunWithValidatesBeforeExecuting : opération inconnue ou cible invalide sont
// refusées sans que rien ne s'exécute.
func TestRunWithValidatesBeforeExecuting(t *testing.T) {
	acts := newFakeActions()
	s := startWith(t, &appsCollector{}, acts)
	s.next(t, time.Second) // relevé
	hostile := []string{
		`{"schema":1,"type":"request","id":"h0","op":"exec","target":"touch /tmp/pwn"}`,
		req("h1", "start", "x; touch /tmp/pwn"),
		req("h2", "start", "../../x"),
		req("h3", "stop", "$(touch /tmp/pwn)"),
		req("h4", "free_port", "abc"),
		`{"schema":1,"type":"request","id":"h5","op":"start","target":"ok","command":"touch /tmp/pwn"}`,
	}
	go func() { // un tube sans tampon : on écrit pendant qu'on lit
		for _, h := range hostile {
			io.WriteString(s.in, h+"\n")
		}
	}()
	seen := map[string]bool{}
	for len(seen) < len(hostile) {
		m := s.next(t, time.Second)
		if m["type"] != "reply" {
			continue
		}
		id := m["id"].(string)
		seen[id] = true
		if id == "h5" { // champ inconnu ignoré : la requête reste un start « ok », sans commande
			if m["ok"] != true {
				t.Fatalf("h5 : %v", m)
			}
			continue
		}
		if m["ok"] != false || m["error"] == "" {
			t.Fatalf("%s accepté : %v", id, m)
		}
	}
	acts.mu.Lock()
	defer acts.mu.Unlock()
	if len(acts.calls) != 1 || acts.calls[0] != "start ok" {
		t.Fatalf("exécuté : %v", acts.calls)
	}
}

// TestRunWithReplyThenPatch : la réponse d'une action est suivie, en moins
// d'une seconde, du patch qui montre son effet (sans attendre l'intervalle).
func TestRunWithReplyThenPatch(t *testing.T) {
	col := &appsCollector{state: map[string]string{"site": model.AppStopped}}
	acts := newFakeActions()
	acts.after = func(op, target string) {
		col.mu.Lock()
		col.state[target] = model.AppRunning
		col.mu.Unlock()
	}
	s := startWith(t, col, acts)
	s.next(t, time.Second)
	s.send(t, req("r1", "start", "site"))
	rep := s.next(t, time.Second)
	if rep["type"] != "reply" || rep["id"] != "r1" || rep["ok"] != true {
		t.Fatalf("réponse : %v", rep)
	}
	t0 := time.Now()
	p := s.next(t, time.Second)
	if p["type"] != "patch" || time.Since(t0) > time.Second {
		t.Fatalf("patch : %v", p)
	}
	ch := p["changes"].([]any)[0].(map[string]any)
	if ch["kind"] != "app" || ch["key"] != "site" || ch["data"].(map[string]any)["state"] != "running" {
		t.Fatalf("changement : %v", ch)
	}
	s.send(t, req("r2", "start", "refusee"))
	rep = s.next(t, time.Second)
	if rep["id"] != "r2" || rep["ok"] != false || rep["error"] != "« refusee » tourne déjà" {
		t.Fatalf("refus : %v", rep)
	}
}

// TestRunWithLongActionDoesNotBlock : une action retenue ne bloque ni les
// autres requêtes ni la lecture.
func TestRunWithLongActionDoesNotBlock(t *testing.T) {
	acts := newFakeActions()
	gate := make(chan struct{})
	acts.gates["lente"] = gate
	s := startWith(t, &appsCollector{}, acts)
	s.next(t, time.Second)
	s.send(t, req("slow", "stop", "lente"))
	s.send(t, req("r", "refresh", ""))
	s.send(t, req("other", "stop", "autre"))
	got := map[string]bool{}
	for !got["r"] || !got["other"] {
		m := s.next(t, 2*time.Second)
		if m["type"] == "reply" {
			if m["id"] == "slow" {
				t.Fatal("l'action retenue a répondu avant d'être libérée")
			}
			got[m["id"].(string)] = true
		}
	}
	close(gate)
	for {
		m := s.next(t, 2*time.Second)
		if m["type"] == "reply" && m["id"] == "slow" {
			break
		}
	}
	s.in.Close()
	s.wait(t, 2*time.Second)
}

// TestRunWithSameKeySerialized : les actions d'une même appli s'enchaînent dans
// l'ordre, sans se chevaucher (celles d'une autre appli tournent à côté : voir
// TestRunWithLongActionDoesNotBlock).
func TestRunWithSameKeySerialized(t *testing.T) {
	acts := newFakeActions()
	acts.delay = 60 * time.Millisecond
	s := startWith(t, &appsCollector{}, acts)
	s.next(t, time.Second)
	for _, r := range []string{req("a1", "stop", "a"), req("a2", "start", "a"), req("b1", "stop", "b"), req("a3", "restart", "a")} {
		s.send(t, r)
	}
	var order []string
	for len(order) < 4 {
		m := s.next(t, 2*time.Second)
		if m["type"] == "reply" {
			order = append(order, m["id"].(string))
		}
	}
	var as []string
	for _, id := range order {
		if id[0] == 'a' {
			as = append(as, id)
		}
	}
	acts.mu.Lock()
	defer acts.mu.Unlock()
	if len(as) != 3 || as[0] != "a1" || as[1] != "a2" || as[2] != "a3" {
		t.Fatalf("ordre des réponses de a : %v", order)
	}
	if acts.maxKey["a"] != 1 {
		t.Fatalf("%d actions sur a en même temps", acts.maxKey["a"])
	}
}

// TestRunWithStopAnswersEveryAction : à la fin de l'entrée, une action qui ne
// finit pas reçoit msgInterrupted, celle qui attendait derrière elle
// msgCancelled (sans être exécutée), et l'arrêt tient dans les 2 s promises.
func TestRunWithStopAnswersEveryAction(t *testing.T) {
	acts := newFakeActions()
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	acts.gates["lente"] = gate // Do ne rend pas la main, même annulé
	s := startWith(t, &appsCollector{}, acts)
	s.next(t, time.Second)
	s.send(t, req("s1", "stop", "lente"))
	s.send(t, req("s2", "start", "lente"))
	for { // s1 a commencé : s2 attend derrière elle
		acts.mu.Lock()
		n := len(acts.calls)
		acts.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	t0 := time.Now()
	s.in.Close()
	got := map[string]any{}
	for len(got) < 2 {
		m := s.next(t, 3*time.Second)
		if m["type"] == "reply" {
			got[m["id"].(string)] = m["error"]
		}
	}
	if got["s1"] != msgInterrupted || got["s2"] != msgCancelled {
		t.Fatalf("réponses : %v", got)
	}
	s.wait(t, time.Second)
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("arrêt en %s", d)
	}
	acts.mu.Lock()
	defer acts.mu.Unlock()
	if len(acts.calls) != 1 {
		t.Fatalf("exécuté : %v", acts.calls)
	}
}

// TestRunWithActionFinishesAfterEOF : une action en cours quand l'entrée se
// ferme a le temps (drainGrace) de rendre son vrai résultat.
func TestRunWithActionFinishesAfterEOF(t *testing.T) {
	acts := newFakeActions()
	acts.delay = 200 * time.Millisecond
	s := startWith(t, &appsCollector{}, acts)
	s.next(t, time.Second)
	s.send(t, req("a1", "stop", "site"))
	s.in.Close()
	for {
		m := s.next(t, 2*time.Second)
		if m["type"] == "reply" {
			if m["id"] != "a1" || m["ok"] != true {
				t.Fatalf("réponse : %v", m)
			}
			break
		}
	}
	s.wait(t, 2*time.Second)
}

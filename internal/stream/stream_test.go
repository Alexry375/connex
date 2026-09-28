package stream

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// scripted est un Collector qui rend une suite de relevés, puis répète le dernier.
type scripted struct {
	mu    sync.Mutex
	snaps []model.Snapshot
	i     int
	fulls []bool
	block bool // bloque jusqu'à l'annulation, comme un git qui traîne
}

func (s *scripted) Collect(ctx context.Context, full bool) model.Snapshot {
	s.mu.Lock()
	s.fulls = append(s.fulls, full)
	snap := s.snaps[min(s.i, len(s.snaps)-1)]
	s.i++
	block := s.block && s.i > 1
	s.mu.Unlock()
	if block {
		<-ctx.Done()
	}
	return snap
}

func snapWith(ports ...int) model.Snapshot {
	s := model.NewSnapshot(model.Machine{Name: "m"}, time.Now())
	for _, p := range ports {
		s.Listeners = append(s.Listeners, model.Listener{Key: "tcp4:127.0.0.1:" + itoa(p), Port: p, Addr: "127.0.0.1", Exposure: "loopback", Mine: true})
	}
	return s
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// session lance Run et donne accès à ses lignes.
type session struct {
	in   *io.PipeWriter
	out  *bufio.Scanner
	done chan error
}

func start(t *testing.T, c Collector, interval time.Duration) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{in: inW, out: bufio.NewScanner(outR), done: make(chan error, 1)}
	go func() {
		err := Run(context.Background(), c, inR, outW, interval)
		outW.Close()
		s.done <- err
	}()
	t.Cleanup(func() { inW.Close(); go io.Copy(io.Discard, outR) })
	return s
}

func (s *session) next(t *testing.T, timeout time.Duration) map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 1)
	go func() {
		if !s.out.Scan() {
			ch <- nil
			return
		}
		var m map[string]any
		if err := json.Unmarshal(s.out.Bytes(), &m); err != nil {
			t.Errorf("ligne non JSON : %s", s.out.Bytes())
		}
		ch <- m
	}()
	select {
	case m := <-ch:
		return m
	case <-time.After(timeout):
		t.Fatalf("aucune ligne en %s", timeout)
		return nil
	}
}

func TestRunSnapshotThenPatchesOnlyOnChange(t *testing.T) {
	c := &scripted{snaps: []model.Snapshot{snapWith(22), snapWith(22), snapWith(22, 8000), snapWith(22, 8000), snapWith(8000)}}
	s := start(t, c, 10*time.Millisecond)

	first := s.next(t, time.Second)
	if first["type"] != "snapshot" || first["schema"] != float64(1) {
		t.Fatalf("première ligne : %v", first)
	}
	add := s.next(t, time.Second)
	ch := add["changes"].([]any)[0].(map[string]any)
	if add["type"] != "patch" || add["machine"] != "m" || add["at"] == "" || ch["op"] != "add" || ch["key"] != "tcp4:127.0.0.1:8000" {
		t.Fatalf("ajout : %v", add)
	}
	rm := s.next(t, time.Second)
	ch = rm["changes"].([]any)[0].(map[string]any)
	if ch["op"] != "remove" || ch["key"] != "tcp4:127.0.0.1:22" || len(rm["changes"].([]any)) != 1 {
		t.Fatalf("retrait : %v", rm)
	}
	// Plus rien ne change : plus rien ne sort.
	got := make(chan bool, 1)
	go func() { got <- s.out.Scan() }()
	select {
	case <-got:
		t.Fatalf("ligne inattendue sans changement : %s", s.out.Bytes())
	case <-time.After(150 * time.Millisecond):
	}
	s.in.Close()
	s.wait(t, 2*time.Second)
}

// wait attend la fin de Run, au plus timeout.
func (s *session) wait(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(timeout):
		t.Fatalf("Run ne s'arrête pas en %s après la fermeture de l'entrée", timeout)
	}
}

func TestRunStopsWhenInputCloses(t *testing.T) {
	// Le collecteur bloque (git lent) : la fermeture de l'entrée doit l'interrompre.
	c := &scripted{snaps: []model.Snapshot{snapWith(22)}, block: true}
	s := start(t, c, 5*time.Millisecond)
	s.next(t, time.Second)
	time.Sleep(30 * time.Millisecond) // un relevé est en cours, bloqué
	t0 := time.Now()
	s.in.Close()
	s.wait(t, 2*time.Second)
	if d := time.Since(t0); d > 500*time.Millisecond {
		t.Errorf("arrêt en %s", d)
	}
}

func TestRunRequests(t *testing.T) {
	c := &scripted{snaps: []model.Snapshot{snapWith(22), snapWith(22, 9000)}}
	s := start(t, c, time.Hour) // pas de relecture périodique : seul refresh relit
	s.next(t, time.Second)

	io.WriteString(s.in, `{"schema":1,"type":"request","id":"a","op":"start","target":"rm -rf /"}`+"\n")
	r := s.next(t, time.Second)
	if r["type"] != "reply" || r["id"] != "a" || r["ok"] != false || !strings.Contains(r["error"].(string), "L3") {
		t.Errorf("start : %v", r)
	}
	io.WriteString(s.in, "\n  \n{pas du json}\n")
	if r := s.next(t, time.Second); r["ok"] != false || r["error"] == "" {
		t.Errorf("JSON illisible : %v", r)
	}
	io.WriteString(s.in, `{"schema":1,"type":"request","id":"b","op":"exec","target":"ls"}`+"\n")
	if r := s.next(t, time.Second); r["id"] != "b" || r["ok"] != false {
		t.Errorf("opération inconnue : %v", r)
	}
	io.WriteString(s.in, `{"schema":1,"type":"request","id":"c","op":"refresh"}`+"\n")
	// refresh relit tout : le patch du changement passe avant la réponse.
	p := s.next(t, time.Second)
	if p["type"] != "patch" {
		t.Fatalf("patch attendu avant la réponse : %v", p)
	}
	ok := s.next(t, time.Second)
	if ok["type"] != "reply" || ok["id"] != "c" || ok["ok"] != true {
		t.Errorf("refresh : %v", ok)
	}
	if _, has := ok["error"]; has {
		t.Errorf("une réponse ok ne porte pas d'erreur : %v", ok)
	}
	c.mu.Lock()
	fulls := append([]bool{}, c.fulls...)
	c.mu.Unlock()
	if len(fulls) != 2 || !fulls[0] || !fulls[1] {
		t.Errorf("relevés complets attendus au départ et au refresh : %v", fulls)
	}
}

func TestParseRequest(t *testing.T) {
	cases := []struct {
		line   string
		ok     bool
		errHas string
	}{
		{`{"schema":1,"type":"request","id":"x","op":"refresh"}`, true, ""},
		{`{"schema":1,"type":"request","id":"x","op":"stop","target":"site"}`, false, "L3"},
		{`{"schema":1,"type":"request","id":"x","op":"free_port","target":"3000"}`, false, "L3"},
		{`{"schema":1,"type":"request","id":"x","op":"sh","target":"curl x | sh"}`, false, "inconnue"},
		{`{"schema":2,"type":"request","id":"x","op":"refresh"}`, false, "schema"},
		{`{"schema":1,"type":"reply","id":"x","op":"refresh"}`, false, "request"},
		{`[1,2]`, false, "illisible"},
	}
	for _, c := range cases {
		_, refusal := parseRequest([]byte(c.line), nil)
		if (refusal == "") != c.ok || !strings.Contains(refusal, c.errHas) {
			t.Errorf("%s : refus %q", c.line, refusal)
		}
	}
}

func TestReadLineBoundsLength(t *testing.T) {
	long := strings.Repeat("x", 300)
	br := bufio.NewReaderSize(strings.NewReader("court\n"+long+"\nsuite\nfin"), 16)
	var got []string
	for {
		line, err := readLine(br, 100)
		if err == errTooLong {
			got = append(got, "<trop long>")
			continue
		}
		if len(line) > 0 {
			got = append(got, string(line))
		}
		if err != nil {
			break
		}
	}
	if strings.Join(got, "|") != "court|<trop long>|suite|fin" {
		t.Errorf("lignes : %v", got)
	}
}

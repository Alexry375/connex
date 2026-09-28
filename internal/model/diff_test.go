package model

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func boolp(b bool) *bool { return &b }

func TestDiff(t *testing.T) {
	at := time.Date(2026, 9, 27, 21, 40, 0, 0, time.UTC)
	prev := NewSnapshot(Machine{Name: "m"}, at)
	prev.Listeners = []Listener{
		{Key: "tcp4:127.0.0.1:3000", Port: 3000, Addr: "127.0.0.1", Mine: true, PID: 10},
		{Key: "tcp4:0.0.0.0:22", Port: 22, Addr: "0.0.0.0", UID: 0},
	}
	prev.Repos = []Repo{{Key: "/r", Path: "/r", Branch: "main", Dirty: boolp(false), Worktrees: []Worktree{}}}
	prev.Agents = []Agent{{Key: "claude:5", Tool: "claude", PID: 5, State: StateWorking}}

	if got := Diff(prev, prev); len(got) != 0 {
		t.Fatalf("deux relevés égaux : %+v", got)
	}
	// Seule la date change : aucun changement.
	same := prev
	same.TakenAt = Timestamp(at.Add(time.Minute))
	if got := Diff(prev, same); len(got) != 0 {
		t.Fatalf("seule la date diffère : %+v", got)
	}

	next := NewSnapshot(Machine{Name: "m"}, at.Add(2*time.Second))
	next.Listeners = []Listener{
		{Key: "tcp4:127.0.0.1:3000", Port: 3000, Addr: "127.0.0.1", Mine: true, PID: 11}, // redémarré
		{Key: "tcp6:[::1]:5173", Port: 5173, Addr: "::1", Mine: true, PID: 12},           // nouveau
	}
	next.Repos = []Repo{{Key: "/r", Path: "/r", Branch: "main", Dirty: boolp(true), Worktrees: []Worktree{}}}
	next.Agents = []Agent{{Key: "claude:5", Tool: "claude", PID: 5, State: StateWorking}}

	got := Diff(prev, next)
	type k struct{ op, kind, key string }
	var ks []k
	for _, c := range got {
		ks = append(ks, k{c.Op, c.Kind, c.Key})
		if c.Op == OpRemove && c.Data != nil {
			t.Errorf("un retrait ne porte pas de données : %+v", c)
		}
	}
	want := []k{
		{OpUpdate, KindListener, "tcp4:127.0.0.1:3000"},
		{OpAdd, KindListener, "tcp6:[::1]:5173"},
		{OpRemove, KindListener, "tcp4:0.0.0.0:22"},
		{OpUpdate, KindRepo, "/r"},
	}
	if !reflect.DeepEqual(ks, want) {
		t.Errorf("changements : %+v ; attendu %+v", ks, want)
	}

	// Rejouer les changements sur prev redonne next, sans toucher à prev.
	before, _ := json.Marshal(prev)
	replayed := Apply(prev, got)
	replayed.TakenAt = next.TakenAt
	if a, b := mustJSON(t, replayed), mustJSON(t, next); a != b {
		t.Errorf("rejeu :\n%s\nattendu :\n%s", a, b)
	}
	if after, _ := json.Marshal(prev); string(after) != string(before) {
		t.Error("Apply a modifié son entrée")
	}
}

func TestSnapshotJSONShape(t *testing.T) {
	s := NewSnapshot(Machine{Name: "m", OS: "linux", Arch: "arm64", User: "abc"}, time.Date(2026, 9, 27, 21, 40, 0, 0, time.UTC))
	s.Listeners = append(s.Listeners, Listener{Key: "tcp4:127.0.0.1:5432", Port: 5432, Addr: "127.0.0.1", Exposure: "loopback", UID: 0})
	s.Repos = append(s.Repos, Repo{Key: "/r", Path: "/r", Worktrees: []Worktree{}})
	var m map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, s)), &m); err != nil {
		t.Fatal(err)
	}
	if m["schema"] != float64(1) || m["type"] != "snapshot" || m["taken_at"] != "2026-09-27T21:40:00Z" {
		t.Errorf("en-tête : %v", m)
	}
	for _, f := range []string{"listeners", "repos", "agents", "apps", "errors"} {
		if _, ok := m[f].([]any); !ok {
			t.Errorf("%s doit être une liste, même vide : %v", f, m[f])
		}
	}
	l := m["listeners"].([]any)[0].(map[string]any)
	if l["uid"] != float64(0) || l["mine"] != false {
		t.Errorf("un port de root garde uid 0 : %v", l)
	}
	for _, f := range []string{"pid", "process", "cwd", "project_root"} {
		if _, ok := l[f]; ok {
			t.Errorf("port d'un autre compte : %s ne doit pas sortir", f)
		}
	}
	r := m["repos"].([]any)[0].(map[string]any)
	if v, ok := r["dirty"]; !ok || v != nil {
		t.Errorf("dirty inconnu doit sortir null : %v", r)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

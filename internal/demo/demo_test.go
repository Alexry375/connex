package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

func newTestSim(seed uint64) *sim {
	return newSim(rand.New(rand.NewPCG(seed, 0x636f6e6e6578)), time.Date(2026, 9, 27, 21, 0, 0, 0, time.UTC))
}

func TestDemoSnapshotsMeetMinimums(t *testing.T) {
	for seed := uint64(1); seed <= 30; seed++ {
		snaps := newTestSim(seed).snapshots()
		if len(snaps) != 3 {
			t.Fatalf("graine %d : %d machines", seed, len(snaps))
		}
		var L []model.Listener
		var R []model.Repo
		var A []model.Agent
		archs := map[string]bool{}
		for _, s := range snaps {
			archs[s.Machine.Arch] = true
			L, R, A = append(L, s.Listeners...), append(R, s.Repos...), append(A, s.Agents...)
			// Deux sockets de même clé sur une machine : impossible en vrai.
			keys := map[string]bool{}
			for _, l := range s.Listeners {
				if keys[l.Key] {
					t.Errorf("graine %d, %s : clé %s en double", seed, s.Machine.Name, l.Key)
				}
				keys[l.Key] = true
			}
		}
		procs, states, samePort := map[string]bool{}, map[string]bool{}, false
		allMineVPS, foreign, worktrees, clean, dirty, long := false, false, false, false, false, false
		for _, l := range L {
			if l.Mine {
				procs[l.Process] = true
			} else {
				foreign = true
				if l.PID != 0 || l.Process != "" || l.CWD != "" {
					t.Errorf("port d'un autre compte trop bavard : %+v", l)
				}
			}
		}
		for _, s := range snaps {
			ports := map[int]int{}
			for _, l := range s.Listeners {
				if l.Mine {
					ports[l.Port]++
					if ports[l.Port] == 2 && s.Machine.Name == "alfred-laptop" {
						samePort = true
					}
				}
				if s.Machine.Name == "vps-40517391" && l.Mine && l.Exposure == "all" {
					allMineVPS = true
				}
			}
		}
		for _, r := range R {
			worktrees = worktrees || len(r.Worktrees) > 0
			clean = clean || !*r.Dirty
			dirty = dirty || *r.Dirty
			long = long || len(r.Path) >= 60
		}
		for _, a := range A {
			states[a.State] = true
		}
		switch {
		case !archs["amd64"] || !archs["arm64"]:
			t.Errorf("graine %d : architectures %v", seed, archs)
		case len(L) < 30:
			t.Errorf("graine %d : %d ports", seed, len(L))
		case len(procs) < 8:
			t.Errorf("graine %d : %d noms de processus", seed, len(procs))
		case !allMineVPS || !foreign || !samePort:
			t.Errorf("graine %d : all sur le VPS %v, autre compte %v, même port %v", seed, allMineVPS, foreign, samePort)
		case !worktrees || !clean || !dirty || !long:
			t.Errorf("graine %d : worktrees %v, propres %v, modifiés %v, chemin long %v", seed, worktrees, clean, dirty, long)
		case len(states) != 4:
			t.Errorf("graine %d : états %v", seed, states)
		}
	}
}

// replay fait tourner le simulateur pendant d sec simulées et vérifie, patch par
// patch, que le flux est cohérent avec le relevé qu'on reconstruit à partir de lui.
func replay(t *testing.T, s *sim, d float64) (lines []any) {
	t.Helper()
	view := map[string]model.Snapshot{}
	down := map[string]bool{}
	for _, snap := range s.snapshots() {
		view[snap.Machine.Name] = snap
	}
	for s.peek() <= d {
		for _, line := range s.step() {
			lines = append(lines, line)
			switch v := line.(type) {
			case model.Patch:
				if down[v.Machine] {
					t.Fatalf("patch pour %s injoignable", v.Machine)
				}
				cur := view[v.Machine]
				for _, c := range v.Changes {
					exists := hasKey(cur, c.Kind, c.Key)
					if (c.Op == model.OpAdd) == exists {
						t.Fatalf("%s %s %s : existe déjà=%v", c.Op, c.Kind, c.Key, exists)
					}
				}
				view[v.Machine] = model.Apply(cur, v.Changes)
			case model.MachineStatus:
				down[v.Machine] = !v.Reachable
			case model.Snapshot:
				if down[v.Machine.Name] {
					t.Fatalf("relevé de %s alors qu'elle est injoignable", v.Machine.Name)
				}
				view[v.Machine.Name] = v
			}
		}
	}
	for _, m := range s.machines {
		if !m.reachable {
			continue
		}
		got, want := view[m.info.Name], cloneSnap(m.snap)
		got.TakenAt, want.TakenAt = "", ""
		sortSnap(&got)
		if a, b := js(got), js(want); a != b {
			t.Fatalf("%s : l'état rejoué diffère de l'état simulé\n%s\n%s", m.info.Name, a, b)
		}
	}
	return lines
}

func hasKey(s model.Snapshot, kind, key string) bool {
	switch kind {
	case model.KindListener:
		for _, l := range s.Listeners {
			if l.Key == key {
				return true
			}
		}
	case model.KindRepo:
		for _, r := range s.Repos {
			if r.Key == key {
				return true
			}
		}
	case model.KindAgent:
		for _, a := range s.Agents {
			if a.Key == key {
				return true
			}
		}
	case model.KindApp:
		for _, a := range s.Apps {
			if a.Key == key {
				return true
			}
		}
	}
	return false
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestDemoFlowIsConsistent(t *testing.T) {
	for seed := uint64(1); seed <= 10; seed++ {
		lines := replay(t, newTestSim(seed), 4*3600)
		down, up := 0, 0
		for _, l := range lines {
			if ms, ok := l.(model.MachineStatus); ok {
				if ms.Reachable {
					up++
				} else {
					down++
				}
			}
		}
		if down < 2 || up < 2 {
			t.Errorf("graine %d : %d coupures, %d retours en 4 h", seed, down, up)
		}
	}
}

// Sur 6 minutes simulées (6 s à --speed 60), il se passe assez de choses, dont
// une coupure ; et sur 5 minutes, aucune suite de changements ne se répète.
func TestDemoFirstMinutes(t *testing.T) {
	for seed := uint64(1); seed <= 50; seed++ {
		lines := replay(t, newTestSim(seed), 360)
		patches, down := 0, false
		distinct := map[string]bool{}
		var sigs []string
		for _, l := range lines {
			switch v := l.(type) {
			case model.Patch:
				patches++
				for _, c := range v.Changes {
					sig := fmt.Sprintf("%s %s %s %s", v.Machine, c.Kind, c.Key, c.Op)
					distinct[sig] = true
					if v.At < "2026-09-27T21:05:00Z" {
						sigs = append(sigs, sig+" "+js(c.Data))
					}
				}
			case model.MachineStatus:
				down = down || !v.Reachable
			}
		}
		if patches < 10 || len(distinct) < 10 || !down {
			t.Errorf("graine %d : %d patchs, %d changements distincts, coupure %v", seed, patches, len(distinct), down)
		}
		seen := map[string]bool{}
		for i := 0; i+3 <= len(sigs); i++ {
			w := strings.Join(sigs[i:i+3], "\n")
			if seen[w] {
				t.Errorf("graine %d : suite répétée en 5 min :\n%s", seed, w)
			}
			seen[w] = true
		}
	}
}

func TestDemoSeedsDiffer(t *testing.T) {
	a, b := replay(t, newTestSim(1), 600), replay(t, newTestSim(2), 600)
	if js(a) == js(b) {
		t.Error("deux graines donnent la même histoire")
	}
	if js(replay(t, newTestSim(3), 600)) != js(replay(t, newTestSim(3), 600)) {
		t.Error("une même graine doit redonner la même histoire")
	}
}

func TestDemoRunRealTime(t *testing.T) {
	var buf bytes.Buffer
	t0 := time.Now()
	err := Run(context.Background(), Options{Out: &buf, JSON: true, Duration: 400 * time.Millisecond, Speed: 900, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d < 400*time.Millisecond || d > 2*time.Second {
		t.Errorf("durée %s pour 400ms", d)
	}
	var types []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("ligne non JSON : %s", line)
		}
		if m["schema"] != float64(1) {
			t.Errorf("schema : %s", line)
		}
		types = append(types, m["type"].(string))
	}
	if len(types) < 13 || types[0] != "snapshot" || types[1] != "snapshot" || types[2] != "snapshot" {
		t.Errorf("début du flux : %v", types)
	}

	buf.Reset()
	if err := Run(context.Background(), Options{Out: &buf, Speed: 1, Seed: 7}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"alfred-laptop", "vps-40517391", "abc-desktop", "PORTS", "DÉPÔTS", "AGENTS", "all ⚠"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("sortie texte sans %q", want)
		}
	}
}

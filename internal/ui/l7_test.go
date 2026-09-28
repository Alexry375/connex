package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// l7App ajoute au petit monde des tests ce que L7 fait voir : un dépôt à
// pousser et en retard, une écoute du pont Docker (host), une écoute publiée
// par Tailscale Funnel, un programme nommé par son unité.
func l7App(t *testing.T, w, h int) *App {
	t.Helper()
	a, _ := newTestApp(t, w, h)
	two, three := 2, 3
	repo := model.Repo{Key: "/home/alfred/p/site", Path: "/home/alfred/p/site", Branch: "main", Worktrees: []model.Worktree{},
		Upstream: "origin/main", Ahead: &two, Behind: &three}
	host := model.Listener{Key: "tcp4:172.17.0.1:4000", Port: 4000, Addr: "172.17.0.1", Exposure: model.ExposureHost,
		Mine: true, UID: 1000, PID: 401, Process: "litellm", Exe: "litellm", CWD: "/home/alfred"}
	funnel := model.Listener{Key: "tcp4:100.64.0.1:443", Port: 443, Addr: "100.64.0.1", Exposure: model.ExposureAll, Funnel: true, UID: 0}
	bot := model.Listener{Key: "tcp4:127.0.0.1:7070", Port: 7070, Addr: "127.0.0.1", Exposure: model.ExposureLoopback,
		Mine: true, UID: 1000, PID: 402, Process: "MainThread", Exe: "python3.12", Unit: "notifbot.service", CWD: "/home/alfred/notifbot"}
	a.event(patchEv("laptop", t0,
		model.Change{Op: model.OpAdd, Kind: model.KindRepo, Key: repo.Key, Data: repo},
		model.Change{Op: model.OpAdd, Kind: model.KindListener, Key: host.Key, Data: host},
		model.Change{Op: model.OpAdd, Kind: model.KindListener, Key: funnel.Key, Data: funnel},
		model.Change{Op: model.OpAdd, Kind: model.KindListener, Key: bot.Key, Data: bot},
	), t0)
	return a
}

// plateOf ouvre la planche de l'objet dont l'id contient key et renvoie son image.
func plateOf(t *testing.T, a *App, key string, now time.Time) string {
	t.Helper()
	for _, mv := range a.w.machines {
		for _, it := range mv.items {
			if strings.Contains(it.id, key) {
				a.plate, a.view = it.id, viewPlate
				return screenOf(t, a, now)
			}
		}
	}
	t.Fatalf("aucun objet %q", key)
	return ""
}

// flat recolle les rangées d'une image en une phrase : un texte replié s'y
// retrouve d'un seul tenant.
func flat(screen string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(screen, "\u00a0", " ")), " ")
}

func TestL7Catalogue(t *testing.T) {
	a := l7App(t, 120, 50) // assez haut pour que les dépôts paraissent
	all := flat(screenOf(t, a, t0.Add(5*time.Second)))
	for _, want := range []string{"2 à pousser", "3 en retard", "Internet (Funnel)", "cette machine seulement", "notifbot"} {
		if !strings.Contains(all, want) {
			t.Errorf("catalogue : « %s » absent\n%s", want, all)
		}
	}
	if strings.Contains(all, "MainThread") {
		t.Errorf("catalogue : le nom de fil MainThread paraît au lieu de l'unité\n%s", all)
	}
}

func TestL7Plates(t *testing.T) {
	now := t0.Add(5 * time.Second)
	for _, c := range []struct {
		key  string
		want []string
	}{
		{"/home/alfred/p/site", []string{"origin/main", "2 commits pas encore poussés", "3 commits de origin/main pas encore pris", "d’après le dernier fetch"}},
		{"172.17.0.1:4000", []string{"cette machine", "conteneurs"}},
		{"100.64.0.1:443", []string{"Funnel", "Internet"}},
		{"127.0.0.1:7070", []string{"notifbot", "notifbot.service", "python3.12"}},
	} {
		a := l7App(t, 120, 36)
		all := flat(plateOf(t, a, c.key, now))
		for _, w := range c.want {
			if !strings.Contains(all, w) {
				t.Errorf("planche de %s : « %s » absent\n%s", c.key, w, all)
			}
		}
	}
}

// La légende explique t’attend et les trois mots des dépôts, en entier, à
// toutes les tailles utilisables.
func TestLegendExplainsWords(t *testing.T) {
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {120, 36}, {160, 45}} {
		a, _ := newTestApp(t, size[0], size[1])
		a.view = viewLegend
		all := flat(screenOf(t, a, t0.Add(5*time.Second)))
		if !strings.Contains(all, flat(legendWords)) {
			t.Errorf("légende à %d × %d : l’explication des mots est incomplète\n%s", size[0], size[1], all)
		}
	}
}

// Chercher « pousser » ou « retard » trouve les dépôts concernés.
func TestSearchSyncWords(t *testing.T) {
	a := l7App(t, 120, 36)
	for _, q := range []string{"pousser", "en retard", "origin/main"} {
		found := false
		for _, it := range a.w.find(q, false) {
			found = found || (it.repo != nil && it.repo.Path == "/home/alfred/p/site")
		}
		if !found {
			t.Errorf("chercher %q ne trouve pas le dépôt site", q)
		}
	}
}

package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/canvas"
	"github.com/Alexry375/connex/internal/ui/term"
)

// openCall est un appel de la fausse fonction d'ouverture.
type openCall struct {
	machine, app string
	snaps        map[string]model.Snapshot
}

// fakeOpen retient ses appels et rend url, ou err.
type fakeOpen struct {
	calls []openCall
	url   string
	err   error
}

func (f *fakeOpen) open(machine, app string, snaps map[string]model.Snapshot) (string, error) {
	f.calls = append(f.calls, openCall{machine, app, snaps})
	if f.err != nil {
		return "", f.err
	}
	return f.url, nil
}

// withNamesakes ajoute sur le laptop, première machine, un dépôt, un agent et
// un port qui portent le nom de l'appli site du vps.
func withNamesakes(t *testing.T, a *App, now time.Time) {
	t.Helper()
	l := laptopSnap(now)
	l.Repos = append(l.Repos, model.Repo{Key: "/home/alfred/site", Path: "/home/alfred/site", Branch: "main", Worktrees: []model.Worktree{}})
	l.Agents = append(l.Agents, model.Agent{Key: "claude:209", Tool: "claude", PID: 209, CWD: "/home/alfred/site", State: model.StateWorking})
	l.Listeners = append(l.Listeners, model.Listener{Key: "tcp4:127.0.0.1:7000", Port: 7000, Addr: "127.0.0.1", Exposure: "loopback",
		Mine: true, UID: 1000, PID: 210, Process: "site", CWD: "/home/alfred/site", ProjectRoot: "/home/alfred/site"})
	a.event(patchEv("laptop", now,
		model.Change{Op: model.OpAdd, Kind: model.KindRepo, Key: l.Repos[len(l.Repos)-1].Key, Data: l.Repos[len(l.Repos)-1]},
		model.Change{Op: model.OpAdd, Kind: model.KindAgent, Key: l.Agents[len(l.Agents)-1].Key, Data: l.Agents[len(l.Agents)-1]},
		model.Change{Op: model.OpAdd, Kind: model.KindListener, Key: l.Listeners[len(l.Listeners)-1].Key, Data: l.Listeners[len(l.Listeners)-1]},
	), now)
}

// screen renvoie l'image de l'instant now, rangée par rangée.
func screenOf(t *testing.T, a *App, now time.Time) string {
	t.Helper()
	return strings.Join(rowsOf(t, a, now), "\n")
}

// /site puis Entrée ouvre la planche de l'appli site, même quand un dépôt, un
// agent et un port du laptop portent ce nom ; o appelle Open une fois, avec la
// machine, l'appli et le relevé de chaque machine connue. L'écran montre
// l'adresse, le journal la note.
func TestOpenKey(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 36}, {160, 45}} {
		a, src := newTestApp(t, size[0], size[1])
		f := &fakeOpen{url: "http://100.64.0.2:3000/"}
		a.open = f.open
		now := t0.Add(5 * time.Second)
		withNamesakes(t, a, now)
		if got := a.w.find("site", false); len(got) < 2 || got[0].id == appSite {
			t.Fatalf("le monde du test doit mettre un homonyme avant l'appli sans la règle du nom entier : %v", ids(got))
		}

		a.key(term.R('/'), now)
		typeText(a, "site", now)
		a.key(code(term.Enter), now)
		if a.view != viewPlate || a.plate != appSite {
			t.Fatalf("%v : /site Entrée → vue %d, planche %q ; attendu la planche de l'appli site", size, a.view, a.plate)
		}
		if all := screenOf(t, a, now); !strings.Contains(all, "o ouvrir") {
			t.Errorf("%v : la marge de l'appli ne propose pas « o ouvrir »\n%s", size, all)
		} else if last := rowsOf(t, a, now)[size[1]-1]; strings.Index(last, "o ouvrir") > strings.Index(last, "a arrêter") {
			t.Errorf("%v : « o ouvrir » doit précéder les autres actions : %q", size, last)
		}
		if len(f.calls) != 0 {
			t.Fatalf("Open appelée avant o : %+v", f.calls)
		}

		a.key(term.R('o'), now)
		if len(f.calls) != 1 {
			t.Fatalf("%v : Open appelée %d fois, attendu 1", size, len(f.calls))
		}
		c := f.calls[0]
		if c.machine != "vps" || c.app != "site" {
			t.Errorf("Open(%q, %q), attendu (vps, site)", c.machine, c.app)
		}
		if len(c.snaps) != 2 {
			t.Errorf("relevés passés : %d, attendu 2 (laptop et vps)", len(c.snaps))
		}
		for _, m := range a.fleet.Machines() {
			s, ok := c.snaps[m.Name]
			if !ok {
				t.Errorf("relevé de %s absent", m.Name)
				continue
			}
			if s.Machine.Name != m.Name || len(s.Apps) != len(m.Snapshot.Apps) || len(s.Listeners) != len(m.Snapshot.Listeners) {
				t.Errorf("relevé de %s : ce n'est pas le dernier connu", m.Name)
			}
		}
		if all := screenOf(t, a, now.Add(time.Second)); !strings.Contains(all, "http://100.64.0.2:3000/") {
			t.Errorf("%v : l'adresse ouverte n'est pas à l'écran\n%s", size, all)
		}
		if e := lastEntry(t, a); !strings.Contains(e.text, "http://100.64.0.2:3000/") || e.item != appSite || e.machine != "vps" {
			t.Errorf("journal : %+v", e)
		}
		if len(src.sent) != 0 {
			t.Errorf("o a envoyé une requête à la source : %+v", src.sent)
		}
	}
}

// o n'appelle pas Open hors de la planche d'une appli ; sur une autre planche
// ou un autre objet, il dit en une ligne ce qu'il fait. Sur la planche I, o
// commence une recherche.
func TestOpenKeyElsewhere(t *testing.T) {
	a, _ := newTestApp(t, 80, 24)
	f := &fakeOpen{url: "http://127.0.0.1:5000/"}
	a.open = f.open
	now := t0.Add(5 * time.Second)

	a.key(term.R('o'), now)
	if !a.typing || a.query != "o" || len(f.calls) != 0 {
		t.Fatalf("planche I : o doit commencer une recherche (typing=%v, query=%q, appels=%d)", a.typing, a.query, len(f.calls))
	}
	a.key(code(term.Esc), now)

	for _, id := range []string{port5000, agentWait, port8384} {
		a.plate, a.view = id, viewPlate
		a.key(term.R('o'), now)
		if a.notice == nil || a.notice.text != openWhat {
			t.Errorf("planche de %s : o doit expliquer ce qu'il fait, notice %+v", id, a.notice)
		}
	}
	for _, v := range []view{viewJournal, viewLegend} {
		a.view = v
		a.key(term.R('o'), now)
		if a.notice == nil || a.notice.text != openWhat {
			t.Errorf("vue %d : o doit expliquer ce qu'il fait, notice %+v", v, a.notice)
		}
		if all := screenOf(t, a, now); !strings.Contains(all, openWhat) {
			t.Errorf("vue %d : l'explication de o n'est pas à l'écran\n%s", v, all)
		}
	}
	if canvas.StringWidth(openWhat) > 78 {
		t.Errorf("l'explication de o tient sur plus d'une ligne à 80 colonnes : %d", canvas.StringWidth(openWhat))
	}
	if len(f.calls) != 0 {
		t.Fatalf("Open appelée hors d'une appli : %+v", f.calls)
	}

	// Une appli disparue du relevé : pas d'appel.
	a.plate, a.view = itemID("laptop", kindApp, "disparue"), viewPlate
	a.key(term.R('o'), now)
	if len(f.calls) != 0 {
		t.Fatalf("Open appelée sur une appli disparue : %+v", f.calls)
	}
}

// Dans la démo (Open nil), o sur une appli dit que la démo n'ouvre rien.
func TestOpenKeyDemo(t *testing.T) {
	a, src := newTestApp(t, 80, 24)
	now := t0.Add(5 * time.Second)
	a.plate, a.view = appSite, viewPlate
	if strings.Contains(screenOf(t, a, now), "ouvre rien") {
		t.Fatal("« ouvre rien » est à l'écran avant o")
	}
	a.key(term.R('o'), now)
	all := screenOf(t, a, now)
	if !strings.Contains(strings.Join(strings.Fields(all), " "), "la démo n’ouvre rien") {
		t.Errorf("démo : l'écran ne dit pas que la démo n'ouvre rien\n%s", all)
	}
	if a.quit || len(src.sent) != 0 {
		t.Errorf("démo : quit=%v, requêtes %+v", a.quit, src.sent)
	}
}

// Un refus s'affiche en entier, même long, à toutes les tailles ; rien n'est
// noté comme ouvert.
func TestOpenRefusalWhole(t *testing.T) {
	reason := "« planetaire » ne tourne pas sur laptop (état : stopped) ; démarre-la avec d, ou lance `connex start planetaire`, puis réessaie, l’adresse sera alors http://127.0.0.1:5000/"
	for _, size := range [][2]int{{60, 20}, {80, 24}, {120, 36}, {160, 45}} {
		a, _ := newTestApp(t, size[0], size[1])
		f := &fakeOpen{err: errors.New(reason)}
		a.open = f.open
		now := t0.Add(5 * time.Second)
		a.plate, a.view = appPoly, viewPlate
		n := len(a.journal)
		a.key(term.R('o'), now)
		if len(f.calls) != 1 || f.calls[0].machine != "laptop" || f.calls[0].app != "planetaire" {
			t.Fatalf("%v : appels %+v", size, f.calls)
		}
		rows := rowsOf(t, a, now.Add(time.Second))
		all := strings.Join(rows, "\n")
		lines := wrap(a.notice.text, size[0]-2)
		for i, line := range lines {
			// Chaque ligne du refus a sa rangée à elle, en bas de la marge :
			// rien d'autre (la note d'observation) n'y dépasse.
			row := rows[size[1]-len(lines)+i]
			if strings.TrimSpace(row) != strings.TrimSpace(line) {
				t.Errorf("%v : rangée du refus %q, attendu %q\n%s", size, row, line, all)
			}
		}
		if !strings.Contains(a.notice.text, reason) {
			t.Errorf("%v : la raison rendue n'est pas reprise telle quelle : %q", size, a.notice.text)
		}
		if len(a.journal) != n {
			t.Errorf("%v : un refus ne s'inscrit pas comme une ouverture : %+v", size, lastEntry(t, a))
		}
		// Passé le délai du message, la marge reprend sa hauteur.
		later := now.Add(noticeFor + time.Second)
		a.frame(later)
		if a.notice != nil || a.paperRows() > 3 {
			t.Errorf("%v : le message ne se retire pas (notice %v, marge %d rangées)", size, a.notice, a.paperRows())
		}
	}
}

// Un refus trop long pour la marge perd sa fin, marquée « … », jamais son
// début ; un retour à la ligne dans la raison n'en est pas un à l'écran.
func TestOpenRefusalTooLong(t *testing.T) {
	reason := "DEBUT " + strings.Repeat("une raison vraiment très longue, ", 20) + "\nFIN"
	for _, size := range [][2]int{{40, 12}, {60, 20}, {80, 24}, {160, 45}} {
		a, _ := newTestApp(t, size[0], size[1])
		a.open = (&fakeOpen{err: errors.New(reason)}).open
		now := t0.Add(5 * time.Second)
		a.plate, a.view = appPoly, viewPlate
		a.key(term.R('o'), now)
		if strings.Contains(a.notice.text, "\n") {
			t.Errorf("%v : retour à la ligne gardé dans le message", size)
		}
		all := strings.Join(strings.Fields(screenOf(t, a, now)), " ")
		if !strings.Contains(all, "L’appli planetaire ne s’ouvre pas : DEBUT") {
			t.Errorf("%v : le début du refus est perdu\n%s", size, screenOf(t, a, now))
		}
		if !strings.Contains(all, "FIN.") && !strings.Contains(all, "…") {
			t.Errorf("%v : ni la fin du refus ni « … »\n%s", size, screenOf(t, a, now))
		}
	}
}

// Constat 3 : pendant une recherche, la ligne de recherche rappelle que q
// quitte et que Q écrit un q, à toutes les largeurs et pour toute requête ;
// Q cherche bien un q, sans quitter.
func TestSearchLineSaysQQuits(t *testing.T) {
	for w := minW; w <= 160; w++ {
		for _, q := range []string{"p", "a", "site", "polym", "", strings.Repeat("abcdefghi", 5)} {
			a, _ := newTestApp(t, w, 24)
			now := t0.Add(5 * time.Second)
			a.key(term.R('/'), now)
			typeText(a, q, now)
			rows := rowsOf(t, a, now)
			if last := rows[len(rows)-1]; !strings.Contains(last, "q quitte · Q écrit un q") {
				t.Errorf("%d colonnes, « %s » : la ligne de recherche ne dit pas « q quitte · Q écrit un q » : %q", w, q, last)
			}
		}
	}
	for _, size := range [][2]int{{60, 20}, {80, 24}, {120, 36}, {160, 45}} {
		a, _ := newTestApp(t, size[0], size[1])
		now := t0.Add(5 * time.Second)
		typeText(a, "polym", now)
		a.key(term.R('Q'), now)
		if a.quit || a.query != "polymQ" {
			t.Errorf("%v : Q doit s'écrire (quit=%v, query=%q)", size, a.quit, a.query)
		}
	}
	// La note des touches se lit en entier à toutes les tailles.
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {120, 36}, {160, 45}} {
		a, _ := newTestApp(t, size[0], size[1])
		a.view = viewLegend
		all := strings.Join(strings.Fields(screenOf(t, a, t0.Add(5*time.Second))), " ")
		if !strings.Contains(all, legendKeysNote) {
			t.Errorf("légende à %v : la note des touches est coupée\n%s", size, all)
		}
	}
	// La légende montre o et Q, sans perdre ses vignettes à 80 × 24.
	a, _ := newTestApp(t, 80, 24)
	a.view = viewLegend
	rows := rowsOf(t, a, t0.Add(5*time.Second))
	all := strings.Join(rows, "\n")
	for _, s := range []string{"o ouvrir", "Q taper un q", "o l’ouvre dans le navigateur"} {
		if !strings.Contains(strings.Join(strings.Fields(all), " "), s) {
			t.Errorf("légende à 80 × 24 : « %s » absent\n%s", s, all)
		}
	}
	if !strings.ContainsAny(strings.Join(rows[:4], ""), "▀▄█") {
		t.Errorf("légende à 80 × 24 : les vignettes ont disparu\n%s", all)
	}
}

// Constat 6 : l'encre d'une étoile ne dit que des faits. Vingt ports
// identiques en tout ont la même encre, et un ciel où rien ne change reste
// immobile : aucune étoile ne scintille au hasard.
func TestStarsShineForAReason(t *testing.T) {
	a := newApp(&fakeSrc{}, t0)
	a.resize(160, 45)
	s := model.NewSnapshot(model.Machine{Name: "laptop", OS: "linux", Arch: "amd64", User: "alfred"}, t0)
	for p := 6000; p < 6020; p++ {
		s.Listeners = append(s.Listeners, model.Listener{Key: "tcp4:127.0.0.1:" + itoa(p), Port: p, Addr: "127.0.0.1", Exposure: "loopback",
			Mine: true, UID: 1000, PID: 500 + p, Process: "node", CWD: "/home/alfred/p/x" + itoa(p), ProjectRoot: "/home/alfred/p/x" + itoa(p)})
	}
	a.event(source.Event{Machine: "laptop", Snapshot: &s}, t0)
	sc := buildScene(a.w, 160, 30)
	inks := map[canvas.Color]int{}
	for _, st := range sc.stars {
		inks[st.fg]++
	}
	if len(sc.stars) != 20 || len(inks) != 1 {
		t.Fatalf("20 ports identiques : %d étoiles, encres %v", len(sc.stars), inks)
	}
	var first []canvas.Cell
	for step := int64(0); step < 80; step++ { // 20 s
		cv := canvas.New(160, 30)
		sc.render(cv, 0, skyState{now: t0.Add(time.Duration(step) * stepDur), step: step, refused: -1})
		if step == 0 {
			first = append([]canvas.Cell(nil), cv.Cells...)
			continue
		}
		for i, c := range cv.Cells {
			if c != first[i] {
				t.Fatalf("pas %d, case (%d, %d) : %+v devient %+v sans que rien n'arrive (scintillement au hasard ?)",
					step, i%160, i/160, first[i], c)
			}
		}
	}
}

package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/canvas"
	"github.com/Alexry375/connex/internal/ui/term"
)

// Ce qu'une touche mal placée ne doit jamais faire, et ce qu'une requête
// perdue doit dire. Cas tirés de la relecture adverse du lot L4.

func sentOps(src *fakeSrc, from int) []string {
	var out []string
	for i := from; i < len(src.sent); i++ {
		out = append(out, src.to[i]+":"+src.sent[i].Op+":"+src.sent[i].Target)
	}
	return out
}

// Sur la planche I, taper un nom sans « / » cherche ; aucune lettre n'agit.
func TestTypingNeverActs(t *testing.T) {
	now := t0.Add(5 * time.Second)
	for _, sel := range []string{appSite, appPoly, itemID("laptop", kindForeign, "tcp4:0.0.0.0:22")} {
		for _, word := range []string{"tango", "dotfiles", "llama", "aa", "rr", "dd", "ll", "u"} {
			a, src := newTestApp(t, 80, 24)
			a.selectItem(a.w.byID[sel])
			typeText(a, word, now)
			a.key(code(term.Enter), now)
			if got := sentOps(src, 0); len(got) > 0 || a.confirm != nil {
				t.Errorf("« %s⏎ » tapé sur %q : envoyé %v, question %+v", word, sel, got, a.confirm)
			}
			if a.query != word {
				t.Errorf("« %s » tapé : recherche %q", word, a.query)
			}
		}
	}
}

// Sur la planche d'un objet, une rafale de frappe finie par Entrée ne confirme
// rien : la question n'est pas encore armée.
func TestBurstNeverConfirms(t *testing.T) {
	now := t0.Add(5 * time.Second)
	for _, word := range []string{"llama", "dev", "ra", "arrêter", "a"} {
		a, src := newTestApp(t, 80, 24)
		a.plate, a.view = appSite, viewPlate
		at := now
		for _, r := range word {
			a.key(term.R(r), at)
			at = at.Add(60 * time.Millisecond)
		}
		a.key(code(term.Enter), at)
		a.key(code(term.Enter), at.Add(60*time.Millisecond))
		if got := sentOps(src, 0); len(got) > 0 {
			t.Errorf("« %s⏎⏎ » en rafale sur la planche de site : envoyé %v", word, got)
		}
	}
}

// La question s'arme après confirmArm : « Entrée : oui » paraît alors, et
// l'affichage se réveille pour l'écrire.
func TestConfirmArms(t *testing.T) {
	a, src := newTestApp(t, 120, 36)
	now := t0.Add(5 * time.Second)
	a.plate, a.view = appSite, viewPlate
	a.key(term.R('r'), now)
	if d := a.nextWake(now); d > confirmArm {
		t.Errorf("réveil dans %v, après l'armement (%v)", d, confirmArm)
	}
	before := strings.Join(rowsOf(t, a, now.Add(confirmArm-time.Millisecond)), "\n")
	after := strings.Join(rowsOf(t, a, now.Add(confirmArm)), "\n")
	if strings.Contains(before, confirmYes) || !strings.Contains(after, confirmYes) {
		t.Errorf("« %s » : avant l'armement %v, après %v", confirmYes, strings.Contains(before, confirmYes), strings.Contains(after, confirmYes))
	}
	a.key(code(term.Enter), now.Add(confirmArm))
	if got := sentOps(src, 0); len(got) != 1 || got[0] != "vps:restart:site" {
		t.Fatalf("relancer site : %v", got)
	}
}

// Sous 40 × 12, rien ne se voit : aucune touche n'agit, sauf q et Ctrl-C ; une
// question posée avant de rétrécir la fenêtre est retirée.
func TestTinyWindowIgnoresKeys(t *testing.T) {
	now := t0.Add(5 * time.Second)
	a, src := newTestApp(t, 80, 24)
	a.plate, a.view = appSite, viewPlate
	a.key(term.R('r'), now)
	a.resize(30, 8)
	if a.confirm != nil {
		t.Fatal("la question reste en suspens dans une fenêtre où elle ne se voit pas")
	}
	for _, k := range []term.Key{term.R('r'), code(term.Enter), term.R('a'), code(term.Enter), code(term.F5), term.R('d'), code(term.Tab)} {
		a.key(k, now.Add(time.Second))
	}
	if got := sentOps(src, 0); len(got) > 0 || a.confirm != nil || a.view != viewPlate {
		t.Fatalf("touches à 30 × 8 : envoyé %v, question %+v, vue %d", got, a.confirm, a.view)
	}
	a.key(term.R('q'), now)
	if !a.quit {
		t.Fatal("q ne quitte pas à 30 × 8")
	}
	// À 20 × 5, le moyen de sortir reste écrit.
	b, _ := newTestApp(t, 20, 5)
	if rows := rowsOf(t, b, now); !strings.Contains(rows[4], "q quitte.") {
		t.Errorf("20 × 5 : « q quitte. » absent\n%s", strings.Join(rows, "\n"))
	}
}

// Une requête dont la machine devient injoignable, ou dont la source s'arrête,
// est close « sans réponse » ; une réponse tardive est ignorée.
func TestPendingClosedWithoutAnswer(t *testing.T) {
	now := t0.Add(5 * time.Second)
	a, src := newTestApp(t, 80, 24)
	a.plate, a.view = appSite, viewPlate
	a.key(term.R('a'), now)
	a.key(code(term.Enter), now.Add(confirmArm))
	a.plate = appPoly
	a.key(term.R('d'), now)
	a.key(code(term.Enter), now.Add(confirmArm))
	if len(a.pending) != 2 {
		t.Fatalf("2 requêtes en attente voulues : %v", sentOps(src, 0))
	}

	lost := now.Add(2 * time.Second)
	a.event(statusEv("vps", false, "ssh: connexion perdue", lost), lost)
	if len(a.pending) != 1 {
		t.Fatalf("la requête vers vps reste en attente : %d", len(a.pending))
	}
	if e := lastEntry(t, a); e.text != "arrêter l’appli site : sans réponse — la machine a cessé de répondre" || e.tone != toneWarn || e.sign != '◐' {
		t.Fatalf("sans réponse : %+v", e)
	}
	a.view = viewSky
	a.selectItem(a.w.byID[appSite])
	if all := strings.Join(rowsOf(t, a, lost), "\n"); !strings.Contains(all, "◐ sans réponse") {
		t.Errorf("ligne de site : « sans réponse » absent\n%s", all)
	}
	// La réponse arrive quand même : ignorée, le relevé suivant dira l'état.
	n := len(a.journal)
	a.event(source.Event{Machine: "vps", Reply: &model.Reply{ID: src.sent[0].ID, OK: true}}, lost.Add(time.Second))
	if len(a.journal) != n {
		t.Errorf("réponse tardive racontée : %+v", lastEntry(t, a))
	}

	// La source s'arrête : tout ce qui attend encore est clos.
	a.sourceClosed(lost.Add(2 * time.Second))
	if len(a.pending) != 0 {
		t.Fatalf("requêtes en attente après l'arrêt de la source : %d", len(a.pending))
	}
	found := false
	for _, e := range a.journal {
		if e.text == "démarrer l’appli planetaire : sans réponse — la source s’est arrêtée" {
			found = true
		}
	}
	if !found {
		t.Errorf("démarrer planetaire : pas clos à l'arrêt de la source")
	}
	later := lost.Add(time.Hour)
	if d := a.nextWake(later); d <= 120*time.Millisecond {
		t.Errorf("une heure après, réveil dans %v : l'indicateur tourne encore", d)
	}
	// Une requête close sans réponse n'est pas un refus : pas de protubérances.
	if r := a.lastRefused(); r != nil {
		t.Errorf("sans réponse compté comme refus : %+v", r)
	}
}

// Une question que le relevé ne confirme plus est retirée, et l'écran le dit :
// un autre programme sur le port à libérer, l'appli désinscrite, la machine
// qui ne répond plus. Entrée, ensuite, n'envoie rien. Un changement ailleurs
// ne la retire pas. (Sondes S1 à S3 de la seconde relecture.)
func TestStaleQuestionWithdrawn(t *testing.T) {
	now := t0.Add(5 * time.Second)
	java := model.Listener{Key: "tcp4:127.0.0.1:5000", Port: 5000, Addr: "127.0.0.1", Exposure: "loopback", Mine: true, UID: 1000,
		PID: 999, Process: "java", CWD: "/home/alfred/p/autre", ProjectRoot: "/home/alfred/p/autre"}
	cases := []struct {
		name, plate string
		key         rune
		ev          source.Event
		why         string
	}{
		{"le port change de main", port5000, 'l', patchEv("laptop", now, model.Change{Op: model.OpUpdate, Kind: model.KindListener, Key: java.Key, Data: java}),
			"Question retirée : l’objet a changé depuis."},
		{"l'appli est désinscrite", appPoly, 'd', patchEv("laptop", now, model.Change{Op: model.OpRemove, Kind: model.KindApp, Key: "planetaire"}),
			"Question retirée : cet objet n’est plus au relevé."},
		{"la machine ne répond plus", appSite, 'r', statusEv("vps", false, "ssh: connexion perdue", now),
			"Question retirée : la machine vps ne répond plus."},
	}
	for _, c := range cases {
		a, src := newTestApp(t, 120, 36)
		a.plate, a.view = c.plate, viewPlate
		a.key(term.R(c.key), now)
		if a.confirm == nil {
			t.Fatalf("%s : pas de question", c.name)
		}
		at := now.Add(time.Second)
		a.event(c.ev, at)
		if a.confirm != nil {
			t.Errorf("%s : la question reste posée : %+v", c.name, a.confirm)
		}
		all := strings.Join(rowsOf(t, a, at), "\n")
		if !strings.Contains(all, c.why) || strings.Contains(all, confirmYes) {
			t.Errorf("%s : l'écran doit dire « %s », sans « %s »\n%s", c.name, c.why, confirmYes, all)
		}
		a.key(code(term.Enter), at.Add(time.Second))
		if got := sentOps(src, 0); len(got) > 0 {
			t.Errorf("%s : Entrée a envoyé %v", c.name, got)
		}
	}
	// Un changement qui ne touche pas l'objet laisse la question posée.
	a, src := newTestApp(t, 120, 36)
	a.plate, a.view = port5000, viewPlate
	a.key(term.R('l'), now)
	clean := false
	nuage := model.Repo{Key: "/home/alfred/p/nuage", Path: "/home/alfred/p/nuage", Branch: "main", Dirty: &clean, Worktrees: []model.Worktree{}}
	a.event(patchEv("laptop", now, model.Change{Op: model.OpUpdate, Kind: model.KindRepo, Key: nuage.Key, Data: nuage}), now.Add(time.Second))
	a.key(code(term.Enter), now.Add(2*time.Second))
	if got := sentOps(src, 0); len(got) != 1 || got[0] != "laptop:free_port:5000" {
		t.Errorf("changement ailleurs : envoyé %v, attendu la libération du port 5000", got)
	}
}

// Une question sans réponse est retirée au bout de confirmTTL : l'image le dit,
// l'affichage se réveille pour cela, et une Entrée tardive n'envoie rien, même
// sans image entre-temps.
func TestQuestionExpires(t *testing.T) {
	now := t0.Add(5 * time.Second)
	a, _ := newTestApp(t, 80, 24)
	a.plate, a.view = appSite, viewPlate
	a.key(term.R('a'), now)
	if d := a.nextWake(now.Add(confirmTTL - 300*time.Millisecond)); d > 300*time.Millisecond {
		t.Errorf("réveil dans %v : la question expire dans 300 ms", d)
	}
	rowsOf(t, a, now.Add(confirmTTL-time.Millisecond))
	if a.confirm == nil {
		t.Fatal("question retirée avant son terme")
	}
	all := strings.Join(rowsOf(t, a, now.Add(confirmTTL)), "\n")
	if a.confirm != nil || !strings.Contains(all, "Question retirée au bout de 30 s : rien n’a été fait.") {
		t.Errorf("question expirée : %+v\n%s", a.confirm, all)
	}

	b, src := newTestApp(t, 80, 24)
	b.plate, b.view = appSite, viewPlate
	b.key(term.R('a'), now)
	b.key(code(term.Enter), now.Add(confirmTTL+time.Second))
	if got := sentOps(src, 0); len(got) > 0 || b.confirm != nil || b.view != viewPlate {
		t.Errorf("Entrée tardive : envoyé %v, question %+v, vue %d", got, b.confirm, b.view)
	}
}

// d, a, r et l ne posent que les questions que la marge propose ; sinon l'écran
// dit pourquoi, et rien ne part. Sur une machine qui ne répond plus, la marge
// ne propose aucune action. (Sondes S8, S11 et S12 de la seconde relecture.)
func TestKeysOnlyWhatIsOffered(t *testing.T) {
	now := t0.Add(5 * time.Second)
	atlas := model.App{Key: "atlas", Name: "atlas", Project: "/home/alfred/p/atlas", CWD: "/home/alfred/p/atlas", Port: 4000,
		Unit: "connex-atlas.service", State: model.AppStopped, View: model.ViewRemote}
	cases := []struct {
		plate string
		key   rune
		say   string
	}{
		{appSite, 'd', "L’appli site est déjà en marche."},
		{appPoly, 'a', "L’appli planetaire ne tourne pas."},
		{appPoly, 'r', "L’appli planetaire ne tourne pas."},
		{itemID("laptop", kindApp, "atlas"), 'l', "Rien n’écoute sur le port 4000."},
		{agentWait, 'l', "Rien à libérer ici : choisis un port, ou une appli qui en a un."},
		{port5000, 'd', "Seule une appli inscrite se démarre, s’arrête ou se relance."},
	}
	for _, c := range cases {
		a, src := newTestApp(t, 80, 24)
		a.event(patchEv("laptop", now, model.Change{Op: model.OpAdd, Kind: model.KindApp, Key: atlas.Key, Data: atlas}), now)
		a.plate, a.view = c.plate, viewPlate
		a.key(term.R(c.key), now)
		if a.confirm != nil || a.notice == nil || a.notice.text != c.say {
			t.Errorf("« %c » sur %q : question %+v, message %+v, attendu « %s »", c.key, c.plate, a.confirm, a.notice, c.say)
		}
		a.key(code(term.Enter), now.Add(confirmArm))
		if got := sentOps(src, 0); len(got) > 0 {
			t.Errorf("« %c⏎ » sur %q : envoyé %v", c.key, c.plate, got)
		}
	}

	a, src := newTestApp(t, 120, 36)
	a.plate, a.view = appSite, viewPlate
	if rows := rowsOf(t, a, now); !strings.Contains(rows[len(rows)-1], "arrêter") {
		t.Fatalf("vps joignable : la marge devrait proposer d'arrêter site\n%s", rows[len(rows)-1])
	}
	a.event(statusEv("vps", false, "ssh: connexion perdue", now), now)
	rows := rowsOf(t, a, now)
	for _, h := range []string{"arrêter", "relancer", "libérer le port"} {
		if strings.Contains(rows[len(rows)-1], h) {
			t.Errorf("vps muette : la marge propose « %s »\n%s", h, rows[len(rows)-1])
		}
	}
	for _, k := range "darl" {
		a.key(term.R(k), now)
		if a.confirm != nil || a.notice == nil || a.notice.text != "La machine vps ne répond plus." {
			t.Errorf("« %c » sur site, vps muette : question %+v, message %+v", k, a.confirm, a.notice)
		}
		a.key(code(term.Enter), now.Add(confirmArm))
		a.view = viewPlate
	}
	if got := sentOps(src, 0); len(got) > 0 {
		t.Errorf("vps muette : envoyé %v", got)
	}
}

// Un agent d'état inconnu n'est jamais compté comme « ne t'attend pas ».
func TestTitleUnknownAgents(t *testing.T) {
	cases := []struct {
		states      []string
		full, short string
	}{
		{[]string{model.StateUnknown, model.StateUnknown}, "AUCUN AGENT NE T’ATTEND, SAUF PEUT-ÊTRE 2 D’ÉTAT INCONNU.", "AUCUN AGENT NE T’ATTEND, SAUF PEUT-ÊTRE 2."},
		{[]string{model.StateWorking, model.StateUnknown}, "AUCUN AGENT NE T’ATTEND, SAUF PEUT-ÊTRE UN D’ÉTAT INCONNU.", "AUCUN AGENT NE T’ATTEND, SAUF PEUT-ÊTRE UN."},
		{[]string{model.StateWorking, model.StateIdle}, "AUCUN AGENT NE T’ATTEND.", "AUCUN AGENT NE T’ATTEND."},
		{[]string{model.StateWaiting, model.StateUnknown}, "UN AGENT T’ATTEND SUR LAPTOP.", "UN AGENT T’ATTEND SUR LAPTOP."},
		{[]string{"un état que la source ajoutera"}, "AUCUN AGENT NE T’ATTEND, SAUF PEUT-ÊTRE UN D’ÉTAT INCONNU.", "AUCUN AGENT NE T’ATTEND, SAUF PEUT-ÊTRE UN."},
	}
	for _, c := range cases {
		a := newApp(&fakeSrc{}, t0)
		a.resize(80, 24)
		l := laptopSnap(t0)
		l.Agents = nil
		for i, st := range c.states {
			l.Agents = append(l.Agents, model.Agent{Key: fmt.Sprintf("codex:%d", 400+i), Tool: "codex", PID: 400 + i, CWD: "/home/alfred/p/nuage", State: st})
		}
		a.event(source.Event{Machine: "laptop", Snapshot: &l}, t0)
		if full, short, _ := a.title(); full != c.full || short != c.short {
			t.Errorf("%v : %q / %q, voulu %q / %q", c.states, full, short, c.full, c.short)
		}
	}
}

// Une machine annoncée sans relevé encore : le titre ne conclut rien sur elle.
func TestTitleMachineWithoutSnapshot(t *testing.T) {
	a := newApp(&fakeSrc{}, t0)
	a.resize(80, 24)
	v := vpsSnap(t0)
	a.event(source.Event{Machine: "vps", Snapshot: &v}, t0)
	a.event(statusEv("laptop", true, "", t0), t0)
	full, short, _ := a.title()
	if full != "RELEVÉ EN COURS SUR LAPTOP · AUCUN AGENT EN COURS AILLEURS." || short != "RELEVÉ EN COURS SUR LAPTOP." {
		t.Errorf("titre : %q / %q", full, short)
	}
	// Machine muette et agents d'état inconnu ailleurs.
	b, _ := newTestApp(t, 80, 24)
	l := laptopSnap(t0)
	l.Agents = []model.Agent{{Key: "codex:202", Tool: "codex", PID: 202, CWD: "/home/alfred/p/planetaire", State: model.StateUnknown}}
	b.event(source.Event{Machine: "laptop", Snapshot: &l}, t0)
	b.event(statusEv("vps", false, "ssh : délai dépassé", t0), t0)
	full, short, _ = b.title()
	if full != "VPS NE RÉPOND PLUS · AUCUN AGENT NE T’ATTEND AILLEURS, SAUF PEUT-ÊTRE UN D’ÉTAT INCONNU." || short != "VPS NE RÉPOND PLUS." {
		t.Errorf("titre : %q / %q", full, short)
	}
}

// Journal plein, regardé avec une ligne choisie : une nouvelle entrée ne fait
// pas changer la ligne choisie.
func TestJournalKeepsSelection(t *testing.T) {
	for _, count := range []int{10, maxJournal} {
		a, _ := newTestApp(t, 80, 24)
		a.journal = nil
		for i := 0; i < count; i++ {
			a.addEntry(entry{text: fmt.Sprintf("e%03d", i)})
		}
		a.view = viewJournal
		a.jsel = 5
		want := a.journal[len(a.journal)-1-a.jsel].text
		a.addEntry(entry{text: "nouvelle"})
		if got := a.journal[len(a.journal)-1-a.jsel].text; got != want {
			t.Errorf("%d entrées : la ligne choisie passe de %s à %s", count, want, got)
		}
	}
}

// Dans le journal, la nouvelle choisie se lit en entier, même à 80 colonnes.
func TestJournalSelectedInFull(t *testing.T) {
	a, src := newTestApp(t, 80, 24)
	now := t0.Add(5 * time.Second)
	a.plate, a.view = appPoly, viewPlate
	a.key(term.R('d'), now)
	a.key(code(term.Enter), now.Add(confirmArm))
	why := "le port 5000 est déjà pris par python3 (pid 262025) : libère-le ou change le port de l’appli dans son unité"
	a.event(source.Event{Machine: "laptop", Reply: &model.Reply{ID: src.sent[0].ID, OK: false, Error: why}}, now.Add(time.Second))
	a.key(code(term.Tab), now.Add(time.Minute))
	all := strings.Join(rowsOf(t, a, now.Add(time.Minute)), "\n")
	for _, part := range strings.Fields(why) {
		if !strings.Contains(all, part) {
			t.Errorf("journal à 80 colonnes : « %s » manque\n%s", part, all)
		}
	}
}

// Une ligne ne déborde jamais à gauche, même quand le nom de sa machine est
// plus large qu'elle.
func TestLineMachineTagStaysInside(t *testing.T) {
	cv := canvas.New(60, 1)
	cv.Text(0, 0, 20, strings.Repeat("X", 20), ink, 0)
	ln := itemLine{sign: '•', name: seg{s: "site"}, right: []seg{{s: "en marche"}},
		tag: seg{s: " · alfred-thinkpad-x1-carbon-gen11a"}}
	drawLine(cv, 20, 0, 30, ln, false, 0)
	row := cv.Row(0)
	if !strings.HasPrefix(row, strings.Repeat("X", 20)+"•") || !strings.Contains(row, "site") {
		t.Errorf("ligne écrite hors de sa place : %q", row)
	}

	src := &fakeSrc{}
	a := newApp(src, t0)
	a.resize(44, 16)
	l, v := laptopSnap(t0), vpsSnap(t0)
	l.Machine.Name = "alfred-thinkpad-x1-carbon-gen11a"
	a.event(source.Event{Machine: l.Machine.Name, Snapshot: &l}, t0)
	a.event(source.Event{Machine: "vps", Snapshot: &v}, t0)
	a.introOver = true
	typeText(a, "node", t0)
	lines := 0
	for _, r := range rowsOf(t, a, t0) {
		if strings.Contains(r, "· alfred") {
			lines++
			if !strings.Contains(r, "• 5173") {
				t.Errorf("le nom de machine a mangé la ligne : %q", r)
			}
		}
	}
	if lines != 2 {
		t.Errorf("recherche « node » à 44 colonnes : %d lignes du laptop au lieu de 2\n%s", lines, strings.Join(rowsOf(t, a, t0), "\n"))
	}
}

// Des chiffres qu'aucun port ne porte sont cherchés comme un mot.
func TestSearchDigitsFallBackToWords(t *testing.T) {
	a, _ := newTestApp(t, 80, 24)
	l := laptopSnap(t0)
	l.Repos = append(l.Repos, model.Repo{Key: "/home/alfred/p/2025-stage-M2", Path: "/home/alfred/p/2025-stage-M2", Branch: "main", Worktrees: []model.Worktree{}})
	a.event(source.Event{Machine: "laptop", Snapshot: &l}, t0)
	typeText(a, "2025", t0)
	if got := ids(a.results); len(got) != 1 || got[0] != itemID("laptop", kindRepo, "/home/alfred/p/2025-stage-M2") {
		t.Errorf("« 2025 » : %q", got)
	}
	a.key(code(term.Esc), t0)
	typeText(a, "5173", t0)
	if got := ids(a.results); len(got) != 2 {
		t.Errorf("« 5173 » ne donne plus que ses ports : %q", got)
	}
}

// Une machine revenue le dit sous son ciel pendant une minute.
func TestMachineBackShown(t *testing.T) {
	a, _ := newTestApp(t, 80, 24)
	lost := t0.Add(10 * time.Second)
	a.event(statusEv("laptop", false, "ssh : délai dépassé", lost), lost)
	back := lost.Add(20 * time.Second)
	s := laptopSnap(back)
	a.event(source.Event{Machine: "laptop", Snapshot: &s}, back)
	// D'autres nouvelles arrivent : la rangée de nouvelles passe à autre chose.
	for i := 0; i < 3; i++ {
		a.addEntry(entry{at: back, text: "autre chose"})
	}
	if all := strings.Join(rowsOf(t, a, back.Add(30*time.Second)), "\n"); !strings.Contains(all, "◐ de retour depuis 30\u00a0s") {
		t.Errorf("30 s après le retour : absent\n%s", all)
	}
	if all := strings.Join(rowsOf(t, a, back.Add(61*time.Second)), "\n"); strings.Contains(all, "de retour") {
		t.Errorf("61 s après le retour : encore là\n%s", all)
	}
}

// Les flèches de défilement ne collent pas au texte.
func TestScrollArrowsApart(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {160, 45}} {
		a, _ := newTestApp(t, size[0], size[1])
		l := laptopSnap(t0)
		for i := 0; i < 40; i++ {
			l.Repos = append(l.Repos, model.Repo{Key: fmt.Sprintf("/home/alfred/p/r%02d", i), Path: fmt.Sprintf("/home/alfred/p/r%02d", i), Branch: "main", Worktrees: []model.Worktree{}})
		}
		a.event(source.Event{Machine: "laptop", Snapshot: &l}, t0)
		seen := 0
		for _, r := range rowsOf(t, a, t0.Add(5*time.Second)) {
			rs := []rune(r)
			arrow := func(i int) bool { return i >= 0 && i < len(rs) && (rs[i] == '↑' || rs[i] == '↓') }
			for i := range rs {
				// « ↑↓ » est une touche de la marge ; une flèche seule défile.
				if arrow(i) && !arrow(i-1) && !arrow(i+1) {
					seen++
					if i == 0 || rs[i-1] != ' ' {
						t.Errorf("%d × %d : flèche collée : %q", size[0], size[1], r)
					}
				}
			}
		}
		if seen == 0 {
			t.Errorf("%d × %d : pas de flèche de défilement", size[0], size[1])
		}
	}
}

// Entrée sur une nouvelle du journal mène toujours quelque part, ou dit
// pourquoi : le port d'une appli ouvre la planche de l'appli, une nouvelle de
// machine ramène à la planche I sur cette machine.
func TestJournalEnterLeads(t *testing.T) {
	now := t0.Add(5 * time.Second)
	a, _ := newTestApp(t, 120, 36)
	sitePort := model.Listener{Key: "tcp4:0.0.0.0:3000", Port: 3000, Addr: "0.0.0.0", Exposure: "tailnet", Mine: true, UID: 1000,
		PID: 301, Process: "node", CWD: "/home/alfred/p/site", ProjectRoot: "/home/alfred/p/site"}
	a.event(patchEv("vps", now, model.Change{Op: model.OpUpdate, Kind: model.KindListener, Key: sitePort.Key, Data: sitePort}), now)
	if e := lastEntry(t, a); !strings.HasPrefix(e.text, "port 3000") {
		t.Fatalf("nouvelle inattendue : %q", e.text)
	}
	if _, ok := a.fresh[appSite]; !ok {
		t.Errorf("le halo ne va pas à l'appli site : %v", a.fresh)
	}
	a.openJournal()
	a.key(code(term.Enter), now.Add(time.Second))
	if a.view != viewPlate || a.plate != appSite {
		t.Errorf("Entrée sur le port 3000 : vue %v planche %q, attendu la planche de site", a.view, a.plate)
	}

	a.event(statusEv("vps", false, "ssh: connexion perdue", now.Add(2*time.Second)), now.Add(2*time.Second))
	a.focus = 0
	a.openJournal()
	a.key(code(term.Enter), now.Add(3*time.Second))
	if mv := a.focusMachine(); a.view != viewSky || mv == nil || mv.m.Name != "vps" {
		t.Errorf("Entrée sur « vps » perdu : vue %v, attendu la planche I sur vps", a.view)
	}

	a.event(patchEv("vps", now.Add(4*time.Second), model.Change{Op: model.OpRemove, Kind: model.KindListener, Key: sitePort.Key}), now.Add(4*time.Second))
	a.openJournal()
	for i := range a.journal {
		if e := a.journal[len(a.journal)-1-i]; strings.HasPrefix(e.text, "port 3000 :") {
			a.jsel = i // la nouvelle du port 3000 modifié : l'écoute a disparu depuis
		}
	}
	a.key(code(term.Enter), now.Add(5*time.Second))
	all := strings.Join(rowsOf(t, a, now.Add(5*time.Second)), "\n")
	if a.view != viewJournal || !strings.Contains(all, "Cet objet n’est plus au relevé.") {
		t.Errorf("Entrée sur un objet disparu : ni message ni vue attendue\n%s", all)
	}
}

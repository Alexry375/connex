package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
)

// fakeSource est une source pilotée par le test.
type fakeSource struct {
	ch   chan source.Event
	sent []sentReq
	err  error
}

type sentReq struct {
	machine string
	req     model.Request
}

func newFake() *fakeSource { return &fakeSource{ch: make(chan source.Event, 64)} }

func (f *fakeSource) Events() <-chan source.Event { return f.ch }
func (f *fakeSource) Send(machine string, req model.Request) error {
	f.sent = append(f.sent, sentReq{machine, req})
	return f.err
}
func (f *fakeSource) Close() error { return nil }

var t0 = time.Date(2026, 9, 27, 21, 0, 0, 0, time.UTC)

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

// snap construit un relevé d'essai pour la machine name.
func snap(name string, fill func(*model.Snapshot)) source.Event {
	s := model.NewSnapshot(model.Machine{Name: name, OS: "linux", Arch: "amd64", User: "alfred"}, t0)
	if fill != nil {
		fill(&s)
	}
	return source.Event{Machine: name, Snapshot: &s}
}

func patch(name string, changes ...model.Change) source.Event {
	p := model.NewPatch(name, t0, changes)
	return source.Event{Machine: name, Patch: &p}
}

func status(name string, ok bool, reason string) source.Event {
	st := model.MachineStatus{Schema: 1, Type: "machine_status", Machine: name, Reachable: ok, Reason: reason}
	return source.Event{Machine: name, Status: &st}
}

func siteListener() model.Listener {
	return model.Listener{Key: "tcp6:[::]:3000", Port: 3000, Addr: "::", Exposure: "all", Mine: true, UID: 1000,
		PID: 4242, Process: "next-server", CWD: "/home/alfred/Global/Projets/site", ProjectRoot: "/home/alfred/Global/Projets/site"}
}

func lastText(w *world) string {
	if len(w.chron) == 0 {
		return ""
	}
	return w.chron[len(w.chron)-1].text
}

func TestWorldTellsOutageAndReturn(t *testing.T) {
	w := newWorld()
	w.apply(snap("vps", nil), t0)
	w.apply(status("vps", false, "Connection timed out during banner exchange"), t0.Add(10*time.Second))
	if got := lastText(w); got != "vps injoignable — Connection timed out during banner exchange" {
		t.Errorf("coupure : %q", got)
	}
	if w.chron[len(w.chron)-1].tone != toneAlert {
		t.Errorf("une coupure doit être une alerte")
	}
	if w.fleet.Machine("vps").Reachable {
		t.Fatal("la machine devrait être injoignable")
	}
	w.apply(status("vps", true, ""), t0.Add(82*time.Second))
	if got := lastText(w); got != "vps de nouveau joignable, après 1 min 12 s" {
		t.Errorf("retour : %q", got)
	}
	if _, ok := w.reason["vps"]; ok {
		t.Errorf("la cause de la coupure doit s'effacer au retour")
	}
}

func TestWorldKeepsGhostsThenForgetsThem(t *testing.T) {
	w := newWorld()
	l := siteListener()
	w.apply(snap("laptop", func(s *model.Snapshot) { s.Listeners = append(s.Listeners, l) }), t0)
	w.apply(patch("laptop", model.Change{Op: model.OpRemove, Kind: model.KindListener, Key: l.Key}), t0.Add(time.Second))
	if got := lastText(w); got != "port 3000 fermé — next-server · site" {
		t.Errorf("fermeture : %q", got)
	}
	es := buildEntries(w)
	if len(es) != 1 || !es[0].ghost || es[0].port.Port != 3000 {
		t.Fatalf("le port fermé doit rester, grisé : %+v", es)
	}
	w.prune(t0.Add(time.Second + ghostTTL + time.Second))
	if es := buildEntries(w); len(es) != 0 {
		t.Errorf("le fantôme aurait dû partir : %d entrées", len(es))
	}
}

func TestDescribeChanges(t *testing.T) {
	home := "/home/alfred"
	repo := model.Repo{Key: "/r/site", Path: "/home/alfred/Global/Projets/site", Branch: "main", Dirty: no()}
	dirty := repo
	dirty.Dirty = yes()
	wt := repo
	wt.Worktrees = []model.Worktree{{Path: "/home/alfred/Global/Projets/site-x", Branch: "feat/x"}}
	agent := model.Agent{Key: "claude:1", Tool: "claude", PID: 1, CWD: "/home/alfred/Global/Projets/site", State: model.StateWorking}
	waiting := agent
	waiting.State = model.StateWaiting
	app := model.App{Key: "site", Name: "site", State: model.AppRunning, Health: model.AppHealth{HTTP: 200}}
	failed := app
	failed.State = model.AppFailed
	sick := app
	sick.Health.HTTP = 502
	l := siteListener()
	l2 := l
	l2.PID = 4410
	cases := []struct {
		op, kind  string
		old, cur  any
		want      string
		wantTone  tone
		wantMatch bool
	}{
		{model.OpUpdate, model.KindRepo, repo, dirty, "dépôt site modifié", toneWarn, false},
		{model.OpUpdate, model.KindRepo, dirty, repo, "dépôt site de nouveau propre", toneNeutral, false},
		{model.OpUpdate, model.KindRepo, repo, wt, "dépôt site : worktree feat/x ouvert", toneNeutral, false},
		{model.OpUpdate, model.KindAgent, agent, waiting, "claude t'attend dans site", toneCall, false},
		{model.OpAdd, model.KindAgent, nil, agent, "claude démarre dans site", toneNeutral, false},
		{model.OpAdd, model.KindAgent, nil, model.Agent{Key: "codex:2", Tool: "codex", PID: 2}, "codex démarre", toneNeutral, false},
		{model.OpUpdate, model.KindAgent, agent, model.Agent{Key: "claude:1", Tool: "claude", PID: 1, State: model.StateWorking}, "claude change de dossier", toneNeutral, false},
		{model.OpUpdate, model.KindApp, app, failed, "site en échec", toneAlert, false},
		{model.OpUpdate, model.KindApp, app, sick, "site répond 502", toneWarn, false},
		{model.OpUpdate, model.KindApp, sick, app, "site répond de nouveau (200)", toneGood, false},
		{model.OpAdd, model.KindListener, nil, l, "port 3000 ouvert — next-server · site · ouvert à tous", toneOpen, false},
		{model.OpUpdate, model.KindListener, l, l2, "port 3000 : next-server relancé (pid 4242 → 4410)", toneNeutral, false},
		{model.OpAdd, model.KindListener, nil, model.Listener{Port: 5432, Exposure: "loopback", UID: 113}, "port 5432 ouvert — autre compte (uid 113)", toneNeutral, false},
	}
	for _, c := range cases {
		got, tn := describe(c.op, c.kind, c.old, c.cur, home)
		if got != c.want || tn != c.wantTone {
			t.Errorf("describe(%s %s) = %q (%d), attendu %q (%d)", c.op, c.kind, got, tn, c.want, c.wantTone)
		}
	}
}

func TestSendThenReply(t *testing.T) {
	w := newWorld()
	src := newFake()
	item := itemID{"laptop", model.KindApp, "site"}
	w.send(src, "laptop", model.ReqStop, "site", item, t0)
	if len(src.sent) != 1 || src.sent[0].req.Op != model.ReqStop || src.sent[0].req.Target != "site" || src.sent[0].machine != "laptop" {
		t.Fatalf("requête envoyée : %+v", src.sent)
	}
	if w.pendingFor(item) == nil {
		t.Fatal("la requête doit être en cours")
	}
	id := src.sent[0].req.ID
	w.apply(source.Event{Machine: "laptop", Reply: &model.Reply{Schema: 1, Type: "reply", ID: id, OK: true}}, t0.Add(600*time.Millisecond))
	if w.pendingFor(item) != nil {
		t.Error("la requête devrait être finie")
	}
	if o := w.results[item]; !o.ok {
		t.Errorf("résultat : %+v", o)
	}
	if got := lastText(w); got != "arrêter site — fait" {
		t.Errorf("chronique : %q", got)
	}

	w.send(src, "laptop", model.ReqStart, "planetaire", itemID{"laptop", model.KindApp, "planetaire"}, t0)
	id = src.sent[1].req.ID
	w.apply(source.Event{Machine: "laptop", Reply: &model.Reply{ID: id, OK: false, Error: "le port 5000 est déjà pris par python3 (pid 181234)"}}, t0.Add(2*time.Second))
	if got := lastText(w); got != "démarrer planetaire — refusé : le port 5000 est déjà pris par python3 (pid 181234)" {
		t.Errorf("refus : %q", got)
	}
	// une réponse inconnue ne change rien
	n := len(w.chron)
	w.apply(source.Event{Machine: "laptop", Reply: &model.Reply{ID: "autre", OK: true}}, t0)
	if len(w.chron) != n {
		t.Error("une réponse à une requête inconnue ne doit rien écrire")
	}
}

func TestSendErrorIsARefusal(t *testing.T) {
	w := newWorld()
	src := newFake()
	src.err = errors.New("trop de requêtes en attente")
	item := itemID{"laptop", model.KindListener, "tcp4:0.0.0.0:5432"}
	w.send(src, "laptop", model.ReqFreePort, "5432", item, t0)
	if w.pendingFor(item) != nil {
		t.Error("aucune réponse ne viendra : rien ne doit rester en cours")
	}
	o, ok := w.results[item]
	if !ok || o.ok || o.err != "trop de requêtes en attente" {
		t.Errorf("refus immédiat : %+v", o)
	}
	if !strings.Contains(lastText(w), "libérer le port 5432 — refusé") {
		t.Errorf("chronique : %q", lastText(w))
	}
}

func TestWorldTracksWaitingSince(t *testing.T) {
	w := newWorld()
	a := model.Agent{Key: "claude:7", Tool: "claude", PID: 7, CWD: "/home/alfred/x", State: model.StateWorking}
	w.apply(snap("laptop", func(s *model.Snapshot) { s.Agents = append(s.Agents, a) }), t0)
	id := itemID{"laptop", model.KindAgent, a.Key}
	if _, ok := w.since[id]; ok {
		t.Error("un agent présent à l'ouverture n'a pas de date de changement connue")
	}
	a.State = model.StateWaiting
	w.apply(patch("laptop", model.Change{Op: model.OpUpdate, Kind: model.KindAgent, Key: a.Key, Data: a}), t0.Add(5*time.Second))
	if got := w.since[id]; !got.Equal(t0.Add(5 * time.Second)) {
		t.Errorf("attente vue à %v", got)
	}
}

// Le simulateur réel, monde figé : l'interface doit y trouver les cas du §8.
func TestWorldOnDemoSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := source.NewDemo(ctx, source.DemoOptions{Seed: 7, Speed: 0})
	w := newWorld()
	deadline := time.After(3 * time.Second)
	for len(w.fleet.Machines()) < 3 {
		select {
		case ev := <-src.Events():
			w.apply(ev, t0)
		case <-deadline:
			t.Fatal("le simulateur n'a pas envoyé ses trois relevés")
		}
	}
	es := buildEntries(w)
	counts := map[stratum]int{}
	for _, e := range es {
		counts[e.stratum]++
	}
	if counts[stApps] == 0 || counts[stOpen] == 0 || counts[stPorts] == 0 || counts[stRepos] == 0 {
		t.Errorf("marches vides : %v", counts)
	}
	for i := 1; i < len(es); i++ {
		if es[i].stratum < es[i-1].stratum {
			t.Fatalf("échelle mal rangée à la ligne %d", i)
		}
	}
	// deux programmes sur le même port : la recherche les rend tous les deux
	res := filterEntries(es, "", "5173")
	ports := 0
	for _, e := range res {
		if e.port != nil && e.port.Port == 5173 {
			ports++
		}
	}
	if ports < 2 {
		t.Errorf("recherche 5173 : %d ports, attendu au moins 2", ports)
	}
}

func TestSearch(t *testing.T) {
	w := newWorld()
	w.apply(snap("laptop", func(s *model.Snapshot) {
		s.Listeners = append(s.Listeners, siteListener(),
			model.Listener{Key: "tcp4:127.0.0.1:30001", Port: 30001, Addr: "127.0.0.1", Exposure: "loopback", Mine: true, PID: 9, Process: "node", CWD: "/home/alfred/b"},
			model.Listener{Key: "tcp4:0.0.0.0:5432", Port: 5432, Addr: "0.0.0.0", Exposure: "all", UID: 0})
		s.Repos = append(s.Repos, model.Repo{Key: "/home/alfred/Global/Projets/site", Path: "/home/alfred/Global/Projets/site", Branch: "main", Dirty: yes()})
		s.Apps = append(s.Apps, model.App{Key: "site", Name: "site", Port: 3000, State: model.AppRunning})
	}), t0)
	w.apply(snap("vps", func(s *model.Snapshot) {
		s.Repos = append(s.Repos, model.Repo{Key: "/home/alfred/apps/site", Path: "/home/alfred/apps/site", Branch: "main", Dirty: no()})
	}), t0)
	all := buildEntries(w)
	cases := []struct {
		q, machine string
		want       int
	}{
		{"", "", 6},
		{"site", "", 4},    // port, appli, deux dépôts
		{"site", "vps", 1}, // la machine choisie filtre
		{"3000", "", 3},    // le port, l'appli qui le réserve, et 30001 qui commence pareil
		{"300", "", 3},     // début de numéro : 3000 et 30001
		{"MODIFIE", "", 1}, // sans accent ni casse
		{"autre compte", "", 1},
		{"site vps", "", 1}, // chaque mot doit y être
		{"ouvert a tous", "", 2},
		{"introuvable", "", 0},
	}
	for _, c := range cases {
		if got := len(filterEntries(all, c.machine, c.q)); got != c.want {
			t.Errorf("recherche %q sur %q : %d résultats, attendu %d", c.q, c.machine, got, c.want)
		}
	}
	res := filterEntries(all, "", "300")
	var first *entry
	for i := range res {
		if res[i].port != nil {
			first = &res[i]
			break
		}
	}
	if first == nil || first.port.Port != 3000 {
		t.Errorf("le port exact doit passer devant")
	}
}

func TestFitPath(t *testing.T) {
	long := "~/Global/Archives/2025-stage-M2-analyse-comparative-des-outils-de-supervision-multi-machines"
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"~/Global/Projets/site", 40, "~/Global/Projets/site"},
		{"~/Global/Projets/site", 16, "~/…/Projets/site"},
		{"~/Global/Projets/site", 10, "~/…/site"},
		{"/home/abc/domotique", 12, "/…/domotique"},
	}
	for _, c := range cases {
		if got := fitPath(c.in, c.w); got != c.want {
			t.Errorf("fitPath(%q, %d) = %q, attendu %q", c.in, c.w, got, c.want)
		}
	}
	for _, w := range []int{8, 20, 30, 60} {
		if got := fitPath(long, w); len([]rune(got)) > w {
			t.Errorf("fitPath(long, %d) déborde : %q", w, got)
		}
	}
	if got := projectName("/home/alfred/Global/Agents_Projects/connex-lab/repo"); got != "connex-lab/repo" {
		t.Errorf("projectName générique : %q", got)
	}
}

func TestWords(t *testing.T) {
	cases := map[time.Duration]string{
		0:                           "à l'instant",
		12 * time.Second:            "il y a 12 s",
		72 * time.Second:            "il y a 1 min 12 s",
		3 * time.Minute:             "il y a 3 min",
		25 * time.Minute:            "il y a 25 min",
		2*time.Hour + 5*time.Minute: "il y a 2 h 05",
		50 * time.Hour:              "il y a 2 j",
	}
	for d, want := range cases {
		if got := ago(d); got != want {
			t.Errorf("ago(%v) = %q, attendu %q", d, got, want)
		}
	}
	if got := seconds(1400 * time.Millisecond); got != "1,4 s" {
		t.Errorf("seconds : %q", got)
	}
	if got := fold("Arrêtée Échec"); got != "arretee echec" {
		t.Errorf("fold : %q", got)
	}
}

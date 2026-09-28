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

const longRepo = "/home/alfred/Global/Agents_Projects/un-projet-au-nom-vraiment-tres-long/sous-dossier/encore-un/depot-final"

// fleetEvents : trois machines ; sur vps un agent qui t'attend ; sur laptop un
// agent qui travaille, une appli qui tourne et une arrêtée, deux programmes
// sur le même port, un port tenu par un autre compte et un dépôt au chemin
// très long ; desk est vide.
func fleetEvents(waiting bool) []source.Event {
	st := model.StateWaiting
	if !waiting {
		st = model.StateIdle
	}
	return []source.Event{
		snap("laptop", func(s *model.Snapshot) {
			s.Agents = append(s.Agents, model.Agent{Key: "claude:100", Tool: "claude", PID: 100,
				CWD: "/home/alfred/Global/Projets/site", State: model.StateWorking})
			s.Apps = append(s.Apps,
				model.App{Key: "site", Name: "site", Project: "/home/alfred/Global/Projets/site",
					CWD: "/home/alfred/Global/Projets/site", Port: 3000, Unit: "connex-site.service",
					State: model.AppRunning, View: model.ViewRemote},
				model.App{Key: "planetaire", Name: "planetaire", Port: 5000, Unit: "connex-planetaire.service",
					State: model.AppStopped, View: model.ViewRemote})
			s.Listeners = append(s.Listeners, siteListener(),
				model.Listener{Key: "tcp4:127.0.0.1:5173", Port: 5173, Addr: "127.0.0.1", Exposure: "loopback",
					Mine: true, UID: 1000, PID: 11, Process: "node", CWD: "/home/alfred/Global/Projets/nuage"},
				model.Listener{Key: "tcp6:[::1]:5173", Port: 5173, Addr: "::1", Exposure: "loopback",
					Mine: true, UID: 1000, PID: 12, Process: "node", CWD: "/home/alfred/Global/Projets/Clepsydre"},
				model.Listener{Key: "tcp4:0.0.0.0:5432", Port: 5432, Addr: "0.0.0.0", Exposure: "all", UID: 0})
			s.Repos = append(s.Repos, model.Repo{Key: longRepo, Path: longRepo, Branch: "main", Dirty: yes()})
		}),
		snap("vps", func(s *model.Snapshot) {
			s.Agents = append(s.Agents, model.Agent{Key: "claude:200", Tool: "claude", PID: 200,
				CWD: "/home/alfred/apps/site", State: st})
		}),
		snap("desk", nil),
	}
}

// newTestApp monte l'interface sur une source d'essai, ouverture passée.
func newTestApp(w, h int, evs ...source.Event) (*app, *fakeSource) {
	src := newFake()
	a := newApp(src, t0)
	a.W, a.H = w, h
	for _, ev := range evs {
		a.event(ev, t0)
	}
	a.tick(t0.Add(2 * time.Second))
	a.sync()
	return a, src
}

// look dessine l'interface et la rend en lignes de texte.
func look(a *app) []string {
	c := canvas.New(a.W, a.H)
	a.draw(c)
	out := make([]string, c.H)
	for y := 0; y < c.H; y++ {
		var b strings.Builder
		for x := 0; x < c.W; x++ {
			switch r := c.Cell(x, y).R; r {
			case 0:
			case '▀':
				b.WriteRune(' ')
			default:
				b.WriteRune(r)
			}
		}
		out[y] = b.String()
	}
	return out
}

func contains(lines []string, s string) bool {
	for _, l := range lines {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

func rn(r rune) term.Key      { return term.Key{Code: term.KeyRune, Rune: r} }
func kc(c term.Code) term.Key { return term.Key{Code: c} }

func typeKeys(a *app, s string) {
	for _, r := range s {
		a.key(rn(r))
	}
}

// selectWhere choisit la première entrée qui vérifie ok.
func selectWhere(t *testing.T, a *app, ok func(e *entry) bool) {
	t.Helper()
	a.sync()
	for i := range a.view {
		if ok(&a.view[i]) {
			a.choose(i)
			return
		}
	}
	t.Fatal("aucune entrée ne convient")
}

func isApp(name string) func(e *entry) bool {
	return func(e *entry) bool { return e.app != nil && e.app.Name == name }
}

func isPort(port int) func(e *entry) bool {
	return func(e *entry) bool { return e.port != nil && e.port.Port == port }
}

// Parcours 1, à 80 × 24 : d'un coup d'œil, qui t'attend et sur quelle machine.
func TestLayoutAt80x24TellsWhoWaits(t *testing.T) {
	a, _ := newTestApp(80, 24, fleetEvents(true)...)
	s := look(a)
	top := s[:5]
	if !contains(top, "un agent t'attend") || !contains(top, "vps") {
		t.Errorf("le haut de l'écran doit dire qui attend et où :\n%s", strings.Join(top, "\n"))
	}
	for _, m := range []string{"laptop", "vps", "desk"} {
		if !contains(top, m) {
			t.Errorf("machine %s absente du haut de l'écran", m)
		}
	}
	if !contains(s, "claude t'attend") {
		t.Error("l'agent qui attend doit ouvrir l'échelle")
	}
	if !strings.Contains(s[23], "q quitter") {
		t.Errorf("ligne des touches : %q", s[23])
	}
	for y, l := range s {
		if w := canvas.Width(l); w != 80 {
			t.Errorf("ligne %d : %d colonnes", y, w)
		}
	}
}

// Moins de place : l'essentiel reste, rien ne casse.
func TestLayoutNarrowAndTiny(t *testing.T) {
	a, _ := newTestApp(60, 18, fleetEvents(true)...)
	s := look(a)
	if !contains(s[:2], "t'attend") || !contains(s[:2], "vps") {
		t.Errorf("à 60 × 18 :\n%s", strings.Join(s, "\n"))
	}
	for _, sz := range [][2]int{{30, 7}, {10, 3}, {1, 1}, {0, 0}} {
		a.W, a.H = sz[0], sz[1]
		look(a) // ne doit pas paniquer
	}
}

// À 160 × 45, la place sert : chemins entiers, chronique à côté, astres peints.
func TestLayoutAt160x45UsesTheSpace(t *testing.T) {
	a, _ := newTestApp(160, 45, fleetEvents(true)...)
	s := look(a)
	if !contains(s, "ce qui vient de changer") {
		t.Error("la chronique doit tenir à côté de l'échelle")
	}
	if !contains(s, "~/Global/Projets/site") {
		t.Error("les chemins doivent s'écrire en entier")
	}
	c := canvas.New(160, 45)
	a.draw(c)
	pixels := 0
	for y := 0; y < 10; y++ {
		for x := 100; x < 160; x++ {
			if c.Cell(x, y).R == '▀' {
				pixels++
			}
		}
	}
	if pixels < 100 {
		t.Errorf("les astres doivent être peints : %d pixels dans le volet droit", pixels)
	}
}

func TestNoAgent(t *testing.T) {
	a, _ := newTestApp(80, 24, snap("laptop", nil), snap("vps", nil))
	s := look(a)
	if !contains(s[:5], "aucun agent ne t'attend") || !contains(s[:5], "aucune session en cours") {
		t.Errorf("sans agent :\n%s", strings.Join(s[:5], "\n"))
	}
	if !contains(s, "rien à montrer sur ces machines") {
		t.Errorf("échelle vide :\n%s", strings.Join(s, "\n"))
	}
	a.W, a.H = 160, 45
	if s := look(a); !contains(s[:8], "aucune session d'agent en cours") {
		t.Errorf("sans agent, à 160 × 45 :\n%s", strings.Join(s[:8], "\n"))
	}
}

// Plus d'agents qui t'attendent que le volet n'en tient : il dit où sont les
// autres, et c'est vrai.
func TestManyWaitingPointToTheLadder(t *testing.T) {
	a, _ := newTestApp(80, 24, snap("vps", func(s *model.Snapshot) {
		for i, p := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"} {
			s.Agents = append(s.Agents, model.Agent{Key: "claude:" + p, Tool: "claude", PID: 300 + i,
				CWD: "/home/alfred/apps/" + p, State: model.StateWaiting})
		}
		s.Agents = append(s.Agents, model.Agent{Key: "claude:omega", Tool: "claude", PID: 400,
			CWD: "/home/alfred/apps/omega", State: model.StateWorking})
	}))
	s := look(a)
	if !contains(s[:6], "6 agents t'attendent") || !contains(s[:6], "et 5 autres, plus bas") {
		t.Fatalf("le volet doit dire où sont les autres :\n%s", strings.Join(s[:6], "\n"))
	}
	seen := 0
	for _, e := range a.view {
		if e.agent == nil {
			continue
		}
		if seen < 6 && e.agent.State != model.StateWaiting {
			t.Fatalf("l'agent %s passe avant un agent qui t'attend", e.agent.Key)
		}
		seen++
	}
	if seen != 7 {
		t.Errorf("%d agents dans l'échelle, 7 attendus", seen)
	}
	// une recherche en cours peut les cacher : le volet ne dit plus où
	typeKeys(a, "/omega")
	if s := look(a); !contains(s[:6], "et 5 autres") || contains(s[:6], "plus bas") {
		t.Errorf("recherche en cours :\n%s", strings.Join(s[:6], "\n"))
	}
}

// Sur une seule machine (connex sans --demo), son nom s'écrit une fois, en
// haut à droite : ni en bout de ligne, ni devant chaque changement. Une issue
// courte reste lisible derrière ce long nom, et le dossier personnel se dit
// « ~ ».
func TestOneMachineSaysItsNameOnce(t *testing.T) {
	const name = "alfred-thinkpad-x1-carbon-gen11a"
	solo := snap(name, func(s *model.Snapshot) {
		s.Agents = append(s.Agents, model.Agent{Key: "claude:100", Tool: "claude", PID: 100,
			CWD: "/home/alfred", State: model.StateWorking})
		s.Listeners = append(s.Listeners, siteListener(),
			model.Listener{Key: "tcp4:127.0.0.1:1080", Port: 1080, Addr: "127.0.0.1", Exposure: "loopback",
				Mine: true, UID: 1000, PID: 7, Process: "ssh", CWD: "/home/alfred"})
	})
	for _, size := range [][2]int{{120, 36}, {80, 24}} {
		a, src := newTestApp(size[0], size[1], solo)
		a.event(patch(name, model.Change{Op: model.OpRemove, Kind: model.KindListener, Key: siteListener().Key}), t0.Add(3*time.Second))
		a.key(kc(term.KeyDown))
		a.tick(t0.Add(4 * time.Second))
		s := look(a)
		if !contains(s, "ssh · ~") || contains(s, "ssh · alfred") || !contains(s, "claude travaille · ~") {
			t.Errorf("%v : le dossier personnel doit s'écrire « ~ » :\n%s", size, strings.Join(s, "\n"))
		}
		named := 0
		for _, l := range s {
			if strings.Contains(l, name[:15]) {
				named++
			}
		}
		if !contains(s, "port 3000 fermé") || named != 1 {
			t.Errorf("%v : le nom de la machine s'écrit %d fois, attendu 1 :\n%s", size, named, strings.Join(s, "\n"))
		}
		typeKeys(a, "R")
		if len(src.sent) != 1 || src.sent[0].req.Op != model.ReqRefresh {
			t.Fatalf("%v : relevé demandé : %+v", size, src.sent)
		}
		a.event(source.Event{Machine: name, Reply: &model.Reply{Schema: 1, Type: "reply", ID: src.sent[0].req.ID, OK: true}}, t0.Add(5*time.Second))
		a.tick(t0.Add(5*time.Second + 100*time.Millisecond))
		if s := look(a); !contains(s, "— fait") {
			t.Errorf("%v : l'issue du relevé doit rester lisible :\n%s", size, strings.Join(s, "\n"))
		}
	}
}

func TestSqueezeKeepsTheCause(t *testing.T) {
	for _, c := range []struct {
		s    string
		w    int
		want string
	}{
		{"court", 10, "court"},
		{"ssh: connect to host desk port 22: Connection timed out", 22, "…Connection timed out"},
		{"desk injoignable — ssh: connect to host desk port 22: Connection timed out", 45, "desk injoignable — …Connection timed out"},
		{"desk injoignable — client_loop: send disconnect: Broken pipe", 50, "desk injoignable — …send disconnect: Broken pipe"},
		{"Connection timed out during banner exchange", 20, "Connection timed ou…"},
		// une phrase de connex, en français, garde son début
		{"libérer le port 22 — refusé : le port 22 est tenu par un autre compte (uid 0) : connex ne peut pas le libérer", 60,
			"libérer le port 22 — refusé : le port 22 est tenu par un au…"},
		// une issue courte derrière un long nom de machine : c'est le nom qui cède
		{"rafraîchir alfred-thinkpad-x1-carbon-gen11a — fait", 40, "rafraîchir alfred-thinkpad-x1-ca… — fait"},
		{"rafraîchir alfred-thinkpad-x1-carbon-gen11a — refusé : machine injoignable", 62,
			"rafraîchir alfred-thinkpad-x1-… — refusé : machine injoignable"},
		// mais un début court garde ce qui est arrivé
		{"port 6006 fermé — node · ui-kit", 30, "port 6006 fermé — node · ui-k…"},
	} {
		if got := squeeze(c.s, c.w); got != c.want || canvas.Width(got) > c.w {
			t.Errorf("squeeze(%q, %d) = %q, attendu %q", c.s, c.w, got, c.want)
		}
	}
	ls := wrapSqueezed("abc-desktop injoignable — ssh: connect to host 100.88.3.21 port 22: Connection timed out", 34, 2)
	if len(ls) != 2 || !strings.HasSuffix(ls[1], "Connection timed out") {
		t.Errorf("sur deux lignes, la raison doit rester : %q", ls)
	}
}

// within fait échouer tout le paquet si f ne rend pas la main à temps : une
// boucle sans fin doit se voir, pas figer la suite des tests.
func within(d time.Duration, what string, f func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(d):
		panic(what + " ne rend pas la main après " + d.String())
	}
}

// Une ligne de cartouche se coupe toujours, même sur un mot plus long que la
// ligne ou des caractères larges : rien ne se perd, rien ne déborde.
func TestWrapLineAlwaysEnds(t *testing.T) {
	long := strings.Repeat("archive-stage-", 5) + "fin" // 73 caractères sans espace ni barre
	for _, text := range []string{
		"dépôt " + long + " — modifié",
		"dossier  /home/alfred/" + long + "/sous-dossier",
		strings.Repeat("端末", 40),
		"agent 端末端末端末 dans /srv/" + strings.Repeat("é", 90),
	} {
		ln := cline{{s: "  "}, {s: text, bold: true}}
		for w := 0; w <= 100; w++ {
			var rows []cline
			within(2*time.Second, fmt.Sprintf("wrapLine(%q, %d)", text, w), func() { rows = wrapLine(ln, w) })
			var all strings.Builder
			for _, r := range rows {
				n := 0
				for _, s := range r {
					n += canvas.Width(s.s)
					all.WriteString(s.s)
				}
				if w >= 4 && n > w {
					t.Errorf("wrapLine(%q, %d) : une ligne de %d colonnes", text, w, n)
				}
			}
			if got, want := strings.ReplaceAll(all.String(), " ", ""), strings.ReplaceAll(text, " ", ""); got != want {
				t.Errorf("wrapLine(%q, %d) perd du texte : %q", text, w, all.String())
			}
		}
	}
	wide := strings.Repeat("端末", 20)
	for w := 2; w <= 30; w++ {
		if got := truncMiddle(wide, w); canvas.Width(got) > w {
			t.Errorf("truncMiddle(%d) déborde : %q", w, got)
		}
		for _, l := range wrap(wide, w, 50) {
			if canvas.Width(l) > w {
				t.Errorf("wrap(%d) déborde : %q", w, l)
			}
		}
	}
}

// Un nom sans coupure possible, plus long que la ligne, se lit dans le
// cartouche à toutes les largeurs, sans figer l'interface.
func TestLongWordsInDetail(t *testing.T) {
	const host = "alfred-thinkpad-x1-carbon-gen11a"
	stage := "/home/alfred/Global/Archives/" + strings.Repeat("archive-stage-", 5) + "fin"
	evs := []source.Event{snap(host, func(s *model.Snapshot) {
		s.Agents = append(s.Agents, model.Agent{Key: "claude:100", Tool: "claude", PID: 100, CWD: stage, State: model.StateWaiting})
		s.Listeners = append(s.Listeners, model.Listener{Key: "tcp4:127.0.0.1:1080", Port: 1080, Addr: "127.0.0.1",
			Exposure: "loopback", Mine: true, UID: 1000, PID: 7, Process: "ssh", CWD: stage})
		s.Repos = append(s.Repos, model.Repo{Key: stage, Path: stage, Branch: "main", Dirty: yes()})
	}), snap("vps", nil)}
	for w := 20; w <= 120; w++ {
		for _, h := range []int{18, 24, 36} {
			a, _ := newTestApp(w, h, evs...)
			a.sync()
			for i := range a.view {
				a.choose(i)
				a.key(kc(term.KeyEnter))
				var s []string
				within(2*time.Second, fmt.Sprintf("le détail n° %d à %d × %d", i, w, h), func() { s = look(a) })
				if w == 80 && h == 24 && !contains(s, "archive-stage-archive") {
					t.Errorf("le long nom doit se lire dans le détail :\n%s", strings.Join(s, "\n"))
				}
				a.key(kc(term.KeyEsc))
			}
		}
	}
}

// Des noms en caractères larges (dossier, programme, machine, appli) ne
// cassent rien, à aucune taille, dans aucun mode.
func TestWideNamesNeverBreak(t *testing.T) {
	wide := "/home/alfred/プロジェクト/" + strings.Repeat("端末", 30)
	evs := []source.Event{snap("端末機械", func(s *model.Snapshot) {
		s.Agents = append(s.Agents, model.Agent{Key: "claude:1", Tool: "claude", PID: 1, CWD: wide, State: model.StateWaiting})
		s.Listeners = append(s.Listeners, model.Listener{Key: "tcp4:127.0.0.1:1080", Port: 1080, Addr: "127.0.0.1",
			Exposure: "loopback", Mine: true, UID: 1000, PID: 7, Process: "端末", CWD: wide})
		s.Repos = append(s.Repos, model.Repo{Key: wide, Path: wide, Branch: "端末", Dirty: yes()})
		s.Apps = append(s.Apps, model.App{Key: "端末", Name: "端末アプリ", Port: 1080, CWD: wide, Project: wide, State: model.AppRunning})
	}), snap("vps", nil)}
	for w := 1; w <= 130; w += 3 {
		for h := 1; h <= 46; h += 3 {
			a, _ := newTestApp(w, h, evs...)
			a.sync()
			for i := range a.view {
				a.choose(i)
				for _, k := range []term.Key{kc(term.KeyEnter), rn('l'), rn('?'), rn('c')} {
					a.key(k)
					within(2*time.Second, fmt.Sprintf("%d × %d, entrée %d, touche %q", w, h, i, k.Rune), func() { look(a) })
					a.key(kc(term.KeyEsc))
				}
			}
		}
	}
}

// Sur une échelle étroite, un long nom de machine cède la place : le port et
// son programme restent lisibles, et aucun séparateur ne finit seul une ligne.
func TestNarrowRowsKeepTheirSense(t *testing.T) {
	const host = "alfred-thinkpad-x1-carbon-gen11a"
	evs := []source.Event{snap(host, func(s *model.Snapshot) {
		s.Agents = append(s.Agents, model.Agent{Key: "claude:100", Tool: "claude", PID: 100,
			CWD: "/home/alfred/Global/Projets/connex-lab", State: model.StateWorking})
		s.Listeners = append(s.Listeners, model.Listener{Key: "tcp4:127.0.0.1:1080", Port: 1080, Addr: "127.0.0.1",
			Exposure: "loopback", Mine: true, UID: 1000, PID: 7, Process: "ssh", CWD: "/home/alfred"})
	}), snap("vps", nil)}
	for w := 36; w <= 100; w++ { // en dessous, connex demande plus de place
		a, _ := newTestApp(w, 24, evs...)
		s := look(a)
		if !contains(s, "1080 ssh") || !contains(s, "claude travaille") || !contains(s, host[:9]) {
			t.Errorf("à %d colonnes, la ligne doit dire ce qu'elle montre :\n%s", w, strings.Join(s, "\n"))
		}
		for _, l := range s {
			if strings.Contains(l+"  ", " ·  ") {
				t.Errorf("à %d colonnes, un séparateur seul : %q", w, l)
			}
		}
	}
}

// Ce qui vient d'arriver se dit « à l'instant », jamais « 0 s ».
func TestJustNow(t *testing.T) {
	a, _ := newTestApp(120, 36, fleetEvents(true)...)
	at := t0.Add(10 * time.Second)
	a.event(status("desk", false, "Connection closed by remote host"), at)
	a.tick(at.Add(200 * time.Millisecond))
	s := look(a)
	if !contains(s[:8], "injoignable à l'instant") || contains(s, " 0 s") {
		t.Errorf("coupure toute fraîche :\n%s", strings.Join(s[:8], "\n"))
	}
	a.event(status("desk", true, ""), at.Add(5*time.Second))
	a.tick(at.Add(5*time.Second + 300*time.Millisecond))
	if s := look(a); !contains(s[:8], "revenue à l'instant") || contains(s, " 0 s") {
		t.Errorf("retour tout frais :\n%s", strings.Join(s[:8], "\n"))
	}
	now := at.Add(9 * time.Second)
	a.event(patch("laptop", model.Change{Op: model.OpUpdate, Kind: model.KindAgent, Key: "claude:100",
		Data: model.Agent{Key: "claude:100", Tool: "claude", PID: 100, CWD: "/home/alfred/Global/Projets/site", State: model.StateWaiting}}), now)
	a.tick(now.Add(100 * time.Millisecond))
	if s := look(a); !contains(s[:8], "laptop · à l'instant") || contains(s, " 0 s") {
		t.Errorf("agent qui se met à attendre :\n%s", strings.Join(s, "\n"))
	}
	a.tick(now.Add(3 * time.Second))
	if s := look(a); !contains(s[:8], "laptop · depuis 3 s") {
		t.Errorf("agent qui attend depuis 3 s :\n%s", strings.Join(s[:8], "\n"))
	}
}

// La sélection suit l'élément choisi quand d'autres lignes arrivent avant lui.
func TestSelectionFollowsItsItem(t *testing.T) {
	a, _ := newTestApp(80, 24, fleetEvents(true)...)
	selectWhere(t, a, isApp("planetaire"))
	before := a.selIdx
	a.event(patch("laptop", model.Change{Op: model.OpAdd, Kind: model.KindAgent, Key: "codex:300",
		Data: model.Agent{Key: "codex:300", Tool: "codex", PID: 300, CWD: "/home/alfred/x", State: model.StateWorking}}), t0.Add(3*time.Second))
	a.sync()
	if e := a.selected(); e == nil || e.app == nil || e.app.Name != "planetaire" {
		t.Fatalf("sélection perdue : %+v", e)
	}
	if a.selIdx != before+1 {
		t.Errorf("la ligne choisie devait descendre d'un cran : %d → %d", before, a.selIdx)
	}
}

// Un élément qui disparaît reste un temps en fantôme, puis la sélection passe
// à la ligne qui prend sa place.
func TestSelectionWhenItemGoes(t *testing.T) {
	a, _ := newTestApp(80, 24, fleetEvents(true)...)
	selectWhere(t, a, isPort(5432))
	a.event(patch("laptop", model.Change{Op: model.OpRemove, Kind: model.KindListener, Key: "tcp4:0.0.0.0:5432"}), t0.Add(3*time.Second))
	a.sync()
	if e := a.selected(); e == nil || !e.ghost {
		t.Fatalf("le port fermé doit rester en fantôme sous la sélection : %+v", e)
	}
	a.tick(t0.Add(5 * time.Second))
	if !contains(look(a), "fermé il y a 2 s") {
		t.Error("le fantôme doit dire depuis quand le port est fermé")
	}
	a.tick(t0.Add(3*time.Second + ghostTTL + time.Second))
	a.sync()
	if e := a.selected(); e == nil || e.ghost {
		t.Fatalf("après le fantôme, la sélection doit tomber sur une vraie ligne : %+v", e)
	}
}

// Parcours 2 : un nombre tapé cherche un port tout de suite.
func TestDigitsSearchAPort(t *testing.T) {
	a, _ := newTestApp(80, 24, fleetEvents(true)...)
	typeKeys(a, "5173")
	if !a.typing || a.query != "5173" {
		t.Fatalf("recherche : typing=%v query=%q", a.typing, a.query)
	}
	a.sync()
	if len(a.view) != 2 {
		t.Fatalf("5173 : %d réponses, attendu les 2 programmes du même port", len(a.view))
	}
	s := look(a)
	if !contains(s, "cherche 5173") || !contains(s, "node") || !contains(s, "laptop") {
		t.Errorf("la recherche doit montrer programme et machine :\n%s", strings.Join(s, "\n"))
	}
	a.key(kc(term.KeyEnter))
	if a.typing || a.query != "5173" {
		t.Error("⏎ garde le filtre et rend les touches")
	}
	a.key(kc(term.KeyEnter))
	if a.mode != modeDetail {
		t.Fatal("⏎ ouvre le détail")
	}
	if s := look(a); !contains(s, "~/Global/Projets/") {
		t.Error("le détail d'un port dit son dossier")
	}
	a.key(kc(term.KeyEsc))
	a.key(kc(term.KeyEsc))
	if a.mode != modeMain || a.query != "" {
		t.Errorf("échap ferme le détail puis efface : mode=%v query=%q", a.mode, a.query)
	}
}

// Des chiffres qu'aucun port ni pid ne porte se cherchent dans les noms : un
// dossier « 2025-stage-… » se retrouve, mais 5173 ne répond qu'au port.
func TestDigitsFallBackToNames(t *testing.T) {
	stage := "/home/alfred/Global/Archives/2025-stage-M2-analyse"
	evs := append(fleetEvents(true), patch("laptop", model.Change{Op: model.OpAdd, Kind: model.KindRepo, Key: stage,
		Data: model.Repo{Key: stage, Path: stage, Branch: "main", Dirty: no()}}))
	a, _ := newTestApp(80, 24, evs...)
	typeKeys(a, "/2025")
	a.sync()
	if len(a.view) != 1 || a.view[0].repo == nil {
		t.Errorf("2025 : %d réponses, attendu le dépôt 2025-stage", len(a.view))
	}
	a.key(kc(term.KeyEsc))
	typeKeys(a, "/5173")
	a.sync()
	if len(a.view) != 2 || a.view[0].port == nil {
		t.Errorf("5173 : %d réponses, attendu les 2 programmes du port", len(a.view))
	}
}

// q quitte partout, même pendant une recherche ; Q y écrit un q.
func TestQuitKeys(t *testing.T) {
	a, _ := newTestApp(80, 24, fleetEvents(true)...)
	typeKeys(a, "/Q")
	if a.query != "q" || a.quit {
		t.Fatalf("Q doit écrire q : query=%q quit=%v", a.query, a.quit)
	}
	a.key(rn('q'))
	if !a.quit {
		t.Error("q doit quitter pendant une recherche")
	}
	b, _ := newTestApp(80, 24, fleetEvents(true)...)
	b.mode = modeConfirm
	b.key(kc(term.KeyCtrlC))
	if !b.quit {
		t.Error("Ctrl-C doit quitter pendant une question")
	}
}

// Parcours 4 : arrêter demande confirmation, puis part par la liste fermée.
func TestStopAsksThenSends(t *testing.T) {
	a, src := newTestApp(80, 24, fleetEvents(true)...)
	selectWhere(t, a, isApp("site"))
	a.key(rn('a'))
	if a.mode != modeConfirm || len(src.sent) != 0 {
		t.Fatalf("a doit demander avant d'envoyer : mode=%v envoyées=%d", a.mode, len(src.sent))
	}
	if s := look(a); !contains(s, "Arrêter site sur laptop ?") {
		t.Errorf("question :\n%s", strings.Join(s, "\n"))
	}
	a.key(kc(term.KeyEnter))
	if len(src.sent) != 1 {
		t.Fatalf("envoyées : %d", len(src.sent))
	}
	r := src.sent[0]
	if r.machine != "laptop" || r.req.Op != model.ReqStop || r.req.Target != "site" {
		t.Errorf("requête : %+v", r)
	}
	a.now = t0.Add(2400 * time.Millisecond)
	if s := look(a); !contains(s, "arrêt · 0,4 s") {
		t.Errorf("l'attente doit se voir sur la ligne :\n%s", strings.Join(s, "\n"))
	}
	a.event(source.Event{Machine: "laptop", Reply: &model.Reply{ID: r.req.ID, OK: true}}, t0.Add(2600*time.Millisecond))
	a.tick(t0.Add(2600 * time.Millisecond))
	if !strings.Contains(a.note, "arrêter site sur laptop : fait en 0,6 s") {
		t.Errorf("réponse : %q", a.note)
	}
}

func TestCancelSendsNothing(t *testing.T) {
	a, src := newTestApp(80, 24, fleetEvents(true)...)
	selectWhere(t, a, isApp("site"))
	a.key(rn('r'))
	a.key(kc(term.KeyEsc))
	if len(src.sent) != 0 || a.mode != modeMain || a.note != "rien n'a été envoyé" {
		t.Errorf("annuler : envoyées=%d mode=%v note=%q", len(src.sent), a.mode, a.note)
	}
}

// Démarrer ne coupe rien : pas de question.
func TestStartGoesStraight(t *testing.T) {
	a, src := newTestApp(80, 24, fleetEvents(true)...)
	selectWhere(t, a, isApp("planetaire"))
	a.key(rn('d'))
	if len(src.sent) != 1 || src.sent[0].req.Op != model.ReqStart || src.sent[0].req.Target != "planetaire" {
		t.Errorf("démarrer : %+v", src.sent)
	}
}

// Une action lente : une seule requête à la fois par élément ; sans réponse
// au bout de 20 s, le signe cesse de tourner, l'affichage revient au calme et
// la requête peut se refaire.
func TestSlowActionStaysCalm(t *testing.T) {
	a, src := newTestApp(80, 24, fleetEvents(false)...)
	selectWhere(t, a, isApp("planetaire"))
	a.key(rn('d'))
	a.key(rn('d'))
	if len(src.sent) != 1 || !strings.Contains(a.note, "démarrage déjà en cours") {
		t.Fatalf("un second d pendant l'attente ne doit rien renvoyer : %d requêtes, note %q", len(src.sent), a.note)
	}
	sent := t0.Add(2 * time.Second) // l'heure de l'app d'essai après l'ouverture
	if got := a.frameEvery(sent.Add(time.Second)); got != 125*time.Millisecond {
		t.Errorf("pendant l'attente, le signe tourne : %v", got)
	}
	late := sent.Add(25 * time.Second)
	a.tick(late)
	if got := a.frameEvery(late); got != time.Second {
		t.Errorf("sans réponse depuis 25 s, l'affichage revient au calme : %v", got)
	}
	if s := look(a); !contains(s, "sans réponse depuis 25 s") {
		t.Errorf("l'attente doit se lire sur la ligne :\n%s", strings.Join(s, "\n"))
	}
	a.key(kc(term.KeyEnter))
	if s := look(a); !contains(s, "démarrage : sans réponse depuis 25 s") || contains(s, "25,0 s") {
		t.Errorf("l'attente doit se lire dans le détail :\n%s", strings.Join(s, "\n"))
	}
	a.key(kc(term.KeyEsc))
	a.key(rn('d'))
	if len(src.sent) != 2 {
		t.Errorf("après 20 s sans réponse, la requête peut se refaire : %d requêtes", len(src.sent))
	}
}

// Un refus s'affiche avec sa raison entière, même à 80 colonnes.
func TestRefusalShowsItsReason(t *testing.T) {
	a, src := newTestApp(80, 24, fleetEvents(true)...)
	selectWhere(t, a, isPort(5432))
	a.key(rn('l'))
	if s := look(a); !contains(s, "tenu par un autre compte (uid 0)") || !contains(s, "dossier inconnus") {
		t.Errorf("la question doit dire que le port est à un autre compte :\n%s", strings.Join(s, "\n"))
	}
	a.key(rn('o'))
	if len(src.sent) != 1 || src.sent[0].req.Op != model.ReqFreePort || src.sent[0].req.Target != "5432" {
		t.Fatalf("libérer : %+v", src.sent)
	}
	why := "le port 5432 est tenu par un autre compte (uid 0) : connex ne peut pas le libérer"
	a.event(source.Event{Machine: "laptop", Reply: &model.Reply{ID: src.sent[0].req.ID, Error: why}}, t0.Add(3*time.Second))
	a.tick(t0.Add(3 * time.Second))
	s := look(a)
	bottom := s[22] + " " + s[23]
	if !strings.Contains(bottom, "refusé") || !strings.Contains(bottom, "connex ne peut pas le libérer") {
		t.Errorf("raison du refus coupée :\n%s\n%s", s[22], s[23])
	}
	if !contains(s, "✕ refusé") {
		t.Error("la ligne du port doit porter le refus")
	}
	a.tick(t0.Add(3*time.Second + answerTTL + time.Second))
	if a.note != "" {
		t.Error("la réponse s'efface après quelques secondes")
	}
	// passé ce temps, la chronique la garde entière : la ligne choisie s'y
	// écrit sur plusieurs lignes au lieu d'être coupée
	a.tick(t0.Add(3*time.Second + resultTTL + time.Second))
	a.key(rn('c'))
	s = look(a)
	whole := strings.Join(strings.Fields(strings.ReplaceAll(strings.Join(s[2:5], " "), "▌", " ")), " ")
	if !strings.Contains(whole, "laptop · libérer le port 5432 — refusé : "+why) {
		t.Errorf("la chronique doit garder le refus entier :\n%s", strings.Join(s, "\n"))
	}
	if !strings.Contains(s[5], "premier relevé") {
		t.Errorf("les autres lignes suivent la ligne choisie :\n%s", strings.Join(s, "\n"))
	}
}

// Une action qui n'a pas de sens ici le dit, sans rien envoyer.
func TestActionOnAgentExplains(t *testing.T) {
	a, src := newTestApp(80, 24, fleetEvents(true)...)
	selectWhere(t, a, func(e *entry) bool { return e.agent != nil })
	a.key(rn('a'))
	if len(src.sent) != 0 || a.mode != modeMain || !strings.Contains(a.note, "choisis une ligne d'appli") {
		t.Errorf("a sur un agent : envoyées=%d mode=%v note=%q", len(src.sent), a.mode, a.note)
	}
}

// L'ouverture ne bloque jamais : une touche l'abrège et agit, et elle finit
// seule avant 1,5 s.
func TestOpeningNeverBlocks(t *testing.T) {
	src := newFake()
	a := newApp(src, t0)
	a.W, a.H = 80, 24
	for _, ev := range fleetEvents(true) {
		a.event(ev, t0)
	}
	a.now = t0.Add(100 * time.Millisecond)
	a.sync()
	start := a.selIdx
	a.key(rn('j'))
	if a.opening || a.selIdx != start+1 {
		t.Errorf("j pendant l'ouverture : opening=%v sélection %d → %d", a.opening, start, a.selIdx)
	}

	b := newApp(src, t0)
	b.key(kc(term.KeyEnter))
	if b.opening || b.mode != modeMain {
		t.Error("⏎ pendant l'ouverture l'abrège seulement")
	}

	// sans toucher à rien, l'état est lisible avant 1,5 s (la limite du prompt)
	c := newApp(src, t0)
	c.W, c.H = 80, 24
	for _, ev := range fleetEvents(true) {
		c.event(ev, t0)
	}
	c.tick(t0.Add(1499 * time.Millisecond))
	if s := look(c); c.opening || !contains(s[:5], "un agent t'attend") || !contains(s, "claude t'attend") {
		t.Errorf("à 1,499 s, l'ouverture doit être finie et l'état lisible :\n%s", strings.Join(s, "\n"))
	}
}

// Parcours 3 : une machine qui tombe puis revient se voit, avec la raison.
func TestOutageThenReturn(t *testing.T) {
	a, _ := newTestApp(80, 24, fleetEvents(true)...)
	at := t0.Add(10 * time.Second)
	a.event(status("desk", false, "ssh: connect to host desk port 22: Connection timed out"), at)
	a.tick(at.Add(3 * time.Second))
	s := look(a)
	if !contains(s[:5], "injoignable · 3 s") {
		t.Errorf("coupure :\n%s", strings.Join(s[:5], "\n"))
	}
	// à 80 colonnes la cause ne tient pas : on lit sa fin, la raison
	if !contains(s[:6], "…Connection timed out") || !strings.Contains(s[22], "desk injoignable — …Connection timed out") {
		t.Errorf("la raison de la coupure doit se lire :\n%s", strings.Join(s, "\n"))
	}
	if got := a.frameEvery(at.Add(100 * time.Millisecond)); got != 40*time.Millisecond {
		t.Errorf("l'éclipse s'anime : image toutes les %v", got)
	}
	a.W, a.H = 160, 45
	if s := look(a); !contains(s[:10], "injoignable") || !contains(s, "desk injoignable — ssh: connect to host desk") || !contains(s, "port 22: Connection timed out") {
		t.Errorf("à 160 × 45, la coupure et sa cause s'écrivent en entier :\n%s", strings.Join(s, "\n"))
	}
	a.W, a.H = 80, 24
	back := at.Add(20 * time.Second)
	a.event(status("desk", true, ""), back)
	a.tick(back.Add(5 * time.Second))
	if s := look(a); !contains(s[:5], "revenue il y a 5 s") {
		t.Errorf("retour :\n%s", strings.Join(s[:5], "\n"))
	}
}

// Un agent qui t'attendait sur une machine coupée reste affiché (dernier état
// connu), mais l'aile dit que sa machine ne répond plus au lieu d'une durée.
func TestWaitingOnACutMachine(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 36}, {160, 45}} {
		a, _ := newTestApp(size[0], size[1], fleetEvents(true)...)
		a.tick(t0.Add(30 * time.Second))
		if s := look(a); !contains(s[:8], "site · claude") || contains(s[:8], "vps · injoignable") {
			t.Fatalf("%v : avant la coupure :\n%s", size, strings.Join(s[:8], "\n"))
		}
		a.event(status("vps", false, "Connection timed out"), t0.Add(31*time.Second))
		a.tick(t0.Add(40 * time.Second))
		if s := look(a); !contains(s[:8], "vps · injoignable") || contains(s[:8], "vps · depuis") {
			t.Errorf("%v : pendant la coupure :\n%s", size, strings.Join(s[:8], "\n"))
		}
	}
}

// Parcours 3 : ce qui vient d'apparaître se voit sur sa ligne et dans les
// nouvelles, et les ports ouverts à tous ont leur marche.
func TestNewsAndOpenPorts(t *testing.T) {
	a, _ := newTestApp(80, 24, fleetEvents(true)...)
	at := t0.Add(5 * time.Second)
	l := model.Listener{Key: "tcp4:0.0.0.0:8080", Port: 8080, Addr: "0.0.0.0", Exposure: "all", Mine: true, UID: 1000,
		PID: 77, Process: "python3", CWD: "/home/alfred/Global/Projets/tango"}
	a.event(patch("laptop", model.Change{Op: model.OpAdd, Kind: model.KindListener, Key: l.Key, Data: l}), at)
	a.tick(at.Add(2 * time.Second))
	a.key(kc(term.KeyTab))
	a.key(kc(term.KeyTab))
	s := look(a)
	if !contains(s, "ouverts à tous") {
		t.Errorf("marche des ports ouverts à tous :\n%s", strings.Join(s, "\n"))
	}
	if !contains(s, "ouvert il y a 2 s") {
		t.Error("le port qui vient d'apparaître doit le dire")
	}
	if !strings.Contains(s[22], "port 8080 ouvert") {
		t.Errorf("nouvelles : %q", s[22])
	}
}

// Ce qu'une machine rapporte en revenant d'une coupure a pu arriver à
// n'importe quel moment de la coupure : connex le dit « au retour », sans
// heure ni durée inventées. Ce qui arrive ensuite est daté normalement.
func TestReturnDoesNotDateWhatItFound(t *testing.T) {
	vps := func(s *model.Snapshot) {
		s.Agents = append(s.Agents, model.Agent{Key: "claude:200", Tool: "claude", PID: 200,
			CWD: "/home/alfred/apps/site", State: model.StateWorking})
		s.Listeners = append(s.Listeners, model.Listener{Key: "tcp4:127.0.0.1:6006", Port: 6006, Addr: "127.0.0.1",
			Exposure: "loopback", Mine: true, UID: 1000, PID: 60, Process: "node", CWD: "/home/alfred/apps/ui-kit"})
	}
	for _, size := range [][2]int{{160, 45}, {120, 36}, {80, 24}} {
		a, _ := newTestApp(size[0], size[1], snap("laptop", nil), snap("vps", vps))
		a.event(status("vps", false, "Connection timed out"), t0.Add(10*time.Second))
		back := t0.Add(40 * time.Second)
		a.event(status("vps", true, ""), back)
		found := func(s *model.Snapshot) {
			s.Agents = append(s.Agents,
				model.Agent{Key: "claude:200", Tool: "claude", PID: 200, CWD: "/home/alfred/apps/site", State: model.StateWaiting},
				model.Agent{Key: "claude:201", Tool: "claude", PID: 201, CWD: "/home/alfred/apps/relay", State: model.StateWaiting})
			s.Listeners = append(s.Listeners, model.Listener{Key: "tcp4:0.0.0.0:8080", Port: 8080, Addr: "0.0.0.0",
				Exposure: "all", Mine: true, UID: 1000, PID: 80, Process: "python3", CWD: "/home/alfred/apps/relay"})
		}
		a.event(snap("vps", found), back)
		for _, id := range []string{"claude:200", "claude:201"} {
			if _, ok := a.w.since[itemID{"vps", model.KindAgent, id}]; ok {
				t.Errorf("%v : %s attend depuis une heure inconnue, rien ne doit être retenu", size, id)
			}
		}
		a.tick(back.Add(3 * time.Second))
		s := look(a)
		if !contains(s, "ouvert au retour") || !contains(s, "fermé au retour") {
			t.Errorf("%v : ce que le retour a montré doit se dire « au retour » :\n%s", size, strings.Join(s, "\n"))
		}
		if contains(s, "depuis 3 s") || contains(s, "ouvert il y a") || contains(s, "fermé il y a") || contains(s, "changé il y a") {
			t.Errorf("%v : une heure inventée :\n%s", size, strings.Join(s, "\n"))
		}
		if !contains(s, "il y a 3 s") {
			t.Errorf("%v : le retour lui-même reste daté :\n%s", size, strings.Join(s, "\n"))
		}
		a.key(rn('c'))
		if s := look(a); !contains(s, "au retour") || !contains(s, "de nouveau joignable, après 30 s") {
			t.Errorf("%v : chronique :\n%s", size, strings.Join(s, "\n"))
		}
		a.key(kc(term.KeyEsc))
		// après le retour, un relevé complet (demandé par R, par exemple) : ce
		// qu'il montre de nouveau a son heure
		a.event(snap("vps", func(s *model.Snapshot) {
			found(s)
			s.Listeners = append(s.Listeners, model.Listener{Key: "tcp4:127.0.0.1:9090", Port: 9090, Addr: "127.0.0.1",
				Exposure: "loopback", Mine: true, UID: 1000, PID: 90, Process: "go", CWD: "/home/alfred/apps/relay"})
		}), back.Add(5*time.Second))
		if r := a.w.chron[len(a.w.chron)-1]; r.back || !strings.Contains(r.text, "9090") {
			t.Errorf("%v : un changement vu après le retour : %+v", size, r)
		}
	}
}

// Sur un très petit écran, la ligne du haut nomme chaque machine une fois,
// même quand plusieurs agents y attendent.
func TestTinyHeaderNamesEachMachineOnce(t *testing.T) {
	evs := append(fleetEvents(true), patch("vps", model.Change{Op: model.OpAdd, Kind: model.KindAgent, Key: "codex:201",
		Data: model.Agent{Key: "codex:201", Tool: "codex", PID: 201, CWD: "/home/alfred/apps/relay", State: model.StateWaiting}}))
	seen := false
	for w := 36; w <= 80; w++ {
		for h := 10; h <= 20; h++ {
			a, _ := newTestApp(w, h, evs...)
			s := look(a)
			seen = seen || contains(s, "2 agents t'attendent — vps")
			if contains(s, "vps, vps") {
				t.Errorf("%d × %d : une machine nommée deux fois :\n%s", w, h, strings.Join(s, "\n"))
			}
		}
	}
	if !seen {
		t.Error("la ligne du haut des petits écrans n'a pas été vue")
	}
}

// Deux programmes sur un port, un port d'un autre compte, un chemin très long.
func TestEdgeCasesRead(t *testing.T) {
	a, _ := newTestApp(80, 24, fleetEvents(true)...)
	typeKeys(a, "/depot-final")
	a.key(kc(term.KeyEnter))
	s := look(a)
	if !contains(s, "depot-final") {
		t.Errorf("le nom d'un dépôt au chemin très long doit rester lisible :\n%s", strings.Join(s, "\n"))
	}
	// le détail l'écrit en entier, sur plusieurs lignes
	a.key(kc(term.KeyEnter))
	s = look(a)
	for _, part := range strings.Split(strings.TrimPrefix(longRepo, "/home/alfred/"), "/") {
		if !contains(s, part+"/") && !contains(s, part+" ") {
			t.Errorf("le détail doit écrire le chemin entier, %q manque :\n%s", part, strings.Join(s, "\n"))
		}
	}
	a.key(kc(term.KeyEsc))
	a.key(kc(term.KeyEsc))
	typeKeys(a, "5432")
	a.key(kc(term.KeyEnter))
	a.key(kc(term.KeyEnter))
	if s := look(a); !contains(s, "programme et dossier inconnus") || !contains(s, "uid 0") {
		t.Errorf("port d'un autre compte :\n%s", strings.Join(s, "\n"))
	}
	a.key(kc(term.KeyEsc))
	a.key(kc(term.KeyEsc))
	typeKeys(a, "5173")
	a.key(kc(term.KeyEnter))
	if s := look(a); !contains(s, "aussi node") {
		t.Errorf("deux programmes sur un port :\n%s", strings.Join(s, "\n"))
	}
}

// La cadence des images reste sobre : une par seconde au repos, plus vite
// seulement quand quelque chose bouge.
func TestFrameCadence(t *testing.T) {
	calm, _ := newTestApp(80, 24, fleetEvents(false)...)
	if got := calm.frameEvery(t0.Add(5 * time.Second)); got != time.Second {
		t.Errorf("au repos : %v", got)
	}
	a, _ := newTestApp(80, 24, fleetEvents(true)...)
	if got := a.frameEvery(t0.Add(5 * time.Second)); got != 125*time.Millisecond {
		t.Errorf("un agent attend : %v", got)
	}
	a.mode = modeChron
	if got := a.frameEvery(t0.Add(5 * time.Second)); got != time.Second {
		t.Errorf("chronique : %v", got)
	}
	o := newApp(newFake(), t0)
	if got := o.frameEvery(t0); got != 40*time.Millisecond {
		t.Errorf("ouverture : %v", got)
	}
}

// Changer de taille en route : tout se recompose.
func TestResizeRecomposes(t *testing.T) {
	a, _ := newTestApp(160, 45, fleetEvents(true)...)
	look(a)
	a.W, a.H = 80, 24
	s := look(a)
	if !strings.Contains(s[23], "q quitter") || !contains(s[:5], "t'attend") {
		t.Errorf("après redimensionnement :\n%s", strings.Join(s, "\n"))
	}
}

// La légende tient entière à 80 × 24 en défilant.
func TestHelpScrollsAt80x24(t *testing.T) {
	a, _ := newTestApp(80, 24, fleetEvents(true)...)
	a.key(rn('?'))
	s := look(a)
	if !a.helpOver || !contains(s, "↑↓ la suite") {
		t.Fatal("la légende dépasse à 80 × 24 : elle doit l'annoncer")
	}
	for _, l := range s {
		if strings.TrimSpace(l) == "voir" {
			t.Errorf("un titre de section ne reste pas seul en bas :\n%s", strings.Join(s, "\n"))
		}
	}
	for i := 0; i < 30; i++ {
		a.key(kc(term.KeyDown))
	}
	if s := look(a); !contains(s, "astres") || a.mode != modeHelp {
		t.Errorf("fin de la légende :\n%s", strings.Join(s, "\n"))
	}
	a.key(rn('x'))
	if a.mode != modeMain {
		t.Error("une autre touche ferme la légende")
	}
}

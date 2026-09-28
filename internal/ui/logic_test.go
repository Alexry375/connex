package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/canvas"
	"github.com/Alexry375/connex/internal/ui/term"
)

// Ce qui ne se voit pas à l'œil : sélection, recherche, mise en page à 80
// colonnes, réactions aux événements, touches qui quittent.

func code(c term.Code) term.Key { return term.Key{Code: c} }

// typeText tape une chaîne, touche par touche.
func typeText(a *App, s string, now time.Time) {
	for _, r := range s {
		a.key(term.R(r), now)
	}
}

// rowsOf renvoie les rangées de l'image de l'instant now.
func rowsOf(t *testing.T, a *App, now time.Time) []string {
	t.Helper()
	cv := a.frame(now)
	rows := make([]string, cv.H)
	for y := range rows {
		rows[y] = cv.Row(y)
		if w := canvas.StringWidth(rows[y]); w != cv.W {
			t.Fatalf("rangée %d : %d colonnes au lieu de %d\n%s", y, w, cv.W, rows[y])
		}
	}
	return rows
}

func patchEv(machine string, at time.Time, cs ...model.Change) source.Event {
	p := model.NewPatch(machine, at, cs)
	return source.Event{Machine: machine, Patch: &p}
}

func statusEv(machine string, reachable bool, reason string, at time.Time) source.Event {
	st := model.MachineStatus{Schema: model.Schema, Type: "status", Machine: machine, Reachable: reachable,
		Since: model.Timestamp(at), Reason: reason}
	return source.Event{Machine: machine, Status: &st}
}

func ids(items []*item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}

func lastEntry(t *testing.T, a *App) entry {
	t.Helper()
	if len(a.journal) == 0 {
		t.Fatal("journal vide")
	}
	return a.journal[len(a.journal)-1]
}

var (
	port5173v4 = itemID("laptop", kindPort, "tcp4:127.0.0.1:5173")
	port5173v6 = itemID("laptop", kindPort, "tcp6:[::1]:5173")
	port5000   = itemID("laptop", kindPort, "tcp4:127.0.0.1:5000")
	port8384   = itemID("laptop", kindPort, "tcp4:100.64.0.1:8384")
	appPoly    = itemID("laptop", kindApp, "planetaire")
	appSite    = itemID("vps", kindApp, "site")
	agentWait  = itemID("laptop", kindAgent, "claude:201")
)

// La sélection suit son élément quand le catalogue change, et passe à son
// voisin s'il disparaît.
func TestSelectionFollowsItsItem(t *testing.T) {
	a, _ := newTestApp(t, 80, 24)
	now := t0.Add(5 * time.Second)
	if it := a.selected(); it == nil || it.id != agentWait {
		t.Fatalf("à l'ouverture, le choix doit aller à l'agent qui attend ; il est sur %v", it)
	}
	for range 3 {
		a.key(code(term.Down), now)
	}
	if it := a.selected(); it.id != port5173v4 {
		t.Fatalf("après 3 × ↓ : %q, voulu %q", it.id, port5173v4)
	}

	// Un agent apparaît en tête du catalogue : le choix ne glisse pas.
	ag := model.Agent{Key: "claude:205", Tool: "claude", PID: 205, CWD: "/home/alfred/p/aaa", State: model.StateWorking}
	a.event(patchEv("laptop", now, model.Change{Op: model.OpAdd, Kind: model.KindAgent, Key: ag.Key, Data: ag}), now)
	rowsOf(t, a, now)
	if it := a.selected(); it.id != port5173v4 {
		t.Fatalf("après un ajout en tête, le choix est passé à %q", it.id)
	}

	// L'élément choisi disparaît : son voisin prend sa place.
	a.event(patchEv("laptop", now, model.Change{Op: model.OpRemove, Kind: model.KindListener, Key: "tcp4:127.0.0.1:5173"}), now)
	rowsOf(t, a, now)
	if it := a.selected(); it == nil || it.id != port5173v6 {
		t.Fatalf("après la disparition de l'élément choisi : %v, voulu le voisin %q", it, port5173v6)
	}

	// Chaque machine garde son choix.
	a.key(code(term.Right), now)
	if it := a.selected(); it == nil || it.id != appSite {
		t.Fatalf("sur vps : %v, voulu %q", it, appSite)
	}
	a.key(code(term.Left), now)
	if it := a.selected(); it.id != port5173v6 {
		t.Fatalf("de retour sur laptop : %q, voulu %q", it.id, port5173v6)
	}
}

// Recherche : chiffres = port (exact d'abord), mots repliés, toutes machines.
func TestSearch(t *testing.T) {
	a, src := newTestApp(t, 80, 24)
	now := t0.Add(5 * time.Second)

	typeText(a, "5173", now)
	if !a.typing || a.query != "5173" {
		t.Fatalf("un chiffre ouvre la recherche : typing=%v query=%q", a.typing, a.query)
	}
	if got, want := ids(a.results), []string{port5173v4, port5173v6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("5173 : %q, voulu %q", got, want)
	}

	// Deux objets sur le port 5000 : l'appli arrêtée et le programme qui le tient.
	a.key(code(term.Esc), now)
	typeText(a, "5000", now)
	if got, want := ids(a.results), []string{appPoly, port5000}; !reflect.DeepEqual(got, want) {
		t.Fatalf("5000 : %q, voulu %q", got, want)
	}
	a.key(code(term.Down), now)
	a.key(code(term.Enter), now)
	if a.typing || a.view != viewPlate || a.plate != port5000 {
		t.Fatalf("↓ Entrée ouvre le 2e résultat : typing=%v vue=%d planche=%q", a.typing, a.view, a.plate)
	}
	a.key(code(term.Esc), now)
	if a.view != viewSky || a.query != "5000" || a.selected().id != port5000 {
		t.Fatalf("Échap revient aux résultats : vue=%d query=%q", a.view, a.query)
	}
	a.key(code(term.Esc), now)
	if a.query != "" || a.view != viewSky {
		t.Fatalf("Échap, Échap : query=%q vue=%d", a.query, a.view)
	}

	// Mots : sans accents ni majuscules, sur toutes les machines.
	cases := []struct {
		q    string
		want []string
	}{
		{"TRÈS", []string{port8384}},
		{"tres-long", []string{port8384}},
		{"node vps", []string{appSite}},
		{"site", []string{appSite}},
		{"zzz", []string{}},
	}
	for _, c := range cases {
		a.key(term.R('/'), now)
		typeText(a, c.q, now)
		if got := ids(a.results); !reflect.DeepEqual(got, c.want) {
			t.Errorf("« %s » : %q, voulu %q", c.q, got, c.want)
		}
		a.key(code(term.Esc), now)
	}

	// « Q » s'écrit, « q » quitte.
	a.key(term.R('/'), now)
	typeText(a, "Q", now)
	if a.quit || a.query != "Q" {
		t.Fatalf("Q pendant la recherche : quit=%v query=%q", a.quit, a.query)
	}
	a.key(code(term.Esc), now)

	// Rien ne répond : la planche le dit.
	a.key(term.R('/'), now)
	typeText(a, "zzz", now)
	if rows := strings.Join(rowsOf(t, a, now), "\n"); !strings.Contains(rows, "Rien ne répond à « zzz ».") {
		t.Fatalf("recherche vide : message absent\n%s", rows)
	}
	a.key(code(term.Esc), now)

	// Une action sur un résultat, depuis sa planche, vise la machine du résultat.
	a.key(term.R('/'), now)
	typeText(a, "site", now)
	a.key(code(term.Enter), now)
	a.key(term.R('a'), now)
	if a.confirm == nil || a.confirm.machine != "vps" || a.confirm.target != "site" {
		t.Fatalf("arrêter depuis la recherche : %+v", a.confirm)
	}
	if len(src.sent) != 0 {
		t.Fatalf("rien ne part avant la confirmation : %+v", src.sent)
	}
}

// À 80 × 24, chaque planche tient : titre, touches, agent qui attend.
func TestLayoutAt80(t *testing.T) {
	a, _ := newTestApp(t, 80, 24)
	now := t0.Add(5 * time.Second)

	rows := rowsOf(t, a, now)
	all := strings.Join(rows, "\n")
	for _, want := range []string{"UN AGENT T’ATTEND SUR LAPTOP.", "✦ 1 agent t’attend", "✦ nuage"} {
		if !strings.Contains(all, want) {
			t.Errorf("planche I : « %s » absent\n%s", want, all)
		}
	}
	if last := rows[23]; !strings.Contains(last, "q quitter") || !strings.Contains(last, "? légende") {
		t.Errorf("planche I : touches absentes : %q", last)
	}

	// Planche II de chaque objet : son titre et le moyen d'en sortir.
	for _, mv := range a.w.machines {
		for _, it := range mv.items {
			a.plate, a.view = it.id, viewPlate
			rows := rowsOf(t, a, now)
			title := strings.ToUpper(objectTitle(it)) + "."
			if !strings.Contains(rows[22], title) {
				t.Errorf("planche II de %q : titre %q absent de %q", it.id, title, rows[22])
			}
			if !strings.Contains(rows[23], "Échap retour") || !strings.Contains(rows[23], "q quitter") {
				t.Errorf("planche II de %q : touches absentes : %q", it.id, rows[23])
			}
		}
	}

	// Un chemin très long se lit en entier, coupé après ses « / ».
	a.plate = port8384
	all = strings.Join(rowsOf(t, a, now), "\n")
	for _, part := range strings.Split(strings.Trim(longDir, "/"), "/") {
		if !strings.Contains(all, part) {
			t.Errorf("chemin long : « %s » manque\n%s", part, all)
		}
	}

	// La planche d'une appli dit sa machine, le programme qui tient son port
	// et son dossier (parcours 2).
	a.plate = appSite
	all = strings.Join(rowsOf(t, a, now), "\n")
	for _, want := range []string{"appli · vps", "programme", "node (pid 301)", "/home/alfred/p/site"} {
		if !strings.Contains(all, want) {
			t.Errorf("planche de site : « %s » absent\n%s", want, all)
		}
	}

	// Journal et légende : toutes les touches de la légende paraissent.
	a.view = viewJournal
	if rows := rowsOf(t, a, now); !strings.Contains(rows[22], "JOURNAL D’OBSERVATION.") || !strings.Contains(rows[23], "q quitter") {
		t.Errorf("planche III : %q / %q", rows[22], rows[23])
	}
	a.view = viewLegend
	all = strings.Join(rowsOf(t, a, now), "\n")
	for _, k := range legendKeys {
		if !strings.Contains(all, k[1]) {
			t.Errorf("légende à 80 × 24 : touche « %s %s » absente", k[0], k[1])
		}
	}
	for _, s := range legendSigns {
		if !strings.Contains(all, s.text) {
			t.Errorf("légende à 80 × 24 : signe « %s » absent", s.text)
		}
	}

	// Confirmation : la question, son détail et la réponse attendue tiennent,
	// « Entrée : oui » une fois la question armée.
	a.plate, a.view = appSite, viewPlate
	a.key(term.R('a'), now)
	rows = rowsOf(t, a, now)
	if !strings.Contains(rows[21], "ARRÊTER L’APPLI SITE SUR VPS ?") || !strings.Contains(rows[22], "Son unité connex-site.service sera arrêtée.") ||
		!strings.Contains(rows[23], "autre touche : non") || strings.Contains(rows[23], "Entrée : oui") {
		t.Errorf("confirmation avant d'être armée : %q / %q / %q", rows[21], rows[22], rows[23])
	}
	if rows = rowsOf(t, a, now.Add(confirmArm)); !strings.Contains(rows[23], "Entrée : oui") {
		t.Errorf("confirmation armée : %q", rows[23])
	}
	// À 120 colonnes, le détail tient sur la rangée de la réponse.
	a.resize(120, 24)
	if rows = rowsOf(t, a, now.Add(confirmArm)); !strings.Contains(rows[22], "ARRÊTER L’APPLI SITE SUR VPS ?") ||
		!strings.Contains(rows[23], "Son unité connex-site.service sera arrêtée.   Entrée : oui   autre touche : non") {
		t.Errorf("confirmation à 120 colonnes : %q / %q", rows[22], rows[23])
	}
	a.resize(80, 24)
	// Un détail qui ne tient pas sur une rangée se replie.
	a.key(code(term.Esc), now)
	a.resize(50, 24)
	a.key(term.R('r'), now)
	if all := strings.Join(rowsOf(t, a, now.Add(confirmArm)), "\n"); !strings.Contains(all, "Elle sera arrêtée puis relancée par son unité") ||
		!strings.Contains(all, "connex-site.service.") || !strings.Contains(all, "Entrée : oui") {
		t.Errorf("confirmation à 50 colonnes :\n%s", all)
	}
	a.key(code(term.Esc), now)
	a.resize(80, 24)
	a.key(term.R('r'), now)
	rows = rowsOf(t, a, now.Add(confirmArm))
	if !strings.Contains(rows[21], "RELANCER L’APPLI SITE SUR VPS ?") ||
		!strings.Contains(rows[22], "Elle sera arrêtée puis relancée par son unité connex-site.service.") ||
		!strings.Contains(rows[23], "Entrée : oui") {
		t.Errorf("confirmation au détail long : %q / %q / %q", rows[21], rows[22], rows[23])
	}
	// Libérer le port d'un autre compte : la raison se lit en entier.
	a.key(code(term.Esc), now)
	a.plate = itemID("laptop", kindForeign, "tcp4:0.0.0.0:22")
	a.key(term.R('l'), now)
	if all := strings.Join(rowsOf(t, a, now), "\n"); !strings.Contains(all, "Il est tenu par un autre compte (uid 0).") {
		t.Errorf("libérer le port 22 : détail coupé\n%s", all)
	}
}

// Petites fenêtres : la plus petite planche garde l'essentiel ; en dessous,
// l'état tient en une phrase.
func TestSmallWindows(t *testing.T) {
	a, _ := newTestApp(t, minW, minH)
	now := t0.Add(5 * time.Second)
	all := strings.Join(rowsOf(t, a, now), "\n")
	if !strings.Contains(all, "UN AGENT T’ATTEND SUR LAPTOP.") {
		t.Errorf("%d × %d : titre absent\n%s", minW, minH, all)
	}
	a.resize(30, 8)
	all = strings.Join(rowsOf(t, a, now), "\n")
	for _, want := range []string{"UN AGENT T’ATTEND SUR", "Agrandis la fenêtre"} {
		if !strings.Contains(all, want) {
			t.Errorf("30 × 8 : « %s » absent\n%s", want, all)
		}
	}
	// Retour à une grande fenêtre : le choix n'a pas bougé.
	a.resize(160, 45)
	rowsOf(t, a, now)
	if it := a.selected(); it == nil || it.id != agentWait {
		t.Errorf("après redimensionnement : %v", it)
	}
}

// Sans rien reçu, l'interface n'affiche ni nom ni chiffre.
func TestNothingInvented(t *testing.T) {
	a := newApp(&fakeSrc{}, t0)
	a.resize(80, 24)
	a.introOver = true
	all := strings.Join(rowsOf(t, a, t0.Add(2*time.Second)), "\n")
	if strings.ContainsAny(all, "0123456789") {
		t.Errorf("un chiffre sans source :\n%s", all)
	}
	if !strings.Contains(all, "RELEVÉ EN COURS…") {
		t.Errorf("titre d'attente absent :\n%s", all)
	}
}

// Machine injoignable puis revenue : le journal, l'état et le titre le disent.
func TestMachineLostAndBack(t *testing.T) {
	a, _ := newTestApp(t, 80, 24)
	now := t0.Add(10 * time.Second)
	a.event(statusEv("laptop", false, "ssh : délai dépassé", now), now)
	if e := lastEntry(t, a); e.text != "injoignable : ssh : délai dépassé" || e.tone != toneBad {
		t.Fatalf("perte : %+v", e)
	}
	later := now.Add(2 * time.Minute)
	all := strings.Join(rowsOf(t, a, later), "\n")
	for _, want := range []string{"injoignable depuis 2\u00a0min\u00a000\u00a0s", "LAPTOP NE RÉPOND PLUS"} {
		if !strings.Contains(all, want) {
			t.Errorf("pendant l'éclipse : « %s » absent\n%s", want, all)
		}
	}
	// Une raison longue, telle que ssh la donne, se lit en entier dans une
	// colonne de 38 cases.
	long := "ssh: connect to host 100.88.3.21 port 22: Connection timed out"
	a.event(statusEv("laptop", false, long, now), now)
	if all := strings.Join(rowsOf(t, a, later), "\n"); !strings.Contains(all, "Connection timed out") {
		t.Errorf("raison longue coupée\n%s", all)
	}
	// Le dernier état connu reste consultable.
	a.plate, a.view = agentWait, viewPlate
	if all := strings.Join(rowsOf(t, a, later), "\n"); !strings.Contains(all, "Éclipse : laptop ne répond plus depuis 2\u00a0min\u00a000\u00a0s.") {
		t.Errorf("planche II pendant l'éclipse :\n%s", all)
	}
	a.view = viewSky

	back := now.Add(3 * time.Minute)
	s := laptopSnap(back)
	a.event(source.Event{Machine: "laptop", Snapshot: &s}, back)
	if e := lastEntry(t, a); e.text != "de nouveau joignable, après 3\u00a0min\u00a000\u00a0s de silence" || e.tone != toneGood {
		t.Fatalf("retour : %+v", e)
	}
	if all := strings.Join(rowsOf(t, a, back), "\n"); !strings.Contains(all, "UN AGENT T’ATTEND SUR LAPTOP.") {
		t.Errorf("après le retour, le titre revient à l'agent\n%s", all)
	}
}

// Une machine qui s'annonce avant son premier relevé.
func TestMachineAnnouncedBeforeSnapshot(t *testing.T) {
	a := newApp(&fakeSrc{}, t0)
	a.resize(80, 24)
	a.event(statusEv("pi", true, "", t0), t0)
	if len(a.journal) != 0 {
		t.Fatalf("rien à raconter avant le premier relevé : %+v", a.journal)
	}
	a.event(statusEv("pi", false, "ssh : hôte inconnu", t0), t0)
	s := model.NewSnapshot(model.Machine{Name: "pi", User: "abc"}, t0)
	a.event(source.Event{Machine: "pi", Snapshot: &s}, t0.Add(time.Minute))
	if e := lastEntry(t, a); !strings.HasPrefix(e.text, "premier relevé : 0 port, 0 agent, 0 dépôt") {
		t.Fatalf("premier relevé après une perte : %+v", e)
	}
}

// Actions : depuis la planche de l'objet, question, oui par Entrée, envoi,
// réponse, refus avec sa raison, erreur d'envoi.
func TestActions(t *testing.T) {
	a, src := newTestApp(t, 80, 24)
	now := t0.Add(5 * time.Second)
	armed := now.Add(confirmArm)

	// F5 actualise la machine en focus, sans question ; l'attente se lit sous
	// le ciel de la machine, pas sur la ligne choisie, qui garde son état.
	a.key(code(term.F5), now)
	if len(src.sent) != 1 || src.sent[0].Op != model.ReqRefresh || src.to[0] != "laptop" {
		t.Fatalf("actualiser : %+v → %v", src.sent, src.to)
	}
	all := strings.Join(rowsOf(t, a, now.Add(time.Second)), "\n")
	if !strings.Contains(all, "relevé… 1,0\u00a0s") || !strings.Contains(all, "t’attend") {
		t.Errorf("actualiser : l'attente doit se lire sur la machine, la ligne garder son état\n%s", all)
	}
	a.event(source.Event{Machine: "laptop", Reply: &model.Reply{ID: src.sent[0].ID, OK: true}}, now)
	if e := lastEntry(t, a); e.tone != toneGood || e.sign != '✓' {
		t.Fatalf("réponse acceptée : %+v", e)
	}

	// Arrêter : la question ; une autre touche annule.
	a.plate, a.view = appSite, viewPlate
	a.key(term.R('a'), now)
	if a.confirm == nil {
		t.Fatal("arrêter sans question")
	}
	a.key(term.R('x'), now)
	if a.confirm != nil || len(src.sent) != 1 || a.notice == nil || a.notice.text != "Rien n’a été fait." {
		t.Fatalf("annulation : confirm=%v envoyées=%d notice=%v", a.confirm, len(src.sent), a.notice)
	}
	// La même touche ne confirme plus : elle annule.
	a.key(term.R('a'), now)
	a.key(term.R('a'), now)
	if a.confirm != nil || len(src.sent) != 1 {
		t.Fatalf("« a a » : confirm=%v envoyées=%d", a.confirm, len(src.sent))
	}
	// Entrée avant que la question soit armée : ignorée ; après : oui.
	a.key(term.R('a'), now)
	a.key(code(term.Enter), now.Add(confirmArm-time.Millisecond))
	if a.confirm == nil || len(src.sent) != 1 {
		t.Fatalf("Entrée trop tôt : confirm=%v envoyées=%d", a.confirm, len(src.sent))
	}
	a.key(code(term.Enter), armed)
	if len(src.sent) != 2 || src.sent[1].Op != model.ReqStop || src.sent[1].Target != "site" || src.to[1] != "vps" || a.view != viewPlate {
		t.Fatalf("arrêter site : %+v → %v (vue %d)", src.sent, src.to, a.view)
	}
	// Action lente : l'attente se voit sur la planche et sur la ligne.
	slow := armed.Add(3 * time.Second)
	if all := strings.Join(rowsOf(t, a, slow), "\n"); !strings.Contains(all, "arrêt… 3,0\u00a0s") {
		t.Errorf("action en cours, planche II : indicateur absent\n%s", all)
	}
	a.view = viewSky
	a.selectItem(a.w.byID[appSite])
	if all := strings.Join(rowsOf(t, a, slow), "\n"); !strings.Contains(all, "arrêt… 3,0\u00a0s") {
		t.Errorf("action en cours, planche I : indicateur absent\n%s", all)
	}
	// Refus : la raison paraît dans le journal, sur la ligne et sur la planche II.
	a.event(source.Event{Machine: "vps", Reply: &model.Reply{ID: src.sent[1].ID, OK: false, Error: "unité introuvable"}}, slow)
	if e := lastEntry(t, a); e.text != "arrêter l’appli site : refusé — unité introuvable" || e.tone != toneBad {
		t.Fatalf("refus : %+v", e)
	}
	if all := strings.Join(rowsOf(t, a, slow), "\n"); !strings.Contains(all, "refusé — unité introuvable") {
		t.Errorf("refus absent des nouvelles de la planche I\n%s", all)
	}
	a.key(code(term.Enter), slow)
	if all := strings.Join(rowsOf(t, a, slow), "\n"); !strings.Contains(all, "refusé : unité introuvable") {
		t.Errorf("refus absent de la planche II\n%s", all)
	}

	// Sur la planche II, une action impossible le dit dans la marge.
	a.plate, a.view = agentWait, viewPlate
	a.key(term.R('a'), slow)
	if rows := rowsOf(t, a, slow); !strings.Contains(rows[23], "Seule une appli inscrite se démarre, s’arrête ou se relance.") {
		t.Errorf("action impossible sur un agent : %q", rows[23])
	}

	// Libérer le port d'un autre compte : la question dit à qui il est.
	a.plate = itemID("laptop", kindForeign, "tcp4:0.0.0.0:22")
	a.key(term.R('l'), slow)
	if a.confirm == nil || a.confirm.target != "22" || !strings.Contains(a.confirm.detail, "autre compte (uid 0)") {
		t.Fatalf("libérer le port 22 : %+v", a.confirm)
	}
	a.key(code(term.Enter), slow.Add(confirmArm))
	if last := src.sent[len(src.sent)-1]; last.Op != model.ReqFreePort || last.Target != "22" {
		t.Fatalf("libérer : %+v", last)
	}

	// Démarrer demande aussi un oui.
	a.plate = appPoly
	n := len(src.sent)
	a.key(term.R('d'), slow)
	if len(src.sent) != n || a.confirm == nil || a.confirm.detail != "Son unité connex-planetaire.service sera démarrée." {
		t.Fatalf("démarrer sans question : %+v", a.confirm)
	}
	a.key(code(term.Enter), slow.Add(confirmArm))
	if len(src.sent) != n+1 || src.sent[n].Op != model.ReqStart || src.sent[n].Target != "planetaire" {
		t.Fatalf("démarrer : %+v", src.sent[n:])
	}

	// Erreur d'envoi : elle tient lieu de réponse.
	src.err = errRefused
	a.key(code(term.F5), slow)
	if e := lastEntry(t, a); !strings.Contains(e.text, errRefused.Error()) || e.tone != toneBad {
		t.Fatalf("erreur d'envoi : %+v", e)
	}
	// Une réponse à une requête inconnue est ignorée.
	before := len(a.journal)
	a.event(source.Event{Machine: "laptop", Reply: &model.Reply{ID: "inconnue", OK: true}}, slow)
	if len(a.journal) != before {
		t.Fatalf("réponse inconnue racontée : %+v", lastEntry(t, a))
	}

	// Seules les requêtes de la liste fermée sont parties.
	closed := map[string]bool{model.ReqStart: true, model.ReqStop: true, model.ReqRestart: true, model.ReqFreePort: true, model.ReqRefresh: true}
	for _, r := range src.sent {
		if !closed[r.Op] {
			t.Errorf("requête hors liste : %+v", r)
		}
	}
}

// q et Ctrl-C quittent à tout moment, sans rien envoyer.
func TestQuitEverywhere(t *testing.T) {
	now := t0.Add(5 * time.Second)
	modes := []struct {
		name string
		set  func(a *App)
		ok   func(a *App) bool
	}{
		{"ouverture", func(a *App) { a.introOver = false }, func(a *App) bool { return !a.introOver }},
		{"ciel", func(a *App) {}, func(a *App) bool { return a.view == viewSky }},
		{"recherche en cours", func(a *App) { typeText(a, "/no", now) }, func(a *App) bool { return a.typing }},
		{"résultats", func(a *App) { typeText(a, "/no", now); a.key(code(term.Enter), now); a.key(code(term.Esc), now) }, func(a *App) bool { return !a.typing && a.query != "" && a.view == viewSky }},
		{"planche II", func(a *App) { a.key(code(term.Enter), now) }, func(a *App) bool { return a.view == viewPlate }},
		{"journal", func(a *App) { a.key(code(term.Tab), now) }, func(a *App) bool { return a.view == viewJournal }},
		{"légende", func(a *App) { a.key(term.R('?'), now) }, func(a *App) bool { return a.view == viewLegend }},
		{"confirmation", func(a *App) { a.plate, a.view = appSite, viewPlate; a.key(term.R('a'), now) }, func(a *App) bool { return a.confirm != nil }},
	}
	for _, m := range modes {
		for _, k := range []term.Key{term.R('q'), code(term.CtrlC)} {
			a, src := newTestApp(t, 80, 24)
			m.set(a)
			if !m.ok(a) {
				t.Fatalf("%s : mode non atteint", m.name)
			}
			a.key(k, now)
			if !a.quit {
				t.Errorf("%s : %v ne quitte pas", m.name, k)
			}
			if len(src.sent) != 0 {
				t.Errorf("%s : quitter a envoyé %+v", m.name, src.sent)
			}
		}
	}
}

// L'ouverture : la planche entière, puis le rideau qui monte, fini à 1 s.
func TestIntroCurtain(t *testing.T) {
	H := 24
	prev := H
	for ms := 0; ms <= 1100; ms += 20 {
		e := introEdge(time.Duration(ms)*time.Millisecond, H)
		if e > prev || e < 0 || e > H {
			t.Fatalf("%d ms : rideau à %d après %d", ms, e, prev)
		}
		prev = e
	}
	if introEdge(0, H) != H || introEdge(introDur, H) != 0 {
		t.Fatal("début ou fin du rideau")
	}
	// Une touche abrège l'ouverture et sert quand même.
	a, _ := newTestApp(t, 80, 24)
	a.introOver = false
	a.key(code(term.Tab), t0.Add(100*time.Millisecond))
	if !a.introOver || a.view != viewJournal {
		t.Fatalf("touche pendant l'ouverture : introOver=%v vue=%d", a.introOver, a.view)
	}
}

// Une ligne du catalogue garde ses morceaux entiers : quand la place manque,
// elle en omet, par ordre d'importance, plutôt que de les couper.
func TestLineKeepsWholePieces(t *testing.T) {
	ln := itemLine{sign: '✧', name: seg{"clepsydre", ink, 0}, detail: []seg{{":5173", inkDim, 0}},
		path: "~/Global/Projets/Clepsydre", right: []seg{{"en marche", mint, 0}, {" · port partagé", gold, 0}}}
	for w := 14; w <= 90; w++ {
		cv := canvas.New(w, 1)
		drawLine(cv, 0, 0, w, ln, false, 0)
		row := strings.TrimRight(cv.Row(0), " ")
		if canvas.StringWidth(row) > w {
			t.Fatalf("%d colonnes : déborde : %q", w, row)
		}
		if !strings.HasPrefix(row, "✧ clepsydre") && w >= 24 {
			t.Errorf("%d colonnes : nom coupé : %q", w, row)
		}
		if !strings.HasSuffix(row, "en marche") && !strings.HasSuffix(row, "en marche · port partagé") && w >= 24 {
			t.Errorf("%d colonnes : état absent ou coupé : %q", w, row)
		}
		if strings.Contains(row, ":51") != strings.Contains(row, ":5173") ||
			strings.Contains(row, "port") != strings.Contains(row, "port partagé") ||
			strings.Contains(row, "Clepsy") != strings.Contains(row, "Clepsydre ") {
			t.Errorf("%d colonnes : morceau coupé : %q", w, row)
		}
		// Le premier détail passe avant la mention de droite et le chemin.
		if (strings.Contains(row, "port partagé") || strings.Contains(row, "Clepsydre ")) && !strings.Contains(row, ":5173") {
			t.Errorf("%d colonnes : ordre d'importance : %q", w, row)
		}
	}
}

// Sur un écran large, la planche II est une vue à la lunette : le fond hors
// du champ reste celui de la page, l'objet est marqué au centre, et un trait
// de rappel mène du centre à son nom ; le texte ne déborde jamais.
func TestWidePlate(t *testing.T) {
	for _, size := range [][2]int{{110, 30}, {120, 36}, {160, 45}} {
		a, _ := newTestApp(t, size[0], size[1])
		now := t0.Add(5 * time.Second)
		for _, mv := range a.w.machines {
			for _, it := range mv.items {
				a.plate, a.view = it.id, viewPlate
				rows := rowsOf(t, a, now)
				cv := a.frame(now)
				name, _ := plateName(it)
				nameY := -1
				for y, r := range rows {
					if strings.Contains(r, name) && strings.Contains(r, "┈") {
						nameY = y
						break
					}
				}
				bottom := size[1] - 2 // la marge de papier : 2 rangées, 3 dès 32 lignes
				if size[1] >= 32 {
					bottom--
				}
				cx, cy := (min(bottom*2-6, size[0]*2/5)+4)/2, bottom/2
				if nameY < 0 {
					t.Errorf("%dx%d, %s : pas de trait de rappel jusqu'au nom %q", size[0], size[1], it.id, name)
					continue
				}
				if got := cv.At(cx, cy).R; got != a.lineFor(it, now).sign {
					t.Errorf("%dx%d, %s : %q au centre du champ au lieu du signe de l'objet", size[0], size[1], it.id, got)
				}
				for _, p := range [][2]int{{0, 0}, {0, bottom - 1}, {cx, 0}} {
					if c := cv.At(p[0], p[1]); c.BG != landBG || c.R != ' ' {
						t.Errorf("%dx%d, %s : la case %v hors du champ n'est pas le fond de la page", size[0], size[1], it.id, p)
					}
				}
			}
		}
	}
}

// La légende tient à toutes les tailles utilisables : chaque signe et chaque
// touche paraît en entier (les vignettes cèdent d'abord la place). À 40 × 12,
// elle ne déborde pas, sans promettre de tout montrer.
func TestLegendWhole(t *testing.T) {
	small, _ := newTestApp(t, 40, 12)
	small.view = viewLegend
	rowsOf(t, small, t0.Add(5*time.Second))
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {120, 36}, {130, 40}, {160, 45}} {
		a, _ := newTestApp(t, size[0], size[1])
		a.view = viewLegend
		all := strings.Join(rowsOf(t, a, t0.Add(5*time.Second)), "\n")
		for _, s := range legendSigns {
			if !strings.Contains(all, string(s.sign)+" "+s.text) {
				t.Errorf("légende à %d × %d : « %s %s » incomplet", size[0], size[1], string(s.sign), s.text)
			}
		}
		for _, k := range legendKeys {
			if !strings.Contains(all, k[1]) {
				t.Errorf("légende à %d × %d : touche « %s » absente", size[0], size[1], k[1])
			}
		}
	}
}

// Aucun agent : le titre le dit, sans rien inventer ; puis un agent arrive au
// travail, puis il attend.
func TestNoAgent(t *testing.T) {
	a := newApp(&fakeSrc{}, t0)
	a.resize(80, 24)
	l, v := laptopSnap(t0), vpsSnap(t0)
	l.Agents = []model.Agent{}
	a.event(source.Event{Machine: "laptop", Snapshot: &l}, t0)
	a.event(source.Event{Machine: "vps", Snapshot: &v}, t0)
	a.introOver = true
	now := t0.Add(5 * time.Second)

	all := strings.Join(rowsOf(t, a, now), "\n")
	if !strings.Contains(all, "AUCUN AGENT EN COURS.") || !strings.Contains(all, "aucun agent") {
		t.Errorf("sans agent : titre ou résumé absent\n%s", all)
	}
	for _, bad := range []string{"t’attend", "agents", "☄", "✦"} {
		if strings.Contains(all, bad) {
			t.Errorf("sans agent : « %s » ne devrait pas paraître\n%s", bad, all)
		}
	}

	ag := model.Agent{Key: "claude:301", Tool: "claude", PID: 301, CWD: "/home/alfred/p/nuage", State: model.StateWorking}
	a.event(patchEv("laptop", now, model.Change{Op: model.OpAdd, Kind: model.KindAgent, Key: ag.Key, Data: ag}), now)
	if all := strings.Join(rowsOf(t, a, now), "\n"); !strings.Contains(all, "AUCUN AGENT NE T’ATTEND.") {
		t.Errorf("un agent au travail : titre absent\n%s", all)
	}
	ag.State = model.StateWaiting
	a.event(patchEv("laptop", now, model.Change{Op: model.OpUpdate, Kind: model.KindAgent, Key: ag.Key, Data: ag}), now)
	if all := strings.Join(rowsOf(t, a, now), "\n"); !strings.Contains(all, "UN AGENT T’ATTEND SUR LAPTOP.") {
		t.Errorf("un agent qui attend : titre absent\n%s", all)
	}
}

// Un port écouté sur deux adresses (IPv4 et IPv6) par le même programme ne
// fait qu'une ligne ; sa planche nomme les deux adresses, les comptes de ports
// le comptent une fois, et le journal qui nomme la seconde écoute la retrouve.
func TestTwinListeners(t *testing.T) {
	a := newApp(&fakeSrc{}, t0)
	a.resize(160, 45)
	l, v := laptopSnap(t0), vpsSnap(t0)
	l.Listeners = append(l.Listeners,
		model.Listener{Key: "tcp4:127.0.0.1:631", Port: 631, Addr: "127.0.0.1", Exposure: "loopback", UID: 0},
		model.Listener{Key: "tcp6:[::1]:631", Port: 631, Addr: "::1", Exposure: "loopback", UID: 0})
	v.Listeners = append(v.Listeners, model.Listener{Key: "tcp6:[::]:3000", Port: 3000, Addr: "::", Exposure: "all",
		Mine: true, UID: 1000, PID: 301, Process: "node", CWD: "/home/alfred/p/site", ProjectRoot: "/home/alfred/p/site"})
	a.event(source.Event{Machine: "laptop", Snapshot: &l}, t0)
	a.event(source.Event{Machine: "vps", Snapshot: &v}, t0)
	a.introOver = true
	now := t0.Add(5 * time.Second)

	hits := a.w.search("631")
	if len(hits) != 1 || len(hits[0].twins) != 1 {
		t.Fatalf("631 : une seule ligne attendue, avec une jumelle : %v", ids(hits))
	}
	if a.w.byID[itemID("laptop", kindForeign, "tcp6:[::1]:631")] != hits[0] {
		t.Errorf("la seconde écoute de 631 ne mène pas à sa ligne")
	}
	a.plate, a.view = hits[0].id, viewPlate
	if all := strings.Join(rowsOf(t, a, now), "\n"); !strings.Contains(all, "631 sur 127.0.0.1 et ::1") {
		t.Errorf("planche de 631 : les deux adresses manquent\n%s", all)
	}

	for _, it := range a.w.search("3000") {
		if it.kind == kindPort {
			t.Errorf("3000 : l'écoute IPv6 de site fait une ligne à part : %s", it.id)
		}
	}
	a.plate = appSite
	if all := strings.Join(rowsOf(t, a, now), "\n"); !strings.Contains(all, "3000 sur 0.0.0.0 et ::") {
		t.Errorf("planche de site : les deux adresses manquent\n%s", all)
	}

	if n, open := portNumbers(&a.fleet.Machine("laptop").Snapshot); n != 5 || open != 1 {
		t.Errorf("laptop : %d ports dont %d ouverts à tous, attendu 5 et 1", n, open)
	}
	a.view = viewSky
	all := strings.Join(rowsOf(t, a, now), "\n")
	if !strings.Contains(all, "1 port ouvert à tous") || strings.Contains(all, "2 ports ouverts à tous") {
		t.Errorf("vps : 3000 en IPv4 et IPv6 compte pour un port\n%s", all)
	}
}

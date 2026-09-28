package ui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/canvas"
	"github.com/Alexry375/connex/internal/ui/term"
)

// view est l'écran affiché : une planche.
type view int

const (
	viewSky     view = iota // planche I : le ciel et le catalogue
	viewPlate               // planche II : un objet
	viewJournal             // planche III : le journal d'observation
	viewLegend              // planche IV : la légende
)

// sender est ce que l'interface demande à la source : envoyer une requête de la
// liste fermée (§4.3). Rien d'autre ne sort de l'interface.
type sender interface {
	Send(machine string, req model.Request) error
}

// request est une action demandée par l'utilisateur, en attente ou finie.
type request struct {
	id, machine, op, target, item string
	what                          string // « l’appli site », « le port 5000 »
	sent, at                      time.Time
	done, ok                      bool
	lost                          bool // close sans réponse : machine perdue ou source arrêtée
	reason                        string
}

// confirmation est une action qui attend un « oui ».
type confirmation struct {
	op, machine, target, item, what string
	detail                          string    // ce qui va se passer
	at                              time.Time // quand la question a été posée
}

// confirmArm : Entrée ne confirme qu'une fois la question posée depuis ce
// délai. Une Entrée tapée dans la même rafale que la touche d'action (« ma⏎ »)
// ne vaut pas oui.
const confirmArm = 400 * time.Millisecond

// armed dit si la question attend sa réponse depuis assez longtemps.
func (c *confirmation) armed(now time.Time) bool { return now.Sub(c.at) >= confirmArm }

// confirmTTL : une question restée sans réponse est retirée au bout de ce
// délai. Sur la planche II, Entrée est aussi la touche du retour : une question
// oubliée ne doit pas se valider au premier retour à l'écran.
const confirmTTL = 30 * time.Second

// expired dit si la question a trop attendu sa réponse.
func (c *confirmation) expired(now time.Time) bool { return now.Sub(c.at) >= confirmTTL }

// notice est un message passager, en bas de la planche.
type notice struct {
	text string
	tone tone
	at   time.Time
}

// App est l'état de l'interface. Toutes ses méthodes tournent dans la boucle
// principale : rien n'y est partagé entre fils d'exécution.
type App struct {
	src   sender
	open  func(machine, app string, snaps map[string]model.Snapshot) (string, error) // touche o ; nil : démo
	fleet *fleet.Fleet
	w     *world
	W, H  int

	opened    time.Time
	introOver bool

	view   view
	focus  int
	sel    map[string]string // machine → élément choisi
	selIdx map[string]int    // machine → rang de l'élément choisi (s'il disparaît)
	scroll map[string]int

	query   string
	typing  bool
	results []*item
	qsel    string
	qidx    int
	qscroll int

	plate string // élément montré sur la planche II

	confirm  *confirmation
	pending  map[string]*request
	finished []*request
	seq      int

	journal []entry
	jsel    int
	fresh   map[string]time.Time
	back    map[string]time.Time // machine → heure de son retour
	meteors []meteor
	notice  *notice
	flare   time.Time // dernier refus : les protubérances montent

	reasons  map[string]string
	lastTime time.Time // dernier horodatage reçu de la source
	closed   bool      // la source s'est arrêtée

	quit     bool
	pageRows int  // rangées du catalogue au dernier dessin (pour PgUp, PgDn)
	auto     bool // tant que l'utilisateur n'a touché à rien, le focus va à l'agent qui attend

	sc  *scene
	gfx gfxCache
	cv  *canvas.Canvas
}

// newApp prépare l'interface ; now marque le début de l'ouverture.
func newApp(src sender, now time.Time) *App {
	a := &App{
		src:     src,
		fleet:   fleet.New(),
		opened:  now,
		sel:     map[string]string{},
		selIdx:  map[string]int{},
		scroll:  map[string]int{},
		pending: map[string]*request{},
		fresh:   map[string]time.Time{},
		back:    map[string]time.Time{},
		reasons: map[string]string{},
		auto:    true,
	}
	a.w = buildWorld(a.fleet)
	return a
}

// resize change la taille de la toile.
func (a *App) resize(w, h int) {
	if w == a.W && h == a.H && a.cv != nil {
		return
	}
	a.W, a.H = w, h
	if a.tiny() {
		a.confirm = nil // la question ne s'y dessine pas : on ne la laisse pas en suspens
	}
	a.cv = canvas.New(w, h)
	a.sc = nil
	a.gfx = gfxCache{}
}

// --- événements de la source

// event intègre un événement de la source.
func (a *App) event(ev source.Event, now time.Time) {
	if ev.Reply != nil {
		a.reply(ev.Reply, now)
		return
	}
	var prev model.Snapshot
	known, wasReachable := false, true
	var since time.Time
	if m := a.fleet.Machine(ev.Machine); m != nil {
		prev, known, wasReachable, since = m.Snapshot, m.Known, m.Reachable, m.Since
	}
	a.noteTime(ev)
	changes := a.fleet.Apply(ev, now)
	m := a.fleet.Machine(ev.Machine)
	if m == nil {
		return
	}
	if ev.Status != nil && !ev.Status.Reachable {
		a.reasons[m.Name] = ev.Status.Reason
	}
	var prevPtr *model.Snapshot
	if known {
		prevPtr = &prev
	}
	home := homeOf(m.Snapshot.Machine.User)
	spawned := 0
	for _, c := range changes {
		var lost time.Duration
		if c.Kind == fleet.KindMachine && m.Reachable && !wasReachable {
			lost = now.Sub(since)
			if known {
				a.back[m.Name] = now
			}
		}
		e, ok := describe(c, prevPtr, &m.Snapshot, m, ev.Status, lost, home)
		if ok {
			a.addEntry(e)
		}
		if e.item != "" {
			a.fresh[e.item] = now
		}
		// Au plus deux météores par événement : un retour de machine peut
		// apporter des dizaines de changements d'un coup.
		if at, ok := changeUV(c, prevPtr, &m.Snapshot); ok && spawned < 2 && known {
			a.spawnMeteor(c, e, at, now)
			spawned++
		}
	}
	if len(changes) > 0 {
		a.rebuild()
		a.autoFocus(now)
		// Le halo va à l'objet qui montre l'écoute (une appli, un port jumeau).
		for k, t := range a.fresh {
			if it := a.w.byID[k]; it != nil && it.id != k && a.fresh[it.id].Before(t) {
				a.fresh[it.id] = t
			}
		}
	}
	if ev.Status != nil && !ev.Status.Reachable {
		a.abandon(m.Name, "la machine a cessé de répondre", now)
	}
	a.recheck(now)
	for k, t := range a.fresh {
		if now.Sub(t) >= haloLife {
			delete(a.fresh, k) // plus de halo : inutile de le retenir
		}
	}
}

// noteTime retient le dernier horodatage donné par la source.
func (a *App) noteTime(ev source.Event) {
	var ts string
	switch {
	case ev.Snapshot != nil:
		ts = ev.Snapshot.TakenAt
	case ev.Patch != nil:
		ts = ev.Patch.At
	case ev.Status != nil:
		ts = ev.Status.Since
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil && t.After(a.lastTime) {
		a.lastTime = t
	}
}

func (a *App) addEntry(e entry) {
	a.journal = append(a.journal, e)
	if len(a.journal) > maxJournal {
		a.journal = append([]entry{}, a.journal[len(a.journal)-maxJournal:]...)
	}
	if a.view == viewJournal && a.jsel > 0 {
		// La ligne regardée ne bouge pas sous les yeux ; la plus ancienne,
		// si elle tombe du journal plein, laisse la place à la dernière.
		a.jsel = min(a.jsel+1, len(a.journal)-1)
	}
}

// sourceClosed : la source ne dira plus rien. L'affichage reste ouvert et le dit.
func (a *App) sourceClosed(now time.Time) {
	if a.closed {
		return
	}
	a.closed = true
	a.addEntry(entry{at: now, text: "la source s’est arrêtée : plus aucune nouvelle ne viendra", tone: toneBad, sign: '◐'})
	a.abandon("", "la source s’est arrêtée", now)
	a.say("La source s’est arrêtée. L’état affiché est le dernier connu.", toneBad, now)
}

// abandon clôt « sans réponse » les requêtes en attente sur une machine (toutes
// si machine est vide) : la source ne répondra plus. Si la réponse vient quand
// même, elle est ignorée ; le relevé suivant dira l'état réel.
func (a *App) abandon(machine, why string, now time.Time) {
	var gone []*request
	for id, r := range a.pending {
		if machine == "" || r.machine == machine {
			gone = append(gone, r)
			delete(a.pending, id)
		}
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i].sent.Before(gone[j].sent) })
	for _, r := range gone {
		r.done, r.lost, r.reason, r.at = true, true, why, now
		a.finish(r, now)
	}
}

// rebuild recalcule le catalogue et garde les sélections sur leurs éléments.
func (a *App) rebuild() {
	a.w = buildWorld(a.fleet)
	a.sc = nil
	if a.query != "" {
		a.results = a.w.search(a.query)
	}
	if a.focus >= len(a.w.machines) {
		a.focus = max(0, len(a.w.machines)-1)
	}
}

// autoFocus : à l'ouverture, tant que l'utilisateur n'a touché à rien, la planche
// montre la machine où un agent attend, et cet agent.
func (a *App) autoFocus(now time.Time) {
	if !a.auto || now.Sub(a.opened) > 4*time.Second || a.query != "" {
		return
	}
	for i, mv := range a.w.machines {
		if mv.m.Reachable && len(mv.waiting) > 0 {
			a.focus = i
			it := mv.waiting[0]
			for j, x := range mv.items {
				if x == it {
					a.sel[mv.m.Name], a.selIdx[mv.m.Name] = it.id, j
				}
			}
			return
		}
	}
}

// --- réponses et actions

func (a *App) reply(r *model.Reply, now time.Time) {
	req := a.pending[r.ID]
	if req == nil {
		return // réponse à une requête qu'on n'a pas faite : ignorée
	}
	delete(a.pending, r.ID)
	req.done, req.ok, req.reason, req.at = true, r.OK, r.Error, now
	a.finish(req, now)
}

func (a *App) finish(req *request, now time.Time) {
	a.finished = append(a.finished, req)
	if len(a.finished) > 32 {
		a.finished = a.finished[len(a.finished)-32:]
	}
	e := entry{at: now, machine: req.machine, item: req.item}
	switch {
	case req.ok:
		e.text, e.tone, e.sign = opFR(req.op)+" "+req.what+" : "+opDoneFR(req.op), toneGood, '✓'
	case req.lost:
		e.text, e.tone, e.sign = opFR(req.op)+" "+req.what+" : sans réponse — "+req.reason, toneWarn, '◐'
	default:
		e.text, e.tone, e.sign = opFR(req.op)+" "+req.what+" : refusé — "+orUnknown(req.reason), toneBad, '✕'
		a.flare = now
	}
	a.addEntry(e)
	if req.item != "" {
		a.fresh[req.item] = now
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "sans raison donnée"
	}
	return s
}

// ask prépare une action sur un élément. Toutes attendent un oui, sauf
// actualiser, qui ne change rien sur la machine et part aussitôt. Une action
// que la marge ne propose pas n'est pas posée : l'écran dit pourquoi.
func (a *App) ask(op string, it *item, now time.Time) {
	if op == model.ReqRefresh {
		mv := a.focusMachine()
		if it != nil {
			mv = a.w.machine(it.m.Name)
		}
		if mv == nil {
			return
		}
		// Le relevé concerne la machine, pas l'objet choisi : il se suit sous
		// le ciel de la machine et sur la planche de ses objets.
		a.send(op, mv.m.Name, "", machineKey(mv.m.Name), "le relevé de "+mv.m.Name, now)
		return
	}
	c, why := a.question(op, it)
	if c == nil {
		a.say(upperFirst(why)+".", toneNeutral, now)
		return
	}
	c.at = now
	a.confirm = c
}

// question prépare, d'après le relevé courant, la question d'une action sur
// un élément. Si l'action n'a pas lieu d'être (objet disparu, machine qui ne
// répond plus, action que la marge ne propose pas), elle renvoie nil et dit
// pourquoi.
func (a *App) question(op string, it *item) (*confirmation, string) {
	switch {
	case it == nil:
		return nil, "cet objet n’est plus au relevé"
	case !it.m.Reachable:
		return nil, "la machine " + it.m.Name + " ne répond plus"
	case !offers(it, op):
		return nil, whyNot(op, it)
	}
	c := &confirmation{op: op, machine: it.m.Name, item: it.id}
	if op == model.ReqFreePort {
		p := itemPort(it)
		c.target, c.what, c.detail = strconv.Itoa(p), "le port "+strconv.Itoa(p), freeDetail(it, p)
	} else {
		c.target, c.what, c.detail = it.app.Name, "l’appli "+it.app.Name, unitDetail(op, it.app.Unit)
	}
	return c, ""
}

// recheck retire la question posée si le relevé ne la confirme plus : objet
// disparu ou changé (un autre programme sur le port), machine qui ne répond
// plus, action devenue sans objet. Entrée enverrait sinon une action que
// l'écran ne décrit plus.
func (a *App) recheck(now time.Time) {
	c := a.confirm
	if c == nil {
		return
	}
	nc, why := a.question(c.op, a.w.byID[c.item])
	switch {
	case nc == nil:
	case nc.machine != c.machine || nc.target != c.target || nc.what != c.what || nc.detail != c.detail:
		why = "l’objet a changé depuis"
	default:
		return
	}
	a.confirm = nil
	a.say("Question retirée : "+why+".", toneBad, now)
}

// expire retire une question restée trop longtemps sans réponse.
func (a *App) expire(now time.Time) {
	if c := a.confirm; c != nil && c.expired(now) {
		a.confirm = nil
		a.say("Question retirée au bout de "+durFR(confirmTTL)+" : rien n’a été fait.", toneNeutral, now)
	}
	if a.notice != nil && !a.noticeLive(now) {
		a.notice = nil // la marge reprend sa hauteur
	}
}

// action est une action que la marge propose sur un objet.
type action struct {
	op   string
	hint hint
}

// objectActions renvoie les actions possibles sur un objet, les plus probables
// d'abord. Aucune sur une machine qui ne répond plus : la requête n'y
// arriverait pas, et l'état affiché n'est que le dernier connu.
func objectActions(it *item) []action {
	if it == nil || !it.m.Reachable {
		return nil
	}
	var as []action
	switch it.kind {
	case kindApp:
		switch it.app.State {
		case model.AppRunning, model.AppStarting:
			as = append(as, action{model.ReqStop, hint{"a", "arrêter", 2}}, action{model.ReqRestart, hint{"r", "relancer", 2}})
		default:
			as = append(as, action{model.ReqStart, hint{"d", "démarrer", 2}})
			if it.app.State == model.AppFailed {
				as = append(as, action{model.ReqRestart, hint{"r", "relancer", 3}})
			}
		}
		if itemPort(it) > 0 && (it.appPort != nil || it.holder != nil) {
			as = append(as, action{model.ReqFreePort, hint{"l", "libérer le port", 4}})
		}
	case kindPort, kindForeign:
		as = append(as, action{model.ReqFreePort, hint{"l", "libérer le port", 2}})
	}
	return as
}

// offers dit si la marge propose l'action op sur l'objet.
func offers(it *item, op string) bool {
	for _, x := range objectActions(it) {
		if x.op == op {
			return true
		}
	}
	return false
}

// whyNot dit pourquoi la marge ne propose pas l'action op sur l'objet, qui
// est sur une machine joignable.
func whyNot(op string, it *item) string {
	if op == model.ReqFreePort {
		if p := itemPort(it); p > 0 {
			return "rien n’écoute sur le port " + strconv.Itoa(p)
		}
		return "rien à libérer ici : choisis un port, ou une appli qui en a un"
	}
	if it.kind != kindApp {
		return "seule une appli inscrite se démarre, s’arrête ou se relance"
	}
	name := "l’appli " + it.app.Name
	switch it.app.State {
	case model.AppRunning:
		return name + " est déjà en marche"
	case model.AppStarting:
		return name + " démarre déjà"
	case model.AppStopped, model.AppFailed:
		return name + " ne tourne pas"
	}
	return name + " est dans un état inconnu"
}

// upperFirst met une majuscule au début d'une phrase.
func upperFirst(s string) string {
	rs := []rune(s)
	if len(rs) == 0 {
		return s
	}
	return strings.ToUpper(string(rs[:1])) + string(rs[1:])
}

// machineKey est la clé des requêtes qui visent une machine entière.
func machineKey(name string) string { return "\x00machine:" + name }

// unitDetail dit ce que l'action va faire de l'unité de l'appli.
func unitDetail(op, unit string) string {
	if unit == "" {
		return ""
	}
	switch op {
	case model.ReqStart:
		return "Son unité " + unit + " sera démarrée."
	case model.ReqStop:
		return "Son unité " + unit + " sera arrêtée."
	}
	return "Elle sera arrêtée puis relancée par son unité " + unit + "."
}

// freeDetail dit ce que libérer le port p va arrêter.
func freeDetail(it *item, p int) string {
	var l *model.Listener
	switch {
	case it.port != nil:
		l = it.port
	case it.appPort != nil:
		l = it.appPort
	case it.holder != nil:
		l = it.holder
	}
	if l == nil {
		return "Rien n’y écoute au dernier relevé."
	}
	if !l.Mine {
		return "Il est tenu par un autre compte (uid " + strconv.Itoa(l.UID) + ")."
	}
	return l.Program() + " (pid " + strconv.Itoa(l.PID) + ") sera arrêté."
}

// send envoie la requête à la source.
func (a *App) send(op, machine, target, itemKey, what string, now time.Time) {
	a.seq++
	id := "connex-ui-" + strconv.Itoa(a.seq)
	req := &request{id: id, machine: machine, op: op, target: target, item: itemKey, what: what, sent: now}
	if err := a.src.Send(machine, source.NewRequest(id, op, target)); err != nil {
		req.done, req.ok, req.reason, req.at = true, false, err.Error(), now
		a.finish(req, now)
		return
	}
	a.pending[id] = req
}

// say affiche un message passager, jusqu'à la touche suivante ou noticeFor.
func (a *App) say(text string, t tone, now time.Time) {
	a.notice = &notice{text: text, tone: t, at: now}
}

const noticeFor = 8 * time.Second

func (a *App) noticeLive(now time.Time) bool {
	return a.notice != nil && now.Sub(a.notice.at) < noticeFor
}

// requestFor renvoie la dernière requête concernant l'élément : en cours, ou finie
// depuis moins de d.
func (a *App) requestFor(itemKey string, now time.Time, d time.Duration) *request {
	if itemKey == "" {
		return nil
	}
	var best *request
	for _, r := range a.pending {
		if r.item == itemKey && (best == nil || r.sent.After(best.sent)) {
			best = r
		}
	}
	if best != nil {
		return best
	}
	for i := len(a.finished) - 1; i >= 0; i-- {
		r := a.finished[i]
		if r.item == itemKey && now.Sub(r.at) < d {
			return r
		}
	}
	return nil
}

// --- clavier

// key traite une touche.
func (a *App) key(k term.Key, now time.Time) {
	// q et Ctrl-C quittent à tout moment, même pendant une recherche : Q
	// cherche un q.
	if k.Code == term.CtrlC || (k.Code == term.Rune && k.Rune == 'q') {
		a.quit = true
		return
	}
	// Dans une fenêtre trop petite, rien ne se voit : aucune autre touche n'agit.
	if a.tiny() {
		return
	}
	if !a.introOver {
		a.introOver = true // une touche abrège l'ouverture, et sert quand même
	}
	a.auto = false
	if k.Code == term.CtrlL {
		a.sc = nil
		a.gfx = gfxCache{}
		return
	}
	if c := a.confirm; c != nil && c.expired(now) {
		// La touche répondait à une question qui n'est plus posée : elle ne
		// vaut ni oui ni autre chose.
		a.expire(now)
		return
	}
	if c := a.confirm; c != nil {
		// Seule Entrée confirme, et seulement une fois la question armée : une
		// Entrée plus tôt est ignorée. Toute autre touche annule.
		if k.Code == term.Enter {
			if c.armed(now) {
				a.confirm = nil
				a.send(c.op, c.machine, c.target, c.item, c.what, now)
			}
			return
		}
		a.confirm = nil
		a.say("Rien n’a été fait.", toneNeutral, now)
		return
	}
	a.notice = nil
	if a.typing {
		a.typeKey(k, now)
		return
	}
	switch a.view {
	case viewPlate:
		a.plateKey(k, now)
	case viewJournal:
		a.journalKey(k, now)
	case viewLegend:
		a.legendKey(k, now)
	default:
		a.skyKey(k, now)
	}
}

// tiny dit si la fenêtre est trop petite pour une planche.
func (a *App) tiny() bool { return a.W < minW || a.H < minH }

func (a *App) typeKey(k term.Key, now time.Time) {
	switch k.Code {
	case term.Rune:
		a.setQuery(a.query + string(k.Rune))
	case term.Backspace:
		if rs := []rune(a.query); len(rs) > 0 {
			a.setQuery(string(rs[:len(rs)-1]))
		}
	case term.CtrlU:
		a.setQuery("")
	case term.CtrlW:
		rs := []rune(a.query)
		i := len(rs)
		for i > 0 && rs[i-1] == ' ' {
			i--
		}
		for i > 0 && rs[i-1] != ' ' {
			i--
		}
		a.setQuery(string(rs[:i]))
	case term.Esc:
		a.typing = false
		a.setQuery("")
	case term.Enter:
		// Entrée ouvre la planche du résultat choisi, où sont ses actions ;
		// Échap, depuis la planche, revient à la liste.
		a.typing = false
		if it := a.selected(); it != nil && a.query != "" {
			a.plate = it.id
			a.view = viewPlate
		}
	case term.Up, term.Down, term.PageUp, term.PageDown:
		a.move(k.Code)
	}
}

func (a *App) setQuery(q string) {
	a.query = q
	a.results = a.w.search(q)
	a.qsel, a.qidx, a.qscroll = "", 0, 0
	if len(a.results) > 0 {
		a.qsel = a.results[0].id
	}
}

// skyKey : sur la planche I, taper cherche. Les actions sont sur la planche
// de l'objet (Entrée), où l'on voit sur quoi elles portent.
func (a *App) skyKey(k term.Key, now time.Time) {
	switch k.Code {
	case term.Up, term.Down, term.PageUp, term.PageDown, term.Home, term.End:
		a.move(k.Code)
	case term.Left:
		if a.query == "" {
			a.shiftFocus(-1)
		}
	case term.Right:
		if a.query == "" {
			a.shiftFocus(1)
		}
	case term.Tab:
		a.openJournal()
	case term.Enter:
		if it := a.selected(); it != nil {
			a.plate = it.id
			a.view = viewPlate
		}
	case term.Esc:
		if a.query != "" {
			a.setQuery("")
		}
	case term.Backspace:
		if a.query != "" {
			a.typing = true
			a.typeKey(k, now)
		}
	case term.F5:
		a.ask(model.ReqRefresh, a.selected(), now)
	case term.Rune:
		switch r := k.Rune; {
		case r == '?':
			a.view = viewLegend
		case r == '/':
			a.typing = true
		case r != ' ':
			a.typing = true
			a.setQuery(string(r))
		}
	}
}

// otherRune : sur les planches II à IV, les lettres sont des commandes. « / »
// ou un chiffre y commencent une recherche, sur la planche I ; o dit ce qu'il
// fait ; une autre lettre dit comment chercher.
func (a *App) otherRune(r rune, now time.Time) {
	switch {
	case r == 'o':
		a.say(openWhat, toneNeutral, now)
	case r == '/':
		a.view, a.typing = viewSky, true
		a.setQuery("")
	case r >= '0' && r <= '9':
		a.view, a.typing = viewSky, true
		a.setQuery(string(r))
	case r != ' ':
		a.say("Pour chercher, tape / puis le mot.", toneNeutral, now)
	}
}

// openWhat dit, en une ligne, ce que fait o hors de la planche d'une appli.
const openWhat = "o ouvre une appli dans le navigateur, depuis sa planche."

// openApp ouvre l'appli de la planche par a.open, avec le dernier relevé de
// chaque machine connue. L'écran montre l'adresse ouverte, que le journal
// note, ou la raison du refus, en entier. Sans fonction d'ouverture (la démo),
// rien ne s'ouvre et l'écran le dit.
func (a *App) openApp(it *item, now time.Time) {
	if it == nil || it.kind != kindApp {
		a.say(openWhat, toneNeutral, now)
		return
	}
	name, machine := it.app.Name, it.m.Name
	if a.open == nil {
		a.say("Ici, la démo n’ouvre rien : hors démo, o ouvre l’appli "+name+" dans le navigateur du laptop.", toneNeutral, now)
		return
	}
	snaps := map[string]model.Snapshot{}
	for _, m := range a.fleet.Machines() {
		if m.Known {
			snaps[m.Name] = m.Snapshot
		}
	}
	url, err := a.open(machine, name, snaps)
	if err != nil {
		// La raison est lue en entier, sur les rangées de la marge : ses
		// retours à la ligne n'en sont pas.
		a.say("L’appli "+name+" ne s’ouvre pas : "+sentence(strings.Join(strings.Fields(err.Error()), " ")), toneBad, now)
		return
	}
	a.say("Ouvert dans le navigateur : "+url, toneGood, now)
	a.addEntry(entry{at: now, machine: machine, item: it.id, text: "ouvrir l’appli " + name + " : " + url, tone: toneGood, sign: '✓'})
}

// sentence finit une raison par un point, si elle n'en a pas.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "raison inconnue."
	}
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "…") {
		return s
	}
	return s + "."
}

func (a *App) openJournal() {
	a.view = viewJournal
	a.jsel = 0
}

func (a *App) plateKey(k term.Key, now time.Time) {
	it := a.w.byID[a.plate]
	switch k.Code {
	case term.Esc, term.Left, term.Backspace, term.Enter:
		a.view = viewSky
	case term.Up, term.Down:
		// On feuillette les planches dans l'ordre du catalogue.
		a.move(k.Code)
		if s := a.selected(); s != nil {
			a.plate = s.id
		}
	case term.Tab:
		a.openJournal()
	case term.F5:
		a.ask(model.ReqRefresh, it, now)
	case term.Rune:
		switch k.Rune {
		case 'd':
			a.ask(model.ReqStart, it, now)
		case 'a':
			a.ask(model.ReqStop, it, now)
		case 'r':
			a.ask(model.ReqRestart, it, now)
		case 'l':
			a.ask(model.ReqFreePort, it, now)
		case 'o':
			a.openApp(it, now)
		case 'j':
			a.openJournal()
		case '?':
			a.view = viewLegend
		default:
			a.otherRune(k.Rune, now)
		}
	}
}

func (a *App) journalKey(k term.Key, now time.Time) {
	n := len(a.journal)
	switch k.Code {
	case term.Up:
		a.jsel = max(0, a.jsel-1)
	case term.Down:
		a.jsel = min(max(0, n-1), a.jsel+1)
	case term.PageUp:
		a.jsel = max(0, a.jsel-10)
	case term.PageDown:
		a.jsel = min(max(0, n-1), a.jsel+10)
	case term.Home:
		a.jsel = 0
	case term.End:
		a.jsel = max(0, n-1)
	case term.Enter:
		if n > 0 {
			a.openEntry(a.journal[n-1-a.jsel], now)
		}
	case term.Esc, term.Left, term.Backspace, term.Tab:
		a.view = viewSky
	case term.Rune:
		switch k.Rune {
		case 'j':
			a.view = viewSky
		case '?':
			a.view = viewLegend
		default:
			a.otherRune(k.Rune, now)
		}
	}
}

// openEntry va à l'objet d'une nouvelle : sa planche s'il est encore au
// relevé ; pour une nouvelle de machine, la planche I sur cette machine ;
// sinon l'écran dit pourquoi Entrée ne mène nulle part.
func (a *App) openEntry(e entry, now time.Time) {
	if it := a.w.byID[e.item]; it != nil {
		a.plate = it.id
		a.view = viewPlate
		a.selectItem(it)
		return
	}
	if e.machine != "" && (e.item == "" || e.item == machineKey(e.machine)) {
		for i, mv := range a.w.machines {
			if mv.m.Name == e.machine {
				a.focus = i
				a.view = viewSky
				return
			}
		}
	}
	a.say("Cet objet n’est plus au relevé.", toneNeutral, now)
}

func (a *App) legendKey(k term.Key, now time.Time) {
	switch {
	case k.Code == term.Tab, k.Code == term.Rune && k.Rune == 'j':
		a.openJournal()
	case k.Code == term.Esc, k.Code == term.Left, k.Code == term.Enter, k.Code == term.Backspace,
		k.Code == term.Rune && k.Rune == '?':
		a.view = viewSky
	case k.Code == term.Rune:
		a.otherRune(k.Rune, now)
	}
}

// --- sélection

// focusMachine renvoie la machine en focus.
func (a *App) focusMachine() *machineView {
	if len(a.w.machines) == 0 {
		return nil
	}
	return a.w.machines[min(a.focus, len(a.w.machines)-1)]
}

func (a *App) shiftFocus(d int) {
	n := len(a.w.machines)
	if n == 0 {
		return
	}
	a.focus = (a.focus + d + n) % n
}

// list renvoie la liste parcourue : les résultats d'une recherche, ou le
// catalogue de la machine en focus.
func (a *App) list() []*item {
	if a.query != "" {
		return a.results
	}
	if mv := a.focusMachine(); mv != nil {
		return mv.items
	}
	return nil
}

// selected renvoie l'élément choisi. S'il a disparu, son voisin prend sa place.
func (a *App) selected() *item {
	items := a.list()
	if len(items) == 0 {
		return nil
	}
	id, idx := a.cursor()
	for i, it := range items {
		if it.id == id {
			a.setCursor(it.id, i)
			return it
		}
	}
	idx = min(max(idx, 0), len(items)-1)
	a.setCursor(items[idx].id, idx)
	return items[idx]
}

func (a *App) cursor() (string, int) {
	if a.query != "" {
		return a.qsel, a.qidx
	}
	mv := a.focusMachine()
	if mv == nil {
		return "", 0
	}
	return a.sel[mv.m.Name], a.selIdx[mv.m.Name]
}

func (a *App) setCursor(id string, idx int) {
	if a.query != "" {
		a.qsel, a.qidx = id, idx
		return
	}
	if mv := a.focusMachine(); mv != nil {
		a.sel[mv.m.Name], a.selIdx[mv.m.Name] = id, idx
	}
}

// selectItem choisit un élément, en passant sur sa machine.
func (a *App) selectItem(it *item) {
	if a.query != "" {
		for i, r := range a.results {
			if r.id == it.id {
				a.qsel, a.qidx = it.id, i
				return
			}
		}
		a.setQuery("")
	}
	for i, mv := range a.w.machines {
		if mv.m == it.m {
			a.focus = i
			for j, x := range mv.items {
				if x.id == it.id {
					a.sel[mv.m.Name], a.selIdx[mv.m.Name] = it.id, j
				}
			}
		}
	}
}

// move déplace la sélection.
func (a *App) move(code term.Code) {
	items := a.list()
	if len(items) == 0 {
		return
	}
	cur := a.selected()
	i := 0
	for j, it := range items {
		if it == cur {
			i = j
		}
	}
	page := max(1, a.pageRows-2)
	switch code {
	case term.Up:
		i--
	case term.Down:
		i++
	case term.PageUp:
		i -= page
	case term.PageDown:
		i += page
	case term.Home:
		i = 0
	case term.End:
		i = len(items) - 1
	}
	i = min(max(i, 0), len(items)-1)
	a.setCursor(items[i].id, i)
}

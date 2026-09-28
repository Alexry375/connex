package ui

import (
	"strconv"
	"time"

	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/source"
	"github.com/Alexry375/connex/internal/ui/canvas"
	"github.com/Alexry375/connex/internal/ui/term"
)

// mode est l'écran affiché.
type mode int

const (
	modeMain    mode = iota // le retable : soleil, astres, échelle
	modeDetail              // le cartouche d'un élément
	modeConfirm             // une question avant d'agir
	modeHelp                // la légende
	modeChron               // la chronique entière
)

// Durées du mouvement.
const (
	openingSpan = 950 * time.Millisecond  // le retable s'ouvre
	eclipseSpan = 1400 * time.Millisecond // un astre s'éteint ou se rallume
	noteTTL     = 4 * time.Second         // remarque passagère en bas
	answerTTL   = 8 * time.Second         // réponse à une action, en bas
)

// question est ce qu'on demande avant d'envoyer une requête qui arrête
// quelque chose.
type question struct {
	op      string
	machine string
	target  string
	item    itemID
	title   string   // « Arrêter site sur alfred-laptop ? »
	facts   []string // ce qu'on sait de la cible
	yes     string   // « arrêter »
}

// app est l'état de l'affichage : le monde, et ce que l'utilisateur regarde.
type app struct {
	src  source.Source
	w    *world
	born time.Time
	now  time.Time
	W, H int

	mode mode
	back mode // l'écran d'où vient la question

	all   []entry
	view  []entry
	built int // version du monde qui a produit all ; -1 : à refaire

	sel    itemID
	selIdx int
	top    int // première ligne visible de l'échelle
	rows   int // lignes visibles de l'échelle au dernier dessin

	focus  string // machine regardée seule ; vide : toutes
	query  string
	typing bool

	ask      *question
	chronTop int
	helpTop  int  // première ligne visible de la légende
	helpOver bool // la légende dépasse l'écran : ↑↓ la font défiler

	note    string
	noteAt  time.Time
	noteFor time.Duration
	noteCol canvas.Color
	answers int // réponses déjà annoncées en bas de l'écran

	opening bool
	curtain *curtain             // le tableau de l'ouverture, à la taille de l'écran
	flips   map[string]time.Time // machine → dernier changement de joignabilité vu en direct
	quit    bool
}

func newApp(src source.Source, now time.Time) *app {
	return &app{
		src:     src,
		w:       newWorld(),
		born:    now,
		now:     now,
		built:   -1,
		opening: true,
		flips:   map[string]time.Time{},
	}
}

// event intègre un événement de la source.
func (a *app) event(ev source.Event, now time.Time) {
	a.now = now
	was, known := true, false
	if m := a.w.fleet.Machine(ev.Machine); m != nil {
		was, known = m.Reachable, true
	}
	a.w.apply(ev, now)
	if m := a.w.fleet.Machine(ev.Machine); m != nil && known && m.Reachable != was {
		a.flips[m.Name] = now
	}
}

// tick fait vieillir ce qui doit vieillir.
func (a *app) tick(now time.Time) {
	a.now = now
	a.w.prune(now)
	if a.opening && now.Sub(a.born) >= openingSpan {
		a.opening = false
	}
	if a.w.answers != a.answers {
		// la réponse à une action se lit en entier, raison du refus comprise
		a.answers = a.w.answers
		col := cSage
		if !a.w.last.ok {
			col = cRed
		}
		a.note, a.noteAt, a.noteFor, a.noteCol = outcomeNote(a.w.last), now, answerTTL, col
	}
	if a.note != "" && now.Sub(a.noteAt) > a.noteFor {
		a.note = ""
	}
}

// say affiche une remarque passagère dans la ligne du bas.
func (a *app) say(s string, bad bool) {
	col := cInkSoft
	if bad {
		col = cRed
	}
	a.note, a.noteAt, a.noteFor, a.noteCol = s, a.now, noteTTL, col
}

// outcomeNote dit une réponse en une phrase : ce qui a été fait, ou le refus
// et sa raison.
func outcomeNote(o outcome) string {
	s := actionPhrase(o.op, o.target, o.machine)
	if o.op != model.ReqRefresh {
		s += " sur " + o.machine
	}
	if o.ok {
		return "✓ " + s + " : fait en " + seconds(o.done.Sub(o.at))
	}
	s = "✕ " + s + " : refusé"
	if o.err != "" {
		s += " — " + o.err
	}
	return s
}

// machines renvoie les machines dans l'ordre d'apparition.
func (a *app) machines() []*fleet.Machine { return a.w.fleet.Machines() }

// oneMachine dit si connex ne regarde qu'une machine : son nom, écrit en haut
// à droite, n'a pas à se répéter sur chaque ligne.
func (a *app) oneMachine() bool { return len(a.machines()) == 1 }

// order renvoie le rang d'une machine, -1 si inconnue.
func (a *app) order(name string) int {
	for i, m := range a.machines() {
		if m.Name == name {
			return i
		}
	}
	return -1
}

// sync reconstruit l'échelle si le monde a changé et garde la sélection sur le
// même élément ; s'il a disparu pour de bon, sur la ligne à la même place.
func (a *app) sync() {
	if a.built != a.w.version {
		a.all = buildEntries(a.w)
		a.built = a.w.version
	}
	a.view = filterEntries(a.all, a.focus, a.query)
	if len(a.view) == 0 {
		a.selIdx = 0
		return
	}
	for i := range a.view {
		if a.view[i].id == a.sel {
			a.selIdx = i
			return
		}
	}
	a.selIdx = min(max(a.selIdx, 0), len(a.view)-1)
	a.sel = a.view[a.selIdx].id
}

// invalidate force la reconstruction de l'échelle au prochain sync.
func (a *app) invalidate() { a.built = -1 }

// selected renvoie l'entrée choisie, ou nil.
func (a *app) selected() *entry {
	if a.selIdx < 0 || a.selIdx >= len(a.view) {
		return nil
	}
	return &a.view[a.selIdx]
}

func (a *app) choose(i int) {
	if len(a.view) == 0 {
		return
	}
	a.selIdx = min(max(i, 0), len(a.view)-1)
	a.sel = a.view[a.selIdx].id
}

// line est une ligne de l'échelle : un titre de marche ou une entrée.
type line struct {
	header bool
	st     stratum
	count  int
	idx    int // entrée dans a.view
}

// lines met l'échelle filtrée en lignes : chaque marche commence par son titre.
func (a *app) lines() []line {
	var out []line
	for i := 0; i < len(a.view); {
		st := a.view[i].stratum
		j := i
		for j < len(a.view) && a.view[j].stratum == st {
			j++
		}
		out = append(out, line{header: true, st: st, count: j - i, idx: i})
		for k := i; k < j; k++ {
			out = append(out, line{st: st, idx: k})
		}
		i = j
	}
	return out
}

// scrollTo ajuste la première ligne visible pour montrer la sélection, avec le
// titre de sa marche quand elle en est la première entrée.
func (a *app) scrollTo(ls []line, rows int) {
	if rows <= 0 {
		return
	}
	pos := -1
	for i, l := range ls {
		if !l.header && l.idx == a.selIdx {
			pos = i
			break
		}
	}
	if pos < 0 {
		a.top = 0
		return
	}
	want := pos
	if pos > 0 && ls[pos-1].header {
		want = pos - 1
	}
	margin := 1
	if rows < 6 {
		margin = 0
	}
	if want-margin < a.top {
		a.top = max(want-margin, 0)
		if pos > 0 && ls[pos-1].header && a.top > pos-1 {
			a.top = pos - 1
		}
	}
	if pos+margin >= a.top+rows {
		a.top = pos + margin - rows + 1
	}
	a.top = min(max(a.top, 0), max(len(ls)-rows, 0))
}

// key traite une touche.
func (a *app) key(k term.Key) {
	if k.Code == term.KeyCtrlC || (k.Code == term.KeyRune && k.Rune == 'q') {
		a.quit = true
		return
	}
	if a.opening {
		a.opening = false
		// Entrée ou espace abrègent seulement ; les autres touches agissent aussi.
		if k.Code == term.KeyEnter || (k.Code == term.KeyRune && k.Rune == ' ') {
			return
		}
	}
	a.sync()
	switch a.mode {
	case modeConfirm:
		a.keyConfirm(k)
	case modeHelp:
		a.keyHelp(k)
	case modeChron:
		a.keyChron(k)
	case modeDetail:
		a.keyDetail(k)
	default:
		if a.typing {
			a.keySearch(k)
			return
		}
		a.keyMain(k)
	}
}

func (a *app) keyMain(k term.Key) {
	switch k.Code {
	case term.KeyUp:
		a.choose(a.selIdx - 1)
	case term.KeyDown:
		a.choose(a.selIdx + 1)
	case term.KeyPgUp:
		a.choose(a.selIdx - max(a.rows-2, 1))
	case term.KeyPgDn:
		a.choose(a.selIdx + max(a.rows-2, 1))
	case term.KeyHome:
		a.choose(0)
	case term.KeyEnd:
		a.choose(len(a.view) - 1)
	case term.KeyTab:
		a.jumpStratum(1)
	case term.KeyBacktab:
		a.jumpStratum(-1)
	case term.KeyLeft:
		a.cycleFocus(-1)
	case term.KeyRight:
		a.cycleFocus(1)
	case term.KeyEnter:
		if a.selected() != nil {
			a.mode = modeDetail
		}
	case term.KeyEsc:
		switch {
		case a.query != "":
			a.query = ""
		case a.focus != "":
			a.focus = ""
		}
	case term.KeyCtrlR:
		a.refresh()
	case term.KeyRune:
		a.runeMain(k.Rune)
	}
}

func (a *app) runeMain(r rune) {
	switch r {
	case 'k':
		a.choose(a.selIdx - 1)
	case 'j':
		a.choose(a.selIdx + 1)
	case 'g':
		a.choose(0)
	case 'G':
		a.choose(len(a.view) - 1)
	case '/':
		a.typing = true
	case '?':
		a.mode = modeHelp
	case 'c':
		a.mode, a.chronTop = modeChron, 0
	case 'd':
		a.act(model.ReqStart)
	case 'a':
		a.act(model.ReqStop)
	case 'r':
		a.act(model.ReqRestart)
	case 'l':
		a.act(model.ReqFreePort)
	case 'R':
		a.refresh()
	default:
		if r >= '0' && r <= '9' {
			// taper un nombre cherche un port, sans passer par « / »
			a.typing, a.query = true, string(r)
		}
	}
}

func (a *app) keySearch(k term.Key) {
	switch k.Code {
	case term.KeyEnter:
		a.typing = false
		if a.query == "" {
			return
		}
	case term.KeyEsc:
		a.typing, a.query = false, ""
	case term.KeyBackspace, term.KeyDelete:
		if rs := []rune(a.query); len(rs) > 0 {
			a.query = string(rs[:len(rs)-1])
		}
	case term.KeyCtrlU:
		a.query = ""
	case term.KeyCtrlW:
		rs := []rune(a.query)
		i := len(rs)
		for i > 0 && rs[i-1] == ' ' {
			i--
		}
		for i > 0 && rs[i-1] != ' ' {
			i--
		}
		a.query = string(rs[:i])
	case term.KeyUp:
		a.choose(a.selIdx - 1)
	case term.KeyDown:
		a.choose(a.selIdx + 1)
	case term.KeyLeft:
		a.cycleFocus(-1)
	case term.KeyRight:
		a.cycleFocus(1)
	case term.KeyRune:
		r := k.Rune
		if r == 'Q' {
			r = 'q' // q quitte ; Q l'écrit dans la recherche
		}
		a.query += string(r)
		a.selIdx = 0 // la meilleure réponse est en tête
		a.sel = itemID{}
	}
}

func (a *app) keyDetail(k term.Key) {
	switch k.Code {
	case term.KeyEsc, term.KeyEnter, term.KeyBackspace:
		a.mode = modeMain
	case term.KeyUp:
		a.choose(a.selIdx - 1)
	case term.KeyDown:
		a.choose(a.selIdx + 1)
	case term.KeyCtrlR:
		a.refresh()
	case term.KeyRune:
		switch k.Rune {
		case 'k':
			a.choose(a.selIdx - 1)
		case 'j':
			a.choose(a.selIdx + 1)
		case 'd':
			a.act(model.ReqStart)
		case 'a':
			a.act(model.ReqStop)
		case 'r':
			a.act(model.ReqRestart)
		case 'l':
			a.act(model.ReqFreePort)
		case 'R':
			a.refresh()
		case '?':
			a.mode = modeHelp
		}
	}
}

// keyHelp fait défiler la légende quand elle dépasse ; toute autre touche la
// ferme.
func (a *app) keyHelp(k term.Key) {
	step := 0
	switch {
	case k.Code == term.KeyUp || k.Code == term.KeyRune && k.Rune == 'k':
		step = -1
	case k.Code == term.KeyDown || k.Code == term.KeyRune && k.Rune == 'j':
		step = 1
	case k.Code == term.KeyPgUp:
		step = -max(a.H/2, 1)
	case k.Code == term.KeyPgDn:
		step = max(a.H/2, 1)
	}
	if step != 0 && a.helpOver {
		a.helpTop += step
		return
	}
	a.mode, a.helpTop = modeMain, 0
}

func (a *app) keyConfirm(k term.Key) {
	yes := k.Code == term.KeyEnter || (k.Code == term.KeyRune && (k.Rune == 'o' || k.Rune == 'O' || k.Rune == 'y'))
	no := k.Code == term.KeyEsc || (k.Code == term.KeyRune && (k.Rune == 'n' || k.Rune == 'N'))
	switch {
	case yes && a.ask != nil:
		q := a.ask
		a.ask, a.mode = nil, a.back
		a.w.send(a.src, q.machine, q.op, q.target, q.item, a.now)
	case no:
		a.ask, a.mode = nil, a.back
		a.say("rien n'a été envoyé", false)
	}
}

func (a *app) keyChron(k term.Key) {
	n := len(a.w.chron)
	switch k.Code {
	case term.KeyEsc, term.KeyBackspace:
		a.mode = modeMain
	case term.KeyUp:
		a.chronTop--
	case term.KeyDown:
		a.chronTop++
	case term.KeyPgUp:
		a.chronTop -= max(a.H-6, 1)
	case term.KeyPgDn:
		a.chronTop += max(a.H-6, 1)
	case term.KeyHome:
		a.chronTop = 0
	case term.KeyEnd:
		a.chronTop = n
	case term.KeyEnter:
		// va à l'élément de la ligne du haut, s'il est encore là
		if i := n - 1 - a.chronTop; i >= 0 && i < n {
			a.goTo(a.w.chron[i].id)
		}
		a.mode = modeMain
	case term.KeyRune:
		switch k.Rune {
		case 'c':
			a.mode = modeMain
		case 'k':
			a.chronTop--
		case 'j':
			a.chronTop++
		}
	}
	a.chronTop = min(max(a.chronTop, 0), max(n-1, 0))
}

// goTo choisit l'élément id dans l'échelle, en levant filtre et focus s'il le
// faut.
func (a *app) goTo(id itemID) {
	for pass := 0; pass < 2; pass++ {
		a.sync()
		for i := range a.view {
			if a.view[i].id == id {
				a.choose(i)
				return
			}
		}
		if id.kind == fleet.KindMachine {
			a.focus, a.query, a.typing = id.machine, "", false
			a.sync()
			a.choose(0)
			return
		}
		a.focus, a.query, a.typing = "", "", false
	}
	a.say("cet élément n'est plus là", false)
}

// jumpStratum va à la première entrée de la marche suivante (dir = 1) ou
// précédente (dir = -1).
func (a *app) jumpStratum(dir int) {
	if len(a.view) == 0 {
		return
	}
	cur := a.view[a.selIdx].stratum
	if dir > 0 {
		for i := a.selIdx; i < len(a.view); i++ {
			if a.view[i].stratum != cur {
				a.choose(i)
				return
			}
		}
		a.choose(0)
		return
	}
	// début de la marche courante, puis début de la précédente
	start := a.selIdx
	for start > 0 && a.view[start-1].stratum == cur {
		start--
	}
	if start != a.selIdx {
		a.choose(start)
		return
	}
	if start == 0 {
		// tout en bas : début de la dernière marche
		last := len(a.view) - 1
		for last > 0 && a.view[last-1].stratum == a.view[len(a.view)-1].stratum {
			last--
		}
		a.choose(last)
		return
	}
	prev := a.view[start-1].stratum
	i := start - 1
	for i > 0 && a.view[i-1].stratum == prev {
		i--
	}
	a.choose(i)
}

// cycleFocus fait tourner la machine regardée : toutes, puis chacune.
func (a *app) cycleFocus(dir int) {
	var names []string
	for _, m := range a.machines() {
		names = append(names, m.Name)
	}
	if len(names) == 0 {
		return
	}
	cur := -1
	for i, n := range names {
		if n == a.focus {
			cur = i
		}
	}
	next := cur + dir
	switch {
	case next < -1:
		next = len(names) - 1
	case next >= len(names):
		next = -1
	}
	if next == -1 {
		a.focus = ""
	} else {
		a.focus = names[next]
	}
	a.sync()
}

// act prépare une requête sur l'élément choisi : start part tout de suite, les
// autres passent par une question.
func (a *app) act(op string) {
	e := a.selected()
	if e == nil {
		a.say("rien n'est choisi", false)
		return
	}
	if e.ghost {
		a.say("cet élément vient de disparaître : rien à faire dessus", false)
		return
	}
	if p := a.w.pendingFor(e.id); p != nil && a.now.Sub(p.at) < silentAfter {
		a.say(opProgress(p.op)+" déjà en cours : attends sa réponse", false)
		return
	}
	m := e.machine
	switch op {
	case model.ReqStart, model.ReqStop, model.ReqRestart:
		if e.app == nil {
			a.say(opWord(op)+" agit sur une appli : choisis une ligne d'appli", false)
			return
		}
		if op == model.ReqStart {
			a.w.send(a.src, m.Name, op, e.app.Name, e.id, a.now)
			return
		}
		verb := opWord(op)
		a.askFor(&question{
			op: op, machine: m.Name, target: e.app.Name, item: e.id,
			title: capital(verb) + " " + e.app.Name + " sur " + m.Name + " ?",
			facts: appFacts(e.app, e.home()),
			yes:   verb,
		})
	case model.ReqFreePort:
		port := 0
		switch {
		case e.port != nil:
			port = e.port.Port
		case e.app != nil:
			port = e.app.Port
		}
		if port == 0 {
			a.say("l libère un port : choisis une ligne de port, ou une appli qui en a un", false)
			return
		}
		a.askFor(&question{
			op: op, machine: m.Name, target: strconv.Itoa(port), item: e.id,
			title: "Libérer le port " + strconv.Itoa(port) + " sur " + m.Name + " ?",
			facts: portFacts(m, port),
			yes:   "libérer",
		})
	}
}

func (a *app) askFor(q *question) {
	a.ask = q
	if a.mode != modeConfirm {
		a.back = a.mode
	}
	a.mode = modeConfirm
}

// refresh demande un relevé neuf à la machine de la sélection, ou à la machine
// regardée, ou à toutes.
func (a *app) refresh() {
	var targets []string
	switch e := a.selected(); {
	case a.focus != "":
		targets = []string{a.focus}
	case e != nil:
		targets = []string{e.machine.Name}
	default:
		for _, m := range a.machines() {
			targets = append(targets, m.Name)
		}
	}
	for _, t := range targets {
		a.w.send(a.src, t, model.ReqRefresh, "", itemID{t, fleet.KindMachine, t}, a.now)
	}
	if len(targets) == 1 {
		a.say("relevé demandé à "+targets[0], false)
	} else if len(targets) > 1 {
		a.say("relevé demandé aux machines", false)
	}
}

// appFacts résume une appli pour une question.
func appFacts(ap *model.App, home string) []string {
	f := []string{appWord(ap.State)}
	if ap.Port != 0 {
		f[0] += " · port " + strconv.Itoa(ap.Port)
	}
	if ap.Unit != "" {
		f = append(f, "unité "+ap.Unit)
	}
	if ap.CWD != "" {
		f = append(f, "dossier "+shortPath(ap.CWD, home))
	}
	return f
}

// portFacts dit qui tient le port, d'après le relevé.
func portFacts(m *fleet.Machine, port int) []string {
	var f []string
	home := homeOf(m.Snapshot.Machine.User)
	for _, l := range m.Snapshot.Listeners {
		if l.Port != port {
			continue
		}
		if !l.Mine {
			f = append(f, "écoute sur "+l.Addr+" : tenu par un autre compte (uid "+strconv.Itoa(l.UID)+"), programme et dossier inconnus")
			continue
		}
		s := "écoute sur " + l.Addr + " : " + orWord(l.Process, "programme inconnu")
		if l.PID != 0 {
			s += " (pid " + strconv.Itoa(l.PID) + ")"
		}
		if d := listenerDir(l); d != "" {
			s += " · " + shortPath(d, home)
		}
		f = append(f, s)
	}
	if len(f) == 0 {
		f = append(f, "le dernier relevé ne montre rien en écoute sur ce port")
	}
	return f
}

// capital met une majuscule au premier mot.
func capital(s string) string {
	rs := []rune(s)
	if len(rs) == 0 {
		return s
	}
	switch rs[0] {
	case 'é':
		rs[0] = 'É'
	case 'à':
		rs[0] = 'À'
	default:
		if rs[0] >= 'a' && rs[0] <= 'z' {
			rs[0] -= 'a' - 'A'
		}
	}
	return string(rs)
}

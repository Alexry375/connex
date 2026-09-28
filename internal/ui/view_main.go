package ui

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// layout découpe la planche I.
type layout struct {
	W, H      int
	skyRows   int
	nameY     int // noms des machines, au ras de l'horizon
	stateY    int // ce qui compte sur chaque machine
	catY      int
	catRows   int
	newsY     int
	newsRows  int
	paperY    int
	paperRows int
	regions   []span
	multi     bool // un catalogue par machine, côte à côte
}

// Taille en dessous de laquelle la planche ne tient plus.
const minW, minH = 40, 12

func mainLayout(W, H, n int) layout {
	l := layout{W: W, H: H}
	big := H >= 32
	l.paperRows, l.newsRows = 2, 1
	if big {
		l.paperRows, l.newsRows = 3, 2
	}
	if H >= 42 {
		l.newsRows = 3
	}
	l.skyRows = min(16, max(3, H/3))
	l.nameY = l.skyRows
	l.stateY = l.skyRows + 1
	l.catY = l.skyRows + 2
	if big {
		l.catY++
	}
	l.paperY = H - l.paperRows
	l.newsY = l.paperY - l.newsRows
	l.catRows = l.newsY - l.catY
	if big {
		l.catRows-- // une rangée d'air avant les nouvelles
	}
	l.regions = regionsFor(W, n)
	l.multi = n > 1 && W/n >= 38
	return l
}

// scene renvoie le ciel à la taille voulue, recalculé si besoin.
func (a *App) scene(W, rows int) *scene {
	if a.sc == nil || a.sc.W != W || a.sc.rows != rows || len(a.sc.regions) != max(1, len(a.w.machines)) {
		a.sc = buildScene(a.w, W, rows)
	}
	return a.sc
}

// skyState rassemble ce qui anime le ciel à l'instant now.
func (a *App) skyState(now time.Time) skyState {
	st := skyState{now: now, step: int64(now.Sub(a.opened) / stepDur), refused: -1, meteors: a.meteors}
	ph := float64(now.Sub(a.opened)%pulsePeriod) / float64(pulsePeriod)
	// Respiration : lente montée, lente descente, par pas de 250 ms.
	ph = float64(int(ph*float64(pulsePeriod/stepDur))) / float64(pulsePeriod/stepDur)
	st.pulse = 0.5 - 0.5*cosTurn(ph)
	if it := a.selected(); it != nil {
		st.selected = it.id
	}
	if a.query != "" {
		st.search = true
		st.matches = map[string]int{}
		for _, it := range a.results {
			st.matches[it.id] = 1
		}
	}
	if !a.flare.IsZero() && now.Sub(a.flare) < 3*time.Second {
		if r := a.lastRefused(); r != nil {
			if mv := a.w.machine(r.machine); mv != nil {
				st.refused = a.indexOf(mv)
				st.flareAge = now.Sub(a.flare)
			}
		}
	}
	return st
}

func (a *App) indexOf(mv *machineView) int {
	for i, m := range a.w.machines {
		if m == mv {
			return i
		}
	}
	return -1
}

func (a *App) lastRefused() *request {
	for i := len(a.finished) - 1; i >= 0; i-- {
		if !a.finished[i].ok && !a.finished[i].lost {
			return a.finished[i]
		}
	}
	return nil
}

// drawMain dessine la planche I : le ciel, les machines, le catalogue.
func (a *App) drawMain(cv *canvas.Canvas, now time.Time) {
	l := mainLayout(a.W, a.H, len(a.w.machines))
	cv.Clear(landBG)
	sc := a.scene(a.W, l.skyRows)
	sc.render(cv, 0, a.skyState(now))
	a.drawMachines(cv, l, now)

	// Catalogues.
	a.pageRows = l.catRows
	switch {
	case a.query != "":
		v := catView{x: 0, y: l.catY, w: a.W, rows: l.catRows, items: a.results, key: "\x00search",
			selected: a.qsel, active: true, gutter: !l.multi, machineTag: len(a.w.machines) > 1}
		if l.multi {
			v.x, v.w = 2, a.W-4
		}
		if len(a.results) == 0 {
			cv.Text(v.x+1, v.y, v.x+v.w, "Rien ne répond à « "+a.query+" ».", inkDim, canvas.Italic)
		}
		if it := a.selected(); it != nil {
			v.selected = it.id
		}
		a.drawCatalogue(cv, v, now)
	case l.multi:
		for i, mv := range a.w.machines {
			r := l.regions[i]
			a.drawMachineCatalogue(cv, mv, r.x0+1, l.catY, r.x1-r.x0-2, l.catRows, i == a.focus, false, now)
		}
	default:
		if mv := a.focusMachine(); mv != nil {
			a.drawMachineCatalogue(cv, mv, 0, l.catY, a.W, l.catRows, true, true, now)
		} else {
			cv.Text(2, l.catY, a.W, "En attente du premier relevé…", inkDim, canvas.Italic)
		}
	}

	a.drawNews(cv, 0, l.newsY, a.W, l.newsRows, now)
	a.drawPaper(cv, l.paperY, l.paperRows, 1, now)
}

// drawMachineCatalogue dessine le catalogue d'une machine, précédé de son
// éclipse si elle ne répond plus.
func (a *App) drawMachineCatalogue(cv *canvas.Canvas, mv *machineView, x, y, w, rows int, focused, gutter bool, now time.Time) {
	if !mv.m.Known {
		msg := "En attente du premier relevé…"
		if !mv.m.Reachable {
			msg = "Injoignable avant tout relevé."
		}
		cv.Text(x+1, y, x+w, msg, inkDim, canvas.Italic)
		if r := a.reasons[mv.m.Name]; r != "" && !mv.m.Reachable {
			for i, ln := range wrapRows(r, w-2, rows-1) {
				cv.Text(x+1, y+1+i, x+w-1, ln, redDim, canvas.Italic)
			}
		}
		return
	}
	if !mv.m.Reachable && rows > 3 {
		// La raison de la coupure se lit en entier sur deux rangées si le
		// catalogue en a la place, quitte à perdre la mention du dernier état
		// (le catalogue pâli le dit déjà) ; sinon elle tient sur une, coupée.
		msg := "Éclipse : dernier état connu, relevé à " + clockFR(mv.m.Updated.Local()) + "."
		r := a.reasons[mv.m.Name]
		if r != "" {
			msg = r + " — dernier état connu ci-dessous."
		}
		lines := wrap(msg, w-2)
		if len(lines) > 2 && r != "" {
			lines = wrap(r, w-2)
		}
		if len(lines) > 2 || rows < 8 {
			lines = []string{canvas.Truncate(msg, w-2)}
		}
		for _, ln := range lines {
			cv.Text(x+1, y, x+w-1, ln, redDim, canvas.Italic)
			y++
			rows--
		}
	}
	if len(mv.items) == 0 {
		cv.Text(x+1, y, x+w, "Rien à signaler : aucun port, aucun agent, aucun dépôt.", inkDim, canvas.Italic)
		return
	}
	sel := a.sel[mv.m.Name]
	if focused {
		if it := a.selected(); it != nil {
			sel = it.id
		}
	}
	a.drawCatalogue(cv, catView{x: x, y: y, w: w, rows: rows, items: mv.items, key: mv.m.Name,
		selected: sel, active: focused, gutter: gutter, dim: !mv.m.Reachable}, now)
}

// drawMachines écrit, sous chaque ciel, le nom de la machine et ce qui y compte.
func (a *App) drawMachines(cv *canvas.Canvas, l layout, now time.Time) {
	if len(a.w.machines) == 0 {
		return
	}
	for i, mv := range a.w.machines {
		r := l.regions[i]
		w := r.x1 - r.x0
		name := mv.m.Name
		fg, at := inkDim, canvas.Attr(0)
		if i == a.focus && a.query == "" {
			fg, at = inkBright, canvas.Bold
		}
		if !mv.m.Reachable {
			fg = canvas.Mix(fg, red, 0.35)
		}
		name = canvas.Truncate(name, w-2)
		nx := r.x0 + (w-canvas.StringWidth(name))/2
		if i == a.focus && a.query == "" && len(a.w.machines) > 1 {
			cv.Text(nx-2, l.nameY, r.x1, "›", gold, canvas.Bold)
		}
		cv.Text(nx, l.nameY, r.x1, name, fg, at)

		segs := a.machineState(mv, now, l.multi)
		fitted := fitSegs(segs, w-2)
		sx := r.x0 + (w-segsWidth(fitted))/2
		for _, s := range fitted {
			sx = cv.Text(sx, l.stateY, r.x1, s.s, s.fg, s.a)
		}
	}
}

// backShown : combien de temps l'en-tête d'une machine dit son retour.
const backShown = time.Minute

// machineState résume une machine, par ordre d'importance.
func (a *App) machineState(mv *machineView, now time.Time, wide bool) []seg {
	m := mv.m
	var out []seg
	if !m.Reachable {
		out = append(out, seg{"injoignable depuis " + durFR(now.Sub(m.Since)), red, 0})
		return out
	}
	if !m.Known {
		return []seg{{"relevé en cours…", inkFaint, canvas.Italic}}
	}
	// Un relevé demandé (F5), puis le retour d'une machine, pendant une minute :
	// à 80 × 24, la rangée de nouvelles ne le garde pas si longtemps.
	if r := a.requestFor(machineKey(m.Name), now, resultShown); r != nil {
		out = append(out, requestSegs(r, now)...)
	}
	if t, ok := a.back[m.Name]; ok && now.Sub(t) < backShown {
		out = append(out, seg{"◐ de retour depuis " + durFR(now.Sub(t)), mint, 0})
	}
	if n := len(mv.waiting); n > 0 {
		s := "✦ 1 agent t’attend"
		if n > 1 {
			s = "✦ " + strconv.Itoa(n) + " agents t’attendent"
		}
		out = append(out, seg{s, gold, canvas.Bold})
	}
	failed, httpErr := 0, 0
	numbers, open := portNumbers(&m.Snapshot)
	for _, it := range mv.items {
		switch {
		case it.kind == kindApp && it.app.State == model.AppFailed:
			failed++
		case it.kind == kindApp && it.app.State == model.AppRunning && it.app.Health.HTTP >= 400:
			httpErr++
		}
	}
	if failed > 0 {
		out = append(out, seg{"✕ " + plural(failed, "appli en échec", "applis en échec"), red, 0})
	}
	if httpErr > 0 {
		out = append(out, seg{plural(httpErr, "appli répond en erreur", "applis répondent en erreur"), salmon, 0})
	}
	if open > 0 {
		out = append(out, seg{plural(open, "port ouvert à tous", "ports ouverts à tous"), salmon, 0})
	}
	switch {
	case len(mv.waiting) > 0:
	case mv.agents == 0:
		out = append(out, seg{"aucun agent", inkDim, 0})
	default:
		out = append(out, seg{plural(mv.agents, "agent", "agents"), inkDim, 0})
	}
	out = append(out, seg{plural(numbers, "port", "ports"), inkFaint, 0})
	if wide {
		out = append(out, seg{plural(mv.repos, "dépôt", "dépôts"), inkFaint, 0})
	}
	return out
}

// fitSegs garde les morceaux qui tiennent en w colonnes, dans l'ordre, séparés
// par « · ». Le premier est coupé s'il ne tient pas seul.
func fitSegs(segs []seg, w int) []seg {
	var out []seg
	used := 0
	for i, s := range segs {
		sw := canvas.StringWidth(s.s)
		sep := 0
		if len(out) > 0 {
			sep = 3
		}
		if used+sep+sw > w {
			if i == 0 {
				out = append(out, seg{canvas.Truncate(s.s, w), s.fg, s.a})
			}
			break
		}
		if sep > 0 {
			out = append(out, seg{" · ", inkGhost, 0})
		}
		out = append(out, s)
		used += sep + sw
	}
	return out
}

func segsWidth(segs []seg) int {
	n := 0
	for _, s := range segs {
		n += canvas.StringWidth(s.s)
	}
	return n
}

// drawNews écrit les dernières nouvelles, la plus récente en haut.
func (a *App) drawNews(cv *canvas.Canvas, x, y, w, rows int, now time.Time) {
	i := 0
	if a.noticeLive(now) {
		cv.Text(x+2, y, x+w-1, a.notice.text, toneColor(a.notice.tone), canvas.Italic)
		i++
	}
	for k := len(a.journal) - 1; k >= 0 && i < rows; k-- {
		a.drawEntry(cv, x, y+i, w, a.journal[k], false, 1, now)
		i++
	}
	if len(a.journal) == 0 && i == 0 {
		cv.Text(x+2, y, x+w-1, "Aucun changement observé pour l’instant.", inkFaint, canvas.Italic)
	}
}

// drawEntry écrit une nouvelle : heure, machine, signe, texte. Le texte prend
// jusqu'à rows rangées, la dernière coupée s'il le faut ; drawEntry renvoie
// les rangées prises.
func (a *App) drawEntry(cv *canvas.Canvas, x, y, w int, e entry, full bool, rows int, now time.Time) int {
	end := x + w - 1
	cx := cv.Text(x+1, y, end, clockFR(e.at.Local()), inkFaint, 0)
	cx = max(cx+1, x+9)
	if a.entryMachine(e, full) {
		cx = cv.Text(cx, y, end, e.machine, inkDim, canvas.Italic) + 1
	}
	fg := toneColor(e.tone)
	cx = cv.Text(cx, y, end, string(e.sign), fg, canvas.Bold) + 1
	lines := entryLines(e.text, end-cx, rows)
	for i, ln := range lines {
		cv.Text(cx, y+i, end, ln, fg, 0)
	}
	return max(1, len(lines))
}

// entryMachine dit si la nouvelle nomme sa machine.
func (a *App) entryMachine(e entry, full bool) bool {
	return e.machine != "" && (len(a.w.machines) > 1 || full)
}

// entryX renvoie la colonne où drawEntry, partie de x, écrit le texte.
func (a *App) entryX(x int, e entry, full bool) int {
	cx := max(x+2+canvas.StringWidth(clockFR(e.at.Local())), x+9)
	if a.entryMachine(e, full) {
		cx += canvas.StringWidth(e.machine) + 1
	}
	return cx + canvas.StringWidth(string(e.sign)) + 1
}

// entryLines coupe le texte d'une nouvelle en rows rangées de w colonnes au
// plus ; la dernière est coupée si le texte ne tient pas.
func entryLines(s string, w, rows int) []string {
	if w <= 0 {
		return nil
	}
	if rows <= 1 || canvas.StringWidth(s) <= w {
		return []string{canvas.Truncate(s, w)}
	}
	lines := wrap(s, w)
	if len(lines) > rows {
		lines = append(lines[:rows-1], canvas.Truncate(strings.Join(lines[rows-1:], " "), w))
	}
	return lines
}

func toneColor(t tone) canvas.Color {
	switch t {
	case toneGood:
		return mint
	case toneWarn:
		return salmon
	case toneCall:
		return gold
	case toneBad:
		return red
	}
	return inkDim
}

// title est le titre de la planche I : l'état qui compte. full dit aussi les
// machines qui ne répondent plus ou n'ont rien relevé encore ; short, quand la
// place manque, garde l'essentiel. Un agent d'état inconnu n'est jamais compté
// comme « ne t'attend pas ».
func (a *App) title() (full, short string, t tone) {
	if len(a.w.machines) == 0 {
		return "RELEVÉ EN COURS…", "RELEVÉ EN COURS…", toneNeutral
	}
	var names, lostNames, pendingNames []string
	n, agents, unknown, known := 0, 0, 0, 0
	for _, mv := range a.w.machines {
		switch {
		case !mv.m.Reachable:
			lostNames = append(lostNames, strings.ToUpper(mv.m.Name))
			continue
		case !mv.m.Known:
			pendingNames = append(pendingNames, strings.ToUpper(mv.m.Name))
			continue
		}
		known++
		agents += mv.agents
		unknown += mv.unknown
		if len(mv.waiting) > 0 {
			n += len(mv.waiting)
			names = append(names, strings.ToUpper(mv.m.Name))
		}
	}
	// Ce qu'on ne sait pas des machines muettes ou pas encore relevées.
	var others []string
	switch len(lostNames) {
	case 0:
	case 1:
		others = append(others, lostNames[0]+" NE RÉPOND PLUS")
	default:
		others = append(others, strconv.Itoa(len(lostNames))+" MACHINES NE RÉPONDENT PLUS")
	}
	switch len(pendingNames) {
	case 0:
	case 1:
		others = append(others, "RELEVÉ EN COURS SUR "+pendingNames[0])
	default:
		others = append(others, "RELEVÉ EN COURS SUR "+strconv.Itoa(len(pendingNames))+" MACHINES")
	}
	status := strings.Join(others, " · ")
	if len(lostNames) > 0 {
		t = toneBad
	}
	switch {
	case n > 0:
		var head string
		switch {
		case n == 1:
			head = "UN AGENT T’ATTEND SUR " + names[0]
		case len(names) == 1:
			head = strconv.Itoa(n) + " AGENTS T’ATTENDENT SUR " + names[0]
		default:
			head = strconv.Itoa(n) + " AGENTS T’ATTENDENT : " + strings.Join(names, ", ")
		}
		if status != "" {
			return head + " · " + status + ".", head + ".", toneCall
		}
		return head + ".", head + ".", toneCall
	case known == 0 && len(pendingNames) == 0:
		return "AUCUNE MACHINE NE RÉPOND.", "AUCUNE MACHINE NE RÉPOND.", toneBad
	case known == 0:
		return status + ".", status + ".", t
	}
	// Aucun agent n'attend, à ce qu'on sait. Sur les machines muettes, on ne
	// sait rien : on le dit « ailleurs ».
	where := ""
	if status != "" {
		where = " AILLEURS"
	}
	head := "AUCUN AGENT NE T’ATTEND" + where
	if agents == 0 {
		head = "AUCUN AGENT EN COURS" + where
	}
	shortHead := head
	switch {
	case unknown == 1:
		head += ", SAUF PEUT-ÊTRE UN D’ÉTAT INCONNU"
		shortHead += ", SAUF PEUT-ÊTRE UN"
	case unknown > 1:
		head += ", SAUF PEUT-ÊTRE " + strconv.Itoa(unknown) + " D’ÉTAT INCONNU"
		shortHead += ", SAUF PEUT-ÊTRE " + strconv.Itoa(unknown)
	}
	if status != "" {
		return status + " · " + head + ".", status + ".", t
	}
	return head + ".", shortHead + ".", t
}

// cosTurn est cos(2π·t).
func cosTurn(t float64) float64 { return math.Cos(2 * math.Pi * t) }

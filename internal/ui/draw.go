package ui

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// L'écran principal est composé comme le Retable n° 1 : le soleil en haut,
// accroché au bord, entre deux volets (à gauche les agents qui t'attendent, à
// droite les machines, en astres) ; dessous, l'échelle, rangée en marches comme
// la pyramide. Aucun cadre : tout est posé sur le noir du tableau.

type rect struct{ x, y, w, h int }

// layout place les régions de l'écran selon sa taille.
type layout struct {
	W, H   int
	tiny   bool // trop petit : un message seulement
	narrow bool // pas de tympan : une ligne d'oracle
	tympH  int

	sunCX, sunCY, sunR float64 // en pixels (deux par case en hauteur)
	sun                rect
	left, right        rect
	medals             bool // les astres peints plutôt que des pastilles

	ladder rect
	side   rect // la chronique en colonne (grands écrans) ; w = 0 sinon
	strip  int  // ligne de la dernière nouvelle (petits écrans) ; -1 sinon
	hint   int
}

func computeLayout(W, H int) layout {
	l := layout{W: W, H: H, strip: -1, hint: H - 1}
	if W < 36 || H < 8 {
		l.tiny = true
		return l
	}
	if W < 64 || H < 18 {
		l.narrow = true
		l.tympH = 2
		l.left = rect{1, 0, W - 2, 2}
		l.ladder = rect{0, 3, W, H - 5}
		l.strip = H - 2
		return l
	}
	l.tympH = min(max(int(math.Round(float64(H)/4.8)), 5), 11)
	// un dôme d'or accroché au bord du haut : le centre est au-dessus de
	// l'écran, les rayons descendent jusqu'au bas du tympan
	l.sunR = float64(2*l.tympH) / (ringOut - 0.25)
	l.sunCX = float64(W) / 2
	l.sunCY = -l.sunR * 0.25
	half := int(math.Ceil(l.sunR * rayMax))
	cx := W / 2
	l.sun = rect{cx - half, 0, 2*half + 1, l.tympH}
	l.left = rect{2, 0, l.sun.x - 4, l.tympH}
	l.right = rect{l.sun.x + l.sun.w + 2, 0, W - (l.sun.x + l.sun.w + 2) - 2, l.tympH}
	l.medals = l.right.w >= 36 && l.tympH >= 8
	top := l.tympH + 1
	if W >= 118 && H >= 30 {
		sw := min(max(W*31/100, 38), 64)
		l.side = rect{W - sw - 1, top, sw, H - top - 2}
		l.ladder = rect{0, top, W - sw - 3, H - top - 2}
	} else {
		l.ladder = rect{0, top, W, H - top - 2}
		l.strip = H - 2
	}
	return l
}

// Styles de texte : fond transparent, pour écrire sur les bandes et la ligne
// choisie sans les effacer.
func fg(c canvas.Color) canvas.Style { return canvas.Style{Fg: c, Bg: canvas.Transparent} }
func bold(c canvas.Color) canvas.Style {
	return canvas.Style{Fg: c, Bg: canvas.Transparent, A: canvas.Bold}
}

// draw peint tout l'écran.
func (a *app) draw(c *canvas.Canvas) {
	a.sync()
	c.Fill(' ', canvas.Style{Fg: cInk, Bg: cGround})
	l := computeLayout(c.W, c.H)
	if l.tiny {
		a.drawTiny(c)
		return
	}
	switch a.mode {
	case modeHelp:
		a.drawHelp(c)
		return
	case modeChron:
		a.drawChronicle(c)
		return
	}
	a.drawMain(c, l)
	switch a.mode {
	case modeDetail:
		a.drawDetail(c, l)
	case modeConfirm:
		a.drawConfirm(c, l)
	}
	if a.opening {
		a.drawOpening(c, a.now.Sub(a.born))
	}
}

func (a *app) drawMain(c *canvas.Canvas, l layout) {
	if l.narrow {
		a.drawOracleLine(c, l.left)
	} else {
		a.drawSun(c, l)
		a.drawAgents(c, l.left)
		if l.medals {
			a.drawMedals(c, l.right)
		} else {
			a.drawMachineList(c, l.right)
		}
	}
	a.drawLadder(c, l.ladder)
	if l.side.w > 0 {
		a.drawSide(c, l.side)
	}
	if l.strip >= 0 {
		a.drawStrip(c, l.strip)
	}
	a.drawHint(c, l.hint)
}

func (a *app) drawTiny(c *canvas.Canvas) {
	msg := "agrandis la fenêtre"
	c.Text(max((c.W-canvas.Width(msg))/2, 0), c.H/2, canvas.Truncate(msg, c.W), fg(cInkSoft))
	if n := a.waitingCount(); n > 0 && c.H > 2 {
		s := plural(n, "agent t'attend", "agents t'attendent")
		c.Text(max((c.W-canvas.Width(s))/2, 0), c.H/2-1, canvas.Truncate(s, c.W), bold(cGold))
	}
}

// waiting renvoie les agents qui t'attendent, dans l'ordre de l'échelle.
func (a *app) waiting() []*entry {
	var out []*entry
	for i := range a.all {
		e := &a.all[i]
		if e.agent != nil && !e.ghost && e.agent.State == model.StateWaiting {
			out = append(out, e)
		}
	}
	return out
}

func (a *app) waitingCount() int { return len(a.waiting()) }

// agentCounts compte les agents par état (sans les disparus).
func (a *app) agentCounts() map[string]int {
	n := map[string]int{}
	for i := range a.all {
		if e := &a.all[i]; e.agent != nil && !e.ghost {
			n[e.agent.State]++
		}
	}
	return n
}

// ---------------------------------------------------------------- volets

// drawAgents peint le volet gauche : qui t'attend, et où.
func (a *app) drawAgents(c *canvas.Canvas, r rect) {
	if r.w < 8 {
		return
	}
	s := c.Sub(r.x, r.y, r.w, r.h)
	if len(a.machines()) == 0 {
		s.Text(0, 0, canvas.Truncate("premier relevé en cours…", s.W), fg(cInkDim))
		return
	}
	wait := a.waiting()
	if len(wait) == 0 {
		s.Text(0, 0, canvas.Truncate("aucun agent ne t'attend", s.W), bold(cInkSoft))
		n := a.agentCounts()
		y := 1
		for _, st := range []string{model.StateWorking, model.StateIdle, model.StateUnknown} {
			if n[st] == 0 || y >= s.H {
				continue
			}
			g, col := agentGlyph(st)
			s.Set(0, y, g, fg(col))
			s.Text(2, y, canvas.Truncate(agentCountWord(st, n[st]), s.W-2), fg(col))
			y++
		}
		if y == 1 && s.H > 1 {
			s.Text(0, 1, fit([]string{"aucune session d'agent en cours", "aucune session en cours", "aucune session"}, s.W), fg(cInkDim))
		}
		return
	}
	title := "un agent t'attend"
	if len(wait) > 1 {
		title = strconv.Itoa(len(wait)) + " agents t'attendent"
	}
	pulse := a.breath()
	s.Text(0, 0, canvas.Truncate(title, s.W), bold(canvas.Mix(cGoldDeep, cGoldBright, pulse)))
	rows := s.H - 1
	two := rows >= 2*len(wait) || s.W < 44 // deux lignes par agent si la place le permet
	per := 1
	if two {
		per = 2
	}
	shown := rows / per
	if shown < len(wait) && shown > 0 {
		shown-- // garde une ligne pour « et N autres »
	}
	y := 1
	for i := 0; i < len(wait) && i < shown; i++ {
		e := wait[i]
		ag := e.agent
		s.Set(0, y, '◉', fg(cGold))
		where := placeName(ag.CWD, e.home())
		if where == "" {
			where = "dossier inconnu"
		}
		dur, durCol := "", cInkDim
		if t, ok := a.w.since[e.id]; ok {
			dur = since(a.now.Sub(t))
		}
		if !e.machine.Reachable {
			// c'est son dernier état connu : la machine ne répond plus
			dur, durCol = "injoignable", cRed
		}
		if two {
			x := 2 + s.Text(2, y, canvas.Truncate(where, s.W-2), bold(cInk))
			if x+3+canvas.Width(ag.Tool) <= s.W {
				s.Text(x, y, " · "+ag.Tool, fg(cInkDim))
			}
			x = 2 + s.Text(2, y+1, canvas.Truncate(e.machine.Name, s.W-2), fg(machineTint(e.order)))
			if dur != "" && x+3+canvas.Width(dur) <= s.W {
				s.Text(x, y+1, " · "+dur, fg(durCol))
			}
		} else {
			x := 2 + s.Text(2, y, canvas.Truncate(where, max(s.W-4-canvas.Width(e.machine.Name), 8)), bold(cInk))
			x += s.Text(x, y, " · ", fg(cInkFaint))
			x += s.Text(x, y, canvas.Truncate(e.machine.Name, s.W-x), fg(machineTint(e.order)))
			if dur != "" && x+3+canvas.Width(dur) <= s.W {
				s.Text(x, y, " · "+dur, fg(durCol))
			}
		}
		y += per
	}
	if rest := len(wait) - shown; rest > 0 && y < s.H {
		more := "et " + plural(rest, "autre", "autres")
		say := []string{more + ", en tête de l'échelle", more + ", plus bas", more}
		if a.query != "" || a.focus != "" {
			say = say[2:] // l'échelle filtrée peut ne pas les montrer
		}
		s.Text(2, y, fit(say, s.W-2), fg(cGoldDeep))
	}
}

func agentCountWord(state string, n int) string {
	switch state {
	case model.StateWorking:
		return plural(n, "travaille", "travaillent")
	case model.StateIdle:
		return strconv.Itoa(n) + " au repos"
	}
	return strconv.Itoa(n) + " en état inconnu"
}

// drawOracleLine est le tympan des très petits écrans : une ligne pour les
// agents, une pour les machines.
func (a *app) drawOracleLine(c *canvas.Canvas, r rect) {
	s := c.Sub(r.x, r.y, r.w, r.h)
	wait := a.waiting()
	if len(wait) == 0 {
		s.Text(0, 0, canvas.Truncate("aucun agent ne t'attend", s.W), bold(cInkSoft))
	} else {
		x := s.Text(0, 0, "◉ ", fg(cGold))
		t := plural(len(wait), "agent t'attend", "agents t'attendent") + " — "
		named := map[string]bool{} // chaque machine une fois
		for _, e := range wait {
			if named[e.machine.Name] {
				continue
			}
			if len(named) > 0 {
				t += ", "
			}
			named[e.machine.Name] = true
			t += e.machine.Name
		}
		s.Text(x, 0, canvas.Truncate(t, s.W-x), bold(cGold))
	}
	x := 0
	for i, m := range a.machines() {
		g, col := a.astreGlyph(m, i)
		if x+2+canvas.Width(m.Name) > s.W {
			break
		}
		s.Set(x, 1, g, fg(col))
		x += 2
		nc := cInkSoft
		if !m.Reachable {
			nc = cRed
		}
		x += s.Text(x, 1, m.Name, fg(nc)) + 2
	}
}

// machinePhrase dit ce qui cloche sur une machine, ou rien : plusieurs
// tournures, de la plus longue à la plus courte, pour choisir celle qui tient.
func (a *app) machinePhrase(m *fleet.Machine) ([]string, canvas.Color) {
	switch {
	case !m.Reachable:
		d := a.now.Sub(m.Since)
		if d < time.Second {
			return []string{"injoignable à l'instant", "injoignable"}, cRed
		}
		return []string{"injoignable " + since(d), "injoignable · " + span(d), "injoignable"}, cRed
	case !m.Known:
		return []string{"premier relevé attendu", "relevé attendu"}, cInkDim
	}
	if t, ok := a.flips[m.Name]; ok && a.now.Sub(t) < recentTTL {
		d := a.now.Sub(t)
		if d < time.Second {
			return []string{"revenue à l'instant", "revenue"}, cSage
		}
		return []string{"revenue " + ago(d), "revenue · " + span(d), "revenue"}, cSage
	}
	var failed []string
	for _, ap := range m.Snapshot.Apps {
		if ap.State == model.AppFailed {
			failed = append(failed, ap.Name)
		}
	}
	switch len(failed) {
	case 0:
		return nil, cInkDim
	case 1:
		return []string{failed[0] + " en échec", "1 appli en échec", "1 échec"}, cRed
	}
	n := strconv.Itoa(len(failed))
	return []string{n + " applis en échec", n + " échecs"}, cRed
}

// fit choisit la première tournure qui tient en w colonnes, ou coupe la
// dernière.
func fit(variants []string, w int) string {
	for _, v := range variants {
		if canvas.Width(v) <= w {
			return v
		}
	}
	if len(variants) == 0 {
		return ""
	}
	return canvas.Truncate(variants[len(variants)-1], w)
}

// squeeze fait tenir s dans w colonnes. Un message d'erreur de ssh ou d'un
// programme s'écrit du contexte vers la raison, en morceaux « contexte: »
// (« ssh: connect to host … port 22: Connection timed out ») : quand il ne
// tient pas, on garde ce qui est arrivé (avant « — ») et la fin de la cause,
// en morceaux entiers, et c'est le milieu qui saute. Une issue courte
// (« fait ») derrière un début qui prend plus de la moitié de la place (un
// long nom de machine) se garde entière : c'est le début qui cède. Les
// phrases de connex, en français (« : » précédé d'une espace), se coupent à
// la fin comme le reste.
func squeeze(s string, w int) string {
	if canvas.Width(s) <= w {
		return s
	}
	head, cause := "", s
	if i := strings.Index(s, " — "); i >= 0 {
		head, cause = s[:i+len(" — ")], s[i+len(" — "):]
	}
	room := w - canvas.Width(head) - 1
	for i := 1; room > 0 && i+2 < len(cause); i++ {
		if cause[i] == ':' && cause[i+1] == ' ' && cause[i-1] != ' ' && canvas.Width(cause[i+2:]) <= room {
			return head + "…" + cause[i+2:]
		}
	}
	if what, ok := strings.CutSuffix(head, " — "); ok && canvas.Width(what) > w/2 && canvas.Width(" — "+cause) <= w/2 {
		return canvas.Truncate(what, w-canvas.Width(" — "+cause)) + " — " + cause
	}
	return canvas.Truncate(s, w)
}

// wrapSqueezed coupe s en n lignes de w colonnes au plus ; quand elle n'y
// tient pas, elle se resserre comme avec squeeze.
func wrapSqueezed(s string, w, n int) []string {
	for budget := n * w; budget > w; budget-- {
		if ls := wrap(squeeze(s, budget), w, n+1); len(ls) <= n {
			return ls
		}
	}
	return wrap(s, w, n)
}

// drawMachineList peint le volet droit en petit : une ligne par machine,
// alignée à droite, l'astre au bord.
func (a *app) drawMachineList(c *canvas.Canvas, r rect) {
	if r.w < 8 {
		return
	}
	s := c.Sub(r.x, r.y, r.w, r.h)
	ms := a.machines()
	y := 0
	for i, m := range ms {
		if y >= s.H {
			break
		}
		g, col := a.astreGlyph(m, i)
		s.Set(s.W-1, y, g, fg(col))
		nameCol := machineTint(i)
		st := fg(nameCol)
		switch {
		case a.focus == m.Name:
			st = bold(cInk)
		case a.focus != "":
			st = fg(cInkFaint)
		}
		name := canvas.Truncate(m.Name, s.W-2)
		nx := s.W - 2 - canvas.Width(name)
		s.Text(nx, y, name, st)
		phrases, pc := a.machinePhrase(m)
		if len(phrases) == 0 {
			y++
			continue
		}
		// la phrase entière sur la même ligne si elle tient, sinon dessous s'il
		// reste une ligne, sinon une tournure plus courte
		room := nx - 2
		switch {
		case canvas.Width(phrases[0]) <= room:
			s.Text(room-canvas.Width(phrases[0]), y, phrases[0], fg(pc))
		case s.H-y-1 > len(ms)-i-1:
			y++
			p := fit(phrases, s.W-2)
			s.Text(s.W-2-canvas.Width(p), y, p, fg(pc))
		default:
			p := fit(phrases, room)
			s.Text(room-canvas.Width(p), y, p, fg(pc))
		}
		y++
	}
	// la cause de la coupure, s'il reste une ligne
	for _, m := range ms {
		if !m.Reachable && y < s.H {
			if why := a.w.reason[m.Name]; why != "" {
				p := squeeze(why, s.W-2)
				s.Text(s.W-2-canvas.Width(p), y, p, fg(cInkDim))
				y++
			}
		}
	}
}

// ---------------------------------------------------------------- échelle

// stratumTitle nomme une marche, avec son compte.
func stratumTitle(st stratum, n int, others bool, waiting int) string {
	switch st {
	case stAgents:
		s := plural(n, "agent", "agents")
		switch {
		case waiting == n && n == 1:
			s += " : il t'attend"
		case waiting == n:
			s += " : tous t'attendent"
		case waiting == 1:
			s += ", dont 1 qui t'attend"
		case waiting > 1:
			s += ", dont " + strconv.Itoa(waiting) + " qui t'attendent"
		}
		return s
	case stApps:
		return plural(n, "appli", "applis")
	case stOpen:
		return plural(n, "port ouvert à tous", "ports ouverts à tous")
	case stPorts:
		if others {
			return plural(n, "autre port", "autres ports")
		}
		return plural(n, "port", "ports")
	case stRepos:
		return plural(n, "dépôt", "dépôts")
	}
	return ""
}

// pyramidAt donne la couleur de la pyramide du Retable à la position t (0 à
// gauche, 1 à droite).
func pyramidAt(t float64) canvas.Color {
	t = min(max(t, 0), 1) * float64(len(pyramid)-1)
	i := int(t)
	if i >= len(pyramid)-1 {
		return pyramid[len(pyramid)-1]
	}
	return canvas.Mix(pyramid[i], pyramid[i+1], t-float64(i))
}

func (a *app) drawLadder(c *canvas.Canvas, r rect) {
	if r.h <= 0 {
		return
	}
	s := c.Sub(r.x, r.y, r.w, r.h)
	y := 0
	if a.typing || a.query != "" || a.focus != "" {
		a.drawFilterLine(s, 0)
		y = 1
	}
	rows := s.H - y
	if len(a.machines()) == 0 {
		msg := "le premier relevé arrive…"
		s.Text((s.W-canvas.Width(msg))/2, y+rows/2, msg, fg(cInkDim))
		a.rows = rows
		return
	}
	if len(a.view) == 0 {
		msg := "rien ne répond à cette recherche"
		switch {
		case a.query != "":
		case a.focus != "" || len(a.machines()) == 1:
			msg = "rien à montrer sur cette machine"
		default:
			msg = "rien à montrer sur ces machines"
		}
		s.Text(max((s.W-canvas.Width(msg))/2, 0), y+rows/2, canvas.Truncate(msg, s.W), fg(cInkDim))
		a.rows = rows
		return
	}
	ls := a.lines()
	over := len(ls) > rows
	if over {
		rows-- // la dernière ligne montre la carte des marches
	}
	a.rows = rows
	a.scrollTo(ls, rows)
	nameW := 0
	if a.focus == "" && !a.oneMachine() {
		for _, m := range a.machines() {
			nameW = max(nameW, canvas.Width(m.Name))
		}
		// un nom de machine très long cède : la ligne doit pouvoir dire ce
		// qu'elle montre
		nameW = min(nameW, max(12, s.W/5), s.W/3)
	}
	others := map[stratum]bool{}
	for _, l := range ls {
		if l.header && l.st == stOpen {
			others[stPorts] = true
		}
	}
	// quand la liste défile, le titre de la marche en cours reste en haut, à la
	// place d'une ligne qui n'est pas la sélection
	sticky := !ls[a.top].header && ls[a.top].idx != a.selIdx
	for i := 0; i < rows && a.top+i < len(ls); i++ {
		l := ls[a.top+i]
		if i == 0 && sticky {
			for h := a.top; h >= 0; h-- {
				if ls[h].header {
					a.drawHeader(s, y, ls[h], others[ls[h].st])
					break
				}
			}
			continue
		}
		if l.header {
			a.drawHeader(s, y+i, l, others[l.st])
			continue
		}
		e := &a.view[l.idx]
		same := i > 0 && !(sticky && i == 1) && !ls[a.top+i-1].header && a.view[ls[a.top+i-1].idx].id.machine == e.id.machine
		a.drawRow(s, y+i, e, l.idx == a.selIdx, nameW, same)
	}
	if over {
		a.drawStrataMap(s, s.H-1, ls)
	}
}

// drawHeader peint le titre d'une marche sur une bande aux couleurs de la
// pyramide, d'autant plus large qu'on descend.
func (a *app) drawHeader(s *canvas.Canvas, y int, l line, others bool) {
	waiting := 0
	for i := l.idx; i < l.idx+l.count && i < len(a.view); i++ {
		if e := &a.view[i]; e.agent != nil && waitingEntry(e) {
			waiting++
		}
	}
	title := stratumTitle(l.st, l.count, others, waiting)
	tw := canvas.Width(title)
	k := float64(l.st)
	bw := int(float64(s.W) * (0.30 + 0.12*k))
	bw = min(max(bw, tw+16), s.W-2) // au moins six cases de couleur de chaque côté du titre
	x0 := (s.W - bw) / 2
	tx := (s.W - tw) / 2
	for x := 0; x < bw; x++ {
		t := float64(x) / float64(max(bw-1, 1))
		// les bords de la bande se fondent dans le noir
		fade := min(min(t, 1-t)*float64(bw)/5, 1)
		col := canvas.Mix(cGround, pyramidAt(t).Scale(0.8), 0.8*fade)
		// réserve de noir autour du titre
		if xx := x0 + x; xx >= tx-2 && xx < tx+tw+2 {
			col = cGround
		}
		s.Pixel(x0+x, 2*y, cGround)
		s.Pixel(x0+x, 2*y+1, col)
	}
	s.Text(tx, y, title, canvas.Style{Fg: canvas.Mix(stratumTint(l.st), cInk, 0.1), Bg: cGround, A: canvas.Bold})
}

// drawStrataMap résume toutes les marches sur une ligne, quand l'échelle
// dépasse : la marche de la sélection ressort, Tab passe à la suivante.
func (a *app) drawStrataMap(s *canvas.Canvas, y int, ls []line) {
	cur := stratum(-1)
	if e := a.selected(); e != nil {
		cur = e.stratum
	}
	var parts []line
	for _, l := range ls {
		if l.header {
			parts = append(parts, l)
		}
	}
	x := 2
	x += s.Text(x, y, "⇥ ", fg(cGoldDeep))
	for i, p := range parts {
		label := strconv.Itoa(p.count) + " " + shortStratum(p.st)
		if x+canvas.Width(label)+2 > s.W {
			break
		}
		if i > 0 {
			x += 2
		}
		st := fg(cInkDim)
		if p.st == cur {
			st = bold(stratumTint(p.st))
		}
		x += s.Text(x, y, label, st)
	}
}

func shortStratum(st stratum) string {
	switch st {
	case stAgents:
		return "agents"
	case stApps:
		return "applis"
	case stOpen:
		return "ouverts à tous"
	case stPorts:
		return "ports"
	case stRepos:
		return "dépôts"
	}
	return ""
}

// drawFilterLine dit ce qui restreint l'échelle : la recherche, la machine.
func (a *app) drawFilterLine(s *canvas.Canvas, y int) {
	x := 2
	if a.focus != "" {
		i := a.order(a.focus)
		x += s.Text(x, y, "seulement ", fg(cInkDim))
		x += s.Text(x, y, a.focus, bold(machineTint(i)))
		x += 3
	}
	if a.typing || a.query != "" {
		x += s.Text(x, y, "cherche ", fg(cInkDim))
		x += s.Text(x, y, a.query, bold(cGoldBright))
		if a.typing {
			// curseur : une graine d'or qui respire
			s.Set(x, y, '▏', fg(canvas.Mix(cGoldDeep, cGoldBright, a.breath())))
			x++
		}
		x += 2
		n := len(a.view)
		label := "aucune réponse"
		if n > 0 {
			label = plural(n, "réponse", "réponses")
		}
		x += s.Text(x, y, label, fg(cInkDim))
	}
	_ = x
}

// ---------------------------------------------------------------- chronique

// drawStrip peint la dernière nouvelle, en bas des petits écrans.
func (a *app) drawStrip(c *canvas.Canvas, y int) {
	news := a.news()
	if len(news) == 0 {
		c.Text(2, y, "rien n'a changé depuis l'ouverture", fg(cInkFaint))
		return
	}
	a.drawRecord(c, 2, y, c.W-3, 1, news[len(news)-1], true)
}

// news renvoie la chronique sans les premiers relevés, qui ne sont pas des
// nouvelles.
func (a *app) news() []record {
	out := make([]record, 0, len(a.w.chron))
	for _, r := range a.w.chron {
		if r.id.kind == fleet.KindMachine && r.op == model.OpAdd {
			continue
		}
		out = append(out, r)
	}
	return out
}

// drawRecord écrit une ligne de chronique : quand, où, quoi, sur n lignes au
// plus ; renvoie le nombre de lignes écrites.
func (a *app) drawRecord(c *canvas.Canvas, x, y, w, n int, r record, withAgo bool) int {
	end := x + w
	if withAgo {
		t := when(r.at, a.now, r.back)
		x += c.Text(x, y, t, fg(cInkDim)) + 2
	}
	if r.id.kind != fleet.KindMachine && !a.oneMachine() && x < end {
		i := a.order(r.id.machine)
		x += c.Text(x, y, canvas.Truncate(r.id.machine, min(end-x, max(12, w/4))), fg(machineTint(i)))
		x += c.Text(x, y, " · ", fg(cInkFaint))
	}
	if x >= end {
		return 1
	}
	col := toneColor(r.tone)
	if a.now.Sub(r.at) > recentTTL {
		col = canvas.Mix(col, cInkDim, 0.5)
	}
	ls := []string{squeeze(r.text, end-x)}
	if n > 1 {
		ls = wrapSqueezed(r.text, end-x, n)
	}
	for j, l := range ls {
		c.Text(x, y+j, l, fg(col))
	}
	return max(len(ls), 1)
}

// drawSide peint la colonne de chronique des grands écrans.
func (a *app) drawSide(c *canvas.Canvas, r rect) {
	s := c.Sub(r.x, r.y, r.w, r.h)
	s.Text(0, 0, "ce qui vient de changer", bold(cInkSoft))
	hint := "c tout"
	s.Text(s.W-canvas.Width(hint), 0, "c", fg(cGold))
	s.Text(s.W-canvas.Width(hint)+2, 0, "tout", fg(cInkDim))
	y := 2
	news := a.news()
	if len(news) == 0 {
		s.Text(0, y, canvas.Truncate("rien n'a changé depuis l'ouverture", s.W), fg(cInkFaint))
		return
	}
	for i := len(news) - 1; i >= 0 && y < s.H; i-- {
		rec := news[i]
		age := a.now.Sub(rec.at)
		head := when(rec.at, a.now, rec.back)
		x := s.Text(0, y, head, fg(cInkDim))
		if rec.id.kind != fleet.KindMachine && !a.oneMachine() {
			x += s.Text(x, y, " · ", fg(cInkFaint))
			s.Text(x, y, canvas.Truncate(rec.id.machine, s.W-x), fg(machineTint(a.order(rec.id.machine))))
		}
		y++
		col := toneColor(rec.tone)
		if age > recentTTL {
			col = canvas.Mix(col, cInkDim, 0.55)
		}
		for _, ln := range wrapSqueezed(rec.text, s.W-2, 2) {
			if y >= s.H {
				break
			}
			s.Text(2, y, ln, fg(col))
			y++
		}
		y++
	}
}

// wrap coupe s en lignes de w colonnes au plus, aux espaces, en n lignes au
// plus (la dernière finit par « … » s'il reste du texte).
func wrap(s string, w, n int) []string {
	if w <= 0 || n <= 0 {
		return nil
	}
	var out []string
	rs := []rune(s)
	for len(rs) > 0 && len(out) < n {
		if canvas.Width(string(rs)) <= w {
			out = append(out, string(rs))
			return out
		}
		if len(out) == n-1 {
			out = append(out, canvas.Truncate(string(rs), w))
			return out
		}
		cut, fits := 0, 0
		col := 0
		for i, r := range rs {
			col += canvas.RuneWidth(r)
			if col > w {
				break
			}
			fits = i + 1
			if r == ' ' || r == '/' {
				cut = i + 1
			}
		}
		if cut == 0 {
			cut = max(fits, 1) // pas de coupure possible : net, à la colonne
		}
		out = append(out, string(rs[:cut]))
		rs = rs[cut:]
		for len(rs) > 0 && rs[0] == ' ' {
			rs = rs[1:]
		}
	}
	return out
}

// ---------------------------------------------------------------- touches

type hintItem struct{ key, label string }

// drawHint peint la ligne du bas : les touches utiles là où on est, ou une
// remarque passagère.
func (a *app) drawHint(c *canvas.Canvas, y int) {
	if a.note != "" {
		// une remarque trop longue prend aussi la ligne du dessus
		n := 1
		if y > 0 && a.mode == modeMain {
			n = 2
		}
		lines := wrap(a.note, c.W-3, n)
		if len(lines) == 2 {
			c.Rect(0, y-1, c.W, 1, ' ', canvas.Style{Fg: cInk, Bg: cGround})
			c.Text(2, y-1, lines[0], fg(a.noteCol))
			lines = lines[1:]
		}
		if len(lines) > 0 {
			c.Text(2, y, lines[0], fg(a.noteCol))
		}
		return
	}
	items := a.hints()
	x := 2
	for _, it := range items {
		w := canvas.Width(it.key) + 1 + canvas.Width(it.label)
		if x+w > c.W-1 {
			continue
		}
		x += c.Text(x, y, it.key, bold(cGold))
		x += c.Text(x, y, " "+it.label, fg(cInkDim))
		x += 3
	}
}

func (a *app) hints() []hintItem {
	switch a.mode {
	case modeDetail:
		items := a.actionHints()
		return append(items, hintItem{"↑↓", "voisins"}, hintItem{"échap", "fermer"}, hintItem{"q", "quitter"})
	case modeConfirm:
		return []hintItem{{"⏎", "oui"}, {"échap", "non"}, {"q", "quitter"}}
	}
	if a.typing {
		return []hintItem{{"⏎", "garder"}, {"échap", "effacer"}, {"↑↓", "choisir"}, {"Q", "taper q"}, {"q", "quitter"}}
	}
	items := a.actionHints()
	items = append(items, hintItem{"⏎", "détail"})
	if a.query != "" || a.focus != "" {
		items = append(items, hintItem{"échap", "tout revoir"})
	}
	items = append(items, hintItem{"/", "chercher"}, hintItem{"?", "légende"}, hintItem{"q", "quitter"},
		hintItem{"←→", "machines"}, hintItem{"⇥", "marche suivante"}, hintItem{"c", "chronique"})
	return items
}

// actionHints propose les actions possibles sur la sélection.
func (a *app) actionHints() []hintItem {
	e := a.selected()
	if e == nil || e.ghost {
		return nil
	}
	switch {
	case e.app != nil:
		switch e.app.State {
		case model.AppRunning, model.AppStarting:
			return []hintItem{{"a", "arrêter"}, {"r", "relancer"}}
		default:
			return []hintItem{{"d", "démarrer"}}
		}
	case e.port != nil:
		return []hintItem{{"l", "libérer le port"}}
	}
	return nil
}

// breath est la respiration lente de l'or, entre 0 et 1 (période 4 s).
func (a *app) breath() float64 {
	t := a.now.Sub(a.born).Seconds()
	return 0.5 + 0.5*math.Sin(t*2*math.Pi/4)
}

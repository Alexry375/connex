package ui

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// Le cartouche est l'inscription au pied du retable : le détail d'un élément,
// ou la question avant d'agir. Il se pose sur l'échelle, dans le même noir un
// peu soulevé, tenu à gauche par la colonne de graines d'or de la pyramide.

// cline est une ligne de cartouche.
type cline []seg

func text1(s string, col canvas.Color) cline { return cline{{s: s, col: col}} }

// cartouche peint des lignes au bas de l'écran, sur toute la largeur, au-dessus
// de la ligne des touches. Le texte garde une largeur de lecture ; à gauche,
// une tranche d'or où une lueur descend lentement.
func (a *app) cartouche(c *canvas.Canvas, l layout, lines []cline) {
	w := min(c.W-8, 96)
	var rows []cline
	for _, ln := range lines {
		rows = append(rows, wrapLine(ln, w)...)
	}
	h := len(rows) + 2
	if maxH := c.H - 3; h > maxH {
		rows = rows[:max(maxH-2, 1)]
		h = len(rows) + 2
	}
	y0 := max(l.hint-h, 1)
	bg := cGround2
	for y := y0; y < y0+h; y++ {
		for x := 0; x < c.W; x++ {
			c.Set(x, y, ' ', canvas.Style{Fg: cInk, Bg: bg})
		}
	}
	// bord du haut : un filet d'or pâle sur une demi-case
	for x := 0; x < c.W; x++ {
		t := float64(x) / float64(max(c.W-1, 1))
		c.Pixel(x, 2*y0-1, canvas.Mix(cGround, pyramidAt(t).Scale(0.7), 0.35))
	}
	t := a.now.Sub(a.born).Seconds()
	span := float64(2*h + 8)
	glow := math.Mod(t*5, span) - 4
	for py := 2 * y0; py < 2*(y0+h); py++ {
		d := float64(py-2*y0) - glow
		c.Pixel(2, py, canvas.Mix(cGoldDeep, cGoldBright, 0.85*math.Exp(-d*d/6)))
	}
	for i, ln := range rows {
		writeSegs(c, 5, y0+1+i, 5+w, ln)
	}
}

// wrapLine coupe une ligne de cartouche trop longue : le texte continue sur
// la ligne suivante, en retrait. Un mot plus long que la ligne entière (un
// dossier sans espace ni barre) est coupé net, à la colonne.
func wrapLine(ln cline, w int) []cline {
	total := 0
	for _, s := range ln {
		total += canvas.Width(s.s)
	}
	if total <= w || w < 4 {
		return []cline{ln}
	}
	var out []cline
	cur := cline{}
	col := 0
	bare := true // la ligne en cours ne porte encore que son retrait
	for _, s := range ln {
		rest := s.s
		for rest != "" {
			room := w - col
			if canvas.Width(rest) <= room {
				cur = append(cur, seg{s: rest, col: s.col, bold: s.bold})
				col += canvas.Width(rest)
				bare = false
				break
			}
			// coupe à un espace ou à une barre
			head := cutAt(rest, room)
			if head == "" {
				if !bare {
					out = append(out, cur)
					cur, col, bare = cline{{s: "  ", col: s.col}}, 2, true
					rest = trimLeftSpace(rest)
					continue
				}
				head = headCols(rest, room)
				if head == "" {
					_, n := utf8.DecodeRuneInString(rest)
					head = rest[:n]
				}
			}
			cur = append(cur, seg{s: head, col: s.col, bold: s.bold})
			out = append(out, cur)
			cur, col, bare = cline{{s: "  ", col: s.col}}, 2, true
			rest = trimLeftSpace(rest[len(head):])
		}
	}
	if !bare {
		out = append(out, cur)
	}
	return out
}

// cutAt renvoie le plus long début de s qui tient en w colonnes et finit à un
// espace ou après une barre ; vide s'il n'y en a pas.
func cutAt(s string, w int) string {
	best := ""
	col := 0
	for i, r := range s {
		col += canvas.RuneWidth(r)
		if col > w {
			break
		}
		if r == ' ' || r == '/' {
			best = s[:i+len(string(r))]
		}
	}
	return best
}

func trimLeftSpace(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	return s
}

// drawDetail peint le cartouche de l'élément choisi.
func (a *app) drawDetail(c *canvas.Canvas, l layout) {
	e := a.selected()
	if e == nil {
		return
	}
	a.cartouche(c, l, a.detailLines(e))
}

// detailLines décrit un élément en entier, en phrases.
func (a *app) detailLines(e *entry) []cline {
	m := e.machine
	home := e.home()
	var out []cline
	kind := ""
	name := ""
	switch {
	case e.agent != nil:
		kind, name = "agent", e.agent.Tool
	case e.app != nil:
		kind, name = "appli", e.app.Name
	case e.port != nil:
		kind, name = "port", "port "+strconv.Itoa(e.port.Port)
	case e.repo != nil:
		kind, name = "dépôt", projectName(e.repo.Path)
	}
	out = append(out, cline{{s: name, col: cInk, bold: true}, {s: "  " + kind + " sur ", col: cInkDim},
		{s: m.Name, col: machineTint(e.order)}})
	if !m.Reachable {
		why := ""
		if r := a.w.reason[m.Name]; r != "" {
			why = " (" + r + ")"
		}
		out = append(out, text1(m.Name+" est injoignable "+since(a.now.Sub(m.Since))+why+" : voici le dernier état connu", cRed))
	}
	if e.ghost {
		out = append(out, text1("disparu "+when(e.goneAt, a.now, e.goneBack)+" : voici son dernier état connu", cInkDim))
	}
	switch {
	case e.agent != nil:
		out = append(out, a.agentDetail(e, home)...)
	case e.app != nil:
		out = append(out, a.appDetail(e, home)...)
	case e.port != nil:
		out = append(out, a.portDetail(e, home)...)
	case e.repo != nil:
		out = append(out, repoDetail(e, home)...)
	}
	if p := a.w.pendingFor(e.id); p != nil {
		if el := a.now.Sub(p.at); el >= silentAfter {
			out = append(out, text1(opProgress(p.op)+" : sans réponse depuis "+span(el), cGold))
		} else {
			out = append(out, text1(opProgress(p.op)+" en cours depuis "+seconds(el)+"…", cGold))
		}
	} else if o, ok := a.w.results[e.id]; ok {
		if o.ok {
			out = append(out, text1(actionPhrase(o.op, o.target, o.machine)+" : fait en "+seconds(o.done.Sub(o.at)), cSage))
		} else {
			out = append(out, text1(actionPhrase(o.op, o.target, o.machine)+" : refusé — "+orWord(o.err, "sans raison donnée"), cRed))
		}
	} else if mk, ok := a.w.recent[e.id]; ok {
		out = append(out, text1("dernier changement "+when(mk.at, a.now, mk.back), cInkDim))
	}
	return out
}

func (a *app) agentDetail(e *entry, home string) []cline {
	ag := e.agent
	_, col := agentGlyph(ag.State)
	st := cline{{s: agentWord(ag.State), col: col, bold: ag.State == model.StateWaiting}}
	if t, ok := a.w.since[e.id]; ok {
		st = append(st, seg{s: " " + since(a.now.Sub(t)), col: cInkSoft}, seg{s: " (vu par connex)", col: cInkDim})
	}
	out := []cline{st}
	if ag.CWD != "" {
		out = append(out, cline{{s: "dossier ", col: cInkDim}, {s: shortPath(ag.CWD, home), col: cInk}})
	} else {
		out = append(out, text1("dossier inconnu", cInkDim))
	}
	out = append(out, cline{{s: "pid ", col: cInkDim}, {s: strconv.Itoa(ag.PID), col: cInkSoft}})
	out = append(out, text1("connex montre les agents ; il n'agit pas sur eux", cInkFaint))
	return out
}

func (a *app) appDetail(e *entry, home string) []cline {
	ap := e.app
	h := ap.Health
	st := cline{{s: appWord(ap.State), col: appColor(ap.State), bold: true}}
	switch {
	case h.HTTP >= 400:
		st = append(st, seg{s: " · répond " + strconv.Itoa(h.HTTP), col: cOrange})
	case h.HTTP > 0:
		st = append(st, seg{s: " · répond " + strconv.Itoa(h.HTTP), col: cInkSoft})
	default:
		st = append(st, seg{s: " · pas de réponse HTTP", col: cInkDim})
	}
	if ap.Port != 0 {
		listen := "en écoute"
		if !h.PortListening {
			listen = "pas en écoute"
		}
		st = append(st, seg{s: " · port " + strconv.Itoa(ap.Port) + " " + listen, col: cInkSoft})
	}
	out := []cline{st}
	proc := "processus principal vivant"
	if !h.PIDAlive {
		proc = "pas de processus principal"
	}
	out = append(out, cline{{s: "unité ", col: cInkDim}, {s: orWord(ap.Unit, "inconnue"), col: cInkSoft}, {s: " · " + proc, col: cInkDim}})
	if ap.Project != "" {
		out = append(out, cline{{s: "projet ", col: cInkDim}, {s: shortPath(ap.Project, home), col: cInk}})
	}
	if ap.CWD != "" && ap.CWD != ap.Project {
		out = append(out, cline{{s: "lancée dans ", col: cInkDim}, {s: shortPath(ap.CWD, home), col: cInk}})
	}
	switch ap.View {
	case model.ViewRemote:
		out = append(out, text1("se regarde par Tailscale, sur la machine", cInkDim))
	case model.ViewTrain:
		out = append(out, text1("se regarde par le miroir apercu-vps", cInkDim))
	}
	for _, p := range appProblems(e) {
		out = append(out, text1(p, cOrange))
	}
	if ap.Port != 0 && !e.ghost {
		for _, f := range portFacts(e.machine, ap.Port) {
			if h.PortListening || ap.State != model.AppRunning {
				out = append(out, text1("port "+strconv.Itoa(ap.Port)+" — "+f, cInkDim))
			}
		}
	}
	return out
}

func (a *app) portDetail(e *entry, home string) []cline {
	l := e.port
	out := []cline{{
		{s: exposureWord(l.Exposure), col: exposureColor(l.Exposure), bold: true},
		{s: " — " + exposureLong(l.Exposure), col: cInkSoft},
	}}
	out = append(out, cline{{s: "adresse ", col: cInkDim}, {s: l.Addr, col: cInkSoft}})
	if l.Mine {
		who := cline{{s: "tenu par ", col: cInkDim}, {s: orWord(l.Process, "programme inconnu"), col: cInk, bold: true}}
		if l.PID != 0 {
			who = append(who, seg{s: " · pid " + strconv.Itoa(l.PID), col: cInkSoft})
		}
		who = append(who, seg{s: " · ton compte", col: cInkDim})
		out = append(out, who)
		if l.CWD != "" {
			out = append(out, cline{{s: "dossier ", col: cInkDim}, {s: shortPath(l.CWD, home), col: cInk}})
		}
		if l.ProjectRoot != "" && l.ProjectRoot != l.CWD {
			out = append(out, cline{{s: "projet ", col: cInkDim}, {s: shortPath(l.ProjectRoot, home), col: cInk}})
		}
	} else {
		out = append(out, cline{{s: "tenu par un autre compte", col: cInk}, {s: " · uid " + strconv.Itoa(l.UID), col: cInkSoft}})
		out = append(out, text1("programme et dossier inconnus : connex ne voit pas les processus des autres comptes", cInkDim))
	}
	for _, n := range portNeighbours(e) {
		out = append(out, text1(n+" (même port, autre adresse)", cOrange))
	}
	if !e.ghost {
		for _, ap := range e.machine.Snapshot.Apps {
			if ap.Port == l.Port {
				out = append(out, text1("port réservé à l'appli "+ap.Name+" ("+appWord(ap.State)+")", cInkSoft))
			}
		}
	}
	return out
}

func repoDetail(e *entry, home string) []cline {
	r := e.repo
	state := cline{{s: branchWord(r.Branch, r.Detached), col: cInk, bold: true}}
	switch {
	case r.Dirty == nil:
		state = append(state, seg{s: " · état inconnu", col: cInkFaint})
	case *r.Dirty:
		state = append(state, seg{s: " · modifié", col: cOrange})
	default:
		state = append(state, seg{s: " · propre", col: cInkSoft})
	}
	out := []cline{state, {{s: "chemin ", col: cInkDim}, {s: shortPath(r.Path, home), col: cInk}}}
	for _, wt := range r.Worktrees {
		out = append(out, cline{{s: "worktree ", col: cInkDim}, {s: branchWord(wt.Branch, wt.Detached), col: cInkSoft},
			{s: " · " + shortPath(wt.Path, home), col: cInk}})
	}
	return out
}

// drawConfirm peint la question avant d'agir.
func (a *app) drawConfirm(c *canvas.Canvas, l layout) {
	q := a.ask
	if q == nil {
		return
	}
	lines := []cline{{{s: q.title, col: cInk, bold: true}}}
	for _, f := range q.facts {
		lines = append(lines, text1(f, cInkSoft))
	}
	lines = append(lines, cline{},
		cline{{s: "⏎", col: cGold, bold: true}, {s: " oui, " + q.yes + "     ", col: cInkSoft},
			{s: "échap", col: cGold, bold: true}, {s: " non", col: cInkSoft}})
	a.cartouche(c, l, lines)
}

// ---------------------------------------------------------------- légende

// helpItem est une ligne de la légende : une touche ou un signe, et son sens.
type helpItem struct {
	k, v string
	col  canvas.Color // couleur de la touche ; zéro : l'or des touches
}

type helpSection struct {
	name  string
	items []helpItem
}

var helpSections = []helpSection{
	{"se déplacer", []helpItem{{k: "↑ ↓", v: "choisir une ligne (ou j k)"}, {k: "⇥", v: "marche suivante de l'échelle"},
		{k: "← →", v: "une machine après l'autre, puis toutes"}, {k: "⏎", v: "le détail de la ligne"}, {k: "échap", v: "revenir, effacer"}}},
	{"chercher", []helpItem{{k: "/", v: "un port, un projet, un programme, une machine, un état"},
		{k: "0-9", v: "taper un nombre cherche un port tout de suite"}}},
	{"agir", []helpItem{{k: "d a r", v: "démarrer, arrêter, relancer une appli"}, {k: "l", v: "libérer un port"},
		{k: "R", v: "demander un relevé neuf à la machine"}, {v: "arrêter, relancer et libérer demandent confirmation"}}},
	{"voir", []helpItem{{k: "c", v: "la chronique : tout ce qui a changé"}, {k: "?", v: "cette légende"}, {k: "q", v: "quitter (Ctrl-C aussi)"}}},
	{"signes", []helpItem{{k: "◉ ◐ ○ ◌", v: "agent : t'attend, travaille, au repos, état inconnu"},
		{k: "● ◔ ○ ✕", v: "appli : en marche, démarre, arrêtée, en échec"},
		{k: "◆ ◇", v: "dépôt : modifié, propre"},
		{k: "vermillon", v: "ouvert à tous : joignable de partout où la machine l'est", col: cVermilion},
		{k: "soleil", v: "un rayon par agent ; il s'allume quand un agent t'attend"},
		{k: "astres", v: "une machine chacun ; éclipsé quand elle est injoignable"}}},
}

// helpGutter est la colonne où commence le sens d'une touche.
const helpGutter = 12

// helpLines met des sections de la légende en lignes de largeur w ; un sens
// trop long continue sous lui-même.
func helpLines(secs []helpSection, w int) []cline {
	var out []cline
	for i, s := range secs {
		if i > 0 {
			out = append(out, nil)
		}
		out = append(out, cline{{s: s.name, col: cInkSoft, bold: true}})
		for _, it := range s.items {
			kc := it.col
			if kc == 0 {
				kc = cGold
			}
			for j, p := range wrap(it.v, max(w-helpGutter, 8), 8) {
				key := ""
				if j == 0 {
					key = it.k
				}
				pad := strings.Repeat(" ", max(helpGutter-2-canvas.Width(key), 1))
				out = append(out, cline{{s: "  " + key, col: kc, bold: true}, {s: pad, col: cInkSoft}, {s: p, col: cInkSoft}})
			}
		}
	}
	return out
}

// drawHelp peint la légende : sous le soleil s'il reste la place, en deux
// colonnes quand l'écran est large ; ↑↓ font défiler ce qui dépasse.
func (a *app) drawHelp(c *canvas.Canvas) {
	l := computeLayout(c.W, c.H)
	colW := min(c.W-4, 72)
	cols := [][]cline{}
	if c.W >= 110 {
		colW = min((c.W-10)/2, 64)
		cols = append(cols, helpLines(helpSections[:3], colW), helpLines(helpSections[3:], colW))
	} else {
		cols = append(cols, helpLines(helpSections, colW))
	}
	need := 0
	for _, col := range cols {
		need = max(need, len(col))
	}
	credit := wrap("Formes et couleurs d'après Hilma af Klint : Les Dix Plus Grands n° 3, 7 et 9 (1907), Retable n° 1 (1915).", c.W-4, 2)
	bottom := c.H - 1 - len(credit) // première ligne du crédit
	top := 0
	if !l.narrow && l.tympH+3+need < bottom {
		a.drawSun(c, l)
		top = l.tympH + 1
	}
	title := "légende"
	c.Text((c.W-canvas.Width(title))/2, top, title, bold(cGold))
	y0 := top + 2
	rows := max(bottom-1-y0, 1)
	a.helpOver = need > rows
	a.helpTop = min(max(a.helpTop, 0), max(need-rows, 0))
	const gap = 6
	totalW := len(cols)*colW + (len(cols)-1)*gap
	x0 := max((c.W-totalW)/2, 1)
	for i, col := range cols {
		x := x0 + i*(colW+gap)
		for j := 0; j < rows && a.helpTop+j < len(col); j++ {
			ln := col[a.helpTop+j]
			if j == rows-1 && a.helpTop+j+1 < len(col) && len(ln) == 1 && ln[0].bold {
				break // un titre de section sans rien dessous attend le défilement
			}
			writeSegs(c, x, y0+j, x+colW, ln)
		}
	}
	for i, ln := range credit {
		c.Text(max((c.W-canvas.Width(ln))/2, 1), bottom+i, ln, fg(cInkDim))
	}
	hint := "une touche pour revenir"
	if a.helpOver {
		hint = "↑↓ la suite · une autre touche pour revenir"
	}
	c.Text(2, c.H-1, hint, fg(cInkFaint))
}

// ---------------------------------------------------------------- chronique

func (a *app) drawChronicle(c *canvas.Canvas) {
	c.Text(2, 0, "chronique", bold(cGold))
	sub := "depuis l'ouverture de connex, du plus récent au plus ancien"
	c.Text(14, 0, canvas.Truncate(sub, c.W-16), fg(cInkDim))
	n := len(a.w.chron)
	if n == 0 {
		c.Text(2, 2, "rien n'a encore changé", fg(cInkFaint))
	}
	y := 2
	for i := 0; y < c.H-1; i++ {
		k := n - 1 - a.chronTop - i
		if k < 0 {
			break
		}
		r := a.w.chron[k]
		age := when(r.at, a.now, r.back)
		ax := max(16-canvas.Width(age), 0)
		rx := max(18, ax+canvas.Width(age)+2)
		lines := 1
		if i == 0 {
			// la ligne choisie s'écrit entière, sur trois lignes au plus : un
			// essai à blanc dit combien, pour poser le fond d'abord
			lines = a.drawRecord(canvas.New(c.W, 3), rx, 0, c.W-1-rx, min(3, c.H-1-y), r, false)
			for dy := range lines {
				for x := 0; x < c.W; x++ {
					c.Set(x, y+dy, ' ', canvas.Style{Bg: cGround3})
				}
				c.Set(0, y+dy, '▌', fg(cGold))
			}
		}
		c.Text(ax, y, age, fg(cInkDim))
		a.drawRecord(c, rx, y, c.W-1-rx, lines, r, false)
		y += lines
	}
	hint := []hintItem{{"↑↓", "parcourir"}, {"⏎", "aller à l'élément"}, {"échap", "revenir"}, {"q", "quitter"}}
	x := 2
	for _, it := range hint {
		x += c.Text(x, c.H-1, it.key, bold(cGold))
		x += c.Text(x, c.H-1, " "+it.label, fg(cInkDim)) + 3
	}
}

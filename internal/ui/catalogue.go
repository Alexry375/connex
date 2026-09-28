package ui

import (
	"strconv"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// Le catalogue est la légende gravée sous l'horizon : une ligne par objet, le
// nom à gauche, l'état à droite, des points de conduite entre les deux.

// seg est un morceau de ligne.
type seg struct {
	s  string
	fg canvas.Color
	a  canvas.Attr
}

// itemLine est une ligne du catalogue avant mise en place.
type itemLine struct {
	sign   rune
	signFG canvas.Color
	name   seg
	detail []seg  // coupés en premier
	path   string // raccourci par fitPath
	right  []seg  // l'état, puis des mentions « · … »
	tag    seg    // la machine, dans une recherche
}

// groupOf donne le nom de groupe d'un élément, en marge du catalogue.
func groupOf(k kind) string {
	switch k {
	case kindError:
		return "erreurs"
	case kindAgent:
		return "agents"
	case kindApp:
		return "applis"
	case kindPort, kindForeign:
		return "ports"
	case kindRepo:
		return "dépôts"
	}
	return ""
}

// lineFor compose la ligne d'un élément.
func (a *App) lineFor(it *item, now time.Time) itemLine {
	var ln itemLine
	switch it.kind {
	case kindError:
		// Le dossier en cause distingue les erreurs entre elles ; la raison
		// se coupe s'il faut, et se lit entière sur la planche.
		ln.sign, ln.signFG = '✕', red
		from := errorSourceFR(it.err.Source)
		path, reason := errorParts(it.err.Message)
		if path != "" {
			ln.name = seg{baseName(path), ink, canvas.Bold}
			ln.detail = []seg{{from, inkDim, canvas.Italic}}
			ln.path = shortPath(path, it.home)
		} else {
			ln.name = seg{from, ink, 0}
		}
		ln.right = []seg{{reason, inkDim, canvas.Italic}}

	case kindAgent:
		ag := it.agent
		ln.sign, ln.signFG = '☄', ink
		ln.name = seg{baseName(ag.CWD), ink, canvas.Bold}
		ln.detail = []seg{{ag.Tool, inkDim, 0}}
		ln.path = shortPath(ag.CWD, it.home)
		st := seg{agentStateFR(ag.State), inkDim, canvas.Italic}
		switch ag.State {
		case model.StateWaiting:
			ln.sign, ln.signFG = '✦', gold
			ln.name.fg = gold
			st = seg{"t’attend", gold, canvas.Bold}
		case model.StateWorking:
			st.fg, st.a = ink, 0
		case model.StateUnknown:
			ln.signFG, st.fg = inkFaint, inkFaint
		default:
			ln.signFG = inkDim
		}
		ln.right = []seg{st}

	case kindApp:
		ap := it.app
		ln.name = seg{ap.Name, ink, canvas.Bold}
		if ap.Port > 0 {
			ln.detail = []seg{{":" + strconv.Itoa(ap.Port), inkDim, 0}}
		}
		dir := ap.CWD
		if dir == "" {
			dir = ap.Project
		}
		ln.path = shortPath(dir, it.home)
		st := seg{appStateFR(ap.State), inkDim, canvas.Italic}
		switch ap.State {
		case model.AppRunning:
			ln.sign, ln.signFG = '✧', mint
			st = seg{"en marche", mint, 0}
		case model.AppStarting:
			ln.sign, ln.signFG = '◌', gold
			st = seg{"démarre", gold, canvas.Italic}
		case model.AppStopped:
			ln.sign, ln.signFG = '○', inkDim
		case model.AppFailed:
			ln.sign, ln.signFG = '✕', red
			st = seg{"en échec", red, canvas.Bold}
		default:
			ln.sign, ln.signFG = '○', inkFaint
		}
		ln.right = []seg{st}
		if ap.State == model.AppRunning && ap.Health.HTTP >= 400 {
			ln.right = append(ln.right, seg{" · " + httpFR(ap.Health.HTTP), red, 0})
			ln.signFG = salmon
		}
		if it.appPort != nil && it.appPort.Exposure == model.ExposureAll {
			ln.right = append(ln.right, seg{" · " + listenerExposureFR(it.appPort), salmon, 0})
		}
		if it.appPort == nil && it.holder != nil && ap.State != model.AppRunning {
			who := "un autre compte"
			if it.holder.Mine {
				who = it.holder.Program()
			}
			ln.right = append(ln.right, seg{" · :" + strconv.Itoa(ap.Port) + " pris par " + who, gold, 0})
		}
		if len(it.sharers) > 0 {
			ln.right = append(ln.right, seg{" · port partagé", gold, 0})
		}

	case kindPort, kindForeign:
		l := it.port
		ln.name = seg{strconv.Itoa(l.Port), ink, canvas.Bold}
		if l.Mine {
			ln.sign, ln.signFG = '•', inkDim
			ln.detail = []seg{{l.Program(), ink, 0}}
			dir := l.ProjectRoot
			if dir == "" {
				dir = l.CWD
			}
			ln.path = shortPath(dir, it.home)
		} else {
			ln.sign, ln.signFG = '◇', inkFaint
			ln.name.fg = inkDim
			ln.detail = []seg{{"autre compte", inkFaint, canvas.Italic}, {"· uid " + strconv.Itoa(l.UID), inkFaint, canvas.Italic}}
		}
		ex := seg{listenerExposureFR(l), inkFaint, 0}
		switch l.Exposure {
		case "all":
			ex.fg = salmon
			ln.signFG = salmon
		case "tailnet":
			ex.fg = blue
		}
		ln.right = []seg{ex}
		if len(it.sharers) > 0 {
			ln.right = append(ln.right, seg{" · port partagé", gold, 0})
		}

	case kindRepo:
		r := it.repo
		ln.name = seg{baseName(r.Path), ink, 0}
		ln.detail = []seg{{branchFR(r.Branch, r.Detached), inkDim, canvas.Italic}}
		if n := len(r.Worktrees); n > 0 {
			ln.detail = append(ln.detail, seg{" +" + plural(n, "worktree", "worktrees"), inkFaint, 0})
		}
		ln.sign, ln.signFG = '◆', inkFaint
		st := seg{dirtyFR(r.Dirty), inkFaint, canvas.Italic}
		if r.Dirty != nil && *r.Dirty {
			ln.signFG, st = goldDim, seg{"modifié", goldDim, 0}
		}
		ln.right = []seg{st}
		ahead, behind := syncFR(r)
		if ahead != "" {
			ln.right = append(ln.right, seg{" · " + ahead, goldDim, 0})
		}
		if behind != "" {
			ln.right = append(ln.right, seg{" · " + behind, inkDim, 0})
		}
		if it.agentsIn > 0 {
			ln.right = append(ln.right, seg{" · " + plural(it.agentsIn, "agent", "agents"), inkDim, 0})
		}
	}

	// Une action en cours ou qui vient de finir prend la place de l'état.
	if req := a.requestFor(it.id, now, resultShown); req != nil {
		ln.right = requestSegs(req, now)
	}
	return ln
}

// resultShown est le temps pendant lequel le résultat d'une action reste sur sa ligne.
const resultShown = 10 * time.Second

var spinner = [...]rune{'◜', '◝', '◞', '◟'}

// requestSegs dit où en est une action : en cours, réussie, restée sans
// réponse ou refusée.
func requestSegs(r *request, now time.Time) []seg {
	switch {
	case !r.done:
		el := max(0, now.Sub(r.sent)) // une image datée d'avant l'envoi ne tourne pas à l'envers
		sp := spinner[int(el/(120*time.Millisecond))%len(spinner)]
		return []seg{{string(sp) + " " + opPendingFR(r.op) + "… " + secondsFR(el), gold, 0}}
	case r.ok:
		return []seg{{"✓ " + opDoneFR(r.op), mint, canvas.Bold}}
	case r.lost:
		return []seg{{"◐ sans réponse : " + r.reason, salmon, 0}}
	}
	return []seg{{"✕ refusé : " + orUnknown(r.reason), red, 0}}
}

// drawLine met une ligne en place sur w colonnes à partir de (x, y). Quand la
// place manque, on garde dans l'ordre : le nom, l'état, la machine (dans une
// recherche), le premier détail, les autres mentions de droite, les autres
// détails, puis le chemin, raccourci au milieu. Un morceau qui ne tient pas
// entier est omis plutôt que coupé ; seuls le nom et l'état se coupent.
func drawLine(cv *canvas.Canvas, x, y, w int, ln itemLine, dim bool, bg canvas.Color) {
	if w < 6 {
		return
	}
	fade := func(c canvas.Color) canvas.Color {
		if dim {
			return canvas.Mix(c, inkGhost, 0.55)
		}
		return c
	}
	put := func(x, max int, s string, fg canvas.Color, a canvas.Attr) int {
		if bg.IsSet() {
			return cv.TextBG(x, y, max, s, fade(fg), bg, a)
		}
		return cv.Text(x, y, max, s, fade(fg), a)
	}
	sw := canvas.StringWidth
	end := x + w
	if bg.IsSet() {
		cv.Fill(x, y, w, 1, bg)
	}
	put(x, end, string(ln.sign), ln.signFG, canvas.Bold)
	avail := w - 2
	const gap = 2 // un blanc de part et d'autre des points de conduite

	// Le nécessaire : nom, état, machine. Le nom garde au moins 40 % de la
	// ligne (ou sa largeur) ; l'état se coupe s'il faut.
	name := ln.name.s
	right := make([]bool, len(ln.right))
	state := seg{}
	if len(ln.right) > 0 {
		state, right[0] = ln.right[0], true
	}
	// La machine d'un résultat garde au plus le tiers de la ligne : un nom
	// d'hôte de 32 lettres ne pousse ni le nom ni l'état hors de leur place.
	tag := ln.tag
	if sw(tag.s) > avail/3 {
		tag.s = canvas.Truncate(tag.s, avail/3)
	}
	tw := sw(tag.s)
	nameMin := min(sw(name), max(4, avail*4/10))
	if room := avail - nameMin - gap - tw; sw(state.s) > room {
		state.s = canvas.Truncate(state.s, max(1, room))
	}
	used := sw(state.s) + tw + gap
	if sw(name) > avail-used {
		name = canvas.Truncate(name, max(1, avail-used))
	}
	used += sw(name)

	// Le reste, par ordre d'importance, tant qu'il tient entier.
	details := make([]bool, len(ln.detail))
	fits := func(n int) bool {
		if used+n > avail {
			return false
		}
		used += n
		return true
	}
	if len(ln.detail) > 0 {
		details[0] = fits(1 + sw(ln.detail[0].s))
	}
	for i := 1; i < len(ln.right); i++ {
		right[i] = fits(sw(ln.right[i].s))
	}
	for i := 1; i < len(ln.detail); i++ {
		details[i] = fits(1 + sw(ln.detail[i].s))
	}
	// Le chemin se raccourcit par le milieu, mais garde son dernier dossier entier.
	path := ""
	if room := avail - used - 1; ln.path != "" && room >= min(sw(ln.path), sw(baseName(ln.path))+2) {
		path = fitPath(ln.path, room)
		used += 1 + sw(path)
	}

	// Mise en place : nom, détails, chemin, points de conduite, état, machine.
	cx := put(x+2, end, name, ln.name.fg, ln.name.a)
	for i, d := range ln.detail {
		if details[i] {
			cx = put(cx+1, end, d.s, d.fg, d.a)
		}
	}
	if path != "" {
		cx = put(cx+1, end, path, inkFaint, 0)
	}
	rw := tw
	for i, s := range ln.right {
		if right[i] {
			if i == 0 {
				s = state
			}
			rw += sw(s.s)
		}
	}
	rx := max(end-rw, cx+1)
	for i := cx + 1; i < rx-1; i++ {
		put(i, i+1, "·", inkGhost, 0)
	}
	for i, s := range ln.right {
		if right[i] {
			if i == 0 {
				s = state
			}
			rx = put(rx, end, s.s, s.fg, s.a)
		}
	}
	if tag.s != "" {
		put(rx, end, tag.s, tag.fg, tag.a)
	}
}

// catRow est une rangée du catalogue : un titre de groupe ou un élément.
type catRow struct {
	head string
	it   *item
}

// catRows range les éléments ; avec heads, un titre ouvre chaque groupe.
func catRows(items []*item, heads bool) []catRow {
	out := make([]catRow, 0, len(items)+6)
	last := ""
	for _, it := range items {
		g := groupOf(it.kind)
		if heads && g != last {
			out = append(out, catRow{head: g})
		}
		last = g
		out = append(out, catRow{it: it})
	}
	return out
}

// catView décrit un catalogue à dessiner.
type catView struct {
	x, y, w, rows int
	items         []*item
	key           string // clé de défilement
	selected      string // élément choisi ("" : aucun)
	active        bool   // la sélection est celle qu'on déplace
	gutter        bool   // titres de groupe en marge plutôt qu'en ligne
	machineTag    bool   // nom de la machine en fin de ligne (recherche)
	dim           bool   // dernier état connu d'une machine injoignable
}

// flowMin : à partir de cette largeur, un catalogue se lit en deux colonnes,
// comme un journal (une seule machine en plein écran) plutôt qu'en lignes de
// 150 caractères.
const flowMin = 150

// drawCatalogue dessine un catalogue et fait défiler pour garder l'élément
// choisi en vue.
func (a *App) drawCatalogue(cv *canvas.Canvas, v catView, now time.Time) {
	if v.rows <= 0 || v.w < 10 {
		return
	}
	rows := catRows(v.items, !v.gutter)
	cols, sep := 1, 0
	if v.w >= flowMin {
		cols, sep = 2, 3 // trois blancs de plus entre les colonnes
	}
	colW := (v.w - sep*(cols-1)) / cols
	page := v.rows * cols
	sel := -1
	for i, r := range rows {
		if r.it != nil && r.it.id == v.selected {
			sel = i
		}
	}
	top := a.scroll[v.key]
	if sel >= 0 {
		if sel < top {
			top = sel
			if top > 0 && rows[top-1].head != "" {
				top--
			}
		}
		if sel >= top+page {
			top = sel - page + 1
		}
	}
	top = max(0, min(top, len(rows)-page))
	a.scroll[v.key] = top

	gw := 0
	if v.gutter {
		gw = 8
	}
	lastGroup := ""
	for i := 0; i < page && top+i < len(rows); i++ {
		r := rows[top+i]
		x0, y := v.x+i/v.rows*(colW+sep), v.y+i%v.rows
		if i%v.rows == 0 {
			lastGroup = "" // le groupe se redit en tête de chaque colonne
		}
		if r.head != "" {
			cv.Text(x0+1, y, x0+colW, r.head, inkFaint, canvas.Italic)
			continue
		}
		it := r.it
		if v.gutter {
			if g := groupOf(it.kind); g != lastGroup {
				cv.Text(x0, y, x0+gw-1, g, inkFaint, canvas.Italic)
				lastGroup = g
			}
		}
		ln := a.lineFor(it, now)
		if v.machineTag {
			ln.tag = seg{" · " + it.m.Name, inkFaint, canvas.Italic}
		}
		var bg canvas.Color
		if t, ok := a.fresh[it.id]; ok && now.Sub(t) < haloLife {
			k := 1 - now.Sub(t).Seconds()/haloLife.Seconds()
			bg = canvas.Mix(landBG, canvas.Hex(0x2a3a2c), k*0.9)
		}
		if it.id == v.selected {
			if v.active {
				bg = selectBG
			} else {
				bg = selectBGDim
			}
		}
		lx := x0 + gw
		if !v.gutter {
			lx = x0 + 1
		}
		// Les deux dernières colonnes restent aux flèches de défilement,
		// séparées du texte par un blanc.
		drawLine(cv, lx, y, x0+colW-2-lx, ln, v.dim && it.id != v.selected, bg)
	}
	// Ce qui dépasse, en haut de la première colonne et en bas de la dernière.
	if top > 0 {
		cv.Text(v.x+colW-1, v.y, v.x+colW, "↑", inkFaint, 0)
	}
	if top+page < len(rows) {
		x := v.x + (cols-1)*(colW+sep)
		cv.Text(x+colW-1, v.y+v.rows-1, x+colW, "↓", inkFaint, 0)
	}
}

package ui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/ui/canvas"
)

// La marge de papier, en bas de chaque planche, comme sous les gravures de
// Trouvelot : numéro de planche, titre en capitales, signature ; puis les
// touches utiles, la question d'une confirmation ou la recherche en cours.

// hint est une touche proposée dans la marge.
type hint struct {
	key, label string
	prio       int // les plus petits restent quand la place manque
}

// drawPaper dessine la marge de la planche n° plate sur rows rangées à partir de y.
func (a *App) drawPaper(cv *canvas.Canvas, y, rows, plate int, now time.Time) {
	W := a.W
	cv.Fill(0, y, W, rows, paper)
	titleY, lastY := y, y+rows-1
	title, short, tn := a.plateTitle(plate)
	titleFG := paperInk
	switch tn {
	case toneCall:
		titleFG = paperGold
	case toneBad:
		titleFG = paperRed
	}
	if a.confirm != nil {
		title, titleFG = strings.ToUpper(opFR(a.confirm.op)+" "+a.confirm.what+" sur "+a.confirm.machine)+" ?", paperRed
		short = title
	}

	// Rangée du titre : planche à gauche, signature à droite, titre au centre.
	left := "PLANCHE " + roman(plate) + "."
	if W < 100 {
		left = "PL. " + roman(plate) + "."
	}
	right := "connex."
	if canvas.StringWidth(title)+canvas.StringWidth(left)+4 > W {
		title = short
	}
	tw := canvas.StringWidth(title)
	lw, rw := canvas.StringWidth(left), canvas.StringWidth(right)
	showRight := tw+lw+rw+6 <= W
	showLeft := tw+lw+4 <= W
	if showLeft {
		cv.Text(1, titleY, W, left, paperFaint, 0)
	}
	if showRight {
		cv.Text(W-rw-1, titleY, W, right, paperFaint, canvas.Italic)
	}
	tx := (W - tw) / 2
	lo, hi := 1, W-1
	if showLeft {
		lo = lw + 3
	}
	if showRight {
		hi = W - rw - 3
	}
	if tx < lo {
		tx = lo
	}
	cv.Text(tx, titleY, hi, canvas.Truncate(title, hi-tx), titleFG, canvas.Bold)

	if a.confirm != nil {
		a.drawConfirm(cv, y+1, lastY, now)
		return
	}
	if rows >= 3 {
		a.drawNote(cv, y+1, plate, now)
	}

	// Dernière rangée : recherche, message passager (la planche I l'écrit avec
	// les nouvelles), ou touches.
	switch {
	case a.typing || (a.query != "" && plate == 1):
		a.drawPrompt(cv, lastY)
	case plate != 1 && a.noticeLive(now):
		// Le message se lit en entier (une raison de refus, une adresse) :
		// paperRows a gardé une rangée par ligne, au-dessus de la dernière.
		fg := paperInk
		if a.notice.tone == toneBad {
			fg = paperRed
		}
		// S'il ne tient pas, c'est sa fin qui se perd, pas son début.
		lines := wrap(a.notice.text, W-2)
		if room := lastY - titleY; len(lines) > room {
			lines = lines[:room]
			lines[room-1] = canvas.Truncate(lines[room-1]+" …", W-2)
		}
		for i, line := range lines {
			ly := lastY - len(lines) + 1 + i
			cv.Fill(0, ly, W, 1, paper) // la note d'observation cède sa rangée
			msg := canvas.Truncate(line, W-2)
			cv.Text(max(1, (W-canvas.StringWidth(msg))/2), ly, W-1, msg, fg, canvas.Italic)
		}
	default:
		drawHints(cv, 1, lastY, W-2, a.hints(plate, now))
	}
}

// paperRows : la marge prend 2 rangées, 3 sur un grand écran ; une question y
// ajoute ce qu'il faut pour que son détail se lise en entier, un message
// passager des planches II à IV aussi.
func (a *App) paperRows() int {
	rows := 2
	if a.H >= 32 {
		rows = 3
	}
	if c := a.confirm; c != nil && c.detail != "" && !a.confirmFits(c) {
		rows = max(rows, 2+len(wrap(c.detail, a.W-2)))
	}
	if a.confirm == nil && a.notice != nil && a.view != viewSky {
		rows += len(wrap(a.notice.text, a.W-2)) - 1
	}
	return min(rows, max(5, a.H/2))
}

// Les deux réponses possibles à une question.
const (
	confirmYes = "Entrée : oui"
	confirmNo  = "autre touche : non"
)

// confirmFits dit si le détail de la question tient sur la rangée de la réponse.
func (a *App) confirmFits(c *confirmation) bool {
	return canvas.StringWidth(c.detail)+3+canvas.StringWidth(confirmYes)+3+canvas.StringWidth(confirmNo) <= a.W-2
}

// plateTitle renvoie le titre d'une planche, et sa forme courte.
func (a *App) plateTitle(plate int) (string, string, tone) {
	one := func(s string, t tone) (string, string, tone) { return s, s, t }
	switch plate {
	case 2:
		if it := a.w.byID[a.plate]; it != nil {
			return one(strings.ToUpper(objectTitle(it))+".", toneNeutral)
		}
		return one("OBJET DISPARU.", toneBad)
	case 3:
		return one("JOURNAL D’OBSERVATION.", toneNeutral)
	case 4:
		return one("LÉGENDE DES PLANCHES.", toneNeutral)
	}
	return a.title()
}

// objectTitle nomme un objet : « site, appli », « port 5173 ».
func objectTitle(it *item) string {
	switch it.kind {
	case kindAgent:
		return it.agent.Tool + " dans " + baseName(it.agent.CWD)
	case kindApp:
		return "appli " + it.app.Name
	case kindPort, kindForeign:
		return "port " + strconv.Itoa(it.port.Port)
	case kindRepo:
		return "dépôt " + baseName(it.repo.Path)
	case kindError:
		return "erreur de relevé"
	}
	return ""
}

// drawNote écrit la note d'observation, en italique, sous le titre.
func (a *App) drawNote(cv *canvas.Canvas, y, plate int, now time.Time) {
	var note string
	switch {
	case a.lastTime.IsZero():
		note = "En attente des premières observations."
	default:
		t := a.lastTime.Local()
		note = "Observé le " + dateFR(t) + " à " + clockSecFR(t) + "."
	}
	if n := len(a.w.machines); n > 0 {
		ok := 0
		for _, mv := range a.w.machines {
			if mv.m.Reachable {
				ok++
			}
		}
		note += "   " + plural(n, "machine", "machines")
		if ok < n {
			note += ", " + plural(ok, "joignable", "joignables")
		}
		note += "."
	}
	if a.closed {
		note += "   La source s’est arrêtée."
	}
	w := canvas.StringWidth(note)
	cv.Text(max(1, (a.W-w)/2), y, a.W-1, note, paperFaint, canvas.Italic)
}

// drawHints écrit les touches qui tiennent, dans leur ordre, en gardant les
// plus importantes quand la place manque.
func drawHints(cv *canvas.Canvas, x, y, w int, hs []hint) {
	idx := make([]int, len(hs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return hs[idx[i]].prio < hs[idx[j]].prio })
	keep := make([]bool, len(hs))
	used := 0
	for _, i := range idx {
		n := canvas.StringWidth(hs[i].key) + 1 + canvas.StringWidth(hs[i].label)
		if used > 0 {
			n += 3
		}
		if used+n > w {
			continue
		}
		keep[i] = true
		used += n
	}
	cx := x + max(0, (w-used)/2)
	first := true
	for i, h := range hs {
		if !keep[i] {
			continue
		}
		if !first {
			cx += 3
		}
		first = false
		cx = cv.Text(cx, y, x+w, h.key, paperInk, canvas.Bold)
		cx = cv.Text(cx+1, y, x+w, h.label, paperFaint, 0)
	}
}

// hints renvoie les touches utiles, d'après la planche et l'objet choisi. Sur
// la planche I, taper cherche ; les actions sont sur la planche d'un objet.
func (a *App) hints(plate int, now time.Time) []hint {
	var hs []hint
	switch plate {
	case 1:
		hs = append(hs, hint{"↑↓", "choisir", 7})
		if len(a.w.machines) > 1 && a.query == "" {
			hs = append(hs, hint{"←→", "machine", 4})
		}
		if a.selected() != nil {
			hs = append(hs, hint{"Entrée", "détails", 3})
		}
		hs = append(hs, hint{"/", "chercher", 2}, hint{"Tab", "journal", 5})
		if len(a.w.machines) > 0 {
			hs = append(hs, hint{"F5", "actualiser", 6})
		}
		hs = append(hs, hint{"?", "légende", 1}, hint{"q", "quitter", 1})
	case 2:
		it := a.w.byID[a.plate]
		hs = append(hs, hint{"Échap", "retour", 1})
		if it != nil && it.kind == kindApp {
			// Ouvrir est la plus fréquente et la seule sans risque : d'abord.
			hs = append(hs, hint{"o", "ouvrir", 1})
		}
		hs = append(hs, actionHints(it)...)
		hs = append(hs, hint{"↑↓", "objet voisin", 5}, hint{"F5", "actualiser", 7}, hint{"Tab", "journal", 6},
			hint{"?", "légende", 4}, hint{"q", "quitter", 2})
	case 3:
		hs = append(hs, hint{"↑↓", "parcourir", 3}, hint{"Entrée", "voir l’objet", 2}, hint{"Échap", "retour", 1},
			hint{"?", "légende", 4}, hint{"q", "quitter", 1})
	case 4:
		hs = append(hs, hint{"Échap", "retour", 1}, hint{"Tab", "journal", 3}, hint{"q", "quitter", 1})
	}
	return hs
}

// actionHints : les touches des actions possibles sur un objet, les plus
// probables d'abord (objectActions : les touches d, a, r, l n'agissent que là).
func actionHints(it *item) []hint {
	var hs []hint
	for _, x := range objectActions(it) {
		hs = append(hs, x.hint)
	}
	return hs
}

// drawConfirm pose la question d'une action, à l'encre rouge : son détail sur
// les rangées libres de la marge, ou devant la réponse sur une marge de deux
// rangées, puis la réponse attendue. « Entrée : oui » ne paraît qu'une fois la
// question armée ; sa place est gardée, pour que rien ne bouge.
func (a *App) drawConfirm(cv *canvas.Canvas, y0, last int, now time.Time) {
	c := a.confirm
	W := a.W
	sw := canvas.StringWidth
	tail := sw(confirmYes) + 3 + sw(confirmNo)
	x := max(1, (W-tail)/2)
	switch {
	case c.detail == "":
	case last > y0:
		lines := wrap(c.detail, W-2)
		for i := 0; i < last-y0 && i < len(lines); i++ {
			ln := lines[i]
			if i == last-y0-1 && i < len(lines)-1 {
				ln = canvas.Truncate(ln+" "+strings.Join(lines[i+1:], " "), W-2)
			}
			cv.Text(max(1, (W-sw(ln))/2), y0+i, W-1, ln, paperRed, canvas.Italic)
		}
	default:
		d := c.detail
		if !a.confirmFits(c) {
			d = canvas.Truncate(d, max(0, W-2-tail-3))
		}
		if d != "" {
			x = max(1, (W-sw(d)-3-tail)/2)
			x = cv.Text(x, last, W-1, d, paperRed, canvas.Italic) + 3
		}
	}
	if c.armed(now) {
		cv.Text(x, last, W-1, confirmYes, paperRed, canvas.Bold)
	}
	cv.Text(x+sw(confirmYes)+3, last, W-1, confirmNo, paperFaint, 0)
}

// qHint rappelle, pendant une recherche, que q quitte et que Q écrit un q.
var qHint = hint{"q", "quitte · Q écrit un q", 1}

// hintWidth est la largeur d'une touche et de son libellé.
func hintWidth(h hint) int { return canvas.StringWidth(h.key) + 1 + canvas.StringWidth(h.label) }

// drawPrompt écrit la recherche en cours.
func (a *App) drawPrompt(cv *canvas.Canvas, y int) {
	x := cv.Text(1, y, a.W-1, "chercher ", paperFaint, canvas.Italic)
	q := a.query
	room := a.W/2 - x
	// Pendant la frappe, q quitte : la ligne le rappelle, avec Q, avant tout
	// le reste. La requête et le compte lui laissent sa place.
	end := a.W - 1
	if a.typing {
		end = a.W - 3 - hintWidth(qHint)
		room = min(room, end-x-3) // le curseur et deux blancs
	}
	if canvas.StringWidth(q) > room {
		q = canvas.TruncateLeft(q, max(1, room))
	}
	x = cv.Text(x, y, a.W-1, q, paperInk, canvas.Bold)
	if a.typing {
		cv.TextBG(x, y, a.W-1, " ", paper, paperInk, 0)
		x++
	}
	x += 2
	var counts []string
	switch n := len(a.results); {
	case a.query == "":
		counts = []string{"un port, un projet, un programme…", ""}
	case n == 0:
		counts = []string{"rien", ""}
	default:
		c := plural(n, "objet", "objets")
		if ms := a.resultMachines(); ms > 1 {
			counts = append(counts, c+" sur "+strconv.Itoa(ms)+" machines")
		}
		counts = append(counts, c, strconv.Itoa(n), "")
	}
	count := counts[len(counts)-1]
	for _, c := range counts {
		if x+canvas.StringWidth(c) <= end {
			count = c
			break
		}
	}
	x = cv.Text(x, y, a.W-1, count, paperFaint, canvas.Italic)
	var hs []hint
	if a.typing {
		hs = []hint{{"↑↓", "choisir", 3}, {"Entrée", "détails", 2}, {"Échap", "effacer", 2}, qHint}
	} else {
		hs = []hint{{"Entrée", "détails", 1}, {"/", "modifier", 2}, {"Échap", "effacer", 1}}
	}
	drawHints(cv, x+2, y, a.W-x-3, hs)
}

func (a *App) resultMachines() int {
	seen := map[string]bool{}
	for _, it := range a.results {
		seen[it.m.Name] = true
	}
	return len(seen)
}

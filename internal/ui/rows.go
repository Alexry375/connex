package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// Une ligne de l'échelle se lit comme une phrase courte : le nom, l'état, ce
// qui cloche, puis où. À droite, la machine, et ce qui vient d'arriver à
// l'élément (une action en cours, sa réponse, un changement récent).

// seg est un morceau de phrase.
type seg struct {
	s    string
	col  canvas.Color
	bold bool
	path bool // coupé en gardant la fin
}

// agentGlyph dit l'état d'un agent en un signe.
func agentGlyph(state string) (rune, canvas.Color) {
	switch state {
	case model.StateWaiting:
		return '◉', cGold
	case model.StateWorking:
		return '◐', cSky
	case model.StateIdle:
		return '○', cInkDim
	}
	return '◌', cInkFaint
}

// appGlyph dit l'état d'une appli en un signe.
func appGlyph(state string) (rune, canvas.Color) {
	switch state {
	case model.AppRunning:
		return '●', cSage
	case model.AppStarting:
		return '◔', cGold
	case model.AppStopped:
		return '○', cInkDim
	case model.AppFailed:
		return '✕', cRed
	}
	return '◌', cInkFaint
}

func agentColor(state string) canvas.Color {
	_, c := agentGlyph(state)
	if state == model.StateIdle {
		return cInkSoft
	}
	return c
}

func appColor(state string) canvas.Color {
	_, c := appGlyph(state)
	if state == model.AppStopped {
		return cInkSoft
	}
	return c
}

func exposureColor(e string) canvas.Color {
	switch e {
	case "all":
		return cVermilion
	case "tailnet":
		return cSky
	}
	return cInkSoft
}

// drawRow peint une entrée de l'échelle.
func (a *app) drawRow(s *canvas.Canvas, y int, e *entry, sel bool, nameW int, sameMachine bool) {
	if sel {
		bg := cGround3
		for x := 0; x < s.W; x++ {
			s.Set(x, y, ' ', canvas.Style{Fg: cInk, Bg: bg})
		}
		s.Set(0, y, '▌', fg(cGold))
	}
	wide := s.W >= 100
	// à droite : la machine, puis devant elle ce qui vient d'arriver
	right := s.W - 1
	if nameW > 0 {
		// la machine n'est écrite qu'au changement : les lignes d'une même
		// machine se suivent sous son nom
		if !sameMachine || sel {
			name := e.machine.Name
			col := canvas.Mix(machineTint(e.order), cInkDim, 0.3)
			if !e.machine.Reachable {
				col = cRed
			}
			s.Text(right-nameW, y, canvas.Truncate(name, nameW), fg(col))
		}
		right -= nameW + 2
	}
	status, scol := a.rowStatus(e)
	maxStatus := max(s.W/3, 14)
	if status != "" {
		status = canvas.Truncate(status, maxStatus)
		w := canvas.Width(status)
		s.Text(right-w, y, status, fg(scol))
		right -= w + 2
	}
	// à gauche : le signe et la phrase
	x := 2
	var segs []seg
	switch {
	case e.agent != nil:
		g, col := agentGlyph(e.agent.State)
		s.Set(x, y, g, fg(col))
		x = 4
		segs = agentSegs(e, wide)
	case e.app != nil:
		g, col := appGlyph(e.app.State)
		if e.app.State == model.AppStarting {
			g = spinner(a.now.Sub(a.born), []rune("◔◑◕●"))
		}
		s.Set(x, y, g, fg(col))
		x = 4
		segs = a.appSegs(e, wide)
	case e.port != nil:
		num := strconv.Itoa(e.port.Port)
		col := cInk
		if e.port.Exposure == "all" {
			col = cVermilion
		}
		if e.ghost {
			col = cInkGhost
		}
		s.Text(2+max(5-len(num), 0), y, num, bold(col))
		x = 8
		segs = a.portSegs(e, wide)
	case e.repo != nil:
		g, col := '◇', cInkDim
		switch {
		case e.repo.Dirty == nil:
			g, col = '◌', cInkFaint
		case *e.repo.Dirty:
			g, col = '◆', cOrange
		}
		s.Set(x, y, g, fg(col))
		x = 4
		segs = repoSegs(e, wide)
	}
	if e.ghost {
		s.Set(2, y, ' ', fg(cInkGhost))
		if e.port == nil {
			s.Set(2, y, '·', fg(cInkGhost))
		}
		for i := range segs {
			segs[i].col = cInkGhost
			segs[i].bold = false
		}
	}
	writeSegs(s, x, y, right-1, segs)
}

// writeSegs écrit les morceaux tant qu'il y a la place ; le dernier qui ne
// tient pas est coupé proprement, et un séparateur n'est écrit que si quelque
// chose le suit.
func writeSegs(s *canvas.Canvas, x, y, end int, segs []seg) {
	for i, sg := range segs {
		txt, ok := sg.cut(end - x)
		if ok && txt == " · " && i+1 < len(segs) {
			_, ok = segs[i+1].cut(end - x - canvas.Width(txt))
		}
		if !ok {
			return
		}
		st := fg(sg.col)
		if sg.bold {
			st = bold(sg.col)
		}
		x += s.Text(x, y, txt, st)
	}
}

// cut renvoie ce qu'on écrit d'un morceau en room colonnes : lui entier, ou
// coupé proprement ; rien (false) s'il n'en resterait qu'un séparateur et
// quelques lettres.
func (sg seg) cut(room int) (string, bool) {
	if canvas.Width(sg.s) <= room {
		return sg.s, true
	}
	lead, txt := "", sg.s
	if rest, ok := strings.CutPrefix(txt, " · "); ok {
		lead, txt = " · ", rest
	}
	left := room - canvas.Width(lead)
	switch {
	case sg.path && room >= 14:
		// un chemin plus coupé que ça ne dit plus rien : on le laisse au détail
		return lead + fitPath(txt, left), true
	case !sg.path && left >= 4:
		return lead + canvas.Truncate(txt, left), true
	}
	return "", false
}

func agentSegs(e *entry, wide bool) []seg {
	ag := e.agent
	segs := []seg{
		{s: ag.Tool, col: cInk, bold: true},
		{s: " " + agentWord(ag.State), col: agentColor(ag.State)},
	}
	if p := placeName(ag.CWD, e.home()); p != "" {
		segs = append(segs, seg{s: " · ", col: cInkFaint}, seg{s: p, col: cInkSoft})
	}
	if wide && ag.CWD != "" {
		segs = append(segs, seg{s: " · " + shortPath(ag.CWD, e.home()), col: cInkDim, path: true})
	}
	return segs
}

func (a *app) appSegs(e *entry, wide bool) []seg {
	ap := e.app
	segs := []seg{
		{s: ap.Name, col: cInk, bold: true},
		{s: " " + appWord(ap.State), col: appColor(ap.State)},
	}
	if ap.Port != 0 {
		segs = append(segs, seg{s: " · port " + strconv.Itoa(ap.Port), col: cInkSoft})
	}
	for _, p := range appProblems(e) {
		segs = append(segs, seg{s: " · " + p, col: cOrange})
	}
	if ap.CWD != "" {
		segs = append(segs, seg{s: " · " + shortPath(ap.CWD, e.home()), col: cInkDim, path: true})
	}
	return segs
}

// appProblems dit ce qui cloche autour d'une appli, d'après le relevé : réponse
// HTTP en erreur, port partagé avec une autre appli, port déjà tenu.
func appProblems(e *entry) []string {
	ap := e.app
	var out []string
	if ap.Health.HTTP >= 400 {
		out = append(out, "répond "+strconv.Itoa(ap.Health.HTTP))
	}
	if ap.Port == 0 || e.ghost {
		return out
	}
	snap := &e.machine.Snapshot
	for _, o := range snap.Apps {
		if o.Name != ap.Name && o.Port == ap.Port {
			out = append(out, "même port que "+o.Name)
		}
	}
	if ap.State != model.AppRunning && ap.State != model.AppStarting {
		for _, l := range snap.Listeners {
			if l.Port == ap.Port {
				out = append(out, "port pris par "+holderShort(l))
				break
			}
		}
	}
	return out
}

// holderShort nomme ce qui tient un port, en court.
func holderShort(l model.Listener) string {
	if !l.Mine {
		return "un autre compte"
	}
	if l.Process != "" {
		return l.Process
	}
	return "un programme inconnu"
}

func (a *app) portSegs(e *entry, wide bool) []seg {
	l := e.port
	var segs []seg
	if l.Mine {
		segs = append(segs, seg{s: orWord(l.Process, "programme inconnu"), col: cInk, bold: true})
		if p := placeName(listenerDir(*l), e.home()); p != "" {
			segs = append(segs, seg{s: " · ", col: cInkFaint}, seg{s: p, col: cInkSoft})
		}
	} else {
		segs = append(segs, seg{s: "autre compte", col: cInkSoft}, seg{s: " · uid " + strconv.Itoa(l.UID), col: cInkDim})
	}
	segs = append(segs, seg{s: " · " + exposureWord(l.Exposure), col: exposureColor(l.Exposure)})
	for _, p := range portNeighbours(e) {
		segs = append(segs, seg{s: " · " + p, col: cOrange})
	}
	if wide {
		segs = append(segs, seg{s: " · " + l.Addr, col: cInkDim})
		if l.PID != 0 {
			segs = append(segs, seg{s: " · pid " + strconv.Itoa(l.PID), col: cInkDim})
		}
		if !l.Mine {
			segs = append(segs, seg{s: " · programme et dossier inconnus", col: cInkFaint})
		}
	}
	return segs
}

// portNeighbours signale un autre programme qui écoute sur le même port de la
// même machine (une autre adresse) : deux applis qui veulent le même port.
func portNeighbours(e *entry) []string {
	if e.ghost {
		return nil
	}
	l := e.port
	var out []string
	seen := map[string]bool{}
	for _, o := range e.machine.Snapshot.Listeners {
		if o.Key == l.Key || o.Port != l.Port {
			continue
		}
		same := o.Mine == l.Mine && o.UID == l.UID && o.PID == l.PID
		if same {
			continue
		}
		h := holderShort(o)
		if p := placeName(listenerDir(o), e.home()); o.Mine && p != "" {
			h += " · " + p
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, "aussi "+h)
		}
	}
	return out
}

func repoSegs(e *entry, wide bool) []seg {
	r := e.repo
	segs := []seg{
		{s: projectName(r.Path), col: cInk, bold: true},
		{s: " · " + branchWord(r.Branch, r.Detached), col: cInkSoft},
	}
	switch {
	case r.Dirty == nil:
		segs = append(segs, seg{s: " · état inconnu", col: cInkFaint})
	case *r.Dirty:
		segs = append(segs, seg{s: " · modifié", col: cOrange})
	default:
		segs = append(segs, seg{s: " · propre", col: cInkDim})
	}
	if n := len(r.Worktrees); n > 0 {
		segs = append(segs, seg{s: " · " + plural(n, "worktree", "worktrees"), col: cInkDim})
	}
	segs = append(segs, seg{s: " · " + shortPath(r.Path, e.home()), col: cInkDim, path: true})
	return segs
}

// rowStatus dit ce qui vient d'arriver à l'élément, pour le bout de sa ligne.
func (a *app) rowStatus(e *entry) (string, canvas.Color) {
	now := a.now
	if p := a.w.pendingFor(e.id); p != nil {
		el := now.Sub(p.at)
		if el >= silentAfter {
			return "◌ sans réponse depuis " + span(el), cGold
		}
		g := spinner(el, []rune("◐◓◑◒"))
		return string(g) + " " + opProgress(p.op) + " · " + seconds(el), cGold
	}
	if o, ok := a.w.results[e.id]; ok {
		if o.ok {
			return "✓ " + opWord(o.op) + " : fait en " + seconds(o.done.Sub(o.at)), cSage
		}
		why := o.err
		if why == "" {
			why = "sans raison donnée"
		}
		return "✕ refusé : " + why, cRed
	}
	if e.ghost {
		verb := "disparu"
		switch {
		case e.port != nil:
			verb = "fermé"
		case e.app != nil:
			verb = "retirée"
		case e.agent != nil:
			verb = "terminé"
		}
		return verb + " " + when(e.goneAt, now, e.goneBack), cInkFaint
	}
	if m, ok := a.w.recent[e.id]; ok {
		age := now.Sub(m.at)
		col := toneColor(m.tone)
		if m.tone == toneNeutral {
			col = cGoldDeep
		}
		// la marque pâlit en deux minutes
		col = canvas.Mix(col, cInkDim, min(age.Seconds()/recentTTL.Seconds(), 1)*0.7)
		verb := "changé"
		if m.op == model.OpAdd {
			verb = "apparu"
			switch {
			case e.port != nil:
				verb = "ouvert"
			case e.agent != nil:
				verb = "arrivé"
			case e.app != nil:
				verb = "inscrite"
			}
		}
		return verb + " " + when(m.at, now, m.back), col
	}
	if e.agent != nil && e.agent.State == model.StateWaiting {
		if t, ok := a.w.since[e.id]; ok {
			return since(now.Sub(t)), cGoldDeep
		}
	}
	return "", cInkDim
}

// spinner choisit l'image d'un signe tournant (huit images par seconde).
func spinner(el time.Duration, frames []rune) rune {
	i := int(el.Milliseconds()/125) % len(frames)
	return frames[i]
}

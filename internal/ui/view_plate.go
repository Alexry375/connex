package ui

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/art"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// La planche II : un objet, en détail, comme vu à la lunette. Le champ rond
// montre la nébuleuse (un port, une appli, un dépôt), les météores (un agent)
// ou les protubérances (un échec, un refus) ; l'objet est marqué au centre et
// un trait de rappel mène à son nom. La légende dit tout ce que le relevé sait
// de l'objet, et rien de plus.

// fact est une ligne de la légende d'un objet.
type fact struct {
	label string
	value []seg
	path  bool // la valeur est un chemin : on la coupe après les « / »
}

func val(s string, fg canvas.Color) []seg { return []seg{{s, fg, 0}} }

// plateArt choisit la planche qui illustre l'objet.
func plateArt(it *item, refused bool) (art.Plate, art.Rect) {
	switch {
	case refused, it.kind == kindError, it.kind == kindApp && it.app.State == model.AppFailed:
		return art.Protuberances, art.Rect{X0: 0.05, Y0: 0.05, X1: 0.95, Y1: 0.6}
	case it.kind == kindAgent:
		return art.Meteors, art.Rect{X0: 0.05, Y0: 0.04, X1: 0.95, Y1: 0.62}
	}
	return art.Nebula, art.Rect{X0: 0.12, Y0: 0.08, X1: 0.96, Y1: 0.86}
}

// facts rassemble ce que le relevé dit d'un objet.
func (a *App) facts(it *item, now time.Time) []fact {
	var fs []fact
	home := it.home
	full := func(p string) string { return p }
	add := func(label string, v []seg) { fs = append(fs, fact{label: label, value: v}) }
	addPath := func(label, p string) {
		if p != "" {
			fs = append(fs, fact{label: label, value: val(full(p), ink), path: true})
		}
	}
	s := &it.m.Snapshot
	switch it.kind {
	case kindAgent:
		ag := it.agent
		st := val(agentStateFR(ag.State), ink)
		if ag.State == model.StateWaiting {
			st = []seg{{"t’attend", gold, canvas.Bold}}
		}
		add("état", st)
		add("outil", val(ag.Tool, ink))
		addPath("dossier", ag.CWD)
		if r := repoOf(s, ag.CWD); r != nil {
			add("dépôt", repoSegs(r, home))
		}
		add("processus", val("pid "+strconv.Itoa(ag.PID), inkDim))

	case kindApp:
		ap := it.app
		st := val(appStateFR(ap.State), ink)
		switch ap.State {
		case model.AppRunning:
			st = val("en marche", mint)
		case model.AppFailed:
			st = []seg{{"en échec", red, canvas.Bold}}
		case model.AppStarting:
			st = val("démarre", gold)
		}
		add("état", st)
		add("santé", healthSegs(ap))
		if ap.Port > 0 {
			switch {
			case it.appPort != nil:
				if it.appPort.Program() != "" {
					add("programme", val(it.appPort.Program()+" (pid "+strconv.Itoa(it.appPort.PID)+")", ink))
				}
				add("port", listenSegs(it.appPort, it.twins))
			case it.holder != nil && it.holder.Mine:
				add("port", []seg{{strconv.Itoa(ap.Port) + " : tenu par " + it.holder.Program() + " (pid " + strconv.Itoa(it.holder.PID) + ")", gold, 0}})
				if d := dirOf(it.holder); d != "" {
					addPath("   depuis", d)
				}
			case it.holder != nil:
				add("port", []seg{{strconv.Itoa(ap.Port) + " : tenu par un autre compte (uid " + strconv.Itoa(it.holder.UID) + ")", gold, 0}})
			default:
				add("port", val(strconv.Itoa(ap.Port)+" : réservé, personne n’y écoute", inkDim))
			}
		}
		for _, l := range it.sharers {
			add("aussi sur "+strconv.Itoa(l.Port), val(l.Program()+" (pid "+strconv.Itoa(l.PID)+") · "+l.Addr+" · "+shortPath(dirOf(l), home), gold))
		}
		add("unité", val(ap.Unit, inkDim))
		addPath("projet", ap.Project)
		if ap.CWD != ap.Project {
			addPath("lancée depuis", ap.CWD)
		}
		add("aperçu", val(viewFR(ap.View), inkDim))
		if r := repoOf(s, ap.Project); r != nil && ap.Project != "" {
			add("dépôt", repoSegs(r, home))
		}

	case kindPort:
		l := it.port
		add("programme", val(l.Program()+" (pid "+strconv.Itoa(l.PID)+")", ink))
		if l.Unit != "" {
			add("unité", val(l.Unit, inkDim))
		}
		if l.Exe != "" && l.Exe != l.Program() {
			add("exécutable", val(l.Exe, inkDim))
		}
		add("écoute", listenSegs(l, it.twins))
		expo := val(exposureLongFR(l), inkDim)
		if l.Funnel {
			expo = val(exposureLongFR(l), salmon)
		}
		add("exposition", expo)
		addPath("dossier", l.CWD)
		if l.ProjectRoot != "" && l.ProjectRoot != l.CWD {
			addPath("projet", l.ProjectRoot)
		}
		for _, o := range it.sharers {
			add("aussi sur "+strconv.Itoa(o.Port), val(o.Program()+" (pid "+strconv.Itoa(o.PID)+") · "+o.Addr+" · "+shortPath(dirOf(o), home), gold))
		}
		if r := repoOf(s, dirOf(l)); r != nil {
			add("dépôt", repoSegs(r, home))
		}

	case kindForeign:
		l := it.port
		add("compte", val("un autre compte (uid "+strconv.Itoa(l.UID)+")", ink))
		add("écoute", listenSegs(l, it.twins))
		add("exposition", val(exposureLongFR(l), inkDim))
		add("programme", []seg{{"inconnu : connex ne voit ni le programme ni le dossier d’un autre compte", inkFaint, canvas.Italic}})

	case kindRepo:
		r := it.repo
		addPath("chemin", r.Path)
		add("branche", val(branchFR(r.Branch, r.Detached), ink))
		d := val(dirtyFR(r.Dirty), inkDim)
		if r.Dirty != nil && *r.Dirty {
			d = val("modifié : des changements ne sont pas enregistrés", gold)
		}
		add("état", d)
		if r.Upstream != "" {
			add("suit", val(r.Upstream, inkDim))
		}
		if r.Ahead != nil && *r.Ahead > 0 {
			add("à pousser", val(plural(*r.Ahead, "commit pas encore poussé", "commits pas encore poussés"), goldDim))
		}
		if r.Behind != nil && *r.Behind > 0 {
			add("en retard", val(plural(*r.Behind, "commit", "commits")+" de "+r.Upstream+" pas encore pris ici, d’après le dernier fetch", inkDim))
		}
		for _, wt := range r.Worktrees {
			fs = append(fs, fact{label: "worktree", value: val(shortPath(wt.Path, home)+" · "+branchFR(wt.Branch, wt.Detached), inkDim), path: true})
		}
		for i := range s.Agents {
			ag := &s.Agents[i]
			if repoHolds(r, ag.CWD) {
				add("agent", val(ag.Tool+" · "+agentStateFR(ag.State)+" · "+shortPath(ag.CWD, home), inkDim))
			}
		}

	case kindError:
		path, reason := errorParts(it.err.Message)
		add("échec", val(errorSourceFR(it.err.Source), ink))
		addPath("dossier", path)
		fs = append(fs, fact{label: "raison", value: val(reason, ink), path: true})
	}
	return fs
}

func dirOf(l *model.Listener) string {
	if l.ProjectRoot != "" {
		return l.ProjectRoot
	}
	return l.CWD
}

// listenSegs dit où écoute un port : « 53 sur 127.0.0.53 et 127.0.0.54 · local ».
func listenSegs(l *model.Listener, twins []*model.Listener) []seg {
	addrs := []string{l.Addr}
	for _, t := range twins {
		addrs = append(addrs, t.Addr)
	}
	on := strings.Join(addrs[:len(addrs)-1], ", ")
	if len(addrs) > 1 {
		on += " et "
	}
	on += addrs[len(addrs)-1]
	fg := inkDim
	switch l.Exposure {
	case model.ExposureAll:
		fg = salmon
	case model.ExposureTailnet:
		fg = blue
	}
	return []seg{{strconv.Itoa(l.Port) + " sur " + on + " · ", ink, 0}, {listenerExposureFR(l), fg, 0}}
}

func healthSegs(ap *model.App) []seg {
	h := ap.Health
	var out []seg
	sep := func() {
		if len(out) > 0 {
			out = append(out, seg{" · ", inkGhost, 0})
		}
	}
	if h.PIDAlive {
		out = append(out, seg{"processus vivant", mint, 0})
	} else {
		out = append(out, seg{"aucun processus", inkDim, 0})
	}
	sep()
	if h.PortListening {
		out = append(out, seg{"port en écoute", mint, 0})
	} else if ap.Port > 0 {
		out = append(out, seg{"port muet", inkDim, 0})
	}
	sep()
	switch {
	case h.HTTP == 0:
		out = append(out, seg{httpFR(0), inkDim, 0})
	case h.HTTP >= 400:
		out = append(out, seg{httpFR(h.HTTP), red, canvas.Bold})
	default:
		out = append(out, seg{httpFR(h.HTTP), mint, 0})
	}
	return out
}

func repoSegs(r *model.Repo, home string) []seg {
	d := seg{dirtyFR(r.Dirty), inkDim, 0}
	if r.Dirty != nil && *r.Dirty {
		d.fg = gold
	}
	out := []seg{{baseName(r.Path) + " · " + branchFR(r.Branch, r.Detached) + " · ", ink, 0}, d}
	ahead, behind := syncFR(r)
	if ahead != "" {
		out = append(out, seg{" · " + ahead, goldDim, 0})
	}
	if behind != "" {
		out = append(out, seg{" · " + behind, inkDim, 0})
	}
	return out
}

// drawPlate dessine la planche II.
func (a *App) drawPlate(cv *canvas.Canvas, now time.Time) {
	W, H := a.W, a.H
	cv.Clear(landBG)
	paperRows := a.paperRows()
	bottom := H - paperRows
	it := a.w.byID[a.plate]
	if it == nil {
		cv.Text(2, 1, W-1, "Cet objet a disparu du relevé.", inkDim, canvas.Italic)
		a.drawPaper(cv, bottom, paperRows, 2, now)
		return
	}
	req := a.plateRequest(it, now)
	refused := req != nil && req.done && !req.ok && !req.lost && now.Sub(req.at) < 5*time.Second
	pl, rect := plateArt(it, refused)

	name, sub := plateName(it)
	if W >= 110 {
		a.drawEyepiece(cv, it, req, pl, rect, name, sub, bottom, now)
		a.drawPaper(cv, bottom, paperRows, 2, now)
		return
	}

	// En bandeau : l'illustration en haut, le nom gravé dedans.
	iw, ih := W, min(8, max(3, H/4))
	key := "plate:" + strconv.Itoa(int(pl)) + ":" + strconv.Itoa(iw) + "x" + strconv.Itoa(ih) + ":" + strconv.FormatBool(it.m.Reachable)
	img := a.gfx.get(key, func() *canvas.Pixels {
		im := art.Get(pl)
		p := im.Resample(im.Fit(rect, float64(iw)/float64(2*ih), 0.5), iw, ih*2)
		// Assombrir le bas, où se grave le nom de l'objet.
		for y := 0; y < p.H; y++ {
			k := float32(1)
			if f := float32(y) / float32(p.H); f > 0.55 {
				k = 1 - (f-0.55)*1.4
			}
			for x := 0; x < p.W; x++ {
				p.Set(x, y, p.At(x, y).Scale(k*0.85))
			}
		}
		art.Posterize(p, plateInks)
		if !it.m.Reachable {
			veil(p, nil)
		}
		return p
	})
	cv.Blit(img, 0, 0)
	cv.Text(2, ih-2, iw-1, canvas.Truncate(name, iw-3), inkBright, canvas.Bold)
	cv.Text(2, ih-1, iw-1, canvas.Truncate(sub, iw-3), inkDim, canvas.Italic)
	a.drawPlateText(cv, it, req, 2, ih+1, W-4, bottom, now)
	a.drawPaper(cv, bottom, paperRows, 2, now)
}

// plateRequest renvoie la dernière requête qui concerne l'objet : la sienne,
// ou le relevé de sa machine s'il est plus récent.
func (a *App) plateRequest(it *item, now time.Time) *request {
	req := a.requestFor(it.id, now, time.Minute)
	if m := a.requestFor(machineKey(it.m.Name), now, resultShown); m != nil && (req == nil || m.sent.After(req.sent)) {
		return m
	}
	return req
}

// veil voile des pixels comme le ciel d'une machine injoignable ; mask, s'il
// est donné, dit lesquels.
func veil(p *canvas.Pixels, mask []bool) {
	for i, c := range p.P {
		if mask == nil || mask[i] {
			l := c.Luma()
			p.P[i] = canvas.Lerp(canvas.RGB{R: l, G: l, B: l}, eclipseShade, 0.5)
		}
	}
}

// stretch étale les lumières d'une vue réduite : la moyenne de zone éteint
// les traits fins (les météores, les étoiles), on leur rend leur éclat. Le
// fond garde un peu de sa couleur, pour que le champ se distingue de la page.
func stretch(p *canvas.Pixels) {
	lum := make([]float64, len(p.P))
	for i, c := range p.P {
		lum[i] = float64(c.Luma())
	}
	sort.Float64s(lum)
	lo := float32(lum[len(lum)*2/100]) * 0.6
	hi := float32(lum[len(lum)*995/1000])
	if hi-lo < 0.05 {
		return
	}
	// On change la lumière, pas la teinte : chaque pixel est multiplié, sans
	// qu'aucune de ses composantes ne sature.
	for i, c := range p.P {
		l := c.Luma()
		if l <= 0 {
			continue
		}
		f := max(0, (l-lo)/(hi-lo)) / l
		if m := max(c.R, c.G, c.B); m*f > 1 {
			f = 1 / m
		}
		p.P[i] = c.Scale(f)
	}
}

// gain multiplie la lumière de tous les pixels.
func gain(p *canvas.Pixels, k float32) {
	for i, c := range p.P {
		p.P[i] = c.Scale(k)
	}
}

// grain renvoie une valeur fixe de [0, 1) pour le pixel (x, y).
func grain(x, y int) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263
	h = (h ^ h>>13) * 1274126177
	return float64(h^h>>16) / (1 << 32)
}

// drawEyepiece dessine la planche II d'un écran large : à gauche, le champ
// rond d'une lunette, bord égrené, l'objet marqué au centre ; à droite, son
// nom, relié au centre par un trait de rappel, puis sa légende.
func (a *App) drawEyepiece(cv *canvas.Canvas, it *item, req *request, pl art.Plate, rect art.Rect, name, sub string, bottom int, now time.Time) {
	W := a.W
	d := min(bottom*2-6, W*2/5) // diamètre du champ, en pixels (une colonne chacun)
	iw := d + 4
	tx, tw := iw+4, W-iw-6
	cx, cy := iw/2, bottom/2 // centre du champ, en cases

	if d >= 10 {
		key := "eyepiece:" + strconv.Itoa(int(pl)) + ":" + strconv.Itoa(d) + "x" + strconv.Itoa(bottom) + ":" + strconv.FormatBool(it.m.Reachable)
		img := a.gfx.get(key, func() *canvas.Pixels {
			im := art.Get(pl)
			sq := im.Resample(im.Fit(rect, 1, 0.5), d, d)
			stretch(sq)
			if pl == art.Protuberances {
				gain(sq, 0.72) // le soleil rouge dit l'échec sans éblouir
			}
			p := canvas.NewPixels(iw, bottom*2)
			inside := make([]bool, len(p.P))
			bg := landBG.RGB()
			r := float64(d) / 2
			ox, oy := float64(cx), float64(cy*2+1) // centre, en pixels
			for y := 0; y < p.H; y++ {
				for x := 0; x < p.W; x++ {
					q := math.Hypot(float64(x)+0.5-ox, float64(y)+0.5-oy) / r
					// Le bord du champ s'égrène au lieu de finir net.
					if q >= 1 || (q > 0.86 && grain(x, y) > (1-q)/0.14) {
						p.Set(x, y, bg)
						continue
					}
					sx, sy := x-int(ox-r), y-int(oy-r)
					p.Set(x, y, sq.At(sx, sy).Scale(float32(0.92-0.3*q*q)))
					inside[y*p.W+x] = true
				}
			}
			art.Posterize(p, plateInks)
			for i := range p.P {
				if !inside[i] {
					p.P[i] = bg // le fond reste exactement celui de la page
				}
			}
			if !it.m.Reachable {
				veil(p, inside)
			}
			return p
		})
		cv.Blit(img, 0, 0)
	}

	// Le texte, un peu au-dessus du milieu : nom, ligne de légende, filet.
	head := func(c *canvas.Canvas, y int) int {
		c.Text(tx, y, tx+tw, canvas.Truncate(name, tw), inkBright, canvas.Bold)
		c.Text(tx, y+1, tx+tw, canvas.Truncate(sub, tw), inkDim, canvas.Italic)
		c.Text(tx, y+2, tx+min(tw, 24), strings.Repeat("─", min(tw, 24)), inkGhost, 0)
		return y + 4
	}
	scratch := canvas.New(W, a.H)
	h := a.drawPlateText(scratch, it, req, tx, head(scratch, 0), tw, a.H, now)
	y0 := max(1, (bottom-h)/3)
	a.drawPlateText(cv, it, req, tx, head(cv, y0), tw, bottom, now)

	if d < 10 {
		return
	}
	// L'objet au centre du champ, sur un fond creusé pour qu'il se détache.
	ln := a.lineFor(it, now)
	cv.TextBG(cx, cy, cx+1, string(ln.sign), ln.signFG, canvas.Mix(cv.Backdrop(cx, cy), landBG, 0.75), 0)
	// Le trait de rappel : vers la droite, puis à la hauteur du nom.
	ex := iw + 1
	r := float64(d) / 2
	for x := cx + 2; x < ex; x++ {
		// Il passe sous le bord égrené du champ au lieu de le hacher.
		if q := math.Abs(float64(x)+0.5-float64(cx)) / r; q > 0.84 && q < 1.02 {
			continue
		}
		cv.SetRune(x, cy, '┈', inkFaint, 0)
	}
	switch {
	case y0 == cy:
		cv.SetRune(ex, cy, '┈', inkFaint, 0)
	case y0 < cy:
		cv.SetRune(ex, cy, '┘', inkFaint, 0)
		cv.SetRune(ex, y0, '┌', inkFaint, 0)
	default:
		cv.SetRune(ex, cy, '┐', inkFaint, 0)
		cv.SetRune(ex, y0, '└', inkFaint, 0)
	}
	for y := min(y0, cy) + 1; y < max(y0, cy); y++ {
		cv.SetRune(ex, y, '┊', inkFaint, 0)
	}
	for x := ex + 1; x < tx-1; x++ {
		cv.SetRune(x, y0, '┈', inkFaint, 0)
	}
}

// drawPlateText écrit le texte de la planche II à partir de (tx, y) sur tw
// colonnes, sans dépasser la rangée bottom : éclipse, dernière action, faits,
// puis ce qui l'entoure dans son projet. Elle renvoie la rangée qui suit.
func (a *App) drawPlateText(cv *canvas.Canvas, it *item, req *request, tx, y, tw, bottom int, now time.Time) int {
	if !it.m.Reachable {
		for _, line := range wrap("Éclipse : "+it.m.Name+" ne répond plus depuis "+durFR(now.Sub(it.m.Since))+". Ce qui suit est son dernier état connu.", tw) {
			cv.Text(tx, y, tx+tw, line, redDim, canvas.Italic)
			y++
		}
	}

	// Où en est la dernière action sur cet objet.
	if req != nil {
		segs := requestSegs(req, now)
		if req.done {
			segs[0].s += " · " + agoFR(now.Sub(req.at))
		}
		txt := opFR(req.op) + " : "
		cx := cv.Text(tx, y, tx+tw, txt, inkDim, canvas.Italic)
		for _, line := range wrap(segsText(segs), tx+tw-cx) {
			cv.Text(cx, y, tx+tw, line, segs[0].fg, segs[0].a)
			y++
			if y >= bottom {
				break
			}
		}
		y++
	}

	// La légende.
	lw := 14
	for _, f := range a.facts(it, now) {
		if y >= bottom-1 {
			cv.Text(tx, bottom-1, tx+tw, "…", inkFaint, 0)
			break
		}
		cv.Text(tx, y, tx+lw, f.label, inkDim, canvas.Italic)
		for x := tx + canvas.StringWidth(f.label) + 1; x < tx+lw; x++ {
			cv.Text(x, y, x+1, "·", inkGhost, 0)
		}
		vx := tx + lw + 1
		vw := tx + tw - vx
		if f.path {
			for _, line := range wrap(segsText(f.value), vw) {
				if y >= bottom {
					break
				}
				cv.Text(vx, y, vx+vw, line, f.value[0].fg, f.value[0].a)
				y++
			}
			continue
		}
		y = drawWrapped(cv, vx, y, vw, f.value, bottom-1)
	}

	// Autour : ce qui tourne dans le même projet, sur la même machine.
	if rel := a.related(it); len(rel) > 0 && y+2 < bottom {
		y++
		cv.Text(tx, y, tx+tw, "autour de "+baseName(projectRoot(it)), inkDim, canvas.Italic)
		y++
		for _, o := range rel {
			if y >= bottom {
				break
			}
			drawLine(cv, tx, y, tw, a.lineFor(o, now), !it.m.Reachable, 0)
			y++
		}
	}
	return y
}

// plateName renvoie le nom gravé sur l'illustration et sa ligne de légende.
func plateName(it *item) (string, string) {
	on := " · " + it.m.Name
	switch it.kind {
	case kindAgent:
		return baseName(it.agent.CWD), "agent " + it.agent.Tool + on
	case kindApp:
		return it.app.Name, "appli" + on
	case kindPort:
		return strconv.Itoa(it.port.Port), "port de " + it.port.Program() + on
	case kindForeign:
		return strconv.Itoa(it.port.Port), "port d’un autre compte" + on
	case kindRepo:
		return baseName(it.repo.Path), "dépôt git" + on
	}
	return it.name, "erreur · " + errorSourceFR(it.err.Source) + on
}

func segsText(segs []seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.s)
	}
	return b.String()
}

// drawWrapped écrit des morceaux colorés sur w colonnes, en passant à la ligne
// entre les mots, sans dépasser la rangée maxY. Il renvoie la rangée suivante.
func drawWrapped(cv *canvas.Canvas, x, y, w int, segs []seg, maxY int) int {
	cx := x
	for _, s := range segs {
		for _, wd := range strings.SplitAfter(s.s, " ") {
			if wd == "" {
				continue
			}
			if cx > x && cx+canvas.StringWidth(strings.TrimRight(wd, " ")) > x+w {
				y, cx = y+1, x
				if y > maxY {
					return y
				}
			}
			for canvas.StringWidth(wd) > x+w-cx && cx == x {
				head := cutWidth(wd, w)
				if len(head) == len(wd) {
					break
				}
				cv.Text(cx, y, x+w, head, s.fg, s.a)
				wd = wd[len(head):]
				y++
				if y > maxY {
					return y
				}
			}
			cx = cv.Text(cx, y, x+w, wd, s.fg, s.a)
		}
	}
	return y + 1
}

// projectRoot renvoie le projet d'un objet : le dépôt qui contient son dossier,
// sinon son dossier.
func projectRoot(it *item) string {
	var dir string
	switch it.kind {
	case kindAgent:
		dir = it.agent.CWD
	case kindApp:
		dir = it.app.Project
		if dir == "" {
			dir = it.app.CWD
		}
	case kindPort:
		dir = dirOf(it.port)
	case kindRepo:
		return it.repo.Path
	}
	if dir == "" {
		return ""
	}
	if r := repoOf(&it.m.Snapshot, dir); r != nil {
		return r.Path
	}
	return dir
}

// related renvoie les autres objets du même projet, sur la même machine.
func (a *App) related(it *item) []*item {
	root := projectRoot(it)
	if root == "" {
		return nil
	}
	mv := a.w.machine(it.m.Name)
	if mv == nil {
		return nil
	}
	var out []*item
	for _, o := range mv.items {
		if o != it && projectRoot(o) == root {
			out = append(out, o)
		}
	}
	return out
}

package ui

import (
	"math"
	"time"

	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/art"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// Le ciel de la planche I. Tout ce qui y brille vient du relevé :
//   - un port ouvert à tous est un rayon d'aurore qui monte de l'horizon ;
//   - les autres ports sont des étoiles : la position dit le numéro de port
//     (échelle logarithmique), la hauteur dit l'exposition ;
//   - un agent est une comète ; un agent qui t'attend, une étoile d'or qui respire ;
//   - une appli en échec, une protubérance rouge au ras de l'horizon ;
//   - une machine injoignable, une éclipse : son ciel se voile ;
//   - ce qui vient de changer, une météore puis un halo qui s'éteint.

// Durées des animations.
const (
	stepDur      = 250 * time.Millisecond // pas du chatoiement au repos
	meteorFlight = 700 * time.Millisecond
	haloLife     = 90 * time.Second
	pulsePeriod  = 2800 * time.Millisecond
)

// span est un intervalle de colonnes [x0, x1).
type span struct{ x0, x1 int }

// uv est une position dans le ciel d'une machine : u de 0 à 1 sur la largeur,
// v de 0 (haut du ciel) à 1 (horizon).
type uv struct{ u, v float64 }

// hash32 est FNV-1a, mélangé : un hasard stable tiré d'une clé.
func hash32(s string, salt uint32) uint32 {
	h := uint32(2166136261) ^ salt*0x9e3779b9
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	h ^= h >> 16
	h *= 0x85ebca6b
	h ^= h >> 13
	h *= 0xc2b2ae35
	h ^= h >> 16
	return h
}

func hashF(s string, salt uint32) float64 { return float64(hash32(s, salt)) / (1 << 32) }

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }

// portU place un port sur la largeur du ciel, en échelle logarithmique : les
// ports système à gauche, les ports éphémères à droite.
func portU(port int) float64 {
	if port <= 0 {
		return 0.5
	}
	t := (math.Log(float64(port)) - math.Log(20)) / (math.Log(65536) - math.Log(20))
	return 0.05 + 0.9*clamp01(t)
}

// listenerUV : plus un port est exposé, plus il monte.
func listenerUV(l *model.Listener) uv {
	j := hashF(l.Key, 7)
	v := 0.64 + 0.2*j // boucle locale : bas sur l'horizon
	switch l.Exposure {
	case model.ExposureAll:
		v = 0.22 + 0.12*j
	case model.ExposureTailnet:
		v = 0.36 + 0.18*j
	case model.ExposureHost: // la machine et ses conteneurs : juste au-dessus de la boucle locale
		v = 0.56 + 0.06*j
	}
	return uv{portU(l.Port), v}
}

func agentUV(a *model.Agent) uv {
	return uv{0.06 + 0.88*hashF(a.Key, 5), 0.1 + 0.26*hashF(a.Key, 3)}
}

func appUV(a *model.App, l *model.Listener) uv {
	if l != nil {
		return listenerUV(l)
	}
	u := portU(a.Port)
	if a.Port == 0 {
		u = 0.1 + 0.8*hashF(a.Key, 11)
	}
	return uv{u, 0.86}
}

func repoUV(r *model.Repo) uv { return uv{0.08 + 0.84*hashF(r.Key, 13), 0.97} }

// itemUV renvoie la place d'un élément du catalogue dans son ciel.
func itemUV(it *item) uv {
	switch it.kind {
	case kindAgent:
		return agentUV(it.agent)
	case kindApp:
		return appUV(it.app, it.appPort)
	case kindPort, kindForeign:
		return listenerUV(it.port)
	case kindRepo:
		return repoUV(it.repo)
	}
	return uv{0.5, 0.5}
}

// changeUV renvoie la place de l'objet d'un changement, avant ou après.
func changeUV(c fleet.Change, prev, next *model.Snapshot) (uv, bool) {
	switch c.Kind {
	case model.KindListener:
		if l := findListener(next, c.Key); l != nil {
			return listenerUV(l), true
		}
		if l := findListener(prev, c.Key); l != nil {
			return listenerUV(l), true
		}
	case model.KindAgent:
		if a := findAgent(next, c.Key); a != nil {
			return agentUV(a), true
		}
		if a := findAgent(prev, c.Key); a != nil {
			return agentUV(a), true
		}
	case model.KindApp:
		s := next
		ap := findApp(s, c.Key)
		if ap == nil {
			s, ap = prev, findApp(prev, c.Key)
		}
		if ap != nil {
			var l *model.Listener
			if ap.State == model.AppRunning && ap.Port > 0 {
				for i := range s.Listeners {
					if s.Listeners[i].Port == ap.Port && s.Listeners[i].Mine {
						l = &s.Listeners[i]
						break
					}
				}
			}
			return appUV(ap, l), true
		}
	case model.KindRepo:
		if r := findRepo(next, c.Key); r != nil {
			return repoUV(r), true
		}
		if r := findRepo(prev, c.Key); r != nil {
			return repoUV(r), true
		}
	case fleet.KindMachine:
		return uv{0.5, 0.3}, true
	}
	return uv{}, false
}

// meteor est un changement qui file vers son objet, puis y laisse un halo.
type meteor struct {
	machine string
	at      uv
	born    time.Time
	col     canvas.RGB
	from    float64 // angle d'arrivée, tiré de la clé
}

func toneRGB(t tone) canvas.RGB {
	switch t {
	case toneGood:
		return meteorGreen
	case toneBad:
		return canvas.RGB{R: 1, G: 0.42, B: 0.36}
	case toneWarn, toneCall:
		return meteorGold
	}
	return meteorBlue
}

// spawnMeteor lance une météore pour un changement, si le ciel n'en est pas
// déjà plein. Le premier relevé d'une machine n'en lance pas.
func (a *App) spawnMeteor(c fleet.Change, e entry, at uv, now time.Time) {
	if c.Kind == fleet.KindMachine && c.Op == model.OpAdd {
		return
	}
	flying := 0
	for _, m := range a.meteors {
		if now.Sub(m.born) < meteorFlight {
			flying++
		}
	}
	if flying >= 3 {
		return
	}
	// Les halos se recouvrent au même endroit : on garde le plus récent.
	kept := a.meteors[:0]
	for _, m := range a.meteors {
		if now.Sub(m.born) < meteorFlight+haloLife && !(m.machine == c.Machine && m.at == at) {
			kept = append(kept, m)
		}
	}
	a.meteors = append(kept, meteor{machine: c.Machine, at: at, born: now, col: toneRGB(e.tone), from: hashF(c.Key, 17)})
}

// --- scène : ce qui ne bouge pas tant que le relevé ne change pas

type star struct {
	x, y   float64 // en points braille
	fg     canvas.Color
	id     string
	bright bool
}

// ray est un rayon d'aurore : un port ouvert à tous.
type ray struct {
	x, y0  float64 // pied du rayon, en points braille
	tilt   float64 // décalage horizontal par point de hauteur
	top    float64 // hauteur atteinte, en points
	k      float32
	col    canvas.RGB
	id     string
	phase  float64
	period float64 // secondes d'une respiration
	wide   bool    // un port à toi : trait double
}

type comet struct {
	x, y   float64 // tête, en points braille
	state  string
	id     string
	veiled bool
}

// flame est une protubérance : une appli en échec, ou qui répond en erreur.
type flame struct {
	x, y0 float64 // pied, en points braille
	k     float32
	h     float64 // hauteur, en points
	id    string
	phase float64
}

type scene struct {
	W, rows int
	base    *canvas.Pixels // W × 2·rows pixels
	work    *canvas.Pixels
	regions []span
	stars   []star
	rays    []ray
	comets  []comet
	flames  []flame
	eclipse []int            // régions voilées
	pos     map[string]uv    // élément → place
	reg     map[string]int   // élément → région
	land    []float64        // hauteur de la terre par colonne, en pixels
	byName  map[string]int   // machine → région
	items   map[string]*item // élément → élément
}

// regionsFor partage la largeur entre les machines.
func regionsFor(W, n int) []span {
	if n < 1 {
		n = 1
	}
	out := make([]span, n)
	for i := range out {
		out[i] = span{i * W / n, (i + 1) * W / n}
	}
	return out
}

// place convertit une position de ciel en points braille (2 par case en
// largeur, 4 en hauteur).
func (sc *scene) place(r int, p uv) (float64, float64) {
	sp := sc.regions[r]
	w := float64(sp.x1 - sp.x0)
	x := float64(sp.x0)*2 + 1 + p.u*(w*2-2)
	h := float64(sc.rows) * 4
	y := 1 + p.v*(h-6)
	return x, y
}

// horizonAt renvoie la hauteur de l'horizon sous la colonne de points x, en
// points depuis le haut du ciel.
func (sc *scene) horizonAt(x float64) float64 {
	c := int(x / 2)
	c = max(0, min(c, len(sc.land)-1))
	return float64(sc.rows)*4 - sc.land[c]*2
}

// treeline dessine la lisière : des collines basses et des bouquets de
// conifères, comme au bas de la planche I, un tracé différent par machine.
func treeline(W, ph int, regions []span) []float64 {
	scale := float64(ph) / 16
	land := make([]float64, W)
	for i, r := range regions {
		seed := float64(i)*2.7 + 0.4
		for x := r.x0; x < r.x1; x++ {
			fx := float64(x)
			hill := 0.9 + 0.45*math.Sin(fx*0.13+seed) + 0.3*math.Sin(fx*0.31+seed*2)
			land[x] = math.Max(0.5, hill) * scale
		}
		for x := r.x0; x < r.x1; x++ {
			grove := math.Sin(float64(x)*0.09+seed*3) > -0.2
			n := hash32(string(rune(x)), uint32(i)+41)
			if !grove || n%100 >= 45 {
				continue
			}
			th := (1.6 + float64(n>>8%100)/100*1.6) * scale
			land[x] = math.Max(land[x], th)
			if x > r.x0 {
				land[x-1] = math.Max(land[x-1], th*0.6)
			}
			if x+1 < r.x1 {
				land[x+1] = math.Max(land[x+1], th*0.6)
			}
		}
	}
	return land
}

// buildScene compose le ciel de W colonnes sur rows rangées.
func buildScene(w *world, W, rows int) *scene {
	n := len(w.machines)
	sc := &scene{W: W, rows: rows, regions: regionsFor(W, n), pos: map[string]uv{}, reg: map[string]int{},
		byName: map[string]int{}, items: map[string]*item{}}
	ph := rows * 2
	sc.base = canvas.NewPixels(W, ph)
	sc.work = canvas.NewPixels(W, ph)

	// Fond : le dégradé de la planche I, nuit verte en haut, lueur saumon au ras
	// de l'horizon, avec le grain de la gravure (la planche floutée, atténuée).
	grain := art.Get(art.Aurora).Resample(art.Rect{X0: 0, Y0: 0, X1: 1, Y1: 0.35}, W, ph)
	art.Blur(grain, 1)
	for y := 0; y < ph; y++ {
		t := float32(y) / float32(max(1, ph-1))
		var c canvas.RGB
		if t < 0.55 {
			c = canvas.Lerp(nightTop, nightMid, t/0.55)
		} else {
			c = canvas.Lerp(nightMid, nightLow, (t-0.55)/0.45)
		}
		for x := 0; x < W; x++ {
			k := 0.85 + 0.35*grain.At(x, y).Luma()
			p := c.Scale(k)
			if t > 0.6 {
				g := (t - 0.6) / 0.4
				p = p.Plus(salmonGlow.Scale(0.09 * g * g))
			}
			sc.base.Set(x, y, p)
		}
	}

	// La terre, en silhouette.
	sc.land = treeline(W, ph, sc.regions)
	for x := 0; x < W; x++ {
		h := sc.land[x]
		for y := ph - 1; y >= 0; y-- {
			d := float64(ph - 1 - y) // pixels sous ce pixel
			switch {
			case d+1 <= h:
				sc.base.Set(x, y, landTop)
			case d < h:
				sc.base.Set(x, y, canvas.Lerp(sc.base.At(x, y), landTop, float32(h-d)))
			}
		}
	}

	for i, mv := range w.machines {
		sc.byName[mv.m.Name] = i
		veiled := !mv.m.Reachable
		if veiled {
			sc.eclipse = append(sc.eclipse, i)
			r := sc.regions[i]
			for y := 0; y < ph; y++ {
				for x := r.x0; x < r.x1; x++ {
					p := sc.base.At(x, y)
					l := p.Luma()
					sc.base.Set(x, y, canvas.Lerp(canvas.RGB{R: l, G: l, B: l}, eclipseShade, 0.55))
				}
			}
		}
		for _, it := range mv.items {
			p := itemUV(it)
			sc.pos[it.id], sc.reg[it.id], sc.items[it.id] = p, i, it
			x, y := sc.place(i, p)
			switch it.kind {
			case kindAgent:
				sc.comets = append(sc.comets, comet{x: x, y: y, state: it.agent.State, id: it.id, veiled: veiled})
			case kindApp:
				a := it.app
				if !veiled && (a.State == model.AppFailed || (a.State == model.AppRunning && a.Health.HTTP >= 400)) {
					k, h := float32(1), 11.0
					if a.State != model.AppFailed {
						k, h = 0.6, 7
					}
					sc.flames = append(sc.flames, flame{x: x, y0: sc.horizonAt(x), k: k, h: h * float64(rows) / 8, id: it.id, phase: hashF(it.id, 3)})
				}
				if it.appPort != nil && it.appPort.Exposure == "all" && !veiled {
					sc.rays = append(sc.rays, sc.rayFor(i, x, it.id, 1, rayCore, true))
				}
				fg := mint
				switch {
				case veiled:
					fg = inkGhost
				case a.State != model.AppRunning:
					fg = inkFaint
				}
				sc.stars = append(sc.stars, star{x: x, y: y, fg: fg, id: it.id, bright: a.State == model.AppRunning})
			case kindPort, kindForeign:
				l := it.port
				if l.Exposure == "all" && !veiled {
					if l.Mine {
						sc.rays = append(sc.rays, sc.rayFor(i, x, it.id, 0.9, rayCore, true))
					} else {
						sc.rays = append(sc.rays, sc.rayFor(i, x, it.id, 0.5, rayForeign, false))
					}
				}
				fg := ink
				switch {
				case veiled:
					fg = inkGhost
				case !l.Mine:
					fg = inkFaint
				}
				sc.stars = append(sc.stars, star{x: x, y: y, fg: fg, id: it.id})
			}
		}
	}
	return sc
}

// veiled dit si la région r est celle d'une machine injoignable.
func (sc *scene) veiled(r int) bool {
	for _, e := range sc.eclipse {
		if e == r {
			return true
		}
	}
	return false
}

// rayFor prépare un rayon qui part de l'horizon sous le point x, penché comme
// les rayons de la planche I : ils rayonnent d'un centre caché sous
// l'horizon, au milieu de la région.
func (sc *scene) rayFor(r int, x float64, id string, k float32, col canvas.RGB, wide bool) ray {
	sp := sc.regions[r]
	mid := float64(sp.x0 + sp.x1) // en points
	h := float64(sc.rows) * 4
	tilt := (x - mid) / (h * 2.4)
	top := h * (0.55 + 0.4*hashF(id, 23))
	return ray{x: x, y0: sc.horizonAt(x), tilt: tilt, top: top, k: k, col: col, id: id,
		phase: hashF(id, 21), period: 12 + 8*hashF(id, 22), wide: wide}
}

// glow ajoute une lueur ronde de rayon r pixels.
func glow(p *canvas.Pixels, cx, cy, r float64, col canvas.RGB, k float32) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			d := math.Sqrt(dx*dx+dy*dy) / r
			if d < 1 {
				f := float32((1 - d) * (1 - d))
				p.Add(x, y, col, k*f)
			}
		}
	}
}

// skyState est ce qui, hors du relevé, change l'aspect du ciel.
type skyState struct {
	now      time.Time
	step     int64          // pas de 250 ms depuis l'ouverture
	selected string         // élément choisi : un fil d'aplomb le relie à l'horizon
	matches  map[string]int // éléments trouvés par la recherche
	search   bool
	refused  int // région d'un refus récent (-1 : aucune)
	flareAge time.Duration
	meteors  []meteor
	pulse    float64 // 0..1, respiration des agents qui attendent
}

// breath renvoie l'éclat d'un rayon à l'instant du pas : une respiration lente,
// sur quatre niveaux seulement, pour que peu de cases changent à chaque pas.
// Les rayons des autres comptes ne respirent pas : seuls les tiens vivent.
func (r *ray) breath(step int64) float32 {
	if !r.wide {
		return 0.9
	}
	t := float64(step) * stepDur.Seconds()
	s := math.Sin(2 * math.Pi * (t/r.period + r.phase))
	lvl := math.Round((s + 1) / 2 * 3)
	return float32(0.76 + 0.08*lvl)
}

// render dessine le ciel dans la toile, à partir de la rangée y0.
func (sc *scene) render(cv *canvas.Canvas, y0 int, st skyState) {
	p := sc.work
	p.CopyFrom(sc.base)
	oy := float64(y0) * 4

	// 1. Lumières douces, en pixels : pied et voile des rayons, halos des
	// changements, base des protubérances.
	for i := range sc.rays {
		r := &sc.rays[i]
		k := r.k * 0.85 // la lueur ne respire pas : seul le trait respire
		if st.search && st.matches[r.id] == 0 {
			k *= 0.3
		}
		for hgt := 0.0; hgt < r.top; hgt += 2 {
			fade := float32(math.Pow(1-hgt/r.top, 1.3))
			px, py := float64(int(r.x/2))+0.5, (r.y0-hgt)/2-0.5
			for dx := -3; dx <= 3; dx++ {
				xx := int(math.Floor(px)) + dx
				d := float64(xx) + 0.5 - px
				w := float32(math.Exp(-d * d / 2.6))
				p.Add(xx, int(py), rayGlow, k*fade*w*0.3)
			}
		}
		// Le pied du rayon éclaire l'horizon, comme l'arc de la planche I.
		glow(p, r.x/2, r.y0/2-0.5, 2.6, rayGlow, k*0.25)
	}
	for _, f := range sc.flames {
		glow(p, f.x/2, f.y0/2-0.5, 2.2, flameRed, 0.35*f.k)
	}
	for _, m := range st.meteors {
		r, ok := sc.byName[m.machine]
		if !ok || sc.veiled(r) {
			continue // un ciel voilé n'a plus de lumières : l'éclipse suffit
		}
		if age := st.now.Sub(m.born); age >= meteorFlight {
			k := float32(0.45 * (1 - (age-meteorFlight).Seconds()/haloLife.Seconds()))
			if k > 0 {
				bx, by := sc.place(r, m.at)
				glow(p, bx/2, by/2, 2.4, m.col, k)
			}
		}
	}
	if r := st.refused; r >= 0 && r < len(sc.regions) {
		sp := sc.regions[r]
		for x := sp.x0; x < sp.x1; x++ {
			glow(p, float64(x)+0.5, (sc.horizonAt(float64(x)*2+1))/2-0.5, 1.6, flameRed, 0.12*sc.flareK(st))
		}
	}

	cv.Blit(p, 0, y0)

	// 2. Traits fins, en points braille.

	// Éclipses : la couronne d'un disque sombre.
	for _, r := range sc.eclipse {
		sp := sc.regions[r]
		cx := float64(sp.x0)*2 + float64(sp.x1-sp.x0)*2*0.8
		cy := float64(sc.rows) * 4 * 0.28
		rad := math.Min(7, float64(sc.rows)*4*0.2)
		for a := 0.0; a < 2*math.Pi; a += 0.2 {
			cv.DotAt(cx+math.Cos(a)*rad*1.1, oy+cy+math.Sin(a)*rad, canvas.Hex(0x86938f))
		}
	}

	// Rayons : un trait fin et droit, plein au pied, en tirets vers le haut,
	// comme les rayons de la planche I qui se perdent dans la nuit.
	for i := range sc.rays {
		r := &sc.rays[i]
		k := float64(r.k * r.breath(st.step))
		if st.search && st.matches[r.id] == 0 {
			k *= 0.3
		}
		cx := max(0, min(int(r.x/2), sc.W-1))
		// Le pied s'arrête à la terre : le plus bas pixel de ciel de la colonne,
		// et un demi-trait (╵) si la case du pied est à moitié terre.
		sky := int(math.Floor(float64(sc.rows*2) - 0.5 - sc.land[cx]))
		if sky < 0 {
			continue
		}
		base := sky / 2
		cells := int(r.top / 4)
		for j := 0; j <= cells; j++ {
			y := base - j
			if y < 0 {
				break
			}
			fade := 1 - float64(j)/float64(cells+1)
			glyph := '│'
			switch {
			case j == 0 && sky%2 == 0:
				glyph = '╵'
			case fade < 0.35:
				glyph = '┊'
			case fade < 0.65:
				glyph = '┆'
			}
			fg := canvas.Mix(inkGhost, r.col.Color(), clamp01(k*(0.3+0.7*fade)))
			cv.SetRune(cx, y0+y, glyph, fg, 0)
		}
	}

	// Protubérances : deux filaments qui ondulent en montant.
	for _, f := range sc.flames {
		phi := f.phase*2*math.Pi + float64(st.step/2)*0.2 // une ondulation par demi-seconde
		flameDots(cv, f.x, oy+f.y0-1, f.h, 3, phi, f.k)
	}
	if r := st.refused; r >= 0 && r < len(sc.regions) {
		k := sc.flareK(st)
		sp := sc.regions[r]
		for x := sp.x0 + 2; x < sp.x1-2; x += 5 {
			fx := float64(x) * 2
			seed := hashF(string(rune(x)), 5)
			h := (6 + 8*seed) * float64(sc.rows) / 8 * float64(k)
			flameDots(cv, fx, oy+sc.horizonAt(fx)-1, h, 2.5, seed*6+float64(st.step)*0.15, k)
		}
	}

	// Étoiles : leur encre ne dit que des faits (état, propriétaire, voile,
	// recherche, choix) ; aucune ne pâlit ni ne scintille au hasard.
	for _, s := range sc.stars {
		fg := s.fg
		if st.search {
			if st.matches[s.id] > 0 {
				fg = gold
			} else {
				fg = inkGhost
			}
		}
		if s.id == st.selected {
			fg = gold
		}
		cv.DotAt(s.x, oy+s.y, fg)
		if s.bright {
			cv.DotAt(s.x+1, oy+s.y, fg)
		}
	}

	// Comètes : tête et chevelure ; l'agent qui attend est une étoile d'or.
	for _, c := range sc.comets {
		y := oy + c.y
		if c.state == model.StateWaiting && !c.veiled {
			col := canvas.Mix(goldDim, canvas.Hex(0xffe3a3), st.pulse)
			if st.search && st.matches[c.id] == 0 {
				col = goldDim
			}
			cv.SetRune(int(c.x/2), int(y/4), '✦', col, canvas.Bold)
			continue
		}
		head, tail := inkBright, 9
		switch {
		case c.veiled:
			head, tail = inkGhost, 2
		case c.state == model.StateIdle:
			head, tail = inkDim, 4
		case c.state != model.StateWorking:
			head, tail = inkFaint, 2
		}
		if st.search && st.matches[c.id] == 0 {
			head = inkGhost
		}
		if c.id == st.selected || (st.search && st.matches[c.id] > 0) {
			head = gold
		}
		for k := tail; k >= 1; k-- {
			t := float64(k) / float64(tail+1)
			cv.DotAt(c.x-float64(k)*1.2, y-float64(k)*0.6, canvas.Mix(head, inkGhost, t))
		}
		cv.DotAt(c.x, y, head)
		cv.DotAt(c.x+1, y, head)
	}

	// Météores en vol : une tête blanche, une traînée qui s'éteint.
	for _, m := range st.meteors {
		r, ok := sc.byName[m.machine]
		if !ok {
			continue
		}
		age := st.now.Sub(m.born)
		if age >= meteorFlight {
			continue
		}
		tx, ty := sc.place(r, m.at)
		ty += oy
		ang := math.Pi/5 + m.from*math.Pi/6 // d'en haut à droite
		dist := 30.0
		sx, sy := tx+math.Cos(ang)*dist, ty-math.Sin(ang)*dist
		t := age.Seconds() / meteorFlight.Seconds()
		e := 1 - math.Pow(1-t, 3)
		hx, hy := sx+(tx-sx)*e, sy+(ty-sy)*e
		dx, dy := (sx-tx)/dist, (sy-ty)/dist
		for k := 12; k >= 1; k-- {
			f := float64(k) / 13
			cv.DotAt(hx+dx*float64(k)*1.3, hy+dy*float64(k)*1.3, canvas.Mix(m.col.Color(), inkGhost, f))
		}
		cv.DotAt(hx, hy, meteorHead.Color())
	}

	// Fil d'aplomb : de l'élément choisi jusqu'à l'horizon.
	if st.selected != "" {
		if r, ok := sc.reg[st.selected]; ok {
			x, y := sc.place(r, sc.pos[st.selected])
			bottom := sc.horizonAt(x) - 1
			for yy := y + 3; yy < bottom; yy += 2 {
				cv.DotAt(x, oy+yy, goldDim)
			}
		}
	}
}

// flareK dit la force des protubérances après un refus : déclinante pendant 3 s.
func (sc *scene) flareK(st skyState) float32 {
	return float32(math.Max(0, 1-st.flareAge.Seconds()/3))
}

// flameDots dessine une protubérance : deux filaments qui s'enroulent en
// montant de (x, y) sur h points, d'amplitude a points.
func flameDots(cv *canvas.Canvas, x, y, h, a, phi float64, k float32) {
	if h < 1 {
		return
	}
	for s := 0.0; s <= 1; s += 0.5 / h {
		yy := y - s*h
		amp := a * (0.3 + s)
		for strand := 0.0; strand < 2; strand++ {
			xx := x + amp*math.Sin(s*2.4*math.Pi+phi+strand*math.Pi)
			fg := canvas.Mix(red, canvas.Hex(0x5a1612), clamp01(s*0.9+float64(1-k)*0.5))
			cv.DotAt(xx, yy, fg)
		}
	}
}

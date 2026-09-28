package ui

import (
	"math"

	"github.com/Alexry375/connex/internal/fleet"
	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// Le soleil est celui du Retable n° 1 : un disque d'or, une couronne de
// pointes crème entre des coins verts à bout bleu, un anneau pâle ; il est
// accroché au bord du haut, coupé comme dans le tableau. Chaque agent en cours
// allume une pointe de la couronne : d'or s'il t'attend, bleue s'il travaille,
// bronze s'il se repose. Quand un agent t'attend, le disque s'allume et sa
// pointe respire ; sinon le soleil reste terni et immobile.

// Proportions du soleil, en rayons du disque.
const (
	coronaOut = 1.40 // bout des pointes
	ringIn    = 1.45 // anneau
	ringOut   = 1.56
	rayMax    = ringOut
)

// La lueur qui passe sur l'or quand un agent t'attend.
const (
	sheenEvery = 11.0 // secondes
	sheenSpan  = 1.6
)

// Couleurs de la couronne, prélevées dans le tableau.
var (
	cSpike = canvas.Hex(0xf8e1b7) // pointe crème
	cWedge = canvas.Hex(0x6f7f5c) // coin vert
	cTip   = canvas.Hex(0xa8c8ee) // bout bleu
	cRing  = canvas.Hex(0xa9adb6) // anneau (le tableau le peint lavande ; ici, argent)
)

// ray est un agent vu du soleil.
type ray struct {
	state string
	fresh float64 // 1 juste après que l'agent s'est mis à attendre, puis 0
}

func (a *app) rays() []ray {
	var out []ray
	for i := range a.all {
		e := &a.all[i]
		if e.agent == nil || e.ghost {
			continue
		}
		r := ray{state: e.agent.State}
		if t, ok := a.w.since[e.id]; ok && r.state == model.StateWaiting {
			r.fresh = max(0, 1-a.now.Sub(t).Seconds()/3)
		}
		out = append(out, r)
	}
	return out
}

func (a *app) drawSun(c *canvas.Canvas, l layout) {
	rays := a.rays()
	waiting := 0
	call := 0.0 // un agent vient de se mettre à attendre : un anneau d'or s'élargit
	for _, r := range rays {
		if r.state == model.StateWaiting {
			waiting++
		}
		call = math.Max(call, r.fresh)
	}
	t := a.now.Sub(a.born).Seconds()
	breath := a.breath()
	bright := 0.55
	if waiting > 0 {
		bright = 0.98
	}
	sun := pic("soleil")
	R := l.sunR
	cx, cy := l.sunCX, l.sunCY
	box := l.sun

	// les pointes : 36 autour du cercle, comme dans le tableau, davantage s'il
	// y a plus d'agents que de pointes visibles
	a0, a1 := visibleArc(cy, R*1.2)
	n := len(rays)
	spikes := max(int(math.Round(2*math.Pi*R*1.2/3)), 12) // une pointe tous les trois pixels
	if need := int(math.Ceil(float64(n) * 2 * math.Pi / (a1 - a0))); need > spikes {
		spikes = need
	}
	P := 2 * math.Pi / float64(spikes)
	first := int(math.Ceil((a0 + math.Pi) / P))
	last := int(math.Floor((a1+math.Pi)/P)) - 1
	agentAt := map[int]int{} // pointe → agent
	if vis := last - first + 1; n > 0 && vis > 0 {
		for k := 0; k < n; k++ {
			j := first + int((float64(k)+0.5)*float64(vis)/float64(n))
			agentAt[j] = k
		}
	}
	// toutes les 11 s, une lueur traverse l'or en 1,6 s ; entre deux, le
	// disque ne change pas et le terminal n'a rien à repeindre
	sheen := math.Inf(1)
	if u := (math.Mod(t, sheenEvery) - (sheenEvery - sheenSpan)) / sheenSpan; u >= 0 {
		sheen = 2.8*u - 1.4
	}
	callR := R * (1.05 + 0.6*(1-call))
	for py := 0; py < 2*box.h; py++ {
		for px := box.x; px < box.x+box.w; px++ {
			dx := float64(px) + 0.5 - cx
			dy := float64(py) + 0.5 - cy
			d := math.Hypot(dx, dy)
			if d > R*rayMax+1 {
				continue
			}
			var col canvas.Color
			alpha := 1.0
			switch {
			case d < R+0.5:
				// le disque d'or, assombri vers le bord
				col = sun.at(0.5+0.3*dx/R, 0.40+0.3*dy/R)
				limb := 0.74 + 0.26*math.Sqrt(math.Max(0, 1-(d/R)*(d/R)))
				glow := 0.0
				if s := (dx*0.8+dy*0.35)/R - sheen; waiting > 0 && math.Abs(s) < 0.5 {
					glow = 0.16 * math.Exp(-s*s/0.06)
				}
				col = col.Scale(bright*limb + glow)
			case d < R*coronaOut:
				s := (d - R) / (R*coronaOut - R)
				pos := (math.Atan2(dy, dx) + math.Pi) / P
				j := int(math.Floor(pos))
				f := pos - float64(j)
				spike := math.Abs(f-0.5) < 0.46*(1-s)+0.04
				k, isAgent := agentAt[j]
				switch {
				case isAgent && math.Abs(f-0.5) < 0.42:
					col = spikeColor(rays[k].state, breath)
				case spike:
					col = cSpike.Scale(0.42 * (bright + 0.3))
				case s > 0.72:
					col = cTip.Scale(0.36 * (bright + 0.3))
				default:
					col = cWedge.Scale(0.5 * (bright + 0.3))
				}
			case d < R*ringOut+0.5 && waitingSpike(dx, dy, P, agentAt, rays):
				// la pointe d'un agent qui t'attend traverse l'anneau
				col = spikeColor(model.StateWaiting, breath)
				alpha = math.Min(R*ringOut+0.5-d, 1)
			case d >= R*ringIn && d < R*ringOut:
				col = cRing.Scale(0.34 * (bright + 0.3))
				alpha = math.Min(math.Min(d-R*ringIn, R*ringOut-d)+0.5, 1)
			default:
				continue
			}
			c.BlendPixel(px, py, col, alpha)
			if call > 0 && math.Abs(d-callR) < 1 {
				c.BlendPixel(px, py, cGoldBright, 0.6*call*(1-math.Abs(d-callR)))
			}
		}
	}
}

// waitingSpike dit si la direction (dx, dy) tombe au milieu de la pointe d'un
// agent qui t'attend.
func waitingSpike(dx, dy, P float64, agentAt map[int]int, rays []ray) bool {
	pos := (math.Atan2(dy, dx) + math.Pi) / P
	j := int(math.Floor(pos))
	k, ok := agentAt[j]
	return ok && rays[k].state == model.StateWaiting && math.Abs(pos-float64(j)-0.5) < 0.3
}

// spikeColor est la couleur d'une pointe allumée par un agent.
func spikeColor(state string, breath float64) canvas.Color {
	switch state {
	case model.StateWaiting:
		return canvas.Mix(cGold, cGoldBright, 0.3+0.7*breath)
	case model.StateWorking:
		return cSky
	case model.StateIdle:
		return cBronze.Scale(1.6)
	}
	return cInkDim
}

// visibleArc renvoie l'intervalle d'angles (en radians, y vers le bas) où un
// cercle de rayon r centré à la hauteur cy est sous le bord du haut.
func visibleArc(cy, r float64) (float64, float64) {
	if cy >= r {
		return -math.Pi, math.Pi
	}
	// sin(a) > -cy/r pour être sous le bord (y > 0)
	s := math.Max(-1, math.Min(1, -cy/r))
	a := math.Asin(s)
	margin := 0.1
	return a + margin, math.Pi - a - margin
}

// smooth adoucit une progression de 0 à 1.
func smooth(t float64) float64 {
	t = math.Min(math.Max(t, 0), 1)
	return t * t * (3 - 2*t)
}

// ---------------------------------------------------------------- astres

// astreNames associe à chaque machine, dans l'ordre d'apparition, une rosace
// d'un des tableaux.
var astreNames = []string{"astre-1", "astre-2", "astre-3", "astre-3b"}

// eclipse dit où en est l'éclipse d'une machine : 0 astre plein, 1 éteint.
func (a *app) eclipse(m *fleet.Machine) float64 {
	t, ok := a.flips[m.Name]
	p := 1.0
	if ok {
		p = min(a.now.Sub(t).Seconds()/eclipseSpan.Seconds(), 1)
	}
	if m.Reachable {
		return 1 - smooth(p) // se rallume
	}
	return smooth(p)
}

// astreGlyph dit une machine en un signe : un disque qui s'éteint par
// quartiers quand elle devient injoignable.
func (a *app) astreGlyph(m *fleet.Machine, i int) (rune, canvas.Color) {
	e := a.eclipse(m)
	col := machineTint(i)
	if !m.Known && m.Reachable {
		return '◌', cInkDim
	}
	switch {
	case e < 0.1:
		return '●', col
	case e < 0.35:
		return '◕', canvas.Mix(col, cRed, e)
	case e < 0.6:
		return '◑', canvas.Mix(col, cRed, e)
	case e < 0.9:
		return '◔', canvas.Mix(col, cRed, e)
	}
	return '○', cRed
}

// drawMedals peint le volet droit en grand : chaque machine est une rosace
// peinte ; une machine injoignable s'éclipse, un disque sombre passe devant.
func (a *app) drawMedals(c *canvas.Canvas, r rect) {
	ms := a.machines()
	if len(ms) == 0 {
		return
	}
	slot := r.w / len(ms)
	if slot < 12 {
		a.drawMachineList(c, r)
		return
	}
	rows := r.h - 2 // le nom et l'état sous l'astre
	rad := math.Min(float64(rows*2)/2-0.5, float64(slot)/2-1.5)
	for i, m := range ms {
		x0 := r.x + i*slot
		mx := float64(x0) + float64(slot)/2
		my := float64(2*r.y) + rad + 0.5
		a.drawAstre(c, m, i, mx, my, rad)
		nameSt := fg(machineTint(i))
		switch {
		case a.focus == m.Name:
			nameSt = bold(cInk)
		case a.focus != "":
			nameSt = fg(cInkFaint)
		}
		name := canvas.Truncate(m.Name, slot-1)
		c.Text(x0+(slot-canvas.Width(name))/2, r.y+rows, name, nameSt)
		phrases, pc := a.machinePhrase(m)
		if len(phrases) == 0 {
			phrases, pc = machineSummary(m), cInkDim
		}
		p := fit(phrases, slot-1)
		c.Text(x0+(slot-canvas.Width(p))/2, r.y+rows+1, p, fg(pc))
	}
}

// machineSummary résume une machine joignable, comptée dans son relevé :
// applis et ports, ou les applis seules si la place manque.
func machineSummary(m *fleet.Machine) []string {
	s := m.Snapshot
	apps := plural(len(s.Apps), "appli", "applis")
	return []string{apps + " · " + plural(len(s.Listeners), "port", "ports"), apps}
}

// drawAstre peint la rosace d'une machine, avec son éclipse.
func (a *app) drawAstre(c *canvas.Canvas, m *fleet.Machine, i int, cx, cy, r float64) {
	img := pic(astreNames[i%len(astreNames)])
	e := a.eclipse(m)
	// le disque sombre glisse de la gauche vers le centre (s'éteint), puis du
	// centre vers la droite (se rallume)
	var ox float64
	if m.Reachable {
		ox = (1 - e) * 2.3 * r // part vers la droite
		if e <= 0 {
			ox = 99 * r
		}
	} else {
		ox = -(1 - e) * 2.3 * r
	}
	ox += 0.08 * r * e
	dim := 1.0
	if !m.Known {
		dim = 0.35
	}
	for py := int(cy - r - 1); py <= int(cy+r+1); py++ {
		for px := int(cx - r - 1); px <= int(cx+r+1); px++ {
			dx := float64(px) + 0.5 - cx
			dy := float64(py) + 0.5 - cy
			d := math.Hypot(dx, dy)
			if d > r+0.6 {
				continue
			}
			col := img.at(0.5+0.47*dx/r, 0.5+0.47*dy/r).Scale(0.92 * dim)
			alpha := math.Min(math.Max(r+0.5-d, 0), 1)
			// l'ombre
			od := math.Hypot(dx-ox, dy)
			if od < r*1.02 {
				shade := math.Min(math.Max(r*1.02-od, 0), 1)
				col = canvas.Mix(col, canvas.Hex(0x120f11), shade*0.94)
			}
			c.BlendPixel(px, py, col, alpha)
		}
	}
	// éteinte : un anneau de lumière autour, comme la couronne d'une éclipse
	if e > 0.85 {
		ring := canvas.Mix(cGround, cRed, 0.55*(e-0.85)/0.15)
		for k := 0; k < 96; k++ {
			ang := float64(k) / 96 * 2 * math.Pi
			px := cx + (r+0.9)*math.Cos(ang)
			py := cy + (r+0.9)*math.Sin(ang)
			c.BlendPixel(int(px), int(py), ring, 0.5)
		}
	}
}

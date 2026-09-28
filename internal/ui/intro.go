package ui

import (
	"strconv"
	"time"

	"github.com/Alexry375/connex/internal/ui/art"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// L'ouverture : la planche I occupe tout l'écran, réduite à ses encres (une
// douzaine de teintes tirées d'elle-même, en aplats, comme une gravure
// coloriée), puis elle se lève de bas en haut comme un rideau, un fil de
// lumière au bord : le titre et le catalogue paraissent d'abord, le ciel des
// données en dernier. Une touche l'abrège (et sert quand même).
//
// Les aplats et le rideau ménagent le terminal : chaque case change deux fois
// en tout, par rangées entières (≈ 110 Ko à 160 × 45, au lieu de 400 Ko pour un
// fondu case par case d'une image en couleurs pleines).
const (
	introDur  = 1000 * time.Millisecond
	introHold = 0.25 // part de l'ouverture où la planche reste entière
	introInks = 12
	plateInks = 14 // encres des planches II, III et IV
)

// gfxCache garde les planches réduites à la taille de l'écran.
type gfxCache struct {
	pix map[string]*canvas.Pixels
}

func (g *gfxCache) get(key string, mk func() *canvas.Pixels) *canvas.Pixels {
	if g.pix == nil {
		g.pix = map[string]*canvas.Pixels{}
	}
	p := g.pix[key]
	if p == nil {
		p = mk()
		g.pix[key] = p
	}
	return p
}

// drawIntro recouvre l'image finale par ce qui reste de la planche.
func (a *App) drawIntro(cv *canvas.Canvas, now time.Time) {
	edge := introEdge(now.Sub(a.opened), cv.H)
	if edge <= 0 {
		return
	}
	W, H := cv.W, cv.H
	img := a.gfx.get("intro:"+strconv.Itoa(W)+"x"+strconv.Itoa(H), func() *canvas.Pixels {
		im := art.Get(art.Aurora)
		p := im.Resample(im.Fit(art.Full, float64(W)/float64(2*H), 0.7), W, 2*H)
		art.Posterize(p, introInks)
		return p
	})
	for y := 0; y < min(edge, H); y++ {
		for x := 0; x < W; x++ {
			top, bot := img.At(x, 2*y).Color(), img.At(x, 2*y+1).Color()
			if top == bot {
				cv.Set(x, y, canvas.Cell{R: ' ', BG: top})
			} else {
				cv.Set(x, y, canvas.Cell{R: canvas.HalfTop, FG: top, BG: bot})
			}
		}
	}
	// Le fil de lumière, au bord du rideau.
	if edge < H {
		for x := 0; x < W; x++ {
			cv.Set(x, edge-1, canvas.Cell{R: '─', FG: rayCore.Color(), BG: landBG})
		}
	}
}

// introEdge renvoie la rangée où s'arrête la planche, d rangées après
// l'ouverture : H pendant la pose, puis le rideau monte jusqu'à 0.
func introEdge(d time.Duration, H int) int {
	t := d.Seconds() / introDur.Seconds()
	switch {
	case t >= 1:
		return 0
	case t <= introHold:
		return H
	}
	u := (t - introHold) / (1 - introHold)
	u = u * u * (3 - 2*u) // départ et arrivée en douceur
	return H - int(float64(H+1)*u)
}

package ui

import (
	"math"
	"time"

	"github.com/Alexry375/connex/internal/ui/canvas"
)

// À l'ouverture, le Retable n° 1 couvre l'écran ; il se fend en deux volets
// qui se replient vers les bords en moins d'une seconde, sur le retable vivant
// déjà en place. Une touche l'ouvre d'un coup.

// curtainPalette : les couleurs de connex dans lesquelles le tableau est
// repeint. Il y perd son rose et son violet, et le terminal n'a qu'une
// vingtaine de couleurs à changer : l'image arrive d'un coup, même dans un
// terminal lent.
var curtainPalette = []canvas.Color{
	cGround, cGround3, cInkFaint, cInkDim, cInkSoft, cInk,
	cBronze, cGoldDeep, cGold, cGoldBright,
	pyramid[0], cRed, cVermilion, cOrange, pyramid[2],
	cSage, cWedge, pyramid[3],
	cSky, cTip, pyramid[4], pyramid[5], pyramid[6], cRing,
}

// curtain est le tableau repeint à la taille de l'écran, en pixels (deux par
// case en hauteur) : calculé une fois par taille, puis découvert.
type curtain struct {
	w, h int
	pix  []canvas.Color
}

// newCurtain couvre w × h pixels avec le Retable, sans le déformer : ce qui
// dépasse en hauteur est coupé.
func newCurtain(w, h int) *curtain {
	img := pic("tableau-4")
	u0, v0, du, dv := 0.0, 0.0, 1.0, 1.0
	if ia, wa := float64(img.w)/float64(img.h), float64(w)/float64(h); wa > ia {
		dv = ia / wa
		v0 = (1 - dv) / 2
	} else {
		du = wa / ia
		u0 = (1 - du) / 2
	}
	cu := &curtain{w: w, h: h, pix: make([]canvas.Color, w*h)}
	sx, sy := du/float64(w), dv/float64(h)
	for y := 0; y < h; y++ {
		v := v0 + float64(y)*sy
		for x := 0; x < w; x++ {
			u := u0 + float64(x)*sx
			cu.pix[y*w+x] = nearest(img.area(u, v, u+sx, v+sy), curtainPalette)
		}
	}
	return cu
}

// nearest renvoie la couleur de pal la plus proche de c, à l'œil (distance
// « redmean », qui pèse le vert plus que le rouge et le bleu).
func nearest(c canvas.Color, pal []canvas.Color) canvas.Color {
	r, g, b := c.RGB()
	best, bd := pal[0], math.Inf(1)
	for _, p := range pal {
		pr, pg, pb := p.RGB()
		rm := (float64(r) + float64(pr)) / 2
		dr, dg, db := float64(r)-float64(pr), float64(g)-float64(pg), float64(b)-float64(pb)
		if d := (2+rm/256)*dr*dr + 4*dg*dg + (2+(255-rm)/256)*db*db; d < bd {
			best, bd = p, d
		}
	}
	return best
}

func (a *app) drawOpening(c *canvas.Canvas, el time.Duration) {
	p := smooth(el.Seconds() / openingSpan.Seconds())
	lw := int(math.Round(float64(c.W) / 2 * (1 - p)))
	if lw <= 0 {
		return
	}
	H2 := 2 * c.H
	if a.curtain == nil || a.curtain.w != c.W || a.curtain.h != H2 {
		a.curtain = newCurtain(c.W, H2)
	}
	// Le tableau reste en place et les volets se replient vers les bords :
	// d'une image à l'autre, seules les colonnes découvertes changent.
	pix := a.curtain.pix
	for y := 0; y < H2; y++ {
		for x := 0; x < lw; x++ {
			xr := c.W - 1 - x
			c.Pixel(x, y, pix[y*c.W+x])
			c.Pixel(xr, y, pix[y*c.W+xr])
		}
	}
	// au bord de chaque volet, un fil de lumière d'or
	for y := 0; y < H2; y++ {
		c.BlendPixel(lw-1, y, cGoldBright, 0.75)
		c.BlendPixel(c.W-lw, y, cGoldBright, 0.75)
	}
}

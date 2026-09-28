package ui

import (
	"strconv"
	"time"

	"github.com/Alexry375/connex/internal/ui/art"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// La planche III : le journal d'observation, la nouvelle la plus récente en
// haut, sous un bandeau tiré des météores de novembre.

func (a *App) drawJournal(cv *canvas.Canvas, now time.Time) {
	W, H := a.W, a.H
	cv.Clear(landBG)
	paperRows := a.paperRows()
	bottom := H - paperRows
	band := min(6, max(2, H/7))
	img := a.gfx.get("journal:"+strconv.Itoa(W)+"x"+strconv.Itoa(band), func() *canvas.Pixels {
		im := art.Get(art.Meteors)
		p := im.Resample(im.Fit(art.Rect{X0: 0, Y0: 0, X1: 1, Y1: 0.6}, float64(W)/float64(2*band), 0.15), W, band*2)
		for i, c := range p.P {
			p.P[i] = c.Scale(0.8)
		}
		art.Posterize(p, plateInks)
		return p
	})
	cv.Blit(img, 0, 0)
	n := len(a.journal)
	head := "Rien n’a changé depuis l’ouverture."
	if n > 0 {
		head = plural(n, "changement observé", "changements observés") + " depuis l’ouverture, le plus récent en haut."
	}
	cv.Text(2, band-1, W-1, head, inkBright, canvas.Italic)

	y0 := band + 1
	rows := bottom - y0 - 1
	if rows < 1 || n == 0 {
		a.drawPaper(cv, bottom, paperRows, 3, now)
		return
	}
	a.jsel = min(max(0, a.jsel), n-1)
	// La nouvelle choisie se lit en entier : sa suite prend jusqu'à deux
	// rangées sous elle. Les flèches ont la dernière colonne, après un blanc.
	x, w := 1, W-2
	sel := a.journal[n-1-a.jsel]
	extra := max(0, min(2, rows-1, len(entryLines(sel.text, x+w-1-a.entryX(x, sel, true), 3))-1))
	top := a.scroll["\x00journal"]
	if a.jsel < top {
		top = a.jsel
	}
	if a.jsel+extra >= top+rows {
		top = a.jsel + extra - rows + 1
	}
	top = max(0, min(top, n-rows+extra))
	a.scroll["\x00journal"] = top
	y, k := y0, top
	for ; y < y0+rows && k < n; k++ {
		e := a.journal[n-1-k]
		if k == a.jsel {
			cv.Fill(0, y, W, min(1+extra, y0+rows-y), selectBG)
			y += a.drawEntry(cv, x, y, w, e, true, min(1+extra, y0+rows-y), now)
			continue
		}
		a.drawEntry(cv, x, y, w, e, true, 1, now)
		y++
	}
	if top > 0 {
		cv.Text(W-1, y0, W, "↑", inkFaint, 0)
	}
	if k < n {
		cv.Text(W-1, y0+rows-1, W, "↓", inkFaint, 0)
	}
	a.drawPaper(cv, bottom, paperRows, 3, now)
}

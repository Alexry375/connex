package ui

import (
	"embed"
	"image"
	"image/jpeg"
	"math"
	"sync"

	"github.com/Alexry375/connex/internal/ui/canvas"
)

// Les images viennent des quatre tableaux de Hilma af Klint donnés au lot
// (inputs/univers, 1.jpg à 4.jpg) : le Retable n° 1 entier pour l'ouverture,
// son soleil, et une rosace par tableau des Dix Plus Grands, qui devient
// l'astre d'une machine. Découpes (centre, demi-côté, en pixels de l'original)
// puis réduction, JPEG qualité 92 :
//
//	soleil.jpg     4.jpg  (930, 462)  r 392 → 128 × 128  le disque d'or du Retable
//	astre-1.jpg    1.jpg  (725, 420)  r 345 →  96 × 96   la rosace de Jeunesse
//	astre-2.jpg    2.jpg  (1640, 252) r 245 →  96 × 96   le disque vert d'Âge adulte
//	astre-3.jpg    3.jpg  (350, 355)  r 330 →  96 × 96   la rosace de gauche de Vieillesse
//	astre-3b.jpg   3.jpg  (1000, 355) r 330 →  96 × 96   celle de droite
//	tableau-4.jpg  4.jpg  entier      → 150 de large
//
//go:embed assets/*.jpg
var assetFS embed.FS

// picture est une image décodée, prête à être échantillonnée.
type picture struct {
	w, h int
	pix  []canvas.Color
}

var (
	picOnce sync.Once
	pics    map[string]*picture
)

// pic renvoie l'image nommée (sans « .jpg »). Une image illisible devient un
// aplat du noir du fond : l'affichage continue sans elle.
func pic(name string) *picture {
	picOnce.Do(func() {
		pics = map[string]*picture{}
		for _, n := range []string{"soleil", "astre-1", "astre-2", "astre-3", "astre-3b", "tableau-4"} {
			pics[n] = decodePicture("assets/" + n + ".jpg")
		}
	})
	if p := pics[name]; p != nil {
		return p
	}
	return &picture{w: 1, h: 1, pix: []canvas.Color{cGround}}
}

func decodePicture(path string) *picture {
	f, err := assetFS.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		return nil
	}
	return fromImage(img)
}

func fromImage(img image.Image) *picture {
	b := img.Bounds()
	p := &picture{w: b.Dx(), h: b.Dy(), pix: make([]canvas.Color, b.Dx()*b.Dy())}
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			p.pix[y*p.w+x] = canvas.RGB(uint8(r>>8), uint8(g>>8), uint8(bl>>8))
		}
	}
	return p
}

// at échantillonne l'image en (u, v), coordonnées de 0 à 1, par interpolation
// bilinéaire ; hors de l'image, le bord le plus proche.
func (p *picture) at(u, v float64) canvas.Color {
	x := u*float64(p.w) - 0.5
	y := v*float64(p.h) - 0.5
	x0, y0 := math.Floor(x), math.Floor(y)
	fx, fy := x-x0, y-y0
	c00 := p.px(int(x0), int(y0))
	c10 := p.px(int(x0)+1, int(y0))
	c01 := p.px(int(x0), int(y0)+1)
	c11 := p.px(int(x0)+1, int(y0)+1)
	return canvas.Mix(canvas.Mix(c00, c10, fx), canvas.Mix(c01, c11, fx), fy)
}

func (p *picture) px(x, y int) canvas.Color {
	x = min(max(x, 0), p.w-1)
	y = min(max(y, 0), p.h-1)
	return p.pix[y*p.w+x]
}

// area fait la moyenne de l'image sur le rectangle (u0, v0)–(u1, v1) : pour
// réduire fortement sans scintillement.
func (p *picture) area(u0, v0, u1, v1 float64) canvas.Color {
	x0 := int(math.Floor(u0 * float64(p.w)))
	x1 := int(math.Ceil(u1 * float64(p.w)))
	y0 := int(math.Floor(v0 * float64(p.h)))
	y1 := int(math.Ceil(v1 * float64(p.h)))
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	var sr, sg, sb, n float64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b := p.px(x, y).RGB()
			sr += float64(r)
			sg += float64(g)
			sb += float64(b)
			n++
		}
	}
	return canvas.RGB(uint8(sr/n), uint8(sg/n), uint8(sb/n))
}

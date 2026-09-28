// Package art garde les quatre planches de Trouvelot (1872-1882) qui font
// l'univers de connex, réduites et recadrées sur leur illustration, et sait les
// rééchantillonner à la taille du terminal.
//
//	aurore.jpg         Aurora Borealis, planche IV (1872)
//	nebuleuse.jpg      The Great Nebula in Orion, planche XV (1875-76)
//	meteores.jpg       The November Meteors, planche XII (1868)
//	protuberances.jpg  Solar Protuberances, planche II (1873)
//
// Les planches sont dans le domaine public. Elles sont lues dans le binaire
// (embed), jamais sur le disque.
package art

import (
	"bytes"
	"embed"
	"image"
	"image/jpeg"
	"sort"
	"sync"

	"github.com/Alexry375/connex/internal/ui/canvas"
)

//go:embed aurore.jpg nebuleuse.jpg meteores.jpg protuberances.jpg
var files embed.FS

// Plate désigne une planche.
type Plate int

// Les quatre planches.
const (
	Aurora Plate = iota
	Nebula
	Meteors
	Protuberances
	plates
)

var names = [plates]string{"aurore.jpg", "nebuleuse.jpg", "meteores.jpg", "protuberances.jpg"}

// Image est une planche décodée, en flottants.
type Image struct {
	W, H int
	Pix  []canvas.RGB
}

var (
	once  [plates]sync.Once
	cache [plates]*Image
)

// Get renvoie la planche p, décodée au premier appel. Si le décodage échouait
// (fichier embarqué abîmé), il renvoie une image grise d'un pixel : l'affichage
// reste utilisable.
func Get(p Plate) *Image {
	once[p].Do(func() {
		cache[p] = decode(names[p])
	})
	return cache[p]
}

func decode(name string) *Image {
	b, err := files.ReadFile(name)
	if err == nil {
		var img image.Image
		if img, err = jpeg.Decode(bytes.NewReader(b)); err == nil {
			return fromImage(img)
		}
	}
	return &Image{W: 1, H: 1, Pix: []canvas.RGB{{R: 0.1, G: 0.12, B: 0.12}}}
}

func fromImage(img image.Image) *Image {
	r := img.Bounds()
	out := &Image{W: r.Dx(), H: r.Dy(), Pix: make([]canvas.RGB, r.Dx()*r.Dy())}
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			cr, cg, cb, _ := img.At(r.Min.X+x, r.Min.Y+y).RGBA()
			out.Pix[y*out.W+x] = canvas.RGB{R: float32(cr) / 65535, G: float32(cg) / 65535, B: float32(cb) / 65535}
		}
	}
	return out
}

// Rect est une zone de la planche, en fractions de sa largeur et de sa hauteur.
type Rect struct{ X0, Y0, X1, Y1 float64 }

// Full est la planche entière.
var Full = Rect{0, 0, 1, 1}

// Resample réduit la zone r de la planche à w × h pixels, par moyenne de zone
// (les étoiles d'un pixel ne scintillent pas en crénelage).
func (im *Image) Resample(r Rect, w, h int) *canvas.Pixels {
	out := canvas.NewPixels(w, h)
	if w <= 0 || h <= 0 {
		return out
	}
	sx0, sy0 := r.X0*float64(im.W), r.Y0*float64(im.H)
	sw, sh := (r.X1-r.X0)*float64(im.W), (r.Y1-r.Y0)*float64(im.H)
	for y := 0; y < h; y++ {
		ya := int(sy0 + sh*float64(y)/float64(h))
		yb := int(sy0 + sh*float64(y+1)/float64(h))
		if yb <= ya {
			yb = ya + 1
		}
		for x := 0; x < w; x++ {
			xa := int(sx0 + sw*float64(x)/float64(w))
			xb := int(sx0 + sw*float64(x+1)/float64(w))
			if xb <= xa {
				xb = xa + 1
			}
			var acc canvas.RGB
			n := 0
			for yy := ya; yy < yb; yy++ {
				if yy < 0 || yy >= im.H {
					continue
				}
				for xx := xa; xx < xb; xx++ {
					if xx < 0 || xx >= im.W {
						continue
					}
					acc = acc.Plus(im.Pix[yy*im.W+xx])
					n++
				}
			}
			if n > 0 {
				out.P[y*w+x] = acc.Scale(1 / float32(n))
			}
		}
	}
	return out
}

// Blur adoucit p en place : moyenne sur une fenêtre de (2r+1)², en deux passes.
func Blur(p *canvas.Pixels, r int) {
	if r <= 0 || p.W == 0 || p.H == 0 {
		return
	}
	tmp := make([]canvas.RGB, len(p.P))
	pass := func(src, dst []canvas.RGB, w, h, dx, dy int) {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var acc canvas.RGB
				n := 0
				for k := -r; k <= r; k++ {
					xx, yy := x+k*dx, y+k*dy
					if xx < 0 || yy < 0 || xx >= w || yy >= h {
						continue
					}
					acc = acc.Plus(src[yy*w+xx])
					n++
				}
				dst[y*w+x] = acc.Scale(1 / float32(n))
			}
		}
	}
	pass(p.P, tmp, p.W, p.H, 1, 0)
	pass(tmp, p.P, p.W, p.H, 0, 1)
}

// Posterize réduit p, en place, à k encres tirées de l'image elle-même (k-moyennes
// sur les couleurs, départ aux quantiles de luminance), comme une gravure
// coloriée en aplats. Les aplats font des suites de cases identiques : le
// terminal les reçoit à moindres frais.
func Posterize(p *canvas.Pixels, k int) {
	n := len(p.P)
	if k < 2 || n < k {
		return
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return p.P[order[a]].Luma() < p.P[order[b]].Luma() })
	inks := make([]canvas.RGB, k)
	for i := range inks {
		inks[i] = p.P[order[(2*i+1)*n/(2*k)]]
	}
	owner := make([]int, n)
	for iter := 0; iter < 8; iter++ {
		sum := make([]canvas.RGB, k)
		cnt := make([]int, k)
		for i, c := range p.P {
			j := nearest(inks, c)
			owner[i] = j
			sum[j] = sum[j].Plus(c)
			cnt[j]++
		}
		for j := range inks {
			if cnt[j] > 0 {
				inks[j] = sum[j].Scale(1 / float32(cnt[j]))
			}
		}
	}
	for i := range p.P {
		p.P[i] = inks[owner[i]]
	}
}

func nearest(inks []canvas.RGB, c canvas.RGB) int {
	best, bd := 0, float32(1e9)
	for j, q := range inks {
		dr, dg, db := c.R-q.R, c.G-q.G, c.B-q.B
		if d := dr*dr + dg*dg + db*db; d < bd {
			best, bd = j, d
		}
	}
	return best
}

// Fit rogne le cadre r de l'image pour qu'il ait les proportions dst (largeur
// sur hauteur, en pixels carrés) sans rien déformer : il garde toute la largeur
// ou toute la hauteur de r, centré en largeur et calé à ay en hauteur (0 : en
// haut, 1 : en bas).
func (im *Image) Fit(r Rect, dst, ay float64) Rect {
	rw, rh := (r.X1-r.X0)*float64(im.W), (r.Y1-r.Y0)*float64(im.H)
	if rw <= 0 || rh <= 0 || dst <= 0 {
		return r
	}
	if src := rw / rh; dst >= src {
		h := (r.Y1 - r.Y0) * src / dst
		y0 := r.Y0 + (r.Y1-r.Y0-h)*ay
		return Rect{X0: r.X0, Y0: y0, X1: r.X1, Y1: y0 + h}
	}
	w := dst * rh / float64(im.W)
	x0 := r.X0 + (r.X1-r.X0-w)/2
	return Rect{X0: x0, Y0: r.Y0, X1: x0 + w, Y1: r.Y1}
}

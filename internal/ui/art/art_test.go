package art

import (
	"math"
	"testing"

	"github.com/Alexry375/connex/internal/ui/canvas"
)

// Les quatre planches se décodent, et ce qui est embarqué tient sous 3 Mo.
func TestPlatesDecodeWithinBudget(t *testing.T) {
	total := 0
	for p := Plate(0); p < plates; p++ {
		b, err := files.ReadFile(names[p])
		if err != nil {
			t.Fatal(err)
		}
		total += len(b)
		if im := Get(p); im.W < 100 || im.H < 100 {
			t.Errorf("%s : %d × %d", names[p], im.W, im.H)
		}
	}
	if total > 3<<20 {
		t.Fatalf("%d octets embarqués, plus de 3 Mo", total)
	}
}

// Fit recadre sans déformer : la zone rendue a les proportions demandées et
// reste dans la zone de départ.
func TestFitKeepsProportions(t *testing.T) {
	im := &Image{W: 320, H: 240}
	r := Rect{X0: 0.1, Y0: 0.1, X1: 0.9, Y1: 0.8}
	for _, dst := range []float64{0.5, 1, 1.33, 2, 4, 8} {
		f := im.Fit(r, dst, 0.7)
		w, h := (f.X1-f.X0)*float64(im.W), (f.Y1-f.Y0)*float64(im.H)
		if math.Abs(w/h-dst) > 1e-9 {
			t.Errorf("rapport %v : %v", dst, w/h)
		}
		if f.X0 < r.X0-1e-12 || f.Y0 < r.Y0-1e-12 || f.X1 > r.X1+1e-12 || f.Y1 > r.Y1+1e-12 {
			t.Errorf("rapport %v : %+v sort de %+v", dst, f, r)
		}
	}
}

// Posterize ne garde que k encres.
func TestPosterizeKeepsKInks(t *testing.T) {
	im := Get(Aurora)
	p := im.Resample(Full, 80, 48)
	Posterize(p, 12)
	seen := map[canvas.RGB]bool{}
	for _, c := range p.P {
		seen[c] = true
	}
	if len(seen) > 12 || len(seen) < 6 {
		t.Fatalf("%d encres après Posterize(12)", len(seen))
	}
}

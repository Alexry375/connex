package ui

import (
	"strconv"
	"time"

	"github.com/Alexry375/connex/internal/ui/art"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// La planche IV : la légende. Les quatre planches d'où vient tout ce qui est
// dessiné, ce que veut dire chaque signe, et les touches.

type legendPlate struct {
	p       art.Plate
	r       art.Rect
	name    string
	meaning string
}

var legendPlates = []legendPlate{
	{art.Aurora, art.Rect{X0: 0.05, Y0: 0.1, X1: 0.95, Y1: 0.95}, "I. Aurore", "un port ouvert à tous"},
	{art.Nebula, art.Rect{X0: 0.2, Y0: 0.15, X1: 0.9, Y1: 0.8}, "II. Nébuleuse", "le détail d’un objet"},
	{art.Meteors, art.Rect{X0: 0.12, Y0: 0.36, X1: 0.95, Y1: 0.72}, "III. Météores", "changements et agents"},
	{art.Protuberances, art.Rect{X0: 0.05, Y0: 0.12, X1: 0.95, Y1: 0.62}, "IV. Protubérances", "échecs et refus"},
}

// Les signes, tels qu'ils paraissent dans le ciel et le catalogue.
var legendSigns = []struct {
	sign rune
	fg   canvas.Color
	text string
}{
	{'✦', gold, "un agent t’attend"},
	{'☄', ink, "agent au travail (comète)"},
	{'✧', mint, "une appli en marche"},
	{'○', inkDim, "une appli arrêtée"},
	{'✕', red, "une appli en échec"},
	{'•', inkDim, "un de tes ports (étoile)"},
	{'◇', inkFaint, "le port d’un autre compte"},
	{'◆', goldDim, "un dépôt (or : modifié)"},
}

var legendKeys = [][2]string{
	{"a-z", "chercher"},
	{"↑↓", "choisir"},
	{"←→", "machine"},
	{"Entrée", "détails"},
	{"o", "ouvrir"},
	{"Échap", "revenir"},
	{"Tab", "journal"},
	{"d", "démarrer"},
	{"a", "arrêter"},
	{"r", "relancer"},
	{"l", "libérer"},
	{"F5", "actualiser"},
	{"q", "quitter"},
	{"Q", "taper un q"},
}

// legendKeyW : la colonne des touches, la plus longue et un blanc.
func legendKeyW() int {
	w := 0
	for _, k := range legendKeys {
		w = max(w, canvas.StringWidth(k[0]))
	}
	return w + 1
}

// legendKeysNote dit où servent les lettres.
const legendKeysNote = "Sur la planche I, taper cherche. Sur celle d’une appli, o l’ouvre dans le navigateur ; sur celle d’un objet, d a r l agissent, et Entrée confirme."

// legendWords explique les mots de l'écran qui ne sont pas des signes : l'état
// d'un agent qui attend, et les trois mots d'un dépôt.
const legendWords = "t’attend : l’agent a fini ou pose une question. Un dépôt modifié a des fichiers changés ; à pousser, des commits pas encore envoyés ; en retard, des commits distants pas encore pris, d’après le dernier fetch."

// legendColumns choisit autant de colonnes de signes et de touches que la
// largeur en permet sans couper un seul texte.
func legendColumns(W int) (cols, kcols int) {
	longest, klongest := 0, 0
	for _, s := range legendSigns {
		longest = max(longest, canvas.StringWidth(s.text))
	}
	for _, k := range legendKeys {
		klongest = max(klongest, canvas.StringWidth(k[1]))
	}
	cols = 4
	for cols > 1 && (W-4)/cols-3 < longest {
		cols--
	}
	kcols = 4
	for kcols > 1 && (W-4)/kcols-legendKeyW()-1 < klongest {
		kcols--
	}
	return cols, kcols
}

func (a *App) drawLegend(cv *canvas.Canvas, now time.Time) {
	W, H := a.W, a.H
	cv.Clear(landBG)
	paperRows := a.paperRows()
	bottom := H - paperRows

	// Ce qui suit les vignettes, pour leur donner toute la hauteur qui reste.
	sky := "Dans le ciel, chaque étoile est un port : de gauche à droite, du port 20 au port 65535 ; plus elle monte, plus il est exposé. Rien n’y brille pour rien."
	skyLines := wrap(sky, W-4)
	cols, kcols := legendColumns(W)
	rowsSigns := (len(legendSigns) + cols - 1) / cols
	rowsKeys := (len(legendKeys) + kcols - 1) / kcols
	noteLines := wrap(legendKeysNote, W-4)
	wordLines := wrap(legendWords, W-4)
	// Quand la hauteur manque, on renonce d'abord à la phrase sur le ciel si
	// cela garde les vignettes, puis aux vignettes, puis à la phrase : les
	// signes, les mots et les touches restent.
	rest := rowsSigns + len(wordLines) + 1 + rowsKeys + len(noteLines)
	switch {
	case bottom-rest-len(skyLines)-1-4 >= 3: // tout tient
	case bottom-rest-4 >= 3:
		skyLines = nil
	case bottom-rest < len(skyLines)+1:
		skyLines = nil
	}
	if len(skyLines) > 0 {
		rest += len(skyLines) + 1
	}
	// Une rangée vide avant la note, si les vignettes gardent de quoi se lire.
	noteGap := 0
	if bottom-rest-4 > 8 {
		noteGap = 1
		rest++
	}
	vh := min(18, bottom-rest-4)
	y := max(0, (bottom-rest)/6)
	if vh >= 3 {
		y = max(0, (bottom-rest-4-vh)/2)
		a.drawLegendPlates(cv, y, vh)
		y += vh + 4
	}

	// Le ciel, en une phrase.
	for _, line := range skyLines {
		if y >= bottom-1 {
			break
		}
		cv.Text(2, y, W-2, line, inkDim, canvas.Italic)
		y++
	}
	if len(skyLines) > 0 {
		y++
	}

	// Les signes, puis les touches, en autant de colonnes que la largeur permet.
	colW := (W - 4) / cols
	for i, s := range legendSigns {
		cx, cy := 2+(i/rowsSigns)*colW, y+i%rowsSigns
		if cy >= bottom-1 {
			continue
		}
		cv.Text(cx, cy, cx+colW, string(s.sign), s.fg, canvas.Bold)
		cv.Text(cx+2, cy, cx+colW-1, s.text, ink, 0)
	}
	y += rowsSigns
	for _, line := range wordLines {
		if y >= bottom-1 {
			break
		}
		cv.Text(2, y, W-2, line, inkDim, canvas.Italic)
		y++
	}
	y++
	kw := (W - 4) / kcols
	for i, k := range legendKeys {
		cx, cy := 2+(i/rowsKeys)*kw, y+i%rowsKeys
		if cy >= bottom {
			continue
		}
		cv.Text(cx, cy, cx+legendKeyW(), k[0], gold, canvas.Bold)
		cv.Text(cx+legendKeyW(), cy, cx+kw-1, k[1], ink, 0)
	}
	y += rowsKeys + noteGap
	for _, line := range noteLines {
		if y >= bottom {
			break
		}
		cv.Text(2, y, W-2, line, inkDim, canvas.Italic)
		y++
	}
	a.drawPaper(cv, bottom, paperRows, 4, now)
}

// drawLegendPlates pose les quatre planches côte à côte, à la rangée y0, sur
// vh rangées, et ce que chacune veut dire ici.
func (a *App) drawLegendPlates(cv *canvas.Canvas, y0, vh int) {
	W := a.W
	gap := 2
	vw := (W - 2 - gap*3) / 4
	for i, lp := range legendPlates {
		x := 1 + i*(vw+gap)
		img := a.gfx.get("legend:"+strconv.Itoa(i)+":"+strconv.Itoa(vw)+"x"+strconv.Itoa(vh), func() *canvas.Pixels {
			im := art.Get(lp.p)
			p := im.Resample(im.Fit(lp.r, float64(vw)/float64(2*vh), 0.5), vw, vh*2)
			// Assez grandes, les traînées des météores se perdent dans la
			// moyenne : on leur rend leur éclat, et la nuit reste la nuit.
			// Plus petites, l'étalement ne ferait que du bruit clair.
			if lp.p == art.Meteors && vh >= 8 {
				stretch(p)
				gain(p, 0.7)
			}
			art.Posterize(p, plateInks)
			return p
		})
		cv.Blit(img, x, y0)
		cv.Text(x, y0+vh, x+vw, lp.name, ink, canvas.Bold)
		for j, line := range wrap(lp.meaning, vw) {
			if j < 2 {
				cv.Text(x, y0+vh+1+j, x+vw, line, inkDim, canvas.Italic)
			}
		}
	}
}

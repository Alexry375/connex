package ui

import (
	"strconv"
	"strings"

	"github.com/Alexry375/connex/internal/ui/canvas"
)

func itoa(n int) string { return strconv.Itoa(n) }

// wrap coupe s en lignes de w colonnes au plus, entre les mots si possible, et
// dans un mot trop long pour une ligne après une barre oblique, sinon après un
// trait d'union ou un souligné.
func wrap(s string, w int) []string {
	if w < 1 {
		return nil
	}
	var out []string
	var line strings.Builder
	lw := 0
	flush := func() {
		out = append(out, strings.TrimRight(line.String(), " "))
		line.Reset()
		lw = 0
	}
	for _, word := range strings.Split(s, " ") {
		ww := canvas.StringWidth(word)
		if lw > 0 && lw+1+ww > w {
			flush()
		}
		if lw > 0 {
			line.WriteByte(' ')
			lw++
		}
		// Un mot plus long que la ligne : on le coupe après un « / », sinon après
		// un « - » ou un « _ », sinon net.
		for ww > w-lw {
			cut := breakAt(word, w-lw)
			if cut == "" && lw > 0 {
				flush()
				continue
			}
			if cut == "" {
				cut = cutWidth(word, w)
			}
			line.WriteString(cut)
			word = word[len(cut):]
			ww = canvas.StringWidth(word)
			flush()
		}
		line.WriteString(word)
		lw += ww
	}
	if lw > 0 || len(out) == 0 {
		flush()
	}
	return out
}

// wrapRows coupe s comme wrap, en n lignes au plus : s'il n'y tient pas, la
// dernière finit par « … ».
func wrapRows(s string, w, n int) []string {
	if n <= 0 {
		return nil
	}
	lines := wrap(s, w)
	if len(lines) > n {
		lines = lines[:n]
		// « … » après le dernier mot gardé s'il reste la place, sinon à la
		// place de sa fin.
		lines[n-1] = canvas.Truncate(lines[n-1]+" …", w)
	}
	return lines
}

// breakAt renvoie le plus long début de s qui tient en w colonnes et finit
// par « / » ; à défaut, par « - » ou « _ » (dans un nom de dossier : «
// …-de-supervision- » puis « multi-machines ») ; sinon "".
func breakAt(s string, w int) string {
	slash, dash := "", ""
	cw := 0
	for i, r := range s {
		cw += canvas.RuneWidth(r)
		if cw > w {
			break
		}
		switch r {
		case '/':
			slash = s[:i+1]
		case '-', '_':
			if i > 0 {
				dash = s[:i+1]
			}
		}
	}
	if slash != "" {
		return slash
	}
	return dash
}

// cutWidth renvoie le plus long début de s qui tient en w colonnes (au moins
// un caractère).
func cutWidth(s string, w int) string {
	cw := 0
	for i, r := range s {
		cw += canvas.RuneWidth(r)
		if cw > w && i > 0 {
			return s[:i]
		}
	}
	return s
}

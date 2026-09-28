package ui

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/canvas"
)

// Les mots de l'interface : états, durées, chemins. Tout ce qui s'affiche
// passe par ici, pour que le ton reste le même d'un écran à l'autre.

// agentWord dit l'état d'une session d'agent.
func agentWord(state string) string {
	switch state {
	case model.StateWaiting:
		return "t'attend"
	case model.StateWorking:
		return "travaille"
	case model.StateIdle:
		return "au repos"
	}
	return "état inconnu"
}

// appWord dit l'état d'une appli inscrite.
func appWord(state string) string {
	switch state {
	case model.AppRunning:
		return "en marche"
	case model.AppStarting:
		return "démarre"
	case model.AppStopped:
		return "arrêtée"
	case model.AppFailed:
		return "en échec"
	}
	return "état inconnu"
}

// dirtyWord dit l'état d'un dépôt.
func dirtyWord(d *bool) string {
	switch {
	case d == nil:
		return "état inconnu"
	case *d:
		return "modifié"
	}
	return "propre"
}

// exposureWord dit qui peut joindre un port.
func exposureWord(e string) string {
	switch e {
	case "loopback":
		return "cette machine"
	case "tailnet":
		return "tailnet"
	case "all":
		return "ouvert à tous"
	}
	return e
}

// exposureLong explique une exposition en une phrase.
func exposureLong(e string) string {
	switch e {
	case "loopback":
		return "joignable depuis cette machine seulement"
	case "tailnet":
		return "joignable depuis les machines du réseau Tailscale"
	case "all":
		return "écoute sur toutes les interfaces : joignable de partout où la machine l'est, Internet compris si elle y est exposée"
	}
	return "exposition " + e
}

// opWord est l'action à l'infinitif, pour une question ou un compte rendu.
func opWord(op string) string {
	switch op {
	case model.ReqStart:
		return "démarrer"
	case model.ReqStop:
		return "arrêter"
	case model.ReqRestart:
		return "relancer"
	case model.ReqFreePort:
		return "libérer le port"
	case model.ReqRefresh:
		return "rafraîchir"
	}
	return op
}

// opProgress est l'action en cours.
func opProgress(op string) string {
	switch op {
	case model.ReqStart:
		return "démarrage"
	case model.ReqStop:
		return "arrêt"
	case model.ReqRestart:
		return "relance"
	case model.ReqFreePort:
		return "libération"
	case model.ReqRefresh:
		return "relevé"
	}
	return op
}

// ago dit depuis combien de temps, en français : « il y a 12 s ».
func ago(d time.Duration) string {
	if d < time.Second {
		return "à l'instant"
	}
	return "il y a " + span(d)
}

// when date un changement vu par connex : « il y a 12 s », ou « au retour »
// pour ce qu'une machine rapporte en revenant d'une coupure.
func when(at, now time.Time, back bool) string {
	if back {
		return "au retour"
	}
	return ago(now.Sub(at))
}

// since dit depuis quand dure un état : « depuis 12 s », ou « à l'instant »
// s'il vient de commencer.
func since(d time.Duration) string {
	if d < time.Second {
		return "à l'instant"
	}
	return "depuis " + span(d)
}

// span écrit une durée courte : « 12 s », « 3 min », « 1 min 12 s », « 2 h 05 ».
func span(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	switch {
	case s < 60:
		return strconv.Itoa(s) + " s"
	case s < 600:
		if s%60 == 0 {
			return strconv.Itoa(s/60) + " min"
		}
		return fmt.Sprintf("%d min %02d s", s/60, s%60)
	case s < 3600:
		return strconv.Itoa(s/60) + " min"
	case s < 86400:
		return fmt.Sprintf("%d h %02d", s/3600, s%3600/60)
	}
	return strconv.Itoa(s/86400) + " j"
}

// seconds écrit une durée avec un chiffre après la virgule : « 0,4 s ».
func seconds(d time.Duration) string {
	return strings.Replace(fmt.Sprintf("%.1f s", d.Seconds()), ".", ",", 1)
}

// plural accorde un nom : plural(2, "agent", "agents") → « 2 agents ».
func plural(n int, one, many string) string {
	if n < 2 {
		return strconv.Itoa(n) + " " + one // en français : 0 appli, 1 appli, 2 applis
	}
	return strconv.Itoa(n) + " " + many
}

// homeOf devine le dossier personnel d'après le compte du relevé, pour écrire
// les chemins en ~/… ; ce n'est qu'une façon de raccourcir l'affichage.
func homeOf(user string) string {
	switch user {
	case "":
		return ""
	case "root":
		return "/root"
	}
	return "/home/" + user
}

// shortPath écrit p en ~/… s'il est sous home.
func shortPath(p, home string) string {
	if home == "" || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+"/"); ok {
		return "~/" + rest
	}
	return p
}

// fitPath fait tenir un chemin en w colonnes en gardant sa fin, la partie la
// plus parlante : « ~/…/Projets/site ».
func fitPath(p string, w int) string {
	if canvas.Width(p) <= w || w <= 0 {
		if w <= 0 {
			return ""
		}
		return p
	}
	head := ""
	body := p
	if strings.HasPrefix(p, "~/") {
		head, body = "~/", p[2:]
	} else if strings.HasPrefix(p, "/") {
		head, body = "/", p[1:]
	}
	parts := strings.Split(body, "/")
	// garde le plus de segments de fin possible derrière « head…/ »
	for i := 1; i < len(parts); i++ {
		cand := head + "…/" + strings.Join(parts[i:], "/")
		if canvas.Width(cand) <= w {
			return cand
		}
	}
	last := parts[len(parts)-1]
	if canvas.Width(head+"…/"+last) <= w {
		return head + "…/" + last
	}
	return truncMiddle(last, w)
}

// truncMiddle coupe le milieu de s : « analyse-com…ines ».
func truncMiddle(s string, w int) string {
	if canvas.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	left := (w - 1) / 2
	return headCols(s, left) + canvas.TruncateLeft(s, w-left)
}

// headCols renvoie le plus long début de s qui tient en w colonnes.
func headCols(s string, w int) string {
	col := 0
	for i, r := range s {
		col += canvas.RuneWidth(r)
		if col > w {
			return s[:i]
		}
	}
	return s
}

// generic sont des noms de dossier qui ne disent rien du projet.
var generic = map[string]bool{"repo": true, "app": true, "apps": true, "web": true, "src": true,
	"frontend": true, "backend": true, "server": true, "client": true, "atelier": true, "api": true}

// projectName donne le nom parlant d'un dossier de projet : son dernier
// segment, ou les deux derniers si le dernier est générique (« repo »).
func projectName(p string) string {
	if p == "" {
		return ""
	}
	p = strings.TrimRight(p, "/")
	base := path.Base(p)
	if generic[strings.ToLower(base)] {
		parent := path.Base(path.Dir(p))
		if parent != "/" && parent != "." && parent != "" {
			return parent + "/" + base
		}
	}
	return base
}

// placeName nomme un dossier pour une ligne : le projet, ou « ~ » pour le
// dossier personnel, qui n'en est pas un.
func placeName(dir, home string) string {
	if dir != "" && dir == home {
		return "~"
	}
	return projectName(dir)
}

// fold met en minuscules et retire les accents, pour chercher sans se soucier
// de la casse ni des accents.
func fold(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'à', 'â', 'ä', 'á', 'ã':
			r = 'a'
		case 'é', 'è', 'ê', 'ë':
			r = 'e'
		case 'î', 'ï', 'í':
			r = 'i'
		case 'ô', 'ö', 'ó':
			r = 'o'
		case 'ù', 'û', 'ü', 'ú':
			r = 'u'
		case 'ç':
			r = 'c'
		case 'ÿ':
			r = 'y'
		case 'œ':
			sb.WriteString("oe")
			continue
		case '’':
			r = '\''
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

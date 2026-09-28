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

// Les textes de l'interface : français, accents compris, typographie des
// planches (« 21 h 42 », virgule décimale, apostrophe courbe).

var monthsFR = [...]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet",
	"août", "septembre", "octobre", "novembre", "décembre"}

// dateFR écrit « 27 septembre 2026 ».
func dateFR(t time.Time) string {
	return fmt.Sprintf("%d %s %d", t.Day(), monthsFR[t.Month()-1], t.Year())
}

// clockFR écrit « 21 h 42 ».
func clockFR(t time.Time) string { return fmt.Sprintf("%d h %02d", t.Hour(), t.Minute()) }

// clockSecFR écrit « 21 h 42 min 07 s ».
func clockSecFR(t time.Time) string {
	return fmt.Sprintf("%d h %02d min %02d s", t.Hour(), t.Minute(), t.Second())
}

// durFR écrit une durée : « 12 s », « 1 min 05 s », « 14 min », « 2 h 05 », « 3 j ».
// Des espaces insécables la tiennent d'un seul bloc : un texte replié ne
// coupe ni « 12 | s » ni « 1 min | 05 s ».
func durFR(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%d\u00a0s", s)
	case s < 600:
		return fmt.Sprintf("%d\u00a0min\u00a0%02d\u00a0s", s/60, s%60)
	case s < 3600:
		return fmt.Sprintf("%d\u00a0min", s/60)
	case s < 48*3600:
		return fmt.Sprintf("%d\u00a0h\u00a0%02d", s/3600, s%3600/60)
	}
	return fmt.Sprintf("%d\u00a0j", s/86400)
}

// agoFR écrit « à l’instant », « il y a 12 s », « il y a 3 min », d'un seul
// bloc lui aussi : un texte replié ne laisse pas « à » seul en fin de ligne.
func agoFR(d time.Duration) string {
	if d < 2*time.Second {
		return "à\u00a0l’instant"
	}
	return "il\u00a0y\u00a0a\u00a0" + durFR(d)
}

// secondsFR écrit une durée courte avec une décimale : « 1,4 s ».
func secondsFR(d time.Duration) string {
	return strings.Replace(strconv.FormatFloat(d.Seconds(), 'f', 1, 64), ".", ",", 1) + "\u00a0s"
}

// plural écrit « 0 agent », « 1 agent », « 3 agents » : en français, le
// pluriel commence à deux.
func plural(n int, one, many string) string {
	if n < 2 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + many
}

// homeOf devine le dossier personnel d'un compte pour raccourcir les chemins en
// « ~ ». Le chemin complet reste affiché sur la planche de l'objet.
func homeOf(user string) string {
	switch user {
	case "":
		return ""
	case "root":
		return "/root"
	}
	return "/home/" + user
}

// shortPath écrit p en « ~/… » s'il est sous home.
func shortPath(p, home string) string {
	if home == "" {
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

// fitPath fait tenir un chemin en w colonnes en gardant son début et son dernier
// dossier, le plus parlant : « ~/Global/…/ml-notebooks », puis en coupant dans le
// dernier dossier s'il le faut.
func fitPath(p string, w int) string {
	if canvas.StringWidth(p) <= w {
		return p
	}
	if w <= 1 {
		return canvas.Truncate(p, w)
	}
	segs := strings.Split(p, "/")
	last := segs[len(segs)-1]
	// On retire des dossiers au milieu tant que ça ne tient pas.
	for keep := len(segs) - 2; keep >= 1; keep-- {
		cand := strings.Join(segs[:keep], "/") + "/…/" + last
		if canvas.StringWidth(cand) <= w {
			return cand
		}
	}
	// Le dernier dossier seul est trop long : on le coupe au milieu.
	head := "…/"
	room := w - canvas.StringWidth(head)
	if room < 5 {
		return canvas.TruncateLeft(last, w)
	}
	return head + middleCut(last, room)
}

// middleCut coupe s au milieu pour qu'il tienne en w colonnes.
func middleCut(s string, w int) string {
	if canvas.StringWidth(s) <= w {
		return s
	}
	rs := []rune(s)
	left := (w - 1) / 2
	right := w - 1 - left
	if left+right >= len(rs) {
		return canvas.Truncate(s, w)
	}
	return string(rs[:left]) + "…" + string(rs[len(rs)-right:])
}

// baseName renvoie le dernier élément d'un chemin, ou « ? ».
func baseName(p string) string {
	if p == "" {
		return "?"
	}
	return path.Base(p)
}

// fold met en minuscules et retire les accents, pour chercher sans y penser.
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if f, ok := foldMap[r]; ok {
			b.WriteRune(f)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

var foldMap = map[rune]rune{
	'à': 'a', 'â': 'a', 'ä': 'a', 'á': 'a', 'ã': 'a', 'å': 'a',
	'ç': 'c', 'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'î': 'i', 'ï': 'i', 'í': 'i', 'ì': 'i',
	'ô': 'o', 'ö': 'o', 'ó': 'o', 'ò': 'o', 'õ': 'o',
	'û': 'u', 'ü': 'u', 'ú': 'u', 'ù': 'u', 'ÿ': 'y', 'ñ': 'n',
	'’': '\'', 'œ': 'o', 'æ': 'a',
}

// errorSourceFR nomme en français la partie du relevé qui a échoué.
func errorSourceFR(s string) string {
	switch s {
	case "repos":
		return "relevé des dépôts"
	case "listeners":
		return "relevé des ports"
	case "agents":
		return "relevé des agents"
	case "apps":
		return "relevé des applis"
	case "machine":
		return "relevé de la machine"
	case "config":
		return "configuration de connex"
	}
	return "relevé " + s
}

// errorParts sépare d'un message d'erreur de relevé le chemin qu'il nomme :
// « /a/b : fatal… » et « permission refusée : /a/b » donnent « /a/b » et le
// reste. Sans chemin, tout le message est la raison.
func errorParts(msg string) (path, reason string) {
	parts := strings.Split(msg, " : ")
	if len(parts) < 2 {
		return "", msg
	}
	if strings.HasPrefix(parts[0], "/") {
		return parts[0], strings.Join(parts[1:], " : ")
	}
	if last := parts[len(parts)-1]; strings.HasPrefix(last, "/") {
		return last, strings.Join(parts[:len(parts)-1], " : ")
	}
	return "", msg
}

// États en français.

func agentStateFR(s string) string {
	switch s {
	case model.StateWorking:
		return "travaille"
	case model.StateWaiting:
		return "t’attend"
	case model.StateIdle:
		return "au repos"
	}
	return "état inconnu"
}

func appStateFR(s string) string {
	switch s {
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

// exposureFR : ce que dit l'exposition, en court.
func exposureFR(e string) string {
	switch e {
	case model.ExposureAll:
		return "ouvert à tous"
	case model.ExposureTailnet:
		return "Tailscale"
	case model.ExposureHost:
		return "cette machine seulement"
	case model.ExposureLoopback:
		return "local"
	}
	return e
}

// listenerExposureFR : l'exposition d'une écoute, en court ; une écoute
// publiée par Tailscale Funnel le dit.
func listenerExposureFR(l *model.Listener) string {
	if l.Funnel {
		return "Internet (Funnel)"
	}
	return exposureFR(l.Exposure)
}

// exposureLongFR : la même chose, en phrase, pour la planche d'un port.
func exposureLongFR(l *model.Listener) string {
	if l.Funnel {
		return "Internet, par Tailscale Funnel : ce port est publié pour n’importe qui"
	}
	switch l.Exposure {
	case model.ExposureAll:
		return "toutes les interfaces : joignable de l’extérieur, Internet compris si la machine y est exposée"
	case model.ExposureTailnet:
		return "réseau Tailscale : joignable depuis tes machines seulement"
	case model.ExposureHost:
		return "interface virtuelle (pont Docker…) : joignable depuis cette machine et ses conteneurs seulement"
	case model.ExposureLoopback:
		return "boucle locale : joignable depuis cette machine seulement"
	}
	return l.Exposure
}

func viewFR(v string) string {
	switch v {
	case model.ViewRemote:
		return "à distance, par Tailscale"
	case model.ViewTrain:
		return "mode train, par le miroir apercu-vps"
	}
	return v
}

// syncFR dit ce qu'un dépôt a à pousser et de retard, quand ce n'est pas nul :
// « 2 à pousser », « 1 en retard ». Le retard date du dernier fetch.
func syncFR(r *model.Repo) (ahead, behind string) {
	if r.Ahead != nil && *r.Ahead > 0 {
		ahead = strconv.Itoa(*r.Ahead) + "\u00a0à pousser"
	}
	if r.Behind != nil && *r.Behind > 0 {
		behind = strconv.Itoa(*r.Behind) + "\u00a0en retard"
	}
	return ahead, behind
}

func dirtyFR(d *bool) string {
	switch {
	case d == nil:
		return "état inconnu"
	case *d:
		return "modifié"
	}
	return "propre"
}

func branchFR(branch string, detached bool) string {
	switch {
	case detached:
		return "HEAD détachée"
	case branch == "":
		return "branche inconnue"
	}
	return branch
}

// opFR : le nom d'une action, à l'infinitif.
func opFR(op string) string {
	switch op {
	case model.ReqStart:
		return "démarrer"
	case model.ReqStop:
		return "arrêter"
	case model.ReqRestart:
		return "relancer"
	case model.ReqFreePort:
		return "libérer"
	case model.ReqRefresh:
		return "actualiser"
	}
	return op
}

// opDoneFR : l'action réussie, au participe.
func opDoneFR(op string) string {
	switch op {
	case model.ReqStart:
		return "démarrée"
	case model.ReqStop:
		return "arrêtée"
	case model.ReqRestart:
		return "relancée"
	case model.ReqFreePort:
		return "port libéré"
	case model.ReqRefresh:
		return "relevé à jour"
	}
	return op
}

// opPendingFR : l'action en cours.
func opPendingFR(op string) string {
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

// roman écrit les numéros de planche.
func roman(n int) string {
	return [...]string{"", "I", "II", "III", "IV", "V", "VI"}[max(0, min(n, 6))]
}

// httpFR décrit la dernière sonde HTTP d'une appli.
func httpFR(code int) string {
	if code == 0 {
		return "pas de réponse HTTP"
	}
	return "HTTP " + strconv.Itoa(code)
}

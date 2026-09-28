// Package address choisit l'adresse où ouvrir une appli depuis le laptop (lot
// L5, D13). C'est une fonction pure : le relevé de la machine de l'appli, et
// pour le mode train le contenu du fichier du miroir et les ports du laptop,
// sont fournis par l'appelant. Rien n'est lu ni lancé ici, pour que
// l'affichage puisse l'appeler tel quel.
//
// L'adresse rendue est toujours en http, sur une adresse IP : 127.0.0.1 pour
// le laptop et le miroir, l'adresse Tailscale (100.64.0.0/10) pour une autre
// machine. Jamais un nom d'hôte.
package address

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/Alexry375/connex/internal/model"
)

// Input est ce qu'il faut pour choisir l'adresse d'une appli.
type Input struct {
	App      string         // nom de l'appli
	Machine  string         // nom de la machine M, pour les messages
	Local    bool           // M est le laptop
	Snapshot model.Snapshot // relevé frais de M, avec ses applis

	// Pour une appli en mode train seulement.
	Mirror          string           // contenu de ~/.local/state/miroir/<nom>.url ; vide s'il manque
	LaptopListeners []model.Listener // ports en écoute du laptop
}

// Refusal est un refus d'ouvrir, avec le message pour l'utilisateur.
type Refusal struct{ Msg string }

func (r *Refusal) Error() string { return r.Msg }

func refuse(format string, a ...any) (string, error) {
	return "", &Refusal{Msg: fmt.Sprintf(format, a...)}
}

var tailnet = netip.MustParsePrefix("100.64.0.0/10")

// mirrorRe est la seule forme acceptée du fichier du miroir.
var mirrorRe = regexp.MustCompile(`^http://127\.0\.0\.1:([1-9][0-9]{0,4})/?$`)

// For rend l'adresse où ouvrir l'appli in.App de la machine in.Machine, ou un
// *Refusal.
func For(in Input) (string, error) {
	var app *model.App
	for i := range in.Snapshot.Apps {
		if in.Snapshot.Apps[i].Name == in.App {
			app = &in.Snapshot.Apps[i]
			break
		}
	}
	if app == nil {
		return refuse("aucune appli « %s » sur %s", in.App, in.Machine)
	}
	if app.View == model.ViewTrain {
		return mirror(in)
	}
	if app.State != model.AppRunning || !app.Health.PortListening || app.Port == 0 {
		return refuse("« %s » ne tourne pas sur %s (état : %s)", in.App, in.Machine, app.State)
	}
	var loopback, host, tailscale, all bool
	for _, l := range in.Snapshot.Listeners {
		if !l.Mine || l.Port != app.Port {
			continue
		}
		switch {
		case l.Funnel:
			// Écoute sur l'adresse Tailscale, publiée sur Internet par Funnel :
			// depuis le laptop, on la joint par cette adresse.
			tailscale = true
		case l.Exposure == model.ExposureLoopback:
			loopback = true
		case l.Exposure == model.ExposureHost:
			host = true
		case l.Exposure == model.ExposureTailnet:
			tailscale = true
		case l.Exposure == model.ExposureAll:
			all = true
		}
	}
	if !loopback && !host && !tailscale && !all {
		return refuse("« %s » ne tourne pas sur %s (état : %s)", in.App, in.Machine, app.State)
	}
	if in.Local && (loopback || all) {
		return fmt.Sprintf("http://127.0.0.1:%d/", app.Port), nil
	}
	if !tailscale && !all && !loopback {
		// Seulement sur une interface virtuelle (pont Docker…) : 127.0.0.1 ne
		// la joint pas, et l'adresse Tailscale non plus.
		return refuse("« %s » n'écoute que sur une interface virtuelle de %s (pont Docker ou autre) : "+
			"seule cette machine et ses conteneurs la joignent. Fais-la écouter sur 127.0.0.1 ou sur son adresse Tailscale.", in.App, in.Machine)
	}
	if !in.Local && !tailscale && !all {
		return refuse("« %s » n'écoute que sur la boucle locale de %s : injoignable depuis ce laptop. "+
			"Fais-la écouter sur son adresse Tailscale, ou passe-la en mode train.", in.App, in.Machine)
	}
	ip, err := netip.ParseAddr(in.Snapshot.Machine.TailscaleIP)
	if err != nil || !ip.Is4() || !tailnet.Contains(ip) {
		return refuse("adresse Tailscale inconnue pour %s", in.Machine)
	}
	return fmt.Sprintf("http://%s:%d/", ip, app.Port), nil
}

// mirror rend l'adresse du miroir apercu-vps, si le fichier a la forme permise
// et que son port est en écoute sur le laptop.
func mirror(in Input) (string, error) {
	port := MirrorPort(in.Mirror)
	if port == 0 || !loopbackReachable(in.LaptopListeners, port) {
		return refuse("mode train arrêté pour « %s » : lance connex train %s", in.App, in.App)
	}
	return fmt.Sprintf("http://127.0.0.1:%d/", port), nil
}

// MirrorPort lit le port du fichier du miroir : exactement
// http://127.0.0.1:<port>/ ou http://127.0.0.1:<port>, espaces et fin de ligne
// retirés. 0 si le contenu n'a pas cette forme.
func MirrorPort(content string) int {
	m := mirrorRe.FindStringSubmatch(strings.TrimSpace(content))
	if m == nil {
		return 0
	}
	p, err := strconv.Atoi(m[1])
	if err != nil || p > 65535 {
		return 0
	}
	return p
}

// loopbackReachable dit si un port en écoute répond sur 127.0.0.1 : écoute sur
// 127.0.0.1 même, ou sur toutes les adresses (0.0.0.0, ::).
func loopbackReachable(ls []model.Listener, port int) bool {
	for _, l := range ls {
		if l.Port != port {
			continue
		}
		a, err := netip.ParseAddr(l.Addr)
		if err != nil {
			continue
		}
		a = a.Unmap()
		if a == netip.AddrFrom4([4]byte{127, 0, 0, 1}) || a.IsUnspecified() {
			return true
		}
	}
	return false
}

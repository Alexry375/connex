package main

// La touche « ouvrir » de l'affichage (D13). L'affichage ne lance rien : il
// appelle la fonction rendue par uiOpener avec les relevés qu'il tient déjà.
// Elle choisit l'adresse par internal/address, comme `connex open`, sans
// nouveau relevé ni session ssh, puis lance le navigateur sans l'attendre.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/Alexry375/connex/internal/address"
	"github.com/Alexry375/connex/internal/config"
	"github.com/Alexry375/connex/internal/model"
)

// uiOpener renvoie la fonction « ouvrir » de l'affichage pour la configuration
// cfg. Elle reçoit la machine et l'appli choisies et les derniers relevés de
// chaque machine, par nom ; elle rend l'adresse ouverte, ou la raison du refus.
// Elle ne bloque pas : le navigateur est lancé, pas attendu.
func uiOpener(cfg config.Config) func(machine, app string, snaps map[string]model.Snapshot) (string, error) {
	local := cfg.Name
	if local == "" {
		local, _ = os.Hostname()
	}
	// Sans machine distante, l'affichage n'a qu'une machine : la locale, sous
	// le nom que lui donne son relevé.
	alone := len(cfg.Machines) == 0
	return func(machine, app string, snaps map[string]model.Snapshot) (string, error) {
		if !config.ValidName(app) {
			return "", fmt.Errorf("nom d'appli invalide : %q", app)
		}
		snap, ok := snaps[machine]
		if !ok {
			return "", fmt.Errorf("pas encore de relevé de %s", machine)
		}
		isLocal := alone || machine == local
		in := address.Input{App: app, Machine: machine, Local: isLocal, Snapshot: snap}
		if a := findApp(snap, app); a != nil && a.View == model.ViewTrain {
			mirror, err := readMirror(app)
			if err != nil {
				return "", err
			}
			in.Mirror = mirror
			laptop, ok := snaps[local]
			if isLocal {
				laptop, ok = snap, true
			}
			if !ok {
				return "", errors.New("pas encore de relevé du laptop : le port du miroir n'est pas vérifiable")
			}
			in.LaptopListeners = laptop.Listeners
		}
		url, err := address.For(in)
		if err != nil {
			return "", err
		}
		return url, startOpener(url)
	}
}

// startOpener lance le programme d'ouverture avec l'adresse pour seul
// argument, sans shell, sans l'attendre. Ni entrée ni sortie : l'affichage
// tient le terminal. Dans sa propre session, le navigateur survit à la
// fermeture du terminal et à la fin de connex.
func startOpener(url string) error {
	prog := openerProgram()
	cmd := exec.Command(prog, url)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s n'a pas pu être lancé : %v", prog, err)
	}
	go func() { _ = cmd.Wait() }() // ramasse le processus quand il se termine
	return nil
}

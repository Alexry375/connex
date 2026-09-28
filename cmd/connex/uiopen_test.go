package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/address"
	"github.com/Alexry375/connex/internal/config"
	"github.com/Alexry375/connex/internal/model"
)

func runningApp(name string, port int, view string) model.App {
	return model.App{Key: name, Name: name, Port: port, State: model.AppRunning, View: view,
		Health: model.AppHealth{PIDAlive: true, PortListening: true}}
}

func mineOn(port int, addr, exposure string) model.Listener {
	return model.Listener{Key: "tcp:" + addr, Port: port, Addr: addr, Exposure: exposure, Mine: true}
}

// waitArgs attend que le faux programme d'ouverture ait noté ses arguments.
func waitArgs(t *testing.T, argsLog string) string {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if s := readOr(argsLog); strings.HasSuffix(s, "---\n") {
			return s
		}
	}
	t.Fatal("le programme d'ouverture n'a pas été lancé")
	return ""
}

func TestUIOpenerRemoteAndRefusals(t *testing.T) {
	prog, argsLog, _ := fakeProgram(t)
	t.Setenv("CONNEX_OPEN", prog)
	cfg := config.Config{Name: "laptop", Machines: []config.Machine{{Name: "vps", SSH: "vps"}}}
	open := uiOpener(cfg)
	vps := model.Snapshot{
		Machine:   model.Machine{Name: "vps", TailscaleIP: "100.71.85.119"},
		Apps:      []model.App{runningApp("atelier", 8765, model.ViewRemote), {Key: "arret", Name: "arret", State: model.AppStopped, View: model.ViewRemote}},
		Listeners: []model.Listener{mineOn(8765, "100.71.85.119", "tailnet")},
	}
	snaps := map[string]model.Snapshot{"vps": vps}

	url, err := open("vps", "atelier", snaps)
	if err != nil || !strings.HasPrefix(url, "http://100.71.85.119:8765") {
		t.Fatalf("adresse : %q, %v", url, err)
	}
	if got := waitArgs(t, argsLog); got != url+"\n---\n" {
		t.Errorf("arguments du programme d'ouverture : %q", got)
	}

	// Refus : rien n'est lancé.
	os.Remove(argsLog)
	var r *address.Refusal
	if _, err := open("vps", "arret", snaps); !errors.As(err, &r) {
		t.Errorf("appli arrêtée : %v, refus attendu", err)
	}
	if _, err := open("pi", "atelier", snaps); err == nil || !strings.Contains(err.Error(), "pas encore de relevé de pi") {
		t.Errorf("machine sans relevé : %v", err)
	}
	if _, err := open("vps", "../x", snaps); err == nil {
		t.Error("nom d'appli invalide accepté")
	}
	time.Sleep(200 * time.Millisecond)
	if got := readOr(argsLog); got != "" {
		t.Errorf("programme d'ouverture lancé pour un refus : %q", got)
	}
}

// Sans machine distante, la seule machine est la locale, quel que soit le nom
// que lui donne son relevé.
func TestUIOpenerAloneIsLocal(t *testing.T) {
	prog, argsLog, _ := fakeProgram(t)
	t.Setenv("CONNEX_OPEN", prog)
	open := uiOpener(config.Config{Name: "laptop"})
	s := model.Snapshot{
		Machine:   model.Machine{Name: "autre-nom", TailscaleIP: "100.122.145.72"},
		Apps:      []model.App{runningApp("site", 3000, model.ViewRemote)},
		Listeners: []model.Listener{mineOn(3000, "127.0.0.1", "loopback")},
	}
	url, err := open("autre-nom", "site", map[string]model.Snapshot{"autre-nom": s})
	if err != nil || !strings.HasPrefix(url, "http://127.0.0.1:3000") {
		t.Fatalf("adresse : %q, %v", url, err)
	}
	waitArgs(t, argsLog)
}

// Mode train : l'adresse vient du fichier du miroir, vérifiée contre les ports
// du laptop tels que l'affichage les tient.
func TestUIOpenerTrain(t *testing.T) {
	prog, argsLog, _ := fakeProgram(t)
	t.Setenv("CONNEX_OPEN", prog)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, mirrorDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site.url"), []byte("http://127.0.0.1:8801/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Name: "laptop", Machines: []config.Machine{{Name: "vps", SSH: "vps", Train: true}}}
	open := uiOpener(cfg)
	vps := model.Snapshot{Machine: model.Machine{Name: "vps"}, Apps: []model.App{runningApp("site", 4000, model.ViewTrain)}}
	laptop := model.Snapshot{Machine: model.Machine{Name: "laptop"}, Listeners: []model.Listener{mineOn(8801, "127.0.0.1", "loopback")}}

	if _, err := open("vps", "site", map[string]model.Snapshot{"vps": vps}); err == nil {
		t.Error("mode train sans relevé du laptop : accepté")
	}
	url, err := open("vps", "site", map[string]model.Snapshot{"vps": vps, "laptop": laptop})
	if err != nil || url != "http://127.0.0.1:8801/" {
		t.Fatalf("adresse du miroir : %q, %v", url, err)
	}
	waitArgs(t, argsLog)

	// Le port du miroir n'écoute plus : refus.
	laptop.Listeners = nil
	var r *address.Refusal
	if _, err := open("vps", "site", map[string]model.Snapshot{"vps": vps, "laptop": laptop}); !errors.As(err, &r) {
		t.Errorf("miroir arrêté : %v, refus attendu", err)
	}
}

// L'affichage attend la fonction : elle rend la main avant la fin du programme
// d'ouverture.
func TestStartOpenerDoesNotWait(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "lent")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONNEX_OPEN", prog)
	start := time.Now()
	if err := startOpener("http://127.0.0.1:1/"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("startOpener a attendu %v", d)
	}
	t.Setenv("CONNEX_OPEN", filepath.Join(dir, "absent"))
	if err := startOpener("http://127.0.0.1:1/"); err == nil {
		t.Error("programme d'ouverture absent : aucune erreur")
	}
}

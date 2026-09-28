// Package apps gère les applis inscrites par `connex run` (lot L3) : le registre
// local, leurs unités systemd --user, leur état et leur santé, et les actions
// fermées du §4.3 (start, stop, restart, free_port).
//
// La commande d'une appli et son PATH ne vivent que dans le registre (D11) : rien
// ici ne les met dans un relevé, une réponse ou un message d'erreur.
package apps

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// Entry est une appli du registre.
type Entry struct {
	Name         string   `json:"name"`
	Project      string   `json:"project"`
	Dir          string   `json:"dir"`
	Command      []string `json:"command"` // chemin absolu, puis les arguments ({port} déjà remplacé)
	Path         string   `json:"path"`    // PATH au moment de l'inscription
	Port         int      `json:"port"`
	View         string   `json:"view"`
	RegisteredAt string   `json:"registered_at"`
}

// Unit est le nom de l'unité systemd --user de l'appli.
func (e Entry) Unit() string { return UnitName(e.Name) }

// UnitName est le nom de l'unité systemd --user d'une appli.
func UnitName(name string) string { return "connex-" + name + ".service" }

// registryFile est la forme du fichier.
type registryFile struct {
	Schema int     `json:"schema"`
	Apps   []Entry `json:"apps"`
}

// Registry est le fichier des applis inscrites sur cette machine, en 0600 dans
// un dossier en 0700 (§6.7).
type Registry struct {
	Path string
}

// DefaultRegistry renvoie le registre de l'utilisateur :
// $XDG_STATE_HOME/connex/apps.json, par défaut ~/.local/state/connex/apps.json.
func DefaultRegistry() (Registry, error) {
	if d := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return Registry{Path: filepath.Join(d, "connex", "apps.json")}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Registry{}, err
	}
	return Registry{Path: filepath.Join(home, ".local", "state", "connex", "apps.json")}, nil
}

// Load lit le registre. Un registre absent est vide.
func (r Registry) Load() ([]Entry, error) {
	b, err := os.ReadFile(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("registre : %w", err)
	}
	var f registryFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("registre %s illisible : %w", r.Path, err)
	}
	return f.Apps, nil
}

// Find renvoie l'appli nommée.
func (r Registry) Find(name string) (Entry, bool, error) {
	list, err := r.Load()
	if err != nil {
		return Entry{}, false, err
	}
	for _, e := range list {
		if e.Name == name {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

// Update modifie le registre sous un verrou (deux `connex run` en même temps ne
// perdent rien), puis l'écrit de façon atomique.
func (r Registry) Update(fn func([]Entry) ([]Entry, error)) error {
	dir := filepath.Dir(r.Path)
	if err := ensureDir(dir); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "apps.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("registre : %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("registre : verrou : %w", err)
	}
	list, err := r.Load()
	if err != nil {
		return err
	}
	list, err = fn(list)
	if err != nil {
		return err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	b, err := json.MarshalIndent(registryFile{Schema: 1, Apps: list}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(r.Path, append(b, '\n'))
}

// ensureDir crée le dossier en 0700, ou le remet en 0700 s'il existe.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("registre : %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("registre : %w", err)
	}
	return nil
}

// writeAtomic écrit un fichier en 0600 : fichier temporaire du même dossier,
// synchronisé, puis renommé.
func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".apps-*.tmp")
	if err != nil {
		return fmt.Errorf("registre : %w", err)
	}
	defer os.Remove(tmp.Name()) // sans effet après le renommage
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("registre : %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("registre : %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("registre : %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("registre : %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("registre : %w", err)
	}
	return nil
}

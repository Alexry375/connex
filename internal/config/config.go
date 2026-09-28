// Package config lit ~/.config/connex/config.toml, ou le fichier donné par
// --config : le nom de la machine locale, les racines des dépôts, le dossier des
// binaires à poser ailleurs, et les machines distantes. La lecture est stricte :
// une clé inconnue, un nom invalide ou en double est une erreur.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultPath est le fichier de configuration, relatif à $HOME.
const DefaultPath = ".config/connex/config.toml"

// DefaultDist est le dossier des binaires à poser, relatif à $HOME.
const DefaultDist = ".local/share/connex/dist"

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// ValidName dit si s est un nom de machine ou d'appli acceptable.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Machine est une machine distante, jointe par SSH.
type Machine struct {
	Name   string // nom affiché, unique
	SSH    string // destination ssh : alias de ~/.ssh/config, ou utilisateur@hôte
	Train  bool   // train = true : la machine dont apercu-vps fait le miroir (une au plus)
	Public bool   // public = true : la machine est joignable depuis Internet
}

// Config est la configuration lue et validée.
type Config struct {
	Path     string    // fichier lu ; vide si le fichier par défaut n'existe pas
	Name     string    // nom de la machine locale ; vide : celui qu'elle annonce
	Dist     string    // dossier absolu des binaires linux-<arch>
	Machines []Machine // dans l'ordre du fichier
}

// file est la forme du fichier TOML.
type file struct {
	Name     *string  `toml:"name"`
	Roots    []string `toml:"roots"`
	Dist     *string  `toml:"dist"`
	Machines []struct {
		Name   string `toml:"name"`
		SSH    string `toml:"ssh"`
		Train  bool   `toml:"train"`
		Public bool   `toml:"public"`
	} `toml:"machines"`
}

// Load lit la configuration. path vide : le fichier par défaut sous home, qui
// peut manquer (configuration vide). Un fichier donné explicitement doit exister.
func Load(path, home string) (Config, error) {
	explicit := path != ""
	if !explicit {
		path = filepath.Join(home, DefaultPath)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return Config{Dist: filepath.Join(home, DefaultDist)}, nil
		}
		return Config{}, fmt.Errorf("configuration : %w", err)
	}
	c, err := Parse(string(b), home)
	if err != nil {
		return Config{}, fmt.Errorf("configuration %s : %w", path, err)
	}
	c.Path = path
	return c, nil
}

// Parse lit et valide le texte d'une configuration.
func Parse(src, home string) (Config, error) {
	var f file
	md, err := toml.Decode(src, &f)
	if err != nil {
		return Config{}, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return Config{}, fmt.Errorf("clé inconnue : %s", strings.Join(keys, ", "))
	}
	c := Config{Dist: filepath.Join(home, DefaultDist)}
	seen := map[string]bool{}
	train := ""
	if f.Name != nil {
		if !ValidName(*f.Name) {
			return Config{}, fmt.Errorf("name : nom invalide %q (attendu : %s)", *f.Name, nameRe)
		}
		c.Name = *f.Name
		seen[c.Name] = true
	}
	if f.Dist != nil {
		abs, err := absPath(*f.Dist, home)
		if err != nil {
			return Config{}, fmt.Errorf("dist : %w", err)
		}
		c.Dist = abs
	}
	for i, m := range f.Machines {
		where := fmt.Sprintf("machines[%d]", i+1)
		switch {
		case !ValidName(m.Name):
			return Config{}, fmt.Errorf("%s : nom invalide %q (attendu : %s)", where, m.Name, nameRe)
		case seen[m.Name]:
			return Config{}, fmt.Errorf("%s : nom en double %q", where, m.Name)
		case m.SSH == "":
			return Config{}, fmt.Errorf("%s (%s) : destination ssh manquante", where, m.Name)
		case strings.HasPrefix(m.SSH, "-"):
			return Config{}, fmt.Errorf("%s (%s) : une destination ssh ne peut pas commencer par « - »", where, m.Name)
		case strings.ContainsFunc(m.SSH, func(r rune) bool { return r <= ' ' || r == 0x7f }):
			return Config{}, fmt.Errorf("%s (%s) : destination ssh avec un espace ou un caractère de contrôle", where, m.Name)
		}
		if m.Train {
			if train != "" {
				return Config{}, fmt.Errorf("%s (%s) : train = true est déjà posé sur %s ; une seule machine au plus", where, m.Name, train)
			}
			train = m.Name
		}
		seen[m.Name] = true
		c.Machines = append(c.Machines, Machine{Name: m.Name, SSH: m.SSH, Train: m.Train, Public: m.Public})
	}
	return c, nil
}

// TrainMachine renvoie la machine train = true, s'il y en a une.
func (c Config) TrainMachine() (Machine, bool) {
	for _, m := range c.Machines {
		if m.Train {
			return m, true
		}
	}
	return Machine{}, false
}

// Public dit si la machine nommée est public = true.
func (c Config) Public(name string) bool {
	for _, m := range c.Machines {
		if m.Name == name {
			return m.Public
		}
	}
	return false
}

// absPath remplace un ~ initial par home et exige un chemin absolu.
func absPath(p, home string) (string, error) {
	switch {
	case p == "~":
		p = home
	case strings.HasPrefix(p, "~/"):
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("chemin non absolu : %q", p)
	}
	return filepath.Clean(p), nil
}

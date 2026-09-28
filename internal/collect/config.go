package collect

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Alexry375/connex/internal/model"
)

// ConfigPath est le fichier de configuration facultatif, relatif à $HOME.
const ConfigPath = ".config/connex/config.toml"

// Roots choisit les racines où chercher les dépôts :
//   - les --root de la ligne de commande, s'il y en a ;
//   - sinon la clé roots de ~/.config/connex/config.toml, si elle existe ;
//   - sinon ~/Global s'il existe, et ~ à défaut.
//
// Un fichier de configuration illisible est signalé, et on retombe sur le défaut.
func Roots(home string, cli []string) ([]string, []model.Error) {
	if len(cli) > 0 {
		out := make([]string, 0, len(cli))
		for _, r := range cli {
			if abs, err := filepath.Abs(expandHome(r, home)); err == nil {
				out = append(out, abs)
			}
		}
		return out, nil
	}
	var errs []model.Error
	path := filepath.Join(home, ConfigPath)
	if b, err := os.ReadFile(path); err == nil {
		roots, found, perr := parseRootsTOML(string(b))
		switch {
		case perr != nil:
			errs = append(errs, model.Error{Source: "config", Message: path + " : " + perr.Error()})
		case found:
			out := make([]string, 0, len(roots))
			for _, r := range roots {
				r = expandHome(r, home)
				if !filepath.IsAbs(r) {
					errs = append(errs, model.Error{Source: "config", Message: "racine non absolue ignorée : " + r})
					continue
				}
				out = append(out, filepath.Clean(r))
			}
			return out, errs
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, model.Error{Source: "config", Message: err.Error()})
	}
	if fi, err := os.Stat(filepath.Join(home, "Global")); err == nil && fi.IsDir() {
		return []string{filepath.Join(home, "Global")}, errs
	}
	return []string{home}, errs
}

// expandHome remplace un ~ initial par home.
func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// parseRootsTOML lit la clé de premier niveau roots = [ "…", '…' ] d'un fichier
// TOML. C'est un sous-ensemble volontaire : chaînes simples ou littérales,
// tableau sur une ou plusieurs lignes, commentaires. Les autres clés et les
// tables sont ignorées. found indique si la clé est présente.
func parseRootsTOML(src string) (roots []string, found bool, err error) {
	lines := strings.Split(src, "\n")
	inTable := false
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inTable = true
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok || inTable || strings.Trim(strings.TrimSpace(key), `"'`) != "roots" {
			continue
		}
		if found {
			return nil, false, fmt.Errorf("ligne %d : clé roots en double", i+1)
		}
		found = true
		// Le tableau peut s'étendre sur plusieurs lignes : on accumule jusqu'au ].
		value := rest
		for !arrayClosed(value) && i+1 < len(lines) {
			i++
			value += "\n" + lines[i]
		}
		roots, err = parseStringArray(value)
		if err != nil {
			return nil, false, fmt.Errorf("roots : %w", err)
		}
	}
	return roots, found, nil
}

// arrayClosed dit si s contient le ] qui ferme le tableau, hors chaînes et commentaires.
func arrayClosed(s string) bool {
	_, _, err := scanArray(s)
	return err == nil
}

func parseStringArray(s string) ([]string, error) {
	vals, rest, err := scanArray(s)
	if err != nil {
		return nil, err
	}
	rest = strings.TrimSpace(rest)
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return nil, fmt.Errorf("texte inattendu après le tableau : %q", rest)
	}
	return vals, nil
}

// scanArray lit un tableau de chaînes TOML au début de s (espaces admis) et
// renvoie ses valeurs et le texte qui suit le ].
func scanArray(s string) ([]string, string, error) {
	s = strings.TrimLeft(s, " \t")
	if !strings.HasPrefix(s, "[") {
		return nil, "", errors.New("un tableau [ … ] est attendu")
	}
	var vals []string
	expectValue := true
	for i := 1; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '#':
			nl := strings.IndexByte(s[i:], '\n')
			if nl < 0 {
				return nil, "", errors.New("tableau non fermé")
			}
			i += nl
		case c == ']':
			return vals, s[i+1:], nil
		case c == ',':
			if expectValue {
				return nil, "", errors.New("virgule inattendue")
			}
			expectValue = true
			i++
		case c == '"' || c == '\'':
			if !expectValue {
				return nil, "", errors.New("virgule manquante entre deux valeurs")
			}
			end := closingQuote(s, i)
			if end < 0 {
				return nil, "", errors.New("chaîne non fermée")
			}
			v := s[i+1 : end]
			if c == '"' {
				u, err := strconv.Unquote(s[i : end+1])
				if err != nil {
					return nil, "", fmt.Errorf("chaîne invalide %s", s[i:end+1])
				}
				v = u
			}
			vals = append(vals, v)
			expectValue = false
			i = end + 1
		default:
			return nil, "", fmt.Errorf("seules des chaînes sont admises (%q)", string(c))
		}
	}
	return nil, "", errors.New("tableau non fermé")
}

// closingQuote renvoie l'indice du guillemet qui ferme la chaîne ouverte en s[i],
// ou -1. Dans une chaîne "…", \ échappe le caractère suivant ; pas dans '…'.
func closingQuote(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if q == '"' {
				j++
			}
		case '\n':
			return -1
		case q:
			return j
		}
	}
	return -1
}

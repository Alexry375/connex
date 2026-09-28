package collect

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseRootsTOML(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
		found     bool
		bad       bool
	}{
		{"absente", "autre = 3\n", nil, false, false},
		{"une ligne", `roots = ["~/Global", '/srv/a b']`, []string{"~/Global", "/srv/a b"}, true, false},
		{"plusieurs lignes", "# config\nroots = [\n  \"/a\", # premier\n  \"/b\",\n]\n", []string{"/a", "/b"}, true, false},
		{"échappements", `roots = ["/x\"y", "/\u00e9t\u00e9"]`, []string{`/x"y`, "/été"}, true, false},
		{"littérale sans échappement", `roots = ['C:\chemin']`, []string{`C:\chemin`}, true, false},
		{"dans une table : ignorée", "[autre]\nroots = [\"/z\"]\n", nil, false, false},
		{"vide", "roots = []", nil, true, false},
		{"pas un tableau", `roots = "/a"`, nil, false, true},
		{"non fermé", "roots = [\"/a\",\n", nil, false, true},
		{"nombre", "roots = [1]", nil, false, true},
		{"virgule manquante", `roots = ["/a" "/b"]`, nil, false, true},
		{"en double", "roots = [\"/a\"]\nroots = [\"/b\"]", nil, false, true},
	}
	for _, c := range cases {
		got, found, err := parseRootsTOML(c.src)
		if (err != nil) != c.bad {
			t.Errorf("%s : erreur %v", c.name, err)
			continue
		}
		if !c.bad && (found != c.found || !reflect.DeepEqual(got, c.want)) {
			t.Errorf("%s : %q (trouvée %v) ; attendu %q (%v)", c.name, got, found, c.want, c.found)
		}
	}
}

func TestRoots(t *testing.T) {
	home := t.TempDir()
	// Sans ~/Global ni configuration : ~.
	if got, errs := Roots(home, nil); !reflect.DeepEqual(got, []string{home}) || len(errs) != 0 {
		t.Errorf("défaut sans Global : %v %v", got, errs)
	}
	// Avec ~/Global : ~/Global.
	must(t, os.Mkdir(filepath.Join(home, "Global"), 0o755))
	if got, _ := Roots(home, nil); !reflect.DeepEqual(got, []string{filepath.Join(home, "Global")}) {
		t.Errorf("défaut avec Global : %v", got)
	}
	// La configuration remplace le défaut, ~ compris ; une racine relative est refusée.
	cfg := filepath.Join(home, ConfigPath)
	must(t, os.MkdirAll(filepath.Dir(cfg), 0o755))
	must(t, os.WriteFile(cfg, []byte("roots = [\"~/code\", \"/srv/depots\", \"relatif\"]\n"), 0o644))
	got, errs := Roots(home, nil)
	if !reflect.DeepEqual(got, []string{filepath.Join(home, "code"), "/srv/depots"}) || len(errs) != 1 {
		t.Errorf("configuration : %v %v", got, errs)
	}
	// --root remplace tout.
	if got, errs := Roots(home, []string{"/tmp/x", "~/y"}); !reflect.DeepEqual(got, []string{"/tmp/x", filepath.Join(home, "y")}) || len(errs) != 0 {
		t.Errorf("--root : %v %v", got, errs)
	}
	// Configuration cassée : erreur signalée, et retour au défaut.
	must(t, os.WriteFile(cfg, []byte("roots = [\n"), 0o644))
	got, errs = Roots(home, nil)
	if !reflect.DeepEqual(got, []string{filepath.Join(home, "Global")}) || len(errs) != 1 || errs[0].Source != "config" {
		t.Errorf("configuration cassée : %v %v", got, errs)
	}
}

// TestRootsFromExplicitConfig : le fichier de --config se lit comme celui par
// défaut (racine relative ignorée et signalée, liste vide = aucune racine), et
// le fichier par défaut n'est alors pas lu.
func TestRootsFromExplicitConfig(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "connex"), 0o700)
	os.WriteFile(filepath.Join(home, ConfigPath), []byte(`roots = ["/defaut"]`), 0o600)
	f := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(f, []byte("roots = [\"~/a\", \"relative\"]\n[[machines]]\nname = \"vps\"\nssh = \"vps\"\n"), 0o600)
	roots, errs := RootsFrom(home, f, nil)
	if !reflect.DeepEqual(roots, []string{filepath.Join(home, "a")}) || len(errs) != 1 || errs[0].Message != "racine non absolue ignorée : relative" {
		t.Fatalf("%v %v", roots, errs)
	}
	os.WriteFile(f, []byte("roots = []\n"), 0o600)
	if roots, errs = RootsFrom(home, f, nil); len(roots) != 0 || len(errs) != 0 {
		t.Fatalf("liste vide : %v %v", roots, errs)
	}
	if roots, _ = RootsFrom(home, "", nil); !reflect.DeepEqual(roots, []string{"/defaut"}) {
		t.Fatalf("défaut : %v", roots)
	}
	if roots, _ = RootsFrom(home, f, []string{"/cli"}); !reflect.DeepEqual(roots, []string{"/cli"}) {
		t.Fatalf("--root : %v", roots)
	}
}

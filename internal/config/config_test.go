package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseFullConfig lit l'exemple du contrat : ~ développé, ordre des
// machines gardé, dist par défaut. roots est une clé connue, lue par
// collect.RootsFrom comme au L1 (une racine relative y est ignorée).
func TestParseFullConfig(t *testing.T) {
	src := `
name  = "laptop"                      # nom de la machine locale
roots = ["~/Global", '/srv/x', "relative"]

[[machines]]
name = "vps"
ssh  = "vps"

[[machines]]
name = "pi"
ssh  = "abc@pi5"
`
	c, err := Parse(src, "/home/a")
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Name:     "laptop",
		Dist:     "/home/a/.local/share/connex/dist",
		Machines: []Machine{{Name: "vps", SSH: "vps"}, {Name: "pi", SSH: "abc@pi5"}},
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("obtenu %+v\nattendu %+v", c, want)
	}
	c, err = Parse(`dist = "~/d"`, "/home/a")
	if err != nil || c.Dist != "/home/a/d" || c.Name != "" {
		t.Fatalf("dist : %+v, %v", c, err)
	}
}

// TestParseRejects : clé inconnue, nom invalide ou en double, destination ssh
// dangereuse sont des erreurs de configuration claires.
func TestParseRejects(t *testing.T) {
	cases := []struct{ src, want string }{
		{`nom = "x"`, "clé inconnue : nom"},
		{"[[machines]]\nname = \"a\"\nssh = \"a\"\nport = 22\n", "clé inconnue : machines.port"},
		{"[[machines]]\nname = \"Mauvais Nom\"\nssh = \"a\"\n", "nom invalide"},
		{"[[machines]]\nname = \"-a\"\nssh = \"a\"\n", "nom invalide"},
		{"[[machines]]\nname = \"" + strings.Repeat("a", 42) + "\"\nssh = \"a\"\n", "nom invalide"},
		{"[[machines]]\nname = \"a\"\nssh = \"a\"\n[[machines]]\nname = \"a\"\nssh = \"b\"\n", "nom en double"},
		{"name = \"a\"\n[[machines]]\nname = \"a\"\nssh = \"b\"\n", "nom en double"},
		{"[[machines]]\nname = \"x\"\nssh = \"-oProxyCommand=touch\"\n", "commencer par « - »"},
		{"[[machines]]\nname = \"x\"\n", "destination ssh manquante"},
		{"[[machines]]\nname = \"x\"\nssh = \"a b\"\n", "espace"},
		{`dist = "relatif"`, "chemin non absolu"},
		{`name = "Laptop"`, "nom invalide"},
		{`roots = "pas un tableau"`, ""},
	}
	for _, c := range cases {
		_, err := Parse(c.src, "/home/a")
		if err == nil {
			t.Errorf("%q accepté", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q : erreur %q, attendu %q", c.src, err, c.want)
		}
	}
}

// TestLoadDefaultAndExplicit : le fichier par défaut peut manquer, pas celui
// donné par --config ; l'erreur cite le fichier.
func TestLoadDefaultAndExplicit(t *testing.T) {
	home := t.TempDir()
	c, err := Load("", home)
	if err != nil || c.Path != "" || len(c.Machines) != 0 || c.Dist != filepath.Join(home, DefaultDist) {
		t.Fatalf("défaut absent : %+v, %v", c, err)
	}
	if _, err := Load(filepath.Join(home, "absent.toml"), home); err == nil {
		t.Fatal("fichier explicite absent accepté")
	}
	bad := filepath.Join(home, "bad.toml")
	os.WriteFile(bad, []byte("inconnue = 1\n"), 0o600)
	if _, err := Load(bad, home); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("erreur sans le nom du fichier : %v", err)
	}
	def := filepath.Join(home, DefaultPath)
	os.MkdirAll(filepath.Dir(def), 0o700)
	os.WriteFile(def, []byte("[[machines]]\nname = \"vps\"\nssh = \"vps\"\n"), 0o600)
	c, err = Load("", home)
	if err != nil || c.Path != def || len(c.Machines) != 1 {
		t.Fatalf("défaut présent : %+v, %v", c, err)
	}
}

// train = true et public = true, facultatifs, par machine ; une seule machine
// train au plus.
func TestParseTrainPublic(t *testing.T) {
	c, err := Parse(`
[[machines]]
name = "vps"
ssh = "vps"
train = true
public = true

[[machines]]
name = "pi"
ssh = "pi"
`, "/home/a")
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := c.TrainMachine(); !ok || m.Name != "vps" || m.SSH != "vps" {
		t.Errorf("TrainMachine : %+v, %v", m, ok)
	}
	if !c.Public("vps") || c.Public("pi") || c.Public("absente") {
		t.Errorf("Public : vps %v, pi %v", c.Public("vps"), c.Public("pi"))
	}
	c, err = Parse("[[machines]]\nname = \"pi\"\nssh = \"pi\"\n", "/home/a")
	if _, ok := c.TrainMachine(); err != nil || ok {
		t.Errorf("sans train : %v, %v", ok, err)
	}
	_, err = Parse("[[machines]]\nname = \"a\"\nssh = \"a\"\ntrain = true\n[[machines]]\nname = \"b\"\nssh = \"b\"\ntrain = true\n", "/home/a")
	if err == nil || !strings.Contains(err.Error(), "une seule machine") {
		t.Errorf("deux machines train acceptées : %v", err)
	}
	for _, bad := range []string{`train = true`, `public = true`, "[[machines]]\nname = \"a\"\nssh = \"a\"\ntrain = \"oui\"\n"} {
		if _, err := Parse(bad, "/home/a"); err == nil {
			t.Errorf("accepté : %q", bad)
		}
	}
}

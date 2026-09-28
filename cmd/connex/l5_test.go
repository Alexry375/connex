package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Alexry375/connex/internal/config"
)

// fakeProgram écrit un faux programme qui note ses arguments (un par ligne,
// puis ---) et son entrée standard, écrit une ligne et sort avec $FAUX_CODE.
func fakeProgram(t *testing.T) (prog, argsLog, stdinLog string) {
	t.Helper()
	dir := t.TempDir()
	prog, argsLog, stdinLog = filepath.Join(dir, "faux"), filepath.Join(dir, "args"), filepath.Join(dir, "stdin")
	script := "#!/usr/bin/env bash\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\"; done >> " + argsLog + "\n" +
		"echo --- >> " + argsLog + "\n" +
		"timeout 2 cat >> " + stdinLog + "\n" +
		"echo sortie du faux\n" +
		"exit ${FAUX_CODE:-0}\n"
	if err := os.WriteFile(prog, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return prog, argsLog, stdinLog
}

func readOr(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckMirrorDir(t *testing.T) {
	ok := []string{"/home/alfred/projets/site-01", "/srv/a.b_c@d+e-f", "/home/alfred/.hidden/x", "/a..b/c"}
	bad := []string{"", "relatif/x", "/home/a b", "/home/a;touch x", "/home/$(id)", "/home/a/../b", "/home/./a",
		"/home/a/..", "/home/a/.", "/home/`x`", "/home/a'b", "/home/a\nb", "/home/é", "/home/a*", "/home/a~b", "-x", "/", "//"}
	for _, d := range ok {
		if !checkMirrorDir(d) {
			t.Errorf("refusé : %q", d)
		}
	}
	for _, d := range bad {
		if checkMirrorDir(d) {
			t.Errorf("accepté : %q", d)
		}
	}
}

// apercu-vps reçoit exactement ses arguments, une entrée standard vide, et son
// code devient celui de connex.
func TestRunApercu(t *testing.T) {
	prog, argsLog, stdinLog := fakeProgram(t)
	t.Setenv("CONNEX_APERCU", prog)
	var out bytes.Buffer
	if err := runApercu(context.Background(), &out, "/home/a/projet x", "--nom", "site"); err != nil {
		t.Fatal(err)
	}
	if got := readOr(argsLog); got != "/home/a/projet x\n--nom\nsite\n---\n" {
		t.Errorf("arguments : %q", got)
	}
	if got := readOr(stdinLog); got != "" {
		t.Errorf("entrée standard transmise : %q", got)
	}
	if !strings.Contains(out.String(), "sortie du faux") {
		t.Errorf("sortie non transmise : %q", out.String())
	}
	t.Setenv("FAUX_CODE", "7")
	err := runApercu(context.Background(), &out, "site", "stop")
	var code exitCodeError
	if !errors.As(err, &code) || code.code != 7 {
		t.Errorf("code non rendu : %v", err)
	}
	t.Setenv("CONNEX_APERCU", filepath.Join(t.TempDir(), "absent"))
	if err := runApercu(context.Background(), &out, "site", "stop"); err == nil || errors.As(err, &code) {
		t.Errorf("programme absent : %v", err)
	}
}

// Les refus de train tombent avant tout lancement ; --stop ne fait pas de relevé.
func TestRunTrainRefusalsAndStop(t *testing.T) {
	prog, argsLog, _ := fakeProgram(t)
	t.Setenv("CONNEX_APERCU", prog)
	t.Setenv("CONNEX_SSH", "/nonexistent/ssh") // aucun relevé ne doit être tenté
	withTrain := writeConfig(t, "[[machines]]\nname = \"vps\"\nssh = \"vps\"\ntrain = true\n")
	noTrain := writeConfig(t, "[[machines]]\nname = \"vps\"\nssh = \"vps\"\n")
	twoTrains := writeConfig(t, "[[machines]]\nname = \"a\"\nssh = \"a\"\ntrain = true\n[[machines]]\nname = \"b\"\nssh = \"b\"\ntrain = true\n")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"../x", "--config", withTrain}, "nom d'appli invalide"},
		{[]string{"../x", "--stop", "--config", withTrain}, "nom d'appli invalide"},
		{[]string{"Site", "--config", "/nonexistent/config.toml"}, "nom d'appli invalide"}, // le nom d'abord
		{[]string{"site", "--config", noTrain}, "aucune machine en mode train dans la configuration (clé train = true)"},
		{[]string{"site", "--stop", "--config", noTrain}, "aucune machine en mode train"},
		{[]string{"site", "--config", twoTrains}, "une seule machine"},
		{[]string{"--config", withTrain}, "connex train <nom>"},
	}
	for _, c := range cases {
		var out bytes.Buffer
		err := runTrain(context.Background(), c.args, &out)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("train %v : %v ; attendu « %s »", c.args, err, c.want)
		}
	}
	if got := readOr(argsLog); got != "" {
		t.Fatalf("apercu-vps lancé pour un refus : %q", got)
	}
	var out bytes.Buffer
	if err := runTrain(context.Background(), []string{"site", "--stop", "--config", withTrain}, &out); err != nil {
		t.Fatal(err)
	}
	if got := readOr(argsLog); got != "site\nstop\n---\n" {
		t.Errorf("arguments de --stop : %q", got)
	}
}

// Le programme d'ouverture reçoit l'adresse pour seul argument, une entrée
// standard vide ; son échec est rendu.
func TestOpenURL(t *testing.T) {
	prog, argsLog, stdinLog := fakeProgram(t)
	t.Setenv("CONNEX_OPEN", prog)
	if err := openURL(context.Background(), "http://127.0.0.1:8801/"); err != nil {
		t.Fatal(err)
	}
	if got := readOr(argsLog); got != "http://127.0.0.1:8801/\n---\n" {
		t.Errorf("arguments : %q", got)
	}
	if got := readOr(stdinLog); got != "" {
		t.Errorf("entrée standard transmise : %q", got)
	}
	t.Setenv("FAUX_CODE", "3")
	if err := openURL(context.Background(), "http://127.0.0.1:8801/"); err == nil {
		t.Error("échec du programme d'ouverture non rendu")
	}
}

func TestResolveMachine(t *testing.T) {
	cfg := config.Config{Name: "laptop", Machines: []config.Machine{{Name: "vps", SSH: "alfred@vps"}}}
	for _, on := range []string{"", "laptop"} {
		if m, err := resolveMachine(cfg, on); err != nil || !m.local || m.name != "laptop" {
			t.Errorf("--on %q : %+v, %v", on, m, err)
		}
	}
	m, err := resolveMachine(cfg, "vps")
	if err != nil || m.local || m.name != "vps" || m.target.SSH != "alfred@vps" || m.target.Name != "vps" {
		t.Errorf("--on vps : %+v, %v", m, err)
	}
	if _, err := resolveMachine(cfg, "pi"); err == nil {
		t.Error("machine absente de la configuration acceptée")
	}
	host, _ := os.Hostname()
	if m, err := resolveMachine(config.Config{}, ""); err != nil || !m.local || m.name != host {
		t.Errorf("sans nom : %+v, %v", m, err)
	}
}

// Le fichier du miroir est lu sous $HOME ; absent ou trop long, il ne donne rien.
func TestReadMirror(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, err := readMirror("site"); err != nil || got != "" {
		t.Fatalf("absent : %q, %v", got, err)
	}
	dir := filepath.Join(home, ".local/state/miroir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site.url"), []byte("http://127.0.0.1:8801/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readMirror("site"); err != nil || got != "http://127.0.0.1:8801/\n" {
		t.Fatalf("lu : %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gros.url"), bytes.Repeat([]byte("x"), mirrorMax+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readMirror("gros"); err != nil || got != "" {
		t.Fatalf("trop long : %d octets, %v", len(got), err)
	}
	// Un lien, un dossier ou un tube ne sont pas un fichier écrit par apercu-vps.
	if err := os.Symlink(filepath.Join(dir, "site.url"), filepath.Join(dir, "lien.url")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "dossier.url"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "tube.url"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"lien", "dossier", "tube"} {
		if got, err := readMirror(n); err != nil || got != "" {
			t.Errorf("%s : %q, %v", n, got, err)
		}
	}
}

// Le nom puis la machine sont validés avant tout relevé ; rien n'est lancé.
func TestRunOpenValidation(t *testing.T) {
	prog, argsLog, _ := fakeProgram(t)
	t.Setenv("CONNEX_OPEN", prog)
	t.Setenv("CONNEX_SSH", "/nonexistent/ssh")
	cfg := writeConfig(t, "[[machines]]\nname = \"vps\"\nssh = \"vps\"\n")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"x;touch", "--config", "/nonexistent/config.toml"}, "nom d'appli invalide"},
		{[]string{"site", "--on", "pi", "--config", cfg}, "machine inconnue"},
		{[]string{"site", "--on", "vps", "--config", cfg}, "vps est injoignable"},
		{[]string{"--print", "--config", cfg}, "connex open <nom>"},
	}
	for _, c := range cases {
		var out bytes.Buffer
		err := runOpen(context.Background(), c.args, &out)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("open %v : %v ; attendu « %s »", c.args, err, c.want)
		}
		if out.Len() != 0 {
			t.Errorf("open %v : sortie %q", c.args, out.String())
		}
	}
	if got := readOr(argsLog); got != "" {
		t.Fatalf("programme d'ouverture lancé pour un refus : %q", got)
	}
}

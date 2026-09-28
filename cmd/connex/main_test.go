package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLsJSONOnThisMachine fait un vrai relevé de la machine, limité à une racine
// témoin, et vérifie sa forme (§4.1).
func TestLsJSONOnThisMachine(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "temoin")
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"--no-optional-locks"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v : %v %s", args, err, out)
		}
	}
	var out bytes.Buffer
	if err := runLs(context.Background(), []string{"--json", "--root", root, "--root", filepath.Join(root, "absente")}, &out); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "\n"); n != 1 {
		t.Fatalf("%d lignes, une attendue", n)
	}
	var s struct {
		Schema  int    `json:"schema"`
		Type    string `json:"type"`
		Machine struct{ Name, OS, Arch, User string }
		Repos   []struct {
			Path  string
			Dirty *bool
		}
		Listeners []map[string]any
		Agents    []map[string]any
		Errors    []struct{ Source, Message string }
	}
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Schema != 1 || s.Type != "snapshot" || s.Machine.OS != "linux" || s.Machine.Name == "" || s.Machine.User == "" {
		t.Errorf("en-tête : %+v", s)
	}
	if len(s.Repos) != 1 || s.Repos[0].Path != repo || s.Repos[0].Dirty == nil || *s.Repos[0].Dirty {
		t.Errorf("dépôts : %+v", s.Repos)
	}
	found := false
	for _, e := range s.Errors {
		found = found || (e.Source == "repos" && strings.Contains(e.Message, "absente"))
	}
	if !found {
		t.Errorf("la racine absente n'est pas signalée : %+v", s.Errors)
	}
	for _, l := range s.Listeners {
		if l["mine"] == false && (l["pid"] != nil || l["cwd"] != nil || l["process"] != nil) {
			t.Errorf("port d'un autre compte trop bavard : %v", l)
		}
	}
}

// Un ls interrompu (Ctrl-C) n'écrit pas un relevé incomplet.
func TestLsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := runLs(ctx, []string{"--json", "--root", t.TempDir()}, &out); err == nil || out.Len() != 0 {
		t.Errorf("erreur %v, sortie %q", err, out.String())
	}
}

func TestUsageErrors(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	for name, err := range map[string]error{
		"ls argument en trop":   runLs(ctx, []string{"--json", "x"}, &out),
		"ls drapeau inconnu":    runLs(ctx, []string{"--jsn"}, &out),
		"stream interval nul":   runStream(ctx, []string{"--interval", "0s"}, strings.NewReader(""), &out),
		"demo vitesse négative": runDemo(ctx, []string{"--speed", "-1"}, &out),
	} {
		if !errors.Is(err, errUsage) {
			t.Errorf("%s : %v", name, err)
		}
	}
	if out.Len() != 0 {
		t.Errorf("rien ne doit sortir sur stdout : %q", out.String())
	}
}

// TestSourceKeepsSecurityRules relit le code livré (hors tests) et vérifie les
// règles NEVER qui se voient dans le texte : rien n'écoute, rien n'est écrit
// hors du registre des applis (L3), aucune ligne de commande ni aucun
// environnement de processus n'est lu, aucun fichier interdit n'est nommé, et
// git passe toujours par --no-optional-locks.
func TestSourceKeepsSecurityRules(t *testing.T) {
	writes := regexp.MustCompile(`os\.(WriteFile|Create|OpenFile|Mkdir|MkdirAll|Remove|RemoveAll|Rename|Chmod|Symlink)\(`)
	// Seul fichier qui écrit : le registre des applis, 0600 dans un dossier 0700.
	const registry = "internal/apps/registry.go"
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`net\.Listen|ListenUnix|ListenPacket|ListenAndServe|ListenConfig`),
		regexp.MustCompile(`"cmdline"|"environ"|/cmdline|/environ`),
		writes,
		regexp.MustCompile(`"projects"|history\.jsonl|"\.key"|"sessions"\)`),
		regexp.MustCompile(`"sudo"|"lsof"|"ss"`),
	}
	gitCall := regexp.MustCompile(`"git"`)
	var files []string
	for _, dir := range []string{".", "../../internal"} {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				files = append(files, p)
			}
			return nil
		})
	}
	if len(files) < 10 {
		t.Fatalf("seulement %d fichiers relus", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			code, _, _ := strings.Cut(line, "//")
			for _, re := range forbidden {
				if re == writes && strings.HasSuffix(filepath.ToSlash(f), registry) {
					continue
				}
				if re.MatchString(code) {
					t.Errorf("%s:%d : %q interdit", f, i+1, re.FindString(code))
				}
			}
			if gitCall.MatchString(code) && !strings.Contains(code, "--no-optional-locks") {
				t.Errorf("%s:%d : git appelé sans --no-optional-locks", f, i+1)
			}
		}
	}
}

package apps

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/collect"
	"github.com/Alexry375/connex/internal/model"
)

// TestRealSystemdLifecycle fait vivre une vraie appli sous systemd --user : un
// python3 -m http.server jetable, inscrit dans un registre temporaire sous un
// nom test-…, qui est l'appli testée. Le test l'arrête et la désinscrit.
func TestRealSystemdLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	for _, p := range []string{"systemctl", "systemd-run", "python3"} {
		if _, err := exec.LookPath(p); err != nil {
			t.Skip(p + " absent")
		}
	}
	if err := exec.Command("systemctl", "--user", "show", "-p", "Version").Run(); err != nil {
		t.Skip("systemd --user indisponible")
	}
	ctx := context.Background()
	name := "test-it" + strconv.Itoa(os.Getpid())
	unit := UnitName(name)
	t.Cleanup(func() {
		exec.Command("systemctl", "--user", "stop", unit).Run()
		exec.Command("systemctl", "--user", "reset-failed", unit).Run()
	})
	reg := Registry{Path: filepath.Join(t.TempDir(), "state", "connex", "apps.json")}
	m := NewManager(reg, "ici")
	dir := t.TempDir()
	e, err := m.Register(RunOptions{Name: name, Command: []string{"python3", "-m", "http.server", "{port}", "--bind", "127.0.0.1"},
		Dir: dir, PathEnv: os.Getenv("PATH")})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	app := func() model.App {
		l, _ := collect.Listeners("")
		apps, errs := m.Apps(ctx, l)
		if len(errs) > 0 || len(apps) != 1 {
			t.Fatalf("%+v %+v", apps, errs)
		}
		return apps[0]
	}
	waitFor := func(what string, ok func(model.App) bool) model.App {
		t.Helper()
		var a model.App
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
			if a = app(); ok(a) {
				return a
			}
		}
		t.Fatalf("%s : dernier état %+v", what, a)
		return a
	}
	a := waitFor("running", func(a model.App) bool { return a.State == model.AppRunning && a.Health.HTTP == 200 })
	if !a.Health.PIDAlive || !a.Health.PortListening || a.Port != e.Port || a.Unit != unit {
		t.Fatalf("santé : %+v", a)
	}
	b, _ := json.Marshal(a)
	if strings.Contains(string(b), "http.server") || strings.Contains(string(b), os.Getenv("PATH")) {
		t.Fatalf("commande ou PATH dans le relevé : %s", b)
	}
	if err := m.Start(ctx, name); err == nil || !strings.Contains(err.Error(), "tourne déjà") {
		t.Fatalf("deuxième start : %v", err)
	}
	if err := m.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	waitFor("stopped", func(a model.App) bool { return a.State == model.AppStopped && !a.Health.PortListening })
	if err := m.Stop(ctx, name); err == nil || !strings.Contains(err.Error(), "déjà arrêtée") {
		t.Fatalf("deuxième stop : %v", err)
	}
	if err := m.Restart(ctx, name); err != nil { // arrêtée : restart la démarre
		t.Fatal(err)
	}
	waitFor("running après restart", func(a model.App) bool { return a.State == model.AppRunning })
	if err := m.FreePort(ctx, e.Port); err != nil {
		t.Fatal(err)
	}
	a = waitFor("stopped après free", func(a model.App) bool { return a.State == model.AppStopped })
	if a.Health.PortListening {
		t.Fatalf("port encore tenu : %+v", a)
	}
	if err := m.Remove(ctx, name); err != nil {
		t.Fatal(err)
	}
	if list, _ := reg.Load(); len(list) != 0 {
		t.Fatalf("registre après rm : %+v", list)
	}
}

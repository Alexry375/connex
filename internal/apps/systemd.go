package apps

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// Runner lance un programme avec un tableau d'arguments, jamais par un shell,
// et renvoie sa sortie standard et la fin de sa sortie d'erreur.
type Runner func(ctx context.Context, name string, args ...string) (stdout []byte, stderr string, err error)

// ExecRunner est le Runner réel.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	return out.Bytes(), errb.String(), err
}

// startArgs est l'appel à systemd-run qui lance l'appli (contrat L3), sans
// --collect : une unité en échec doit rester visible.
func startArgs(e Entry) []string {
	args := []string{
		"--user",
		"--unit=connex-" + e.Name,
		"--description=connex : " + e.Name,
		"-p", "Restart=on-failure",
		"-p", "RestartSec=2",
		"-p", "TimeoutStopSec=10",
		"--working-directory=" + e.Dir,
		"--setenv=PORT=" + strconv.Itoa(e.Port),
		"--setenv=PATH=" + e.Path,
		"--",
	}
	return append(args, e.Command...)
}

// unitStatus est ce que systemctl --user show dit d'une unité.
type unitStatus struct {
	LoadState   string
	ActiveState string
	SubState    string
	MainPID     int
	Started     time.Time // ExecMainStartTimestamp ; zéro si inconnu
}

// loaded dit si l'unité existe pour systemd.
func (u unitStatus) loaded() bool { return u.LoadState == "loaded" }

// running dit si l'unité tourne ou va tourner (« tourne déjà » pour start).
func (u unitStatus) running() bool {
	switch u.ActiveState {
	case "active", "activating", "reloading":
		return true
	}
	return u.SubState == "auto-restart"
}

// showProps sont les propriétés lues, en un seul appel pour toutes les unités.
var showProps = "Id,LoadState,ActiveState,SubState,MainPID,ExecMainStartTimestamp"

// showArgs construit l'appel à systemctl show pour ces unités.
func showArgs(units []string) []string {
	args := []string{"--user", "show", "--timestamp=unix", "--property=" + showProps, "--"}
	return append(args, units...)
}

// parseShow lit la sortie de systemctl show : un bloc de clé=valeur par unité,
// séparés par une ligne vide. Les blocs sont rattachés à leur unité par Id.
func parseShow(out []byte) map[string]unitStatus {
	res := map[string]unitStatus{}
	for _, block := range strings.Split(strings.TrimSpace(string(out)), "\n\n") {
		var id string
		var u unitStatus
		for _, line := range strings.Split(block, "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch k {
			case "Id":
				id = v
			case "LoadState":
				u.LoadState = v
			case "ActiveState":
				u.ActiveState = v
			case "SubState":
				u.SubState = v
			case "MainPID":
				u.MainPID, _ = strconv.Atoi(v)
			case "ExecMainStartTimestamp":
				if s, ok := strings.CutPrefix(v, "@"); ok {
					if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
						u.Started = time.Unix(n, 0)
					}
				}
			}
		}
		if id != "" {
			res[id] = u
		}
	}
	return res
}

// warmup : une unité active depuis moins longtemps, dont le port n'est pas
// encore tenu, est encore en démarrage.
const warmup = 30 * time.Second

// appState traduit l'état de l'unité en état d'appli (table du contrat L3).
func appState(u unitStatus, portHeld bool, now time.Time) string {
	switch {
	case u.ActiveState == "failed":
		return model.AppFailed
	case u.ActiveState == "activating", u.SubState == "auto-restart":
		return model.AppStarting
	case u.ActiveState == "active", u.ActiveState == "reloading":
		if portHeld || (!u.Started.IsZero() && now.Sub(u.Started) >= warmup) {
			return model.AppRunning
		}
		return model.AppStarting
	}
	// unité absente, inactive, ou en cours d'arrêt
	return model.AppStopped
}

// errText rend l'erreur d'un appel systemctl lisible : la dernière ligne de sa
// sortie d'erreur, sinon l'erreur elle-même.
func errText(stderr string, err error) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return "code " + strconv.Itoa(ee.ExitCode())
	}
	return err.Error()
}

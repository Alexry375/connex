package collect

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// agentTools sont les valeurs de /proc/<pid>/comm reconnues comme agents.
var agentTools = map[string]bool{"claude": true, "codex": true}

// collectAgents relève les sessions d'agents de l'utilisateur.
func (c *Collector) collectAgents(procs []proc) []model.Agent {
	out := []model.Agent{}
	for _, p := range procs {
		if p.uid != c.uid {
			continue
		}
		base := filepath.Join(c.procRoot, strconv.Itoa(p.pid))
		tool := readComm(base)
		if !agentTools[tool] {
			continue
		}
		cwd, _ := os.Readlink(filepath.Join(base, "cwd"))
		state, known := "", false
		if tool == "claude" {
			state, known = claudeState(c.home, p.pid)
		}
		if !known {
			state = c.ttyState(base)
		}
		out = append(out, model.Agent{
			Key:   tool + ":" + strconv.Itoa(p.pid),
			Tool:  tool,
			PID:   p.pid,
			CWD:   cwd,
			State: state,
		})
	}
	return out
}

// claudeSession ne déclare que des champs autorisés de ~/.claude/sessions/<pid>.json
// (pid, sessionId, cwd, status) — ici pid et status. Les autres, dont name (le
// titre de la conversation), ne sont jamais décodés.
type claudeSession struct {
	PID    any `json:"pid"`
	Status any `json:"status"`
}

// maxSessionFile borne la lecture d'un fichier de session.
const maxSessionFile = 1 << 20

// claudeState lit l'état d'une session Claude Code dans
// ~/.claude/sessions/<pid>.json. known est faux quand le fichier manque, est
// illisible ou décrit un autre pid (pid réutilisé) : l'état se déduit alors du
// terminal. Un fichier lisible au statut inconnu donne unknown : on ne devine pas.
func claudeState(home string, pid int) (state string, known bool) {
	f, err := os.Open(filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json"))
	if err != nil {
		return model.StateUnknown, false
	}
	defer f.Close()
	var s claudeSession
	if err := json.NewDecoder(io.LimitReader(f, maxSessionFile)).Decode(&s); err != nil {
		return model.StateUnknown, false
	}
	if n, ok := s.PID.(float64); ok && int(n) != pid {
		return model.StateUnknown, false
	}
	status, _ := s.Status.(string)
	if strings.TrimSpace(status) == "" { // fichier sans statut (anciennes versions) : le terminal dira
		return model.StateUnknown, false
	}
	return MapClaudeStatus(status), true
}

// MapClaudeStatus traduit le champ status d'une session Claude Code. idle (la
// réponse est finie) vaut waiting : la session attend l'utilisateur, comme la montre
// herdr. On ne devine pas : ce qui n'est pas reconnu vaut unknown.
func MapClaudeStatus(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case s == "busy":
		return model.StateWorking
	case s == "idle", strings.Contains(s, "wait"), strings.Contains(s, "attente"),
		strings.Contains(s, "permission"), strings.Contains(s, "approv"):
		return model.StateWaiting
	default:
		return model.StateUnknown
	}
}

// ttyQuiet est le silence au-delà duquel un agent qui écrivait est tenu pour
// en attente. Le noyau ne met à jour l'heure d'un terminal qu'une fois par
// tranche de 8 s : pendant une écriture continue, son âge reste sous 8 s.
const ttyQuiet = 15 * time.Second

// ttyState déduit l'état d'un agent de l'heure de la dernière écriture sur son
// terminal : 15 s ou moins, working ; plus, waiting ; pas de terminal, ou heure
// illisible, unknown. Seule l'heure du fichier /dev/pts/N est lue (stat),
// jamais ce qui s'y affiche.
func (c *Collector) ttyState(procDir string) string {
	pts := ptsOf(procDir)
	if pts < 0 {
		return model.StateUnknown
	}
	dev := c.devRoot
	if dev == "" {
		dev = "/dev"
	}
	fi, err := os.Stat(filepath.Join(dev, "pts", strconv.Itoa(pts)))
	if err != nil {
		return model.StateUnknown
	}
	return ttyStateAt(fi.ModTime(), c.now())
}

// ttyStateAt applique la règle du silence à l'heure d'écriture written.
func ttyStateAt(written, now time.Time) string {
	if now.Sub(written) <= ttyQuiet {
		return model.StateWorking
	}
	return model.StateWaiting
}

// ptsOf renvoie le numéro N du pseudo-terminal /dev/pts/N d'un processus, ou -1.
// Le terminal de contrôle (champ tty_nr de /proc/<pid>/stat) passe d'abord ;
// sans lui, un descripteur 0, 1 ou 2 qui pointe vers /dev/pts/N.
func ptsOf(procDir string) int {
	if b, err := os.ReadFile(filepath.Join(procDir, "stat")); err == nil {
		if n, ok := ptsFromStat(string(b)); ok {
			return n
		}
	}
	for _, fd := range []string{"0", "1", "2"} {
		link, err := os.Readlink(filepath.Join(procDir, "fd", fd))
		if err != nil {
			continue
		}
		if rest, ok := strings.CutPrefix(link, "/dev/pts/"); ok {
			if n, err := strconv.Atoi(rest); err == nil && n >= 0 {
				return n
			}
		}
	}
	return -1
}

// ptsFromStat lit le champ tty_nr (le 7e) d'une ligne /proc/<pid>/stat. Le nom
// du processus, entre parenthèses, peut contenir des espaces : on compte les
// champs après la dernière parenthèse fermante. Les majeurs 136 à 143 sont
// ceux des pseudo-terminaux Unix 98 (/dev/pts).
func ptsFromStat(stat string) (int, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 5 { // état, ppid, pgrp, session, tty_nr
		return 0, false
	}
	nr, err := strconv.ParseInt(f[4], 10, 64)
	if err != nil || nr <= 0 {
		return 0, false
	}
	major := (nr >> 8) & 0xfff
	minor := (nr & 0xff) | ((nr >> 12) & 0xfff00)
	if major < 136 || major > 143 {
		return 0, false
	}
	return int((major-136)<<20 | minor), true
}

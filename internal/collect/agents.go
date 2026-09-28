package collect

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
		state := model.StateUnknown
		if tool == "claude" {
			state = claudeState(c.home, p.pid)
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
// ~/.claude/sessions/<pid>.json. Sans fichier, ou si le fichier décrit un autre
// pid (pid réutilisé), l'état est inconnu.
func claudeState(home string, pid int) string {
	f, err := os.Open(filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json"))
	if err != nil {
		return model.StateUnknown
	}
	defer f.Close()
	var s claudeSession
	if err := json.NewDecoder(io.LimitReader(f, maxSessionFile)).Decode(&s); err != nil {
		return model.StateUnknown
	}
	if n, ok := s.PID.(float64); ok && int(n) != pid {
		return model.StateUnknown
	}
	status, _ := s.Status.(string)
	return MapClaudeStatus(status)
}

// MapClaudeStatus traduit le champ status d'une session Claude Code. On ne
// devine pas : ce qui n'est pas reconnu vaut unknown.
func MapClaudeStatus(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case s == "busy":
		return model.StateWorking
	case s == "idle":
		return model.StateIdle
	case strings.Contains(s, "wait"), strings.Contains(s, "attente"),
		strings.Contains(s, "permission"), strings.Contains(s, "approv"):
		return model.StateWaiting
	default:
		return model.StateUnknown
	}
}

// Package render affiche un relevé en tableau lisible, pour `connex ls` et
// `connex demo` sans --json.
package render

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/Alexry375/connex/internal/model"
)

// Snapshot écrit le relevé s sous forme de tableaux. home sert à raccourcir les
// chemins en ~/… ; vide, les chemins restent entiers.
func Snapshot(w io.Writer, s model.Snapshot, home string) error {
	m := s.Machine
	head := fmt.Sprintf("%s · %s/%s · %s", m.Name, m.OS, m.Arch, m.User)
	if m.TailscaleIP != "" {
		head += " · " + m.TailscaleIP
	}
	fmt.Fprintf(w, "%s · relevé %s\n", head, s.TakenAt)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "\nPORTS (%d)\n", len(s.Listeners))
	if len(s.Listeners) > 0 {
		fmt.Fprintln(tw, "  PORT\tADRESSE\tEXPOSITION\tPID\tPROGRAMME\tDOSSIER")
	}
	for _, l := range s.Listeners {
		pid, process, dir := "—", "uid "+strconv.Itoa(l.UID), ""
		if l.Mine {
			process = l.Program()
			dir = l.ProjectRoot
			if dir == "" {
				dir = l.CWD
			}
		}
		if l.PID != 0 {
			pid = strconv.Itoa(l.PID)
		}
		fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\t%s\n", l.Port, l.Addr, Exposure(l), pid, orDash(process), orDash(short(dir, home)))
	}

	fmt.Fprintf(tw, "\nDÉPÔTS (%d)\n", len(s.Repos))
	if len(s.Repos) > 0 {
		fmt.Fprintln(tw, "  CHEMIN\tBRANCHE\tÉTAT\tWORKTREES")
	}
	behind := false
	for _, r := range s.Repos {
		state := RepoState(r)
		behind = behind || (r.Behind != nil && *r.Behind > 0)
		var wts []string
		for i, wt := range r.Worktrees {
			if i == maxWorktrees {
				wts = append(wts, fmt.Sprintf("+%d", len(r.Worktrees)-i))
				break
			}
			rel, err := filepath.Rel(r.Path, wt.Path)
			if err != nil || strings.HasPrefix(rel, "../..") {
				rel = short(wt.Path, home)
			}
			wts = append(wts, fmt.Sprintf("%s (%s)", rel, branchLabel(wt.Branch, wt.Detached)))
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", short(r.Path, home), branchLabel(r.Branch, r.Detached), state, orDash(strings.Join(wts, ", ")))
	}

	if behind {
		fmt.Fprintln(tw, "  (« en retard » : d’après la branche distante connue au dernier fetch ; connex n’en fait pas)")
	}

	fmt.Fprintf(tw, "\nAGENTS (%d)\n", len(s.Agents))
	if len(s.Agents) > 0 {
		fmt.Fprintln(tw, "  OUTIL\tPID\tÉTAT\tDOSSIER")
	}
	for _, a := range s.Agents {
		fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\n", a.Tool, a.PID, AgentState(a.State), orDash(short(a.CWD, home)))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if len(s.Errors) > 0 {
		fmt.Fprintf(w, "\nERREURS (%d)\n", len(s.Errors))
		for _, e := range s.Errors {
			fmt.Fprintf(w, "  [%s] %s\n", e.Source, e.Message)
		}
	}
	return nil
}

// Exposure dit en clair qui peut joindre une écoute. all porte ⚠ : joignable
// de partout (§6.8), et Funnel la publie sur Internet.
func Exposure(l model.Listener) string {
	switch {
	case l.Funnel:
		return "Internet (Funnel) ⚠"
	case l.Exposure == model.ExposureAll:
		return "all ⚠"
	case l.Exposure == model.ExposureHost:
		return "host (cette machine seulement)"
	}
	return l.Exposure
}

// AgentState dit l'état d'un agent en français.
func AgentState(s string) string {
	switch s {
	case model.StateWorking:
		return "travaille"
	case model.StateWaiting:
		return "t’attend"
	case model.StateIdle:
		return "au repos"
	}
	return "inconnu"
}

// RepoState dit l'état d'un dépôt : propre, modifié ou état inconnu, puis
// « N à pousser » et « N en retard » quand ils ne sont pas nuls. « En retard »
// compare avec la branche distante connue au dernier fetch.
func RepoState(r model.Repo) string {
	state := "état inconnu"
	if r.Dirty != nil {
		state = "propre"
		if *r.Dirty {
			state = "modifié"
		}
	}
	if r.Ahead != nil && *r.Ahead > 0 {
		state += ", " + strconv.Itoa(*r.Ahead) + " à pousser"
	}
	if r.Behind != nil && *r.Behind > 0 {
		state += ", " + strconv.Itoa(*r.Behind) + " en retard"
	}
	return state
}

// maxWorktrees est le nombre de worktrees détaillés par dépôt ; les autres sont comptés.
const maxWorktrees = 3

func branchLabel(branch string, detached bool) string {
	switch {
	case detached:
		return "HEAD détachée"
	case branch == "":
		return "?"
	default:
		return branch
	}
}

// short écrit p en ~/… s'il est sous home.
func short(p, home string) string {
	if home == "" || home == "/" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+"/"); ok {
		return "~/" + rest
	}
	return p
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Alexry375/connex/internal/model"
)

// describe écrit un changement en une phrase courte pour la chronique. old est
// l'élément avant le changement (nil pour un ajout), cur après (nil pour un
// retrait). La machine n'y figure pas : la chronique l'écrit à part.
func describe(op, kind string, old, cur any, home string) (string, tone) {
	switch kind {
	case model.KindListener:
		o, _ := old.(model.Listener)
		n, _ := cur.(model.Listener)
		return describeListener(op, o, n, home)
	case model.KindRepo:
		o, _ := old.(model.Repo)
		n, _ := cur.(model.Repo)
		return describeRepo(op, o, n)
	case model.KindAgent:
		o, _ := old.(model.Agent)
		n, _ := cur.(model.Agent)
		return describeAgent(op, o, n, home)
	case model.KindApp:
		o, _ := old.(model.App)
		n, _ := cur.(model.App)
		return describeApp(op, o, n)
	}
	return kind + " " + op, toneNeutral
}

// holderPhrase dit qui tient un port : « next-server · site » ou « un autre
// compte (uid 0) ».
func holderPhrase(l model.Listener, home string) string {
	if !l.Mine {
		return "autre compte (uid " + strconv.Itoa(l.UID) + ")"
	}
	s := l.Process
	if s == "" {
		s = "programme inconnu"
	}
	if p := placeName(listenerDir(l), home); p != "" {
		s += " · " + p
	}
	return s
}

// listenerDir est le dossier le plus parlant d'un port : la racine du projet,
// sinon le dossier du processus.
func listenerDir(l model.Listener) string {
	if l.ProjectRoot != "" {
		return l.ProjectRoot
	}
	return l.CWD
}

func describeListener(op string, o, n model.Listener, home string) (string, tone) {
	switch op {
	case model.OpAdd:
		t := toneNeutral
		s := "port " + strconv.Itoa(n.Port) + " ouvert — " + holderPhrase(n, home)
		if n.Exposure == "all" {
			s += " · ouvert à tous"
			t = toneOpen
		}
		return s, t
	case model.OpRemove:
		return "port " + strconv.Itoa(o.Port) + " fermé — " + holderPhrase(o, home), toneNeutral
	}
	p := "port " + strconv.Itoa(n.Port)
	switch {
	case o.PID != n.PID && n.PID != 0 && o.PID != 0:
		return fmt.Sprintf("%s : %s relancé (pid %d → %d)", p, orWord(n.Process, "programme"), o.PID, n.PID), toneNeutral
	case o.Exposure != n.Exposure:
		t := toneNeutral
		if n.Exposure == "all" {
			t = toneOpen
		}
		return p + " : " + exposureWord(o.Exposure) + " → " + exposureWord(n.Exposure), t
	case o.Process != n.Process:
		return p + " : " + orWord(o.Process, "?") + " → " + orWord(n.Process, "?"), toneNeutral
	case o.CWD != n.CWD:
		return p + " : dossier " + shortPath(n.CWD, home), toneNeutral
	}
	return p + " modifié", toneNeutral
}

func describeRepo(op string, o, n model.Repo) (string, tone) {
	switch op {
	case model.OpAdd:
		return "dépôt " + projectName(n.Path) + " apparu (" + branchWord(n.Branch, n.Detached) + ")", toneNeutral
	case model.OpRemove:
		return "dépôt " + projectName(o.Path) + " disparu", toneNeutral
	}
	name := "dépôt " + projectName(n.Path)
	var parts []string
	t := toneNeutral
	od, nd := dirtyWord(o.Dirty), dirtyWord(n.Dirty)
	if od != nd {
		switch {
		case n.Dirty != nil && *n.Dirty:
			parts = append(parts, "modifié")
			t = toneWarn
		case n.Dirty != nil:
			parts = append(parts, "de nouveau propre")
		default:
			parts = append(parts, "état inconnu")
		}
	}
	if o.Branch != n.Branch || o.Detached != n.Detached {
		parts = append(parts, "branche "+branchWord(o.Branch, o.Detached)+" → "+branchWord(n.Branch, n.Detached))
	}
	was := map[string]bool{}
	for _, wt := range o.Worktrees {
		was[wt.Path] = true
	}
	is := map[string]bool{}
	for _, wt := range n.Worktrees {
		is[wt.Path] = true
		if !was[wt.Path] {
			parts = append(parts, "worktree "+branchWord(wt.Branch, wt.Detached)+" ouvert")
		}
	}
	for _, wt := range o.Worktrees {
		if !is[wt.Path] {
			parts = append(parts, "worktree "+branchWord(wt.Branch, wt.Detached)+" retiré")
		}
	}
	if len(parts) == 0 {
		return name + " : changé", t
	}
	if len(parts) == 1 && (parts[0] == "modifié" || parts[0] == "de nouveau propre") {
		return name + " " + parts[0], t
	}
	return name + " : " + strings.Join(parts, ", "), t
}

func describeAgent(op string, o, n model.Agent, home string) (string, tone) {
	// « dans site », ou rien quand la source ne donne pas le dossier
	in := func(dir string) string {
		if p := placeName(dir, home); p != "" {
			return " dans " + p
		}
		return ""
	}
	switch op {
	case model.OpAdd:
		return n.Tool + " démarre" + in(n.CWD), toneNeutral
	case model.OpRemove:
		return o.Tool + " s'arrête" + in(o.CWD), toneNeutral
	}
	where := in(n.CWD)
	if o.State != n.State {
		switch n.State {
		case model.StateWaiting:
			return n.Tool + " t'attend" + where, toneCall
		case model.StateWorking:
			return n.Tool + " se remet au travail" + where, toneNeutral
		case model.StateIdle:
			return n.Tool + " se repose" + where, toneNeutral
		}
		return n.Tool + " : état inconnu" + where, toneNeutral
	}
	if o.CWD != n.CWD {
		if where == "" {
			return n.Tool + " change de dossier", toneNeutral
		}
		return n.Tool + " passe" + where, toneNeutral
	}
	return n.Tool + " change" + where, toneNeutral
}

func describeApp(op string, o, n model.App) (string, tone) {
	switch op {
	case model.OpAdd:
		return "appli " + n.Name + " inscrite", toneNeutral
	case model.OpRemove:
		return "appli " + o.Name + " retirée", toneNeutral
	}
	if o.State != n.State {
		switch n.State {
		case model.AppRunning:
			return n.Name + " en marche", toneGood
		case model.AppStarting:
			return n.Name + " démarre", toneNeutral
		case model.AppStopped:
			return n.Name + " arrêtée", toneNeutral
		case model.AppFailed:
			return n.Name + " en échec", toneAlert
		}
	}
	if o.Health.HTTP != n.Health.HTTP {
		switch {
		case n.Health.HTTP == 0:
			return n.Name + " ne répond plus en HTTP", toneWarn
		case n.Health.HTTP >= 400:
			return n.Name + " répond " + strconv.Itoa(n.Health.HTTP), toneWarn
		case o.Health.HTTP >= 400 || o.Health.HTTP == 0:
			return n.Name + " répond de nouveau (" + strconv.Itoa(n.Health.HTTP) + ")", toneGood
		}
	}
	if o.Port != n.Port {
		return n.Name + " : port " + strconv.Itoa(o.Port) + " → " + strconv.Itoa(n.Port), toneNeutral
	}
	return n.Name + " : santé mise à jour", toneNeutral
}

// branchWord écrit une branche, ou « HEAD détachée ».
func branchWord(branch string, detached bool) string {
	switch {
	case detached:
		return "HEAD détachée"
	case branch == "":
		return "branche inconnue"
	}
	return branch
}

func orWord(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestListenerProgram(t *testing.T) {
	for _, c := range []struct {
		l    Listener
		want string
	}{
		{Listener{Process: "MainThread", Exe: "node", Unit: "notifbot-v2-fc558c59.service"}, "notifbot-v2-fc558c59"},
		{Listener{Process: "MainThread", Exe: "node"}, "node"},
		{Listener{Process: "tokio-runtime-w", Exe: "atuin"}, "atuin"},
		{Listener{Process: "Thread-3", Exe: "python3.12"}, "python3.12"},
		{Listener{Process: "MainThread"}, "MainThread"},                // sans exe : on garde ce qu'on a
		{Listener{Process: "next-server", Exe: "node"}, "next-server"}, // un vrai nom de programme reste
		{Listener{Process: "python3", Exe: "python3.12"}, "python3"},
	} {
		if got := c.l.Program(); got != c.want {
			t.Errorf("%+v : %q ; attendu %q", c.l, got, c.want)
		}
	}
}

// Les champs du lot L7 s'ajoutent sans rien changer au reste : ahead et behind
// sortent null sans branche suivie ; funnel, unit, exe, upstream sont omis vides.
func TestL7FieldsJSON(t *testing.T) {
	b, _ := json.Marshal(Repo{Key: "k", Path: "/p", Worktrees: []Worktree{}})
	if s := string(b); !strings.Contains(s, `"ahead":null`) || !strings.Contains(s, `"behind":null`) || strings.Contains(s, "upstream") {
		t.Errorf("dépôt sans branche suivie : %s", s)
	}
	two, zero := 2, 0
	b, _ = json.Marshal(Repo{Upstream: "origin/main", Ahead: &two, Behind: &zero})
	if s := string(b); !strings.Contains(s, `"upstream":"origin/main"`) || !strings.Contains(s, `"ahead":2`) || !strings.Contains(s, `"behind":0`) {
		t.Errorf("dépôt suivi : %s", s)
	}
	b, _ = json.Marshal(Listener{Key: "k"})
	for _, f := range []string{"funnel", "unit", "exe"} {
		if strings.Contains(string(b), `"`+f+`"`) {
			t.Errorf("%s présent vide : %s", f, b)
		}
	}
	b, _ = json.Marshal(Listener{Funnel: true, Unit: "u.service", Exe: "node"})
	if s := string(b); !strings.Contains(s, `"funnel":true`) || !strings.Contains(s, `"unit":"u.service"`) || !strings.Contains(s, `"exe":"node"`) {
		t.Errorf("écoute : %s", s)
	}
}

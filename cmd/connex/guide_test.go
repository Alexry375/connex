package main

import (
	"regexp"
	"strings"
	"testing"
)

// Le guide des agents parle de tout ce que demande le lot L7, et de toutes les
// valeurs d'exposition et d'état que le moteur produit.
func TestGuideAgentsCoversFields(t *testing.T) {
	for _, w := range []string{
		"connex ls --all --json", "connex watch --json", "snapshot", "machine_status",
		"exposure", "loopback", "host", "tailnet", "all", "funnel", "mine", "unit", "exe", "project_root",
		"dirty", "upstream", "ahead", "behind", "working", "waiting", "unknown",
		"start", "stop", "restart", "free", "rm", "open", "--on", "SIGTERM",
		"ligne de commande", "conversation d'agent", "t'attend",
	} {
		if !strings.Contains(guideAgents, w) {
			t.Errorf("le guide ne dit pas %q", w)
		}
	}
	if !regexp.MustCompile(`dernier\s+fetch`).MatchString(guideAgents) {
		t.Error("le guide ne dit pas que behind date du dernier fetch")
	}
	if !regexp.MustCompile(`demander à l'utilisateur avant stop, free, restart ou rm`).MatchString(guideAgents) {
		t.Error("le guide ne dit pas de demander avant d'arrêter")
	}
	if strings.Contains(guideAgents, "\t") {
		t.Error("tabulation dans le guide : texte simple attendu")
	}
	for i, l := range strings.Split(guideAgents, "\n") {
		if n := len([]rune(l)); n > 92 {
			t.Errorf("ligne %d trop longue (%d) : %q", i+1, n, l)
		}
	}
}

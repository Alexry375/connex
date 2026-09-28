package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
	"github.com/Alexry375/connex/internal/ui/term"
)

// Parcours 3, « ce qui a changé » : chaque changement du relevé devient une
// nouvelle en mots (port apparu ou disparu, exposition, dépôt modifié puis
// propre) ; la dernière se lit au-dessus du papier de la planche I, toutes au
// journal, d'où Entrée mène à la planche de l'objet.
func TestJournalTellsWhatChanged(t *testing.T) {
	a, _ := newTestApp(t, 120, 36)
	now := t0.Add(5 * time.Second)
	jupyter := model.Listener{Key: "tcp4:0.0.0.0:8888", Port: 8888, Addr: "0.0.0.0", Exposure: "all", Mine: true, UID: 1000, PID: 150,
		Process: "jupyter-lab", CWD: "/home/alfred/p/ml-notebooks", ProjectRoot: "/home/alfred/p/ml-notebooks"}
	syncthing := model.Listener{Key: "tcp4:100.64.0.1:8384", Port: 8384, Addr: "0.0.0.0", Exposure: "all", Mine: true, UID: 1000, PID: 104,
		Process: "syncthing", CWD: longDir}
	sshd := model.Listener{Key: "tcp4:0.0.0.0:2222", Port: 2222, Addr: "0.0.0.0", Exposure: "all", Mine: false, UID: 0}
	clean, dirty := false, true
	nuage := model.Repo{Key: "/home/alfred/p/nuage", Path: "/home/alfred/p/nuage", Branch: "main", Dirty: &clean, Worktrees: []model.Worktree{}}
	nuageDirty := nuage
	nuageDirty.Dirty = &dirty

	steps := []struct {
		ch   model.Change
		want string
		tone tone
		sign rune
	}{
		{model.Change{Op: model.OpAdd, Kind: model.KindListener, Key: jupyter.Key, Data: jupyter},
			"port 8888 apparu · jupyter-lab · ~/p/ml-notebooks · ouvert à tous", toneWarn, '+'},
		{model.Change{Op: model.OpRemove, Kind: model.KindListener, Key: "tcp4:127.0.0.1:5000"},
			"port 5000 disparu · python3 · ~/p/ml-notebooks", toneNeutral, '−'},
		{model.Change{Op: model.OpUpdate, Kind: model.KindListener, Key: syncthing.Key, Data: syncthing},
			"port 8384 : Tailscale → ouvert à tous", toneNeutral, '~'},
		{model.Change{Op: model.OpUpdate, Kind: model.KindRepo, Key: nuage.Key, Data: nuage},
			"dépôt nuage redevenu propre", toneGood, '~'},
		{model.Change{Op: model.OpUpdate, Kind: model.KindRepo, Key: nuage.Key, Data: nuageDirty},
			"dépôt nuage modifié", toneWarn, '~'},
		{model.Change{Op: model.OpAdd, Kind: model.KindListener, Key: sshd.Key, Data: sshd},
			"port 2222 apparu · autre compte (uid 0) · ouvert à tous", toneWarn, '+'},
	}
	for i, s := range steps {
		at := now.Add(time.Duration(i) * time.Second)
		a.event(patchEv("laptop", at, s.ch), at)
		e := lastEntry(t, a)
		if e.text != s.want || e.tone != s.tone || e.sign != s.sign {
			t.Fatalf("étape %d : nouvelle %q (ton %d, signe %c), attendu %q (ton %d, signe %c)", i, e.text, e.tone, e.sign, s.want, s.tone, s.sign)
		}
		// Planche I : la dernière nouvelle se lit au-dessus du papier.
		if all := strings.Join(rowsOf(t, a, at), "\n"); !strings.Contains(all, s.want) {
			t.Errorf("étape %d : « %s » ne se lit pas sur la planche I\n%s", i, s.want, all)
		}
	}

	// Planche III : toutes, la plus récente en haut.
	end := now.Add(10 * time.Second)
	a.key(code(term.Tab), end)
	all := strings.Join(rowsOf(t, a, end), "\n")
	last := -1
	for i := len(steps) - 1; i >= 0; i-- {
		at := strings.Index(all, steps[i].want)
		if at < 0 {
			t.Fatalf("journal : « %s » manque\n%s", steps[i].want, all)
		}
		if at < last {
			t.Errorf("journal : « %s » n'est pas sous la nouvelle suivante", steps[i].want)
		}
		last = at
	}
	// Entrée mène à la planche de l'objet de la nouvelle choisie : le port 2222.
	a.key(code(term.Enter), end)
	if a.view != viewPlate || a.plate != itemID("laptop", kindForeign, sshd.Key) {
		t.Fatalf("Entrée depuis le journal : vue %d, planche %q", a.view, a.plate)
	}
	if all := strings.Join(rowsOf(t, a, end), "\n"); !strings.Contains(all, "PORT 2222.") {
		t.Errorf("la planche du port 2222 ne s'ouvre pas\n%s", all)
	}
}

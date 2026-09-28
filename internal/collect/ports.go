package collect

import (
	"os"

	"github.com/Alexry375/connex/internal/model"
)

// Listeners relève seulement les ports TCP en écoute de la machine, sans dépôts,
// agents ni Funnel : c'est ce dont ont besoin les actions sur les applis (lot L3). Les
// règles sont celles du relevé complet (propriétaire, processus, dossier).
// procRoot vaut /proc si vide.
func Listeners(procRoot string) ([]model.Listener, []model.Error) {
	if procRoot == "" {
		procRoot = "/proc"
	}
	uid := os.Getuid()
	c := &Collector{procRoot: procRoot, uid: uid, owners: newSocketOwners(procRoot, uid)}
	socks, errs := c.readSockets()
	procs, err := listProcs(procRoot)
	if err != nil {
		return nil, append(errs, model.Error{Source: "listeners", Message: err.Error()})
	}
	// L'exposition host est lue ici aussi ; pas le Funnel, qui demanderait de
	// lancer tailscale à chaque sonde de santé d'une appli.
	return c.collectListeners(socks, procs, networkMounts(procRoot), c.localHostAddrs()), errs
}

// NetworkMounts renvoie les montages réseau (sshfs, nfs…) que ProjectRoot ne
// doit pas examiner ; procRoot vaut /proc si vide.
func NetworkMounts(procRoot string) map[string]bool {
	if procRoot == "" {
		procRoot = "/proc"
	}
	return networkMounts(procRoot)
}

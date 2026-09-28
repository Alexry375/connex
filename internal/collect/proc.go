package collect

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// proc est un processus vu dans /proc : son pid et le compte qui le possède.
type proc struct {
	pid, uid int
}

// listProcs énumère les processus de procRoot, triés par pid. Le compte vient du
// propriétaire du dossier /proc/<pid> ; rien d'autre n'est lu ici.
func listProcs(procRoot string) ([]proc, error) {
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	out := make([]proc, 0, len(ents))
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fi, err := os.Stat(filepath.Join(procRoot, e.Name()))
		if err != nil {
			continue // processus terminé entre-temps
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		out = append(out, proc{pid: pid, uid: int(st.Uid)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pid < out[j].pid })
	return out, nil
}

// readComm lit le nom de l'exécutable d'un processus (/proc/<pid>/comm) : jamais
// sa ligne de commande.
func readComm(procDir string) string {
	b, err := os.ReadFile(filepath.Join(procDir, "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

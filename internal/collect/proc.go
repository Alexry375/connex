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

// readUnit renvoie l'unité systemd .service qui contient un processus, lue
// dans /proc/<pid>/cgroup : le dernier segment du chemin qui finit en
// .service. Vide si ce n'est pas un .service (session-N.scope, app-….scope ne
// disent rien du programme) ou si c'est le gestionnaire user@….service.
func readUnit(procDir string) string {
	b, err := os.ReadFile(filepath.Join(procDir, "cgroup"))
	if err != nil {
		return ""
	}
	return unitFromCgroup(string(b))
}

// unitFromCgroup lit la ligne cgroup v2 (« 0::/chemin ») ; à défaut, celle de
// la hiérarchie name=systemd d'un système en cgroup v1.
func unitFromCgroup(content string) string {
	var path string
	for _, line := range strings.Split(content, "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) != 3 {
			continue
		}
		if f[0] == "0" && f[1] == "" {
			path = f[2]
			break
		}
		if f[1] == "name=systemd" && path == "" {
			path = f[2]
		}
	}
	segs := strings.Split(path, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		seg := segs[i]
		if !strings.HasSuffix(seg, ".service") {
			continue
		}
		if strings.HasPrefix(seg, "user@") || seg == ".service" {
			return ""
		}
		return seg
	}
	return ""
}

// readExe renvoie le nom de base de l'exécutable d'un processus (lien
// /proc/<pid>/exe), sans la mention « (deleted) » d'un binaire remplacé.
// Vide s'il est illisible.
func readExe(procDir string) string {
	link, err := os.Readlink(filepath.Join(procDir, "exe"))
	if err != nil || link == "" {
		return ""
	}
	link = strings.TrimSuffix(link, " (deleted)")
	base := filepath.Base(link)
	if base == "/" || base == "." {
		return ""
	}
	return base
}

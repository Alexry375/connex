package apps

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Lecture de /proc pour les applis : appartenance d'un processus à une unité
// (par son cgroup), heure de démarrage, nom de l'exécutable. Jamais la ligne de
// commande ni l'environnement.

var unitRe = regexp.MustCompile(`^connex-([a-z0-9][a-z0-9-]{0,40})\.service$`)

// cgroupSegments renvoie les éléments du chemin cgroup du processus.
func cgroupSegments(procRoot string, pid int) []string {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return nil
	}
	var segs []string
	for _, line := range strings.Split(string(b), "\n") {
		// hiérarchie:contrôleurs:chemin ; le chemin peut contenir des « : »
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		segs = append(segs, strings.Split(parts[2], "/")...)
	}
	return segs
}

// inUnit dit si le processus appartient à l'unité, d'après /proc/<pid>/cgroup.
func inUnit(procRoot string, pid int, unit string) bool {
	for _, s := range cgroupSegments(procRoot, pid) {
		if s == unit {
			return true
		}
	}
	return false
}

// connexApp renvoie le nom de l'appli dont l'unité contient le processus, ou "".
func connexApp(procRoot string, pid int) string {
	for _, s := range cgroupSegments(procRoot, pid) {
		if m := unitRe.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	return ""
}

// pidAlive dit si le processus existe.
func pidAlive(procRoot string, pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.Stat(filepath.Join(procRoot, strconv.Itoa(pid)))
	return err == nil
}

// startTime lit l'heure de démarrage du processus (champ 22 de /proc/<pid>/stat,
// en tops d'horloge depuis le démarrage). Deux processus de même pid et de même
// heure de démarrage sont le même processus.
func startTime(procRoot string, pid int) (uint64, error) {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')') // le nom, entre parenthèses, peut contenir des espaces
	if i < 0 {
		return 0, errors.New("stat illisible")
	}
	f := strings.Fields(s[i+1:])
	// f[0] est le champ 3 (état) : le champ 22 est f[19].
	if len(f) < 20 {
		return 0, errors.New("stat illisible")
	}
	return strconv.ParseUint(f[19], 10, 64)
}

// procUIDs renvoie les comptes réel, effectif et sauvegardé du processus (ligne
// Uid: de /proc/<pid>/status). Le propriétaire du dossier /proc/<pid> ne suffit
// pas : celui d'un processus non « dumpable » est root.
func procUIDs(procRoot string, pid int) ([]int, error) {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "status"))
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 3 {
			break
		}
		uids := make([]int, 3)
		for i := range uids {
			if uids[i], err = strconv.Atoi(f[i]); err != nil {
				return nil, errors.New("propriétaire illisible")
			}
		}
		return uids, nil
	}
	return nil, errors.New("propriétaire illisible")
}

// comm lit le nom de l'exécutable (/proc/<pid>/comm).
func comm(procRoot string, pid int) string {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

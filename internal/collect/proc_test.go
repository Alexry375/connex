package collect

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestUnitFromCgroup(t *testing.T) {
	for in, want := range map[string]string{
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/notifbot-v2-fc558c59.service\n": "notifbot-v2-fc558c59.service",
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/essai-x.service/sous-groupe\n":  "essai-x.service",
		"0::/system.slice/caddy.service\n":                                                        "caddy.service",
		"0::/user.slice/user-1000.slice/session-3.scope\n":                                        "",
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-gnome-firefox-42.scope\n": "",
		"0::/user.slice/user-1000.slice/user@1000.service/init.scope\n":                           "", // systemd --user lui-même
		"0::/user.slice/user-1000.slice/user@1000.service\n":                                      "",
		"12:pids:/user.slice\n1:name=systemd:/system.slice/nginx.service\n0::/\n":                 "", // hybride : la ligne v2 passe d'abord
		"12:pids:/user.slice\n1:name=systemd:/system.slice/nginx.service\n":                       "nginx.service",
		"":          "",
		"illisible": "",
	} {
		if got := unitFromCgroup(in); got != want {
			t.Errorf("unitFromCgroup(%q) = %q ; attendu %q", in, got, want)
		}
	}
}

// exe : le nom de base du lien, sans « (deleted) » ; rien s'il est illisible.
// Le programme se nomme par l'unité, sinon par exe quand comm n'est qu'un nom
// de fil d'exécution, sinon par comm.
func TestUnitAndExeOfListener(t *testing.T) {
	uid := strconv.Itoa(os.Getuid())
	tcp := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:0BB9 00000000:0000 0A 00000000:00000000 00:00000000 00000000  " + uid + "        0 8001 1\n" +
		"   1: 0100007F:0BBA 00000000:0000 0A 00000000:00000000 00:00000000 00000000  " + uid + "        0 8002 1\n" +
		"   2: 0100007F:0BBB 00000000:0000 0A 00000000:00000000 00:00000000 00000000  " + uid + "        0 8003 1\n"
	fp := newFakeProc(t, tcp, "")
	fp.addProc(10, "MainThread", "/", 8001)
	fp.addProc(11, "MainThread", "/", 8002)
	fp.addProc(12, "python3", "/", 8003)
	dir := func(pid int) string { return filepath.Join(fp.root, strconv.Itoa(pid)) }
	must(t, os.WriteFile(filepath.Join(dir(10), "cgroup"), []byte("0::/user.slice/user-1000.slice/user@1000.service/app.slice/notifbot-v2-fc558c59.service\n"), 0o644))
	must(t, os.Symlink("/usr/bin/node", filepath.Join(dir(10), "exe")))
	must(t, os.WriteFile(filepath.Join(dir(11), "cgroup"), []byte("0::/user.slice/user-1000.slice/session-2.scope\n"), 0o644))
	must(t, os.Symlink("/home/a/.nvm/versions/node/v22/bin/node (deleted)", filepath.Join(dir(11), "exe")))
	// 12 : ni cgroup ni exe lisibles.

	c := testCollector(fp.root, t.TempDir(), nil)
	socks, _ := c.readSockets()
	procs, _ := listProcs(fp.root)
	got := map[int][3]string{}
	for _, l := range c.collectListeners(socks, procs, map[string]bool{}, nil) {
		got[l.Port] = [3]string{l.Unit, l.Exe, l.Program()}
	}
	want := map[int][3]string{
		3001: {"notifbot-v2-fc558c59.service", "node", "notifbot-v2-fc558c59"},
		3002: {"", "node", "node"},
		3003: {"", "", "python3"},
	}
	for port, w := range want {
		if got[port] != w {
			t.Errorf("port %d : %q ; attendu %q", port, got[port], w)
		}
	}
}

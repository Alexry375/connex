package collect

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Alexry375/connex/internal/model"
)

// tcpListen est l'état TCP_LISTEN dans /proc/net/tcp.
const tcpListen = "0A"

// socket est une ligne en écoute de /proc/net/tcp ou /proc/net/tcp6.
type socket struct {
	proto string // "tcp4" ou "tcp6"
	addr  netip.Addr
	port  uint16
	uid   int
	inode uint64
}

func (s socket) key() string {
	return s.proto + ":" + netip.AddrPortFrom(s.addr, s.port).String()
}

// parseProcNet lit le contenu de /proc/net/tcp (proto "tcp4") ou /proc/net/tcp6
// (proto "tcp6") et renvoie les sockets en écoute.
func parseProcNet(r io.Reader, proto string) ([]socket, error) {
	var out []socket
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		if first { // ligne d'en-tête
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != tcpListen {
			continue
		}
		hexAddr, hexPort, ok := strings.Cut(f[1], ":")
		if !ok {
			return nil, fmt.Errorf("adresse illisible : %q", f[1])
		}
		addr, err := decodeProcAddr(hexAddr)
		if err != nil {
			return nil, err
		}
		port, err := strconv.ParseUint(hexPort, 16, 16)
		if err != nil {
			return nil, fmt.Errorf("port illisible : %q", hexPort)
		}
		uid, err := strconv.Atoi(f[7])
		if err != nil {
			return nil, fmt.Errorf("uid illisible : %q", f[7])
		}
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("inode illisible : %q", f[9])
		}
		out = append(out, socket{proto: proto, addr: addr, port: uint16(port), uid: uid, inode: inode})
	}
	return out, sc.Err()
}

// decodeProcAddr décode une adresse de /proc/net/tcp*. Le noyau l'écrit par mots
// de 32 bits, chacun dans l'ordre d'octets de la machine.
func decodeProcAddr(h string) (netip.Addr, error) {
	if len(h) != 8 && len(h) != 32 {
		return netip.Addr{}, fmt.Errorf("adresse de longueur inattendue : %q", h)
	}
	b := make([]byte, len(h)/2)
	for i := 0; i < len(h); i += 8 {
		w, err := strconv.ParseUint(h[i:i+8], 16, 32)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("adresse illisible : %q", h)
		}
		binary.NativeEndian.PutUint32(b[i/2:], uint32(w))
	}
	addr, _ := netip.AddrFromSlice(b)
	return addr, nil
}

var (
	tailnet4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnet6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// Exposure classe une adresse d'écoute : loopback, tailnet ou all. Une adresse
// IPv4 mappée dans IPv6 (::ffff:a.b.c.d) est jugée sur son adresse IPv4.
func Exposure(a netip.Addr) string {
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		return "loopback"
	case tailnet4.Contains(a), tailnet6.Contains(a):
		return "tailnet"
	default:
		return "all"
	}
}

// owner est le processus qui tient un socket : son pid et le numéro de descripteur.
type owner struct {
	pid, fd int
}

// socketOwners retrouve, parmi les processus de l'utilisateur, celui qui tient
// chaque inode demandé. Les dossiers fd des autres comptes sont illisibles sans
// root : on ne les ouvre même pas.
type socketOwners struct {
	procRoot string
	uid      int
	known    map[uint64]owner    // inode → propriétaire vérifié
	orphans  map[uint64]struct{} // inodes sans propriétaire lisible au dernier balayage
}

func newSocketOwners(procRoot string, uid int) *socketOwners {
	return &socketOwners{procRoot: procRoot, uid: uid, known: map[uint64]owner{}, orphans: map[uint64]struct{}{}}
}

// resolve renvoie le propriétaire de chaque inode trouvé. Tant que les inodes
// sont déjà connus et que leurs descripteurs pointent toujours sur eux, aucun
// balayage de /proc/*/fd n'a lieu : c'est ce qui garde stream sobre au repos.
func (o *socketOwners) resolve(inodes []uint64, procs []proc) map[uint64]owner {
	needScan := false
	for _, ino := range inodes {
		if ow, ok := o.known[ino]; ok {
			if o.fdIs(ow, ino) {
				continue
			}
			delete(o.known, ino)
			needScan = true
		} else if _, ok := o.orphans[ino]; !ok {
			needScan = true
		}
	}
	if needScan {
		o.scan(inodes, procs)
	}
	out := make(map[uint64]owner, len(inodes))
	for _, ino := range inodes {
		if ow, ok := o.known[ino]; ok {
			out[ino] = ow
		}
	}
	return out
}

func (o *socketOwners) fdIs(ow owner, ino uint64) bool {
	link, err := os.Readlink(filepath.Join(o.procRoot, strconv.Itoa(ow.pid), "fd", strconv.Itoa(ow.fd)))
	return err == nil && link == socketLink(ino)
}

func socketLink(ino uint64) string {
	return "socket:[" + strconv.FormatUint(ino, 10) + "]"
}

func (o *socketOwners) scan(inodes []uint64, procs []proc) {
	want := make(map[string]uint64, len(inodes))
	for _, ino := range inodes {
		want[socketLink(ino)] = ino
	}
	found := map[uint64]owner{}
	for _, p := range procs { // procs est trié par pid : le plus petit pid gagne
		if p.uid != o.uid {
			continue
		}
		fdDir := filepath.Join(o.procRoot, strconv.Itoa(p.pid), "fd")
		ents, err := os.ReadDir(fdDir)
		if err != nil {
			continue // processus disparu ou non inspectable
		}
		for _, e := range ents {
			link, err := os.Readlink(filepath.Join(fdDir, e.Name()))
			if err != nil {
				continue
			}
			ino, ok := want[link]
			if !ok {
				continue
			}
			if _, dup := found[ino]; dup {
				continue
			}
			fd, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			found[ino] = owner{pid: p.pid, fd: fd}
		}
	}
	o.known = found
	o.orphans = map[uint64]struct{}{}
	for _, ino := range inodes {
		if _, ok := found[ino]; !ok {
			o.orphans[ino] = struct{}{}
		}
	}
}

// projectMarkers sont les fichiers qui signalent la racine d'un projet (§4.1).
var projectMarkers = []string{".git", "go.mod", "package.json", "pyproject.toml", "Cargo.toml"}

// ProjectRoot remonte depuis dir jusqu'au premier dossier qui contient un
// marqueur de projet. Seule l'existence du marqueur est testée, jamais son contenu.
func ProjectRoot(dir string) string {
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		for _, m := range projectMarkers {
			if _, err := os.Lstat(filepath.Join(d, m)); err == nil {
				return d
			}
		}
		if d == "/" {
			return ""
		}
	}
}

// collectListeners relève les ports TCP en écoute de la machine.
func (c *Collector) collectListeners(procs []proc) ([]model.Listener, []model.Error) {
	var errs []model.Error
	var socks []socket
	for _, src := range []struct{ file, proto string }{{"tcp", "tcp4"}, {"tcp6", "tcp6"}} {
		f, err := os.Open(filepath.Join(c.procRoot, "net", src.file))
		if err != nil {
			if !os.IsNotExist(err) { // pas d'IPv6 sur la machine : rien à signaler
				errs = append(errs, model.Error{Source: "listeners", Message: err.Error()})
			}
			continue
		}
		s, err := parseProcNet(f, src.proto)
		f.Close()
		if err != nil {
			errs = append(errs, model.Error{Source: "listeners", Message: src.file + " : " + err.Error()})
		}
		socks = append(socks, s...)
	}

	inodes := make([]uint64, 0, len(socks))
	for _, s := range socks {
		inodes = append(inodes, s.inode)
	}
	owners := c.owners.resolve(inodes, procs)
	uidOf := make(map[int]int, len(procs))
	for _, p := range procs {
		uidOf[p.pid] = p.uid
	}

	byKey := map[string]model.Listener{}
	for _, s := range socks {
		l := model.Listener{
			Key:      s.key(),
			Port:     int(s.port),
			Addr:     s.addr.String(),
			Exposure: Exposure(s.addr),
			UID:      s.uid,
			Mine:     s.uid == c.uid,
		}
		if ow, ok := owners[s.inode]; ok {
			l.UID = uidOf[ow.pid]
			l.Mine = l.UID == c.uid
			if l.Mine {
				base := filepath.Join(c.procRoot, strconv.Itoa(ow.pid))
				l.PID = ow.pid
				l.Process = readComm(base)
				l.CWD, _ = os.Readlink(filepath.Join(base, "cwd"))
				l.ProjectRoot = ProjectRoot(l.CWD)
			}
		}
		// Deux sockets sur la même adresse (SO_REUSEPORT) : une seule entrée,
		// de préférence celle dont on connaît le processus.
		if prev, ok := byKey[l.Key]; ok && (prev.PID != 0 && (l.PID == 0 || prev.PID < l.PID)) {
			continue
		}
		byKey[l.Key] = l
	}
	out := make([]model.Listener, 0, len(byKey))
	for _, l := range byKey {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Key < out[j].Key
	})
	return out, errs
}

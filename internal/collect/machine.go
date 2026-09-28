package collect

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// tailscaleSelf est la partie utile de `tailscale status --json`.
type tailscaleSelf struct {
	Self struct {
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
}

// parseTailscale extrait le nom Tailscale (première étiquette de Self.DNSName)
// et l'adresse IPv4 Tailscale de la sortie de `tailscale status --json`.
func parseTailscale(out []byte) (name, ipv4 string, err error) {
	var st tailscaleSelf
	if err := json.Unmarshal(out, &st); err != nil {
		return "", "", err
	}
	name, _, _ = strings.Cut(st.Self.DNSName, ".")
	for _, s := range st.Self.TailscaleIPs {
		if a, err := netip.ParseAddr(s); err == nil && a.Is4() {
			ipv4 = a.String()
			break
		}
	}
	return name, ipv4, nil
}

// Identify décrit la machine locale. Le nom est le nom Tailscale, à défaut le
// nom d'hôte ; une panne de tailscale est signalée dans les erreurs.
func Identify(ctx context.Context) (model.Machine, []model.Error) {
	m := model.Machine{OS: runtime.GOOS, Arch: runtime.GOARCH, User: currentUser()}
	var errs []model.Error

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tailscale", "status", "--json", "--peers=false").Output()
	switch {
	case errors.Is(err, exec.ErrNotFound):
		// pas de tailscale : le nom d'hôte suffit, ce n'est pas une erreur
	case err != nil:
		errs = append(errs, model.Error{Source: "machine", Message: "tailscale status : " + err.Error()})
	default:
		name, ip, perr := parseTailscale(out)
		if perr != nil {
			errs = append(errs, model.Error{Source: "machine", Message: "tailscale status illisible : " + perr.Error()})
		}
		m.Name, m.TailscaleIP = name, ip
	}
	if m.Name == "" {
		m.Name, _ = os.Hostname()
	}
	return m, errs
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return strconv.Itoa(os.Getuid())
}

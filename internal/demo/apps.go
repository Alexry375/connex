package demo

import (
	"math/rand/v2"
	"net/netip"
	"strings"

	"github.com/Alexry375/connex/internal/model"
)

// Applis inscrites (lot L3). Certains serveurs ne sont pas lancés à la main mais
// par `connex run`, sous une unité systemd --user : leur vie suit celle de
// l'unité (plantage, relance par systemd, arrêt et démarrage demandés).

// appSpec désigne un projet d'une machine à inscrire comme appli.
type appSpec struct {
	project string // nom du projet dans le monde simulé
	view    string
	state   string // état de départ
}

// registerApps inscrit des applis sur les trois machines. Les cas du §8 y sont :
// deux applis qui veulent le même port (clepsydre et nuage, 5173), une appli
// arrêtée dont le port est occupé par un serveur oublié (planetaire, 5000), une
// appli en échec (cadre-photo), une appli regardée en mode train (atelier).
func registerApps(r *rand.Rand, laptop, vps, pi *machine) {
	specs := map[*machine][]appSpec{
		laptop: {
			{"site", model.ViewRemote, model.AppRunning},
			{"Tango", model.ViewRemote, model.AppRunning},
			{"Clepsydre", model.ViewRemote, model.AppRunning},
			{"nuage", model.ViewRemote, model.AppRunning},
			{"planetaire", model.ViewRemote, model.AppStopped},
			{"radio-z", model.ViewRemote, model.AppRunning},
		},
		vps: {
			{"site", model.ViewRemote, model.AppRunning},
			{"tango", model.ViewRemote, model.AppRunning},
			{"n8n-flows", model.ViewRemote, model.AppRunning},
			{"atelier", model.ViewTrain, model.AppRunning},
			{"clepsydre", model.ViewRemote, model.AppStopped},
		},
		pi: {
			{"cadre-photo", model.ViewRemote, model.AppFailed},
		},
	}
	for _, m := range []*machine{laptop, vps, pi} {
		for _, sp := range specs[m] {
			p := m.project(sp.project)
			if p == nil || p.stack == nil {
				continue
			}
			key := strings.ToLower(p.name)
			p.app = key
			app := model.App{
				Key: key, Name: key, Project: p.path, CWD: p.path, Port: p.stack.port,
				Unit: "connex-" + key + ".service", View: sp.view,
			}
			if p.stack.subdir != "" {
				app.CWD = p.path + "/" + p.stack.subdir
			}
			m.snap.Apps = append(m.snap.Apps, app)
			switch sp.state {
			case model.AppRunning:
				if p.server == "" {
					startServer(m, p, r)
				}
				if l := m.listener(p.server); l != nil {
					m.app(key).Port = l.Port
				}
				m.setApp(key, model.AppRunning, p)
			default:
				if p.server != "" {
					m.removeListener(p.server)
					p.server = ""
				}
				m.setApp(key, sp.state, p)
			}
		}
	}
	// Un serveur oublié tient le port de planetaire : démarrer l'appli échoue
	// tant qu'on ne libère pas le port.
	if p := laptop.project("ml-notebooks"); p != nil {
		laptop.snap.Listeners = append(laptop.snap.Listeners,
			listener("127.0.0.1", 5000, true, laptop.uid, laptop.newPID(r), "python3", p.path))
	}
	// Une appli qui tourne mais répond mal.
	if a := laptop.app("tango"); a != nil {
		a.Health.HTTP = 500
	}
}

// setApp met l'appli dans l'état donné et recalcule sa santé d'après son port.
func (m *machine) setApp(key, state string, p *project) {
	a := m.app(key)
	if a == nil {
		return
	}
	a.State = state
	listening := p != nil && p.server != ""
	a.Health = model.AppHealth{
		PIDAlive:      state == model.AppRunning || state == model.AppStarting,
		PortListening: listening,
	}
	if state == model.AppRunning && listening {
		a.Health.HTTP = 200
	}
}

func (m *machine) app(key string) *model.App {
	for i := range m.snap.Apps {
		if m.snap.Apps[i].Key == key {
			return &m.snap.Apps[i]
		}
	}
	return nil
}

func (m *machine) appProject(key string) *project {
	for _, p := range m.projects {
		if p.app == key {
			return p
		}
	}
	return nil
}

func (m *machine) project(name string) *project {
	for _, p := range m.projects {
		if p.name == name {
			return p
		}
	}
	return nil
}

func (m *machine) listener(key string) *model.Listener {
	for i := range m.snap.Listeners {
		if m.snap.Listeners[i].Key == key {
			return &m.snap.Listeners[i]
		}
	}
	return nil
}

// --- vie des applis, en temps simulé

// scheduleApps prévoit les plantages et les réponses HTTP dégradées.
func (s *sim) scheduleApps(m *machine) {
	crash, hiccup := 900.0, 500.0
	if m.remote {
		crash, hiccup = 1800, 900
	}
	s.after(s.exp(crash), func() { s.appCrash(m, crash) })
	s.after(s.exp(hiccup), func() { s.appHiccup(m, hiccup) })
}

// appCrash : une appli plante ; systemd la relance (Restart=on-failure), ce qui
// échoue parfois : elle reste alors en échec.
func (s *sim) appCrash(m *machine, every float64) {
	defer s.after(s.exp(every), func() { s.appCrash(m, every) })
	key := s.pickApp(m, model.AppRunning, "crash")
	if key == "" {
		return
	}
	p := m.appProject(key)
	s.change(m, func() {
		if p.server != "" {
			m.removeListener(p.server)
			p.server = ""
		}
		m.setApp(key, model.AppFailed, p)
	})
	if s.r.IntN(4) == 0 {
		return // systemd abandonne
	}
	s.after(s.uniform(2, 6), func() {
		if a := m.app(key); a == nil || a.State != model.AppFailed {
			return
		}
		s.change(m, func() { m.setApp(key, model.AppStarting, p) })
		s.after(s.uniform(2, 5), func() {
			if a := m.app(key); a != nil && a.State == model.AppStarting {
				s.change(m, func() { s.appUp(m, key) })
			}
		})
	})
}

// appHiccup : une appli qui tourne répond en erreur un moment, puis se remet.
func (s *sim) appHiccup(m *machine, every float64) {
	defer s.after(s.exp(every), func() { s.appHiccup(m, every) })
	key := s.pickApp(m, model.AppRunning, "http")
	if key == "" {
		return
	}
	code := pickInt(s.r, []int{500, 502, 503, 404})
	s.change(m, func() { m.app(key).Health.HTTP = code })
	s.after(s.uniform(20, 120), func() {
		if a := m.app(key); a != nil && a.State == model.AppRunning && a.Health.HTTP == code {
			s.change(m, func() { a := m.app(key); a.Health.HTTP = 200 })
		}
	})
}

// appUp lance le serveur de l'appli sur son port réservé ; si le port est pris,
// l'appli passe en échec. Renvoie le programme qui tient le port, s'il y en a un.
func (s *sim) appUp(m *machine, key string) *model.Listener {
	a, p := m.app(key), m.appProject(key)
	if a == nil || p == nil {
		return nil
	}
	if l := m.holder(mustAddr(p.stack.addr), a.Port, p.server); l != nil {
		m.setApp(key, model.AppFailed, p)
		return l
	}
	if p.server == "" {
		startServer(m, p, s.r)
	}
	m.setApp(key, model.AppRunning, p)
	return nil
}

// holder renvoie le port en écoute qui empêche d'ouvrir a:port, hors except.
func (m *machine) holder(a netip.Addr, port int, except string) *model.Listener {
	for i := range m.snap.Listeners {
		l := &m.snap.Listeners[i]
		if l.Port != port || l.Key == except {
			continue
		}
		la := mustAddr(l.Addr)
		if la == a || la.IsUnspecified() || a.IsUnspecified() {
			return l
		}
	}
	return nil
}

// pickApp tire une appli dans l'état donné pour un incident de la sorte donnée.
// Une appli ne subit pas deux fois le même incident en un quart d'heure : la même
// panne répétée se verrait (§8).
func (s *sim) pickApp(m *machine, state, sort string) string {
	if m.incidents == nil {
		m.incidents = map[string]float64{}
	}
	var keys []string
	for _, a := range m.snap.Apps {
		last, ok := m.incidents[sort+"/"+a.Key]
		if a.State == state && (!ok || s.now-last > 900) {
			keys = append(keys, a.Key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	k := keys[s.r.IntN(len(keys))]
	m.incidents[sort+"/"+k] = s.now
	return k
}

func pickInt(r *rand.Rand, xs []int) int { return xs[r.IntN(len(xs))] }

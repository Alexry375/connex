// Package model définit les lignes JSON échangées entre le moteur et l'affichage
// (cahier des charges §4). C'est un contrat : un champ renommé ou retiré exige
// de passer à Schema = 2.
package model

import "time"

// Schema est la version du modèle portée par chaque ligne JSON.
const Schema = 1

// Machine décrit la machine relevée.
type Machine struct {
	Name        string `json:"name"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	User        string `json:"user"`
	TailscaleIP string `json:"tailscale_ip,omitempty"`
}

// Listener est un port TCP en écoute. Pour un processus d'un autre compte, seuls
// Port, Addr, Exposure, UID et Mine sont renseignés.
type Listener struct {
	Key         string `json:"key"`
	Port        int    `json:"port"`
	Addr        string `json:"addr"`
	Exposure    string `json:"exposure"`
	Mine        bool   `json:"mine"`
	UID         int    `json:"uid"`
	PID         int    `json:"pid,omitempty"`
	Process     string `json:"process,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	ProjectRoot string `json:"project_root,omitempty"`
}

// Worktree est un dossier de travail d'un dépôt autre que le dépôt lui-même.
type Worktree struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Detached bool   `json:"detached,omitempty"`
}

// Repo est un dépôt git trouvé sous une racine. Dirty vaut nil si l'état est inconnu.
type Repo struct {
	Key       string     `json:"key"`
	Path      string     `json:"path"`
	Branch    string     `json:"branch"`
	Detached  bool       `json:"detached,omitempty"`
	Dirty     *bool      `json:"dirty"`
	Worktrees []Worktree `json:"worktrees"`
}

// États possibles d'une session d'agent.
const (
	StateWorking = "working"
	StateWaiting = "waiting"
	StateIdle    = "idle"
	StateUnknown = "unknown"
)

// Agent est une session d'agent en cours (processus claude ou codex).
type Agent struct {
	Key   string `json:"key"`
	Tool  string `json:"tool"`
	PID   int    `json:"pid"`
	CWD   string `json:"cwd,omitempty"`
	State string `json:"state"`
}

// États d'une appli inscrite.
const (
	AppRunning  = "running"  // l'unité systemd tourne
	AppStarting = "starting" // lancée, pas encore prête
	AppStopped  = "stopped"  // arrêtée volontairement
	AppFailed   = "failed"   // arrêtée sur une erreur
)

// Façons de regarder une appli depuis le laptop (D1, D7).
const (
	ViewRemote = "remote" // par Tailscale, directement sur la machine
	ViewTrain  = "train"  // par le miroir apercu-vps
)

// App est une appli inscrite par `connex run` (lot L3), supervisée par une unité
// systemd --user. La commande qui la lance reste dans le registre de la machine :
// elle n'est jamais transmise (§6.3, D11).
type App struct {
	Key     string    `json:"key"`               // égal à Name : unique sur la machine
	Name    string    `json:"name"`              // [a-z0-9][a-z0-9-]*, au plus 40 caractères
	Project string    `json:"project,omitempty"` // racine du projet
	CWD     string    `json:"cwd,omitempty"`     // dossier de lancement
	Port    int       `json:"port,omitempty"`    // port réservé à l'appli, 0 si aucun
	Unit    string    `json:"unit"`              // unité systemd --user, par exemple connex-site.service
	State   string    `json:"state"`             // AppRunning, AppStarting, AppStopped ou AppFailed
	Health  AppHealth `json:"health"`
	View    string    `json:"view"` // ViewRemote ou ViewTrain
}

// AppHealth résume la santé d'une appli au dernier relevé.
type AppHealth struct {
	PIDAlive      bool `json:"pid_alive"`      // le processus principal de l'unité existe
	PortListening bool `json:"port_listening"` // son port est en écoute
	HTTP          int  `json:"http"`           // code HTTP de la dernière sonde ; 0 : pas de réponse ou pas de sonde
}

// Error est une erreur non fatale : le relevé est produit quand même.
type Error struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

// Snapshot est le relevé complet d'une machine (§4.1).
type Snapshot struct {
	Schema    int        `json:"schema"`
	Type      string     `json:"type"`
	TakenAt   string     `json:"taken_at"`
	Machine   Machine    `json:"machine"`
	Listeners []Listener `json:"listeners"`
	Repos     []Repo     `json:"repos"`
	Agents    []Agent    `json:"agents"`
	Apps      []App      `json:"apps"`
	Errors    []Error    `json:"errors"`
}

// NewSnapshot renvoie un relevé vide, aux listes non nulles (elles sortent en []).
func NewSnapshot(m Machine, at time.Time) Snapshot {
	return Snapshot{
		Schema:    Schema,
		Type:      "snapshot",
		TakenAt:   Timestamp(at),
		Machine:   m,
		Listeners: []Listener{},
		Repos:     []Repo{},
		Agents:    []Agent{},
		Apps:      []App{},
		Errors:    []Error{},
	}
}

// Timestamp formate une date comme dans tout le modèle : RFC 3339, UTC, à la seconde.
func Timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// Nature d'un élément dans un patch.
const (
	KindListener = "listener"
	KindRepo     = "repo"
	KindAgent    = "agent"
	KindApp      = "app"
)

// Opérations d'un changement.
const (
	OpAdd    = "add"
	OpUpdate = "update"
	OpRemove = "remove"
)

// Change est une modification d'un élément du relevé. Data est absent pour un retrait.
type Change struct {
	Op   string `json:"op"`
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Data any    `json:"data,omitempty"`
}

// Patch regroupe les changements observés à un instant (§4.2).
type Patch struct {
	Schema  int      `json:"schema"`
	Type    string   `json:"type"`
	Machine string   `json:"machine"`
	At      string   `json:"at"`
	Changes []Change `json:"changes"`
}

// NewPatch construit un patch pour la machine donnée.
func NewPatch(machine string, at time.Time, changes []Change) Patch {
	return Patch{Schema: Schema, Type: "patch", Machine: machine, At: Timestamp(at), Changes: changes}
}

// MachineStatus signale qu'une machine devient injoignable ou revient (§4.2).
type MachineStatus struct {
	Schema    int    `json:"schema"`
	Type      string `json:"type"`
	Machine   string `json:"machine"`
	Reachable bool   `json:"reachable"`
	Since     string `json:"since"`
	// Reason dit pourquoi la machine est injoignable, en une ligne lisible (par
	// exemple la dernière ligne d'erreur de ssh). Vide quand elle est joignable.
	Reason string `json:"reason,omitempty"`
}

// Opérations de requête : la liste est fermée (§4.3).
const (
	ReqStart    = "start"     // Target : nom de l'appli
	ReqStop     = "stop"      // Target : nom de l'appli
	ReqRestart  = "restart"   // Target : nom de l'appli
	ReqFreePort = "free_port" // Target : numéro de port, en texte
	ReqRefresh  = "refresh"   // Target : vide
)

// Request est une requête reçue sur l'entrée standard de stream (§4.3).
type Request struct {
	Schema int    `json:"schema"`
	Type   string `json:"type"`
	ID     string `json:"id"`
	Op     string `json:"op"`
	Target string `json:"target,omitempty"`
}

// Reply est la réponse à une requête.
type Reply struct {
	Schema int    `json:"schema"`
	Type   string `json:"type"`
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

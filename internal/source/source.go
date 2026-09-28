// Package source est le contrat entre l'affichage et ce qui produit les données
// (D8, D10). Une source livre des événements du modèle de schéma 1 (§4) et
// transmet des requêtes de la liste fermée (§4.3). Le simulateur, la machine
// locale et, au lot L2, les trois machines réunies par SSH sont des sources.
package source

import "github.com/Alexry375/connex/internal/model"

// Event est une ligne du flux, décodée. Exactement un des champs pointeurs est
// rempli. Dans un Patch, chaque Change.Data est typé : model.Listener,
// model.Repo, model.Agent ou model.App (absent pour un retrait).
type Event struct {
	Machine  string // la machine concernée, toujours rempli
	Snapshot *model.Snapshot
	Patch    *model.Patch
	Status   *model.MachineStatus
	Reply    *model.Reply // réponse à une requête envoyée par Send, même ID
}

// Source produit des événements et accepte des requêtes.
//
// Ordre garanti pour chaque machine : un Snapshot avant tout Patch ; après un
// Status injoignable, le retour de la machine arrive en Status joignable suivi
// d'un Snapshot neuf, qui remplace l'ancien.
type Source interface {
	// Events renvoie le canal des événements. Il est fermé quand la source
	// s'arrête (Close, ou fin du contexte donné à sa création).
	Events() <-chan Event
	// Send envoie une requête à une machine. Il ne bloque pas. La réponse arrive
	// plus tard dans Events, en Event.Reply avec le même ID ; une erreur
	// immédiate veut dire qu'aucune réponse ne viendra.
	Send(machine string, req model.Request) error
	// Close arrête la source et ce qu'elle a lancé.
	Close() error
}

// NewRequest construit une requête de la liste fermée (§4.3). id doit être
// unique pour la source ; target est un nom d'appli, un numéro de port, ou vide.
func NewRequest(id, op, target string) model.Request {
	return model.Request{Schema: model.Schema, Type: "request", ID: id, Op: op, Target: target}
}

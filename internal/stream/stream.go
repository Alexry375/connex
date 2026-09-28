// Package stream tient à jour le relevé de la machine sous forme de lignes JSON :
// un snapshot, puis des patchs quand quelque chose change (§4.2). Il lit des
// requêtes sur son entrée, parmi une liste fermée d'opérations (§4.3), et
// s'arrête quand cette entrée se ferme — c'est-à-dire quand la session SSH tombe.
// Il n'ouvre aucun port ni socket.
package stream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

// Collector fait un relevé de la machine. full demande de tout relire.
type Collector interface {
	Collect(ctx context.Context, full bool) model.Snapshot
}

// maxRequest borne la taille d'une ligne de requête.
const maxRequest = 64 * 1024

// drainGrace est le temps laissé au travail en cours (premier relevé, refresh
// déjà reçu) quand l'entrée se ferme ; ensuite, tout est annulé. Il reste ainsi
// de la marge sous les 2 s d'arrêt promises.
const drainGrace = time.Second

// maxPending borne les requêtes lues mais pas encore traitées. Au-delà, le
// lecteur attend : un client normal n'en envoie qu'une de temps en temps.
const maxPending = 256

// stopGrace est le temps laissé, une fois l'arrêt décidé, aux actions en cours
// pour rendre leur résultat : drainGrace + stopGrace reste sous les 2 s promises.
const stopGrace = 500 * time.Millisecond

// Opérations d'applis du lot L3 : exécutées par un Actions, refusées sans lui.
var laterOps = map[string]bool{"start": true, "stop": true, "restart": true, "free_port": true}

// Réponses des actions que l'arrêt empêche de finir.
const (
	msgCancelled   = "action annulée : stream s'arrête"
	msgInterrupted = "action interrompue : stream s'arrête ; son effet est incertain"
)

// Actions exécute les opérations d'applis (start, stop, restart, free_port).
// Rien d'autre n'est jamais exécuté à partir d'une requête.
type Actions interface {
	// Validate contrôle la cible ; il renvoie un refus, vide si la requête est
	// recevable. Il est appelé avant toute autre chose.
	Validate(op, target string) string
	// Key regroupe les requêtes qui ne doivent pas se chevaucher (même appli).
	Key(op, target string) string
	// Do exécute l'opération et renvoie la raison d'un échec.
	Do(ctx context.Context, op, target string) error
}

// pending est une requête lue. refusal est vide pour une requête acceptée :
// un refresh, ou une action validée par Actions.
type pending struct {
	id, op, target string
	refusal        string
}

// Run écrit le relevé puis les patchs sur out, toutes les interval, jusqu'à la
// fermeture de in ou l'annulation de ctx. Toute requête lue reçoit une réponse,
// sauf si l'arrêt survient avant le premier relevé. Il ne renvoie une erreur
// que si out devient inutilisable. Les opérations d'applis sont refusées.
func Run(ctx context.Context, c Collector, in io.Reader, out io.Writer, interval time.Duration) error {
	return RunWith(ctx, c, in, out, interval, nil)
}

// RunWith est Run, avec les opérations d'applis confiées à acts (lot L3). Une
// action s'exécute à part : elle ne bloque ni la lecture des requêtes ni les
// relevés ; deux actions de même clé s'enchaînent. Sa réponse est suivie
// aussitôt d'un nouveau relevé, donc du patch qui montre son effet. Elle
// compte dans maxPending jusqu'à sa réponse. À l'arrêt, une action pas encore
// commencée reçoit msgCancelled ; une action en cours voit son contexte
// annulé et reçoit son résultat s'il arrive dans stopGrace, msgInterrupted
// sinon.
func RunWith(ctx context.Context, c Collector, in io.Reader, out io.Writer, interval time.Duration, acts Actions) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &lineWriter{w: out}

	// Le lecteur ne fait que lire et mettre en file : il voit donc la fin de
	// l'entrée même pendant un relevé lent, et arme aussitôt l'arrêt. Il n'écrit
	// jamais lui-même, pour que le snapshot reste la première ligne.
	queue := make(chan pending, maxPending)
	go func() {
		readRequests(ctx, in, acts, queue)
		time.AfterFunc(drainGrace, cancel)
		close(queue)
	}()
	q := &actionQueue{acts: acts, done: make(chan model.Reply), quit: make(chan struct{})}
	defer close(q.quit)
	var open []string // actions acceptées, sans réponse écrite (dans l'ordre)
	answered := func(id string) {
		if i := slices.Index(open, id); i >= 0 {
			open = slices.Delete(open, i, i+1)
		}
	}

	prev := c.Collect(ctx, true)
	if ctx.Err() != nil {
		return nil
	}
	if err := w.write(prev); err != nil {
		return err
	}

	// update relit la machine et écrit le patch s'il y a du changement. Un relevé
	// interrompu par l'arrêt est incomplet : on ne le compare pas.
	update := func(full bool) error {
		next := c.Collect(ctx, full)
		if ctx.Err() != nil {
			return nil
		}
		changes := model.Diff(prev, next)
		prev = next
		if len(changes) == 0 {
			return nil
		}
		return w.write(model.NewPatch(next.Machine.Name, time.Now(), changes))
	}
	// answer répond à un lot de requêtes : les refus tels quels, les actions en
	// les confiant à leur file, et tous les refresh par un seul relevé complet.
	// Un refresh ou une action que l'arrêt devance reçoit un refus plutôt que rien.
	answer := func(batch []pending) error {
		var refreshes []string
		for _, p := range batch {
			switch {
			case p.refusal != "":
				if err := w.write(reply(p.id, p.refusal)); err != nil {
					return err
				}
			case laterOps[p.op] && ctx.Err() != nil:
				if err := w.write(reply(p.id, msgCancelled)); err != nil {
					return err
				}
			case laterOps[p.op]:
				open = append(open, p.id)
				q.push(ctx, p)
			default:
				refreshes = append(refreshes, p.id)
			}
		}
		if len(refreshes) == 0 {
			return nil
		}
		if ctx.Err() == nil {
			if err := update(true); err != nil {
				return err
			}
		}
		msg := ""
		if ctx.Err() != nil {
			msg = "relevé interrompu : stream s'arrête"
		}
		for _, id := range refreshes {
			if err := w.write(reply(id, msg)); err != nil {
				return err
			}
		}
		return nil
	}

	// finish répond, à l'arrêt, aux actions sans réponse : annulées si elles
	// n'ont pas commencé, sinon leur résultat s'il arrive dans stopGrace.
	finish := func() error {
		for _, id := range q.cancelQueued() {
			answered(id)
			if err := w.write(reply(id, msgCancelled)); err != nil {
				return err
			}
		}
		grace := time.NewTimer(stopGrace)
		defer grace.Stop()
		for len(open) > 0 {
			select {
			case r := <-q.done:
				answered(r.ID)
				if err := w.write(r); err != nil {
					return err
				}
			case <-grace.C:
				for _, id := range open {
					if err := w.write(reply(id, msgInterrupted)); err != nil {
						return err
					}
				}
				return nil
			}
		}
		return nil
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()
	input := queue // nil une fois l'entrée finie
	for {
		// La borne maxPending vaut aussi pour les actions sans réponse : au-delà,
		// la file n'est plus lue et le lecteur attend.
		in := input
		if len(open) >= maxPending {
			in = nil
		}
		select {
		case <-ctx.Done():
			batch, _ := take(queue, nil)
			if err := answer(batch); err != nil {
				return err
			}
			return finish()
		case <-timer.C:
			if err := update(false); err != nil {
				return err
			}
			timer.Reset(interval)
		case r := <-q.done:
			answered(r.ID)
			if err := w.write(r); err != nil {
				return err
			}
			if err := update(false); err != nil {
				return err
			}
			if input == nil && len(open) == 0 {
				return nil
			}
		case p, ok := <-in:
			closed := !ok
			if ok {
				var batch []pending
				batch, closed = take(queue, []pending{p})
				if err := answer(batch); err != nil {
					return err
				}
			}
			if closed {
				// Fin de l'entrée : tout ce qui avait été lu est traité, sauf
				// les actions en cours, attendues jusqu'à l'arrêt (drainGrace).
				if len(open) == 0 {
					return nil
				}
				input = nil
			}
		}
	}
}

// actionQueue exécute les actions : une file par clé, dans l'ordre d'arrivée,
// chaque file à part des autres et de la boucle. Les résultats reviennent à la
// boucle par done ; quit libère les files quand RunWith rend la main.
type actionQueue struct {
	acts   Actions
	done   chan model.Reply
	quit   chan struct{}
	mu     sync.Mutex
	busy   map[string][]pending // actions en attente de chaque clé en cours
	closed bool                 // arrêt : plus rien ne commence
}

// push ajoute une action à la file de sa clé, et lance la file si elle dormait.
func (q *actionQueue) push(ctx context.Context, p pending) {
	key := q.acts.Key(p.op, p.target)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.busy == nil {
		q.busy = map[string][]pending{}
	}
	if waiting, ok := q.busy[key]; ok {
		q.busy[key] = append(waiting, p)
		return
	}
	q.busy[key] = nil
	go q.drain(ctx, key, p)
}

func (q *actionQueue) drain(ctx context.Context, key string, p pending) {
	for {
		msg := ""
		if err := q.acts.Do(ctx, p.op, p.target); err != nil {
			msg = err.Error()
		}
		select {
		case q.done <- reply(p.id, msg):
		case <-q.quit:
			return
		}
		q.mu.Lock()
		waiting := q.busy[key]
		switch {
		case len(waiting) == 0 || q.closed:
			delete(q.busy, key)
			q.mu.Unlock()
			return
		case ctx.Err() != nil:
			// Arrêt : la suite n'est pas commencée, cancelQueued la reprend.
			q.mu.Unlock()
			return
		}
		p, q.busy[key] = waiting[0], waiting[1:]
		q.mu.Unlock()
	}
}

// cancelQueued arrête les files : les actions pas encore commencées ne le
// seront jamais. Il renvoie leurs identifiants.
func (q *actionQueue) cancelQueued() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	var ids []string
	for key, waiting := range q.busy {
		for _, p := range waiting {
			ids = append(ids, p.id)
		}
		q.busy[key] = nil
	}
	return ids
}

// take ajoute à batch les requêtes déjà en file, sans attendre. closed dit si
// la file est fermée : l'entrée est finie.
func take(queue <-chan pending, batch []pending) ([]pending, bool) {
	for {
		select {
		case p, ok := <-queue:
			if !ok {
				return batch, true
			}
			batch = append(batch, p)
		default:
			return batch, false
		}
	}
}

// readRequests lit les requêtes ligne à ligne et les met en file, avec leur
// refus s'il y a lieu : seules refresh et, avec acts, les opérations d'applis
// dont la cible est valide sont acceptées. Rien n'est exécuté ici. Il rend la
// main à la fin de l'entrée.
func readRequests(ctx context.Context, in io.Reader, acts Actions, queue chan<- pending) {
	br := bufio.NewReaderSize(in, 4096)
	for {
		line, err := readLine(br, maxRequest)
		if len(bytes.TrimSpace(line)) > 0 || errors.Is(err, errTooLong) {
			req, refusal := parseRequestWith(line, err, acts)
			select {
			case queue <- pending{id: req.ID, op: req.Op, target: req.Target, refusal: refusal}:
			case <-ctx.Done():
				return
			}
		}
		if err != nil && !errors.Is(err, errTooLong) {
			return // fin de l'entrée : la session est finie
		}
	}
}

// parseRequest valide une requête. Il renvoie un message de refus, vide si la
// requête est acceptée.
func parseRequest(line []byte, readErr error) (model.Request, string) {
	return parseRequestWith(line, readErr, nil)
}

// parseRequestWith est parseRequest ; sans acts, les opérations d'applis sont
// refusées.
func parseRequestWith(line []byte, readErr error, acts Actions) (model.Request, string) {
	var req model.Request
	if errors.Is(readErr, errTooLong) {
		return req, "requête trop longue"
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return model.Request{}, "requête illisible : JSON attendu"
	}
	switch {
	case req.Schema != model.Schema:
		return req, "schema non pris en charge : 1 attendu"
	case req.Type != "request":
		return req, `type "request" attendu`
	case req.Op == "refresh":
		return req, ""
	case laterOps[req.Op] && acts != nil:
		return req, acts.Validate(req.Op, req.Target)
	case laterOps[req.Op]:
		return req, "opération « " + req.Op + " » pas encore disponible : elle viendra au lot L3"
	case acts != nil:
		return req, "opération inconnue ; permises : start, stop, restart, free_port, refresh"
	default:
		return req, "opération inconnue ; permises : refresh"
	}
}

func reply(id, errMsg string) model.Reply {
	return model.Reply{Schema: model.Schema, Type: "reply", ID: id, OK: errMsg == "", Error: errMsg}
}

var errTooLong = errors.New("ligne trop longue")

// readLine lit une ligne sans son \n. Une ligne de plus de max octets est
// consommée jusqu'au bout et signalée par errTooLong.
func readLine(br *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > max+1 {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		if tooLong && err == nil {
			return nil, errTooLong
		}
		return line, err
	}
}

// lineWriter écrit une valeur JSON par ligne, d'un seul appel à Write.
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineWriter) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.w.Write(append(b, '\n'))
	return err
}

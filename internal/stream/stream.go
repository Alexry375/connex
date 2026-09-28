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

// Opérations prévues au lot L3 : reconnues, mais refusées pour l'instant.
var laterOps = map[string]bool{"start": true, "stop": true, "restart": true, "free_port": true}

// Run écrit le relevé puis les patchs sur out, toutes les interval, jusqu'à la
// fermeture de in ou l'annulation de ctx. Il ne renvoie une erreur que si out
// devient inutilisable.
func Run(ctx context.Context, c Collector, in io.Reader, out io.Writer, interval time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &lineWriter{w: out}

	reqs := make(chan model.Request)
	replies := make(chan model.Reply)
	go readRequests(ctx, in, reqs, replies, cancel)

	prev := c.Collect(ctx, true)
	if ctx.Err() != nil {
		return nil
	}
	if err := w.write(prev); err != nil {
		return err
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()
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
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			if err := update(false); err != nil {
				return err
			}
			timer.Reset(interval)
		case r := <-replies:
			if err := w.write(r); err != nil {
				return err
			}
		case req := <-reqs: // seule l'opération refresh arrive ici
			if err := update(true); err != nil {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			if err := w.write(reply(req.ID, "")); err != nil {
				return err
			}
		}
	}
}

// readRequests lit les requêtes ligne à ligne. Les requêtes valides d'opération
// refresh partent vers reqs ; toutes les autres reçoivent aussitôt un refus sur
// replies. Rien de ce qui arrive ici n'est jamais exécuté. À la fin de l'entrée,
// cancel arrête tout.
func readRequests(ctx context.Context, in io.Reader, reqs chan<- model.Request, replies chan<- model.Reply, cancel func()) {
	defer cancel()
	br := bufio.NewReaderSize(in, 4096)
	for {
		line, err := readLine(br, maxRequest)
		if len(bytes.TrimSpace(line)) > 0 || errors.Is(err, errTooLong) {
			req, refusal := parseRequest(line, err)
			var sent bool
			if refusal == "" {
				sent = send(ctx, reqs, req)
			} else {
				sent = send(ctx, replies, reply(req.ID, refusal))
			}
			if !sent {
				return
			}
		}
		if err != nil && !errors.Is(err, errTooLong) {
			return // fin de l'entrée : la session est finie
		}
	}
}

func send[T any](ctx context.Context, ch chan<- T, v T) bool {
	select {
	case ch <- v:
		return true
	case <-ctx.Done():
		return false
	}
}

// parseRequest valide une requête. Il renvoie un message de refus, vide si la
// requête est acceptée.
func parseRequest(line []byte, readErr error) (model.Request, string) {
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
	case laterOps[req.Op]:
		return req, "opération « " + req.Op + " » pas encore disponible : elle viendra au lot L3"
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

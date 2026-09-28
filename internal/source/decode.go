package source

import (
	"encoding/json"
	"fmt"

	"github.com/Alexry375/connex/internal/model"
)

// Decode lit une ligne JSON de schéma 1. machine sert pour les réponses, qui ne
// portent pas de nom de machine ; les autres lignes portent le leur.
func Decode(line []byte, machine string) (Event, error) {
	var head struct {
		Schema int    `json:"schema"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return Event{}, fmt.Errorf("ligne illisible : %w", err)
	}
	if head.Schema != model.Schema {
		return Event{}, fmt.Errorf("schéma %d inconnu (attendu : %d)", head.Schema, model.Schema)
	}
	switch head.Type {
	case "snapshot":
		var s model.Snapshot
		if err := json.Unmarshal(line, &s); err != nil {
			return Event{}, fmt.Errorf("relevé illisible : %w", err)
		}
		return Event{Machine: s.Machine.Name, Snapshot: &s}, nil
	case "patch":
		p, err := decodePatch(line)
		if err != nil {
			return Event{}, err
		}
		return Event{Machine: p.Machine, Patch: &p}, nil
	case "machine_status":
		var st model.MachineStatus
		if err := json.Unmarshal(line, &st); err != nil {
			return Event{}, fmt.Errorf("état de machine illisible : %w", err)
		}
		return Event{Machine: st.Machine, Status: &st}, nil
	case "reply":
		var r model.Reply
		if err := json.Unmarshal(line, &r); err != nil {
			return Event{}, fmt.Errorf("réponse illisible : %w", err)
		}
		return Event{Machine: machine, Reply: &r}, nil
	}
	return Event{}, fmt.Errorf("type de ligne inconnu : %q", head.Type)
}

func decodePatch(line []byte) (model.Patch, error) {
	var raw struct {
		model.Patch
		Changes []struct {
			Op   string          `json:"op"`
			Kind string          `json:"kind"`
			Key  string          `json:"key"`
			Data json.RawMessage `json:"data"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return model.Patch{}, fmt.Errorf("patch illisible : %w", err)
	}
	p := raw.Patch
	p.Changes = make([]model.Change, 0, len(raw.Changes))
	for _, c := range raw.Changes {
		ch := model.Change{Op: c.Op, Kind: c.Kind, Key: c.Key}
		if c.Op != model.OpRemove {
			v, err := decodeData(c.Kind, c.Data)
			if err != nil {
				return model.Patch{}, fmt.Errorf("patch : %s %q : %w", c.Kind, c.Key, err)
			}
			ch.Data = v
		}
		p.Changes = append(p.Changes, ch)
	}
	return p, nil
}

func decodeData(kind string, data json.RawMessage) (any, error) {
	switch kind {
	case model.KindListener:
		return unmarshal[model.Listener](data)
	case model.KindRepo:
		return unmarshal[model.Repo](data)
	case model.KindAgent:
		return unmarshal[model.Agent](data)
	case model.KindApp:
		return unmarshal[model.App](data)
	}
	return nil, fmt.Errorf("nature inconnue : %q", kind)
}

func unmarshal[T any](data json.RawMessage) (T, error) {
	var v T
	if len(data) == 0 {
		return v, fmt.Errorf("donnée absente")
	}
	err := json.Unmarshal(data, &v)
	return v, err
}

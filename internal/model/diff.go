package model

import (
	"bytes"
	"encoding/json"
)

// Diff renvoie les changements qui mènent du relevé prev au relevé next, dans
// l'ordre : ports, dépôts, agents, applis ; dans chaque nature, ajouts et mises à jour
// dans l'ordre de next, puis retraits dans l'ordre de prev. Deux relevés égaux
// donnent une liste vide.
func Diff(prev, next Snapshot) []Change {
	var out []Change
	out = diffKind(out, KindListener, keyed(prev.Listeners, func(l Listener) string { return l.Key }), keyed(next.Listeners, func(l Listener) string { return l.Key }))
	out = diffKind(out, KindRepo, keyed(prev.Repos, func(r Repo) string { return r.Key }), keyed(next.Repos, func(r Repo) string { return r.Key }))
	out = diffKind(out, KindAgent, keyed(prev.Agents, func(a Agent) string { return a.Key }), keyed(next.Agents, func(a Agent) string { return a.Key }))
	out = diffKind(out, KindApp, keyed(prev.Apps, func(a App) string { return a.Key }), keyed(next.Apps, func(a App) string { return a.Key }))
	return out
}

type item struct {
	key  string
	data any
	raw  []byte
}

func keyed[T any](xs []T, key func(T) string) []item {
	out := make([]item, 0, len(xs))
	for _, x := range xs {
		raw, _ := json.Marshal(x)
		out = append(out, item{key: key(x), data: x, raw: raw})
	}
	return out
}

func diffKind(out []Change, kind string, prev, next []item) []Change {
	before := make(map[string][]byte, len(prev))
	for _, it := range prev {
		before[it.key] = it.raw
	}
	seen := make(map[string]bool, len(next))
	for _, it := range next {
		seen[it.key] = true
		old, ok := before[it.key]
		switch {
		case !ok:
			out = append(out, Change{Op: OpAdd, Kind: kind, Key: it.key, Data: it.data})
		case !bytes.Equal(old, it.raw):
			out = append(out, Change{Op: OpUpdate, Kind: kind, Key: it.key, Data: it.data})
		}
	}
	for _, it := range prev {
		if !seen[it.key] {
			out = append(out, Change{Op: OpRemove, Kind: kind, Key: it.key})
		}
	}
	return out
}

// Apply applique des changements à une copie du relevé et la renvoie. Les données
// doivent être typées (Listener, Repo, Agent, App) : c'est ce que produisent Diff
// et source.Decode. Une donnée de type inattendu est ignorée.
func Apply(s Snapshot, changes []Change) Snapshot {
	s.Listeners = append([]Listener{}, s.Listeners...)
	s.Repos = append([]Repo{}, s.Repos...)
	s.Agents = append([]Agent{}, s.Agents...)
	s.Apps = append([]App{}, s.Apps...)
	for _, c := range changes {
		switch c.Kind {
		case KindListener:
			s.Listeners = applyOne(s.Listeners, c, func(l Listener) string { return l.Key })
		case KindRepo:
			s.Repos = applyOne(s.Repos, c, func(r Repo) string { return r.Key })
		case KindAgent:
			s.Agents = applyOne(s.Agents, c, func(a Agent) string { return a.Key })
		case KindApp:
			s.Apps = applyOne(s.Apps, c, func(a App) string { return a.Key })
		}
	}
	return s
}

func applyOne[T any](xs []T, c Change, key func(T) string) []T {
	idx := -1
	for i, x := range xs {
		if key(x) == c.Key {
			idx = i
			break
		}
	}
	if c.Op == OpRemove {
		if idx >= 0 {
			xs = append(xs[:idx], xs[idx+1:]...)
		}
		return xs
	}
	v, ok := c.Data.(T)
	if !ok {
		return xs
	}
	if idx >= 0 {
		xs[idx] = v
		return xs
	}
	return append(xs, v)
}

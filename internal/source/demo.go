package source

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/Alexry375/connex/internal/demo"
	"github.com/Alexry375/connex/internal/model"
)

// DemoOptions règle le simulateur.
type DemoOptions struct {
	Seed  int64   // même graine, même histoire ; 0 est une graine comme une autre
	Speed float64 // secondes simulées par seconde réelle ; 0 fige le monde
}

// Demo est le simulateur vu comme une source : trois machines inventées mais
// riches (§8), qui vivent et répondent aux requêtes avec des délais réalistes.
// Ses lignes passent par JSON puis Decode, comme celles d'une vraie machine.
type Demo struct {
	live   *demo.Live
	events chan Event
	cancel context.CancelFunc
	once   sync.Once
}

// NewDemo démarre le simulateur. Il s'arrête avec ctx ou Close.
func NewDemo(ctx context.Context, o DemoOptions) *Demo {
	ctx, cancel := context.WithCancel(ctx)
	d := &Demo{live: demo.StartLive(ctx, o.Seed, o.Speed), events: make(chan Event, 64), cancel: cancel}
	go d.pump(ctx)
	return d
}

func (d *Demo) pump(ctx context.Context) {
	defer close(d.events)
	for line := range d.live.Lines() {
		b, err := json.Marshal(line.Value)
		if err != nil {
			continue
		}
		ev, err := Decode(b, line.Machine)
		if err != nil {
			continue
		}
		select {
		case d.events <- ev:
		case <-ctx.Done():
			return
		}
	}
}

// Events implémente Source.
func (d *Demo) Events() <-chan Event { return d.events }

// Send implémente Source.
func (d *Demo) Send(machine string, req model.Request) error { return d.live.Send(machine, req) }

// Close implémente Source.
func (d *Demo) Close() error {
	d.once.Do(d.cancel)
	return nil
}

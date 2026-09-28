package source

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Alexry375/connex/internal/model"
)

func TestDecodeTypesPatchData(t *testing.T) {
	d := true
	p := model.NewPatch("m", time.Now(), []model.Change{
		{Op: model.OpAdd, Kind: model.KindListener, Key: "l", Data: model.Listener{Key: "l", Port: 1}},
		{Op: model.OpUpdate, Kind: model.KindRepo, Key: "r", Data: model.Repo{Key: "r", Dirty: &d}},
		{Op: model.OpAdd, Kind: model.KindAgent, Key: "a", Data: model.Agent{Key: "a", State: model.StateWaiting}},
		{Op: model.OpUpdate, Kind: model.KindApp, Key: "x", Data: model.App{Key: "x", State: model.AppFailed}},
		{Op: model.OpRemove, Kind: model.KindListener, Key: "old"},
	})
	b, _ := json.Marshal(p)
	ev, err := Decode(b, "")
	if err != nil || ev.Patch == nil || ev.Machine != "m" {
		t.Fatalf("%v %+v", err, ev)
	}
	c := ev.Patch.Changes
	if _, ok := c[0].Data.(model.Listener); !ok {
		t.Errorf("listener : %T", c[0].Data)
	}
	if r, ok := c[1].Data.(model.Repo); !ok || r.Dirty == nil || !*r.Dirty {
		t.Errorf("repo : %#v", c[1].Data)
	}
	if a, ok := c[2].Data.(model.Agent); !ok || a.State != model.StateWaiting {
		t.Errorf("agent : %#v", c[2].Data)
	}
	if a, ok := c[3].Data.(model.App); !ok || a.State != model.AppFailed {
		t.Errorf("app : %#v", c[3].Data)
	}
	if c[4].Data != nil {
		t.Errorf("retrait avec donnée : %#v", c[4].Data)
	}
}

func TestDecodeReplyTakesMachineFromCaller(t *testing.T) {
	ev, err := Decode([]byte(`{"schema":1,"type":"reply","id":"r","ok":false,"error":"non"}`), "vps")
	if err != nil || ev.Reply == nil || ev.Machine != "vps" || ev.Reply.Error != "non" {
		t.Fatalf("%v %+v", err, ev)
	}
}

func TestDecodeRejects(t *testing.T) {
	for _, line := range []string{
		`{"schema":2,"type":"snapshot"}`,
		`{"schema":1,"type":"autre"}`,
		`pas du json`,
		`{"schema":1,"type":"patch","machine":"m","changes":[{"op":"add","kind":"listener","key":"k"}]}`,
		`{"schema":1,"type":"patch","machine":"m","changes":[{"op":"add","kind":"bizarre","key":"k","data":{}}]}`,
	} {
		if _, err := Decode([]byte(line), "m"); err == nil {
			t.Errorf("accepté : %s", line)
		}
	}
}

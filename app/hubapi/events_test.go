package hubapi

import (
	"encoding/json"
	"testing"
)

func TestDecode(t *testing.T) {
	req := `{"id":"r1","client":"c","server":"box","target":"u@h:22","command":"ls","description":"d","sudo":true,"timeoutSec":60,"receivedAt":"2026-10-15T01:02:03Z","stdin":"x"}`
	ev, ok := Decode("pending", json.RawMessage(`{"request":`+req+`}`))
	p, _ := ev.(PendingEvent)
	if !ok || p.Request.ID != "r1" || !p.Request.Sudo || p.Request.Stdin != "x" || p.Request.ReceivedAt.IsZero() {
		t.Fatalf("pending: %+v %v", ev, ok)
	}
	ev, ok = Decode("decided", json.RawMessage(`{"request":`+req+`,"decision":{"outcome":"expired","reason":"approval timed out"}}`))
	d, _ := ev.(DecidedEvent)
	if !ok || d.Request.ID != "r1" || d.Decision.Outcome != "expired" {
		t.Fatalf("decided: %+v", ev)
	}
	ev, ok = Decode("locked", json.RawMessage(`{"reason":"idle"}`))
	if l, _ := ev.(LockedEvent); !ok || l.Reason != "idle" {
		t.Fatalf("locked: %+v", ev)
	}
	ev, ok = Decode("autoAllow.off", json.RawMessage(`{"server":"box","reason":"locked"}`))
	if o, _ := ev.(AutoOffEvent); !ok || o.Server != "box" || o.Reason != "locked" {
		t.Fatalf("off: %+v", ev)
	}
	ev, ok = Decode("autoAllow.ran", json.RawMessage(`{"server":"box","command":"ls","description":"","exitCode":0,"time":"2026-10-15T01:02:03Z","stdinBytes":8}`))
	r, _ := ev.(AutoRanEvent)
	if !ok || r.ExitCode == nil || *r.ExitCode != 0 || r.StdinBytes != 8 {
		t.Fatalf("ran: %+v", ev)
	}
	for _, m := range []string{"audit.appended", "term.data", "tunnels.state", "bogus"} {
		if _, ok := Decode(m, json.RawMessage(`{}`)); ok {
			t.Errorf("%s decoded", m)
		}
	}
	if _, ok := Decode("pending", json.RawMessage(`not json`)); ok {
		t.Error("malformed params decoded")
	}
}

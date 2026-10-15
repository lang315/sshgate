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

func TestDecodeFiles(t *testing.T) {
	ev, ok := Decode("files.planned", json.RawMessage(`{"id":"f1","files":2,"dirs":1,"links":0,"bytes":9,"conflicts":{"count":2,"sample":["proj/a.txt"]},"errorCount":1,"errors":["x: bad"]}`))
	p, _ := ev.(FilesPlanned)
	if !ok || p.ID != "f1" || p.Files != 2 || p.Dirs != 1 || p.Bytes != 9 || p.Conflicts.Count != 2 || p.Conflicts.Sample[0] != "proj/a.txt" || p.ErrorCount != 1 {
		t.Fatalf("planned: %+v %v", ev, ok)
	}
	ev, ok = Decode("files.progress", json.RawMessage(`{"id":"f1","file":"proj/a.txt","done":5,"total":9}`))
	if g, _ := ev.(FilesProgress); !ok || g.File != "proj/a.txt" || g.Done != 5 || g.Total != 9 {
		t.Fatalf("progress: %+v", ev)
	}
	ev, ok = Decode("files.done", json.RawMessage(`{"id":"f1","op":"upload","copied":2,"skipped":1,"deleted":0,"bytes":9,"errorCount":0,"errors":[],"cancelled":true,"reason":"server changed"}`))
	if d, _ := ev.(FilesDone); !ok || d.Op != "upload" || d.Copied != 2 || d.Skipped != 1 || !d.Cancelled || d.Reason != "server changed" {
		t.Fatalf("done: %+v", ev)
	}
}

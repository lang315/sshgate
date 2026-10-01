package hub

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
)

type auditPage struct {
	Records []struct {
		Seq    int            `json:"seq"`
		Record map[string]any `json:"record"`
	} `json:"records"`
	Next    int    `json:"next"`
	Skipped int    `json:"skipped"`
	Path    string `json:"path"`
}

// waitAuditSink waits for a UI door to install its audit sink: ServeUIDoor
// runs in a goroutine, and a record written before that is dropped.
func waitAuditSink(t *testing.T, h *Hub) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.auditSink.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatal("UI door never installed its audit sink")
		}
		time.Sleep(time.Millisecond)
	}
}

// waitAudit returns the next audit.appended whose record has kind (exec
// records have none), skipping every other notification.
func waitAudit(t *testing.T, notes chan note, kind string) (int, map[string]any) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case n := <-notes:
			if n.method != "audit.appended" {
				continue
			}
			var p struct {
				Seq    int            `json:"seq"`
				Record map[string]any `json:"record"`
			}
			if err := json.Unmarshal(n.params, &p); err != nil {
				t.Fatal(err)
			}
			if k, _ := p.Record["kind"].(string); cmp.Or(k, "exec") == kind {
				return p.Seq, p.Record
			}
		case <-deadline:
			t.Fatalf("no audit.appended of kind %s", kind)
		}
	}
}

func TestAuditReadLockedAndCountsAsActivity(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: -1})
	c, _ := startUIRaw(t, h)
	ctx := context.Background()
	h.mu.Lock()
	h.lastActivity = time.Now().Add(-time.Hour)
	h.mu.Unlock()
	if err := c.Call(ctx, "audit.read", map[string]any{}, nil); err == nil || err.Error() != ErrLocked.Error() {
		t.Fatalf("locked: got %v, want %v", err, ErrLocked)
	}
	h.mu.Lock()
	quiet := time.Since(h.lastActivity)
	h.mu.Unlock()
	if quiet > time.Minute {
		t.Fatal("audit.read did not count as UI activity")
	}
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	var page auditPage
	if err := c.Call(ctx, "audit.read", map[string]any{}, &page); err != nil {
		t.Fatal(err)
	}
	if page.Records == nil || len(page.Records) != 0 || filepath.Base(page.Path) != "audit.jsonl" {
		t.Fatalf("%+v", page)
	}
}

func TestAuditReadStrictParams(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	ctx := context.Background()
	for _, p := range []map[string]any{
		{"limit": 501}, {"limit": -1}, {"before": -1}, {"limit": "5"},
		{"kinds": []string{"bogus"}}, {"outcomes": []string{"sent_to_tab"}},
		{"extra": 1}, {"Limit": 5},
	} {
		err := c.Call(ctx, "audit.read", p, nil)
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != -32602 {
			t.Fatalf("%v: want -32602, got %v", p, err)
		}
	}
	all := map[string]any{"limit": 500, "before": 1, "server": "vis", "text": "x",
		"kinds":    []string{"exec", "config", "file", "tunnel"},
		"outcomes": []string{"allowed", "auto", "denied", "expired", "cancelled", "error"}}
	if err := c.Call(ctx, "audit.read", all, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAuditReadReturnsRecordsNewestFirst(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	ctx := context.Background()
	go decideFirst(t, h.Broker(), broker.Decision{Outcome: broker.Denied, Reason: "no"})
	if _, err := h.Exec(ctx, ExecRequest{Server: "vis", Command: "rm -rf /tmp/x", Description: "clean up"}); err == nil {
		t.Fatal("want denied")
	}
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	var first auditPage
	if err := c.Call(ctx, "audit.read", map[string]any{"limit": 1}, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Records) != 1 || first.Records[0].Seq != 2 || first.Records[0].Record["action"] != "autoAllowOn" || first.Next != 2 {
		t.Fatalf("page 1: %+v", first)
	}
	var older auditPage
	if err := c.Call(ctx, "audit.read", map[string]any{"before": first.Next, "outcomes": []string{"denied"}}, &older); err != nil {
		t.Fatal(err)
	}
	if len(older.Records) != 1 || older.Records[0].Record["command"] != "rm -rf /tmp/x" ||
		older.Records[0].Record["description"] != "clean up" || older.Next != 0 {
		t.Fatalf("page 2: %+v", older)
	}
}

func TestAuditAppendedExecAndConfig(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	_, _, notes := startTermDoor(t, h)
	waitAuditSink(t, h)
	go decideFirst(t, h.Broker(), broker.Decision{Outcome: broker.Denied})
	h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	if seq, rec := waitAudit(t, notes, "exec"); seq != 1 || rec["command"] != "ls" || rec["outcome"] != "denied" {
		t.Fatalf("exec: seq %d %v", seq, rec)
	}
	// SetAutoAllow writes its record with h.mu held: the callback must not take it.
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	if seq, rec := waitAudit(t, notes, "config"); seq != 2 || rec["action"] != "autoAllowOn" {
		t.Fatalf("config: seq %d %v", seq, rec)
	}
}

func TestAuditAppendedFileAndTunnel(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	if err := fx.c.Call(ctx, "files.mkdir", map[string]any{"server": "fs", "path": "/home/d"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, rec := waitAudit(t, fx.notes, "file"); rec["action"] != "mkdir" {
		t.Fatalf("file: %v", rec)
	}
	id := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": tunnelFreePort(t)})
	if _, rec := waitAudit(t, fx.notes, "config"); rec["action"] != "tunnelSave" {
		t.Fatalf("config: %v", rec)
	}
	if err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id}, nil); err != nil {
		t.Fatal(err)
	}
	if _, rec := waitAudit(t, fx.notes, "tunnel"); rec["phase"] != "start" || rec["id"] != id {
		t.Fatalf("tunnel: %v", rec)
	}
}

func TestAuditAppendedNotWhileLocked(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	_, _, notes := startTermDoor(t, h)
	waitAuditSink(t, h)
	h.Lock()
	h.auditConfig(broker.ConfigRecord{Action: "whileLocked"})
	unlockForTest(h)
	h.auditConfig(broker.ConfigRecord{Action: "afterUnlock"})
	if seq, rec := waitAudit(t, notes, "config"); seq != 2 || rec["action"] != "afterUnlock" {
		t.Fatalf("first audit.appended: seq %d %v; the locked write must send nothing", seq, rec)
	}
}

// A UI door whose output nobody reads must not hold up writers (the sink only
// queues) nor the idle lock.
func TestAuditAppendedNeverBlocksUnderHubLock(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	uiR, hubW := io.Pipe()
	hubR, uiW := io.Pipe()
	go ServeUIDoor(context.Background(), h, hubR, hubW)
	t.Cleanup(func() { uiR.Close(); uiW.Close(); hubW.Close() })
	waitAuditSink(t, h)

	in := config.ServerInput{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 300; i++ { // more than the queue holds; each writes a record
			if err := h.SaveServer("vis", in); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("audit writes blocked on an unread UI door")
	}
	go h.lockIfIdle(0) // its "locked" notification may block on the pipe; the key must already be gone
	deadline := time.Now().Add(10 * time.Second)
	for !h.Locked() {
		if time.Now().After(deadline) {
			t.Fatal("idle lock did not zero the key")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAuditAfterVaultCreate(t *testing.T) {
	h, _ := newHubAt(t, &config.File{Version: 1}, nil)
	c, _, notes := startTermDoor(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, rec := waitAudit(t, notes, "config"); rec["action"] != "vaultCreate" {
		t.Fatalf("%v", rec)
	}
	var page auditPage
	if err := c.Call(ctx, "audit.read", map[string]any{}, &page); err != nil || len(page.Records) != 1 {
		t.Fatalf("%v %+v", err, page)
	}
}

// A file record's local paths stay in the hub: audit.read and audit.appended
// both drop them, and the file on disk keeps them. Other records are
// returned as written.
func TestAuditFileRecordsLoseLocalPaths(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _, notes := startTermDoor(t, h)
	waitAuditSink(t, h)
	h.auditFile(broker.FileRecord{Kind: "file", Action: "upload", Server: "vis", Remote: []string{"/r/a"}, Local: []string{"/Users/me/secret/a"}})
	h.auditConfig(broker.ConfigRecord{Action: "save", Server: "vis", Reason: `{"local":"/kept"}`})
	_, rec := waitAudit(t, notes, "file")
	if _, ok := rec["local"]; ok || rec["action"] != "upload" || rec["remote"] == nil {
		t.Fatalf("audit.appended: %v", rec)
	}
	var page auditPage
	if err := c.Call(context.Background(), "audit.read", map[string]any{}, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 2 {
		t.Fatalf("%+v", page)
	}
	if _, ok := page.Records[1].Record["local"]; ok || page.Records[1].Record["action"] != "upload" {
		t.Fatalf("audit.read file record: %v", page.Records[1].Record)
	}
	if page.Records[0].Record["reason"] != `{"local":"/kept"}` {
		t.Fatalf("audit.read config record: %v", page.Records[0].Record)
	}
	disk, err := os.ReadFile(page.Path)
	if err != nil || !strings.Contains(string(disk), "/Users/me/secret/a") {
		t.Fatalf("the file on disk must keep local: %v %s", err, disk)
	}
}

// Nothing follows locked: records written while a lock lands are either sent
// before the locked notification or not at all.
func TestAuditAppendedNeverFollowsLocked(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _, notes := startTermDoor(t, h)
	waitAuditSink(t, h)
	// A reply proves Serve is running: a notification sent before that is dropped.
	if err := c.Call(context.Background(), "hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		unlockForTest(h)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for j := 0; j < 50; j++ {
				h.auditConfig(broker.ConfigRecord{Action: "race"})
			}
		}()
		h.Lock()
		<-done
		// Drain notes up to locked, then probe: a later write while locked and
		// a fresh unlock must show nothing stale in between.
		locked := false
		for !locked {
			select {
			case n := <-notes:
				locked = n.method == "locked"
			case <-time.After(10 * time.Second):
				t.Fatal("no locked notification")
			}
		}
		h.auditConfig(broker.ConfigRecord{Action: "marker"}) // locked: dropped
		unlockForTest(h)
		h.auditConfig(broker.ConfigRecord{Action: "marker"})
		for {
			select {
			case n := <-notes:
				if n.method != "audit.appended" {
					continue
				}
				var p struct {
					Record map[string]any `json:"record"`
				}
				json.Unmarshal(n.params, &p)
				if p.Record["action"] == "race" {
					t.Fatalf("iteration %d: a record written before the lock came after locked", i)
				}
				goto next
			case <-time.After(10 * time.Second):
				t.Fatal("no marker")
			}
		}
	next:
		h.Lock()
		for { // drain through this round's locked
			if n := <-notes; n.method == "locked" {
				break
			}
		}
	}
}

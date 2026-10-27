package view

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// execEntry is an exec record of box at the fake clock's time.
func execEntry(seq int, cmd, outcome string) hubapi.AuditEntry {
	b, _ := json.Marshal(map[string]any{"time": "2026-10-15T12:00:00Z", "server": "box", "client": "e2e", "command": cmd, "outcome": outcome, "description": "audit e2e"})
	return hubapi.AuditEntry{Seq: seq, Record: b}
}

// auditDoor answers audit.read from recs, newest first, as the hub
// filters them; limit records a page, Next set when older ones are left.
func auditDoor(recs *[]hubapi.AuditEntry, limit int) func(q hubapi.AuditQuery) hubapi.AuditPage {
	return func(q hubapi.AuditQuery) hubapi.AuditPage {
		p := hubapi.AuditPage{Records: []hubapi.AuditEntry{}, Path: "/a/audit.jsonl"}
		for i := len(*recs) - 1; i >= 0; i-- {
			e := (*recs)[i]
			if q.Before != 0 && e.Seq >= q.Before || !model.AuditMatches(model.RowOf(e).Rec, q) {
				continue
			}
			if len(p.Records) == limit {
				p.Next = p.Records[limit-1].Seq
				break
			}
			p.Records = append(p.Records, e)
		}
		return p
	}
}

// auditApp is the ready screen on box, the audit log holding recs.
func auditApp(t *testing.T, recs ...hubapi.AuditEntry) (*App, *tester, *fakeDoor, *clock) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box"}, {Name: "db"}}}
	d.audit = auditDoor(&recs, 200)
	a, tt, c, _ := newApp(t, d)
	tt.SetSize(1100, 900) // an expanded row's Raw JSON in view on any platform's fonts
	tt.flush()
	return a, tt, d, c
}

func auditParams(d *fakeDoor) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.params["audit.read"]
}

func TestAuditTabReadsWhenShownOnly(t *testing.T) {
	a, tt, d, _ := auditApp(t, execEntry(1, "echo one", "denied"), execEntry(2, "echo two", "allowed"))
	if n := calls(d, "audit.read"); n != 0 {
		t.Fatalf("audit.read before the tab showed: %d", n)
	}
	clickT(t, tt, "Audit")
	tt.flush()
	if n := calls(d, "audit.read"); n != 1 || auditParams(d) != `{}` {
		t.Fatalf("reads %d, params %s", n, auditParams(d))
	}
	if a.audit.list.Status != "loaded" || !tt.HasText("echo one") || !tt.HasText("echo two") || !tt.HasText("Today") ||
		!tt.HasText("Audit file: /a/audit.jsonl") || !tt.HasText("Command or change") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if got := tt.Texts(); slices.Index(got, "echo two") > slices.Index(got, "echo one") {
		t.Fatalf("not newest first: %q", got)
	}
	clickT(t, tt, "Hosts")
	clickT(t, tt, "Audit")
	tt.flush()
	if n := calls(d, "audit.read"); n != 1 {
		t.Fatalf("showing the tab again read again: %d", n)
	}
}

func TestAuditFiltersRead(t *testing.T) {
	_, tt, d, c := auditApp(t, execEntry(1, "tail x", "allowed"))
	clickT(t, tt, "Audit")
	tt.flush()

	clickT(t, tt, "Filter Auto")
	tt.flush()
	if p := auditParams(d); p != `{"outcomes":["auto"]}` {
		t.Fatalf("chip: %s", p)
	}
	clickT(t, tt, "Filter Exec")
	tt.flush()
	if p := auditParams(d); p != `{"kinds":["exec"],"outcomes":["auto"]}` {
		t.Fatalf("two chips: %s", p)
	}
	choose(t, tt, "All hosts", "db") // not "box": a row's host cell would take the click
	tt.flush()
	if p := auditParams(d); p != `{"server":"db","kinds":["exec"],"outcomes":["auto"]}` {
		t.Fatalf("host: %s", p)
	}

	// Typing reads 300 ms after the last key; Enter reads at once.
	n := calls(d, "audit.read")
	clickT(t, tt, "Search audit")
	tt.Type("tai")
	tt.flush()
	c.add(200 * time.Millisecond)
	tt.Type("l")
	tt.flush()
	c.add(200 * time.Millisecond)
	tt.flush()
	if got := calls(d, "audit.read"); got != n {
		t.Fatalf("read while still typing: %d, was %d", got, n)
	}
	c.add(150 * time.Millisecond)
	tt.flush()
	if got := calls(d, "audit.read"); got != n+1 || !strings.Contains(auditParams(d), `"text":"tail"`) {
		t.Fatalf("after the pause: %d reads, %s", got, auditParams(d))
	}
	tt.Type(" ")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if got := calls(d, "audit.read"); got != n+2 {
		t.Fatalf("Enter: %d reads", got)
	}
	c.add(time.Second)
	tt.flush()
	if got := calls(d, "audit.read"); got != n+2 {
		t.Fatalf("the pause after Enter read again: %d", got)
	}
	clickT(t, tt, "Refresh")
	tt.flush()
	if got := calls(d, "audit.read"); got != n+3 {
		t.Fatalf("Refresh: %d reads", got)
	}
}

func TestAuditLiveRecordsCallNothing(t *testing.T) {
	a, tt, d, _ := auditApp(t, execEntry(1, "echo one", "denied"))
	clickT(t, tt, "Audit")
	clickT(t, tt, "Filter Denied")
	tt.flush()
	d.resetCalls()
	note(t, a, tt, "audit.appended", execEntry(2, "echo live", "denied"))
	note(t, a, tt, "audit.appended", execEntry(3, "echo allowed", "allowed")) // not what the list was read with
	note(t, a, tt, "audit.appended", execEntry(2, "echo live", "denied"))     // a duplicate
	if got := tt.Texts(); !tt.HasText("echo live") || tt.HasText("echo allowed") || slices.Index(got, "echo live") > slices.Index(got, "echo one") {
		t.Fatalf("texts %q", got)
	}
	if got := len(a.audit.list.Entries); got != 2 {
		t.Fatalf("entries %d", got)
	}
	for _, m := range d.callList() {
		if m != "status" {
			t.Fatalf("a live record called %q", m)
		}
	}
}

func TestAuditHoldsNewRecordsWhileScrolledAway(t *testing.T) {
	var recs []hubapi.AuditEntry
	for i := 1; i <= 80; i++ {
		recs = append(recs, execEntry(i, fmt.Sprintf("echo %02d", i), "denied"))
	}
	a, tt, _, _ := auditApp(t, recs...)
	clickT(t, tt, "Audit")
	tt.flush()
	for i := 0; i < 20; i++ {
		tt.Scroll(500, 300, 0, 200)
	}
	tt.flush()
	if a.audit.atTop {
		t.Fatal("still at the top after scrolling")
	}
	note(t, a, tt, "audit.appended", execEntry(81, "echo live", "denied"))
	note(t, a, tt, "audit.appended", execEntry(82, "echo live 2", "denied"))
	if len(a.audit.list.Held) != 2 || !tt.HasText("2 new") || len(a.audit.list.Entries) != 80 {
		t.Fatalf("held %d entries %d, texts %q", len(a.audit.list.Held), len(a.audit.list.Entries), tt.Texts())
	}
	clickT(t, tt, "2 new")
	tt.flush()
	if len(a.audit.list.Held) != 0 || a.audit.list.Entries[0].Seq != 82 || !a.audit.atTop || !tt.HasText("echo live 2") {
		t.Fatalf("after the pill: held %d first %d atTop %v", len(a.audit.list.Held), a.audit.list.Entries[0].Seq, a.audit.atTop)
	}
}

func TestAuditRowExpands(t *testing.T) {
	a, tt, _, _ := auditApp(t, execEntry(1, "echo audit-denied", "denied"))
	clickT(t, tt, "Audit")
	tt.flush()
	if tt.HasText("AI's description · unverified") {
		t.Fatal("detail shown before a click")
	}
	clickFold(t, tt, "echo audit-denied")
	tt.flush()
	for _, want := range []string{"AI's description · unverified", "audit e2e", "Client", "e2e"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	clickFold(t, tt, "Raw JSON")
	tt.flush()
	if !tt.HasText(`  "command": "echo audit-denied",`) {
		t.Fatalf("raw: %q", tt.Texts())
	}
	clickT(t, tt, "echo audit-denied")
	tt.flush()
	if *a.audit.open[1] {
		t.Fatal("a second click did not collapse the row")
	}
	// The panel shrinks out of view as its animation runs.
	tt.Advance(time.Second)
	if tt.HasText("AI's description · unverified") {
		t.Fatalf("the detail still shows: %q", tt.Texts())
	}
}

func TestAuditLoadOlder(t *testing.T) {
	var recs []hubapi.AuditEntry
	for i := 1; i <= 5; i++ {
		recs = append(recs, execEntry(i, fmt.Sprintf("echo %d", i), "denied"))
	}
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box"}}}
	d.audit = auditDoor(&recs, 3)
	a, tt, _, _ := newApp(t, d)
	tt.SetSize(1100, 900)
	clickT(t, tt, "Audit")
	tt.flush()
	if a.audit.list.Next != 3 || len(a.audit.list.Entries) != 3 {
		t.Fatalf("first page %+v", a.audit.list)
	}
	clickT(t, tt, "Load older")
	tt.flush()
	if p := auditParams(d); p != `{"before":3}` {
		t.Fatalf("older: %s", p)
	}
	if a.audit.list.Next != 0 || len(a.audit.list.Entries) != 5 || tt.HasText("Load older") || !tt.HasText("echo 1") {
		t.Fatalf("after older %+v", a.audit.list)
	}
}

func TestAuditLockClearsAndUnlockReadsAgain(t *testing.T) {
	a, tt, d, _ := auditApp(t, execEntry(1, "echo one", "denied"))
	d.pw = "pw"
	clickT(t, tt, "Audit")
	tt.flush()
	clickT(t, tt, "echo one")
	tt.flush()
	if o := a.audit.open[1]; o == nil || !*o || !tt.HasText("AI's description · unverified") {
		t.Fatal("the row did not expand before the lock")
	}

	// The locked note lands before the status reply: nothing shows, nothing reads.
	gate := make(chan struct{})
	d.set(func() {
		d.status.Locked = true
		d.gates = map[string]chan struct{}{"status": gate}
	})
	d.resetCalls()
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	note(t, a, tt, "audit.appended", execEntry(2, "echo behind the lock", "denied"))
	if a.Screen() != model.ScreenReady || tt.HasText("echo one") || a.audit.list.Status != "cleared" || calls(d, "audit.read") != 0 {
		t.Fatalf("screen %s list %+v reads %d", a.Screen(), a.audit.list, calls(d, "audit.read"))
	}
	close(gate)
	tt.flush()
	if a.Screen() != model.ScreenLocked {
		t.Fatal(a.Screen())
	}
	d.set(func() { d.gates = nil })

	clickT(t, tt, "Master password")
	tt.Type("pw")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if a.Screen() != model.ScreenReady || calls(d, "audit.read") != 1 || !tt.HasText("echo one") {
		t.Fatalf("after unlock: screen %s reads %d texts %q", a.Screen(), calls(d, "audit.read"), tt.Texts())
	}
	if tt.HasText("AI's description · unverified") {
		t.Fatal("a row stayed expanded across the lock")
	}
}

// A read whose list was reset (a lock, a hub stop) while it was in flight
// changes nothing when it lands.
func TestAuditStaleReplyDropped(t *testing.T) {
	a, tt, d, _ := auditApp(t, execEntry(1, "echo old", "denied"))
	gate := make(chan struct{})
	d.set(func() { d.gates = map[string]chan struct{}{"audit.read": gate} })
	clickT(t, tt, "Audit")
	tt.flush()
	if a.audit.list.Status != "loading" || !tt.HasText("Loading…") {
		t.Fatalf("list %+v", a.audit.list)
	}
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	close(gate)
	tt.flush()
	if a.audit.list.Status != "cleared" || len(a.audit.list.Entries) != 0 {
		t.Fatalf("a stale reply landed: %+v", a.audit.list)
	}
}

func TestAuditErrorKeepsRows(t *testing.T) {
	a, tt, d, _ := auditApp(t, execEntry(1, "echo one", "denied"))
	clickT(t, tt, "Audit")
	tt.flush()
	d.set(func() { d.errs = map[string]error{"audit.read": errors.New("Vault is locked; unlock it in the app")} })
	clickT(t, tt, "Refresh")
	tt.flush()
	if a.audit.list.Status != "error" || !tt.HasText("Vault is locked; unlock it in the app") {
		t.Fatalf("list %+v texts %q", a.audit.list, tt.Texts())
	}
	d.set(func() { d.errs = nil })
	clickT(t, tt, "Refresh")
	tt.flush()
	if a.audit.list.Status != "loaded" || tt.HasText("Vault is locked; unlock it in the app") || !tt.HasText("echo one") {
		t.Fatalf("after a good read %+v", a.audit.list)
	}
}

// Server text is shown through DisplayText: a hidden character in a
// command shows as its escape.
func TestAuditEscapesHiddenText(t *testing.T) {
	_, tt, _, _ := auditApp(t, execEntry(1, "echo a\u202eb", "denied"))
	clickT(t, tt, "Audit")
	tt.flush()
	if !tt.HasText(`echo a\u202eb`) || tt.HasText("echo a\u202eb") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

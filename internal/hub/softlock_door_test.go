package hub

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// doorParams has valid params for every UI-door request, aimed at "vis", the
// host with a grant. Valid matters: a strict decoder that rejects the params
// answers -32602 before any lock check, and the comparison below would pass
// on a broken build.
var doorParams = map[string]string{
	"hello":                  `{}`,
	"status":                 `{}`,
	"servers":                `{}`,
	"pending":                `{}`,
	"denyAll":                `{"reason":"x"}`,
	"decide":                 `{"id":"none","outcome":"allowed","reason":""}`,
	"vault.create":           `{"password":"password1"}`,
	"servers.save":           `{"original":"vis","server":{"name":"vis","host":"h","port":22,"user":"u","auth":"agent","keyPath":"","aiVisible":true,"autoAllowRoot":false,"autoAllowSudo":false},"autoAllow":"30m"}`,
	"servers.delete":         `{"name":"vis"}`,
	"servers.forgetHostKey":  `{"name":"vis"}`,
	"servers.setAutoAllow":   `{"server":"vis","mode":"30m"}`,
	"servers.autoAllowCheck": `{"server":"vis"}`,
	"import.scan":            `{}`,
	"import.apply":           `{"aliases":["x"]}`,
	"audit.read":             `{}`,
	"term.open":              `{"id":"t1","server":"vis","rows":24,"cols":80}`,
	"term.close":             `{"id":"t1"}`,
	"term.closeAll":          `{}`,
	"files.list":             `{"server":"vis","path":"/"}`,
	"files.mkdir":            `{"server":"vis","path":"/x"}`,
	"files.rename":           `{"server":"vis","from":"/a","to":"/b"}`,
	"files.plan":             `{"id":"j1","server":"vis","op":"delete","sources":["/a"],"dest":""}`,
	"files.run":              `{"id":"j1","conflict":"skip"}`,
	"files.cancelAll":        `{}`,
	"tunnels.list":           `{}`,
	"tunnels.save":           `{"server":"vis","tunnel":{"id":"","kind":"local","listenPort":18080,"targetHost":"127.0.0.1","targetPort":80}}`,
	"tunnels.delete":         `{"server":"vis","id":"x"}`,
	"tunnels.start":          `{"server":"vis","id":"x"}`,
}

// Not called: notifications (no reply to compare) and the two that change
// the lock state itself, which TestLockUnderSoftLockIsHard and
// TestUIDoorUnlockReportsRanWhileLocked cover.
var doorSkipped = map[string]bool{
	"term.write": true, "term.resize": true, "term.ack": true, "files.cancel": true, "tunnels.stop": true,
	"lock": true, "unlock": true,
}

// These must be refused as locked under soft lock, whatever else is compared.
var doorMustRefuse = []string{
	"servers.save", "servers.delete", "servers.forgetHostKey", "servers.setAutoAllow", "servers.autoAllowCheck",
	"import.scan", "audit.read", "term.open", "files.list", "tunnels.save", "tunnels.start", "decide",
}

// Under soft lock the UI door must answer every request exactly as it does
// under a hard lock. The table must cover the whole door: a new method with
// no entry fails the test.
func TestUIDoorSoftLockAnswersLikeHardLock(t *testing.T) {
	var methods []string
	uiDoorMethods = func(m []string) { methods = m }
	defer func() { uiDoorMethods = nil }()

	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	c, _ := startUI(t, h)
	if err := c.Call(context.Background(), "hello", nil, nil); err != nil { // the door is up, methods is set
		t.Fatal(err)
	}
	for _, m := range methods {
		if _, ok := doorParams[m]; !ok && !doorSkipped[m] {
			t.Errorf("UI-door method %q has no entry in doorParams or doorSkipped", m)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	ask := func() map[string]string {
		out := map[string]string{}
		for _, m := range methods {
			if doorSkipped[m] {
				continue
			}
			var res json.RawMessage
			err := c.Call(context.Background(), m, json.RawMessage(doorParams[m]), &res)
			if err != nil {
				out[m] = "error: " + err.Error()
			} else {
				out[m] = string(res)
			}
		}
		return out
	}

	softLockForTest(h)
	soft := ask()
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("state after the soft pass = %+v: a call changed something", got)
	}
	h.Lock()
	hard := ask()

	for _, m := range methods {
		if doorSkipped[m] {
			continue
		}
		switch m {
		case "status":
			if !strings.Contains(soft[m], `"autoHosts":["vis"]`) || strings.Contains(hard[m], "autoHosts") {
				t.Errorf("status: soft %s, hard %s", soft[m], hard[m])
			}
		case "servers.setAutoAllow":
			// Under a hard lock the timed mode fails in checkLocked too; both
			// are the locked error, compared below.
			fallthrough
		default:
			if soft[m] != hard[m] {
				t.Errorf("%s differs\nsoft: %s\nhard: %s", m, soft[m], hard[m])
			}
		}
	}
	for _, m := range doorMustRefuse {
		if !strings.Contains(strings.ToLower(soft[m]), "locked") || !strings.HasPrefix(soft[m], "error: ") {
			t.Errorf("%s under soft lock = %s, want a locked refusal", m, soft[m])
		}
	}
}

// setAutoAllow off is the one request whose soft-lock answer differs from the
// hard-lock one: under a hard lock it has nothing to end and succeeds.
func TestUIDoorSetAutoAllowOffRefusedUnderSoftLock(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	c, _ := startUI(t, h)
	softLockForTest(h)
	err := c.Call(context.Background(), "servers.setAutoAllow", map[string]string{"server": "vis", "mode": "off"}, nil)
	if err == nil || err.Error() != ErrLocked.Error() {
		t.Fatalf("off under soft lock = %v, want the locked error", err)
	}
	if grantOf(h, "vis") == nil {
		t.Fatal("the refused off ended the grant")
	}
}

// Notifications that only move to a safer state still work behind the lock.
func TestUIDoorSafeNotificationsUnderSoftLock(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	c, w, _ := startTermDoor(t, h)
	softLockForTest(h)
	before := func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.lastActivity }()
	sendNote(t, w, "tunnels.stop", map[string]any{"server": "vis", "id": "x"})
	sendNote(t, w, "files.cancel", map[string]any{"id": "j1"})
	sendNote(t, w, "term.write", map[string]any{"id": "t1", "data": "eA=="})
	if err := c.Call(context.Background(), "status", nil, nil); err != nil { // the read loop got past all three
		t.Fatal(err)
	}
	if after := func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.lastActivity }(); !after.Equal(before) {
		t.Fatal("a notification without user:true counted as UI activity")
	}
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("state = %+v", got)
	}
}

// The invariant under concurrent execs, sweeps, idle ticks, unlocks and
// locks: softLocked implies a key and at least one grant. Run with -race.
func TestSoftLockInvariantUnderRace(t *testing.T) {
	be := newBlockExec() // not fakeExec: its calls slice has no mutex
	close(be.release)
	h, _ := newHub(t, be)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	loop := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					f()
				}
			}
		}()
	}
	loop(func() { unlockForTest(h); h.SetAutoAllow("vis", "15m") })
	loop(func() { idleNow(h) })
	loop(func() { h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}) })
	loop(func() { h.sweepGrants(time.Now().Add(time.Hour)) }) // every timed grant is past its deadline
	loop(func() { h.Lock() })
	loop(func() {
		h.mu.Lock()
		if h.softLocked && (h.deps.MasterKey == nil || len(h.grants) == 0) {
			t.Errorf("invariant broken: soft-locked with key=%v grants=%d", h.deps.MasterKey != nil, len(h.grants))
		}
		h.mu.Unlock()
	})
	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}

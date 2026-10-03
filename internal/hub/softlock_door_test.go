package hub

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/rpc"
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
	"import.scan", "import.apply", "audit.read", "term.open", "files.list", "files.mkdir", "files.rename",
	"files.plan", "files.run", "tunnels.save", "tunnels.start", "tunnels.delete", "decide",
}

// These must fail under both locks, but not as "locked": vault.create refuses
// because a vault exists, whatever the lock state.
var doorMustError = []string{"vault.create"}

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
				var re *rpc.Error
				if errors.As(err, &re) && re.Code == -32602 {
					t.Errorf("%s: invalid params, the entry in doorParams proves nothing: %v", m, err)
				}
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
			if !strings.Contains(soft[m], `"autoHosts":["vis"]`) || strings.Contains(hard[m], "autoHosts") ||
				!strings.Contains(soft[m], `"locked":true`) || !strings.Contains(hard[m], `"locked":true`) {
				t.Errorf("status: soft %s, hard %s", soft[m], hard[m])
			}
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
	for _, m := range doorMustError {
		if !strings.HasPrefix(soft[m], "error: ") || !strings.HasPrefix(hard[m], "error: ") {
			t.Errorf("%s: soft %s, hard %s, want an error under both locks", m, soft[m], hard[m])
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

// Under soft lock the read loop survives tunnels.stop, files.cancel and a
// term.write without user:true, none of them counts as UI activity, and the
// lock state is unchanged. The ids do not exist, so this does not show a cancel
// taking effect: TestJobCancelWhileRunningAndWhileLocked drives a real one.
// (term.write with user:true touches the idle clock before it looks the id up,
// even for an unknown id, so it is not asserted here.)
func TestUIDoorNotificationsUnderSoftLockAreNotActivity(t *testing.T) {
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
// locks: autoKey implies no deps key and at least one grant. Run with -race.
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
	var seen int // under h.mu: how often the checker saw a soft lock
	loop(func() {
		h.mu.Lock()
		if h.autoKey != nil {
			seen++
		}
		if h.autoKey != nil && (h.deps.MasterKey != nil || len(h.grants) == 0) {
			t.Errorf("invariant broken: soft-locked with deps key=%v grants=%d", h.deps.MasterKey != nil, len(h.grants))
		}
		h.mu.Unlock()
	})
	time.Sleep(500 * time.Millisecond)
	// A soft lock is rare under this churn: run on until the checker has seen one.
	for end := time.Now().Add(testWait); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		h.mu.Lock()
		n := seen
		h.mu.Unlock()
		if n > 0 {
			break
		}
	}
	close(stop)
	wg.Wait()
	if seen == 0 {
		t.Fatal("no soft lock was ever observed: the test did not exercise the invariant")
	}
}

// What a locked UI receives: nothing with content. The softLock audit record
// and a run's record and ran notice are withheld; the lock itself and a
// grant ending are not.
func TestUIDoorSoftLockNotifications(t *testing.T) {
	be := newBlockExec()
	h, _ := newHub(t, be)
	c, _, notes := startTermDoor(t, h)
	if err := c.Call(context.Background(), "status", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	}()
	waitInflight(t, h, "vis", 1)

	// collect gathers notifications for d.
	collect := func(d time.Duration) []note {
		var got []note
		tm := time.After(d)
		for {
			select {
			case n := <-notes:
				got = append(got, n)
			case <-tm:
				return got
			}
		}
	}
	has := func(ns []note, method string) bool {
		for _, n := range ns {
			if n.method == method {
				return true
			}
		}
		return false
	}
	if !has(collect(300*time.Millisecond), "audit.appended") {
		t.Fatal("no audit.appended while unlocked: the notification channel does not work")
	}

	idleNow(h)
	got := collect(300 * time.Millisecond)
	if len(got) == 0 || got[0].method != "locked" || string(got[0].params) != `{"reason":"idle"}` {
		t.Fatalf("first notification after idle = %+v, want locked idle", got)
	}
	for _, n := range got {
		if n.method == "audit.appended" || n.method == "autoAllow.ran" {
			t.Fatalf("locked UI received %s: %s", n.method, n.params)
		}
	}

	close(be.release)
	<-done
	for _, n := range collect(300 * time.Millisecond) {
		if n.method == "audit.appended" || n.method == "autoAllow.ran" {
			t.Fatalf("locked UI received %s after a run: %s", n.method, n.params)
		}
	}
	if ran := h.TakeRanLocked(); len(ran) != 1 || ran[0] != (RanLocked{"vis", 1}) {
		t.Fatalf("TakeRanLocked = %+v", ran)
	}

	h.mu.Lock()
	h.grants["vis"].until = time.Now().Add(-time.Minute)
	h.mu.Unlock()
	h.sweepGrants(time.Now())
	var off, hard bool
	for _, n := range collect(300 * time.Millisecond) {
		switch n.method {
		case "autoAllow.off":
			off = off || string(n.params) == `{"reason":"expired","server":"vis"}`
		case "locked":
			hard = hard || string(n.params) == `{"reason":"grantsEnded"}`
		}
	}
	if !off || !hard {
		t.Fatalf("grant ended under soft lock: autoAllow.off expired=%v, locked grantsEnded=%v", off, hard)
	}
}

// The door's lock: {} keeps auto-allow running, {stopAuto: true} stops it,
// and nothing else is accepted: invalid params hard-lock (fail closed) before
// the -32602 error.
func TestUIDoorLockParams(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	ctx := context.Background()
	for _, bad := range []any{map[string]any{"stop": true}, map[string]any{"stopAuto": "yes"}, map[string]any{"stopAuto": nil}, []any{}} {
		unlockForTest(h)
		if err := h.SetAutoAllow("vis", "forever"); err != nil {
			t.Fatal(err)
		}
		var rerr *rpc.Error
		if err := c.Call(ctx, "lock", bad, nil); !errors.As(err, &rerr) || rerr.Code != -32602 {
			t.Fatalf("lock %v: %v, want -32602", bad, err)
		}
		if got := stateOf(h); got != (lockState{}) {
			t.Fatalf("lock %v left the state %+v, want hard lock", bad, got)
		}
	}
	unlockForTest(h)
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "lock", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("lock {} = %+v, want soft lock", got)
	}
	if err := c.Call(ctx, "lock", map[string]any{"stopAuto": true}, nil); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("lock {stopAuto: true} = %+v, want hard lock", got)
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "locked") {
		t.Fatalf("no autoAllowOff/locked: %v", recs)
	}
}

// lock with no params at all is lock {}.
func TestUIDoorLockWithoutParamsKeepsAuto(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "lock", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("lock = %+v, want soft lock", got)
	}
}

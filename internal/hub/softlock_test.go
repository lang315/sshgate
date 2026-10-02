package hub

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
)

// softLockForTest puts h in soft lock without waiting for the idle rule. The
// caller arms a grant first: soft lock with no grant breaks the invariant.
func softLockForTest(h *Hub) {
	h.mu.Lock()
	h.softLocked, h.softLockedAt = true, time.Now().Round(0)
	h.unlocked.Store(false)
	h.mu.Unlock()
}

// idleNow makes h look idle for an hour and runs one idle check.
func idleNow(h *Hub) {
	h.mu.Lock()
	h.lastActivity = time.Now().Add(-time.Hour)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
}

type lockState struct {
	soft, key bool
	grants    int
}

func stateOf(h *Hub) lockState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return lockState{h.softLocked, h.deps.MasterKey != nil, len(h.grants)}
}

type lockNote struct {
	reason string
	soft   bool
}

func captureLocks(h *Hub) chan lockNote {
	c := make(chan lockNote, 8)
	h.setLockSink(func(reason string, soft bool) { c <- lockNote{reason, soft} })
	return c
}

func wantLock(t *testing.T, c chan lockNote, want lockNote) {
	t.Helper()
	select {
	case got := <-c:
		if got != want {
			t.Fatalf("locked = %+v, want %+v", got, want)
		}
	case <-time.After(testWait):
		t.Fatalf("no locked notification, want %+v", want)
	}
}

func noLock(t *testing.T, c chan lockNote) {
	t.Helper()
	select {
	case got := <-c:
		t.Fatalf("unexpected locked %+v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func countAction(recs []map[string]any, action string) int {
	n := 0
	for _, r := range recs {
		if r["action"] == action {
			n++
		}
	}
	return n
}

func TestIdleWithGrantSoftLocks(t *testing.T) {
	for _, mode := range []string{"15m", "forever"} {
		t.Run(mode, func(t *testing.T) {
			h, path := newHub(t, &fakeExec{})
			locks := captureLocks(h)
			if err := h.SetAutoAllow("vis", mode); err != nil {
				t.Fatal(err)
			}
			idleNow(h)
			if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
				t.Fatalf("state = %+v", got)
			}
			if !h.Locked() || h.unlocked.Load() {
				t.Fatal("soft lock must read as locked to the UI")
			}
			wantLock(t, locks, lockNote{"idle", true})
			_, recs := readAudit(t, path)
			last := recs[len(recs)-1]
			if last["action"] != "softLock" || !reflect.DeepEqual(last["servers"], []any{"vis"}) {
				t.Fatalf("audit: %v", last)
			}
			if locked, hosts := h.lockStatus(); !locked || !reflect.DeepEqual(hosts, []string{"vis"}) {
				t.Fatalf("lockStatus = %v %v", locked, hosts)
			}
			// A second tick changes nothing and says nothing.
			idleNow(h)
			noLock(t, locks)
			_, recs = readAudit(t, path)
			if n := countAction(recs, "softLock"); n != 1 {
				t.Fatalf("softLock records = %d, want 1", n)
			}
		})
	}
}

func TestIdleWithoutGrantHardLocks(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	locks := captureLocks(h)
	idleNow(h)
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	wantLock(t, locks, lockNote{"idle", false})
	_, recs := readAudit(t, path)
	if countAction(recs, "softLock") != 0 {
		t.Fatal("softLock record without a grant")
	}
	if locked, hosts := h.lockStatus(); !locked || hosts != nil {
		t.Fatalf("lockStatus = %v %v", locked, hosts)
	}
}

// A grant past its deadline is swept before the idle rule looks, so the same
// tick hard-locks.
func TestExpiredGrantAndIdleInOneTickHardLocks(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	locks := captureLocks(h)
	h.mu.Lock()
	h.grants["vis"].until = time.Now().Add(-time.Second)
	h.mu.Unlock()
	h.sweepGrants(time.Now())
	idleNow(h)
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	wantLock(t, locks, lockNote{"idle", false})
	noLock(t, locks)
	_, recs := readAudit(t, path)
	if countAction(recs, "softLock") != 0 {
		t.Fatal("softLock record for an expired grant")
	}
}

func TestPendingRequestHoldsOffSoftLock(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	// sudo without the opt-in goes to approval and stays pending.
	go h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "id", Sudo: true})
	waitPending(t, h.Broker(), 1)
	idleNow(h)
	if got := stateOf(h); got.soft || !got.key {
		t.Fatalf("state = %+v, want still unlocked", got)
	}
	h.Broker().DenyAll("done")
}

func TestSoftLockRefusesWritesAndGrantChanges(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	softLockForTest(h)

	if _, err := h.writeKey(); !errors.Is(err, ErrLocked) {
		t.Fatalf("writeKey = %v, want ErrLocked", err)
	}
	if err := h.DeleteServer("vis"); !errors.Is(err, ErrLocked) {
		t.Fatalf("DeleteServer = %v, want ErrLocked", err)
	}
	for _, mode := range []string{"off", "30m", "forever"} {
		if err := h.SetAutoAllow("vis", mode); !errors.Is(err, ErrLocked) {
			t.Fatalf("SetAutoAllow %s = %v, want ErrLocked", mode, err)
		}
	}
	if _, err := h.AutoAllowCheck(context.Background(), "vis"); !errors.Is(err, ErrLocked) {
		t.Fatalf("AutoAllowCheck = %v, want ErrLocked", err)
	}
	if _, err := h.ReadAudit(auditAll); !errors.Is(err, ErrLocked) {
		t.Fatalf("ReadAudit = %v, want ErrLocked", err)
	}
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("state after refusals = %+v: a refused call changed something", got)
	}
}

// servers must not tell a locked UI which grants are live.
func TestSoftLockServersMatchHardLock(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, encServer(t, "enc", "encSudoPassword", "s3cr3t")) // a server with a stored secret: locked under both
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	softLockForTest(h)
	soft := h.serversForUI()
	h.Lock()
	hard := h.serversForUI()
	if !reflect.DeepEqual(soft, hard) {
		t.Fatalf("servers differ\nsoft: %+v\nhard: %+v", soft, hard)
	}
}

func TestLastGrantEndingHardLocks(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, config.Server{Name: "two", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true})
	for _, n := range []string{"vis", "two"} {
		if err := h.SetAutoAllow(n, "15m"); err != nil {
			t.Fatal(err)
		}
	}
	idleNow(h)
	locks := captureLocks(h)

	// One of two ends: still soft-locked, nothing sent.
	h.mu.Lock()
	h.grants["two"].until = time.Now().Add(-time.Second)
	h.mu.Unlock()
	h.sweepGrants(time.Now())
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("state = %+v", got)
	}
	if _, hosts := h.lockStatus(); !reflect.DeepEqual(hosts, []string{"vis"}) {
		t.Fatalf("autoHosts = %v, want [vis]", hosts)
	}
	noLock(t, locks)

	// The last one ends: the key goes in the same step.
	h.mu.Lock()
	h.grants["vis"].until = time.Now().Add(-time.Second)
	h.mu.Unlock()
	h.sweepGrants(time.Now())
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	wantLock(t, locks, lockNote{"grantsEnded", false})
	// A later tick sends no second locked.
	idleNow(h)
	noLock(t, locks)
}

// auditAll is an audit.read query with no filter.
var auditAll = broker.ReadQuery{}

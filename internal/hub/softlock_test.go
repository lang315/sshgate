package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
)

// softLockForTest puts h in soft lock without waiting for the idle rule. The
// caller arms a grant first: soft lock with no grant breaks the invariant.
func softLockForTest(h *Hub) {
	h.mu.Lock()
	h.autoKey, h.deps.MasterKey, h.softLockedAt = h.deps.MasterKey, nil, time.Now().Round(0)
	h.lockGen.Add(1)
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

// lockState: key is either key present, soft is autoKey.
type lockState struct {
	soft, key bool
	grants    int
}

func stateOf(h *Hub) lockState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return lockState{h.autoKey != nil, h.deps.MasterKey != nil || h.autoKey != nil, len(h.grants)}
}

func captureLocks(h *Hub) chan string {
	c := make(chan string, 8)
	h.setLockSink(func(reason string, gen uint64) {
		if gen == h.lockGen.Load() { // as the door does
			c <- reason
		}
	})
	return c
}

func wantLock(t *testing.T, c chan string, want string) {
	t.Helper()
	select {
	case got := <-c:
		if got != want {
			t.Fatalf("locked = %q, want %q", got, want)
		}
	case <-time.After(testWait):
		t.Fatalf("no locked notification, want %q", want)
	}
}

func noLock(t *testing.T, c chan string) {
	t.Helper()
	select {
	case got := <-c:
		t.Fatalf("unexpected locked %q", got)
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
			wantLock(t, locks, "idle")
			_, recs := readAudit(t, path)
			last := recs[len(recs)-1]
			if last["action"] != "softLock" || last["reason"] != "idle" || !reflect.DeepEqual(last["servers"], []any{"vis"}) {
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
	wantLock(t, locks, "idle")
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
	wantLock(t, locks, "idle")
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
	wantLock(t, locks, "grantsEnded")
	// A later tick sends no second locked.
	idleNow(h)
	noLock(t, locks)
}

// auditAll is an audit.read query with no filter.
var auditAll = broker.ReadQuery{}

func TestSoftLockCeiling(t *testing.T) {
	be := newBlockExec()
	h, path := newHub(t, be)
	addServer(t, h, path, config.Server{Name: "two", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("two", "4h"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	locks := captureLocks(h)

	// One second short: nothing.
	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-maxSoftLock + time.Second)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
	if got := stateOf(h); !got.soft {
		t.Fatalf("state = %+v, want still soft-locked", got)
	}
	noLock(t, locks)

	// At the ceiling: hard lock, both grants end.
	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-maxSoftLock)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	wantLock(t, locks, "softLockLimit")
	noLock(t, locks)
	_, recs := readAudit(t, path)
	n := 0
	for _, r := range recs {
		if r["action"] == "autoAllowOff" && r["reason"] == "soft lock limit" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("autoAllowOff/soft lock limit records = %d, want 2: %v", n, recs)
	}

	// The forever flag stays in the vault, so after unlock the host is paused.
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	for _, s := range h.serversForUI() {
		if s.Name == "vis" && (s.AutoAllow == nil || !s.AutoAllow.Forever || !s.AutoAllow.Paused) {
			t.Fatalf("vis autoAllow = %+v, want forever and paused", s.AutoAllow)
		}
	}
}

func TestSoftLockCeilingCancelsRunInFlight(t *testing.T) {
	be := newBlockExec()
	h, _ := newHub(t, be)
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
		errc <- err
	}()
	waitInflight(t, h, "vis", 1)
	idleNow(h)
	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-maxSoftLock)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
	if err := <-errc; !errors.Is(err, ErrCancelledRunning) {
		t.Fatalf("err = %v, want ErrCancelledRunning", err)
	}
}

// A server hidden by an outside edit of the vault file must not keep its
// grant, and so the key, alive.
func TestSoftLockSweepEndsGrantOfHiddenServer(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	locks := captureLocks(h)
	if err := config.Update(path, testMK, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == "vis" {
				f.Servers[i].AIVisible = false
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.sweepGrants(time.Now())
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	wantLock(t, locks, "grantsEnded")
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "server changed") {
		t.Fatalf("no autoAllowOff/server changed: %v", recs)
	}
}

func TestUnlockFromSoftLockKeepsGrants(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)

	if err := h.Unlock("wrong"); err == nil {
		t.Fatal("wrong password unlocked")
	}
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("state after a wrong password = %+v", got)
	}

	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(h); got != (lockState{key: true, grants: 1}) || h.Locked() || !h.unlocked.Load() {
		t.Fatalf("state after unlock = %+v", got)
	}
	for _, s := range h.serversForUI() {
		if s.Name == "vis" && (s.AutoAllow == nil || !s.AutoAllow.Forever || s.AutoAllow.Paused) {
			t.Fatalf("vis autoAllow = %+v, want forever and not paused", s.AutoAllow)
		}
	}
	// Armed with no Resume: the next exec is auto.
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); err != nil {
		t.Fatalf("exec after unlock: %v", err)
	}
	_, recs := readAudit(t, path)
	if rec := findAutoRecord(t, recs); rec["approval"] != "auto" {
		t.Fatalf("audit: %v", rec)
	}
}

func TestLockUnderSoftLockIsHard(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	locks := captureLocks(h)
	h.Lock()
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	wantLock(t, locks, "manual")
	noLock(t, locks)
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "locked") {
		t.Fatalf("no autoAllowOff/locked: %v", recs)
	}
}

func TestExecUnderSoftLockRunsAuto(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	ran := make(chan string, 4)
	defer h.setAutoSink(func(method string, _ any) { ran <- method })()
	idleNow(h)
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); err != nil {
		t.Fatalf("exec on a granted host under soft lock: %v", err)
	}
	_, recs := readAudit(t, path)
	if rec := findAutoRecord(t, recs); rec["outcome"] != "allowed" || rec["approval"] != "auto" {
		t.Fatalf("audit: %v", rec)
	}
	select {
	case m := <-ran:
		t.Fatalf("%s sent to a locked UI", m)
	case <-time.After(200 * time.Millisecond):
	}
	if got := h.TakeRanLocked(); !reflect.DeepEqual(got, []RanLocked{{Server: "vis", Count: 1}}) {
		t.Fatalf("TakeRanLocked = %+v", got)
	}
	if got := h.TakeRanLocked(); len(got) != 0 {
		t.Fatalf("second TakeRanLocked = %+v, want empty", got)
	}
}

func TestExecUnderSoftLockOtherHostIsLocked(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, config.Server{Name: "two", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	before, _ := readAudit(t, path)
	_, err := h.Exec(context.Background(), ExecRequest{Server: "two", Command: "ls"})
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if n := len(h.Broker().Pending()); n != 0 {
		t.Fatalf("pending = %d, want 0", n)
	}
	if after, _ := readAudit(t, path); after != before {
		t.Fatal("a locked refusal on a host with no grant was audited")
	}
	// Hidden and nonexistent stay byte-identical.
	_, e1 := h.Exec(context.Background(), ExecRequest{Server: "hid", Command: "ls"})
	_, e2 := h.Exec(context.Background(), ExecRequest{Server: "nope", Command: "ls"})
	if e1 == nil || e2 == nil || e1.Error() != serverNotFound("hid").Error() || e2.Error() != serverNotFound("nope").Error() {
		t.Fatalf("hidden %v, missing %v", e1, e2)
	}
	want := []ServerInfo{{Name: "vis", Locked: false}, {Name: "nokey", Locked: true}, {Name: "two", Locked: true}}
	if got := h.ServersForMCP(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ServersForMCP = %+v, want %+v", got, want)
	}
}

func TestExecUnderSoftLockNeedsApprovalFailsAtOnce(t *testing.T) {
	be := newBlockExec()
	h, path := newHubExpiry(t, be, time.Minute) // a request that reached the broker would hang the test
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
		}()
	}
	waitInflight(t, h, "vis", 2)
	idleNow(h)

	_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "third"})
	if !errors.Is(err, ErrAutoBusy) {
		t.Fatalf("third run: %v, want ErrAutoBusy", err)
	}
	_, err = h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "id", Sudo: true})
	if !errors.Is(err, ErrNeedsApproval) {
		t.Fatalf("sudo without the opt-in: %v, want ErrNeedsApproval", err)
	}
	if n := len(h.Broker().Pending()); n != 0 {
		t.Fatalf("pending = %d, want 0", n)
	}
	if grantOf(h, "vis") == nil {
		t.Fatal("a refusal ended the grant")
	}
	// Refusals are not audited: a client retrying ErrAutoBusy would append a
	// record per retry for as long as the soft lock lasts.
	_, recs := readAudit(t, path)
	for _, r := range recs {
		if r["outcome"] == "error" {
			t.Fatalf("a refusal under soft lock was audited: %v", r)
		}
	}
	close(be.release)
	wg.Wait()
}

// The exec that ends the last grant finds the hub hard-locked, not
// soft-locked: it must still fail at once and never reach the broker.
func TestExecThatEndsLastGrantUnderSoftLock(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	h.mu.Lock()
	h.grants["vis"].until = time.Now().Add(-time.Second)
	h.mu.Unlock()
	_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if n := len(h.Broker().Pending()); n != 0 {
		t.Fatalf("pending = %d, want 0", n)
	}
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
}

// A request pending before the soft lock must not run behind the locked UI,
// even on a granted host. This is the test that fails if the grant rule
// leaks into the post-approval resolve; it must use the granted host.
func TestApprovalUnderSoftLockDoesNotRun(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHubExpiry(t, fe, time.Minute)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	c, _ := startUI(t, h)
	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "id", Sudo: true})
		errc <- err
	}()
	waitPending(t, h.Broker(), 1)
	softLockForTest(h)
	id := h.Broker().Pending()[0].ID

	// Through the door: an allow is refused, a deny is not.
	err := c.Call(context.Background(), "decide", map[string]string{"id": id, "outcome": "allowed"}, nil)
	if err == nil || err.Error() != ErrLocked.Error() {
		t.Fatalf("decide allowed = %v, want the locked error", err)
	}
	if n := len(h.Broker().Pending()); n != 1 {
		t.Fatalf("pending = %d after a refused allow, want 1", n)
	}
	// Past the door's check: the post-approval resolve is strict.
	if err := h.Broker().Decide(id, broker.Decision{Outcome: broker.Allowed}); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; !errors.Is(err, ErrLocked) {
		t.Fatalf("approved run under soft lock = %v, want ErrLocked", err)
	}
	if len(fe.calls) != 0 { // read after Exec returned: nothing else writes it
		t.Fatalf("ran %v behind a locked UI", fe.calls)
	}
}

func TestUIDoorUnlockReportsRanWhileLocked(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	c, _ := startUI(t, h)
	idleNow(h)
	for i := 0; i < 2; i++ {
		if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); err != nil {
			t.Fatal(err)
		}
	}
	var st struct {
		Locked    bool     `json:"locked"`
		AutoHosts []string `json:"autoHosts"`
	}
	if err := c.Call(context.Background(), "status", nil, &st); err != nil || !st.Locked || !reflect.DeepEqual(st.AutoHosts, []string{"vis"}) {
		t.Fatalf("status = %+v %v", st, err)
	}
	var reply struct {
		Ran []RanLocked `json:"ranWhileLocked"`
	}
	if err := c.Call(context.Background(), "unlock", map[string]string{"password": "pw"}, &reply); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reply.Ran, []RanLocked{{Server: "vis", Count: 2}}) {
		t.Fatalf("ranWhileLocked = %+v", reply.Ran)
	}
	var raw json.RawMessage
	if err := c.Call(context.Background(), "status", nil, &raw); err != nil || bytes.Contains(raw, []byte("autoHosts")) {
		t.Fatalf("status after unlock = %s %v", raw, err)
	}
}

func TestExecPastCeilingBeforeTickIsLocked(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-maxSoftLock)
	h.mu.Unlock()
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); !errors.Is(err, ErrLocked) {
		t.Fatalf("exec past the ceiling = %v, want ErrLocked", err)
	}
	for _, s := range h.ServersForMCP() {
		if s.Name == "vis" && !s.Locked {
			t.Fatal("vis reported unlocked past the ceiling")
		}
	}
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) { // no tick has run
		t.Fatalf("state = %+v, want still soft-locked with its grant", got)
	}
}

func TestSoftLockSweepHardLocksWhenReloadFails(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	locks := captureLocks(h)
	old := loadStore
	loadStore = func(string) (*config.File, error) { return nil, errors.New("boom") }
	defer func() { loadStore = old }()
	h.sweepGrants(time.Now())
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	wantLock(t, locks, "grantsEnded")
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "server changed") {
		t.Fatalf("no autoAllowOff/server changed: %v", recs)
	}
}

// A deny only moves to a safer state, so the locked UI may still make it.
func TestDenyStillWorksUnderSoftLock(t *testing.T) {
	setup := func(t *testing.T) (*Hub, *rpc.Client, chan error) {
		h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
		if err := h.SetAutoAllow("vis", "15m"); err != nil {
			t.Fatal(err)
		}
		c, _ := startUI(t, h)
		errc := make(chan error, 1)
		go func() {
			_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "id", Sudo: true})
			errc <- err
		}()
		waitPending(t, h.Broker(), 1)
		softLockForTest(h)
		return h, c, errc
	}
	wantDenied := func(t *testing.T, errc chan error) {
		t.Helper()
		var de *DeniedError
		if err := <-errc; !errors.As(err, &de) {
			t.Fatalf("exec = %v, want *DeniedError", err)
		}
	}
	t.Run("decide", func(t *testing.T) {
		h, c, errc := setup(t)
		id := h.Broker().Pending()[0].ID
		err := c.Call(context.Background(), "decide", map[string]string{"id": id, "outcome": "sent_to_tab"}, nil)
		if err == nil || err.Error() != ErrLocked.Error() {
			t.Fatalf("decide sent_to_tab = %v, want the locked error", err)
		}
		if n := len(h.Broker().Pending()); n != 1 {
			t.Fatalf("pending = %d after a refused decide, want 1", n)
		}
		if err := c.Call(context.Background(), "decide", map[string]string{"id": id, "outcome": "denied"}, nil); err != nil {
			t.Fatalf("decide denied: %v", err)
		}
		wantDenied(t, errc)
	})
	t.Run("denyAll", func(t *testing.T) {
		_, c, errc := setup(t)
		if err := c.Call(context.Background(), "denyAll", map[string]string{"reason": "x"}, nil); err != nil {
			t.Fatalf("denyAll: %v", err)
		}
		wantDenied(t, errc)
	})
}

// Under soft lock the key is in autoKey and nowhere a strict reader looks.
func TestSoftLockKeepsKeyOutOfDeps(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, encServer(t, "enc", "encSudoPassword", "s3cr3t"))
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	h.mu.Lock()
	moved := h.deps.MasterKey == nil && h.autoKey != nil
	h.mu.Unlock()
	if !moved {
		t.Fatal("the key is not in autoKey alone")
	}
	dc, err := h.Resolve("enc")
	if err == nil || dc.SudoPassword != "" {
		t.Fatalf("Resolve under soft lock = %+v, %v; want an error and no secret", dc, err)
	}
	if !h.Deps().IsLocked("enc") {
		t.Fatal("Deps().IsLocked(enc) = false under soft lock")
	}
	if _, err := h.writeKey(); !errors.Is(err, ErrLocked) {
		t.Fatalf("writeKey = %v, want ErrLocked", err)
	}
}

// reloadLocked must keep checking the file's MAC under soft lock, with the key
// that is no longer in deps.
func TestSoftLockReloadStillChecksMAC(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Servers[0].Host = "evil" // no key: MAC left stale
	f.Revision++
	b, _ := json.Marshal(f)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err == nil {
		t.Fatal("tampered store accepted under soft lock")
	}
	if s, _ := h.Deps().File.FindServer("vis"); s.Host != "h" {
		t.Fatalf("tampered file took effect: %+v", s)
	}
	h.sweepGrants(time.Now())
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state after the sweep = %+v, want hard lock", got)
	}
}

// The auto path decrypts with autoKey: a granted host that stores a secret
// still runs behind a locked UI.
func TestExecUnderSoftLockDecryptsWithAutoKey(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, encServer(t, "enc", "encSudoPassword", "s3cr3t"))
	setAutoAllowRoot(t, h, path, "enc", true)
	if err := h.SetAutoAllow("enc", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	if got := stateOf(h); !got.soft {
		t.Fatalf("state = %+v, want soft lock", got)
	}
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "enc", Command: "ls"}); err != nil {
		t.Fatalf("exec on a granted host with a stored secret under soft lock: %v", err)
	}
	_, recs := readAudit(t, path)
	if rec := findAutoRecord(t, recs); rec["outcome"] != "allowed" || rec["approval"] != "auto" {
		t.Fatalf("audit: %v", rec)
	}
}

// A lock note carries the generation of its own state change; the door drops
// one the hub has since left.
func TestStaleLockNoteIsDropped(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	c, notes := startUIRaw(t, h)
	if err := c.Call(context.Background(), "status", nil, nil); err != nil { // the door is up
		t.Fatal(err)
	}
	idleNow(h)
	if r := waitLockedNote(t, notes); r != "idle" {
		t.Fatalf("reason = %q", r)
	}
	h.mu.Lock()
	sink, stale := h.lockSink, h.lockGen.Load()
	h.mu.Unlock()
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	sink("grantsEnded", stale)
	select {
	case n := <-notes:
		t.Fatalf("a stale lock note arrived: %s %s", n.method, n.params)
	case <-time.After(200 * time.Millisecond):
	}

	h.Lock() // also ends the grant, which sends autoAllow.off after locked
	if r := waitLockedNote(t, notes); r != "manual" {
		t.Fatalf("reason = %q", r)
	}
	h.mu.Lock()
	cur := h.lockGen.Load()
	h.mu.Unlock()
	sink("current", cur)
	sink("stale", stale)
	var locked []string
	for deadline := time.After(300 * time.Millisecond); ; {
		select {
		case n := <-notes:
			if n.method == "locked" {
				locked = append(locked, string(n.params))
			}
			continue
		case <-deadline:
		}
		break
	}
	if !reflect.DeepEqual(locked, []string{`{"reason":"current"}`}) {
		t.Fatalf("locked notes after a lock = %v, want only the current one", locked)
	}
}

// A run cancelled by a manual Lock did not "run while locked": after pressing
// Lock to cut the AI off, the next unlock must not report it.
func TestManualLockDuringRunIsNotCountedAsRanLocked(t *testing.T) {
	be := newBlockExec()
	h, _ := newHub(t, be)
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
		errc <- err
	}()
	waitInflight(t, h, "vis", 1)
	idleNow(h)
	h.Lock()
	if err := <-errc; !errors.Is(err, ErrCancelledRunning) {
		t.Fatalf("err = %v, want ErrCancelledRunning", err)
	}
	if got := h.TakeRanLocked(); len(got) != 0 {
		t.Fatalf("TakeRanLocked = %+v, want empty", got)
	}
}

func TestFailedRunUnderSoftLockIsNotCounted(t *testing.T) {
	h, _ := newHub(t, &fakeExec{err: errors.New("boom")})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); err == nil {
		t.Fatal("the run did not fail")
	}
	if got := h.TakeRanLocked(); len(got) != 0 {
		t.Fatalf("TakeRanLocked = %+v, want empty", got)
	}
}

// The sweep re-resolves a grant only when the reload replaced the vault file.
// A key file that vanishes while the file is unchanged would end the grant if
// every tick resolved it again.
func TestSoftLockSweepDoesNotReresolveUnchangedFile(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	key := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := config.Server{Name: "kf", Host: "h", Port: 22, User: "u", Auth: "agent", KeyPath: key, HostKey: "SHA256:abc", AIVisible: true}
	addServer(t, h, path, s)
	if err := h.SetAutoAllow("kf", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	h.sweepGrants(time.Now()) // sweptFile is nil after the soft lock began: this one checks every grant once
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	h.sweepGrants(time.Now())
	h.sweepGrants(time.Now())
	if grantOf(h, "kf") == nil {
		t.Fatal("the sweep re-resolved a grant although the vault file did not change")
	}
	// A changed file does re-check: hiding the server ends the grant.
	if err := config.Update(path, testMK, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == "kf" {
				f.Servers[i].AIVisible = false
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.sweepGrants(time.Now())
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
}

// hideInFile hides name in the vault file on disk, as an outside edit.
func hideInFile(t *testing.T, path, name string) {
	t.Helper()
	if err := config.Update(path, testMK, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				f.Servers[i].AIVisible = false
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// status, MCP calls and SetAutoAllow reload too: the sweep must compare with
// the file the grants were last checked against, not with its own "before".
func TestSoftLockSweepSeesFileReloadedByOthers(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	h.sweepGrants(time.Now()) // the first sweep of a soft lock sets sweptFile
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("state after a clean sweep = %+v", got)
	}
	hideInFile(t, path, "vis")
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	h.sweepGrants(time.Now())
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "server changed") {
		t.Fatalf("no autoAllowOff/server changed: %v", recs)
	}
}

// Exec's first resolve fails for a granted host that is hidden: the grant
// ends, as autoStart ends it, and the AI sees the same error as for a
// server that does not exist.
func TestExecEndsGrantOfHiddenServer(t *testing.T) {
	for _, soft := range []bool{true, false} {
		name := "unlocked"
		if soft {
			name = "soft"
		}
		t.Run(name, func(t *testing.T) {
			h, path := newHub(t, &fakeExec{})
			if err := h.SetAutoAllow("vis", "forever"); err != nil {
				t.Fatal(err)
			}
			if soft {
				idleNow(h)
			}
			hideInFile(t, path, "vis")
			if err := h.Reload(); err != nil {
				t.Fatal(err)
			}
			_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
			_, missing := h.Exec(context.Background(), ExecRequest{Server: "nope", Command: "ls"})
			if err == nil || missing == nil || err.Error() != serverNotFound("vis").Error() || missing.Error() != serverNotFound("nope").Error() {
				t.Fatalf("hidden %v, missing %v", err, missing)
			}
			if grantOf(h, "vis") != nil {
				t.Fatal("the grant of a hidden server is still armed")
			}
			if soft {
				if got := stateOf(h); got != (lockState{}) {
					t.Fatalf("state = %+v, want hard lock", got)
				}
			}
			_, recs := readAudit(t, path)
			if !hasAutoAllowOff(recs, "server changed") {
				t.Fatalf("no autoAllowOff/server changed: %v", recs)
			}
		})
	}
}

// A granted host whose resolve fails ends its grant too.
func TestExecEndsGrantWhenResolveFails(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	key := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	addServer(t, h, path, config.Server{Name: "kf", Host: "h", Port: 22, User: "u", Auth: "agent", KeyPath: key, HostKey: "SHA256:abc", AIVisible: true})
	if err := h.SetAutoAllow("kf", "15m"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "kf", Command: "ls"}); !errors.Is(err, ErrConnFailed) {
		t.Fatalf("err = %v, want ErrConnFailed", err)
	}
	if grantOf(h, "kf") != nil {
		t.Fatal("the grant of a server that no longer resolves is still armed")
	}
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "server changed") {
		t.Fatalf("no autoAllowOff/server changed: %v", recs)
	}
}

// The Lock button (spec 2026-10-03): with a live grant it soft-locks, as the
// idle lock does, and the granted host keeps running.
func TestLockKeepAutoWithGrantSoftLocks(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, config.Server{Name: "two", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true})
	locks := captureLocks(h)
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	h.LockKeepAuto()
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("state = %+v, want soft lock", got)
	}
	wantLock(t, locks, "manual")
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["action"] != "softLock" || last["reason"] != "manual" || !reflect.DeepEqual(last["servers"], []any{"vis"}) {
		t.Fatalf("audit: %v", last)
	}
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); err != nil {
		t.Fatalf("exec on the granted host: %v", err)
	}
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "two", Command: "ls"}); !errors.Is(err, ErrLocked) {
		t.Fatalf("exec on a host with no grant: %v, want ErrLocked", err)
	}
	// Pressed again: nothing changes and nothing is written.
	h.LockKeepAuto()
	noLock(t, locks)
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("state after a second lock = %+v", got)
	}
	_, recs = readAudit(t, path)
	if n := countAction(recs, "softLock"); n != 1 {
		t.Fatalf("softLock records = %d, want 1", n)
	}
}

func TestLockKeepAutoWithoutGrantHardLocks(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	locks := captureLocks(h)
	h.LockKeepAuto()
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	wantLock(t, locks, "manual")
	_, recs := readAudit(t, path)
	if n := countAction(recs, "softLock"); n != 0 {
		t.Fatalf("softLock records = %d, want 0", n)
	}
}

// A grant past its deadline that the sweep has not reached yet is not live:
// it is ended with reason "expired" and must not keep the key.
func TestLockKeepAutoWithExpiredGrantHardLocks(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.grants["vis"].until = time.Now().Add(-time.Second)
	h.mu.Unlock()
	h.LockKeepAuto()
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "expired") {
		t.Fatalf("no autoAllowOff/expired: %v", recs)
	}
}

// One live grant plus one expired and unswept: soft lock, and only the live
// host is in the softLock record and in status.autoHosts.
func TestLockKeepAutoSoftLockListsOnlyLiveGrants(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, config.Server{Name: "two", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("two", "15m"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.grants["two"].until = time.Now().Add(-time.Second)
	h.mu.Unlock()
	h.LockKeepAuto()
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("state = %+v, want soft lock with one grant", got)
	}
	_, recs := readAudit(t, path)
	for _, r := range recs {
		if r["action"] == "softLock" {
			if s, _ := r["servers"].([]any); len(s) != 1 || s[0] != "vis" {
				t.Fatalf("softLock servers = %v, want [vis]", r["servers"])
			}
		}
	}
	if _, hosts := h.lockStatus(); len(hosts) != 1 || hosts[0] != "vis" {
		t.Fatalf("autoHosts = %v, want [vis]", hosts)
	}
}

// Lock (stopAuto) after the Lock button's soft lock is the stop: hard, every
// grant ended with reason "locked".
func TestLockAfterLockKeepAutoIsHard(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	h.LockKeepAuto()
	locks := captureLocks(h)
	h.Lock()
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("state = %+v, want hard lock", got)
	}
	wantLock(t, locks, "manual")
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "locked") {
		t.Fatalf("no autoAllowOff/locked: %v", recs)
	}
}

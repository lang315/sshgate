package hub

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
)

// setLimitInFile writes stored into the vault file's softLockLimit with a
// valid MAC (testMK), then the caller reloads.
func setLimitInFile(t *testing.T, path, stored string) {
	t.Helper()
	if err := config.Update(path, testMK, func(f *config.File) error { f.SoftLockLimit = stored; return nil }); err != nil {
		t.Fatal(err)
	}
}

// limitHub is a hub whose vault holds stored as its soft-lock limit, with a
// forever grant on "vis", soft-locked by the idle rule.
func limitHub(t *testing.T, stored string) (*Hub, string) {
	t.Helper()
	h, path := newHub(t, &fakeExec{})
	setLimitInFile(t, path, stored)
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	return h, path
}

// ageSoftLock moves the start of h's soft lock d into the past.
func ageSoftLock(h *Hub, d time.Duration) {
	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-d)
	h.mu.Unlock()
}

// wantSweepHardLocks runs one sweep and expects a hard lock with the grants
// ended and audited.
func wantSweepHardLocks(t *testing.T, h *Hub, path string) {
	t.Helper()
	h.sweepGrants(time.Now())
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("after the sweep: state = %+v, want hard lock", got)
	}
	_, recs := readAudit(t, path)
	if countAction(recs, "autoAllowOff") == 0 {
		t.Fatalf("no autoAllowOff record: %v", recs)
	}
}

func TestSoftLockLimitOf(t *testing.T) {
	for stored, want := range map[string]time.Duration{
		"": 24 * time.Hour, "3d": 72 * time.Hour, "7d": 168 * time.Hour, "none": 0, "2w": 24 * time.Hour,
	} {
		if got := softLockLimitOf(&config.File{SoftLockLimit: stored}); got != want {
			t.Errorf("softLockLimitOf(%q) = %v, want %v", stored, got, want)
		}
	}
	if got := softLockLimitOf(nil); got != 24*time.Hour {
		t.Errorf("softLockLimitOf(nil) = %v, want 24h", got)
	}
	for stored, want := range map[string]string{"": "24h", "24h": "24h", "3d": "3d", "7d": "7d", "none": "none", "2w": "24h"} {
		if got := softLockWire(stored); got != want {
			t.Errorf("softLockWire(%q) = %q, want %q", stored, got, want)
		}
	}
	for _, bad := range []string{"", "1d", "24H", "None", "0"} {
		if _, ok := softLockLimits[bad]; ok {
			t.Errorf("softLockLimits accepts %q", bad)
		}
	}
}

func TestSoftLockLimitThreeDays(t *testing.T) {
	h, _ := limitHub(t, "3d")
	locks := captureLocks(h)

	ageSoftLock(h, 72*time.Hour-time.Second)
	h.lockIfIdle(time.Minute)
	if got := stateOf(h); !got.soft {
		t.Fatalf("one second short of 3 days: state = %+v, want soft", got)
	}
	noLock(t, locks)

	ageSoftLock(h, 72*time.Hour)
	h.lockIfIdle(time.Minute)
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("at 3 days: state = %+v, want hard lock", got)
	}
	wantLock(t, locks, "softLockLimit")
}

func TestSoftLockNoLimit(t *testing.T) {
	h, _ := limitHub(t, "none")
	ageSoftLock(h, 365*24*time.Hour)
	h.lockIfIdle(time.Minute)
	if got := stateOf(h); got != (lockState{soft: true, key: true, grants: 1}) {
		t.Fatalf("a year into a no-limit soft lock: state = %+v, want soft with its grant", got)
	}
	h.mu.Lock()
	locked := h.aiLockedLocked("vis")
	h.mu.Unlock()
	if locked {
		t.Fatal("vis locked to the AI under a no-limit soft lock")
	}
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); err != nil {
		t.Fatalf("auto exec under a no-limit soft lock: %v", err)
	}
}

// The limit is read from deps.File at the check, not copied when the soft
// lock begins: a soft lock entered without enterSoftLocked (the test helper)
// is still bounded, and a value set while unlocked applies.
func TestSoftLockLimitReadLive(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	softLockForTest(h)
	ageSoftLock(h, 24*time.Hour)
	h.mu.Lock()
	past := h.aiLockedLocked("vis")
	h.mu.Unlock()
	if !past {
		t.Fatal("default limit: vis not locked at 24 hours")
	}

	unlockForTest(h)
	setLimitInFile(t, path, "none")
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	softLockForTest(h)
	ageSoftLock(h, 24*time.Hour)
	h.mu.Lock()
	past = h.aiLockedLocked("vis")
	h.mu.Unlock()
	if past {
		t.Fatal("limit none set while unlocked: vis still locked at 24 hours")
	}
}

func TestSoftLockLimitUnknownValueIsDefault(t *testing.T) {
	h, _ := limitHub(t, "2w")
	ageSoftLock(h, 24*time.Hour)
	h.lockIfIdle(time.Minute)
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("unknown limit at 24 hours: state = %+v, want hard lock", got)
	}
}

// An outside edit of softLockLimit under soft lock fails the MAC on the
// sweep's reload: every grant ends and the hub hard-locks.
func TestSoftLockLimitTamperUnderSoftLock(t *testing.T) {
	h, path := limitHub(t, "")
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f.SoftLockLimit = "none" // no key: MAC left stale
	f.Revision++
	b, _ := json.Marshal(f)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	wantSweepHardLocks(t, h, path)
}

// A MAC-valid older copy of the vault written back under soft lock (here one
// whose limit is "none") must not raise the limit: any reload sees a lower
// Revision and refuses it, and the next sweep ends every grant and hard-locks.
func TestSoftLockLimitReplayUnderSoftLock(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	// Armed first, so the old copy also holds the forever flag and passes the
	// sweep's per-grant stale check.
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetSoftLockLimit("none"); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SetSoftLockLimit("24h"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	if got := stateOf(h); !got.soft {
		t.Fatalf("after idleNow: state = %+v, want soft", got)
	}
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	// status, the MCP door and the file/term/tunnel paths reload before any
	// sweep runs; none of them may take the older copy.
	if err := h.Reload(); err == nil {
		t.Fatal("Reload under soft lock accepted an older vault copy")
	}
	h.mu.Lock()
	limit := h.deps.File.SoftLockLimit
	h.mu.Unlock()
	if limit != "" {
		t.Fatalf("in-memory limit = %q after the replay, want the last copy's 24h", limit)
	}
	wantSweepHardLocks(t, h, path)
}

func TestSetSoftLockLimitWritesFileAndAudit(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetSoftLockLimit("none"); err != nil {
		t.Fatal(err)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.SoftLockLimit != "none" {
		t.Fatalf("file softLockLimit = %q, want none", f.SoftLockLimit)
	}
	if err := h.SetSoftLockLimit("none"); err != nil { // unchanged: no write, no record
		t.Fatal(err)
	}
	_, recs := readAudit(t, path)
	if n := countAction(recs, "softLockLimit"); n != 1 {
		t.Fatalf("softLockLimit records = %d, want 1: %v", n, recs)
	}
	for _, r := range recs {
		if r["action"] == "softLockLimit" && (r["limit"] != "none" || r["kind"] != "config") {
			t.Fatalf("record = %v, want kind config, limit none", r)
		}
	}
	if err := h.SetSoftLockLimit("24h"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "softLockLimit") {
		t.Fatalf("24h left the field in the file: %s", raw)
	}
}

func TestSetSoftLockLimitNeedsUnlockedVault(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	idleNow(h) // soft lock
	if err := h.SetSoftLockLimit("none"); !errors.Is(err, ErrLocked) {
		t.Fatalf("under soft lock: err = %v, want ErrLocked", err)
	}
	h.Lock() // hard lock
	if err := h.SetSoftLockLimit("none"); !errors.Is(err, ErrLocked) {
		t.Fatalf("under hard lock: err = %v, want ErrLocked", err)
	}

	nv, _ := newHubAt(t, &config.File{Version: 1}, nil)
	if err := nv.SetSoftLockLimit("none"); !errors.Is(err, errNoVault) {
		t.Fatalf("no vault: err = %v, want errNoVault", err)
	}
}

func TestUIDoorSoftLockLimit(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	ctx := context.Background()
	for _, p := range []any{
		map[string]any{"limit": "1d"}, map[string]any{"limit": 5}, map[string]any{"limit": nil},
		map[string]any{"Limit": "3d"}, map[string]any{"limit": "3d", "x": 1}, map[string]any{}, nil,
	} {
		err := c.Call(ctx, "settings.setSoftLockLimit", p, nil)
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != -32602 {
			t.Fatalf("%v: want -32602, got %v", p, err)
		}
	}
	var st struct {
		SoftLockLimit *string `json:"softLockLimit"`
	}
	if err := c.Call(ctx, "status", nil, &st); err != nil || st.SoftLockLimit == nil || *st.SoftLockLimit != "24h" {
		t.Fatalf("status before: %v %v", err, st.SoftLockLimit)
	}
	if err := c.Call(ctx, "settings.setSoftLockLimit", map[string]any{"limit": "7d"}, nil); err != nil {
		t.Fatal(err)
	}
	st.SoftLockLimit = nil
	if err := c.Call(ctx, "status", nil, &st); err != nil || st.SoftLockLimit == nil || *st.SoftLockLimit != "7d" {
		t.Fatalf("status after: %v %v", err, st.SoftLockLimit)
	}

	// A newer sshgate's value reports as the limit in force, never raw.
	setLimitInFile(t, path, "2w")
	st.SoftLockLimit = nil
	if err := c.Call(ctx, "status", nil, &st); err != nil || st.SoftLockLimit == nil || *st.SoftLockLimit != "24h" {
		t.Fatalf("status with an unknown value: %v %v", err, st.SoftLockLimit)
	}

	h.Lock()
	st.SoftLockLimit = nil
	if err := c.Call(ctx, "status", nil, &st); err != nil || st.SoftLockLimit != nil {
		t.Fatalf("status while locked: %v, softLockLimit = %v, want absent", err, st.SoftLockLimit)
	}
}

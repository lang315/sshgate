package hub

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/config"
)

// setLimitInFile writes stored into the vault file's softLockLimit with a
// valid MAC (testMK), then the caller reloads.
func setLimitInFile(t *testing.T, path, stored string) {
	t.Helper()
	if err := config.Update(path, testMK, func(f *config.File) error { f.SoftLockLimit = stored; return nil }); err != nil {
		t.Fatal(err)
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
	for stored, want := range map[string]string{"": "24h", "3d": "3d", "7d": "7d", "none": "none", "2w": "24h"} {
		if got := softLockWire(stored); got != want {
			t.Errorf("softLockWire(%q) = %q, want %q", stored, got, want)
		}
	}
	for wire, want := range map[string]string{"24h": "", "3d": "3d", "7d": "7d", "none": "none"} {
		if got, ok := softLockStored(wire); !ok || got != want {
			t.Errorf("softLockStored(%q) = %q, %v; want %q, true", wire, got, ok, want)
		}
	}
	for _, bad := range []string{"", "1d", "24H", "None", "0"} {
		if _, ok := softLockStored(bad); ok {
			t.Errorf("softLockStored(%q) accepted", bad)
		}
	}
}

func TestSoftLockLimitThreeDays(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	setLimitInFile(t, path, "3d")
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	locks := captureLocks(h)

	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-72*time.Hour + time.Second)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
	if got := stateOf(h); !got.soft {
		t.Fatalf("one second short of 3 days: state = %+v, want soft", got)
	}
	noLock(t, locks)

	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-72 * time.Hour)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("at 3 days: state = %+v, want hard lock", got)
	}
	wantLock(t, locks, "softLockLimit")
}

func TestSoftLockNoLimit(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	setLimitInFile(t, path, "none")
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-365 * 24 * time.Hour)
	h.mu.Unlock()
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
	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-24 * time.Hour)
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
	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-24 * time.Hour)
	past = h.aiLockedLocked("vis")
	h.mu.Unlock()
	if past {
		t.Fatal("limit none set while unlocked: vis still locked at 24 hours")
	}
}

func TestSoftLockLimitUnknownValueIsDefault(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	setLimitInFile(t, path, "2w")
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
	h.mu.Lock()
	h.softLockedAt = time.Now().Round(0).Add(-24 * time.Hour)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("unknown limit at 24 hours: state = %+v, want hard lock", got)
	}
}

// An outside edit of softLockLimit under soft lock fails the MAC on the
// sweep's reload: every grant ends and the hub hard-locks.
func TestSoftLockLimitTamperUnderSoftLock(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	idleNow(h)
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
	h.sweepGrants(time.Now())
	if got := stateOf(h); got != (lockState{}) {
		t.Fatalf("after the sweep: state = %+v, want hard lock", got)
	}
	_, recs := readAudit(t, path)
	if countAction(recs, "autoAllowOff") == 0 {
		t.Fatalf("no autoAllowOff record: %v", recs)
	}
}

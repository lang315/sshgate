# Soft-Lock Limit Setting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the fixed 24-hour soft-lock ceiling with a vault setting (24h default, 3d, 7d, none) that the author sets from a new Settings dialog.

**Architecture:**
- The limit is a new MAC-covered field, `config.File.SoftLockLimit`.
- The hub reads it live from the verified `deps.File` at its two ceiling checks, through one helper. There is no new hub state, so an absent field means 24 hours: fail closed.
- A new UI-door request writes the field, and `status` reports it while unlocked.
- The desktop app adds a gear button and a `SettingsDialog`, and the auto-allow consent text follows the setting.

**Tech Stack:** Go (internal/hub, internal/config, internal/broker), TypeScript + React 19 (desktop renderer), vitest (`renderToStaticMarkup`, node env), Playwright e2e on Electron.

**Spec:** `docs/superpowers/specs/2026-10-12-soft-lock-limit-design.md`

## Global Constraints

- Wire values: `"24h"`, `"3d"`, `"7d"`, `"none"`.
- Stored values: absent (`""`) for 24h, then `"3d"`, `"7d"`, `"none"`.
- A stored value the hub does not know reads as 24 hours.
- Durations: 24h = `24 * time.Hour`, 3d = `72 * time.Hour`, 7d = `168 * time.Hour`, none = no limit.
- `config.File` field: `SoftLockLimit string \`json:"softLockLimit,omitempty"\``.
- UI-door request: `settings.setSoftLockLimit {limit}`.
  - Params are strict: key `limit` only. Any other value is -32602.
  - It needs an unlocked vault (`writeKeyLocked`).
  - It writes the audit record `{kind:"config", action:"softLockLimit", limit:<wire>}`, and writes nothing when the value is unchanged.
- `status.softLockLimit`: the wire value in force, present only while `deps.MasterKey != nil`.
- `ProtocolVersion` / `PROTOCOL_VERSION`: 11 → 12, changed in Go and TS in the same task.
- `vault.create` clears `SoftLockLimit`.
- No new hub state field: read `h.deps.File` at the check.
- UI copy, verbatim:
  - Label: "Keep running while locked".
  - Options: "24 hours (default)", "3 days", "7 days", "No limit".
  - Help text for 24h, 3d and 7d: "Then the app locks fully and auto-allow stops. Forever grants wait for Resume."
  - Help text for none: "Auto-allow stops only with Stop auto-allow and lock, a restart or a crash."
  - Notice for a higher limit: "While the app is locked, the AI keeps running on hosts with auto-allow for up to <3 days|7 days>. Any program running as your user can use that access too."
  - Notice for none: "While the app is locked, the AI keeps running on hosts with auto-allow for as long as the app runs. Any program running as your user can use that access too."
  - After the notice: "On auto-allow now:" and the host list, or "No host is on auto-allow now. The limit applies from the next grant."
- No typed confirmation in the Settings dialog.
- Save is mouse-only and disabled for `ALLOW_DELAY_MS` (500) after the dialog opens or the notice changes. Cancel is the form submit (Enter) and has autoFocus. Esc also cancels.
- Renderer: CSS tokens only, no raw colours. The renderer calls the hub only on real UI actions, never on a timer.
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never put a session URL in a commit.

## Review Focus

1. **The dialog opened before `status.softLockLimit` arrives.** It must not treat the stored limit as 24h: lowering from `none` to `24h` would read as "same", and Save would stay disabled. Expected: the gear button is disabled until `status.softLockLimit` is known (Task 4, e2e in Task 5).
2. **Save clicked twice, or Save clicked while a save is in flight.** Expected: one request. `saveAllowed` is false while busy (Task 3 unit test).
3. **A grant ends or starts while the dialog is open.** The host list in the notice changes. Expected: the 500 ms Save delay restarts, so the click cannot land on a button that moved (Task 3 unit test on `settingsChangeKey`).
4. **A save that fails** (`ErrLocked` from a lock racing the click, or a reload error). Expected: the error shows in the dialog, the choice is kept, and Save waits out the delay again (Task 3 unit test: an error changes the key).
5. **A vault file written by a newer sshgate, with an unknown `softLockLimit`.** Expected: the hub enforces 24h and `status` reports `"24h"`, never the raw string (Task 1 and Task 2 Go tests).

---

### Task 1: Read the soft-lock limit from the vault

**Files:**
- Modify: `internal/config/store.go:35-41` (`File` struct)
- Modify: `internal/hub/softlock.go:99-134` (`maxSoftLock`, `aiLockedLocked`)
- Modify: `internal/hub/idle.go:41-66` (`lockIfIdle` comment and check)
- Modify: `internal/hub/hub.go:125` (comment on `softLockedAt`)
- Create: `internal/hub/softlocklimit_test.go`

**Interfaces:**
- Consumes: `softLockForTest`, `idleNow`, `captureLocks`, `wantLock`, `noLock`, `stateOf`, `lockState`, `newHub`, `readAudit`, `countAction`, `fakeExec`, `testMK` (existing test helpers in package `hub`).
- Produces:
  - `config.File.SoftLockLimit string`
  - `func softLockLimitOf(f *config.File) time.Duration` (0 = no limit)
  - `func softLockWire(stored string) string`
  - `func softLockStored(wire string) (string, bool)`
  - `func (h *Hub) pastLimitLocked(now time.Time) bool`
  - `func setLimitInFile(t *testing.T, path, stored string)` (test helper)

- [ ] **Step 1: Write the failing tests**

Create `internal/hub/softlocklimit_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/hub -run 'TestSoftLockLimit|TestSoftLockNoLimit' -v`
Expected: build failure: `f.SoftLockLimit undefined`, `undefined: softLockLimitOf`.

- [ ] **Step 3: Add the field to `config.File`**

In `internal/config/store.go`, change the `File` struct to:

```go
type File struct {
	Version       int      `json:"version"`
	Revision      int      `json:"revision"`
	KDF           *KDF     `json:"kdf,omitempty"`
	MAC           string   `json:"mac,omitempty"`
	Servers       []Server `json:"servers"`
	SoftLockLimit string   `json:"softLockLimit,omitempty"` // "" (24h), "3d", "7d" or "none"; see hub/softlock.go
}
```

`computeMAC` marshals the whole struct, so the field is MAC-covered with no other change.

- [ ] **Step 4: Add the helpers and use them at both checks**

In `internal/hub/softlock.go`, replace the `maxSoftLock` block (comment and const, lines 99-102) with:

```go
// maxSoftLock is the default soft-lock limit: how long a key and a grant
// outlive the human's last input when the vault sets no other.
const maxSoftLock = 24 * time.Hour

// softLockLimits maps the vault's softLockLimit to a duration; 0 is no limit.
var softLockLimits = map[string]time.Duration{"": maxSoftLock, "3d": 72 * time.Hour, "7d": 168 * time.Hour, "none": 0}

// softLockLimitOf is f's soft-lock limit. A value this hub does not know (a
// newer sshgate's) reads as the default: fail closed, to the shortest limit.
func softLockLimitOf(f *config.File) time.Duration {
	if f != nil {
		if d, ok := softLockLimits[f.SoftLockLimit]; ok {
			return d
		}
	}
	return maxSoftLock
}

// softLockWire is the UI door's name for a stored value: "24h" for the
// default and for any value this hub does not know.
func softLockWire(stored string) string {
	if _, ok := softLockLimits[stored]; !ok || stored == "" {
		return "24h"
	}
	return stored
}

// softLockStored is the stored value for a UI-door value; false for anything
// but 24h, 3d, 7d and none.
func softLockStored(wire string) (string, bool) {
	switch wire {
	case "24h":
		return "", true
	case "3d", "7d", "none":
		return wire, true
	}
	return "", false
}

// pastLimitLocked reports, with h.mu held, that the soft lock has reached the
// vault's limit. The limit is read from the verified deps.File here, never
// copied when the soft lock begins: an absent field is the 24-hour default,
// so a path that soft-locks some other way is still bounded.
func (h *Hub) pastLimitLocked(now time.Time) bool {
	d := softLockLimitOf(h.deps.File)
	return d != 0 && now.Sub(h.softLockedAt) >= d
}
```

Check that `softlock.go` already imports `github.com/lang315/sshgate/internal/config`. If not, add it.

In `aiLockedLocked` (the last line, `softlock.go:133`), replace:

```go
	return h.grants[name] == nil || time.Now().Round(0).Sub(h.softLockedAt) >= maxSoftLock
```

with:

```go
	return h.grants[name] == nil || h.pastLimitLocked(time.Now().Round(0))
```

In `internal/hub/idle.go`, in `lockIfIdle`, replace:

```go
		if time.Now().Round(0).Sub(h.softLockedAt) < maxSoftLock {
```

with:

```go
		if !h.pastLimitLocked(time.Now().Round(0)) {
```

In the doc comment above `lockIfIdle`, replace "A soft lock older than maxSoftLock becomes a hard lock that ends every grant." with "A soft lock that reaches the vault's soft-lock limit (pastLimitLocked) becomes a hard lock that ends every grant."

In the doc comment above `aiLockedLocked`, replace "or the soft lock has reached its ceiling" with "or the soft lock has reached the vault's limit (pastLimitLocked)".

In `internal/hub/hub.go:125`, change the comment `// wall clock (Round(0)), so time asleep counts toward maxSoftLock` to `// wall clock (Round(0)), so time asleep counts toward the soft-lock limit`.

- [ ] **Step 5: Run the new tests and the existing ceiling tests**

Run: `go test ./internal/hub -run 'TestSoftLockLimit|TestSoftLockNoLimit|TestSoftLockCeiling|TestExecPastCeilingBeforeTickIsLocked|TestSoftLockReloadStillChecksMAC' -v`
Expected: PASS. The existing ceiling tests still use `maxSoftLock` as the default.

- [ ] **Step 6: Run the whole hub and config packages**

Run: `go test -short ./internal/config ./internal/hub`
Expected: `ok` for both.

- [ ] **Step 7: Commit**

```bash
git add internal/config/store.go internal/hub/softlock.go internal/hub/idle.go internal/hub/hub.go internal/hub/softlocklimit_test.go
git commit -m "feat(hub): read the soft-lock limit from the vault

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `settings.setSoftLockLimit`, `status.softLockLimit`, protocol 12

**Files:**
- Create: `internal/hub/settings.go`
- Modify: `internal/broker/audit.go:38-54` (`ConfigRecord`)
- Modify: `internal/hub/hosts.go` (`CreateVault`'s update function)
- Modify: `internal/hub/uidoor.go` (`status` handler at ~169; new request after `servers.autoAllowCheck` at ~285)
- Modify: `internal/hub/idle.go:5` (`ProtocolVersion`)
- Modify: `desktop/src/shared/protocol.ts:42` (`Status`), `:129-134` (`REQUEST_METHODS`), `:141` (`PROTOCOL_VERSION`)
- Modify: `desktop/src/renderer/transport.ts` (the `hub` object)
- Test: `internal/hub/softlocklimit_test.go` (append), `internal/hub/hosts_test.go` (append)

**Interfaces:**
- Consumes: from Task 1, `softLockStored`, `softLockWire`, `config.File.SoftLockLimit`, `setLimitInFile`.
- Produces:
  - `func (h *Hub) SetSoftLockLimit(limit string) error`
  - `func (h *Hub) softLockLimitForUI() (string, bool)`
  - `broker.ConfigRecord.Limit string \`json:"limit,omitempty"\``
  - UI-door `settings.setSoftLockLimit`
  - TS `type SoftLockLimit = '24h' | '3d' | '7d' | 'none'` and `Status.softLockLimit?: SoftLockLimit`
  - `hub.setSoftLockLimit(limit: SoftLockLimit): Promise<void>`

- [ ] **Step 1: Write the failing Go tests**

Append to `internal/hub/softlocklimit_test.go`. Add `"errors"`, `"strings"`, `"github.com/lang315/sshgate/internal/rpc"` to its imports:

```go
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
```

(`status` reloads the store itself, which is why the `"2w"` case needs no `h.Reload()`.)

Append to `internal/hub/hosts_test.go`:

```go
func TestCreateVaultClearsSoftLockLimit(t *testing.T) {
	h, path := newHubAt(t, &config.File{Version: 1, SoftLockLimit: "none", Servers: []config.Server{
		{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent"}}}, nil)
	if err := h.CreateVault("longenough"); err != nil {
		t.Fatal(err)
	}
	f, _ := config.Load(path)
	if f.SoftLockLimit != "" {
		t.Fatalf("softLockLimit survived vault creation: %q", f.SoftLockLimit)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/hub -run 'TestSetSoftLockLimit|TestUIDoorSoftLockLimit|TestCreateVaultClearsSoftLockLimit' -v`
Expected: build failure: `h.SetSoftLockLimit undefined`.

- [ ] **Step 3: Add `Limit` to `ConfigRecord`**

In `internal/broker/audit.go`, add `softLockLimit` to the `Action` comment's list, and add this as the last field of `ConfigRecord`:

```go
	Limit          string    `json:"limit,omitempty"`   // softLockLimit: the new limit, 24h, 3d, 7d or none
```

- [ ] **Step 4: Write `internal/hub/settings.go`**

```go
package hub

import (
	"fmt"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
)

// SetSoftLockLimit stores the soft-lock limit (24h, 3d, 7d or none) in the
// vault, under its MAC. It needs an unlocked vault, like every store write,
// so the limit cannot change during a soft lock. Setting the value already
// stored writes nothing. A failed reload after the write is returned; the
// write stands.
func (h *Hub) SetSoftLockLimit(limit string) error {
	stored, ok := softLockStored(limit)
	if !ok {
		return fmt.Errorf("unknown soft-lock limit %q", limit)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	key, err := h.writeKeyLocked()
	if err != nil {
		return err
	}
	defer clear(key)
	if h.deps.File.SoftLockLimit == stored {
		return nil
	}
	if err := config.Update(h.o.StorePath, key, func(f *config.File) error {
		f.SoftLockLimit = stored
		return nil
	}); err != nil {
		return err
	}
	h.auditConfig(broker.ConfigRecord{Action: "softLockLimit", Limit: limit})
	return h.reloadLocked()
}

// softLockLimitForUI is the limit in force as a UI-door value, only while
// unlocked: while hard-locked the file's MAC is unchecked.
func (h *Hub) softLockLimitForUI() (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deps.MasterKey == nil || h.deps.File == nil {
		return "", false
	}
	return softLockWire(h.deps.File.SoftLockLimit), true
}
```

- [ ] **Step 5: Clear the field in `CreateVault`**

In `internal/hub/hosts.go`, `CreateVault`, inside the `config.Update` function, change:

```go
		f.KDF = &k
		return nil
```

to:

```go
		f.KDF = &k
		f.SoftLockLimit = "" // a KDF-less file was never MAC'd: a planted limit must not survive
		return nil
```

- [ ] **Step 6: Wire the UI door and bump the protocol**

In `internal/hub/uidoor.go`, in the `status` handler, after the `autoHosts` block, add:

```go
		if l, ok := h.softLockLimitForUI(); ok {
			st["softLockLimit"] = l
		}
```

After the `servers.autoAllowCheck` request, add:

```go
	req("settings.setSoftLockLimit", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Limit string `json:"limit"`
		}
		if err := strictParams(raw, &p, "limit"); err != nil {
			return nil, err
		}
		if _, ok := softLockStored(p.Limit); !ok {
			return nil, &rpc.Error{Code: -32602, Message: "limit must be 24h, 3d, 7d, or none"}
		}
		return empty, h.SetSoftLockLimit(p.Limit)
	})
```

In `internal/hub/idle.go`, change `const ProtocolVersion = 11` to `const ProtocolVersion = 12`.

- [ ] **Step 7: Update the desktop protocol and transport in the same commit**

In `desktop/src/shared/protocol.ts`:
- Above `export interface Status`, add:
  ```ts
  // The soft-lock limit (spec 2026-10-12-soft-lock-limit-design.md).
  export type SoftLockLimit = '24h' | '3d' | '7d' | 'none'
  ```
- Add `softLockLimit?: SoftLockLimit` as the last member of `Status`.
- Add `'settings.setSoftLockLimit'` after `'audit.read'` in `REQUEST_METHODS`.
- Change `export const PROTOCOL_VERSION = 11` to `export const PROTOCOL_VERSION = 12`.

In `desktop/src/renderer/transport.ts`, import `SoftLockLimit` with the other protocol types, and add after `setAutoAllow`:

```ts
  setSoftLockLimit: async (limit: SoftLockLimit) => { await call('settings.setSoftLockLimit', { limit }) },
```

- [ ] **Step 8: Run the tests**

Run: `go test ./internal/hub -run 'TestSetSoftLockLimit|TestUIDoorSoftLockLimit|TestCreateVaultClearsSoftLockLimit|TestUIDoorHello' -v && go test -short ./internal/... ./cmd/...`
Expected: PASS. The hello test compares with `ProtocolVersion`.

Run: `cd desktop && npm run typecheck && npx vitest run`
Expected: typecheck clean; all tests pass.

- [ ] **Step 9: Commit**

```bash
git add internal/broker/audit.go internal/hub/settings.go internal/hub/hosts.go internal/hub/uidoor.go internal/hub/idle.go internal/hub/softlocklimit_test.go internal/hub/hosts_test.go desktop/src/shared/protocol.ts desktop/src/renderer/transport.ts
git commit -m "feat(hub): settings.setSoftLockLimit and status.softLockLimit, protocol 12

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Renderer logic for the limit, and the consent text

**Files:**
- Create: `desktop/src/renderer/settings.ts`
- Create: `desktop/test/settings.test.ts`
- Modify: `desktop/src/renderer/autoallow.ts` (add `limitText`)
- Modify: `desktop/src/renderer/AutoAllowDialog.tsx` (prop `limit`, forever copy)
- Modify: `desktop/src/renderer/HostEditor.tsx:31-37` and `:222-225` (pass `softLockLimit`)
- Modify: `desktop/src/renderer/App.tsx` (pass `status?.softLockLimit` to `HostEditor`)
- Test: `desktop/test/AutoAllowDialog.test.ts` (append)

**Interfaces:**
- Consumes: from Task 2, `SoftLockLimit`, `Status.softLockLimit`; `ALLOW_DELAY_MS` from `approvals.ts`.
- Produces:
  - `settings.ts`:
    - `LIMITS: { value: SoftLockLimit; label: string }[]`
    - `type LimitChange = 'same' | 'lower' | 'higher' | 'none'`
    - `limitChange(current: SoftLockLimit, chosen: SoftLockLimit): LimitChange`
    - `limitHelp(l: SoftLockLimit): string`
    - `limitNotice(l: SoftLockLimit): string`
    - `settingsChangeKey(chosen: SoftLockLimit, hosts: string[], error?: string): string`
    - `saveAllowed(o: { change: LimitChange; busy: boolean; openedAt: number; changedAt: number; now: number }): boolean`
  - `autoallow.ts`: `limitText(limit: SoftLockLimit | undefined): string` (the whole forever paragraph)
  - `AutoAllowDialog` prop `limit?: SoftLockLimit`
  - `HostEditor` prop `softLockLimit?: SoftLockLimit`

- [ ] **Step 1: Write the failing tests**

Create `desktop/test/settings.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { LIMITS, limitChange, limitHelp, limitNotice, saveAllowed, settingsChangeKey } from '../src/renderer/settings'
import { limitText } from '../src/renderer/autoallow'

describe('limitChange', () => {
  it('orders 24h < 3d < 7d < none', () => {
    expect(limitChange('24h', '24h')).toBe('same')
    expect(limitChange('24h', '3d')).toBe('higher')
    expect(limitChange('3d', '7d')).toBe('higher')
    expect(limitChange('7d', '3d')).toBe('lower')
    expect(limitChange('none', '24h')).toBe('lower')
    expect(limitChange('24h', 'none')).toBe('none')
    expect(limitChange('none', 'none')).toBe('same')
  })
})

describe('copy', () => {
  it('lists the four options in order with the spec labels', () => {
    expect(LIMITS.map((l) => [l.value, l.label])).toEqual([
      ['24h', '24 hours (default)'], ['3d', '3 days'], ['7d', '7 days'], ['none', 'No limit']])
  })
  it('help text depends on whether there is a limit', () => {
    for (const l of ['24h', '3d', '7d'] as const) expect(limitHelp(l)).toBe('Then the app locks fully and auto-allow stops. Forever grants wait for Resume.')
    expect(limitHelp('none')).toBe('Auto-allow stops only with Stop auto-allow and lock, a restart or a crash.')
  })
  it('the notice names the new limit and the same-user risk', () => {
    expect(limitNotice('7d')).toBe('While the app is locked, the AI keeps running on hosts with auto-allow for up to 7 days. Any program running as your user can use that access too.')
    expect(limitNotice('none')).toBe('While the app is locked, the AI keeps running on hosts with auto-allow for as long as the app runs. Any program running as your user can use that access too.')
  })
})

describe('saveAllowed', () => {
  const base = { change: 'higher' as const, busy: false, openedAt: 1000, changedAt: 0, now: 1000 }
  it('waits ALLOW_DELAY_MS after the dialog opens or the notice changes', () => {
    expect(saveAllowed({ ...base, now: 1499 })).toBe(false)
    expect(saveAllowed({ ...base, now: 1500 })).toBe(true)
    expect(saveAllowed({ ...base, changedAt: 2000, now: 2499 })).toBe(false)
    expect(saveAllowed({ ...base, changedAt: 2000, now: 2500 })).toBe(true)
  })
  it('is false with nothing to save, or while a save is in flight', () => {
    expect(saveAllowed({ ...base, change: 'same', now: 9999 })).toBe(false)
    expect(saveAllowed({ ...base, busy: true, now: 9999 })).toBe(false)
    expect(saveAllowed({ ...base, change: 'lower', now: 9999 })).toBe(true)
  })
})

describe('settingsChangeKey', () => {
  it('changes with the choice, the host list and an error, so Save waits again', () => {
    const k = settingsChangeKey('none', ['box'])
    expect(settingsChangeKey('7d', ['box'])).not.toBe(k)
    expect(settingsChangeKey('none', ['box', 'web'])).not.toBe(k)
    expect(settingsChangeKey('none', ['box'], 'Vault is locked')).not.toBe(k)
    expect(settingsChangeKey('none', ['box'])).toBe(k)
  })
})

describe('limitText', () => {
  it('keeps today\'s 24-hour copy for 24h and an unknown limit', () => {
    const t = limitText('24h')
    expect(t).toContain('the AI keeps running on this host for up to 24 hours.')
    expect(t).toContain('After a restart, the 24-hour limit, or Stop auto-allow and lock, it waits for you to click Resume.')
    expect(limitText(undefined)).toBe(t)
  })
  it('names 3 and 7 days', () => {
    expect(limitText('3d')).toContain('for up to 3 days.')
    expect(limitText('3d')).toContain('the 3-day limit')
    expect(limitText('7d')).toContain('the 7-day limit')
  })
  it('drops the limit under none', () => {
    const t = limitText('none')
    expect(t).toContain('the AI keeps running on this host while the app is locked, with no time limit.')
    expect(t).toContain('After a restart or Stop auto-allow and lock, it waits for you to click Resume.')
    expect(t).not.toMatch(/24|hour|day/)
  })
})
```

Append to `desktop/test/AutoAllowDialog.test.ts`, inside the `describe('AutoAllowDialog', …)` block:

```ts
  it('takes the forever copy from the soft-lock limit', () => {
    expect(dialog({ mode: 'forever', limit: 'none' })).toContain('with no time limit')
    expect(dialog({ mode: 'forever', limit: 'none' })).not.toContain('24 hours')
    expect(dialog({ mode: 'forever', limit: '7d' })).toContain('for up to 7 days')
  })
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd desktop && npx vitest run test/settings.test.ts test/AutoAllowDialog.test.ts`
Expected: FAIL: cannot resolve `../src/renderer/settings`, and `limitText` is not exported.

- [ ] **Step 3: Write `desktop/src/renderer/settings.ts`**

```ts
import type { SoftLockLimit } from '../shared/protocol'
import { ALLOW_DELAY_MS } from './approvals'

// The Settings dialog's rules (spec 2026-10-12-soft-lock-limit-design.md,
// "UI and UX"). Pure: the dialog only renders what these return.

export const LIMITS: { value: SoftLockLimit; label: string }[] = [
  { value: '24h', label: '24 hours (default)' }, { value: '3d', label: '3 days' },
  { value: '7d', label: '7 days' }, { value: 'none', label: 'No limit' },
]
const RANK: Record<SoftLockLimit, number> = { '24h': 1, '3d': 3, '7d': 7, none: Infinity }
const PHRASE: Record<Exclude<SoftLockLimit, 'none'>, string> = { '24h': '24 hours', '3d': '3 days', '7d': '7 days' }

export type LimitChange = 'same' | 'lower' | 'higher' | 'none'

export function limitChange(current: SoftLockLimit, chosen: SoftLockLimit): LimitChange {
  if (chosen === current) return 'same'
  if (chosen === 'none') return 'none'
  return RANK[chosen] > RANK[current] ? 'higher' : 'lower'
}

export const limitHelp = (l: SoftLockLimit) => l === 'none'
  ? 'Auto-allow stops only with Stop auto-allow and lock, a restart or a crash.'
  : 'Then the app locks fully and auto-allow stops. Forever grants wait for Resume.'

export const limitNotice = (l: SoftLockLimit) =>
  `While the app is locked, the AI keeps running on hosts with auto-allow ${l === 'none' ? 'for as long as the app runs' : `for up to ${PHRASE[l]}`}. Any program running as your user can use that access too.`

// Anything that moves Save restarts its delay: the choice (it swaps the
// notice), the host list in the notice, and an error line appearing.
export const settingsChangeKey = (chosen: SoftLockLimit, hosts: string[], error?: string) => JSON.stringify([chosen, hosts, error ?? null])

export function saveAllowed(o: { change: LimitChange; busy: boolean; openedAt: number; changedAt: number; now: number }): boolean {
  if (o.change === 'same' || o.busy) return false
  return o.now - Math.max(o.openedAt, o.changedAt) >= ALLOW_DELAY_MS
}
```

- [ ] **Step 4: Add `limitText` to `autoallow.ts`**

Add `SoftLockLimit` to the type import from `'../shared/protocol'` at the top of `desktop/src/renderer/autoallow.ts`, then add after `dialogChangeKey`:

```ts
// The forever grant's paragraph in AutoAllowDialog, from the vault's soft-lock
// limit (status.softLockLimit; absent reads as the 24-hour default).
export function limitText(limit: SoftLockLimit | undefined): string {
  const l = limit ?? '24h'
  const keep = l === 'none'
    ? 'the AI keeps running on this host while the app is locked, with no time limit'
    : `the AI keeps running on this host for up to ${({ '24h': '24 hours', '3d': '3 days', '7d': '7 days' })[l]}`
  const after = l === 'none'
    ? 'After a restart or Stop auto-allow and lock'
    : `After a restart, the ${({ '24h': '24-hour', '3d': '3-day', '7d': '7-day' })[l]} limit, or Stop auto-allow and lock`
  return `Stays set after restart. Locking the app, by hand or from inactivity, does not stop it: ${keep}. To stop it, use Stop auto-allow and lock on the unlock screen, Stop on the host card, or Stop all auto-allow. ${after}, it waits for you to click Resume.`
}
```

For `'24h'` this returns exactly today's string, so the existing AutoAllowDialog test assertions still hold.

- [ ] **Step 5: Use it in `AutoAllowDialog` and pass the limit through**

In `desktop/src/renderer/AutoAllowDialog.tsx`:
- Import `SoftLockLimit` with the other types, and `limitText` from `'./autoallow'`.
- Add `limit` to the destructured props: `{ server, mode, …, onEnable, onCancel, limit }`.
- Add `limit?: SoftLockLimit` to the props type.
- In the `mode === 'forever'` branch, replace the whole `'Stays set after restart. … click Resume.'` string literal with `limitText(limit)`.

In `desktop/src/renderer/HostEditor.tsx`:
- Import the type `SoftLockLimit`.
- Add `softLockLimit` to the destructured props, and `softLockLimit?: SoftLockLimit` to the props type.
- Add `limit={softLockLimit}` to the `<AutoAllowDialog …>` element.

In `desktop/src/renderer/App.tsx`, add `softLockLimit={status?.softLockLimit}` to the `<HostEditor …>` element.

- [ ] **Step 6: Run the tests and typecheck**

Run: `cd desktop && npx vitest run && npm run typecheck`
Expected: all tests pass; typecheck clean.

- [ ] **Step 7: Commit**

```bash
git add desktop/src/renderer/settings.ts desktop/test/settings.test.ts desktop/src/renderer/autoallow.ts desktop/src/renderer/AutoAllowDialog.tsx desktop/src/renderer/HostEditor.tsx desktop/src/renderer/App.tsx desktop/test/AutoAllowDialog.test.ts
git commit -m "feat(desktop): soft-lock limit rules and consent copy

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The Settings dialog and its entry point

**Files:**
- Create: `desktop/src/renderer/SettingsDialog.tsx`
- Create: `desktop/test/SettingsDialog.test.ts`
- Modify: `desktop/src/renderer/icons.tsx` (add `SettingsIcon`)
- Modify: `desktop/src/renderer/styles.css` (new classes after `.banner-danger`, ~line 300)
- Modify: `desktop/src/renderer/App.tsx` (state, gear button, dialog)

**Interfaces:**
- Consumes:
  - from Task 3: `LIMITS`, `limitChange`, `limitHelp`, `limitNotice`, `saveAllowed`, `settingsChangeKey`
  - from Task 2: `hub.setSoftLockLimit`, `Status.softLockLimit`
  - existing: `ListChanges`, `blockKeyboardActivation` (`approvals.ts`), `ChevronDownIcon` (`icons.tsx`)
- Produces: `SettingsDialog({ limit, hosts, onSave, onCancel })`, `SettingsIcon`.

- [ ] **Step 1: Write the failing render test**

Create `desktop/test/SettingsDialog.test.ts`:

```ts
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { SettingsDialog } from '../src/renderer/SettingsDialog'

const render = (over: Partial<Parameters<typeof SettingsDialog>[0]> = {}) => renderToStaticMarkup(createElement(SettingsDialog, {
  limit: '24h', hosts: [], onSave: async () => {}, onCancel: () => {}, ...over,
}))

describe('SettingsDialog', () => {
  it('opens on the value in force, with no notice and a disabled, mouse-only Save', () => {
    const html = render({ limit: '7d' })
    expect(html).toContain('role="dialog"')
    expect(html).toContain('>Settings</h3>')
    expect(html).toContain('>Auto-allow</h4>')
    expect(html).toContain('Keep running while locked')
    expect(html).toMatch(/<option value="7d" selected="">7 days<\/option>/)
    expect(html).not.toContain('banner-wait')
    expect(html).not.toContain('banner-danger')
    const save = html.match(/<button[^>]*>Save<\/button>/)![0]
    expect(save).toMatch(/disabled=""/)
    expect(save).toMatch(/tabindex="-1"/)
    expect(html).toMatch(/<button type="submit" class="btn primary"[^>]*>Cancel/)
  })
  it('marks the select red when the value in force is No limit', () => {
    expect(render({ limit: 'none' })).toContain('class="select danger"')
    expect(render({ limit: 'none' })).toContain('Auto-allow stops only with Stop auto-allow and lock, a restart or a crash.')
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd desktop && npx vitest run test/SettingsDialog.test.ts`
Expected: FAIL: cannot resolve `../src/renderer/SettingsDialog`.

- [ ] **Step 3: Add the icon**

In `desktop/src/renderer/icons.tsx`, after `ListIcon`, add:

```tsx
export const SettingsIcon = icon(<><circle cx="12" cy="12" r="3" /><circle cx="12" cy="12" r="7" /><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M4.9 19.1l2.1-2.1M17 7l2.1-2.1" /></>)
```

- [ ] **Step 4: Write `SettingsDialog.tsx`**

```tsx
import { useEffect, useId, useRef, useState } from 'react'
import type { SoftLockLimit } from '../shared/protocol'
import { blockKeyboardActivation, ListChanges } from './approvals'
import { ChevronDownIcon, SettingsIcon } from './icons'
import { LIMITS, limitChange, limitHelp, limitNotice, saveAllowed, settingsChangeKey } from './settings'

// App settings (spec 2026-10-12-soft-lock-limit-design.md, "UI and UX"): one
// dialog of sections, each a heading and label/control rows; Save and Cancel
// cover the whole dialog. Today: Auto-allow, one row.
export function SettingsDialog({ limit, hosts, onSave, onCancel }: {
  limit: SoftLockLimit; hosts: string[]
  onSave: (limit: SoftLockLimit) => Promise<void>; onCancel: () => void
}) {
  const id = useId()
  const [chosen, setChosen] = useState(limit)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [openedAt] = useState(Date.now())
  const [now, setNow] = useState(openedAt)
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 100); return () => clearInterval(t) }, [])
  const change = limitChange(limit, chosen)
  // Computed during render, as AutoAllowDialog does: an effect would leave one
  // painted frame where the notice moved Save but Save was still enabled.
  const key = settingsChangeKey(chosen, hosts, error)
  const changes = useRef<ListChanges>(null)
  changes.current ??= new ListChanges(key, openedAt)
  changes.current.setKey(key, Date.now())
  const ok = saveAllowed({ change, busy, openedAt, changedAt: changes.current.at, now })
  const notice = change === 'higher' || change === 'none'
  const save = async () => {
    if (!ok) return
    setBusy(true); setError(undefined)
    try { await onSave(chosen) } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  return (
    <div className="modal" role="dialog" aria-modal="true" aria-labelledby={`${id}-title`}
      onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }}>
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onCancel() }}>
        <div className="dialog-title"><span className="dialog-icon"><SettingsIcon /></span><h3 id={`${id}-title`}>Settings</h3></div>
        <section className="settings-section" aria-labelledby={`${id}-auto`}>
          <h4 id={`${id}-auto`}>Auto-allow</h4>
          <div className="settings-row">
            <label htmlFor={`${id}-limit`}>Keep running while locked</label>
            <span className={'select' + (chosen === 'none' ? ' danger' : '')}>
              <select id={`${id}-limit`} value={chosen} onChange={(e) => setChosen(e.target.value as SoftLockLimit)}>
                {LIMITS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
              </select>
              <ChevronDownIcon />
            </span>
            <p className="muted">{limitHelp(chosen)}</p>
          </div>
        </section>
        <div aria-live="polite">
          {notice && (
            <div className={change === 'none' ? 'banner-danger' : 'banner-wait'}>
              <p>{`${limitNotice(chosen)} ${hosts.length ? 'On auto-allow now:' : 'No host is on auto-allow now. The limit applies from the next grant.'}`}</p>
              {hosts.length > 0 && <ul className="files-sample mono">{hosts.map((h) => <li key={h}>{h}</li>)}</ul>}
            </div>
          )}
        </div>
        {error && <p className="error">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className={'btn' + (notice ? ' danger-outline' : '')} tabIndex={-1} disabled={!ok}
            onKeyDown={blockKeyboardActivation} onClick={save}>Save</button>
          <button type="submit" className="btn primary" autoFocus>Cancel<kbd aria-hidden="true">↵</kbd></button>
        </div>
      </form>
    </div>
  )
}
```

The `select` initial value: `renderToStaticMarkup` renders `selected=""` on the matching `<option>`, which the test checks.

- [ ] **Step 5: Add the styles**

In `desktop/src/renderer/styles.css`, after the `.banner-danger` rule, add:

```css
.banner-wait { background: var(--wait-tint); color: var(--wait); border-radius: 8px; padding: 10px 12px; }
.select.danger select { border-color: var(--danger); color: var(--danger); }
.settings-section { display: flex; flex-direction: column; gap: 10px; }
.settings-section h4 { font-size: 13px; font-weight: 600; padding-bottom: 8px; border-bottom: 1px solid var(--divider); }
.settings-row { display: grid; grid-template-columns: 1fr 180px; gap: 6px 16px; align-items: center; }
.settings-row > .muted { grid-column: 1 / -1; }
@media (max-width: 451px) { .settings-row { grid-template-columns: 1fr; } }
```

(451 px: the dialog is `calc(100% - 32px)` wide, so it is under 420 px below a 452 px window.)

- [ ] **Step 6: Wire it into `App.tsx`**

- Imports: add `SettingsIcon` to the `./icons` import, and `import { SettingsDialog } from './SettingsDialog'`.
- State, next to `editing`: `const [settingsOpen, setSettingsOpen] = useState(false)`.
- After `useEffect(() => { if (ready) setEverReady(true) }, [ready])`, add the line below. A lock unmounts the dialog, and an unlock must not bring back an unsaved one:
  ```tsx
  useEffect(() => { if (!ready) setSettingsOpen(false) }, [ready])
  ```
- In `actions`, between `<ThemeControl …/>` and the Lock button. It stays disabled until `status` has a limit (Review Focus 1):
  ```tsx
  <button type="button" className="btn" aria-label="Settings" title="Settings" disabled={!status?.softLockLimit}
    onClick={() => setSettingsOpen(true)}><SettingsIcon /></button>
  ```
- After the `{ready && mismatch && (…)}` block, add:
  ```tsx
  {ready && settingsOpen && status?.softLockLimit && (
    <SettingsDialog limit={status.softLockLimit} hosts={[...autoHostsSet].sort()}
      onSave={async (l) => { await hub.setSoftLockLimit(l); await refresh(); setSettingsOpen(false) }}
      onCancel={() => setSettingsOpen(false)} />
  )}
  ```

- [ ] **Step 7: Run tests, typecheck, build**

Run: `cd desktop && npx vitest run && npm run typecheck && npm run build`
Expected: all tests pass, typecheck clean, the build succeeds.

- [ ] **Step 8: Commit**

```bash
git add desktop/src/renderer/SettingsDialog.tsx desktop/test/SettingsDialog.test.ts desktop/src/renderer/icons.tsx desktop/src/renderer/styles.css desktop/src/renderer/App.tsx
git commit -m "feat(desktop): Settings dialog with the soft-lock limit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: E2e: Settings end to end

**Files:**
- Create: `desktop/e2e/settings.spec.ts`

**Interfaces:**
- Consumes: `launch`, `Launched` (`e2e/launch.ts`); `unlockDone`, `editAuto`, `enable` (`e2e/autoHelpers.ts`); the dialog names "Settings" and "Auto-allow AI commands on box"; the label "Keep running while locked".
- Produces: nothing for later tasks.

- [ ] **Step 1: Write the spec**

```ts
import { test, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launch, type Launched } from './launch'
import { editAuto, enable, unlockDone } from './autoHelpers'

// The soft-lock limit setting (spec 2026-10-12-soft-lock-limit-design.md).
// The 24-hour wait itself is the exit gate, not an e2e test.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')
test.describe.configure({ mode: 'serial' })

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

type Rec = Record<string, any>
const store = (): Rec => JSON.parse(fs.readFileSync(path.join(l.tmp, 'store', 'servers.json'), 'utf8'))
const audit = (): Rec[] => fs.readFileSync(path.join(l.tmp, 'store', 'audit.jsonl'), 'utf8').trim().split('\n').map((s) => JSON.parse(s))

test('Settings sets No limit, stored in the vault', async () => {
  const win = await l.app.firstWindow()
  await expect(win.getByLabel('Master password')).toBeVisible()
  await expect(win.getByRole('button', { name: 'Settings' })).toHaveCount(0)
  await unlockDone(win)
  await editAuto(win, 'box', 'forever')
  await win.getByRole('dialog', { name: 'Auto-allow AI commands on box' }).getByLabel('Type box to confirm').fill('box')
  await enable(win, 'box')

  const gear = win.getByRole('button', { name: 'Settings' })
  await expect(gear).toBeEnabled()
  await gear.click()
  let d = win.getByRole('dialog', { name: 'Settings' })
  await expect(d.getByLabel('Keep running while locked')).toHaveValue('24h')
  await win.keyboard.press('Enter') // Cancel has focus and is the submit
  await expect(d).toBeHidden()

  await gear.click()
  d = win.getByRole('dialog', { name: 'Settings' })
  await d.getByLabel('Keep running while locked').selectOption('none')
  await expect(d.locator('.banner-danger')).toContainText('for as long as the app runs')
  await expect(d.locator('.banner-danger li')).toHaveText(['box'])
  const save = d.getByRole('button', { name: 'Save' })
  await expect(save).toBeDisabled()
  await win.waitForTimeout(600)
  await save.click()
  await expect(d).toBeHidden()

  expect(store().softLockLimit).toBe('none')
  expect(audit().filter((r) => r.kind === 'config' && r.action === 'softLockLimit').pop()).toMatchObject({ limit: 'none' })
})

test('the value survives a lock, and the consent text follows it', async () => {
  const win = await l.app.firstWindow()
  await win.getByRole('button', { name: 'Lock', exact: true }).click()
  await expect(win.getByRole('button', { name: 'Settings' })).toBeHidden()
  await unlockDone(win)

  await win.getByRole('button', { name: 'Settings' }).click()
  const d = win.getByRole('dialog', { name: 'Settings' })
  await expect(d.getByLabel('Keep running while locked')).toHaveValue('none')
  await win.keyboard.press('Escape')
  await expect(d).toBeHidden()

  await editAuto(win, 'box', 'off')
  await expect(win.getByRole('dialog', { name: 'Host editor' })).toBeHidden()
  await editAuto(win, 'box', 'forever')
  const consent = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await expect(consent).toContainText('with no time limit')
  await expect(consent).not.toContainText('24 hours')
  await consent.getByRole('button', { name: 'Cancel' }).click()
})
```

- [ ] **Step 2: Run it**

Run: `cd desktop && npm run build && npx playwright test e2e/settings.spec.ts`
On Linux, prefix with `xvfb-run -a`.
Expected: 2 passed.

If `editAuto(win, 'box', 'off')` leaves the editor open (Save with Off may not close it), read `HostEditor.tsx`'s save path. Then change only the test's wait, not the app.

- [ ] **Step 3: Run the existing auto-allow and lock specs**

Run: `cd desktop && npx playwright test e2e/autoallow.spec.ts e2e/softlock.spec.ts e2e/manuallock.spec.ts e2e/smoke.spec.ts`
Expected: all pass. A protocol-12 hub with a protocol-12 app is required.

- [ ] **Step 4: Commit**

```bash
git add desktop/e2e/settings.spec.ts
git commit -m "test(e2e): Settings sets the soft-lock limit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Documentation

**Files:**
- Modify: `README.md` (lines ~90, ~95, ~102, ~109)
- Modify: `PRODUCT.md:26`
- Modify: `CLAUDE.md` (UI-door paragraph and Auto-allow paragraph)
- Modify: `docs/superpowers/specs/2026-10-02-soft-lock-design.md:4` (Status line)

**Interfaces:**
- Consumes: the finished behaviour from Tasks 1-5.
- Produces: nothing.

- [ ] **Step 1: README**

Make these replacements. Each old string appears once.
- In **Locking**: "until the grant ends, 24 hours pass, or you press" → "until the grant ends, the soft-lock limit set in **Settings** (⚙, 24 hours by default; 3 days, 7 days or no limit) passes, or you press".
- In **Auto-allow**: "the timer running out, or 24 hours with the app locked all end it." → "the timer running out, or the soft-lock limit from **Settings** passing while the app is locked (24 hours by default) all end it."
- "A grant \"Until turned off\" is paused after a restart, the 24-hour limit, or" → "A grant \"Until turned off\" is paused after a restart, the soft-lock limit, or".
- "- The app having been locked for 24 hours." → "- The app having been locked for the soft-lock limit set in **Settings** (24 hours by default). With **No limit**, only **Stop auto-allow and lock**, a restart or a crash ends it."

After that list, add this paragraph:

```markdown
The soft-lock limit is stored in the vault, under its MAC, and only changes while the app is unlocked. An older sshgate refuses a vault whose limit is not 24 hours (it reads the field as tampering): set it back to 24 hours before downgrading.
```

- [ ] **Step 2: PRODUCT.md**

Replace "the AI keeps running on that host for up to 24 hours." with "the AI keeps running on that host for up to the limit set in Settings (24 hours by default; 3 days, 7 days or no limit)."

- [ ] **Step 3: CLAUDE.md**

- Replace "`tunnels.stop` (a notification), `audit.read`; the hub also pushes" with "`tunnels.stop` (a notification), `audit.read`, `settings.setSoftLockLimit` (`{limit}`: `24h`, `3d`, `7d`, `none`; strict params; needs an unlocked vault; writes `config.File.SoftLockLimit` under the MAC and a `softLockLimit` config record); the hub also pushes".
- Replace "protocol 11, `ProtocolVersion` in `idle.go`" with "protocol 12, `ProtocolVersion` in `idle.go`".
- Replace "`maxSoftLock` (24 h, wall clock, a constant) hard-locks and ends every grant with reason \"soft lock limit\" (`locked` reason `softLockLimit`)." with "The vault's soft-lock limit (`config.File.SoftLockLimit`: absent = 24 h, the default `maxSoftLock`; `3d`, `7d`, `none`; an unknown value reads as 24 h; wall clock), read live from `deps.File` by `pastLimitLocked` at its two checks (`aiLockedLocked`, `lockIfIdle`) and never copied into hub state, hard-locks and ends every grant with reason \"soft lock limit\" (`locked` reason `softLockLimit`). `status.softLockLimit` reports it only while unlocked; `vault.create` clears it."
- In the `desktop/` Auto-allow bullet, after "…shows a dismissible count on every tab.", add: "A gear button (Settings, between the theme control and Lock, enabled once `status.softLockLimit` is known) opens `SettingsDialog.tsx` (rules in `settings.ts`): sections of label/control rows, today one select for the soft-lock limit; raising it shows an amber (`.banner-wait`) or, for No limit, red notice listing the hosts on auto-allow; no typed confirmation; Save is mouse-only and waits 500 ms after the notice changes; Enter and Esc cancel. `AutoAllowDialog`'s forever text comes from `limitText(status.softLockLimit)`."

- [ ] **Step 4: Point the soft-lock spec here**

In `docs/superpowers/specs/2026-10-02-soft-lock-design.md`, line 4, append to the Status line: " The 24-hour ceiling became a vault setting on 2026-10-12: see `2026-10-12-soft-lock-limit-design.md`."

- [ ] **Step 5: Verify nothing else still claims a fixed 24 hours**

Run: `grep -rn "24 hours\|24-hour\|maxSoftLock" README.md PRODUCT.md CLAUDE.md desktop/src | grep -v node_modules`
Expected: only lines that say "24 hours by default", `limitText`/`settings.ts` copy, and `maxSoftLock` as the default constant.

- [ ] **Step 6: Final verification**

Run: `go vet ./... && go test -race ./... && (cd desktop && npm run typecheck && npx vitest run)`
Expected: all pass. The integration tests skip themselves without Docker; say so in the PR evidence if they do.

- [ ] **Step 7: Commit**

```bash
git add README.md PRODUCT.md CLAUDE.md docs/superpowers/specs/2026-10-02-soft-lock-design.md
git commit -m "docs: soft-lock limit setting

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

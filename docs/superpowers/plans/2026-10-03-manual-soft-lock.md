# Lock Button Keeps Auto-Allow Running Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Lock button soft-locks when a grant is live, so AI work on auto-allowed hosts keeps running; `lock {stopAuto: true}` is the hard stop.

**Architecture:** A new `Hub.LockKeepAuto()` reuses the idle path's `enterSoftLocked`; `Hub.Lock()` stays the hard lock and becomes what `stopAuto: true` calls. The UI door's `lock` decodes `{stopAuto?}` strictly and picks one of the two. The desktop sends `stopAuto: true` from the unlock screen's stop button and from renderer-crash recovery.

**Tech Stack:** Go (internal/hub), TypeScript/React/Electron (desktop), vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-03-manual-soft-lock-design.md` (amends `docs/superpowers/specs/2026-10-02-soft-lock-design.md`).

## Global Constraints

- `lock {}` or `lock` with no params: soft lock when at least one live grant (no deadline, or deadline after now), else hard lock as today. Already soft- or hard-locked: nothing.
- `lock {stopAuto: true}`: hard lock, every grant ended with reason "locked". Already hard-locked: nothing.
- Every `locked` notification from `lock` has reason `manual`.
- Params decoded with `strictParams`: any key but `stopAuto` is -32602; a non-boolean `stopAuto` is -32602.
- The `softLock` config audit record carries `reason`: `"manual"` or `"idle"`.
- `ProtocolVersion` 9 → 10 (Go `internal/hub/idle.go`) and `PROTOCOL_VERSION` 9 → 10 (`desktop/src/shared/protocol.ts`), in the same commit.
- The Lock button's `title` while a host is on auto-allow: `Auto-allow keeps running while locked`.
- Renderer-crash recovery and the unlock screen's **Stop auto-allow and lock** send `{stopAuto: true}`.
- Security posture is intentional (fail closed). Every rule of the soft-lock spec applies to a manual soft lock unchanged; do not add a soft-lock special case anywhere else.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

---

### Task 1: Hub — `lock` soft-locks with a live grant, `stopAuto` stops

**Files:**
- Modify: `internal/hub/hub.go` (after `func (h *Hub) Lock()`, line ~259)
- Modify: `internal/hub/softlock.go:44-50` (`enterSoftLocked`)
- Modify: `internal/hub/idle.go` (`ProtocolVersion`, and the `enterSoftLocked` call at line ~80)
- Modify: `internal/hub/uidoor.go:195-198` (the `lock` handler)
- Modify: `desktop/src/shared/protocol.ts:141` (`PROTOCOL_VERSION`)
- Test: `internal/hub/softlock_test.go`, `internal/hub/softlock_door_test.go`

**Interfaces:**
- Produces: `func (h *Hub) LockKeepAuto()`; `func (h *Hub) enterSoftLocked(now time.Time, reason string) lockNote`; UI-door `lock` params `{stopAuto?: boolean}`; protocol 10.
- Unchanged: `func (h *Hub) Lock()` is still the hard lock that ends every grant.

- [ ] **Step 1: Write the failing tests** — append to `internal/hub/softlock_test.go`:

```go
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
// it must not keep the key.
func TestLockKeepAutoWithExpiredGrantHardLocks(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
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
```

In the existing `TestIdleWithGrantSoftLocks`, change the audit check to also require the idle reason:

```go
			if last["action"] != "softLock" || last["reason"] != "idle" || !reflect.DeepEqual(last["servers"], []any{"vis"}) {
```

Append to `internal/hub/softlock_door_test.go`:

```go
// The door's lock: {} keeps auto-allow running, {stopAuto: true} stops it,
// and nothing else is accepted.
func TestUIDoorLockParams(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	ctx := context.Background()
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []any{map[string]any{"stop": true}, map[string]any{"stopAuto": "yes"}, []any{}} {
		var rerr *rpc.Error
		if err := c.Call(ctx, "lock", bad, nil); !errors.As(err, &rerr) || rerr.Code != -32602 {
			t.Fatalf("lock %v: %v, want -32602", bad, err)
		}
	}
	if got := stateOf(h); got != (lockState{key: true, grants: 1}) {
		t.Fatalf("a refused lock changed the state: %+v", got)
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
```

If `softlock_door_test.go` lacks an import these need (`errors`, `context`, `rpc`), add it.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/hub -run 'TestLockKeepAuto|TestLockAfterLockKeepAuto|TestUIDoorLock|TestIdleWithGrantSoftLocks' -count=1`
Expected: build failure, `h.LockKeepAuto undefined`.

- [ ] **Step 3: Implement**

`internal/hub/softlock.go`, `enterSoftLocked` takes who locked and writes it to the audit record:

```go
func (h *Hub) enterSoftLocked(now time.Time, reason string) lockNote {
	h.autoKey, h.deps.MasterKey, h.softLockedAt = h.deps.MasterKey, nil, now
	h.lockGen.Add(1)
	h.unlocked.Store(false)
	h.auditConfig(broker.ConfigRecord{Action: "softLock", Servers: h.grantNamesLocked(), Reason: reason})
	return h.lockNoteLocked()
}
```

Update the comment above it, if it says only the idle lock calls it, to name both callers (`lockIfIdle` and `LockKeepAuto`).

`internal/hub/idle.go`: the call in `lockIfIdle` becomes `note := h.enterSoftLocked(time.Now().Round(0), "idle")`, and `ProtocolVersion` becomes `10`.

`internal/hub/hub.go`, directly after `func (h *Hub) Lock()`:

```go
// LockKeepAuto is the Lock button: with a live grant it soft-locks, as the
// idle lock does, so the AI keeps running on the granted hosts; with none it
// is Lock. Already locked, soft or hard, it changes nothing. Lock is the
// stop (the UI door's lock with stopAuto).
func (h *Hub) LockKeepAuto() {
	h.mu.Lock()
	if h.deps.MasterKey == nil {
		h.mu.Unlock()
		return
	}
	now := time.Now().Round(0)
	live := false
	for _, g := range h.grants {
		if g.until.IsZero() || now.Before(g.until) {
			live = true
			break
		}
	}
	if !live {
		h.mu.Unlock()
		h.Lock()
		return
	}
	note := h.enterSoftLocked(now, "manual")
	h.mu.Unlock()
	if note != nil {
		note("manual")
	}
}
```

(An expired grant left in `h.grants` is ended by `Lock`'s `endAllGrantsLocked`.)

`internal/hub/uidoor.go`, the `lock` handler:

```go
	req("lock", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			StopAuto bool `json:"stopAuto"`
		}
		if len(raw) > 0 && string(raw) != "null" {
			if err := strictParams(raw, &p, "stopAuto"); err != nil {
				return nil, err
			}
		}
		if p.StopAuto {
			h.Lock()
		} else {
			h.LockKeepAuto()
		}
		return empty, nil
	})
```

`desktop/src/shared/protocol.ts`: `export const PROTOCOL_VERSION = 10`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/hub && go test -race -count=1 ./internal/hub`
Expected: `ok`. Also `grep -rn 'ProtocolVersion\|protocol.*9' internal/hub/*_test.go` and fix any test that pins protocol 9.

- [ ] **Step 5: Commit**

```bash
git add internal/hub desktop/src/shared/protocol.ts
git commit -m "feat(hub): the Lock button soft-locks while a grant is live

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Desktop — stop sends `stopAuto`, Lock button title, e2e

**Files:**
- Modify: `desktop/src/renderer/transport.ts:47` (`lock`)
- Modify: `desktop/src/renderer/App.tsx` (`stopAndLock` ~line 211, the Lock button ~line 228)
- Modify: `desktop/src/renderer/shell.ts` (add `lockTitle`)
- Modify: `desktop/src/main/window.ts:57` (`recoverRenderer`'s lock call)
- Test: `desktop/test/shell.test.ts`, `desktop/test/window.test.ts`
- Create: `desktop/e2e/manuallock.spec.ts`

**Interfaces:**
- Consumes: UI-door `lock {stopAuto?: boolean}`, protocol 10 (Task 1).
- Produces: `hub.lock(stopAuto?: boolean): Promise<void>`; `lockTitle(autoHosts: number): string | undefined` in `shell.ts`.

- [ ] **Step 1: Write the failing unit tests**

`desktop/test/shell.test.ts` — add `lockTitle` to the existing import from `../src/renderer/shell` and append:

```ts
describe('lockTitle', () => {
  it('says auto-allow keeps running only while a host is on it', () => {
    expect(lockTitle(1)).toBe('Auto-allow keeps running while locked')
    expect(lockTitle(0)).toBeUndefined()
  })
})
```

`desktop/test/window.test.ts` — the fake hubs record only method names. In the first test that expects `['lock', 'term.closeAll', 'files.cancelAll', 'reload']` (line ~49), also record params and assert them. Read the test's fake hub first; the shape to add is:

```ts
const params: unknown[] = []
const hub = { call: async (m: string, p?: unknown) => { order.push(m); params.push(p) } }
// … existing recoverRenderer call and order assertion …
expect(params[0]).toEqual({ stopAuto: true }) // a crash stops auto-allow
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd desktop && npx vitest run test/shell.test.ts test/window.test.ts`
Expected: FAIL (`lockTitle` is not exported; params[0] is `{}`).

- [ ] **Step 3: Implement**

`desktop/src/renderer/shell.ts`, after `lockKind`:

```ts
// The Lock button's tooltip: with a host on auto-allow, Lock soft-locks and
// the AI keeps running there (spec 2026-10-03).
export const lockTitle = (autoHosts: number): string | undefined =>
  autoHosts > 0 ? 'Auto-allow keeps running while locked' : undefined
```

`desktop/src/renderer/transport.ts`:

```ts
  // stopAuto ends every auto-allow grant; without it a live grant keeps running behind the lock.
  lock: async (stopAuto = false) => { await call('lock', stopAuto ? { stopAuto: true } : {}) },
```

`desktop/src/renderer/App.tsx`:
- in `stopAndLock`: `try { await hub.lock(true); setUnlockError(undefined) }`
- the Lock button: `<button type="button" className="btn" title={lockTitle(autoN)} onClick={lock}><LockIcon />Lock</button>`, and add `lockTitle` to the existing import from `./shell`.

`desktop/src/main/window.ts`, in `recoverRenderer`: `const locked = await hub.call('lock', { stopAuto: true }, 5000).then(…` (the rest unchanged), with a comment `// stopAuto: a renderer crash ends auto-allow (soft-lock spec, Known limits)`.

- [ ] **Step 4: Run the unit tests**

Run: `cd desktop && npm run typecheck && npm test`
Expected: typecheck exit 0, all tests pass.

- [ ] **Step 5: Write the e2e spec** — create `desktop/e2e/manuallock.spec.ts`. It has its own launch with the default 15-minute idle lock, so the idle lock cannot fire first and the soft lock seen is the Lock button's.

```ts
import { test, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launch, type Launched } from './launch'
import { card, editAuto, enable, Mcp, unlockDone } from './autoHelpers'

// The Lock button keeps auto-allow running (spec 2026-10-03-manual-soft-lock-design.md),
// end to end through the real MCP bridge; the unlock screen's stop ends it.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')
test.describe.configure({ mode: 'serial' })

let l: Launched
let mcp: Mcp
test.beforeAll(async () => {
  l = await launch()
  mcp = new Mcp(path.join(l.tmp, 'bin', 'sshgate'), l.tmp)
  await mcp.call('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e-manuallock', version: '1' } })
  mcp.notify('notifications/initialized', {})
})
test.afterAll(async () => { mcp?.close(); await l?.close() })

const exec = (server: string, command: string) => mcp.tool('exec', { server, command, description: 'manual lock e2e' })
type Rec = Record<string, any>
const audit = (): Rec[] => fs.readFileSync(path.join(l.tmp, 'store', 'audit.jsonl'), 'utf8').trim().split('\n').map((s) => JSON.parse(s))

test('the Lock button keeps auto-allow running', async () => {
  const win = await l.app.firstWindow()
  await unlockDone(win)
  await editAuto(win, 'box', 'forever')
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await d.getByLabel('Type box to confirm').fill('box')
  await enable(win, 'box')
  await expect(card(win, 'box').locator('.chip.auto')).toHaveText('Auto ∞')

  const lock = win.getByRole('button', { name: 'Lock', exact: true })
  await expect(lock).toHaveAttribute('title', 'Auto-allow keeps running while locked')
  await lock.click()
  await expect(win.getByLabel('Master password')).toBeVisible()
  await expect(win.getByText('AI auto-allow is still running on: box')).toBeVisible()

  expect(await exec('box', 'echo manual-1')).toContain('echo manual-1')
  const recs = audit()
  const soft = recs.findIndex((r) => r.kind === 'config' && r.action === 'softLock')
  const ran = recs.findIndex((r) => r.command === 'echo manual-1')
  expect(recs[soft]).toMatchObject({ reason: 'manual', servers: ['box'] })
  expect(ran).toBeGreaterThan(soft)
  expect(recs[ran]).toMatchObject({ outcome: 'allowed', approval: 'auto' })
})

test('the unlock screen stop still ends it', async () => {
  const win = await l.app.firstWindow()
  await win.getByRole('button', { name: 'Stop auto-allow and lock' }).click()
  await expect(win.getByText('AI auto-allow is still running on: box')).toHaveCount(0)
  await expect(exec('box', 'echo manual-2')).rejects.toThrow(/Vault is locked/)
  expect(audit().filter((r) => r.kind === 'config' && r.action === 'autoAllowOff' && r.server === 'box').pop()!.reason).toBe('locked')
})
```

Before running, read `desktop/e2e/autoHelpers.ts` and `desktop/e2e/softlock.spec.ts`: confirm `editAuto`, `enable`, `card`, `unlockDone`, `Mcp` exist with these signatures and that `launch()` accepts no argument. The Lock button's accessible name may include its icon; if `{ name: 'Lock', exact: true }` does not match, use the selector the other e2e specs use for the Lock button (`grep -rn "'Lock'" desktop/e2e`).

- [ ] **Step 6: Run the e2e**

Run: `cd desktop && npm run build && npx playwright test e2e/manuallock.spec.ts e2e/softlock.spec.ts e2e/idle.spec.ts`
Expected: all pass. (`npx playwright test` alone does not rebuild the app; `npm run build` must come first.)

- [ ] **Step 7: Commit**

```bash
git add desktop
git commit -m "feat(desktop): the stop button and crash recovery send stopAuto

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Documentation

**Files:**
- Modify: `README.md`, `PRODUCT.md`, `CLAUDE.md`
- Modify: `docs/superpowers/specs/2026-10-03-manual-soft-lock-design.md` (Testing: the e2e lives in `desktop/e2e/manuallock.spec.ts`, its own launch with the default idle lock, so the idle lock cannot fire first)

- [ ] **Step 1: Find every statement to change**

Run: `grep -n -i 'manual lock\|lock button\|Lock button\|kill switch\|one stop\|ends every grant\|entered only by the idle\|idle lock fires with\|`lock`' README.md PRODUCT.md CLAUDE.md`

- [ ] **Step 2: Rewrite them** so that each says: the Lock button soft-locks while a grant is live (hard-locks with none); `lock {stopAuto: true}` is the stop, sent by the unlock screen's **Stop auto-allow and lock** and by renderer-crash recovery; the `softLock` audit record carries `reason` (`manual` or `idle`); the UI door is protocol 10. In CLAUDE.md also: the `lock` method in the UI-door method list takes `{stopAuto?}`; "`servers.setAutoAllow` is refused … (`off` included: `lock` is the one stop)" becomes "… `lock {stopAuto: true}` is the one stop"; add `desktop/e2e/manuallock.spec.ts` to the e2e list in Commands. PRODUCT.md's Capabilities line about **Stop auto-allow and lock** stays true; add nothing beyond what changed. Keep each file's style; touch only sentences that are now false.

- [ ] **Step 3: Check**

Run: `grep -n -i 'kill switch\|Manual Lock stays\|protocol 9' README.md PRODUCT.md CLAUDE.md`
Expected: no line that still claims the Lock button ends auto-allow or that the protocol is 9.

- [ ] **Step 4: Commit**

```bash
git add README.md PRODUCT.md CLAUDE.md docs/superpowers/specs/2026-10-03-manual-soft-lock-design.md
git commit -m "docs: the Lock button keeps auto-allow running

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

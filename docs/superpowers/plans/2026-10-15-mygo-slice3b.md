# mygo desktop, slice 3b (auto-allow and Settings) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The native mygo app arms, shows and stops auto-allow the way the Electron app does:
- the host editor's **Auto-allow** select and its two opt-ins, `Allow on root hosts` and `Also auto-allow sudo-exec`, with the timed-grant note and the refusal hint;
- `saveConfirm` and the **Auto-allow dialog**: the duration text, red warnings for newly ticked opt-ins, the hub's root check, the typed-name confirm for a newly chosen forever grant, a mouse-only Enable behind the 500 ms guard, Cancel back to the editor, and the `Saved, but auto-allow was not turned on:` alert;
- ticking chips (`Auto 42m`, `Auto ∞`, `Auto paused`), the card's **Stop auto-allow**, the tab dot;
- the **paused** banner (Hosts tab and AI column, Resume and Stop) and the **ran-while-locked** banner;
- **Settings**: the gear, the soft-lock limit select, the amber or red notice listing the hosts on auto-allow, a mouse-only Save, Enter and Esc cancel; `limitText` for the forever grant's paragraph;
- an MCP bridge test client, and the e2e specs `autoallow`, `autoallow-mcp`, `settings`, `manuallock` and `softlock` ported as Go flow tests against a real `sshgate hub`, the real `sshgate` bridge and `sshtestd`.

3a's interim `keptMode` goes; `saveConfirm` replaces it.

**Architecture:**
- **Layers.** Unchanged: `hubapi` (typed door calls), `model` (pure rules, their vitest cases as Go table tests), `view` (the `App` controller and its screens), `internal/hubtest` (sshgate, sshtestd and now the bridge, for tests).
- **Dialogs.** The Auto-allow dialog is a `ui.Modal` over the editor sheet; its state (`confirmState`) hangs on `editorState`, so a lock or hub stop that drops the editor drops it too. Settings is a `ui.Modal` whose state (`settingsState`) hangs on `App` and is dropped with the other dialogs. Both reuse the slice-1 guard: `model.ListChanges` keyed by what moves the buttons, `MouseButton` for the decisive button, `c.After` to redraw when the delay ends.
- **Clock.** Chips, the AI button's count, the card's Stop and every guard read the app's clock (`App.Now`); a redraw is requested with `c.After`, never by calling the hub.

**Tech Stack:** Go 1.27.1 (`app/` module), mygo `ui` (fork commit `25387f1`), the existing `hubapi`, `model`, `view`, `hubtest` packages, `sshgate hub` (UI door protocol 12, MCP door) and the `sshgate` stdio bridge.

**Spec:** `docs/superpowers/specs/2026-10-14-mygo-desktop-design.md` (Slices row 3; "Approval column" → "Same rules elsewhere"; "Door client" → Activity rule; "Testing" → "End to end: MCP bridge → hub → sshtestd"). Research notes, with file:line into the Electron sources and mygo: `/private/tmp/claude-501/-Users-lang-GolandProjects-github-com-lang315-ssh-mcp/1e1c2b02-5440-4e17-a378-848816a02d95/scratchpad/s3/{renderer,protocol,mygo}.md` (renderer.md §2, §4, §5, §6.4–6.9). The 3a plan, `docs/superpowers/plans/2026-10-15-mygo-slice3a.md`, Decision 1, lists where 3b plugs in. Every code block below was compiled and its tests run in a scratch clone of the repo at `95a8197`, in task order; the final tree passed the Gate (below) with and without `MYGO_GHOSTTY_VT`, and the six new flow tests passed three times in a row under `-race`.

## Global Constraints

- **Branch and scope.**
  - Work on branch `feat/mygo-slice3b` (stacked on `feat/mygo-slice3`, 3a).
  - Edit only `app/` and this plan. `internal/`, `cmd/sshgate`, `desktop/`, the protocol and the mygo fork are unchanged: this slice found no hub gap (Decision 13). Never touch `/Users/lang/GolandProjects/github.com/lang315/mygo`; the fork checkout `/Users/lang/GolandProjects/github.com/lang315/mygo-sshgate` (branch `sshgate`) is read-only.
  - Tests never use the real `~/.ssh`, the real vault `~/.config/sshgate`, or the installed `/Applications/sshgate.app`. Test hubs come from `hubtest` (temp `HOME`, `SSH_AUTH_SOCK=`, their own `--store` and `SSHGATE_RUNTIME_DIR`); the bridge gets the same `SSHGATE_RUNTIME_DIR`.
  - The packaged app reads no `SSHGATE_*` variable; nothing here adds one.
- **Wire.** UI door protocol 12, newline JSON-RPC. A hub error arrives as `*door.Error`, whose `Error()` is the hub's message verbatim; show it unchanged, through `model.DisplayText`.
- **Activity rule.** The app never calls a hub method on a timer. After a hub notification it calls only `status`.
  - Chips, the AI button's `auto N`, the card's Stop, the dialog's "ends at" and every 500 ms guard tick from the app's clock with `c.After` (a redraw), never with a hub call.
  - `autoAllow.ran` and `autoAllow.off` trigger no hub call (slice 1's `TestNotificationsCallOnlyStatus` keeps checking it).
  - Calls a user action starts: `servers.save` (Save or Enable), `servers.autoAllowCheck` (once, when Save opens the dialog), `servers.setAutoAllow` (card Stop, Resume, banner Stop, Stop all), `settings.setSoftLockLimit` (Settings Save). Completing them may call `servers` and `status`. Opening the editor, its folds, the select, the checkboxes, typing, opening Settings and choosing in it call nothing.
- **Replies.** Every reply to a user action is dropped after a hub stop or a lock (`a.ready(gen)`, Decision 5) and touches only the dialog it was made for (`ed.confirm == cf`, `a.settings == st`).
- **Guards.** `model.AllowDelay` (500 ms) gates Enable, Resume and Settings Save. Each is a `MouseButton` (never focusable, never pressed by a key) whose delay restarts when what the dialog or banner shows changes. The safe choice has the keyboard: Cancel is `AutoFocus`, so Enter cancels; Esc cancels (Settings: not while saving); Enter in the typed-name field cancels.
- **Select labels.** The app keeps its own value↔label tables (`model.AutoModes`, `model.Limits`) and never passes a hub value to `ui.Select`, whose text is its value (3a Decision 4).
- **Untrusted text.** Server names, hub errors, refusal reasons and host lists go through `model.DisplayText` before they are shown.
- **Source text.** Never put a raw invisible or bidi rune in Go source; write escapes (`"\u202e"`, `"\u25a0"`). The Write tool can turn an escape into the raw rune: after writing a file, byte-check it:
```bash
perl -CSD -ne 'print "$ARGV:$.\n" if /[\x{200B}-\x{200F}\x{2028}-\x{202E}\x{2066}-\x{2069}\x{FEFF}]/; close ARGV if eof' <files>
```
  It prints nothing when the files are clean.
- **Verbatim strings.** Character for character:
  - Editor: `Auto-allow`, `Off`, `15 min`, `30 min`, `60 min`, `2 h`, `4 h`, `Until turned off`, `Save the host and trust its key first`, `On for N more min — saving ends it; pick a duration to keep auto-allow on`, `Allow on root hosts`, `Lets a grant run on a root login, or a host with a stored su or sudo password.`, `Also auto-allow sudo-exec`, `sudo-exec runs without asking on a granted host, the same as plain exec.`, the refusals `no pinned host key`, `not visible to AI`, `root login`, `has an su password`, `has a sudo password`.
  - Dialog: `Auto-allow AI commands on <name>`, `Auto-allow is not available here: <reason>`, the consent, sudo, root and duration sentences in Task 3's code, `Type <name> to confirm`, `Enable`, `Cancel`.
  - Cards and banners: `Stop auto-allow <name>` (tooltip `Stop auto-allow`), `auto-allow on`, `Auto-allow is paused on <a, b>. Once resumed, the AI keeps running there while the app is locked.`, `Resume`, `Stop`, `While the app was locked the AI ran N command(s) on a, b. See the Audit tab.`, `Dismiss`, `Could not <stop|resume> auto-allow on <names>: <message>`.
  - Settings: `Settings`, `Auto-allow`, `Keep running while locked`, `24 hours (default)`, `3 days`, `7 days`, `No limit`, `limitHelp`, `limitNotice` and `limitText` as in Task 2, ` On auto-allow now:`, ` No host is on auto-allow now. The limit applies from the next grant.`, `Save`, `Cancel`.
- **Tests.**
  - The Gate, run from the repo root after every task (all must pass, `gofmt -l` must print nothing):
```bash
cd app
go vet ./... && test -z "$(gofmt -l .)"
go test -race -count=1 ./...
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=1 ./...
GOOS=linux go vet ./... && GOOS=linux go test -c -o /dev/null ./view
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```
  - No known flakes: `TestStaleStatusReplyIgnored`, flaky at `f680115`, was made deterministic in 3a (`b963453`). Any failure is real; report it, do not rerun it away.
  - Flow tests against a real hub are `//go:build unix`, skip under `-short`, and check the hub's `audit.jsonl`. A flow that opens a terminal calls `needLib(t)` and skips without `MYGO_GHOSTTY_VT`.
  - No fixed sleep over 500 ms in a test: poll with `until` (10 s). Change fake-door state with `d.set(func() { ... })`. Pass a guard by moving the fake clock (`pass(c, tt)` in view tests, `f.pass()` in flows), never by sleeping.
  - mygo's `Tester.HasText` matches a substring; `Find`, `Click` and `count` match exactly. A `Collapsible` animates open: after opening the AI access fold, flush (`openAI` does) before clicking what it holds.
  - A task's tests must fail without its code (the RED step says how), and every commit compiles on its own.
- **Commits.** Commit by explicit path (`git add` never `-A`: the tree has the user's untracked `.DS_Store` and `.idea/`). Every message ends with exactly `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never put a session URL or a `Claude-Session:` line in a commit. Do not push; the controller pushes.

## Review Focus

1. **A reply that lands after its dialog closed, after a lock, or after the hub stopped** (the root check, Enable's save, a card Stop, Resume, Settings Save). It must not reopen a dialog, show an error in another one, or call the locked hub. Covered by Task 3 (`TestLockDropsTheDialog`), Task 4 (`TestStopReplyBehindTheLockChangesNothing`) and Task 6 (`TestLockClosesSettings`).
2. **Content that moves under the pointer right before a click on a guarded button**: the root check landing, an Enable error appearing, a host joining the paused banner, the Settings notice changing. The click in the next 500 ms does nothing. Covered by Task 3 (`TestRootCheckRestartsTheDelay`, `TestEnableErrorStaysInTheDialog`), Task 5 (`TestResumeWaitsThenResumesThePausedHosts`) and Task 6 (`TestSettingsNoLimitWarnsRedAndWaits`).
3. **A timed grant that ends while nothing happens.** The chip, the AI button's `auto N` and the card's Stop go by the app's clock alone, with no hub call. Covered by Task 4 (`TestChipTicksFromTheAppClock`).
4. **A host the hub would refuse**: a root login, a hidden host, a host that only becomes visible in the same save. Enable never sends a refused host; a refusal the app could not foresee shows as the saved-but alert, and nothing is armed. Covered by Task 3 (`TestRefusedHostCannotEnable`, `TestEnableSavedButAlertsAndCloses`) and Task 8 (`TestFlowAutoAllowRootHost`).
5. **A server name with a bidi or invisible rune** in the dialog's title and its typed-name label. It shows only escaped. Covered by Task 3 (`TestConfirmShowsTheNameEscaped`).

## Decisions taken in this plan (controller rulings, on the spec)

1. **Scope.** 3b is the rest of slice 3 (3a Decision 1). It plugs in at `buildAIAccess` and `saveHost` (`view/editor.go`), `buildReady` and `buildHosts` (`view/home.go`), `buildFeed` (`view/approvals.go`), `buildTabStrip` (`view/tabs.go`), and the flow helpers of `view/flow_unix_test.go`. `keptMode` is deleted: `submitHost` asks `model.SaveConfirm` and opens the dialog when it says so, else `saveHost` sends the draft's own mode. `TestSaveKeepsOnlyALiveForeverGrant` becomes `TestSaveAsksOnlyWhenItArms`: its paused row now opens the dialog, as Electron's does. Cost if wrong: none beyond 3a's; the paused-grant edit now asks, as Electron.
2. **No remote tunnels in the dialog yet.** `app/` has no tunnels until slice 5, and a hub runs a tunnel only when its own UI door starts it, so none can run under this app. `DialogChangeKey` has no tunnels term and the dialog no tunnels box. Slice 5 adds both, with `AutoAllowDialog.test.ts:104-109` and the tunnels half of `:153-158`. Cost if wrong: slice 5 forgets them, and a later grant on a host with a running remote tunnel would not warn; this ruling is the reminder.
3. **The chip ticks from the app's clock.** `buildReady` asks `c.After(15 * time.Second)` while any server has a timed grant (Electron's 15 s, `App.tsx:39-48`); the chip, `auto N`, the card's Stop and the feed's Stop all are recomputed from `a.Now()` on that redraw. The redraw changes nothing the hub owns: the hub's `autoAllow.off` (reason `expired`) ends the grant. The Tester cannot observe `c.After`; `TestChipTicksFromTheAppClock` moves the fake clock and draws. Cost: a minute label up to 15 s late; a Stop shown up to 15 s past the end (it sends a harmless `off`).
4. **The MCP bridge test client is hand-rolled and runs the real bridge.** `hubtest.StartBridge` spawns the built `sshgate` with no arguments and `SSHGATE_RUNTIME_DIR`, speaks newline JSON-RPC (`initialize` with `protocolVersion` `2025-06-18`, `notifications/initialized`, `tools/call`), and routes replies by id, as `desktop/e2e/autoHelpers.ts`'s `Mcp` does. `Tool` returns `<-chan hubtest.ExecResult` (text in `Stdout`, `ExitCode` from its `exit code: N` line, a tool error in `Err`), so `f.ran`, `f.denied`, `f.result` and `f.stillPending` work unchanged. The `go-sdk` MCP client (`cmd/sshgate/e2e_test.go`) is not added to `app/go.mod` for 90 lines. All five ported specs use it; `autoallow.spec.ts` sent its requests straight to the door, and its port asserts the same things through the bridge. Cost if wrong: a bridge protocol change breaks the helper, not the app; `TestBridgeRunsToolsThroughTheHub` catches it first.
5. **No ready counter: `a.ready(gen)` stays** (`gen == a.hubGen && a.screen == model.ScreenReady`). A cleanup review noted that a lock then an unlock before a reply lands passes that check. Walked per new reply: the root check, Enable's error and Settings' error are also guarded by identity (`ed.confirm == cf`, `a.settings == st`), and a lock drops those dialogs, so they cannot land; the app-level replies (card Stop, Resume, banner Stop, Stop all) only set the alert row and reload `servers`, completing a user action. A counter would buy nothing. `setAuto` adds the gen guard `stopAll` lacked (slice-1 ledger L50); the unlock reply's `ranWhileLocked` is kept only while its hub runs. Cost if wrong: after a lock and an unlock within one round trip, a stale "Could not … auto-allow" alert shows.
6. **The Auto-allow dialog** (`view/autoallow.go`). A `ui.Modal` over the editor, keyed per open so Cancel takes the focus each time. Enable, then Cancel, as Electron's; Enable is not focusable, so a focus request from behind lands on Cancel. On open, unless the draft is refused, `servers.autoAllowCheck` runs once; its answer, a failure, Enable's error and the opt-in warnings are the `ListChanges` key; the typed name is not. Enable's save error shows in the dialog (Electron's `setError`), a saved-but error closes both into the alert row. Cancel, Esc, Enter, and Enter in the typed-name field go back to the editor with the draft kept; mygo then focuses the editor's Address (its `AutoFocus`), not Save. "ends at HH:MM" is local time and redraws at the next minute. The typed name is compared to the raw server name, exactly (no trim, case-sensitive). Cost: Electron's danger-outline look on Enable is not ported (`MouseButton` has one look; the Tester shows no colour).
7. **Editor controls.** The select sits in `ui.Field(c, "Auto-allow", …)` (visible label, the field names it; a click on the label focuses the select, a click on its current text opens it). It is disabled for a new host, with `Save the host and trust its key first` as the field's description. The opt-ins are plain `ui.Checkbox`es, disabled while the mode is Off, each with its hint as muted text below it (not the checkbox's name). `TimedNote` shows under the select; `DraftRefusal`, amber, shows only while a mode is chosen.
8. **Cards and tabs.** The card's Stop is a `ui.Button` showing `\u25a0` (Electron's stop icon), named `Stop auto-allow <name>`, tooltip `Stop auto-allow`, always visible while a grant is on or paused. It, Resume, the banner's Stop and Stop all share `setAuto(names, mode, verb)` (Electron's `setAllAuto`): each host is tried after a failure, the host list is read again either way. One deliberate wording change: a failed card Stop says `Could not stop auto-allow on <name>: <message>` where Electron alerted the bare message. A tab whose server is on auto-allow (on or paused) shows `\u25cf` named `auto-allow on`.
9. **Paused banner.** Shown on the Hosts tab under `Hosts · N` and in the AI column under the Auto-allowed heading (Electron's two places). Each placement has its own `ListChanges` (`a.pausedHome`, `a.pausedCol`), nil while that placement is not drawn, so showing it again restarts Resume's delay. The column's copy sits below the request list, which is top-anchored: a request's Allow does not move when it appears (`TestPausedBannerInTheAIColumnLeavesAllow`).
10. **Ran-while-locked banner.** `unlock` keeps the reply's `ranWhileLocked` with `model.KeepRan`; the list is cleared whenever the ready screen goes to the lock or hub screen (`setScreen`), so a later lock never shows an older lock's runs. Shown after the store-error row, before the alert row, on every tab, with `Dismiss`.
11. **Settings** (`view/settings.go`). The gear is a `ui.Button` showing `\u2699`, named and tipped `Settings`, between the theme control and Lock, enabled once `status.softLockLimit` is known (the hub reports it only while unlocked). Save stores the limit, then calls `status` (the gear and the dialog read `status.softLockLimit`), then closes. A lock closes Settings (`dropDialogs`), so an unlock never brings back an unsaved choice. Enter on a focused select opens its popup and the first Esc closes a popup only (mygo); accepted. Not ported: Touch ID (slice 7), and the red select and danger-outline Save looks (colour; the notice's amber or red box carries the warning).
12. **Flow tests.**
    - The flow clock starts at the real time and `f.chip` moves it up to the real time on each look (never back): the hub dates a timed grant by its clock, so a clock frozen at a fixed date read `Auto 16m` or nothing. Guards still need `f.pass()`, since the clock stands still between looks.
    - `f.enable()` waits for `Could not check this host's sudo access.` before passing the delay: sshtestd echoes the root check's probe instead of running it, so the check always fails, and its failure landing restarts Enable's delay.
    - `softlock` runs with `--idleLock=3s` (the e2e used 5 s; arming through the UI calls the hub at least once a second here). Its second host, `other`, is never pinned: nothing the spec checks on it needs a pin, and pinning opens a terminal; the hosts flow covers the trust prompt.
    - `autoallow-mcp`'s root-host test is its own flow, `TestFlowAutoAllowRootHost`, behind `needLib`: pinning rootbox opens a terminal. The other four ports run without the terminal library.
    - `TestFlowManualLockKeepsAutoAllow` and `TestFlowSoftLockAndStop` (slice 1, arming through `f.h.SetAutoAllow`) are replaced by `TestFlowManualLock` and `TestFlowSoftLock`, which arm through the editor and check the audit reasons.
    - Not ported, with reasons: the Lock tooltip (`model.LockTitle`, `TestLockTitleAndAIButton`), CSS classes and colours (the Tester shows none).
13. **No hub change.** Every call and text this slice needs is in protocol 12. Recorded hub facts the app relies on: `servers.autoAllowCheck` answers `could not check this host` against sshtestd; `status.softLockLimit` is absent while locked; `servers.setAutoAllow` is refused under soft lock, `off` included.

---

## File structure

```
app/hubapi/types.go                 + AutoCheck
app/hubapi/client.go                + AutoAllowCheck, SetSoftLockLimit
app/model/autoallow.go              + AutoMode, AutoModes, AutoModeOf, KeepRan, RanLockedText, EnableAllowed,
                                      DialogChangeKey, TimedNote, Confirm, SaveConfirm, DraftRefusal
app/model/settings.go               Limit, Limits, LimitChange, LimitHelp, LimitNotice, SettingsChangeKey,
                                      SaveAllowed, LimitText                                              [new]
app/view/editor.go                  editorState.confirm, submitHost, saveHost (draft mode), buildAIAccess; keptMode gone;
                                      dropDialogs also drops Settings
app/view/autoallow.go               confirmState, openConfirm, buildConfirm, buildConfirmBody, buildPaused  [new]
app/view/settings.go                settingsState, buildSettings, saveSettings                            [new]
app/view/app.go                     setAuto (stopAll uses it), ranLocked, pausedHome/pausedCol, settings; unlock keeps ranWhileLocked
app/view/home.go                    chip tick, card Stop, ran-locked banner, paused banner, Settings gear and dialog
app/view/approvals.go               paused banner in the Auto-allowed section
app/view/tabs.go                    auto-allow dot
app/internal/hubtest/bridge_unix.go Bridge, StartBridge, Tool                                             [new]
app/view/*_test.go                  autoallow_test, cards_test, banners_test, settings_test [new]; app_test (fakeDoor);
                                      editor_test (one test replaced)
app/view/flow*_unix_test.go         flow (infra), flowhosts (trust(user)), flowauto [new], flowlock [new]
```

---
### Task 1: `hubapi`: the root check and the soft-lock limit

Ports `desktop/src/renderer/transport.ts:104-105` (protocol.md §1.5, §1.8).

**Files:**
- Modify: `app/hubapi/types.go` (append), `app/hubapi/client.go` (append)
- Test: `app/hubapi/client_test.go`, `app/hubapi/hub_test.go`

**Interfaces:**
- Consumes: `hubapi.Client`, `Caller`, `Status.SoftLockLimit` (slice 1); `hubtest.SSHD`, `Binaries`, `Env`, `Password`.
- Produces (package `hubapi`):
  - `type AutoCheck struct { UID int; PasswordlessSudo bool }` (JSON `uid`, `passwordlessSudo`).
  - `func (h *Client) AutoAllowCheck(ctx context.Context, server string) (AutoCheck, error)` → `servers.autoAllowCheck {server}`.
  - `func (h *Client) SetSoftLockLimit(ctx context.Context, limit string) error` → `settings.setSoftLockLimit {limit}`.

- [ ] **Step 1: Write the failing tests**

In `app/hubapi/client_test.go`, in `TestCallShapes`, add after the `"import apply"` row:

```go
		{"auto check", "servers.autoAllowCheck", `{"server":"box"}`, func(c *Client) error { _, err := c.AutoAllowCheck(ctx, "box"); return err }},
		{"soft-lock limit", "settings.setSoftLockLimit", `{"limit":"none"}`, func(c *Client) error { return c.SetSoftLockLimit(ctx, "none") }},
```

Append to `app/hubapi/client_test.go`:

```go
func TestAutoCheckDecodes(t *testing.T) {
	c, err := New(&rec{result: `{"uid":0,"passwordlessSudo":true}`}).AutoAllowCheck(context.Background(), "box")
	if err != nil || c != (AutoCheck{UID: 0, PasswordlessSudo: true}) {
		t.Fatalf("%+v %v", c, err)
	}
}
```

Append to `app/hubapi/hub_test.go`:

```go
// TestAutoAllowSettingsAgainstRealHub: the soft-lock limit round-trips
// through status, and the root check answers with the hub's texts (sshtestd
// echoes the probe instead of running it, so it cannot be checked).
func TestAutoAllowSettingsAgainstRealHub(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: []string{"hub", "--store=" + store}, Env: hubtest.Env(t)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx := context.Background()
	h := New(p.Client())
	if s, _ := h.Status(ctx); s.SoftLockLimit != "" {
		t.Fatalf("a locked hub reports the limit %q", s.SoftLockLimit)
	}
	if _, err := h.Unlock(ctx, hubtest.Password); err != nil {
		t.Fatal(err)
	}
	if s, _ := h.Status(ctx); s.SoftLockLimit != "24h" {
		t.Fatalf("default limit %q", s.SoftLockLimit)
	}
	if err := h.SetSoftLockLimit(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	if s, _ := h.Status(ctx); s.SoftLockLimit != "none" {
		t.Fatalf("limit %q after setting none", s.SoftLockLimit)
	}
	if err := h.SetSoftLockLimit(ctx, "1d"); err == nil || err.Error() != "limit must be 24h, 3d, 7d, or none" {
		t.Fatalf("1d: %v", err)
	}
	if _, err := h.AutoAllowCheck(ctx, "box"); err == nil || err.Error() != "could not check this host" {
		t.Fatalf("check box: %v", err)
	}
	if _, err := h.AutoAllowCheck(ctx, "nope"); err == nil || err.Error() != `server "nope" not found` {
		t.Fatalf("check nope: %v", err)
	}
	if err := h.Lock(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := h.SetSoftLockLimit(ctx, "7d"); err == nil || err.Error() != "Vault is locked; unlock it in the app" {
		t.Fatalf("locked: %v", err)
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test -count=1 ./hubapi`
Expected: FAIL, build errors `c.AutoAllowCheck undefined`, `undefined: AutoCheck`, `h.SetSoftLockLimit undefined`.

- [ ] **Step 3: Implement**

Append to `app/hubapi/types.go`:

```go
// AutoCheck is servers.autoAllowCheck's answer: the uid the server's login
// runs as, and whether sudo asks it no password. Advice for the Auto-allow
// dialog's root warning, never a gate.
type AutoCheck struct {
	UID              int  `json:"uid"`
	PasswordlessSudo bool `json:"passwordlessSudo"`
}
```

Append to `app/hubapi/client.go`:

```go
// AutoAllowCheck runs the hub's root check on the server's pinned
// connection; a host it cannot read answers "could not check this host".
func (h *Client) AutoAllowCheck(ctx context.Context, server string) (AutoCheck, error) {
	var c AutoCheck
	err := h.c.Call(ctx, "servers.autoAllowCheck", map[string]string{"server": server}, &c)
	return c, err
}

// SetSoftLockLimit stores how long auto-allow may run under a soft lock:
// "24h", "3d", "7d" or "none".
func (h *Client) SetSoftLockLimit(ctx context.Context, limit string) error {
	return h.c.Call(ctx, "settings.setSoftLockLimit", map[string]string{"limit": limit}, nil)
}
```

- [ ] **Step 4: Run the tests**

Run: `cd app && go test -count=1 ./hubapi`
Expected: PASS (`TestAutoAllowSettingsAgainstRealHub` included; it builds sshgate and sshtestd).

- [ ] **Step 5: The Gate, then commit**

```bash
cd app
go vet ./... && test -z "$(gofmt -l .)"
go test -race -count=1 ./...
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=1 ./...
GOOS=linux go vet ./... && GOOS=linux go test -c -o /dev/null ./view
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```
```bash
git add app/hubapi/types.go app/hubapi/client.go app/hubapi/client_test.go app/hubapi/hub_test.go
git commit -m "app: hubapi calls for the root check and the soft-lock limit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 2: `model`: auto-allow and Settings rules

Ports `desktop/src/renderer/autoallow.ts:9-13, 53-128` and `settings.ts` (all but Touch ID), with `autoallow.test.ts:110-237`, the key rows of `AutoAllowDialog.test.ts:121-158` (tunnels rows: Decision 2) and `settings.test.ts:1-73` (renderer.md §2.1, §4.1). `chipLabel`, `isActive`, `autoHosts`, `pausedHosts`, `dropOnLock`, `applyOff`, `lockHostsFrom`, `dropLockHost` and the feed rules are slice 1's, already ported.

**Files:**
- Modify: `app/model/autoallow.go` (import, append)
- Create: `app/model/settings.go`
- Test: `app/model/autoallow_test.go` (import, append), `app/model/settings_test.go` (new)

**Interfaces:**
- Consumes: `hubapi.Server`, `AutoAllow`, `AutoCheck` (Task 1), `RanLocked`; `model.HostDraft`, `DraftFrom` (3a); `model.TrustEnabled`, `DelayRemaining`, `NewListChanges`, `left`, `plural` (slices 1–2).
- Produces (package `model`):
  - `type AutoMode struct { Mode, Label string; Dur time.Duration }`; `var AutoModes []AutoMode` (`15m`…`forever`); `func AutoModeOf(mode string) (AutoMode, bool)` (false for `off`).
  - `func KeepRan(cur, next []hubapi.RanLocked) []hubapi.RanLocked`; `func RanLockedText(ran []hubapi.RanLocked) string` ("" when nothing ran).
  - `func EnableAllowed(typeName bool, typed, host, refused string, changedAt, now time.Time) bool`.
  - `func DialogChangeKey(mode string, rootNew, sudoNew, sudo bool, check *hubapi.AutoCheck, checkFailed bool, err string) string`.
  - `func TimedNote(s *hubapi.Server, now time.Time) string`.
  - `type Confirm struct { Needed, TypeName, RootNew, SudoNew bool }`; `func SaveConfirm(s *hubapi.Server, d HostDraft) Confirm`.
  - `func DraftRefusal(s *hubapi.Server, d HostDraft) string`.
  - `type Limit struct { Value, Label string }`; `var Limits []Limit`; `func LimitChange(cur, chosen string) string` (`same`, `none`, `higher`, `lower`); `func LimitHelp(l string) string`; `func LimitNotice(l string) string`; `func SettingsChangeKey(chosen string, hosts []string, err string) string`; `func SaveAllowed(change string, busy bool, changedAt, now time.Time) bool`; `func LimitText(limit string) string` ("" reads as `24h`).
- Port notes: Electron's `enableAllowed` and `saveAllowed` take `openedAt` and `changedAt`; the Go ones take `ListChanges.At`, which is already the later of the two (the dialog's `ListChanges` is made at open), and the vitest rows are converted so (`opened 1000, changed 0` → `changedAt 1000`).

- [ ] **Step 1: Write the failing tests**

In `app/model/autoallow_test.go`, add `"strings"` to the imports (after `"reflect"`), and append:

```go
// Ports of the rest of desktop/test/autoallow.test.ts and the key rows of
// AutoAllowDialog.test.ts.

func TestEnableAllowed(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	ms := func(n int) time.Time { return now.Add(time.Duration(n) * time.Millisecond) }
	for _, tc := range []struct {
		typeName       bool
		typed, refused string
		changed, at    time.Time
		want           bool
	}{
		{false, "", "", now, ms(600), true},
		{false, "", "", now, ms(499), false},
		{false, "", "", ms(200), ms(600), false},
		{true, "", "", now, ms(600), false},
		{true, "box", "", now, ms(600), true},
		{true, "Box", "", now, ms(600), false},
		{false, "", "root login", now, ms(600), false},
	} {
		if got := EnableAllowed(tc.typeName, tc.typed, "box", tc.refused, tc.changed, tc.at); got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}

func TestTimedNote(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	srv := func(a *hubapi.AutoAllow) *hubapi.Server { return &hubapi.Server{AutoAllow: a} }
	for _, tc := range []struct {
		s    *hubapi.Server
		want string
	}{
		{srv(until(now, 14200*time.Millisecond*60)), "On for 15 more min — saving ends it; pick a duration to keep auto-allow on"},
		{srv(until(now, 6*time.Second)), "On for 1 more min — saving ends it; pick a duration to keep auto-allow on"},
		{srv(until(now, -time.Minute)), ""},
		{srv(&hubapi.AutoAllow{Forever: true}), ""},
		{srv(nil), ""},
		{nil, ""},
	} {
		if got := TimedNote(tc.s, now); got != tc.want {
			t.Errorf("%+v: got %q", tc.s, got)
		}
	}
}

func TestSaveConfirm(t *testing.T) {
	srv := func(a *hubapi.AutoAllow, root, sudo bool) *hubapi.Server {
		return &hubapi.Server{AutoAllow: a, AutoAllowRoot: root, AutoAllowSudo: sudo}
	}
	with := func(s *hubapi.Server, edit func(*HostDraft)) HostDraft {
		d := DraftFrom(s)
		edit(&d)
		return d
	}
	none := srv(nil, false, false)
	armed := srv(&hubapi.AutoAllow{Forever: true}, false, false)
	for _, tc := range []struct {
		name string
		s    *hubapi.Server
		d    HostDraft
		want Confirm
	}{
		{"off", none, with(none, func(d *HostDraft) { d.AutoAllow = "off" }), Confirm{}},
		{"timed", none, with(none, func(d *HostDraft) { d.AutoAllow = "15m" }), Confirm{Needed: true}},
		{"forever newly chosen", none, with(none, func(d *HostDraft) { d.AutoAllow = "forever" }), Confirm{Needed: true, TypeName: true}},
		{"forever armed, same options", srv(&hubapi.AutoAllow{Forever: true}, true, false), DraftFrom(srv(&hubapi.AutoAllow{Forever: true}, true, false)), Confirm{}},
		{"armed, root newly ticked", armed, with(armed, func(d *HostDraft) { d.AutoAllowRoot = true }), Confirm{Needed: true, RootNew: true}},
		{"armed, sudo newly ticked", armed, with(armed, func(d *HostDraft) { d.AutoAllowSudo = true }), Confirm{Needed: true, SudoNew: true}},
		{"paused forever", srv(&hubapi.AutoAllow{Forever: true, Paused: true}, false, false), DraftFrom(srv(&hubapi.AutoAllow{Forever: true, Paused: true}, false, false)), Confirm{Needed: true}},
	} {
		if got := SaveConfirm(tc.s, tc.d); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestDraftRefusal(t *testing.T) {
	pinned := func(refused string) *hubapi.Server {
		return &hubapi.Server{HostKey: "SHA256:x", AutoAllowRefused: refused}
	}
	visible := func(s *hubapi.Server, root bool) HostDraft {
		d := DraftFrom(s)
		d.AIVisible, d.AutoAllowRoot = true, root
		return d
	}
	if got := DraftRefusal(nil, DraftFrom(nil)); got != "no pinned host key" {
		t.Errorf("new host: %q", got)
	}
	if got := DraftRefusal(&hubapi.Server{}, visible(&hubapi.Server{}, false)); got != "no pinned host key" {
		t.Errorf("unpinned: %q", got)
	}
	if got := DraftRefusal(pinned(""), DraftFrom(pinned(""))); got != "not visible to AI" {
		t.Errorf("hidden: %q", got)
	}
	for _, r := range []string{"root login", "has an su password", "has a sudo password"} {
		if got := DraftRefusal(pinned(r), visible(pinned(r), false)); got != r {
			t.Errorf("%s: %q", r, got)
		}
		if got := DraftRefusal(pinned(r), visible(pinned(r), true)); got != "" {
			t.Errorf("%s with Allow on root hosts: %q", r, got)
		}
	}
	if got := DraftRefusal(pinned(""), visible(pinned(""), false)); got != "" {
		t.Errorf("pinned and visible: %q", got)
	}
}

func TestRanLocked(t *testing.T) {
	if got := RanLockedText(nil); got != "" {
		t.Errorf("none: %q", got)
	}
	if got := RanLockedText([]hubapi.RanLocked{{Server: "a", Count: 1}}); got != "While the app was locked the AI ran 1 command on a. See the Audit tab." {
		t.Errorf("one: %q", got)
	}
	if got := RanLockedText([]hubapi.RanLocked{{Server: "a", Count: 2}, {Server: "b", Count: 3}}); got != "While the app was locked the AI ran 5 commands on a, b. See the Audit tab." {
		t.Errorf("five: %q", got)
	}
	ran := []hubapi.RanLocked{{Server: "box", Count: 2}}
	if !reflect.DeepEqual(KeepRan(nil, ran), ran) || !reflect.DeepEqual(KeepRan(ran, []hubapi.RanLocked{}), ran) || !reflect.DeepEqual(KeepRan(ran, nil), ran) {
		t.Error("KeepRan dropped the runs")
	}
}

// The Enable delay's key: what moves the buttons changes it, typing does not.
func TestDialogChangeKey(t *testing.T) {
	pending := DialogChangeKey("forever", false, false, false, nil, false, "")
	root := DialogChangeKey("forever", false, false, false, &hubapi.AutoCheck{UID: 0}, false, "")
	failed := DialogChangeKey("forever", false, false, false, nil, true, "")
	if root == pending || failed == pending || root == failed {
		t.Fatal("the check's state is not in the key")
	}
	for i, k := range []string{
		DialogChangeKey("forever", true, false, false, nil, false, ""),
		DialogChangeKey("forever", false, true, false, nil, false, ""),
		DialogChangeKey("forever", false, false, true, nil, false, ""),
		DialogChangeKey("forever", false, false, false, nil, false, "boom"),
		DialogChangeKey("15m", false, false, false, nil, false, ""),
	} {
		if k == pending {
			t.Errorf("change %d kept the key", i)
		}
	}
	if DialogChangeKey("forever", false, false, false, nil, false, "") != pending {
		t.Error("the same inputs changed the key")
	}
	// The check resolving at T=1000 restarts the delay at once.
	t0 := time.UnixMilli(0)
	lc := NewListChanges(pending, t0)
	lc.SetKey(pending, time.UnixMilli(1000))
	if !lc.At.Equal(t0) {
		t.Fatal("an unchanged key restarted the delay")
	}
	lc.SetKey(root, time.UnixMilli(1000))
	for _, tc := range []struct {
		at   int64
		want bool
	}{{1000, false}, {1499, false}, {1500, true}} {
		if got := EnableAllowed(false, "", "h", "", lc.At, time.UnixMilli(tc.at)); got != tc.want {
			t.Errorf("at %d: %v", tc.at, got)
		}
	}
}

func TestAutoModes(t *testing.T) {
	var labels []string
	for _, m := range AutoModes {
		labels = append(labels, m.Mode+"="+m.Label)
	}
	if got := strings.Join(labels, ","); got != "15m=15 min,30m=30 min,60m=60 min,2h=2 h,4h=4 h,forever=Until turned off" {
		t.Fatal(got)
	}
	if m, ok := AutoModeOf("2h"); !ok || m.Dur != 2*time.Hour {
		t.Fatal(m, ok)
	}
	if _, ok := AutoModeOf("off"); ok {
		t.Fatal("off is a mode")
	}
}
```

Create `app/model/settings_test.go`:

```go
package model

import (
	"strings"
	"testing"
	"time"
)

// Ports of desktop/test/settings.test.ts (Touch ID rows: slice 7).

func TestLimitChange(t *testing.T) {
	for _, tc := range [][3]string{
		{"24h", "24h", "same"}, {"24h", "3d", "higher"}, {"3d", "7d", "higher"}, {"7d", "3d", "lower"},
		{"none", "24h", "lower"}, {"24h", "none", "none"}, {"none", "none", "same"},
	} {
		if got := LimitChange(tc[0], tc[1]); got != tc[2] {
			t.Errorf("LimitChange(%s, %s) = %s, want %s", tc[0], tc[1], got, tc[2])
		}
	}
}

func TestLimitCopy(t *testing.T) {
	want := []Limit{{"24h", "24 hours (default)"}, {"3d", "3 days"}, {"7d", "7 days"}, {"none", "No limit"}}
	for i, l := range Limits {
		if l != want[i] {
			t.Fatalf("Limits %v", Limits)
		}
	}
	for _, l := range []string{"24h", "3d", "7d"} {
		if got := LimitHelp(l); got != "Then the app locks fully and auto-allow stops. Forever grants wait for Resume." {
			t.Errorf("LimitHelp(%s) = %q", l, got)
		}
	}
	if got := LimitHelp("none"); got != "Auto-allow stops only with Stop auto-allow and lock, a restart or a crash." {
		t.Errorf("LimitHelp(none) = %q", got)
	}
	if got := LimitNotice("7d"); got != "While the app is locked, the AI keeps running on hosts with auto-allow for up to 7 days. Any program running as your user can use that access too." {
		t.Errorf("LimitNotice(7d) = %q", got)
	}
	if got := LimitNotice("none"); got != "While the app is locked, the AI keeps running on hosts with auto-allow for as long as the app runs. Any program running as your user can use that access too." {
		t.Errorf("LimitNotice(none) = %q", got)
	}
}

// SaveAllowed takes the later of open and change as changedAt (ListChanges.At).
func TestSaveAllowed(t *testing.T) {
	at := func(ms int) time.Time { return time.UnixMilli(int64(ms)) }
	for _, tc := range []struct {
		change       string
		busy         bool
		changed, now int
		want         bool
	}{
		{"higher", false, 1000, 1499, false}, {"higher", false, 1000, 1500, true},
		{"higher", false, 2000, 2499, false}, {"higher", false, 2000, 2500, true},
		{"same", false, 1000, 9999, false}, {"higher", true, 1000, 9999, false}, {"lower", false, 1000, 9999, true},
	} {
		if got := SaveAllowed(tc.change, tc.busy, at(tc.changed), at(tc.now)); got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}

func TestSettingsChangeKey(t *testing.T) {
	k := SettingsChangeKey("none", []string{"box"}, "")
	for _, other := range []string{
		SettingsChangeKey("7d", []string{"box"}, ""),
		SettingsChangeKey("none", []string{"box", "web"}, ""),
		SettingsChangeKey("none", []string{"box"}, "Vault is locked"),
	} {
		if other == k {
			t.Errorf("%q did not change the key", other)
		}
	}
	if SettingsChangeKey("none", []string{"box"}, "") != k {
		t.Error("the same inputs changed the key")
	}
}

func TestLimitText(t *testing.T) {
	day := LimitText("24h")
	for _, s := range []string{"the AI keeps running on this host for up to 24 hours.", "After a restart, the 24-hour limit, or Stop auto-allow and lock, it waits for you to click Resume."} {
		if !strings.Contains(day, s) {
			t.Errorf("24h: missing %q in %q", s, day)
		}
	}
	if LimitText("") != day {
		t.Error("an absent limit is not the 24-hour copy")
	}
	for l, s := range map[string]string{"3d": "for up to 3 days.", "7d": "the 7-day limit"} {
		if !strings.Contains(LimitText(l), s) {
			t.Errorf("%s: missing %q", l, s)
		}
	}
	if !strings.Contains(LimitText("3d"), "the 3-day limit") {
		t.Error("3d: missing the 3-day limit")
	}
	none := LimitText("none")
	for _, s := range []string{"the AI keeps running on this host while the app is locked, with no time limit.", "After a restart or Stop auto-allow and lock, it waits for you to click Resume."} {
		if !strings.Contains(none, s) {
			t.Errorf("none: missing %q in %q", s, none)
		}
	}
	for _, s := range []string{"24", "hour", "day"} {
		if strings.Contains(none, s) {
			t.Errorf("none mentions %q: %q", s, none)
		}
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test -count=1 ./model`
Expected: FAIL, build errors such as `undefined: EnableAllowed`, `undefined: LimitChange`.

- [ ] **Step 3: Implement**

In `app/model/autoallow.go`, add `"strings"` to the imports (between `"math"` and `"time"`), and append:

```go
// AutoMode is an Auto-allow choice other than Off: the hub's value, the
// editor's label, and the grant's length (0 for forever).
type AutoMode struct {
	Mode, Label string
	Dur         time.Duration
}

// AutoModes are the editor's choices after Off, in Electron's order.
var AutoModes = []AutoMode{
	{"15m", "15 min", 15 * time.Minute}, {"30m", "30 min", 30 * time.Minute}, {"60m", "60 min", time.Hour},
	{"2h", "2 h", 2 * time.Hour}, {"4h", "4 h", 4 * time.Hour}, {"forever", "Until turned off", 0},
}

// AutoModeOf finds mode among AutoModes; off and anything unknown are not.
func AutoModeOf(mode string) (AutoMode, bool) {
	for _, m := range AutoModes {
		if m.Mode == mode {
			return m, true
		}
	}
	return AutoMode{}, false
}

// KeepRan keeps the runs an unlock reply listed: the hub hands them to the
// first reply only, so a later empty one keeps the list.
func KeepRan(cur, next []hubapi.RanLocked) []hubapi.RanLocked {
	if len(next) > 0 {
		return next
	}
	return cur
}

// RanLockedText says what ran while the app was locked; "" when nothing did.
func RanLockedText(ran []hubapi.RanLocked) string {
	n := 0
	names := make([]string, len(ran))
	for i, r := range ran {
		n += r.Count
		names[i] = r.Server
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("While the app was locked the AI ran %d command%s on %s. See the Audit tab.", n, plural(n), strings.Join(names, ", "))
}

// EnableAllowed is the Auto-allow dialog's Enable: never on a refused host,
// and with typeName only once the host name is typed exactly; then
// AllowDelay after the dialog opened or its content last changed.
func EnableAllowed(typeName bool, typed, host, refused string, changedAt, now time.Time) bool {
	if refused != "" || typeName && typed != host {
		return false
	}
	return TrustEnabled(changedAt, now)
}

// DialogChangeKey is what restarts Enable's delay when it changes: the
// mode, the warnings, the sudo sentence, the root check's state (pending,
// failed or its answer) and an error line. The typed name is not in it.
func DialogChangeKey(mode string, rootNew, sudoNew, sudo bool, check *hubapi.AutoCheck, checkFailed bool, err string) string {
	state := "pending"
	switch {
	case checkFailed:
		state = "error"
	case check != nil:
		state = fmt.Sprintf("uid %d sudo %t", check.UID, check.PasswordlessSudo)
	}
	return fmt.Sprintf("%s|%t|%t|%t|%s|%q", mode, rootNew, sudoNew, sudo, state, err)
}

// TimedNote warns, under the editor's select, that saving ends a running
// timed grant; "" when none runs.
func TimedNote(s *hubapi.Server, now time.Time) string {
	if s == nil {
		return ""
	}
	ms := left(s.AutoAllow, now)
	if ms <= 0 {
		return ""
	}
	return fmt.Sprintf("On for %d more min — saving ends it; pick a duration to keep auto-allow on", int(math.Ceil(float64(ms)/float64(time.Minute))))
}

// Confirm is what Save must ask before it sends the draft's auto-allow.
type Confirm struct {
	Needed, TypeName, RootNew, SudoNew bool
}

// SaveConfirm: any mode but Off asks, unless the host already runs a live
// forever grant with the same opt-ins; a newly chosen forever needs the
// name typed; an opt-in newly ticked gets its warning.
func SaveConfirm(s *hubapi.Server, d HostDraft) Confirm {
	var a hubapi.AutoAllow
	var root, sudo bool
	if s != nil {
		root, sudo = s.AutoAllowRoot, s.AutoAllowSudo
		if s.AutoAllow != nil {
			a = *s.AutoAllow
		}
	}
	armed := d.AutoAllow == "forever" && a.Forever && !a.Paused && d.AutoAllowRoot == root && d.AutoAllowSudo == sudo
	return Confirm{
		Needed:   d.AutoAllow != "off" && !armed,
		TypeName: d.AutoAllow == "forever" && !a.Forever,
		RootNew:  d.AutoAllowRoot && !root,
		SudoNew:  d.AutoAllowSudo && !sudo,
	}
}

// DraftRefusal is why arming the draft would fail, shown before Save; the
// hub has the final say on the draft's other edits.
func DraftRefusal(s *hubapi.Server, d HostDraft) string {
	switch {
	case s == nil || s.HostKey == "":
		return "no pinned host key"
	case !d.AIVisible:
		return "not visible to AI"
	}
	switch s.AutoAllowRefused {
	case "root login", "has an su password", "has a sudo password":
		if !d.AutoAllowRoot {
			return s.AutoAllowRefused
		}
	}
	return ""
}
```

Create `app/model/settings.go`:

```go
package model

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Limit is one soft-lock limit: the hub's value and the Settings label.
type Limit struct{ Value, Label string }

// Limits are the Settings choices, shortest first (Electron's LIMITS).
var Limits = []Limit{{"24h", "24 hours (default)"}, {"3d", "3 days"}, {"7d", "7 days"}, {"none", "No limit"}}

// limitWords is how a fixed limit reads in a sentence: "up to 3 days",
// "the 3-day limit". No limit has none.
var limitWords = map[string][2]string{"24h": {"24 hours", "24-hour"}, "3d": {"3 days", "3-day"}, "7d": {"7 days", "7-day"}}

func limitRank(l string) int {
	return slices.IndexFunc(Limits, func(x Limit) bool { return x.Value == l })
}

// LimitChange is "same", "none" (No limit chosen), "higher" or "lower".
func LimitChange(cur, chosen string) string {
	switch {
	case chosen == cur:
		return "same"
	case chosen == "none":
		return "none"
	case limitRank(chosen) > limitRank(cur):
		return "higher"
	}
	return "lower"
}

func LimitHelp(l string) string {
	if l == "none" {
		return "Auto-allow stops only with Stop auto-allow and lock, a restart or a crash."
	}
	return "Then the app locks fully and auto-allow stops. Forever grants wait for Resume."
}

func LimitNotice(l string) string {
	span := "for as long as the app runs"
	if l != "none" {
		span = "for up to " + limitWords[l][0]
	}
	return "While the app is locked, the AI keeps running on hosts with auto-allow " + span + ". Any program running as your user can use that access too."
}

// SettingsChangeKey is what restarts Save's delay: the choice (it swaps the
// notice), the hosts the notice lists, and an error line.
func SettingsChangeKey(chosen string, hosts []string, err string) string {
	return fmt.Sprintf("%s|%s|%q", chosen, strings.Join(hosts, "\n"), err)
}

// SaveAllowed is Settings' Save: something changed, nothing in flight, and
// AllowDelay since the dialog opened or its content last changed.
func SaveAllowed(change string, busy bool, changedAt, now time.Time) bool {
	return !busy && change != "same" && TrustEnabled(changedAt, now)
}

// LimitText is the Auto-allow dialog's paragraph for a forever grant, from
// the vault's soft-lock limit ("" reads as the 24-hour default).
func LimitText(limit string) string {
	keep := "the AI keeps running on this host while the app is locked, with no time limit"
	after := "After a restart or Stop auto-allow and lock"
	if limit != "none" {
		w, ok := limitWords[limit]
		if !ok {
			w = limitWords["24h"] // absent: the hub's default
		}
		keep = "the AI keeps running on this host for up to " + w[0]
		after = "After a restart, the " + w[1] + " limit, or Stop auto-allow and lock"
	}
	return "Stays set after restart. Locking the app, by hand or from inactivity, does not stop it: " + keep +
		". To stop it, use Stop auto-allow and lock on the unlock screen, Stop on the host card, or Stop all auto-allow. " + after + ", it waits for you to click Resume."
}
```

- [ ] **Step 4: Run the tests**

Run: `cd app && go test -count=1 ./model -v -run 'EnableAllowed|TimedNote|SaveConfirm|DraftRefusal|RanLocked|DialogChangeKey|AutoModes|Limit|SaveAllowed|SettingsChangeKey'`
Expected: PASS, every named test.

- [ ] **Step 5: The Gate, the rune check, then commit**

```bash
cd app
go vet ./... && test -z "$(gofmt -l .)"
go test -race -count=1 ./...
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=1 ./...
GOOS=linux go vet ./... && GOOS=linux go test -c -o /dev/null ./view
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```
```bash
perl -CSD -ne 'print "$ARGV:$.\n" if /[\x{200B}-\x{200F}\x{2028}-\x{202E}\x{2066}-\x{2069}\x{FEFF}]/; close ARGV if eof' app/model/autoallow.go app/model/settings.go app/model/autoallow_test.go app/model/settings_test.go
```
```bash
git add app/model/autoallow.go app/model/settings.go app/model/autoallow_test.go app/model/settings_test.go
git commit -m "app: model rules of auto-allow and Settings, with their vitest cases

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 3: The editor's Auto-allow controls and the Auto-allow dialog

Ports `HostEditor.tsx:22-29, 51-56, 154-192, 218-227` (the AI access fold and `saveConfirm`), `AutoAllowDialog.tsx`, `App.tsx:209-216` (`SAVED_BUT`), and the rendered cases of `hostForm.test.ts:88-97` and `AutoAllowDialog.test.ts:29-114` (renderer.md §1.2, §2.2, §5). Replaces 3a's `keptMode` (Decisions 1, 6, 7).

**Files:**
- Modify: `app/view/editor.go`
- Create: `app/view/autoallow.go`
- Test: `app/view/app_test.go` (`fakeDoor`), `app/view/editor_test.go` (delete one test), `app/view/autoallow_test.go` (new)

**Interfaces:**
- Consumes: Task 1 `hubapi.AutoCheck`, `Client.AutoAllowCheck`; Task 2 `model.AutoModes`, `AutoModeOf`, `TimedNote`, `SaveConfirm`, `Confirm`, `DraftRefusal`, `EnableAllowed`, `DialogChangeKey`, `LimitText`; 3a `editorState`, `buildSheet`, `a.ready`, `savedBut`, `a.server`, `MouseButton`; test helpers `newApp`, `edReady`, `editBox`, `clickT`, `fill`, `count`, `hasCall`, `params`, `lockNow`, `boxServer`.
- Produces:
  - `editorState.confirm *confirmState`; `type confirmState struct { ask model.Confirm; refused string; check *hubapi.AutoCheck; checkFail bool; typed, err string; lc *model.ListChanges }`.
  - `func (a *App) submitHost(s *hubapi.Server)`, `func (a *App) saveHost()` (sends `ed.draft.AutoAllow`), `func (a *App) openConfirm(s *hubapi.Server)`, `func (a *App) buildConfirm(c *ui.Context)`, `func (a *App) buildConfirmBody(c *ui.Context, cf *confirmState, d model.HostDraft, name string, now time.Time, answer *int)`.
  - `fakeDoor.check *hubapi.AutoCheck` (nil answers `{uid: 1000}`; a `servers.autoAllowCheck` error comes from `errs`).
  - Test helpers (`autoallow_test.go`, used by Tasks 4–9): `autoBox() hubapi.Server`, `openAI(t, tt)`, `choose(t, tt, cur, option string)`, `autoApp(t, s, limit) (*App, *tester, *fakeDoor, *clock)`, `confirmApp(t, s, mode, limit)`, `pass(c *clock, tt *tester)`.

- [ ] **Step 1: Let the fake door answer the root check**

Without this the fake door answers `servers.autoAllowCheck` with `{}`, which decodes as uid 0 and shows the root warning in every test. In `app/view/app_test.go`, add to `fakeDoor`'s fields, after `imported hubapi.ImportResult`:

```go
	check    *hubapi.AutoCheck // servers.autoAllowCheck; nil answers uid 1000, no passwordless sudo
```

replace `scan, imported := d.scan, d.imported` with:

```go
	scan, imported, check := d.scan, d.imported, d.check
```

and add a case to the method switch, after `case "import.apply":`'s body:

```go
	case "servers.autoAllowCheck":
		out = hubapi.AutoCheck{UID: 1000}
		if check != nil {
			out = *check
		}
```

(gofmt aligns the field comments; run `gofmt -w app/view/app_test.go`.)

- [ ] **Step 2: Write the failing tests**

In `app/view/editor_test.go`, delete `TestSaveKeepsOnlyALiveForeverGrant` (its rows move to `TestSaveAsksOnlyWhenItArms` below, the paused one now asking). Create `app/view/autoallow_test.go`:

```go
package view

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// autoBox is box visible to AI and pinned: a grant can be armed on it.
func autoBox() hubapi.Server {
	s := boxServer
	s.AIVisible = true
	return s
}

// openAI opens the editor's AI access fold, whatever its title says now,
// and lets its opening animation end, so what it holds can be clicked.
func openAI(t *testing.T, tt *tester) {
	t.Helper()
	for _, x := range tt.Texts() {
		if strings.HasPrefix(x, "AI access · ") {
			clickT(t, tt, x)
			tt.flush()
			return
		}
	}
	t.Fatalf("no AI access fold in %q", tt.Texts())
}

// choose picks option in the select now showing cur: a click on mygo's
// select opens it, a click on an option chooses it.
func choose(t *testing.T, tt *tester, cur, option string) {
	t.Helper()
	clickT(t, tt, cur)
	clickT(t, tt, option)
}

// autoApp opens s's editor with the AI access fold open, on a hub whose
// soft-lock limit is limit.
func autoApp(t *testing.T, s hubapi.Server, limit string) (*App, *tester, *fakeDoor, *clock) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: limit}, servers: []hubapi.Server{s}}
	a, tt, c, _ := newApp(t, d)
	tt.SetSize(800, 900)
	tt.flush()
	clickT(t, tt, "Edit "+model.DisplayText(s.Name))
	openAI(t, tt)
	return a, tt, d, c
}

// confirmApp chooses mode for s and presses Save, which opens the dialog.
func confirmApp(t *testing.T, s hubapi.Server, mode, limit string) (*App, *tester, *fakeDoor, *clock) {
	a, tt, d, c := autoApp(t, s, limit)
	choose(t, tt, "Off", mode)
	d.resetCalls()
	clickT(t, tt, "Save")
	tt.flush()
	if a.editor == nil || a.editor.confirm == nil {
		t.Fatalf("Save did not ask: %q", tt.Texts())
	}
	return a, tt, d, c
}

// pass moves the app's clock past the 500 ms guard and draws.
func pass(c *clock, tt *tester) { c.add(600 * time.Millisecond); tt.Frame() }

func TestAutoAllowSelectAndOptIns(t *testing.T) {
	a, tt, d, _ := autoApp(t, autoBox(), "24h")
	d.resetCalls()
	if !tt.HasText("Auto-allow") || !tt.HasText("Off") {
		t.Fatalf("no Auto-allow select: %q", tt.Texts())
	}
	clickT(t, tt, "Allow on root hosts")
	if a.editor.draft.AutoAllowRoot {
		t.Fatal("an opt-in changed while Auto-allow is Off")
	}
	choose(t, tt, "Off", "15 min")
	if a.editor.draft.AutoAllow != "15m" || !tt.HasText("AI access · On · auto") {
		t.Fatalf("draft %q, texts %q", a.editor.draft.AutoAllow, tt.Texts())
	}
	clickT(t, tt, "Allow on root hosts")
	clickT(t, tt, "Also auto-allow sudo-exec")
	if !a.editor.draft.AutoAllowRoot || !a.editor.draft.AutoAllowSudo {
		t.Fatal("the opt-ins do not tick once a mode is chosen")
	}
	choose(t, tt, "15 min", "Until turned off")
	if a.editor.draft.AutoAllow != "forever" {
		t.Fatal(a.editor.draft.AutoAllow)
	}
	if got := d.callList(); len(got) != 0 {
		t.Fatalf("choosing called %v", got)
	}
}

func TestNewHostCannotChooseAutoAllow(t *testing.T) {
	a, tt, _ := edReady(t, []hubapi.Server{})
	clickT(t, tt, "New host")
	openAI(t, tt)
	if !tt.HasText("Save the host and trust its key first") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Off")
	if tt.HasText("15 min") || a.editor.draft.AutoAllow != "off" {
		t.Fatal("the select opened for a new host")
	}
}

func TestTimedGrantNoteAndRefusalHint(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Until: time.Date(2026, 10, 15, 12, 14, 12, 0, time.UTC).Format(time.RFC3339)}
	a, tt, _, _ := autoApp(t, s, "24h")
	if a.editor.draft.AutoAllow != "off" || !tt.HasText("On for 15 more min — saving ends it; pick a duration to keep auto-allow on") {
		t.Fatalf("draft %q, texts %q", a.editor.draft.AutoAllow, tt.Texts())
	}

	hidden := boxServer // not visible to AI
	_, tt, _, _ = autoApp(t, hidden, "24h")
	if tt.HasText("not visible to AI") {
		t.Fatal("the refusal shows while Auto-allow is Off")
	}
	choose(t, tt, "Off", "15 min")
	if !tt.HasText("not visible to AI") {
		t.Fatal(tt.Texts())
	}
}

// Save asks only when it arms a grant the host does not already run.
func TestSaveAsksOnlyWhenItArms(t *testing.T) {
	for _, tc := range []struct {
		auto *hubapi.AutoAllow
		ask  bool
		want string
	}{
		{&hubapi.AutoAllow{Forever: true}, false, `"autoAllow":"forever"`},
		{&hubapi.AutoAllow{Forever: true, Paused: true}, true, ""},
		{&hubapi.AutoAllow{Until: "2026-10-15T13:00:00Z"}, false, `"autoAllow":"off"`},
		{nil, false, `"autoAllow":"off"`},
	} {
		s := autoBox()
		s.AutoAllow = tc.auto
		a, tt, d := editBox(t, s)
		clickT(t, tt, "Save")
		tt.flush()
		if asked := a.editor != nil && a.editor.confirm != nil; asked != tc.ask {
			t.Errorf("%+v: asked %v", tc.auto, asked)
		}
		if got := params(d, "servers.save"); !tc.ask && !strings.HasSuffix(got, tc.want+"}") {
			t.Errorf("%+v: sent %s", tc.auto, got)
		}
	}
}

func TestEnableWaitsThenSavesTheMode(t *testing.T) {
	a, tt, d, c := confirmApp(t, autoBox(), "15 min", "24h")
	if !tt.HasText("Auto-allow AI commands on box") || !tt.HasText("sudo-exec still asks.") || !tt.HasText("including commands planted by what it reads") {
		t.Fatalf("dialog: %q", tt.Texts())
	}
	ends := regexp.MustCompile(`^For 15 min \(ends at \d{1,2}:\d{2}\)$`)
	found := false
	for _, x := range tt.Texts() {
		found = found || ends.MatchString(x)
	}
	if !found {
		t.Fatalf("no duration line in %q", tt.Texts())
	}
	if got := d.callList(); len(got) != 1 || got[0] != "servers.autoAllowCheck" || params(d, "servers.autoAllowCheck") != `{"server":"box"}` {
		t.Fatalf("calls %v", got)
	}
	clickT(t, tt, "Enable") // before the delay
	for i := 0; i < 20; i++ {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Enable") {
			t.Fatal("Tab reached Enable")
		}
	}
	tt.flush()
	if hasCall(d, "servers.save") {
		t.Fatal("Enable worked before the delay")
	}
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if got := params(d, "servers.save"); !strings.HasSuffix(got, `"autoAllow":"15m"}`) || a.editor != nil {
		t.Fatalf("sent %s, editor open %v", got, a.editor != nil)
	}
}

func TestForeverNeedsTheNameTyped(t *testing.T) {
	_, tt, d, c := confirmApp(t, autoBox(), "Until turned off", "24h")
	for _, s := range []string{"Until turned off", "Type box to confirm", model.LimitText("24h")} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	pass(c, tt)
	clickT(t, tt, "Enable")
	fill(t, tt, "Type box to confirm", "Box")
	clickT(t, tt, "Enable")
	tt.flush()
	if hasCall(d, "servers.save") {
		t.Fatal("Enable worked without the exact name")
	}
	fill(t, tt, "Type box to confirm", "box")
	clickT(t, tt, "Enable") // typing is not a change: no new delay
	tt.flush()
	if got := params(d, "servers.save"); !strings.HasSuffix(got, `"autoAllow":"forever"}`) {
		t.Fatalf("sent %s", got)
	}
}

func TestConfirmTextFollowsTheLimit(t *testing.T) {
	stops := "Stop auto-allow and lock on the unlock screen, Stop on the host card, or Stop all auto-allow"
	_, tt, _, _ := confirmApp(t, autoBox(), "15 min", "24h")
	if !tt.HasText("Locking the app, by hand or from inactivity, does not stop it: the AI keeps running on this host until then.") || !tt.HasText(stops) {
		t.Fatalf("timed: %q", tt.Texts())
	}
	_, tt, _, _ = confirmApp(t, autoBox(), "Until turned off", "24h")
	for _, s := range []string{"does not stop it: the AI keeps running on this host for up to 24 hours.", stops, "After a restart, the 24-hour limit, or Stop auto-allow and lock, it waits for you to click Resume."} {
		if !tt.HasText(s) {
			t.Fatalf("forever: missing %q", s)
		}
	}
	for _, s := range []string{"lock by hand to stop", "or when you lock the vault", "After a manual lock"} {
		if tt.HasText(s) {
			t.Fatalf("stale copy %q", s)
		}
	}
	_, tt, _, _ = confirmApp(t, autoBox(), "Until turned off", "none")
	if !tt.HasText("with no time limit") || tt.HasText("24 hours") {
		t.Fatalf("none: %q", tt.Texts())
	}
	_, tt, _, _ = confirmApp(t, autoBox(), "Until turned off", "7d")
	if !tt.HasText("for up to 7 days") {
		t.Fatalf("7d: %q", tt.Texts())
	}
}

func TestConfirmWarnsOnlyForNewOptIns(t *testing.T) {
	root := "Allowing root: the AI runs as root, and a command it plants can capture sudo or su passwords you type or store"
	sudoNew := "sudo-exec will run without asking: the AI has full root on this host"
	still, too := "sudo-exec still asks.", "sudo-exec also runs without asking on this host, the same as plain exec."
	_, tt, _, _ := autoApp(t, autoBox(), "24h")
	choose(t, tt, "Off", "15 min")
	clickT(t, tt, "Allow on root hosts")
	clickT(t, tt, "Also auto-allow sudo-exec")
	clickT(t, tt, "Save")
	tt.flush()
	if !tt.HasText(root) || !tt.HasText(sudoNew) || tt.HasText(still) || tt.HasText(too) {
		t.Fatalf("both newly ticked: %q", tt.Texts())
	}

	s := autoBox()
	s.AutoAllowRoot, s.AutoAllowSudo = true, true
	_, tt, _, _ = confirmApp(t, s, "15 min", "24h")
	if tt.HasText(root) || tt.HasText(sudoNew) || tt.HasText(still) || !tt.HasText(too) {
		t.Fatalf("already ticked: %q", tt.Texts())
	}
}

// The root check landing moves the buttons: Enable waits again.
func TestRootCheckRestartsTheDelay(t *testing.T) {
	gate := make(chan struct{})
	_, tt, d, c := autoApp(t, autoBox(), "24h")
	d.set(func() {
		d.gates = map[string]chan struct{}{"servers.autoAllowCheck": gate}
		d.check = &hubapi.AutoCheck{UID: 0}
	})
	choose(t, tt, "Off", "15 min")
	clickT(t, tt, "Save")
	pass(c, tt)
	close(gate)
	tt.flush()
	if !tt.HasText("This is root access: the AI can do anything on this host.") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Enable")
	tt.flush()
	if hasCall(d, "servers.save") {
		t.Fatal("Enable worked right after the check moved the buttons")
	}
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if !hasCall(d, "servers.save") {
		t.Fatal("Enable did not work once the delay passed")
	}
}

func TestCheckFailureSaysSo(t *testing.T) {
	_, tt, d, _ := autoApp(t, autoBox(), "24h")
	d.set(func() { d.errs = map[string]error{"servers.autoAllowCheck": errors.New("could not check this host")} })
	choose(t, tt, "Off", "15 min")
	clickT(t, tt, "Save")
	tt.flush()
	if !tt.HasText("Could not check this host's sudo access.") || tt.HasText("This is root access: the AI can do anything on this host.") {
		t.Fatal(tt.Texts())
	}
}

// A host the hub would refuse never gets an Enable, nor a root check.
func TestRefusedHostCannotEnable(t *testing.T) {
	s := autoBox()
	s.AutoAllowRefused = "root login"
	_, tt, d, c := autoApp(t, s, "24h")
	choose(t, tt, "Off", "15 min")
	if !tt.HasText("root login") {
		t.Fatal("no refusal hint in the editor:", tt.Texts())
	}
	d.resetCalls()
	clickT(t, tt, "Save")
	tt.flush()
	if !tt.HasText("Auto-allow is not available here: root login") || tt.HasText("sudo-exec still asks.") || hasCall(d, "servers.autoAllowCheck") {
		t.Fatalf("texts %q, calls %v", tt.Texts(), d.callList())
	}
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if hasCall(d, "servers.save") {
		t.Fatal("Enable saved a refused host")
	}
}

// Cancel, Enter (Cancel has the focus), Esc and Enter in the typed name
// all go back to the editor with the draft kept, and save nothing.
func TestCancelReturnsToTheEditor(t *testing.T) {
	for _, how := range []string{"Cancel", "Enter", "Esc", "Enter in the name"} {
		a, tt, d, _ := confirmApp(t, autoBox(), "Until turned off", "24h")
		switch how {
		case "Cancel":
			clickT(t, tt, "Cancel")
		case "Enter":
			if !tt.Focused("Cancel") {
				t.Fatal("Cancel does not have the focus")
			}
			tt.Key(0, ui.KeyEnter)
		case "Esc":
			tt.Key(0, ui.KeyEscape)
		default:
			clickT(t, tt, "Type box to confirm")
			tt.Type("box")
			tt.Key(0, ui.KeyEnter)
		}
		tt.flush()
		if a.editor == nil || a.editor.confirm != nil || a.editor.draft.AutoAllow != "forever" || hasCall(d, "servers.save") || tt.HasText("Auto-allow AI commands on box") {
			t.Fatalf("%s: editor %+v, calls %v", how, a.editor, d.callList())
		}
	}
}

// Enable's error shows in the dialog, which stays, and waits again.
func TestEnableErrorStaysInTheDialog(t *testing.T) {
	a, tt, d, c := confirmApp(t, autoBox(), "15 min", "24h")
	d.set(func() { d.errs = map[string]error{"servers.save": errors.New("server changed; try again")} })
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if a.editor == nil || a.editor.confirm == nil || !tt.HasText("server changed; try again") || a.editor.err != "" {
		t.Fatalf("texts %q", tt.Texts())
	}
	clickT(t, tt, "Enable")
	tt.flush()
	if n := count(d.callList(), "servers.save"); n != 1 {
		t.Fatalf("%d saves: the error did not restart the delay", n)
	}
}

func TestEnableSavedButAlertsAndCloses(t *testing.T) {
	a, tt, d, c := confirmApp(t, autoBox(), "15 min", "24h")
	d.set(func() {
		d.errs = map[string]error{"servers.save": errors.New("saved, but auto-allow was not turned on: auto-allow refused: root login")}
	})
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if a.editor != nil || !tt.HasText("Saved, but auto-allow was not turned on: auto-allow refused: root login") || !hasCall(d, "servers") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

// A lock takes the dialog with the editor; the check's late reply and an
// unlock bring nothing back.
func TestLockDropsTheDialog(t *testing.T) {
	gate := make(chan struct{})
	a, tt, d, _ := autoApp(t, autoBox(), "24h")
	d.set(func() { d.gates = map[string]chan struct{}{"servers.autoAllowCheck": gate} })
	choose(t, tt, "Off", "15 min")
	clickT(t, tt, "Save")
	lockNow(t, a, tt, d)
	close(gate)
	d.set(func() { d.status.Locked = false })
	a.Update(a.refresh)
	tt.flush()
	if a.Screen() != model.ScreenReady || a.editor != nil || tt.HasText("Auto-allow AI commands on box") {
		t.Fatal("the dialog outlived the lock")
	}
}

func TestConfirmShowsTheNameEscaped(t *testing.T) {
	s := autoBox()
	s.Name = "bo\u202ex"
	_, tt, _, _ := confirmApp(t, s, "Until turned off", "24h")
	if !tt.HasText(`Auto-allow AI commands on bo\u202ex`) || !tt.HasText(`Type bo\u202ex to confirm`) {
		t.Fatal(tt.Texts())
	}
	for _, x := range tt.Texts() {
		if strings.ContainsRune(x, '\u202e') {
			t.Fatalf("%q shown unescaped", x)
		}
	}
}
```

- [ ] **Step 3: Run them and confirm they fail**

Run: `cd app && go test -short -count=1 ./view`
Expected: FAIL, build errors `a.editor.confirm undefined (type *editorState has no field or method confirm)`. (With only the controls of Step 5 in place and no dialog, `TestEnableWaitsThenSavesTheMode` fails `Save did not ask`.)

- [ ] **Step 4: Create the dialog**

Create `app/view/autoallow.go`:

```go
package view

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// confirmState is the Auto-allow dialog over the editor (Electron's
// AutoAllowDialog.tsx): what SaveConfirm asked, the refusal shown instead
// of the warnings, the root check's answer, the typed name, and Enable's
// last error. lc restarts Enable's delay when what it shows moves.
type confirmState struct {
	ask       model.Confirm
	refused   string
	check     *hubapi.AutoCheck
	checkFail bool
	typed     string
	err       string
	lc        *model.ListChanges
}

// openConfirm shows the dialog and, unless the host is refused, runs the
// hub's root check once; its reply touches only this dialog, while its hub
// runs and the ready screen shows.
func (a *App) openConfirm(s *hubapi.Server) {
	ed := a.editor
	cf := &confirmState{ask: model.SaveConfirm(s, ed.draft), refused: model.DraftRefusal(s, ed.draft)}
	ed.confirm = cf
	if cf.refused != "" {
		return
	}
	gen := a.hubGen
	go func() {
		r, err := a.hub().AutoAllowCheck(context.Background(), ed.name)
		a.Update(func() {
			if !a.ready(gen) || ed.confirm != cf {
				return
			}
			if err != nil {
				cf.checkFail = true
			} else {
				cf.check = &r
			}
		})
	}()
}

// buildConfirm is the dialog: Cancel has the focus, so Enter and Esc
// cancel (the typed-name field's Enter too); Enable is mouse-only and waits
// AllowDelay after the dialog opens or anything it shows changes.
func (a *App) buildConfirm(c *ui.Context) {
	t := c.Theme()
	ed, now := a.editor, a.Now()
	cf, d := ed.confirm, ed.draft
	key := model.DialogChangeKey(d.AutoAllow, cf.ask.RootNew, cf.ask.SudoNew, d.AutoAllowSudo, cf.check, cf.checkFail, cf.err)
	if cf.lc == nil {
		cf.lc = model.NewListChanges(key, now)
	}
	cf.lc.SetKey(key, now)
	enabled := func(now time.Time) bool {
		return !ed.busy && model.EnableAllowed(cf.ask.TypeName, cf.typed, ed.name, cf.refused, cf.lc.At, now)
	}
	if wait := model.DelayRemaining(cf.lc.At, cf.lc.At, now); wait > 0 {
		c.After(wait)
	}
	name := model.DisplayText(ed.name)
	title := "Auto-allow AI commands on " + name
	answer := 0 // 1 enable, 2 cancel
	open := true
	m := ui.Modal(c.Key(fmt.Sprintf("auto-%p", cf)), &open, func() { // keyed: Cancel takes the focus on each open
		ui.Column(c).Width(460).Gap(10).Padding(20).Children(func() {
			ui.Text(c, title).Bold().FontSize(16)
			if cf.refused != "" {
				ui.Text(c, "Auto-allow is not available here: "+model.DisplayText(cf.refused)).TextColor(t.Danger)
			} else {
				a.buildConfirmBody(c, cf, d, name, now, &answer)
			}
			if cf.err != "" {
				ui.Text(c, model.DisplayText(cf.err)).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if MouseButton(c, "Enable", enabled(now)) && enabled(a.Now()) {
					answer = 1
				}
				if ui.PrimaryButton(c, "Cancel").AutoFocus().Clicked() {
					answer = 2
				}
			})
		})
	}).Label(title)
	if m.Dismissed() {
		answer = 2
	}
	switch answer {
	case 1:
		a.saveHost()
	case 2:
		ed.confirm = nil // back to the editor, the draft kept
	}
}

// buildConfirmBody is the dialog's consent text, top to bottom as Electron's.
func (a *App) buildConfirmBody(c *ui.Context, cf *confirmState, d model.HostDraft, name string, now time.Time, answer *int) {
	t := c.Theme()
	ui.Text(c, "The AI runs any command this account can run on "+name+" without asking, including commands planted by what it reads (prompt injection), and can leave things that run later (cron jobs, SSH keys, shell startup files).")
	if !d.AutoAllowSudo {
		ui.Text(c, "sudo-exec still asks.")
	}
	if cf.ask.RootNew {
		ui.Text(c, "Allowing root: the AI runs as root, and a command it plants can capture sudo or su passwords you type or store").TextColor(t.Danger)
	}
	if cf.ask.SudoNew {
		ui.Text(c, "sudo-exec will run without asking: the AI has full root on this host").TextColor(t.Danger)
	}
	if d.AutoAllowSudo && !cf.ask.SudoNew {
		ui.Text(c, "sudo-exec also runs without asking on this host, the same as plain exec.")
	}
	if cf.check != nil && (cf.check.UID == 0 || cf.check.PasswordlessSudo) {
		ui.Text(c, "This is root access: the AI can do anything on this host.").TextColor(t.Danger)
	}
	if cf.checkFail {
		ui.Text(c, "Could not check this host's sudo access.").TextColor(t.TextMuted)
	}
	m, _ := model.AutoModeOf(d.AutoAllow)
	if m.Mode == "forever" {
		limit := ""
		if a.status != nil {
			limit = a.status.SoftLockLimit
		}
		ui.Text(c, "Until turned off").Bold()
		ui.Text(c, model.LimitText(limit)).TextColor(t.TextMuted)
	} else {
		ends := now.Add(m.Dur).Local().Format("15:04")
		c.After(now.Truncate(time.Minute).Add(time.Minute).Sub(now)) // the end time moves with the clock
		ui.Text(c, fmt.Sprintf("For %s (ends at %s)", m.Label, ends)).Bold()
		ui.Text(c, "Ends at "+ends+" or when you stop it. Locking the app, by hand or from inactivity, does not stop it: the AI keeps running on this host until then. To stop it, use Stop auto-allow and lock on the unlock screen, Stop on the host card, or Stop all auto-allow.").TextColor(t.TextMuted)
	}
	if cf.ask.TypeName {
		var in ui.Element
		ui.Field(c, "Type "+name+" to confirm", func() { in = ui.TextInput(c.Key("typed"), &cf.typed) })
		if in.Submitted() {
			*answer = 2 // Enter cancels, as everywhere in the dialog
		}
	}
}
```

- [ ] **Step 5: Wire it into the editor**

In `app/view/editor.go`, add the field to `editorState`, after `err string`:

```go
	confirm         *confirmState // the Auto-allow dialog over the editor
```

Replace `keptMode` and the head of `saveHost` (from the comment `// keptMode is the autoAllow a save sends` down to `gen := a.hubGen` inside `saveHost`) with:

```go
// submitHost is Save and Enter: a draft that arms a grant asks first
// (Electron's saveConfirm), anything else saves.
func (a *App) submitHost(s *hubapi.Server) {
	ed := a.editor
	if ed.busy || model.PortError(ed.draft.Port) != "" {
		return
	}
	if s != nil && model.SaveConfirm(s, ed.draft).Needed {
		a.openConfirm(s)
		return
	}
	a.saveHost()
}

// saveHost sends servers.save with the draft's auto-allow, which the hub
// arms in the same write. A bad port is never sent: typed text reaches the
// draft only in the next build, after the check buildEditor drew. The reply
// is dropped if its hub stopped or the ready screen went (a lock); it
// touches this editor only, never one opened since, and its error goes to
// the Auto-allow dialog that sent it while that is still open.
func (a *App) saveHost() {
	ed := a.editor
	if ed == nil || ed.busy || model.PortError(ed.draft.Port) != "" {
		return
	}
	in, mode, cf := model.ToInput(ed.draft), ed.draft.AutoAllow, ed.confirm
	ed.busy, ed.err = true, ""
	gen := a.hubGen
```

In `saveHost`'s reply, replace

```go
			if err != nil && !strings.HasPrefix(err.Error(), savedBut) {
				ed.err = err.Error()
				return
			}
```

with

```go
			if err != nil && !strings.HasPrefix(err.Error(), savedBut) {
				if cf != nil && ed.confirm == cf {
					cf.err = err.Error()
				} else {
					ed.err = err.Error()
				}
				return
			}
```

At the end of `buildEditor`, replace

```go
	case submit:
		a.saveHost()
	}
}
```

with

```go
	case submit:
		a.submitHost(s)
	}
	if ed.confirm != nil && a.editor == ed {
		a.buildConfirm(c)
	}
}
```

Replace `buildAIAccess` (with its comment) with:

```go
// buildAIAccess is the AI access fold: Visible to AI, the Auto-allow select
// (labels on screen, the hub's values in the draft), and the two opt-ins,
// which only count while a mode is chosen.
func (a *App) buildAIAccess(c *ui.Context, s *hubapi.Server) {
	t := c.Theme()
	d := &a.editor.draft
	ui.Field(c, "Visible to AI", func() { ui.Switch(c, &d.AIVisible) })
	ui.Text(c, "AI clients can see this server and ask to run commands. Commands wait for your approval unless auto-allow is on.").TextColor(t.TextMuted)
	if s == nil || s.HostKey == "" {
		ui.Text(c, "Needs a pinned host key.").TextColor(t.TextMuted)
	}
	labels, cur := []string{"Off"}, "Off"
	for _, m := range model.AutoModes {
		labels = append(labels, m.Label)
		if m.Mode == d.AutoAllow {
			cur = m.Label
		}
	}
	var sel ui.Element
	f := ui.Field(c, "Auto-allow", func() { sel = ui.Select(c.Key("auto-allow"), &cur, labels).Disabled(s == nil) })
	if s == nil {
		f.Description("Save the host and trust its key first")
	}
	if sel.Changed() {
		d.AutoAllow = "off"
		for _, m := range model.AutoModes {
			if m.Label == cur {
				d.AutoAllow = m.Mode
			}
		}
	}
	if n := model.TimedNote(s, a.Now()); n != "" {
		ui.Text(c, n).TextColor(t.TextMuted)
	}
	off := d.AutoAllow == "off"
	ui.Checkbox(c.Key("opt-root"), &d.AutoAllowRoot, "Allow on root hosts").Disabled(off)
	ui.Text(c, "Lets a grant run on a root login, or a host with a stored su or sudo password.").TextColor(t.TextMuted)
	ui.Checkbox(c.Key("opt-sudo"), &d.AutoAllowSudo, "Also auto-allow sudo-exec").Disabled(off)
	ui.Text(c, "sudo-exec runs without asking on a granted host, the same as plain exec.").TextColor(t.TextMuted)
	if r := model.DraftRefusal(s, *d); !off && r != "" {
		ui.Text(c, model.DisplayText(r)).TextColor(t.Warning)
	}
}
```

- [ ] **Step 6: Run the tests**

Run: `cd app && go test -short -count=1 ./view`
Expected: PASS (the 16 new tests and every 3a test; `TestSaveKeepsOnlyALiveForeverGrant` is gone).

- [ ] **Step 7: The Gate, the rune check, then commit**

```bash
cd app
go vet ./... && test -z "$(gofmt -l .)"
go test -race -count=1 ./...
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=1 ./...
GOOS=linux go vet ./... && GOOS=linux go test -c -o /dev/null ./view
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```
```bash
perl -CSD -ne 'print "$ARGV:$.\n" if /[\x{200B}-\x{200F}\x{2028}-\x{202E}\x{2066}-\x{2069}\x{FEFF}]/; close ARGV if eof' app/view/autoallow.go app/view/editor.go app/view/autoallow_test.go app/view/app_test.go
```
```bash
git add app/view/autoallow.go app/view/editor.go app/view/autoallow_test.go app/view/app_test.go app/view/editor_test.go
git commit -m "app: Auto-allow select, opt-ins and the confirm dialog; saveConfirm replaces keptMode

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 4: Ticking chips, the card's Stop and the tab dot

Ports `HostList.tsx:47-64` (chip and Stop), `App.tsx:39-48, 224-245` (the chip clock, `stopAuto`, `setAllAuto`) and `TerminalTabs.tsx:91` (renderer.md §2.3). Closes slice 1's M3 (chips did not tick) and ledger L50 (`stopAll` had no gen guard). Decisions 3, 5, 8.

**Files:**
- Modify: `app/view/app.go` (`stopAll`), `app/view/home.go`, `app/view/tabs.go`
- Test: `app/view/cards_test.go` (new)

**Interfaces:**
- Consumes: `model.ChipLabel`, `IsActive`, `AutoHostNames`, `PausedHosts` (slice 1); `a.ready` (3a); Task 3's `autoBox`; test helpers `readyApp`, `newApp`, `note`, `lockNow`.
- Produces: `func (a *App) setAuto(names []string, mode, verb string)` (Task 5's Resume and Stop use it); `stopAll` now calls it.

- [ ] **Step 1: Write the failing tests**

Create `app/view/cards_test.go`:

```go
package view

import (
	"errors"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// The timed chip, the AI button's count and the card's Stop follow the
// app's clock; the redraw asks the hub nothing.
func TestChipTicksFromTheAppClock(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Until: time.Date(2026, 10, 15, 12, 14, 12, 0, time.UTC).Format(time.RFC3339)}
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{s}}
	_, tt, c, _ := newApp(t, d)
	tt.flush()
	if _, ok := tt.Find("Stop auto-allow box"); !ok || !tt.HasText("Auto 15m") || !tt.HasText("auto 1") {
		t.Fatalf("texts %q", tt.Texts())
	}
	d.resetCalls()
	c.add(10 * time.Minute)
	tt.Frame()
	if !tt.HasText("Auto 5m") {
		t.Fatalf("the chip did not tick: %q", tt.Texts())
	}
	c.add(5 * time.Minute)
	tt.Frame()
	if _, ok := tt.Find("Stop auto-allow box"); ok || tt.HasText("Auto 0m") || tt.HasText("auto 1") {
		t.Fatalf("an ended grant still shows: %q", tt.Texts())
	}
	if got := d.callList(); len(got) != 0 {
		t.Fatalf("the clock called %v", got)
	}
}

func TestCardStopEndsTheGrant(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true, Paused: true}
	_, tt, d := readyApp(t, []hubapi.Server{s})
	if !tt.HasText("Auto paused") {
		t.Fatal(tt.Texts())
	}
	d.resetCalls()
	clickT(t, tt, "Stop auto-allow box")
	tt.flush()
	if params(d, "servers.setAutoAllow") != `{"mode":"off","server":"box"}` || !hasCall(d, "servers") {
		t.Fatalf("calls %v %s", d.callList(), params(d, "servers.setAutoAllow"))
	}
	d.set(func() { d.errs = map[string]error{"servers.setAutoAllow": errors.New("boom")} })
	clickT(t, tt, "Stop auto-allow box")
	tt.flush()
	if !tt.HasText("Could not stop auto-allow on box: boom") {
		t.Fatal(tt.Texts())
	}
}

// A stop whose reply lands behind the lock screen changes nothing there.
func TestStopReplyBehindTheLockChangesNothing(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true}
	a, tt, d := readyApp(t, []hubapi.Server{s})
	gate := make(chan struct{})
	d.set(func() {
		d.gates = map[string]chan struct{}{"servers.setAutoAllow": gate}
		d.errs = map[string]error{"servers.setAutoAllow": errors.New("Vault is locked; unlock it in the app")}
	})
	clickT(t, tt, "Stop auto-allow box")
	lockNow(t, a, tt, d)
	d.resetCalls()
	close(gate)
	tt.flush()
	if a.alert != "" || hasCall(d, "servers") {
		t.Fatalf("alert %q, calls %v", a.alert, d.callList())
	}
}

func TestTabDotFollowsTheGrant(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true}
	a, tt, _ := readyApp(t, []hubapi.Server{s})
	a.Update(func() { a.tabs.Open("box") })
	tt.flush()
	if _, ok := tt.Find("auto-allow on"); !ok {
		t.Fatal("no auto-allow dot on box's tab")
	}
	note(t, a, tt, "autoAllow.off", map[string]string{"server": "box", "reason": "turned off"})
	if _, ok := tt.Find("auto-allow on"); ok {
		t.Fatal("the dot outlived the grant")
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test -short -count=1 ./view -run 'ChipTicks|CardStop|StopReply|TabDot'`
Expected: FAIL: `TestChipTicksFromTheAppClock` (`texts …`: no `Stop auto-allow box`), `TestCardStopEndsTheGrant` and `TestStopReplyBehindTheLockChangesNothing` (`ui: no element shows "Stop auto-allow box"`), `TestTabDotFollowsTheGrant` (`no auto-allow dot on box's tab`).

- [ ] **Step 3: One helper for every set of grants**

In `app/view/app.go`, replace `stopAll` (the whole function) with:

```go
func (a *App) stopAll() { a.setAuto(model.AutoHostNames(a.servers, a.Now()), "off", "stop") }

// setAuto sets mode on each of names (Electron's setAllAuto): every host is
// tried after a failure, the host list is read again either way, and a
// failure shows in the alert row as "Could not <verb> auto-allow on …".
// The reply is dropped if its hub stopped or the ready screen went.
func (a *App) setAuto(names []string, mode, verb string) {
	gen := a.hubGen
	go func() {
		var failed []string
		var last string
		for _, n := range names {
			if err := a.hub().SetAutoAllow(context.Background(), n, mode); err != nil {
				failed, last = append(failed, n), err.Error()
			}
		}
		a.Update(func() {
			if !a.ready(gen) {
				return
			}
			a.alert = ""
			if len(failed) > 0 {
				a.alert = fmt.Sprintf("Could not %s auto-allow on %s: %s", verb, strings.Join(failed, ", "), last)
			}
			a.reloadServers()
			a.refresh()
		})
	}()
}
```

- [ ] **Step 4: The chip clock and the card's Stop**

In `app/view/home.go`, add `"time"` to the imports (after `"strings"`). In `buildReady`, after the line `liveAuto := autoN - len(model.PausedHosts(a.servers))`, add:

```go
	for _, s := range a.servers {
		if s.AutoAllow != nil && s.AutoAllow.Until != "" {
			// Timed chips, the AI button's count and the Stop buttons follow
			// the app's clock: a redraw, never a hub call (Electron's 15 s).
			c.After(15 * time.Second)
			break
		}
	}
```

In `buildHosts`, after `t := c.Theme()`, add `now := a.Now()`. In the card, replace

```go
							if chip := model.ChipLabel(s.AutoAllow, a.Now()); chip != "" {
								ui.Text(c, chip).TextColor(t.Warning)
							}
						})
```

with

```go
							if chip := model.ChipLabel(s.AutoAllow, now); chip != "" {
								ui.Text(c, chip).TextColor(t.Warning)
							}
						})
						// Always shown while a grant is on or paused: it must be stoppable at a glance.
						if model.IsActive(s.AutoAllow, now) || s.AutoAllow != nil && s.AutoAllow.Paused {
							if ui.Button(c, "\u25a0").Label("Stop auto-allow " + shownName).Tooltip("Stop auto-allow").Clicked() {
								a.setAuto([]string{name}, "off", "stop")
							}
						}
```

- [ ] **Step 5: The tab dot**

In `app/view/tabs.go`, in `buildTabStrip`, replace

```go
	addr := map[string]string{}
	for _, s := range a.servers {
		addr[s.Name] = model.Addr(s)
	}
```

with

```go
	th := c.Theme()
	addr, auto := map[string]string{}, map[string]bool{}
	for _, s := range a.servers {
		addr[s.Name] = model.Addr(s)
	}
	for _, n := range model.AutoHostNames(a.servers, a.Now()) {
		auto[n] = true // on or paused: Electron's .autodot
	}
```

and make the first child of each tab's row the dot:

```go
			ui.Row(c.Key("tab-" + id)).Gap(2).Children(func() {
				if auto[server] {
					ui.Text(c, "\u25cf").Label("auto-allow on").TextColor(th.Warning)
				}
```

(`th`, not `t`: the loop's tab is `t`.)

- [ ] **Step 6: Run the tests**

Run: `cd app && go test -short -count=1 ./view -run 'ChipTicks|CardStop|StopReply|TabDot|AutoFeedAndStopAll|LockButtonAndAIButton'`
Expected: PASS.

- [ ] **Step 7: The Gate, the rune check, then commit**

```bash
cd app
go vet ./... && test -z "$(gofmt -l .)"
go test -race -count=1 ./...
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=1 ./...
GOOS=linux go vet ./... && GOOS=linux go test -c -o /dev/null ./view
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```
```bash
perl -CSD -ne 'print "$ARGV:$.\n" if /[\x{200B}-\x{200F}\x{2028}-\x{202E}\x{2066}-\x{2069}\x{FEFF}]/; close ARGV if eof' app/view/app.go app/view/home.go app/view/tabs.go app/view/cards_test.go
```
```bash
git add app/view/app.go app/view/home.go app/view/tabs.go app/view/cards_test.go
git commit -m "app: chips tick from the app clock; Stop on the card; auto-allow dot on tabs

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 5: The paused and ran-while-locked banners

Ports `AutoAllowPaused.tsx`, `RanLocked.tsx`, `App.tsx:91, 101, 204, 228-245, 306` and the banner's two places (`HostList.tsx:28`, `ApprovalPanel.tsx:90`) (renderer.md §2.4, §2.5). Decisions 9, 10.

**Files:**
- Modify: `app/view/app.go`, `app/view/home.go`, `app/view/approvals.go`, `app/view/autoallow.go` (append)
- Test: `app/view/app_test.go` (`fakeDoor`), `app/view/banners_test.go` (new)

**Interfaces:**
- Consumes: Task 2 `model.KeepRan`, `RanLockedText`; Task 4 `setAuto`; `model.PausedHosts`, `ListChanges`, `TrustEnabled`; Task 3's `autoBox`, `pass`; test helpers `newApp`, `note`, `reqN`, `lockNow`, `count`, `params`.
- Produces: `App.ranLocked []hubapi.RanLocked`, `App.pausedHome, App.pausedCol *model.ListChanges`; `func (a *App) buildPaused(c *ui.Context, lc **model.ListChanges)`; `fakeDoor.ran []hubapi.RanLocked` (the next unlock reply's `ranWhileLocked`).

- [ ] **Step 1: Let the fake door's unlock report runs**

In `app/view/app_test.go`, add to `fakeDoor`'s fields, after `check`:

```go
	ran      []hubapi.RanLocked // the next unlock reply's ranWhileLocked
```

and in the `"unlock"` case replace

```go
		d.mu.Lock()
		d.status.Locked = false
		d.mu.Unlock()
		out = map[string]any{}
```

with

```go
		d.mu.Lock()
		d.status.Locked = false
		out = hubapi.UnlockReply{RanWhileLocked: d.ran}
		d.mu.Unlock()
```

Run `gofmt -w app/view/app_test.go`.

- [ ] **Step 2: Write the failing tests**

Create `app/view/banners_test.go`:

```go
package view

import (
	"errors"
	"testing"

	"github.com/lang315/sshgate/app/hubapi"
)

const pausedBox = "Auto-allow is paused on box. Once resumed, the AI keeps running there while the app is locked."

func pausedApp(t *testing.T) (*App, *tester, *fakeDoor, *clock) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true, Paused: true}
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{s}}
	a, tt, c, _ := newApp(t, d)
	tt.flush()
	return a, tt, d, c
}

func TestResumeWaitsThenResumesThePausedHosts(t *testing.T) {
	a, tt, d, c := pausedApp(t)
	if !tt.HasText(pausedBox) {
		t.Fatal(tt.Texts())
	}
	d.resetCalls()
	clickT(t, tt, "Resume")
	tt.flush()
	if hasCall(d, "servers.setAutoAllow") {
		t.Fatal("Resume worked before the delay")
	}
	// A host joining the list moves the banner: Resume waits again.
	db := autoBox()
	db.Name, db.AutoAllow = "db", &hubapi.AutoAllow{Forever: true, Paused: true}
	d.set(func() { d.servers = append(d.servers, db) })
	pass(c, tt)
	a.Update(a.reloadServers)
	tt.flush()
	clickT(t, tt, "Resume")
	tt.flush()
	if hasCall(d, "servers.setAutoAllow") || !tt.HasText("Auto-allow is paused on box, db.") {
		t.Fatalf("calls %v, texts %q", d.callList(), tt.Texts())
	}
	pass(c, tt)
	clickT(t, tt, "Resume")
	tt.flush()
	if n := count(d.callList(), "servers.setAutoAllow"); n != 2 || params(d, "servers.setAutoAllow") != `{"mode":"forever","server":"db"}` || !hasCall(d, "servers") {
		t.Fatalf("calls %v, last %s", d.callList(), params(d, "servers.setAutoAllow"))
	}
	d.set(func() { d.errs = map[string]error{"servers.setAutoAllow": errors.New("boom")} })
	clickT(t, tt, "Stop")
	tt.flush()
	if params(d, "servers.setAutoAllow") != `{"mode":"off","server":"db"}` || !tt.HasText("Could not stop auto-allow on box, db: boom") {
		t.Fatalf("last %s, texts %q", params(d, "servers.setAutoAllow"), tt.Texts())
	}
}

// The AI column shows the banner too, below its request list: a request's
// Allow does not move when it appears.
func TestPausedBannerInTheAIColumnLeavesAllow(t *testing.T) {
	a, tt, d, c := pausedApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "ls")})
	pass(c, tt)
	if count(tt.Texts(), pausedBox) != 2 {
		t.Fatalf("want the banner on Hosts and in the AI column: %q", tt.Texts())
	}
	allow, _ := tt.Find("Allow")
	d.set(func() { d.servers[0].AutoAllow = nil })
	a.Update(a.reloadServers)
	tt.flush()
	if after, _ := tt.Find("Allow"); after != allow || tt.HasText(pausedBox) {
		t.Fatalf("Allow moved from %+v to %+v", allow, after)
	}
}

func TestRanWhileLockedBanner(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}, pw: "right", ran: []hubapi.RanLocked{{Server: "box", Count: 1}}}
	a, tt, _, _ := newApp(t, d)
	a.Update(func() { a.unlock("right") })
	tt.flush()
	const ran = "While the app was locked the AI ran 1 command on box. See the Audit tab."
	if !tt.HasText(ran) {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Dismiss")
	if tt.HasText(ran) {
		t.Fatal("Dismiss left the banner")
	}
	lockNow(t, a, tt, d)
	a.Update(func() { a.unlock("right") })
	tt.flush()
	if !tt.HasText(ran) {
		t.Fatal("a second soft lock's runs did not show")
	}
	lockNow(t, a, tt, d)
	d.set(func() { d.ran = nil })
	a.Update(func() { a.unlock("right") })
	tt.flush()
	if tt.HasText(ran) {
		t.Fatal("the last lock's runs outlived the next lock")
	}
}
```

- [ ] **Step 3: Run them and confirm they fail**

Run: `cd app && go test -short -count=1 ./view -run 'Resume|PausedBanner|RanWhile'`
Expected: FAIL: `TestResumeWaitsThenResumesThePausedHosts` and `TestPausedBannerInTheAIColumnLeavesAllow` (no `Auto-allow is paused on box.` in the texts), `TestRanWhileLockedBanner` (no `While the app was locked …`).

- [ ] **Step 4: Implement**

In `app/view/app.go`, add to `App`'s fields, after `col colState`:

```go
	ranLocked    []hubapi.RanLocked // what auto-allow ran while locked, from the unlock reply
	// The paused banner's Resume delay on the Hosts tab and in the AI
	// column; nil while that banner is not drawn.
	pausedHome, pausedCol *model.ListChanges
```

In `setScreen`'s `default:` branch, replace `a.servers = nil` with `a.servers, a.ranLocked = nil, nil`. In `unlock`, add `gen := a.hubGen` before `go func() {`, take the reply (`r, err := a.hub().Unlock(context.Background(), pw)`), and after the `if err != nil { … } else { … }` block add:

```go
			if err == nil && gen == a.hubGen {
				a.ranLocked = model.KeepRan(a.ranLocked, r.RanWhileLocked)
			}
```

In `Build`, inside `if a.screen != model.ScreenReady {`, add after the `a.hk.mounted = false` line:

```go
		a.pausedHome, a.pausedCol = nil, nil
```

In `app/view/home.go`'s `buildReady`, replace `a.col.mounted = false` (under `if !a.aiOpen {`) with `a.col.mounted, a.pausedCol = false, nil`; insert the ran-while-locked banner before `if a.alert != "" {`:

```go
				if text := model.RanLockedText(a.ranLocked); text != "" {
					ui.Row(c).Gap(8).Padding(8, 12).Background(t.Warning.Alpha(0.15)).Role(ui.RoleStatus).Children(func() {
						ui.Text(c, model.DisplayText(text)).Grow(1)
						if ui.Button(c, "Dismiss").Clicked() {
							a.ranLocked = nil
						}
					})
				}
```

and, inside `if t, ok := a.tabs.Get(a.tabs.Active); ok {`, add `a.pausedHome = nil` before `a.buildTab(c, t)`. In `buildHosts`, after the `Hosts · %d` line, add:

```go
		a.buildPaused(c, &a.pausedHome)
```

In `app/view/approvals.go`'s `buildFeed`, after the row holding `Stop all auto-allow` (its closing `})`), add:

```go
		a.buildPaused(c, &a.pausedCol)
```

In `app/view/autoallow.go`, add `"strings"` to the imports (between `"fmt"` and `"time"`), and append:

```go
// buildPaused is the paused banner (Electron's AutoAllowPaused), on the
// Hosts tab and in the AI column: after an unlock, forever grants wait for
// Resume. Resume is mouse-only and waits AllowDelay after this banner shows
// or its hosts change; *lc is this placement's delay. Stop is a plain button.
func (a *App) buildPaused(c *ui.Context, lc **model.ListChanges) {
	hosts := model.PausedHosts(a.servers)
	if len(hosts) == 0 {
		*lc = nil
		return
	}
	t := c.Theme()
	now := a.Now()
	key := strings.Join(hosts, "\n")
	if *lc == nil {
		*lc = model.NewListChanges(key, now)
	}
	(*lc).SetKey(key, now)
	ok := model.TrustEnabled((*lc).At, now)
	if !ok {
		c.After((*lc).At.Add(model.AllowDelay).Sub(now))
	}
	names := make([]string, len(hosts))
	for i, h := range hosts {
		names[i] = model.DisplayText(h)
	}
	ui.Row(c).Gap(8).Padding(8, 12).Radius(8).Background(t.Warning.Alpha(0.15)).Role(ui.RoleStatus).Children(func() {
		ui.Text(c, "Auto-allow is paused on "+strings.Join(names, ", ")+". Once resumed, the AI keeps running there while the app is locked.").Grow(1)
		if MouseButton(c, "Resume", ok) && model.TrustEnabled((*lc).At, a.Now()) {
			a.setAuto(hosts, "forever", "resume")
		}
		if ui.Button(c, "Stop").Clicked() {
			a.setAuto(hosts, "off", "stop")
		}
	})
}
```

- [ ] **Step 5: Run the tests**

Run: `cd app && go test -short -count=1 ./view`
Expected: PASS.

- [ ] **Step 6: The Gate, the rune check, then commit**

```bash
cd app
go vet ./... && test -z "$(gofmt -l .)"
go test -race -count=1 ./...
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=1 ./...
GOOS=linux go vet ./... && GOOS=linux go test -c -o /dev/null ./view
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```
```bash
perl -CSD -ne 'print "$ARGV:$.\n" if /[\x{200B}-\x{200F}\x{2028}-\x{202E}\x{2066}-\x{2069}\x{FEFF}]/; close ARGV if eof' app/view/app.go app/view/home.go app/view/approvals.go app/view/autoallow.go app/view/banners_test.go
```
```bash
git add app/view/app.go app/view/home.go app/view/approvals.go app/view/autoallow.go app/view/app_test.go app/view/banners_test.go
git commit -m "app: paused banner with a guarded Resume, and the ran-while-locked banner

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 6: Settings

Ports `SettingsDialog.tsx` (without Touch ID), `App.tsx:249, 272-273, 345-352` and `SettingsDialog.test.ts:11-28` (renderer.md §4.2, §5). Decision 11.

**Files:**
- Create: `app/view/settings.go`
- Modify: `app/view/app.go` (field), `app/view/editor.go` (`dropDialogs`), `app/view/home.go` (gear, dialog)
- Test: `app/view/settings_test.go` (new)

**Interfaces:**
- Consumes: Task 1 `Client.SetSoftLockLimit`; Task 2 `model.Limits`, `LimitChange`, `LimitHelp`, `LimitNotice`, `SettingsChangeKey`, `SaveAllowed`; `model.AutoHostNames`; `a.ready`, `a.refresh`, `a.focusActive`, `MouseButton`; Task 3's `choose`, `pass`, `autoBox`.
- Produces: `App.settings *settingsState`; `type settingsState struct { limit, chosen string; busy bool; err string; lc *model.ListChanges }`; `func (a *App) buildSettings(c *ui.Context)`, `func (a *App) saveSettings(st *settingsState)`; `dropDialogs` also drops Settings.

- [ ] **Step 1: Write the failing tests**

Create `app/view/settings_test.go`:

```go
package view

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// settingsApp opens Settings on a hub whose limit is limit, with servers.
func settingsApp(t *testing.T, limit string, servers []hubapi.Server) (*App, *tester, *fakeDoor, *clock) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: limit}, servers: servers}
	a, tt, c, _ := newApp(t, d)
	tt.flush()
	d.resetCalls()
	clickT(t, tt, "Settings")
	if a.settings == nil {
		t.Fatal("Settings did not open")
	}
	return a, tt, d, c
}

func TestSettingsGearNeedsTheLimit(t *testing.T) {
	a, tt, _ := readyApp(t, nil) // status without softLockLimit
	clickT(t, tt, "Settings")
	if a.settings != nil {
		t.Fatal("Settings opened before the hub reported its limit")
	}
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}}
	_, tt, _, _ = newApp(t, d)
	if _, ok := tt.Find("Settings"); ok {
		t.Fatal("Settings shows on the lock screen")
	}
}

// SettingsDialog.test.ts: opened on the value in force, no notice, Save
// disabled and mouse-only, Cancel focused; Enter cancels.
func TestSettingsOpensOnTheLimitInForce(t *testing.T) {
	a, tt, d, c := settingsApp(t, "7d", nil)
	for _, s := range []string{"Auto-allow", "Keep running while locked", "7 days", model.LimitHelp("7d")} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if tt.HasText("Any program running as your user") || !tt.Focused("Cancel") {
		t.Fatalf("a notice shows, or Cancel lacks the focus: %q", tt.Texts())
	}
	pass(c, tt)
	clickT(t, tt, "Save")
	for i := 0; i < 10; i++ {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Save") {
			t.Fatal("Tab reached Save")
		}
	}
	tt.flush()
	if hasCall(d, "settings.setSoftLockLimit") {
		t.Fatal("Save stored an unchanged limit")
	}
	clickT(t, tt, "Cancel")
	tt.Frame()
	if a.settings != nil {
		t.Fatal("Cancel left the dialog")
	}
	clickT(t, tt, "Settings")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.settings != nil {
		t.Fatal("Enter did not cancel")
	}
}

func TestSettingsNoLimitWarnsRedAndWaits(t *testing.T) {
	box := autoBox()
	box.AutoAllow = &hubapi.AutoAllow{Forever: true}
	a, tt, d, c := settingsApp(t, "24h", []hubapi.Server{box})
	choose(t, tt, "24 hours (default)", "7 days")
	if !tt.HasText(model.LimitNotice("7d")+" On auto-allow now:") || count(tt.Texts(), "box") < 2 {
		t.Fatalf("7d: %q", tt.Texts())
	}
	pass(c, tt)
	choose(t, tt, "7 days", "No limit")
	if !tt.HasText(model.LimitNotice("none")+" On auto-allow now:") || !tt.HasText(model.LimitHelp("none")) {
		t.Fatalf("none: %q", tt.Texts())
	}
	clickT(t, tt, "Save") // the notice just changed: Save waits again
	tt.flush()
	if hasCall(d, "settings.setSoftLockLimit") {
		t.Fatal("Save worked right after the notice changed")
	}
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.flush()
	if params(d, "settings.setSoftLockLimit") != `{"limit":"none"}` || a.settings != nil {
		t.Fatalf("calls %v %s", d.callList(), params(d, "settings.setSoftLockLimit"))
	}
	if got := d.callList(); got[len(got)-1] != "status" {
		t.Fatalf("calls %v: the save must read status after", got)
	}
}

func TestSettingsNoticeWithNoHostsAndALowerLimit(t *testing.T) {
	_, tt, d, c := settingsApp(t, "3d", nil)
	choose(t, tt, "3 days", "7 days")
	if !tt.HasText(model.LimitNotice("7d") + " No host is on auto-allow now. The limit applies from the next grant.") {
		t.Fatal(tt.Texts())
	}
	choose(t, tt, "7 days", "24 hours (default)")
	if tt.HasText("Any program running as your user") {
		t.Fatal("a lower limit shows the notice")
	}
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.flush()
	if params(d, "settings.setSoftLockLimit") != `{"limit":"24h"}` {
		t.Fatal(d.callList())
	}
}

// An error stays in the dialog; Esc cancels, but not while a save runs.
func TestSettingsErrorStaysAndEscCancels(t *testing.T) {
	a, tt, d, c := settingsApp(t, "24h", nil)
	gate := make(chan struct{})
	d.set(func() {
		d.gates = map[string]chan struct{}{"settings.setSoftLockLimit": gate}
		d.errs = map[string]error{"settings.setSoftLockLimit": errors.New("Vault is locked; unlock it in the app")}
	})
	choose(t, tt, "24 hours (default)", "3 days")
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.settings == nil {
		t.Fatal("Esc closed the dialog while it saved")
	}
	close(gate)
	tt.flush()
	if a.settings == nil || !tt.HasText("Vault is locked; unlock it in the app") {
		t.Fatal(tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.settings != nil {
		t.Fatal("Esc did not cancel")
	}
}

// A lock closes Settings; unlocking does not bring it back.
func TestLockClosesSettings(t *testing.T) {
	a, tt, d, _ := settingsApp(t, "24h", nil)
	lockNow(t, a, tt, d)
	d.set(func() { d.status.Locked = false })
	a.Update(a.refresh)
	tt.flush()
	if a.Screen() != model.ScreenReady || a.settings != nil {
		t.Fatal("Settings outlived the lock")
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test -short -count=1 ./view -run 'Settings'`
Expected: FAIL, build error `a.settings undefined (type *App has no field or method settings)`.

- [ ] **Step 3: Implement**

Create `app/view/settings.go`:

```go
package view

import (
	"context"
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/model"
)

// settingsState is the open Settings dialog (Electron's SettingsDialog.tsx):
// the soft-lock limit in force when it opened, the choice, and Save's state.
// lc restarts Save's delay when the notice moves it.
type settingsState struct {
	limit, chosen string
	busy          bool
	err           string
	lc            *model.ListChanges
}

// buildSettings is the dialog: Cancel has the focus, so Enter and Esc
// cancel (not while saving); Save is mouse-only, enabled once the choice
// differs and AllowDelay has passed since the dialog opened or its notice
// changed. Raising the limit lists the hosts on auto-allow, amber; No limit
// lists them red.
func (a *App) buildSettings(c *ui.Context) {
	st := a.settings
	if st == nil {
		return
	}
	t, now := c.Theme(), a.Now()
	hosts := model.AutoHostNames(a.servers, now)
	slices.Sort(hosts)
	change := model.LimitChange(st.limit, st.chosen)
	key := model.SettingsChangeKey(st.chosen, hosts, st.err)
	if st.lc == nil {
		st.lc = model.NewListChanges(key, now)
	}
	st.lc.SetKey(key, now)
	if wait := model.DelayRemaining(st.lc.At, st.lc.At, now); wait > 0 {
		c.After(wait)
	}
	labels, cur := make([]string, len(model.Limits)), ""
	for i, l := range model.Limits {
		labels[i] = l.Label
		if l.Value == st.chosen {
			cur = l.Label
		}
	}
	answer := 0 // 1 save, 2 cancel
	open := true
	m := ui.Modal(c.Key(fmt.Sprintf("settings-%p", st)), &open, func() { // keyed: Cancel takes the focus on each open
		ui.Column(c).Width(460).Gap(10).Padding(20).Children(func() {
			ui.Text(c, "Settings").Bold().FontSize(16)
			ui.Text(c, "Auto-allow").Bold()
			var sel ui.Element
			ui.Field(c, "Keep running while locked", func() { sel = ui.Select(c.Key("limit"), &cur, labels) })
			if sel.Changed() {
				for _, l := range model.Limits {
					if l.Label == cur {
						st.chosen = l.Value
					}
				}
			}
			ui.Text(c, model.LimitHelp(st.chosen)).TextColor(t.TextMuted)
			if change == "higher" || change == "none" {
				bg := t.Warning
				if change == "none" {
					bg = t.Danger
				}
				ui.Column(c).Gap(4).Padding(8, 12).Radius(6).Background(bg.Alpha(0.15)).Role(ui.RoleStatus).Children(func() {
					if len(hosts) == 0 {
						ui.Text(c, model.LimitNotice(st.chosen)+" No host is on auto-allow now. The limit applies from the next grant.")
						return
					}
					ui.Text(c, model.LimitNotice(st.chosen)+" On auto-allow now:")
					for _, h := range hosts {
						ui.Text(c, model.DisplayText(h)).Font("monospace")
					}
				})
			}
			if st.err != "" {
				ui.Text(c, model.DisplayText(st.err)).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if MouseButton(c, "Save", model.SaveAllowed(change, st.busy, st.lc.At, now)) && model.SaveAllowed(change, st.busy, st.lc.At, a.Now()) {
					answer = 1
				}
				if ui.PrimaryButton(c, "Cancel").AutoFocus().Disabled(st.busy).Clicked() {
					answer = 2
				}
			})
		})
	}).Label("Settings")
	if m.Dismissed() && !st.busy {
		answer = 2
	}
	switch answer {
	case 1:
		a.saveSettings(st)
	case 2:
		a.settings = nil
		a.focusActive()
	}
}

// saveSettings stores the chosen limit, reads status for it, and closes; an
// error stays in the dialog. The reply is dropped if its hub stopped or the
// ready screen went (a lock closes the dialog), and touches only this one.
func (a *App) saveSettings(st *settingsState) {
	st.busy, st.err = true, ""
	gen := a.hubGen
	go func() {
		err := a.hub().SetSoftLockLimit(context.Background(), st.chosen)
		a.Update(func() {
			if !a.ready(gen) || a.settings != st {
				return
			}
			st.busy = false
			if err != nil {
				st.err = err.Error()
				return
			}
			a.settings = nil
			a.focusActive()
			a.refresh()
		})
	}()
}
```

In `app/view/app.go`, add to `App`'s fields, before `sheetSeq`:

```go
	settings               *settingsState
```

In `app/view/editor.go`, replace `dropDialogs` with:

```go
func (a *App) dropDialogs() { a.editor, a.imp, a.del, a.settings = nil, nil, nil, nil }
```

In `app/view/home.go`'s `buildReady`, between `a.buildThemeControl(c)` and `lock := ui.Button(c, "Lock")`, add:

```go
			if ui.Button(c, "\u2699").Label("Settings").Tooltip("Settings").Disabled(a.status == nil || a.status.SoftLockLimit == "").Clicked() {
				a.settings = &settingsState{limit: a.status.SoftLockLimit, chosen: a.status.SoftLockLimit}
			}
```

and after `a.buildDeleteConfirm(c)` at the end of `buildReady`, add `a.buildSettings(c)`.

- [ ] **Step 4: Run the tests**

Run: `cd app && go test -short -count=1 ./view`
Expected: PASS.

- [ ] **Step 5: The Gate, the rune check, then commit**

```bash
cd app
go vet ./... && test -z "$(gofmt -l .)"
go test -race -count=1 ./...
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=1 ./...
GOOS=linux go vet ./... && GOOS=linux go test -c -o /dev/null ./view
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```
```bash
perl -CSD -ne 'print "$ARGV:$.\n" if /[\x{200B}-\x{200F}\x{2028}-\x{202E}\x{2066}-\x{2069}\x{FEFF}]/; close ARGV if eof' app/view/settings.go app/view/home.go app/view/settings_test.go
```
```bash
git add app/view/settings.go app/view/app.go app/view/editor.go app/view/home.go app/view/settings_test.go
git commit -m "app: Settings dialog for the soft-lock limit, Save guarded and mouse-only

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 7: The MCP bridge test client and the flow helpers

Ports `desktop/e2e/autoHelpers.ts:7-47` (`Mcp`) and the audit readers of `autoallow-mcp.spec.ts:30-33` (renderer.md §6.5; spec "Testing" → End to end). Decisions 4, 12.

**Files:**
- Create: `app/internal/hubtest/bridge_unix.go`
- Test: `app/internal/hubtest/bridge_unix_test.go` (new)
- Modify: `app/view/flow_unix_test.go` (clock, `unlock`, `auditRec`, readers, tool helpers)

**Interfaces:**
- Consumes: `hubtest.Binaries`, `Runtime`, `SSHD`, `ExecResult` (slices 0–3a); `hubapi.Client`.
- Produces:
  - Package `hubtest` (unix): `type Bridge struct{…}`; `func StartBridge(t *testing.T, runtimeDir string) *Bridge`; `func (b *Bridge) Tool(name string, args map[string]any) <-chan ExecResult`.
  - Package `view` (flow tests): `flowT.rt string`, `flowT.ai *hubtest.Bridge`; `auditRec` gains `Sudo bool; Stdin string; WaitMs int64; Until, Limit string; Forever bool; Changed, Servers []string`; `func (f *flowT) records() []auditRec`; `func last(recs []auditRec, match func(auditRec) bool) int`; `func (f *flowT) lastConfig(server, action string) auditRec`; `func (f *flowT) lastExec(cmd string) auditRec`; `func (f *flowT) tool(name string, args map[string]any) <-chan hubtest.ExecResult`; `func (f *flowT) aiExec(server, cmd string) …`; `func (f *flowT) aiSudo(server, cmd string) …`.
  - The flow clock starts at `time.Now()`; `f.unlock()` waits for any host, not exactly one.

- [ ] **Step 1: Write the failing test**

Create `app/internal/hubtest/bridge_unix_test.go`:

```go
//go:build unix

package hubtest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/internal/hubtest"
)

// The bridge reaches the hub's MCP door: a locked hub lists box locked, an
// exec waits for the decision, and a denial comes back as the tool's error.
func TestBridgeRunsToolsThroughTheHub(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
	env, dir, _ := hubtest.Runtime(t)
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: []string{"hub", "--store=" + store}, Env: env}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx := context.Background()
	h := hubapi.New(p.Client())
	b := hubtest.StartBridge(t, dir)
	if r := <-b.Tool("list-servers", map[string]any{}); r.Err != nil || r.Stdout != "- box (127.0.0.1) [locked: unlock the app]\n" {
		t.Fatalf("list-servers while locked: %+v", r)
	}
	if _, err := h.Unlock(ctx, hubtest.Password); err != nil {
		t.Fatal(err)
	}
	decide := func(outcome, reason string) {
		t.Helper()
		var reqs []hubapi.Request
		for deadline := time.Now().Add(5 * time.Second); len(reqs) == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			reqs, _ = h.Pending(ctx)
		}
		if len(reqs) != 1 {
			t.Fatalf("pending %+v", reqs)
		}
		if err := h.Decide(ctx, reqs[0].ID, outcome, reason); err != nil {
			t.Fatal(err)
		}
	}
	res := b.Tool("exec", map[string]any{"server": "box", "command": "echo approved", "description": "bridge test"})
	decide(hubapi.Allowed, "")
	if r := <-res; r.Err != nil || r.ExitCode != 0 || !strings.Contains(r.Stdout, "echo approved") {
		t.Fatalf("%+v", r)
	}
	res = b.Tool("exec", map[string]any{"server": "box", "command": "rm -rf /", "description": "bridge test"})
	decide(hubapi.Denied, "not today")
	if r := <-res; r.Err == nil || r.Err.Error() != "Denied by user: not today" {
		t.Fatalf("%+v", r)
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `cd app && go test -count=1 ./internal/hubtest -run Bridge`
Expected: FAIL, build error `undefined: hubtest.StartBridge`.

- [ ] **Step 3: Implement the bridge client**

Create `app/internal/hubtest/bridge_unix.go`:

```go
//go:build unix

package hubtest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Bridge is the real MCP bridge, sshgate run with no arguments, as an AI
// client runs it: newline JSON-RPC over its stdio. Its tools reach the hub
// whose MCP door is in the runtime directory it was started with.
type Bridge struct {
	in   io.WriteCloser
	mu   sync.Mutex
	id   int
	wait map[int]chan bridgeReply // closed unanswered when the bridge exits
	dead bool
}

type bridgeReply struct {
	Result json.RawMessage
	Error  *struct{ Message string }
}

// StartBridge runs the bridge until the test ends, and initializes it.
func StartBridge(t *testing.T, runtimeDir string) *Bridge {
	t.Helper()
	bin, _ := Binaries(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "SSHGATE_RUNTIME_DIR="+runtimeDir)
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); cmd.Process.Kill(); cmd.Wait() })
	b := &Bridge{in: in, wait: map[int]chan bridgeReply{}}
	go b.read(out)
	ch, err := b.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "app-flow", "version": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case r, ok := <-ch:
		if !ok || r.Error != nil {
			t.Fatalf("bridge initialize: %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("bridge initialize: no reply in 10 s")
	}
	b.mu.Lock()
	err = b.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}})
	b.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// read hands each reply to its caller; notifications have no id.
func (b *Bridge) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		var m struct {
			ID *int `json:"id"`
			bridgeReply
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil {
			continue
		}
		b.mu.Lock()
		ch := b.wait[*m.ID]
		delete(b.wait, *m.ID)
		b.mu.Unlock()
		if ch != nil {
			ch <- m.bridgeReply
		}
	}
	b.mu.Lock()
	b.dead = true
	for id, ch := range b.wait {
		close(ch)
		delete(b.wait, id)
	}
	b.mu.Unlock()
}

// send writes one line; the caller holds mu, so lines never interleave.
func (b *Bridge) send(msg any) error {
	line, _ := json.Marshal(msg)
	_, err := b.in.Write(append(line, '\n'))
	return err
}

func (b *Bridge) request(method string, params any) (chan bridgeReply, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dead {
		return nil, errors.New("bridge exited")
	}
	b.id++
	ch := make(chan bridgeReply, 1)
	b.wait[b.id] = ch
	if err := b.send(map[string]any{"jsonrpc": "2.0", "id": b.id, "method": method, "params": params}); err != nil {
		delete(b.wait, b.id)
		return nil, err
	}
	return ch, nil
}

// Tool calls an MCP tool; the result arrives once the hub answers, after a
// human decision unless auto-allow ran it. The tool's text is Stdout, with
// ExitCode from its "exit code: N" line; a tool error's text is Err.
func (b *Bridge) Tool(name string, args map[string]any) <-chan ExecResult {
	out := make(chan ExecResult, 1)
	ch, err := b.request("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		out <- ExecResult{Err: err}
		return out
	}
	go func() {
		var r bridgeReply
		var ok bool
		select {
		case r, ok = <-ch:
		case <-time.After(30 * time.Second): // longer than any approval wait in the tests
			out <- ExecResult{Err: errors.New("bridge: no reply in 30 s")}
			return
		}
		if !ok {
			out <- ExecResult{Err: errors.New("bridge exited")}
			return
		}
		if r.Error != nil {
			out <- ExecResult{Err: errors.New(r.Error.Message)}
			return
		}
		var res struct {
			Content []struct{ Text string }
			IsError bool
		}
		if err := json.Unmarshal(r.Result, &res); err != nil {
			out <- ExecResult{Err: err}
			return
		}
		var text strings.Builder
		for _, c := range res.Content {
			text.WriteString(c.Text)
		}
		if res.IsError {
			out <- ExecResult{Err: errors.New(text.String())}
			return
		}
		e := ExecResult{Stdout: text.String()}
		fmt.Sscanf(e.Stdout, "exit code: %d", &e.ExitCode)
		out <- e
	}()
	return out
}
```

- [ ] **Step 4: Run it**

Run: `cd app && go test -count=1 ./internal/hubtest -run Bridge -v`
Expected: PASS (the bridge prints `sshgate bridge on stdio (forwarding to sshgate hub)` on stderr).

- [ ] **Step 5: The flow helpers**

In `app/view/flow_unix_test.go`, add to `flowT`'s fields, after `port int // sshtestd's`:

```go
	rt     string          // the hub's runtime directory, where its MCP door is
	ai     *hubtest.Bridge // started by the first tool call
```

In `flowWith`, replace `c := &clock{t: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}` with:

```go
	// The real time: the hub dates a timed grant's end by its own clock.
	c := &clock{t: time.Now()}
```

and add `rt: dir` to the returned `&flowT{…}` literal (after `port: srv.Port`). In `unlock`, replace the last two lines with:

```go
	// Not "Hosts · N": a terminal tab may cover the host list.
	f.until(func() bool { return f.a.Screen() == model.ScreenReady && len(f.a.Servers()) > 0 }, "the ready screen")
```

Replace the `auditRec` type (with its comment) with the following, which adds the readers and the tool helpers right after it:

```go
// auditRec is the part of an audit record the tests check.
type auditRec struct {
	Kind, Command, Outcome, Reason, Approval string
	Sudo                                     bool
	Stdin                                    string
	WaitMs                                   int64
	Action, Server                           string // config records
	Until, Limit                             string
	Forever                                  bool
	Changed, Servers                         []string
}

// records reads the hub's audit log, oldest first.
func (f *flowT) records() []auditRec {
	f.t.Helper()
	b, err := os.ReadFile(f.audit)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []auditRec
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var r auditRec
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			f.t.Fatalf("audit line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// last is the index of the newest record that match accepts, or -1.
func last(recs []auditRec, match func(auditRec) bool) int {
	for i := len(recs) - 1; i >= 0; i-- {
		if match(recs[i]) {
			return i
		}
	}
	return -1
}

// lastConfig waits for, and returns, server's newest config record with
// this action.
func (f *flowT) lastConfig(server, action string) auditRec {
	f.t.Helper()
	var rec auditRec
	f.until(func() bool {
		recs := f.records()
		i := last(recs, func(r auditRec) bool { return r.Kind == "config" && r.Action == action && r.Server == server })
		if i >= 0 {
			rec = recs[i]
		}
		return i >= 0
	}, fmt.Sprintf("a %s config record of %s", action, server))
	return rec
}

// lastExec waits for, and returns, cmd's newest exec record.
func (f *flowT) lastExec(cmd string) auditRec {
	f.t.Helper()
	var rec auditRec
	f.until(func() bool {
		recs := f.records()
		i := last(recs, func(r auditRec) bool { return r.Kind == "" && r.Command == cmd })
		if i >= 0 {
			rec = recs[i]
		}
		return i >= 0
	}, "the audit record of "+cmd)
	return rec
}

// tool runs an MCP tool through the real bridge, as an AI client does.
func (f *flowT) tool(name string, args map[string]any) <-chan hubtest.ExecResult {
	if f.ai == nil {
		f.ai = hubtest.StartBridge(f.t, f.rt)
	}
	return f.ai.Tool(name, args)
}

func (f *flowT) aiExec(server, cmd string) <-chan hubtest.ExecResult {
	return f.tool("exec", map[string]any{"server": server, "command": cmd, "description": "flow"})
}

func (f *flowT) aiSudo(server, cmd string) <-chan hubtest.ExecResult {
	return f.tool("sudo-exec", map[string]any{"server": server, "command": cmd, "description": "flow"})
}
```

Run `gofmt -w app/view/flow_unix_test.go`.

- [ ] **Step 6: Run the existing flows with the new helpers**

Run: `cd app && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -count=1 ./view -run 'TestFlow'`
Expected: PASS (every slice 1–3a flow, now on a clock that starts at the real time).

- [ ] **Step 7: The Gate, then commit**

```bash
cd app
go vet ./... && test -z "$(gofmt -l .)"
go test -race -count=1 ./...
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=1 ./...
GOOS=linux go vet ./... && GOOS=linux go test -c -o /dev/null ./view
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```
```bash
git add app/internal/hubtest/bridge_unix.go app/internal/hubtest/bridge_unix_test.go app/view/flow_unix_test.go
git commit -m "app: MCP bridge test client; flow helpers for audit records and AI tools

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 8: Flow tests: `autoallow` and `autoallow-mcp`

Ports `desktop/e2e/autoallow.spec.ts` and `autoallow-mcp.spec.ts` (renderer.md §6.4, §6.5, §6.9), every request through the real bridge (Decision 4), each step checked against the audit log where the e2e did. Decision 12 covers the clock, the root check and the root host.

**Files:**
- Create: `app/view/flowauto_unix_test.go`
- Modify: `app/view/flowhosts_unix_test.go` (`trust` takes the user)

**Interfaces:**
- Consumes: Task 7's `f.tool`, `f.aiExec`, `f.aiSudo`, `f.records`, `f.lastConfig`, `f.lastExec`, `last`; Task 3's `openAI`, `choose`; 3a's `f.fill`, `f.saveEditor`, `f.trust`; slice 1's `f.ran`, `f.denied`, `f.auto`, `f.pass`, `f.until`, `f.click`; `needLib`.
- Produces (used by Task 9): `func autoLabel(mode string) string`; `func (f *flowT) editAuto(server, mode string, on ...string)`; `func (f *flowT) enable()`; `func (f *flowT) chip(server, want string)`; `func (f *flowT) denyWaiting(ch <-chan hubtest.ExecResult, cmd string)`; `func (f *flowT) newHost(label, user, password string, visible bool)`; `func (f *flowT) stopAndLock()`; `func (f *flowT) trust(user string)`.
- Mapping: `autoallow.spec.ts` → `TestFlowAutoAllow` (its three tests in order); `autoallow-mcp.spec.ts` → `TestFlowAutoAllowMCP` (all but the root-host test) and `TestFlowAutoAllowRootHost`.

- [ ] **Step 1: Let `trust` take the user**

In `app/view/flowhosts_unix_test.go`, replace the head of `trust`:

```go
// trust answers the unknown-key prompt of box once Trust is enabled.
func (f *flowT) trust() {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText("Trust this host key?") }, "the host-key prompt")
	for _, s := range []string{"SHA256:", fmt.Sprintf("test@127.0.0.1:%d", f.port)} {
```

with

```go
// trust answers the unknown-key prompt for user on sshtestd once Trust is
// enabled.
func (f *flowT) trust(user string) {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText("Trust this host key?") }, "the host-key prompt")
	for _, s := range []string{"SHA256:", fmt.Sprintf("%s@127.0.0.1:%d", user, f.port)} {
```

and change its two calls in `TestFlowHostsCreateTrustEditForgetDelete` from `f.trust()` to `f.trust("test")`.

- [ ] **Step 2: Write the flows**

Create `app/view/flowauto_unix_test.go`:

```go
//go:build unix

package view

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/internal/hubtest"
	"github.com/lang315/sshgate/app/model"
)

// Ports of desktop/e2e/autoallow.spec.ts and autoallow-mcp.spec.ts. Every
// AI request goes through the real MCP bridge; autoallow.spec.ts sent its
// own straight to the door, with the same assertions.

// autoLabel is the Auto-allow select's text for mode.
func autoLabel(mode string) string {
	if m, ok := model.AutoModeOf(mode); ok {
		return m.Label
	}
	return "Off"
}

// editAuto opens server's editor, chooses mode (a select label) in AI
// access, turns on each switch or opt-in named in on, and presses Save:
// the e2e's editAuto. Opt-ins not named stay as they are.
func (f *flowT) editAuto(server, mode string, on ...string) {
	f.t.Helper()
	f.click("Hosts")
	f.click("Edit " + server)
	f.until(func() bool { return f.a.editor != nil }, "the host editor")
	openAI(f.t, f.tt)
	d := &f.a.editor.draft
	if cur := autoLabel(d.AutoAllow); cur != mode {
		choose(f.t, f.tt, cur, mode)
	}
	for _, l := range on {
		if !map[string]bool{"Visible to AI": d.AIVisible, "Allow on root hosts": d.AutoAllowRoot, "Also auto-allow sudo-exec": d.AutoAllowSudo}[l] {
			f.click(l)
			f.tt.Frame()
		}
	}
	f.click("Save")
}

// enable clicks the Auto-allow dialog's Enable once it may: sshtestd cannot
// answer the root check, and the failure landing restarts the delay, so
// it waits for that first.
func (f *flowT) enable() {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText("Could not check this host's sudo access.") }, "the root check")
	f.pass()
	f.click("Enable")
	f.until(func() bool { return f.a.editor == nil }, "the editor to close after Enable")
}

// chip waits until server's card shows a chip matching want ("" for none).
// The hub dates a timed grant by the real clock, so the app's clock moves up
// to it on each look; it never moves back.
func (f *flowT) chip(server, want string) {
	f.t.Helper()
	re := regexp.MustCompile("^" + want + "$")
	f.until(func() bool {
		if now := time.Now(); now.After(f.c.t) {
			f.c.t = now
			f.tt.Frame()
		}
		s := f.a.server(server)
		label := ""
		if s != nil {
			label = model.ChipLabel(s.AutoAllow, f.a.Now())
		}
		return s != nil && re.MatchString(label) && (label == "" || f.tt.HasText(label))
	}, server+"'s chip "+strconv.Quote(want))
}

// denyWaiting waits for cmd in the AI column, then denies it with Enter in
// its reason field: the e2e's waitThenDecide(deny).
func (f *flowT) denyWaiting(ch <-chan hubtest.ExecResult, cmd string) {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText(cmd) && len(f.a.Items()) == 1 }, cmd+" waiting in the AI column")
	f.click("Reason (optional)")
	f.tt.Key(0, ui.KeyEnter)
	f.denied(ch, "Denied by user")
}

// newHost adds a host on sshtestd through the editor.
func (f *flowT) newHost(label, user, password string, visible bool) {
	f.t.Helper()
	f.click("Hosts")
	f.click("New host")
	f.fill("Label", label)
	f.fill("Address", "127.0.0.1")
	f.fill("Port", strconv.Itoa(f.port))
	f.fill("User", user)
	f.fill("Password", password)
	if visible {
		openAI(f.t, f.tt)
		f.click("Visible to AI")
	}
	f.saveEditor()
}

// stopAndLock locks from the ready screen and ends every grant from the
// unlock screen, then unlocks again.
func (f *flowT) stopAndLock() {
	f.t.Helper()
	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Stop auto-allow and lock") }, "the soft-lock screen")
	f.click("Stop auto-allow and lock")
	f.until(func() bool { return !f.tt.HasText("AI auto-allow is still running on:") }, "the grants to end")
	f.unlock()
}

// TestFlowAutoAllow: autoallow.spec.ts, its three tests in order.
func TestFlowAutoAllow(t *testing.T) {
	f := flow(t)
	f.unlock()
	f.tt.SetSize(1000, 900)

	// A timed grant runs exec without a click; Stop on the card and Stop
	// auto-allow and lock end it.
	f.editAuto("box", "15 min")
	f.until(func() bool { return f.tt.HasText("Auto-allow AI commands on box") }, "the Auto-allow dialog")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	f.ran(f.aiExec("box", "echo auto-one"), "echo auto-one")
	f.click("AI requests")
	f.until(func() bool { return f.tt.HasText("Auto-allowed") && f.tt.HasText("echo auto-one") }, "the run in the Auto-allowed feed")
	f.click("Stop auto-allow box")
	f.chip("box", "")
	f.denyWaiting(f.aiExec("box", "echo must-ask"), "echo must-ask")

	f.editAuto("box", "15 min")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	f.stopAndLock()
	f.chip("box", "")
	f.denyWaiting(f.aiExec("box", "echo after-lock"), "echo after-lock")

	// A forever grant pauses after Stop auto-allow and lock, until Resume.
	f.editAuto("box", "Until turned off")
	f.fill("Type box to confirm", "box")
	f.enable()
	f.chip("box", "Auto ∞")
	f.stopAndLock()
	f.until(func() bool { return f.tt.HasText("Auto-allow is paused on box.") }, "the paused banner")
	f.denyWaiting(f.aiExec("box", "echo paused-asks"), "echo paused-asks")
	f.pass()
	f.click("Resume")
	f.chip("box", "Auto ∞") // armed before the next exec reaches the hub
	f.ran(f.aiExec("box", "echo resumed"), "echo resumed")
	f.click("Stop auto-allow box")
	f.chip("box", "")

	// The sudo-exec opt-in runs sudo-exec without a click.
	f.editAuto("box", "15 min", "Also auto-allow sudo-exec")
	f.until(func() bool {
		return f.tt.HasText("sudo-exec will run without asking: the AI has full root on this host")
	}, "the sudo warning")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	f.ran(f.aiSudo("box", "echo sudo-auto"), "echo sudo-auto")
	f.click("Stop auto-allow box")
	f.chip("box", "")
}

// TestFlowAutoAllowMCP: autoallow-mcp.spec.ts but its root-host test
// (TestFlowAutoAllowRootHost), each step checked against the audit log.
func TestFlowAutoAllowMCP(t *testing.T) {
	f := flow(t)
	f.unlock()
	f.tt.SetSize(1000, 900)

	// Baseline: an MCP exec waits for a human decision.
	out := f.aiExec("box", "echo base")
	f.until(func() bool { return f.tt.HasText("echo base") && len(f.a.Items()) == 1 }, "echo base waiting")
	f.pass()
	f.click("Allow")
	f.ran(out, "echo base")
	if r := f.lastExec("echo base"); r.Outcome != "allowed" || r.Approval != "" || r.WaitMs <= 0 {
		t.Fatalf("audit of echo base: %+v", r)
	}

	// stdin: the card shows it, Send to tab is off, and the command gets it.
	out = f.tool("exec", map[string]any{"server": "box", "command": "stdin-echo", "stdin": "line one\nline two\n", "description": "flow"})
	f.until(func() bool { return f.tt.HasText("stdin · 18 bytes, 2 lines") && f.tt.HasText("line two") }, "the stdin card")
	f.pass()
	f.click("Send to tab")
	f.tt.flush()
	if len(f.a.tabs.Tabs) != 0 {
		t.Fatal("Send to tab worked for a request with stdin")
	}
	f.click("Allow")
	f.ran(out, "line one\nline two")
	if r := f.lastExec("stdin-echo"); r.Outcome != "allowed" || r.Stdin != "line one\nline two\n" {
		t.Fatalf("audit of stdin-echo: %+v", r)
	}

	// A timed grant from the editor: MCP execs run with no decision;
	// sudo-exec still asks.
	f.editAuto("box", "15 min")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	if on := f.lastConfig("box", "autoAllowOn"); on.Until == "" || on.Forever {
		t.Fatalf("autoAllowOn: %+v", on)
	}
	f.ran(f.aiExec("box", "echo auto-1"), "echo auto-1")
	f.ran(f.aiExec("box", "echo auto-2"), "echo auto-2")
	f.auto("echo auto-1")
	f.auto("echo auto-2")
	f.until(func() bool { return f.tt.HasText("echo auto-2") }, "echo auto-2 in the Auto-allowed feed")
	f.denyWaiting(f.aiSudo("box", "echo sudo-asks"), "echo sudo-asks")
	if r := f.lastExec("echo sudo-asks"); r.Outcome != "denied" || !r.Sudo {
		t.Fatalf("audit of echo sudo-asks: %+v", r)
	}

	// stdin under the grant runs with no decision and is audited.
	f.ran(f.tool("exec", map[string]any{"server": "box", "command": "stdin-echo", "stdin": "auto in\n", "description": "flow"}), "auto in")
	if r := f.lastExec("stdin-echo"); r.Outcome != "allowed" || r.Approval != "auto" || r.Stdin != "auto in\n" {
		t.Fatalf("audit of stdin-echo under the grant: %+v", r)
	}
	f.until(func() bool { return f.tt.HasText("stdin 8 B") }, "the stdin size in the feed")

	// The sudo-exec opt-in: sudo-exec runs with no decision and is tagged.
	f.editAuto("box", "15 min", "Also auto-allow sudo-exec")
	f.until(func() bool {
		return f.tt.HasText("sudo-exec will run without asking: the AI has full root on this host")
	}, "the sudo warning")
	f.enable()
	if save := f.lastConfig("box", "save"); !strings.Contains(strings.Join(save.Changed, " "), "autoAllowSudo: false → true") {
		t.Fatalf("save record: %+v", save)
	}
	f.ran(f.aiSudo("box", "echo sudo-auto"), "echo sudo-auto")
	if r := f.lastExec("echo sudo-auto"); r.Outcome != "allowed" || r.Approval != "auto" || !r.Sudo {
		t.Fatalf("audit of echo sudo-auto: %+v", r)
	}
	f.until(func() bool { return count(f.tt.Texts(), "sudo") == 1 }, "the feed's sudo tag") // the tag, not the command's text

	// Stop on the card ends the grant.
	f.click("Hosts")
	f.click("Stop auto-allow box")
	f.chip("box", "")
	if off := f.lastConfig("box", "autoAllowOff"); off.Reason != "turned off" {
		t.Fatalf("autoAllowOff: %+v", off)
	}
	f.denyWaiting(f.aiExec("box", "echo after-stop"), "echo after-stop")

	// Stop auto-allow and lock ends a timed grant.
	f.editAuto("box", "15 min")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	f.stopAndLock()
	f.chip("box", "")
	if off := f.lastConfig("box", "autoAllowOff"); off.Reason != "locked" {
		t.Fatalf("autoAllowOff: %+v", off)
	}
	f.denyWaiting(f.aiExec("box", "echo after-lock"), "echo after-lock")
	if r := f.lastExec("echo after-lock"); r.Approval != "" {
		t.Fatalf("audit of echo after-lock: %+v", r)
	}

	// A forever grant is paused after Stop auto-allow and lock until Resume.
	f.editAuto("box", "Until turned off")
	f.fill("Type box to confirm", "box")
	f.enable()
	f.chip("box", "Auto ∞")
	if on := f.lastConfig("box", "autoAllowOn"); !on.Forever {
		t.Fatalf("autoAllowOn: %+v", on)
	}
	f.stopAndLock()
	f.until(func() bool { return f.tt.HasText("Auto-allow is paused on box.") }, "the paused banner")
	f.denyWaiting(f.aiExec("box", "echo paused-asks"), "echo paused-asks")
	f.pass()
	f.click("Resume")
	f.chip("box", "Auto ∞")
	f.ran(f.aiExec("box", "echo resumed"), "echo resumed")
	f.auto("echo resumed")
	f.click("Stop auto-allow box")
	f.chip("box", "")
}

// TestFlowAutoAllowRootHost: autoallow-mcp.spec.ts's root-host test. A
// root login is refused until "Allow on root hosts" is ticked. Pinning the
// new host opens a terminal, so it needs the terminal library.
func TestFlowAutoAllowRootHost(t *testing.T) {
	needLib(t)
	f := flow(t)
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.newHost("rootbox", "root", "rootpass", false)
	f.click("Open rootbox")
	f.trust("root")
	rootOn := func() int {
		n := 0
		for _, r := range f.records() {
			if r.Kind == "config" && r.Action == "autoAllowOn" && r.Server == "rootbox" {
				n++
			}
		}
		return n
	}

	// The hub listed rootbox as not visible to AI, so the app cannot foresee
	// the root refusal: the save stands and the app says so.
	f.editAuto("rootbox", "15 min", "Visible to AI")
	f.enable()
	f.until(func() bool {
		return f.tt.HasText("Saved, but auto-allow was not turned on: auto-allow refused: root login")
	}, "the saved-but alert")
	if save := f.lastConfig("rootbox", "save"); !strings.Contains(strings.Join(save.Changed, " "), "aiVisible: false → true") || rootOn() != 0 {
		t.Fatalf("save record %+v, autoAllowOn records %d", save, rootOn())
	}
	f.chip("rootbox", "")

	// Now visible, the refusal shows up front and Enable stays disabled.
	f.editAuto("rootbox", "15 min")
	f.until(func() bool { return f.tt.HasText("Auto-allow is not available here: root login") }, "the refusal")
	f.pass()
	f.click("Enable")
	f.tt.flush()
	if f.a.editor == nil || f.a.editor.confirm == nil {
		t.Fatal("Enable worked on a refused host")
	}
	f.click("Cancel")
	f.click("Close")
	f.until(func() bool { return f.a.editor == nil }, "the editor to close")
	if rootOn() != 0 {
		t.Fatal("a refused host was armed")
	}

	f.editAuto("rootbox", "15 min", "Allow on root hosts")
	f.until(func() bool { return f.tt.HasText("Allowing root") }, "the root warning")
	f.enable()
	f.chip("rootbox", "Auto 1[45]m")
	f.ran(f.aiExec("rootbox", "echo root-auto"), "echo root-auto")
	f.auto("echo root-auto")
	f.click("Stop auto-allow rootbox")
	f.chip("rootbox", "")
}
```

- [ ] **Step 3: Run them, with and without the terminal library**

These flows test the committed code of Tasks 3–7 end to end, so they pass on their first run; the RED evidence is those tasks' unit tests. To see a flow catch a regression, comment out the card's Stop button in `app/view/home.go` (Task 4, Step 4): `TestFlowAutoAllow` fails with `ui: no element shows "Stop auto-allow box"`. Restore it with `git checkout app/view/home.go`.

Run: `cd app && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=3 ./view -run 'TestFlowAutoAllow'`
Expected: PASS, three runs of the three flows.

Run: `cd app && go test -count=1 ./view -run 'TestFlowAutoAllow' -v`
Expected: `TestFlowAutoAllow` and `TestFlowAutoAllowMCP` PASS, `TestFlowAutoAllowRootHost` SKIP (`MYGO_GHOSTTY_VT unset`).

- [ ] **Step 4: The Gate, the rune check, then commit**

```bash
cd app
go vet ./... && test -z "$(gofmt -l .)"
go test -race -count=1 ./...
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=1 ./...
GOOS=linux go vet ./... && GOOS=linux go test -c -o /dev/null ./view
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```
```bash
perl -CSD -ne 'print "$ARGV:$.\n" if /[\x{200B}-\x{200F}\x{2028}-\x{202E}\x{2066}-\x{2069}\x{FEFF}]/; close ARGV if eof' app/view/flowauto_unix_test.go
```
```bash
git add app/view/flowauto_unix_test.go app/view/flowhosts_unix_test.go
git commit -m "app: flow tests of autoallow.spec and autoallow-mcp.spec through the real bridge

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 9: Flow tests: `settings`, `manuallock` and `softlock`

Ports `desktop/e2e/settings.spec.ts`, `manuallock.spec.ts` and `softlock.spec.ts` (renderer.md §6.6–6.8). Replaces slice 1's `TestFlowManualLockKeepsAutoAllow` and `TestFlowSoftLockAndStop`, which armed through `f.h.SetAutoAllow` (Decision 12).

**Files:**
- Create: `app/view/flowlock_unix_test.go`
- Modify: `app/view/flow_unix_test.go` (delete the two slice-1 flows)

**Interfaces:**
- Consumes: Task 8's `editAuto`, `enable`, `chip`, `newHost`; Task 7's `f.tool`, `f.aiExec`, `f.records`, `last`, `f.lastConfig`; Task 3's `choose`; Task 2's `model.LimitNotice`; slice 1's `f.idleLocked`, `f.ran`, `f.denied`, `f.auto`, `f.result`.
- Produces: `func next(texts []string, s string) string`; `func (f *flowT) armForever()`; `func (f *flowT) softLockBefore(cmd, reason string)`.

- [ ] **Step 1: Remove the flows these replace**

In `app/view/flow_unix_test.go`, delete `TestFlowManualLockKeepsAutoAllow` and `TestFlowSoftLockAndStop` (the last two functions of the file).

- [ ] **Step 2: Write the flows**

Create `app/view/flowlock_unix_test.go`:

```go
//go:build unix

package view

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/model"
)

// Ports of desktop/e2e/settings.spec.ts, manuallock.spec.ts and
// softlock.spec.ts, through the real MCP bridge.

// next is the text shown right after s, or "".
func next(texts []string, s string) string {
	if i := slices.Index(texts, s); i >= 0 && i+1 < len(texts) {
		return texts[i+1]
	}
	return ""
}

// armForever arms box forever through the editor.
func (f *flowT) armForever() {
	f.t.Helper()
	f.editAuto("box", "Until turned off")
	f.fill("Type box to confirm", "box")
	f.enable()
	f.chip("box", "Auto ∞")
}

// softLockBefore checks the audit log has a softLock record for box with
// reason (any when ""), older than cmd's run, which was auto-allowed.
func (f *flowT) softLockBefore(cmd, reason string) {
	f.t.Helper()
	recs := f.records()
	soft := last(recs, func(r auditRec) bool { return r.Kind == "config" && r.Action == "softLock" })
	ran := last(recs, func(r auditRec) bool { return r.Kind == "" && r.Command == cmd })
	if soft < 0 || ran < soft || !slices.Equal(recs[soft].Servers, []string{"box"}) || reason != "" && recs[soft].Reason != reason {
		f.t.Fatalf("softLock record %d, run %d in %+v", soft, ran, recs)
	}
	if r := recs[ran]; r.Outcome != "allowed" || r.Approval != "auto" {
		f.t.Fatalf("run of %q: %+v", cmd, r)
	}
}

// TestFlowSettings: settings.spec.ts. Its colour checks (the amber and red
// notices, Save's danger outline) are not ported: the Tester shows no colour.
func TestFlowSettings(t *testing.T) {
	f := flow(t)
	f.until(func() bool { return f.tt.HasText("Master password") }, "the unlock screen")
	if _, ok := f.tt.Find("Settings"); ok {
		t.Fatal("Settings shows while locked")
	}
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.armForever()

	f.click("Settings")
	f.until(func() bool { return f.tt.HasText("Keep running while locked") && f.tt.HasText("24 hours (default)") }, "Settings on 24 hours")
	f.tt.Key(0, ui.KeyEnter) // Cancel has the focus
	f.until(func() bool { return f.a.settings == nil }, "Enter to cancel")

	f.click("Settings")
	f.until(func() bool { return f.a.settings != nil }, "Settings")
	choose(t, f.tt, "24 hours (default)", "7 days")
	notice := model.LimitNotice("7d") + " On auto-allow now:"
	if !f.tt.HasText(notice) || next(f.tt.Texts(), notice) != "box" {
		t.Fatalf("7 days: %q", f.tt.Texts())
	}
	choose(t, f.tt, "7 days", "No limit")
	f.click("Save") // the notice just changed: Save waits
	f.tt.flush()
	notice = model.LimitNotice("none") + " On auto-allow now:"
	if f.a.settings == nil || !f.tt.HasText(notice) || next(f.tt.Texts(), notice) != "box" {
		t.Fatalf("No limit: %q", f.tt.Texts())
	}
	f.pass()
	f.click("Save")
	f.until(func() bool { return f.a.settings == nil }, "Save to close Settings")
	b, err := os.ReadFile(filepath.Join(filepath.Dir(f.audit), "servers.json"))
	var vault struct{ SoftLockLimit string }
	if err != nil || json.Unmarshal(b, &vault) != nil || vault.SoftLockLimit != "none" {
		t.Fatalf("vault softLockLimit %q, %v", vault.SoftLockLimit, err)
	}
	if r := f.lastConfig("", "softLockLimit"); r.Limit != "none" {
		t.Fatalf("softLockLimit record %+v", r)
	}

	// The value survives a lock, and the consent text follows it.
	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Master password") }, "the lock screen")
	if _, ok := f.tt.Find("Settings"); ok {
		t.Fatal("Settings shows while locked")
	}
	f.unlock()
	f.click("Settings")
	f.until(func() bool { return f.tt.HasText("No limit") }, "Settings on No limit")
	f.tt.Key(0, ui.KeyEscape)
	f.until(func() bool { return f.a.settings == nil }, "Esc to cancel")
	f.editAuto("box", "Off")
	f.until(func() bool { return f.a.editor == nil }, "the editor to close without a dialog")
	f.editAuto("box", "Until turned off")
	f.until(func() bool { return f.tt.HasText("Auto-allow AI commands on box") }, "the Auto-allow dialog")
	if !f.tt.HasText("with no time limit") || f.tt.HasText("24 hours") {
		t.Fatalf("consent text: %q", f.tt.Texts())
	}
	f.click("Cancel")
}

// TestFlowManualLock: manuallock.spec.ts. The Lock button's tooltip is
// model.LockTitle's (TestLockTitleAndAIButton): the Tester shows no tooltip.
func TestFlowManualLock(t *testing.T) {
	f := flow(t)
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.armForever()
	f.click("Lock")
	f.until(func() bool {
		return f.tt.HasText("Master password") && f.tt.HasText("AI auto-allow is still running on: box")
	}, "the soft-lock screen")
	f.ran(f.aiExec("box", "echo manual-1"), "echo manual-1")
	f.softLockBefore("echo manual-1", "manual")

	// The unlock screen's stop still ends it.
	f.click("Stop auto-allow and lock")
	f.until(func() bool { return !f.tt.HasText("AI auto-allow is still running on: box") }, "the auto-allow list to go")
	f.denied(f.aiExec("box", "echo manual-2"), "Vault is locked")
	if off := f.lastConfig("box", "autoAllowOff"); off.Reason != "locked" {
		t.Fatalf("autoAllowOff: %+v", off)
	}
}

// TestFlowSoftLock: softlock.spec.ts, with the idle lock at 3 s (the e2e's
// 5 s; arming through the UI makes a hub call at least every second here).
// The second host is not pinned: nothing the spec checks on it needs a pin,
// and pinning would open a terminal (TestFlowHostsCreateTrustEditForgetDelete
// covers that).
func TestFlowSoftLock(t *testing.T) {
	f := flow(t, "--idleLock=3s")
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.newHost("other", "u2", "x", true)
	f.armForever()

	// Idle with a grant: the app locks and the AI keeps running on box only.
	f.idleLocked("box")
	f.ran(f.aiExec("box", "echo soft-1"), "echo soft-1")
	f.softLockBefore("echo soft-1", "")
	f.denied(f.aiExec("other", "echo soft-other"), "Vault is locked")
	if last(f.records(), func(r auditRec) bool { return r.Command == "echo soft-other" }) >= 0 {
		t.Fatal("the refused run on other was audited")
	}
	list := f.result(f.tool("list-servers", map[string]any{})).Stdout
	if !strings.Contains(list, "- other (127.0.0.1) [locked: unlock the app]") || !strings.Contains(list, "- box (127.0.0.1)\n") || strings.Contains(list, "box (127.0.0.1) [locked") {
		t.Fatalf("list-servers: %q", list)
	}

	// Unlock keeps the grant and says what ran.
	f.unlock()
	f.until(func() bool { return f.tt.HasText("While the app was locked the AI ran 1 command on box") }, "the ran-while-locked banner")
	if f.tt.HasText("Auto-allow is paused") {
		t.Fatal("the grant paused across a soft lock")
	}
	f.click("Hosts")
	f.chip("box", "Auto ∞")
	f.ran(f.aiExec("box", "echo soft-2"), "echo soft-2")
	f.auto("echo soft-2")

	// The unlock screen stops auto-allow without the password.
	f.idleLocked("box")
	f.click("Stop auto-allow and lock")
	f.until(func() bool { return !f.tt.HasText("AI auto-allow is still running on: box") }, "the auto-allow list to go")
	f.denied(f.aiExec("box", "echo soft-3"), "Vault is locked")
	if off := f.lastConfig("box", "autoAllowOff"); off.Reason != "locked" {
		t.Fatalf("autoAllowOff: %+v", off)
	}
}
```

- [ ] **Step 3: Run them**

As in Task 8, these pass on their first run. To see one catch a regression, comment out the Settings gear in `app/view/home.go` (Task 6, Step 3): `TestFlowSettings` fails with `ui: no element shows "Settings"`. Restore it with `git checkout app/view/home.go`.

Run: `cd app && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=3 ./view -run 'TestFlow(Settings|ManualLock|SoftLock)'`
Expected: PASS, three runs of the three flows.

- [ ] **Step 4: The Gate, the rune check, then commit**

```bash
cd app
go vet ./... && test -z "$(gofmt -l .)"
go test -race -count=1 ./...
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -count=1 ./...
GOOS=linux go vet ./... && GOOS=linux go test -c -o /dev/null ./view
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```
```bash
perl -CSD -ne 'print "$ARGV:$.\n" if /[\x{200B}-\x{200F}\x{2028}-\x{202E}\x{2066}-\x{2069}\x{FEFF}]/; close ARGV if eof' app/view/flowlock_unix_test.go
```
```bash
git add app/view/flowlock_unix_test.go app/view/flow_unix_test.go
git commit -m "app: flow tests of settings.spec, manuallock.spec and softlock.spec

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-review

**Spec coverage** (slice 3 row, 3b half). Every assertion of the five e2e specs maps to a flow step, except those Decisions 11 and 12 list as not ported (colours, CSS classes, a tooltip):

| Item | Task |
|---|---|
| Editor: Auto-allow select (labels table), disabled for a new host, timed note, refusal hint | 3 |
| Opt-ins `Allow on root hosts`, `Also auto-allow sudo-exec`, disabled while Off | 3 |
| `saveConfirm`; `keptMode` removed; already-armed forever saves without asking | 3 |
| Auto-allow dialog: consent text, sudo sentences, red root and sudo warnings, root check (`servers.autoAllowCheck`), duration and `limitText`, typed-name confirm, Enable mouse-only behind 500 ms, Cancel/Enter/Esc back to the editor | 3 |
| Remote tunnels in the dialog | Decision 2 (slice 5) |
| `Saved, but auto-allow was not turned on:` alert after Enable | 3 (view), 8 (flow, root host) |
| Ticking chips, `auto N`, card Stop, tab dot | 4 |
| Paused banner (Hosts tab and AI column), Resume guarded, Stop | 5 |
| Ran-while-locked banner | 5 (view), 9 (`softlock` flow) |
| Settings: gear, select, amber/red notice with hosts, Save guarded, Enter/Esc cancel, closed on lock | 6 (view), 9 (flow) |
| MCP bridge test client | 7 |
| e2e `autoallow` | 8 `TestFlowAutoAllow` |
| e2e `autoallow-mcp` | 8 `TestFlowAutoAllowMCP`, `TestFlowAutoAllowRootHost` |
| e2e `settings` | 9 `TestFlowSettings` (colours not ported, Decision 11) |
| e2e `manuallock` | 9 `TestFlowManualLock` (tooltip: `TestLockTitleAndAIButton`) |
| e2e `softlock` | 9 `TestFlowSoftLock` (second host unpinned, Decision 12) |
| vitest `autoallow.test.ts` (rest), `AutoAllowDialog.test.ts`, `settings.test.ts`, `SettingsDialog.test.ts` (no Touch ID) | 2, 3, 6 |
| Ledger carry-overs: slice 1 M3 (chip tick), L50 (`stopAll` gen guard), 3a `keptMode` | 4, 4, 3 |

**Placeholder scan:** no TBD, TODO or "similar to"; every code step carries its code, read from the scratch clone where it compiled and passed.

**Type consistency:** `confirmState`, `settingsState`, `setAuto(names, mode, verb)`, `buildPaused(c, **model.ListChanges)`, `model.Confirm`, `model.AutoMode`, `model.Limit`, `hubapi.AutoCheck`, `hubtest.Bridge.Tool → <-chan ExecResult` are named the same in every task that defines or uses them.

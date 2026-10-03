# sshgate: Soft lock — the idle lock keeps auto-allow running — Design

Date: 2026-10-02
Status: Approved 2026-10-02; implemented (plan `plans/2026-10-02-soft-lock.md`). Revised 2026-10-02 after four independent reviews (security, code conformance, architecture, testing). The 24-hour ceiling was decided 2026-10-02.
Amended during implementation (2026-10-02): sudo is checked before the in-flight cap; the ceiling is also checked on each exec; a failed vault reload under soft lock hard-locks.
Amended after code review (2026-10-02): the soft-lock key lives in `autoKey`, out of `deps.MasterKey`; a lock generation replaces the `soft` flag; refusals under a locked UI are not audited; `ranLocked` counts only successful runs under soft lock; the sweep checks grants only when the vault file changed; a grant whose server no longer resolves is ended at the first resolve.
Amended 2026-10-03 by `2026-10-03-manual-soft-lock-design.md`: the Lock button soft-locks too, and the stop is `lock {stopAuto: true}`.
Depends on: `2026-09-28-auto-allow-design.md` (with its 2026-09-29 amendment), `2026-09-24-desktop-app-design.md`, `2026-09-30-slice4b-audit-viewer-design.md`. Everything there still holds unless this document changes it by name; where they disagree, this document wins.

This spec reverses four statements of the auto-allow spec:

- Scope, Out: "Grants that survive a lock: not built. Locking stops every grant."
- Lock and idle lock: "`lockIfIdle` does not fire while any timed grant has `until > now`" and "A forever grant does not hold off the idle lock."
- Forever: "paused after each unlock until Resume". After an idle lock a forever grant now keeps running and needs no Resume. Resume was the only recurring human confirmation of a forever grant; it remains after a manual lock and after a restart.
- MCP door: "`listServers` does not show auto-allow."

## Goal

Let the author leave the machine while Claude Code works on a host that is on auto-allow, without the idle lock stopping the work and without leaving the app's window usable by whoever sits down at it.

Why: a forever grant ends at the idle lock (15 minutes without UI input), so a long unattended task stops and waits for an unlock and a Resume. A timed grant avoids that today only by holding the whole vault open, UI included, for up to 4 hours.

The change: when the idle lock fires while a grant is live, the hub goes into a **soft lock**. The UI door is locked exactly as today. The master key stays in memory, and the MCP door keeps running auto-allowed execs on the hosts that have a grant. Nothing else gets through.

What this protects, stated exactly: the app's window. Terminals, files, tunnels, the vault editor and the approval buttons are behind the master password. It does not protect the granted hosts from a process running as the same user, which can talk to the MCP door as the AI does; that is true of any live grant today.

Exit gate: on a real host with a forever grant and the default 15-minute idle lock, the author starts a Claude Code task and leaves. After 20 minutes the app shows the unlock screen naming the host; `exec` calls still run with no click, each in `audit.jsonl` with `approval: "auto"` after a `softLock` record; an `exec` on a host with no grant fails with the locked error. The author unlocks: no "paused" banner, a line says how many commands ran while locked, and the next `exec` is still auto. On a second run, **Stop auto-allow and lock** on the unlock screen makes the next `exec` on the granted host fail.

## Scope

In:
- A third hub state, soft-locked, entered only by the idle lock while at least one grant is live.
- AI execs on granted hosts keep running under soft lock, with every existing grant check.
- The unlock screen shows which hosts are still on auto-allow and offers a password-free stop.
- After unlock, a count of what ran while locked.
- Timed grants no longer hold off the idle lock. This is a behaviour change for someone who relies on a timed grant to keep the app open: they now type the password on return.
- One audit action for entering soft lock.

Out:
- Soft lock from the Lock button: never. Manual Lock stays a hard lock that ends every grant; it is the kill switch.
- Approving anything while soft-locked: not possible. A request that needs a human fails at once.
- Keeping the key out of memory while soft-locked (for example caching resolved secrets in the grant): rejected. The secrets would be in memory anyway, and the vault's MAC could not be checked on reload.
- Soft lock in `hub --cli`: it has no grants, so it never soft-locks. Unchanged.
- Storing the master key in the OS keychain: not part of this.
- Two defects that exist today and that this design does not make worse; they go to ROADMAP: grant deadlines and the idle clock use monotonic time, which stops while the machine sleeps; `Unlock` holds `h.mu` during Argon2.

## Known limits

Unattended work still stops when:

- the renderer crashes: recovery calls `lock`, a hard lock that ends every grant;
- the hub restarts: it starts hard-locked, and a forever grant is paused until Resume;
- a timed grant's deadline passes;
- the soft lock reaches 24 hours;
- the AI needs a host with no grant, or a `sudo-exec` on a host without the sudo opt-in;
- the machine sleeps.

A dropped SSH connection is not a limit: the key is present, so the next auto run redials.

## Decisions made in chat (2026-10-02)

| Question | Decision |
|---|---|
| What stays open while idle | Only the MCP door, only for hosts with a live grant |
| Which grants | Timed and forever alike |
| Timed grants and the idle lock | No longer hold it off; the idle lock fires on time and soft-locks instead |
| Manual Lock | Hard lock, ends every grant, as today |
| Last grant ends during soft lock | The hub hard-locks (key zeroed) in the same step |
| `listServers` during soft lock | Granted hosts are reported not locked; all others locked |
| Requests needing approval during soft lock | Refused at once, with an error that says why |
| `servers.setAutoAllow` during soft lock | Refused, `off` included; `lock` is the one stop |
| Maximum soft-lock duration | 24 hours, a constant, then a hard lock; forever grants included |

## States

| State | Master key in memory | UI door | AI exec, host with a live grant | AI exec, other hosts |
|---|---|---|---|---|
| Unlocked | yes, `deps.MasterKey` | open | auto | waits for approval |
| Soft-locked | yes, `autoKey` | locked | auto | `ErrLocked` |
| Hard-locked | no | locked | `ErrLocked` | `ErrLocked` |

A **live grant** is an entry in `h.grants`. `sweepGrants` runs before `lockIfIdle` in every tick, so an entry past its deadline is gone before the idle rule looks.

Soft-locked means `autoKey != nil`; there is no separate flag. Invariant, checked under `h.mu` at every exit of a section that touches these fields: `autoKey != nil` implies `deps.MasterKey == nil` and `len(h.grants) > 0`.

Transitions:

- Unlocked → soft-locked: the idle lock fires and at least one grant is live.
- Unlocked → hard-locked: the idle lock fires with no live grant; manual Lock; renderer recovery; as today.
- Soft-locked → unlocked: `unlock` with the right master password. Grants stay armed; a forever grant is not paused.
- Soft-locked → hard-locked: the last grant ends for any reason, `lock` is called (the unlock screen's stop button, or renderer recovery), or the soft lock is 24 hours old. The key is zeroed.
- Hub restart: starts hard-locked, as today.

## Hub rules

### Data

`Hub` gains, guarded by `h.mu`:

- `autoKey []byte`: the master key while soft-locked. `enterSoftLocked` moves it out of `deps.MasterKey`; `Unlock` and `zeroKeyLocked` clear it.
- `softLockedAt time.Time`, the wall-clock time the soft lock began (`time.Now().Round(0)`, so time asleep counts). `Unlock` and `zeroKeyLocked` clear it.
- `ranLocked map[string]int`: successful auto runs per server finished under soft lock. Cleared when `unlock` reports it (`TakeRanLocked`).
- `sweptFile *config.File`: the vault file the grants were last checked against by `sweepGrants`. Nil on entering soft lock, so the first sweep checks every grant once, and nil when not soft-locked.
- `lockGen atomic.Uint64`: bumped, under `h.mu`, at every lock-state change (entering soft lock, `Unlock`, `CreateVault`, `zeroKeyLocked`).

`h.unlocked` (the atomic read by the audit sink) keeps its meaning, `deps.MasterKey != nil`. Soft lock moves the key out of `deps.MasterKey`, so it is false in both locked states and true only after `Unlock` and `CreateVault`.

### What "locked" means to each caller

**Strict rule**, `lockedLocked()`: the vault has a KDF and `deps.MasterKey == nil`. It has no soft-lock case: under soft lock the key is not in `deps.MasterKey`, it is in `autoKey`, which no strict reader looks at. So every reader of the key is strict by default, and a reader written later that forgets soft lock fails closed. These all refuse under soft lock with no change of their own:

- everything that goes through `checkLocked`, `Locked()`, `Deps.IsLocked` or `h.unlocked`: `term.open`, `files.list`/`mkdir`/`rename`/`plan`/`run`, `tunnels.start`, `audit.read`;
- `writeKeyLocked` (`hosts.go`): `servers.save`, `servers.delete`, `servers.forgetHostKey`, `tunnels.save`/`delete`, `import.scan`/`apply`;
- arming or extending a grant (`armLocked`, through `autoEligibleLocked`'s `checkLocked`), so every mode but `off` of `servers.setAutoAllow` is refused;
- `serversForUI` (`uidoor.go`), through `Deps.IsLocked`: under soft lock `servers` answers as it would under a hard lock. Every server with a stored secret is `locked: true`.

Only two functions read the key's bytes from `autoKey`:

- `resolveAutoLocked`, which resolves on a copy of `deps` that carries the key. Callers: `resolveForAuto` (Exec's first resolve), `autoStart`, `grantStaleLocked` (the sweep).
- `vaultKeyLocked`, the key the vault file is checked against: `deps.MasterKey`, else `autoKey`. One caller, `reloadLocked`, so the file's MAC is still verified under soft lock.

Other code only tests `autoKey != nil` as the soft-lock state, without touching the key: `lockIfIdle` (the ceiling), `sweepGrants`, `hardenLocked`, `lockStatus`, `aiLockedLocked` and `autoExec`; `zeroKeyLocked` clears it. Two more tests of it are grant-related and refuse or hide something from a locked UI:

- `autoAllowOff` (`SetAutoAllow` with `off`) returns `ErrLocked` before anything else. `off` would otherwise end the in-memory grant even when the vault write is refused, and could not clear the vault flag; `lock` is the one stop.
- `autoStateLocked` hides live grants from a locked UI: `autoAllow` in `servers` comes from the vault flag alone, as under a hard lock.

`decide` gains a check: an `allowed` or `sendToTab` decision returns `ErrLocked` while `lockedLocked()` (soft or hard). `denied` and `denyAll` still work; they only move to the safer state.

**Grant rule**, the one exception: the server is locked when both keys are absent, or when `autoKey` is set and `h.grants[name]` is nil. The rule also treats a soft lock at or past the 24-hour ceiling as locked, so an exec that arrives after the ceiling but before the next idle tick is refused. Exactly two callers use it: the first resolve in `Exec` and `autoStart`. They get it from a separate function (`checkGrantLocked`, over `aiLockedLocked`), not from a flag on `checkLocked`, so no other caller can pick it up by accident. `ServersForMCP` uses `aiLockedLocked` too. `autoEligibleLocked` and the post-approval resolve in `Exec` stay on the strict rule.

### Running an exec under soft lock

`Hub.Exec` keeps its order. Under soft lock:

1. First resolve (`resolveForAuto`), grant rule (the grant-ending part below is not specific to soft lock: it applies whenever this resolve fails for a server that has a grant): vault exists → visible → locked → pinned. A host with no grant returns `ErrLocked` here, unaudited, exactly as under a hard lock. Hidden and nonexistent servers still return their byte-identical error first. If the resolve fails for a server that has a grant, in any lock state (hidden, removed, no pin, or the resolve itself fails), the grant is ended with reason "server changed", as `autoStart` would end it; otherwise this call would fail before `autoStart` looked and the grant would hold the key. `ErrLocked` is the exception: a hard lock has ended every grant itself, and under soft lock it means the ceiling has passed, which the next tick ends. The error the AI sees is the same either way.
2. Sanitize the command, validate the description.
3. `autoStart` applies every existing check: deadline, resolve, `snap`, the root and sudo opt-ins, at most `maxAutoInflight` (2) in flight. A failed check ends the grant as today. It now also reports why it returned no run: `autoSkip`, one of `skipNone`, `skipBusy`, `skipSudo`. The sudo opt-in is checked before the in-flight cap, so a `sudo-exec` without the opt-in gets `ErrNeedsApproval` even when 2 runs are in flight.
4. If `autoStart` returns no run, `Exec` takes `h.mu` and looks at `lockedLocked()`. When it is true, `Exec` returns at once and never reaches `broker.Submit`. It writes no audit record: a client that retries on `ErrAutoBusy` would otherwise append one per retry for as long as the soft lock lasts. The error depends on why:

| Why no auto run | Error to the AI |
|---|---|
| `maxAutoInflight` runs already in flight on this host | `ErrAutoBusy`: "auto-allow is running 2 commands on this server; retry when one finishes" (the number comes from `maxAutoInflight`) |
| `sudo-exec` and the host has no sudo opt-in | `ErrNeedsApproval`: "this command needs approval and the app is locked; unlock it in the app" |
| The grant just ended (the hub is now hard-locked) | `ErrLocked` |

   The check is on `lockedLocked()` after `autoStart`, not on "was soft-locked before", so it also covers the run that ends the last grant itself. `ErrAutoBusy` exists because Claude Code issues parallel calls: a third call must not be told the vault is locked while two others succeed.

5. A request that reached the broker before the soft lock stays pending until it expires or the human unlocks. If something approves it meanwhile, `decide` refuses; and the post-approval resolve uses the strict rule, so it would return `ErrLocked` even on a granted host.

Auto runs still never count as UI activity and never increment `h.running`. What `autoExec` does when a run finishes is decided in one `h.mu` section, from the state it reads there: a run that succeeded while soft-locked is counted in `ranLocked[server]`; while unlocked, `autoAllow.ran` is sent; a failed run under soft lock, or any run finishing under a hard lock (a manual lock cancelled it), is neither counted nor sent. The audit log has every run.

### Ending a grant

One helper, `hardenLocked`, runs with `h.mu` held after every removal from `h.grants` (`endGrantLocked`): when `autoKey` is set and no grant is left, it zeroes the key and queues the `locked` notification with reason `grantsEnded`, from a goroutine of its own because the caller holds `h.mu`. It is called from the removal itself, not from a list of callers, so a new way to end a grant cannot forget it. The note carries the lock generation of its change, and the UI door drops it when the hub has moved on (see Protocol 9). An auto run in flight is cancelled when its grant ends, as today.

Under soft lock `sweepGrants` reloads the vault. If that reload fails it ends every grant with reason "server changed", which hard-locks: the vault can no longer be checked, so it fails closed. Otherwise it ends, with the same reason, any grant whose server no longer passes the visibility and pin checks or whose `snap` no longer matches (`grantStaleLocked`). Without this, a server hidden or removed by an outside edit of the vault file would fail the first resolve before `autoStart` ever looked at its grant, and the grant would hold the key indefinitely. That per-grant check runs only when the vault file differs from the one the grants were last checked against (`sweptFile`, nil on entering soft lock, so the first sweep checks every grant once), not on every tick: it decrypts and reads key files under `h.mu`, and `autoStart` re-resolves on every run anyway. `sweptFile` is not the sweep's own reload: `status`, MCP calls and `SetAutoAllow` reload too.

### Idle lock

`lockIfIdle` on each tick. Pending requests and `h.running` hold it off as today, for a soft lock as for a hard one.

| Hub state | Idle period passed | Live grants | Action |
|---|---|---|---|
| Unlocked | no | any | nothing |
| Unlocked | yes | none | hard lock, as today |
| Unlocked | yes | one or more | enter soft lock; grants stay |
| Soft-locked, under 24 h | n/a | one or more | nothing (no repeated audit record or notification) |
| Soft-locked, 24 h or more | n/a | one or more | hard lock; every grant ends with reason "soft lock limit" |

The ceiling is `maxSoftLock = 24 * time.Hour`, a constant with no flag and no setting. It bounds how long a key and a grant can outlive the human's last input; before this change that bound was the idle period. Like the idle lock, it waits while a request is pending or an approved run is in flight; an auto run in flight does not delay it and is cancelled. A forever grant ended this way keeps its vault flag, so it is paused after the next unlock and needs Resume, as after a manual lock.

`timedGrantLocked` goes away: no grant delays the idle lock. With `--idleLock` disabled (`idle <= 0`) there is no idle lock and so no soft lock. "Soft-locked with no grants" cannot occur (the invariant).

### Unlock and Lock

- `Unlock(pw)` under soft lock runs the full check as today (Argon2, verifier, MAC), replaces the key, clears `autoKey` and sets `h.unlocked`; the UI door's `unlock` handler then returns and clears `ranLocked` (`TakeRanLocked`). A wrong password changes nothing.
- `Lock()` under soft lock zeroes the key and ends every grant with reason "locked", as it does when unlocked. The `lock` request is already answered while locked; no new method.

### MCP door

`ServersForMCP` reports a server `locked: false` when a key is present and either the hub is not soft-locked or the server has a live grant and the soft lock is under the 24-hour ceiling; at or past the ceiling every server is reported locked (`aiLockedLocked`). During a soft lock this tells the AI which hosts have a grant; it learns the same by calling `exec`. Outside soft lock `listServers` still does not show auto-allow. The MCP door still has no method that reads or changes a grant.

### Notifications to a locked UI

The rule "nothing with content goes to a locked UI" holds under soft lock:

- `audit.appended`: not sent (`h.unlocked` is false). This includes the `softLock` record itself.
- `autoAllow.ran`: sent only while `h.unlocked` is true, and sent under the UI door's `sendMu`, the mutex that already orders `audit.appended` against `locked`. `autoExec` also sends it only when the run finished with `deps.MasterKey` set. A run that succeeded under soft lock is counted in `ranLocked` instead, for the `unlock` reply.
- `autoAllow.off {server, reason}`: sent. It carries a server name only.
- `locked`: see Protocol 9.

## Audit

One new `kind: "config"` action: `softLock`, with `servers`, the names of the hosts whose grants are live when the soft lock begins. The audit read filter on `server` matches a record whose `server` equals the query or whose `servers` contains it (`internal/broker/audit.go`), so the Audit tab's host filter finds the `softLock` record by host; the renderer's `matches` and `rowView` mirror it (`desktop/src/renderer/audit.ts`).

Leaving soft lock writes nothing new: a grant ending is already an `autoAllowOff` record with its reason (`expired`, `server changed`, `locked`).

Auto runs under soft lock are audited like any other auto run. The refusals in "Running an exec" step 4 are not audited, like the `ErrLocked` of a host with no grant.

## Protocol 9

`ProtocolVersion` becomes 9.

- `status` gains `autoHosts: [name, …]`, present only while soft-locked. `locked` and `autoHosts` are read in one `h.mu` section so they cannot disagree. `locked` is true in that state, so `screenFor` is unchanged.
- The `locked` notification's params stay `{reason}`; there is no `soft` flag. A soft lock begins with `reason: "idle"`; when it hardens, `locked` is sent again with `"grantsEnded"`, `"softLockLimit"`, or `"manual"`. The renderer tells a soft lock from a hard one by `status.autoHosts`, which the `locked` notification already makes it refresh. `h.lockGen` is bumped at every lock-state change and the lock sink carries the generation of its change; the UI door sends `locked` only when that generation is still the hub's current one, under `sendMu`, so a note for a state the hub has left (the sink may run from a goroutine of its own) is dropped instead of landing after a later note or after an unlock.
- The `unlock` reply gains `ranWhileLocked: [{server, count}]` when it ends a soft lock or follows one that hardened, omitted when empty.
- `decide` can now fail with the locked error.

No new methods.

## Desktop

- `Unlock.tsx`: when the list of auto hosts is non-empty, the card shows "AI auto-allow is still running on: `<names>`" (names joined plainly, as `AutoAllowPaused` does: they are the author's own labels) and a button **Stop auto-allow and lock**. The button's own handler (`stopAndLock` in `App.tsx`) calls `hub.lock()`, and on failure shows `Could not stop auto-allow: <message>` on the unlock screen. It needs no password and no 500 ms delay: it only moves to the safer state, like `tunnels.stop` and `files.cancel`, which also work while locked. The sentence "AI requests are refused until you unlock." is shown only when the list is empty.
- That list is its own piece of state in `App`, derived from `status.autoHosts` (not from a `soft` flag on `locked`, and not from `servers`, which is empty while locked). `locked` already triggers a `status` refresh (`status` is not UI activity); `autoAllow.off` removes a name. A `status` reply that was in flight when `autoAllow.off` arrived cannot re-add that host (`lockHostsFrom` in `autoallow.ts` leaves out the hosts dropped since the call began). The renderer makes no other hub call from a notification or a timer.
- `dropOnLock` still runs on every `locked`, soft included; the server list it edits is reloaded after unlock, so chips and the paused banner come from the hub's fresh answer. The Playwright spec proves end to end that a forever host is not shown paused after an unlock from soft lock.
- The mapping from a `locked` reason to the unlock screen's text becomes a pure function: `idle`, `grantsEnded`, and `softLockLimit` (`lockKind` in `shell.ts`) show "Locked after inactivity."; anything else is manual.
- After an unlock whose reply has `ranWhileLocked`, the app's banner slot (shown on every tab) has a dismissible line: "While the app was locked the AI ran N commands on `<names>`. See the Audit tab." The Auto-allowed feed does not list those runs.
- `AutoAllowDialog` (`AutoAllowDialog.tsx`): the forever text reads "Stays set after restart. When the app locks from inactivity, the AI keeps running on this host for up to 24 hours; lock by hand to stop it. After a manual lock or a restart it waits for you to click Resume." A timed grant's reads "Ends at <time>, when you stop it, or when you lock the vault. When the app locks from inactivity, the AI keeps running on this host until then; lock by hand to stop it." The paused banner (`AutoAllowPaused.tsx`) reads "Auto-allow is paused on <names>. Once resumed, the AI keeps running there while the app is locked from inactivity.", so a forever grant armed before this change is resumed with the new meaning in view.
- Types to widen: `Status` and the `locked` params in `shared/protocol.ts`, the lock reason in `App.tsx` and `Unlock.tsx`.

## Security rules

- Under soft lock the master key is in memory while the app shows the unlock screen. For a timed grant this is not worse than today, when the key is in memory with the whole UI open until the deadline. For a forever grant it is new, and bounded by the 24-hour ceiling.
- No grant delays the idle lock. A live grant only changes what the lock does. (Pending requests and approved runs still delay it, as today.)
- Under soft lock the UI door can do nothing it cannot do under a hard lock, and one thing less: it cannot allow a request. It cannot arm, extend, resume, or turn off a grant, or edit a server.
- The AI cannot cause or extend a soft lock: only a grant armed by the human before the lock keeps a host running, and every per-run check (`snap`, opt-ins, deadline, 2 in flight) still applies. It can tell that a soft lock is on, from `listServers` or from an error.
- The key never outlives the last live grant.
- Under soft lock the key is held in a field (`autoKey`) whose bytes only the auto path and the vault MAC check read, not in `deps.MasterKey`, so a reader that forgets soft lock sees a locked vault and fails closed.
- Stopping needs no password. Unlocking always does.
- Honest limit, restated: a forever grant on an unattended machine lets the AI, and any process running as the same user, run as that account for up to 24 hours after the app locks, and without limit while the author keeps using the app. The dialog says so.

## Testing

Test seams: `softLockForTest` (`softlock_test.go`), next to `unlockForTest`, which puts a hub in soft lock without waiting for the idle rule (needed to combine a pending request with a soft lock, which the idle rule itself never produces); and `uiDoorMethods`, a hook in `uidoor.go` that a test sets to receive the UI door's registered method list.

Go, `internal/hub` (`softlock_test.go`, `softlock_door_test.go`):

Entering and leaving
- `TestIdleWithGrantSoftLocks`: idle with a live grant → soft-locked: `Locked()` true, `status.autoHosts` lists the host, one `softLock` record naming it, one `locked {reason: "idle"}`; a second tick adds nothing. `TestUnlockFromSoftLockKeepsGrants`: after `Unlock` a forever grant is armed, not paused.
- `TestIdleWithoutGrantHardLocks`, `TestExpiredGrantAndIdleInOneTickHardLocks` (no `softLock` record, exactly one `locked`), `TestPendingRequestHoldsOffSoftLock`.
- `TestLastGrantEndingHardLocks`: the last grant ending zeroes the key in the same step and sends `locked {reason: "grantsEnded"}`; a later tick sends no second `locked`. Before that, with two grants, one ending by deadline in `sweepGrants` leaves the hub soft-locked, sends nothing, and shrinks `autoHosts`. `TestSoftLockSweepEndsGrantOfHiddenServer`: the same through the sweep when the server is hidden in the file. `TestSoftLockSweepHardLocksWhenReloadFails`.
- `TestSoftLockCeiling` (24 hours back: key zeroed, grants ended with "soft lock limit", `locked {reason: "softLockLimit"}`, forever host paused after `Unlock`; one second short of 24 hours, nothing), `TestSoftLockCeilingCancelsRunInFlight`, `TestExecPastCeilingBeforeTickIsLocked`. No test covers the ceiling waiting for a pending request or an approved run.
- `TestLockUnderSoftLockIsHard`. `TestUnlockFromSoftLockKeepsGrants`: a wrong password under soft lock changes nothing; the right one leaves the forever grant armed and not paused, and the next exec is auto. `TestUIDoorUnlockReportsRanWhileLocked`: two runs under soft lock, `status` over the door has `autoHosts`, the `unlock` reply's `ranWhileLocked` has the count, and `status` afterwards has no `autoHosts`.
- The key's place: `TestSoftLockKeepsKeyOutOfDeps`, `TestSoftLockReloadStillChecksMAC`, `TestExecUnderSoftLockDecryptsWithAutoKey`.
- Lock generation: `TestStaleLockNoteIsDropped`; `TestUIDoorSoftLockNotifications` covers the notifications a locked UI gets.
- The sweep: `TestSoftLockSweepDoesNotReresolveUnchangedFile`, `TestSoftLockSweepSeesFileReloadedByOthers`.

Exec
- `TestExecUnderSoftLockRunsAuto` (`approval: "auto"`; no `autoAllow.ran` is sent; `TakeRanLocked` returns the count once and is empty the second time), `TestExecUnderSoftLockOtherHostIsLocked` (`ErrLocked`, nothing audited, `broker.Pending()` empty; hidden and nonexistent servers return the same bytes as when unlocked; `ServersForMCP` reports the granted host not locked and the others locked).
- `TestExecUnderSoftLockNeedsApprovalFailsAtOnce`: a third concurrent run gets `ErrAutoBusy` and `sudoExec` without `AutoAllowSudo` gets `ErrNeedsApproval`, each at once, with the grant kept and no audit record.
- `TestExecThatEndsLastGrantUnderSoftLock`: `ErrLocked` at once, `broker.Pending()` empty.
- `TestExecEndsGrantOfHiddenServer`, `TestExecEndsGrantWhenResolveFails`: the first resolve ends the grant with "server changed".
- `TestApprovalUnderSoftLockDoesNotRun`: with `softLockForTest`, a `sudoExec` on the granted host, pending before the lock; `decide allowed` returns the locked error, and with the `decide` check bypassed the approved run still returns `ErrLocked` from the post-approval resolve. This is the test that fails if the grant rule leaks into that resolve; it uses the granted host. `TestDenyStillWorksUnderSoftLock`.
- What counts as ran while locked: `TestManualLockDuringRunIsNotCountedAsRanLocked`, `TestFailedRunUnderSoftLockIsNotCounted`.
- `TestSoftLockRefusesWritesAndGrantChanges`, `TestSoftLockServersMatchHardLock`.

UI door
- `TestUIDoorSoftLockAnswersLikeHardLock`: conformance. The test takes the door's method list from `uiDoorMethods` and fails on any method with no entry in its tables. Two lists: methods that must answer with a locked error under soft lock (`doorMustRefuse`: `servers.save`, `servers.delete`, `servers.forgetHostKey`, `servers.setAutoAllow`, `servers.autoAllowCheck`, `import.scan`, `import.apply`, `audit.read`, `term.open`, `files.list`, `files.mkdir`, `files.rename`, `files.plan`, `files.run`, `tunnels.save`, `tunnels.start`, `tunnels.delete`, `decide` allowing), and methods that must answer with any error under both locks (`doorMustError`: `vault.create`, which refuses because a vault exists). Every other method's reply must equal its reply under a hard lock for the same vault, `servers` included; `status` must differ only by `autoHosts`.
- `TestUIDoorSetAutoAllowOffRefusedUnderSoftLock`; `TestUIDoorNotificationsUnderSoftLockAreNotActivity` (the read loop survives `tunnels.stop`, `files.cancel` and `term.write` without `user: true`, none of which counts as activity, and the lock state is unchanged; the ids do not exist, so it does not show a cancel taking effect); `TestUIDoorSoftLockNotifications` (`audit.appended`, the `softLock` record included, and `autoAllow.ran` are not sent; `autoAllow.off` is); `TestUIDoorHelloAndLockedNotification` (`uidoor_test.go`) checks that `hello` reports 9.

Races, with `-race`
- `TestSoftLockInvariantUnderRace`, in the style of `TestSetAutoAllowConcurrentWithSave`: concurrent `Exec`, `sweepGrants`, `lockIfIdle`, `Unlock`, and `Lock`, asserting the invariant under `h.mu` throughout.

Existing tests that changed
- `TestTimedGrantHoldsIdleLock` was replaced by the entering tests; `TestAutoAllowEndsOnLock`'s idle half became the soft-lock test; `TestAutoRunsDoNotHoldIdleLock` became "auto runs do not delay the soft lock, and the run completes"; `uidoor_test.go` has the protocol number.

Desktop:

- Vitest (`desktop/test`): `Unlock.test.ts` renders `Unlock` with `renderToStaticMarkup`, with and without auto hosts; `shell.test.ts` covers `lockKind`; `autoallow.test.ts` covers `dropLockHost`, `lockHostsFrom` (a host that `autoAllow.off` dropped is not re-added by a stale `status` reply) and `ranLockedText`; `audit.test.ts` covers the `softLock` row and `matches` finding it by `servers`.
- Playwright (`desktop/e2e/softlock.spec.ts`): launched like `idle.spec.ts` with `SSHGATE_IDLE_LOCK` of about 5 s (3 s can fire while the grant is being armed), using the `Mcp` helper extracted from `autoallow-mcp.spec.ts`. Steps: add a second visible host with no grant; arm a forever grant on the first; wait for the unlock screen naming it; exec on it through the real MCP bridge and check the audit log shows `softLock` before the auto records; exec on the second host fails locked; `list-servers` shows the second host locked and the first not; unlock, see no paused banner (so a forever host is not paused after a soft lock, end to end) and the "ran while locked" line, and the next exec is auto; soft-lock again, press **Stop auto-allow and lock**, and the next exec fails, with an `autoAllowOff` record with reason `locked`.

## ROADMAP, PRODUCT, README, CLAUDE.md changes

- README "Locking": the idle lock fires on time; with a host on auto-allow the app locks but the AI keeps running on that host until the grant ends or you stop it. Drop "A timed auto-allow grant holds this off", and say plainly that a timed grant no longer keeps the app open. Update the `list-servers` row and the "only while the vault is unlocked" line. "Until turned off" needs Resume after a manual lock or a restart, not after an idle lock. Add the known limits.
- PRODUCT: the auto-allow sentences ("with a Resume after each unlock") and the fixed-security list (the stop button on the unlock screen needs no password).
- CLAUDE.md: the Auto-allow paragraph (idle sentences, `setAutoAllow` under soft lock), the `Hub.Exec` order and its two resolves, the `h.unlocked` definition, `listServers`, `decide`, protocol 9, and the "refuses every AI exec while the vault is locked" sentence.
- ROADMAP: record soft lock as a decision that changes the auto-allow rule, and the two deferred defects (monotonic deadlines across sleep; `Unlock` holding `h.mu` during Argon2).
- `2026-09-28-auto-allow-design.md`: add a line under Status pointing here.

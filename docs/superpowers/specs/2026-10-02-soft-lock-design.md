# sshgate: Soft lock — the idle lock keeps auto-allow running — Design

Date: 2026-10-02
Status: Draft, revised 2026-10-02 after four independent reviews (security, code conformance, architecture, testing). The 24-hour ceiling was decided 2026-10-02. Approved 2026-10-02; plan `plans/2026-10-02-soft-lock.md`.
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
| Unlocked | yes | open | auto | waits for approval |
| Soft-locked | yes | locked | auto | `ErrLocked` |
| Hard-locked | no | locked | `ErrLocked` | `ErrLocked` |

A **live grant** is an entry in `h.grants`. `sweepGrants` runs before `lockIfIdle` in every tick, so an entry past its deadline is gone before the idle rule looks.

Invariant, checked under `h.mu` at every exit of a section that touches these fields: `softLocked` implies the key is present and `len(h.grants) > 0`.

Transitions:

- Unlocked → soft-locked: the idle lock fires and at least one grant is live.
- Unlocked → hard-locked: the idle lock fires with no live grant; manual Lock; renderer recovery; as today.
- Soft-locked → unlocked: `unlock` with the right master password. Grants stay armed; a forever grant is not paused.
- Soft-locked → hard-locked: the last grant ends for any reason, `lock` is called (the unlock screen's stop button, or renderer recovery), or the soft lock is 24 hours old. The key is zeroed.
- Hub restart: starts hard-locked, as today.

## Hub rules

### Data

`Hub` gains, guarded by `h.mu`:

- `softLocked bool` and `softLockedAt time.Time`, the wall-clock time the soft lock began (`time.Now().Round(0)`, so time asleep counts). `zeroKeyLocked` clears both.
- `ranLocked map[string]int`: auto runs per server since the soft lock began. Cleared when `unlock` reports it.

`h.unlocked` (the atomic read by the audit sink) changes meaning from "`MasterKey != nil`" to "the UI may be served": key present and not soft-locked. It is set false on entering soft lock and true only by `Unlock` and `CreateVault`.

### What "locked" means to each caller

**Strict rule**, `lockedLocked()`: the vault has a KDF and either the key is absent or `softLocked` is set. `checkLocked` keeps using it. Every UI-door path that goes through it, through `Locked()`, or through `h.unlocked` refuses under soft lock with no further change: `term.open`, `files.list`/`mkdir`/`rename`/`plan`/`run`, `tunnels.start`, `audit.read`.

Four places read `deps.MasterKey` directly and must apply the strict rule themselves:

- `writeKeyLocked` (`hosts.go`): `servers.save`, `servers.delete`, `servers.forgetHostKey`, `tunnels.save`/`delete`, `import.scan`/`apply`.
- `armLocked` (`autoallow.go`): arming or extending a grant.
- `SetAutoAllow`, every mode: refused with `ErrLocked` before anything else. This includes `off`, which today skips the eligibility check and ends the in-memory grant even when the vault write is refused, and `forever` on an already-armed host, which today returns before any key check.
- `serversForUI` (`uidoor.go`), through `Deps.IsLocked`: under soft lock `servers` answers as it would under a hard lock. Every server with a stored secret is `locked: true`, and `autoAllow` is derived from the vault flag alone, with no live grant shown.

`decide` gains a check: an `allowed` or `sendToTab` decision returns `ErrLocked` while `lockedLocked()` (soft or hard). `denied` and `denyAll` still work; they only move to the safer state.

**Grant rule**, the one exception: the server is locked when the key is absent, or when `softLocked` is set and `h.grants[name]` is nil. Exactly two callers use it: the first resolve in `Exec` and `autoStart`. They get it from a separate function (`checkGrantLocked`), not from a flag on `checkLocked`, so no other caller can pick it up by accident. `autoEligibleLocked` and the post-approval resolve in `Exec` stay on the strict rule.

### Running an exec under soft lock

`Hub.Exec` keeps its order. Under soft lock:

1. First resolve, grant rule: vault exists → visible → locked → pinned. A host with no grant returns `ErrLocked` here, unaudited, exactly as under a hard lock. Hidden and nonexistent servers still return their byte-identical error first.
2. Sanitize the command, validate the description.
3. `autoStart` applies every existing check: deadline, resolve, `snap`, the root and sudo opt-ins, at most 2 in flight. A failed check ends the grant as today. It now also reports why it returned no run.
4. If `autoStart` returns no run, `Exec` takes `h.mu` and looks at `lockedLocked()`. When it is true, `Exec` returns at once and never reaches `broker.Submit`, writing one audit record with `outcome: "error"` and the reason. The error depends on why:

| Why no auto run | Error to the AI |
|---|---|
| 2 runs already in flight on this host | `ErrAutoBusy`: "auto-allow is running 2 commands on this server; retry when one finishes" |
| `sudo-exec` and the host has no sudo opt-in | `ErrNeedsApproval`: "this command needs approval and the app is locked; unlock it in the app" |
| The grant just ended (the hub is now hard-locked) | `ErrLocked` |

   The check is on `lockedLocked()` after `autoStart`, not on "was soft-locked before", so it also covers the run that ends the last grant itself. `ErrAutoBusy` exists because Claude Code issues parallel calls: a third call must not be told the vault is locked while two others succeed.

5. A request that reached the broker before the soft lock stays pending until it expires or the human unlocks. If something approves it meanwhile, `decide` refuses; and the post-approval resolve uses the strict rule, so it would return `ErrLocked` even on a granted host.

Auto runs still never count as UI activity and never increment `h.running`. Each one under soft lock increments `ranLocked[server]`.

### Ending a grant

One helper runs, with `h.mu` held, after every removal from `h.grants`: when `softLocked` is set and no grant is left, it zeroes the key and queues the `locked` notification with reason `grantsEnded`. It is called from the removal itself, not from a list of callers, so a new way to end a grant cannot forget it. An auto run in flight is cancelled when its grant ends, as today.

Under soft lock `sweepGrants` also ends, with reason "server changed", any grant whose server no longer passes the strict visibility and pin checks or whose `snap` no longer matches. Without this, a server hidden or removed by an outside edit of the vault file would fail the first resolve before `autoStart` ever looked at its grant, and the grant would hold the key indefinitely.

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

- `Unlock(pw)` under soft lock runs the full check as today (Argon2, verifier, MAC), replaces the key, clears `softLocked`, sets `h.unlocked`, and returns and clears `ranLocked`. A wrong password changes nothing.
- `Lock()` under soft lock zeroes the key and ends every grant with reason "locked", as it does when unlocked. The `lock` request is already answered while locked; no new method.

### MCP door

`ServersForMCP` reports a server `locked: false` when the key is present and either the hub is not soft-locked or the server has a live grant. During a soft lock this tells the AI which hosts have a grant; it learns the same by calling `exec`. Outside soft lock `listServers` still does not show auto-allow. The MCP door still has no method that reads or changes a grant.

### Notifications to a locked UI

The rule "nothing with content goes to a locked UI" holds under soft lock:

- `audit.appended`: not sent (`h.unlocked` is false). This includes the `softLock` record itself.
- `autoAllow.ran`: sent only while `h.unlocked` is true, and sent under the UI door's `sendMu`, the mutex that already orders `audit.appended` against `locked`. Today it is sent outside that mutex with no check.
- `autoAllow.off {server, reason}`: sent. It carries a server name only.
- `locked`: see Protocol 9.

## Audit

One new `kind: "config"` action: `softLock`, with `servers`, the names of the hosts whose grants are live when the soft lock begins.

Leaving soft lock writes nothing new: a grant ending is already an `autoAllowOff` record with its reason (`expired`, `server changed`, `locked`).

Auto runs under soft lock are audited like any other auto run. The refusals in "Running an exec" step 4 are audited as exec records with `outcome: "error"`.

## Protocol 9

`ProtocolVersion` becomes 9.

- `status` gains `autoHosts: [name, …]`, present only while soft-locked. `locked` and `autoHosts` are read in one `h.mu` section so they cannot disagree. `locked` is true in that state, so `screenFor` is unchanged.
- The `locked` notification gains `soft: true` when a soft lock begins (`reason: "idle"`). When a soft lock hardens it is sent again with `reason: "grantsEnded"`, `"softLockLimit"`, or `"manual"` and no `soft`.
- The `unlock` reply gains `ranWhileLocked: [{server, count}]` when it ends a soft lock or follows one that hardened, omitted when empty.
- `decide` can now fail with the locked error.

No new methods.

## Desktop

- `Unlock.tsx`: when the list of auto hosts is non-empty, the card shows "AI auto-allow is still running on: `<names>`" (names joined plainly, as `AutoAllowPaused` does: they are the author's own labels) and a button **Stop auto-allow and lock**. The button calls `hub.lock()`. It needs no password and no 500 ms delay: it only moves to the safer state, like `tunnels.stop` and `files.cancel`, which also work while locked. The sentence "AI requests are refused until you unlock." is shown only when the list is empty.
- That list is its own piece of state in `App`, set from `status.autoHosts`. `locked` already triggers a `status` refresh (`status` is not UI activity); `autoAllow.off` removes a name. It is not derived from `servers`, which is empty while locked. The renderer makes no other hub call from a notification or a timer.
- `dropOnLock` still runs on every `locked`, soft included; the server list it edits is reloaded after unlock, so chips and the paused banner come from the hub's fresh answer. A test pins that a forever host is not shown paused after an unlock from soft lock.
- The mapping from a `locked` reason to the unlock screen's text becomes a pure function: `idle`, `grantsEnded`, and `softLockLimit` show "Locked after inactivity."; anything else is manual.
- After an unlock whose reply has `ranWhileLocked`, the app's banner slot (shown on every tab) has a dismissible line: "While the app was locked the AI ran N commands on `<names>`. See the Audit tab." The Auto-allowed feed does not list those runs.
- `AutoAllowDialog`: the text for every mode gains "When the app locks from inactivity, the AI keeps running on this host. Lock by hand to stop it. After 24 hours locked, it stops by itself." The forever text says Resume is needed after a manual lock or a restart. The paused banner's text gains the same sentence, so a forever grant armed before this change is resumed with the new meaning in view.
- Types to widen: `Status` and the `locked` params in `shared/protocol.ts`, the lock reason in `App.tsx` and `Unlock.tsx`.

## Security rules

- Under soft lock the master key is in memory while the app shows the unlock screen. For a timed grant this is not worse than today, when the key is in memory with the whole UI open until the deadline. For a forever grant it is new, and bounded by the 24-hour ceiling.
- No grant delays the idle lock. A live grant only changes what the lock does. (Pending requests and approved runs still delay it, as today.)
- Under soft lock the UI door can do nothing it cannot do under a hard lock, and one thing less: it cannot allow a request. It cannot arm, extend, resume, or turn off a grant, or edit a server.
- The AI cannot cause or extend a soft lock: only a grant armed by the human before the lock keeps a host running, and every per-run check (`snap`, opt-ins, deadline, 2 in flight) still applies. It can tell that a soft lock is on, from `listServers` or from an error.
- The key never outlives the last live grant.
- Stopping needs no password. Unlocking always does.
- Honest limit, restated: a forever grant on an unattended machine lets the AI, and any process running as the same user, run as that account for up to 24 hours after the app locks, and without limit while the author keeps using the app. The dialog says so.

## Testing

Test seams to add: `softLockForTest`, next to `unlockForTest`, which puts a hub in soft lock without waiting for the idle rule (needed to combine a pending request with a soft lock, which the idle rule itself never produces); and a method on `rpc.Server` that lists its registered methods.

Go, `internal/hub`:

Entering and leaving
- Idle with a live timed grant → soft-locked: key present, `Locked()` true, `status.autoHosts` lists the host, one `softLock` record naming it, one `locked {reason: "idle", soft: true}`; a second tick adds nothing.
- Idle with a live forever grant → the same; after `Unlock` the grant is armed, not paused, and the next exec is auto.
- Idle with no grant → hard lock, unchanged. A grant past its deadline and the idle period in the same tick → hard lock, no `softLock` record, exactly one `locked`.
- A pending request with a live grant holds off the soft lock. `idle <= 0` never soft-locks.
- `autoHosts` is absent when unlocked and when hard-locked, and shrinks when one of two grants ends; the hub stays soft-locked.
- The last grant ending zeroes the key in the same step and sends `locked {reason: "grantsEnded"}` with no `soft`: by deadline in `sweepGrants`, by "server changed" in `autoStart`, and by the new sweep check when the server is hidden or removed in the file. A later tick sends no second `locked`.
- The ceiling: with `softLockedAt` set 24 hours back by hand, the next tick zeroes the key, ends a forever and a timed grant with "soft lock limit", cancels a run in flight, and sends `locked {reason: "softLockLimit"}`; after `Unlock` the forever host is paused. One second short of 24 hours, nothing happens. A pending request holds it off.
- `lock` under soft lock: key zeroed, grants ended with "locked", `locked {reason: "manual"}`.
- `Unlock` with a wrong password under soft lock: still soft-locked, the grant still runs. With the right one: `ranWhileLocked` has the count, and a second unlock does not repeat it.

Exec
- Under soft lock a plain exec on the granted host runs with `approval: "auto"`. A run in flight across the transition completes, is not cancelled, and sends no `autoAllow.ran`.
- A visible host with no grant: `ErrLocked`, nothing audited, `broker.Pending()` empty.
- A third concurrent run: `ErrAutoBusy` at once, audited, the grant kept. `sudoExec` without `AutoAllowSudo`: `ErrNeedsApproval` at once, audited, the grant kept.
- The exec that ends the last grant itself returns `ErrLocked` at once and `broker.Pending()` is empty.
- With `softLockForTest`: a `sudoExec` on the granted host, pending before the lock. `decide allowed` returns the locked error. With the `decide` check bypassed in the test, the approved run still returns `ErrLocked` from the post-approval resolve. This is the test that fails if the grant rule leaks into that resolve; it must use the granted host.
- Hidden and nonexistent servers return the same bytes under soft lock as when unlocked.
- `ServersForMCP` under soft lock: granted host `locked: false`, others `locked: true`.

UI door
- Conformance: the test takes the server's method list and fails on any method with no entry in its table. Each entry has valid params aimed at the granted host and the expected result under soft lock. Refusals (`ErrLocked` or the method's own locked error, not `-32602`): `servers.save` with and without `autoAllow`, `servers.delete`, `servers.forgetHostKey`, `servers.setAutoAllow` for `off`, a timed mode, and `forever` on the already-armed host, `servers.autoAllowCheck`, `tunnels.save`, `tunnels.delete`, `tunnels.start`, `term.open`, `files.list`, `files.mkdir`, `files.rename`, `files.plan`, `files.run`, `audit.read`, `import.scan`, `import.apply`, `vault.create`, `decide` allowing. Answered: `hello`, `status`, `unlock`, `lock`, `pending`, `denyAll`, `decide` denying, and `servers`, whose reply must equal the hard-lock reply for the same vault.
- Notifications under soft lock: `tunnels.stop` and `files.cancel` work; `term.write` on an open terminal works and, without `user: true`, is not activity.
- `audit.appended` (the `softLock` record included) and `autoAllow.ran` are not sent under soft lock; `autoAllow.off` is.
- `hello` reports 9.

Races, with `-race`
- A stress test in the style of `TestSetAutoAllowConcurrentWithSave`: concurrent `Exec`, `sweepGrants`, `lockIfIdle`, `Unlock`, and `Lock`, asserting the invariant under `h.mu` throughout. The existing seams (`lastActivity` and `until` set by hand, direct `lockIfIdle` and `sweepGrants(now)`, `blockExec`, `deriveKey`) are enough; no clock injection.

Existing tests that change
- `TestTimedGrantHoldsIdleLock`: replaced by the entering tests.
- `TestAutoAllowEndsOnLock`: its idle half asserted that grants are gone; it becomes the soft-lock test. Its manual half stays.
- `TestAutoRunsDoNotHoldIdleLock`: it waited for the run to be cancelled by the idle lock; it becomes "auto runs do not delay the soft lock, and the run completes".
- `TestAutoExecCancelledOnLock` (manual lock): unchanged.
- `uidoor_test.go`: the protocol number, and `waitLockedNote`, which must carry `soft`.

Desktop:

- Vitest: `Unlock` rendered with `renderToStaticMarkup`, with and without auto hosts; the reason-to-text function; `autoAllow.off` drops a name from the list; `locked {soft: true}` followed by an unlock and a server reload leaves a forever host not paused; the `ranWhileLocked` line.
- Playwright: a new spec, launched like `idle.spec.ts` with `SSHGATE_IDLE_LOCK` of about 5 s (3 s can fire while the grant is being armed), using the `Mcp` helper extracted from `autoallow-mcp.spec.ts`. That file shares one launch with no idle lock and stays as it is. Steps: add a second visible host with no grant; arm a grant on the first; wait for the unlock screen naming it; exec on it through the real MCP bridge and check the audit log shows `softLock` before the auto records; exec on the second host fails locked; unlock, see no paused banner and the "ran while locked" line, and the next exec is auto; soft-lock again, press **Stop auto-allow and lock**, and the next exec fails.

## ROADMAP, PRODUCT, README, CLAUDE.md changes

- README "Locking": the idle lock fires on time; with a host on auto-allow the app locks but the AI keeps running on that host until the grant ends or you stop it. Drop "A timed auto-allow grant holds this off", and say plainly that a timed grant no longer keeps the app open. Update the `list-servers` row and the "only while the vault is unlocked" line. "Until turned off" needs Resume after a manual lock or a restart, not after an idle lock. Add the known limits.
- PRODUCT: the auto-allow sentences ("with a Resume after each unlock") and the fixed-security list (the stop button on the unlock screen needs no password).
- CLAUDE.md: the Auto-allow paragraph (idle sentences, `setAutoAllow` under soft lock), the `Hub.Exec` order and its two resolves, the `h.unlocked` definition, `listServers`, `decide`, protocol 9, and the "refuses every AI exec while the vault is locked" sentence.
- ROADMAP: record soft lock as a decision that changes the auto-allow rule, and the two deferred defects (monotonic deadlines across sleep; `Unlock` holding `h.mu` during Argon2).
- `2026-09-28-auto-allow-design.md`: add a line under Status pointing here.

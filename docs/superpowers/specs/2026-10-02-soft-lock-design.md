# sshgate: Soft lock — the idle lock keeps auto-allow running — Design

Date: 2026-10-02
Status: Draft, awaiting review.
Depends on: `2026-09-28-auto-allow-design.md` (with its 2026-09-29 amendment), `2026-09-24-desktop-app-design.md`, `2026-09-30-slice4b-audit-viewer-design.md`. Everything there still holds unless this document changes it by name; where they disagree, this document wins.

This spec reverses three statements of the auto-allow spec:

- Scope, Out: "Grants that survive a lock: not built. Locking stops every grant."
- Lock and idle lock: "`lockIfIdle` does not fire while any timed grant has `until > now`" and "A forever grant does not hold off the idle lock."
- MCP door: "`listServers` does not show auto-allow."

## Goal

Let the author leave the machine while Claude Code works on a host that is on auto-allow, without the idle lock stopping the work and without leaving the app usable by whoever sits down at it.

Why: a forever grant ends at the idle lock (15 minutes without UI input), so a long unattended task stops and waits for an unlock and a Resume. A timed grant avoids that today only by holding the whole vault open, UI included, for up to 4 hours.

The change: when the idle lock fires while a grant is live, the hub goes into a **soft lock**. The UI door is locked exactly as today. The master key stays in memory, and the MCP door keeps running auto-allowed execs on the hosts that have a grant. Nothing else gets through.

Exit gate: on a real host with a forever grant and the default 15-minute idle lock, the author starts a Claude Code task and leaves. After 20 minutes the app shows the unlock screen naming the host, `exec` calls still run with no click, each is in `audit.jsonl` with `approval: "auto"` after a `softLock` record, an `exec` on a host with no grant fails with the locked error, and **Stop auto-allow and lock** on the unlock screen makes the next `exec` on the granted host fail too.

## Scope

In:
- A third hub state, soft-locked, entered only by the idle lock while at least one grant is live.
- AI execs on granted hosts keep running under soft lock, with every existing grant check.
- The unlock screen shows which hosts are still on auto-allow and offers a password-free stop.
- Timed grants no longer hold off the idle lock.
- Audit records for entering and leaving soft lock.

Out:
- Soft lock from the Lock button: never. Manual Lock stays a hard lock that ends every grant; it is the kill switch.
- Approving anything while soft-locked: not possible. A request that needs a human fails at once.
- Keeping the key out of memory while soft-locked (for example caching resolved secrets in the grant): rejected. The secrets would be in memory anyway, and the vault's MAC could not be checked on reload.
- Soft lock in `hub --cli`: it has no grants, so it never soft-locks. Unchanged.
- Storing the master key in the OS keychain: not part of this.

## Decisions made in chat (2026-10-02)

| Question | Decision |
|---|---|
| What stays open while idle | Only the MCP door, only for hosts with a live grant |
| Which grants | Timed and forever alike |
| Timed grants and the idle lock | No longer hold it off; the idle lock always fires on time and soft-locks instead |
| Manual Lock | Hard lock, ends every grant, as today |
| Last grant ends during soft lock | The hub hard-locks (key zeroed) in the same step |
| `listServers` during soft lock | Granted hosts are reported not locked; all others locked |
| Requests needing approval during soft lock | Refused at once with the locked error |

## States

| State | Master key in memory | UI door | AI exec, host with a live grant | AI exec, other hosts |
|---|---|---|---|---|
| Unlocked | yes | open | auto | waits for approval |
| Soft-locked | yes | locked | auto | `ErrLocked` |
| Hard-locked | no | locked | `ErrLocked` | `ErrLocked` |

Transitions:

- Unlocked → soft-locked: the idle lock fires and at least one grant is live.
- Unlocked → hard-locked: the idle lock fires with no live grant; manual Lock; renderer recovery; as today.
- Soft-locked → unlocked: `unlock` with the right master password. Grants stay armed. A forever grant is not paused and needs no Resume, because it was never ended.
- Soft-locked → hard-locked: the last grant ends (deadline, or "server changed"), or `lock` is called (the unlock screen's stop button). The key is zeroed.
- Hub restart: starts hard-locked, as today. A forever grant is then paused until Resume, as today.

## Hub rules

### Data

`Hub` gains `softLocked bool`, guarded by `h.mu`. It is true only while `deps.MasterKey != nil`. `zeroKeyLocked` clears it.

`h.unlocked` (the atomic read by the audit sink) changes meaning from "`MasterKey != nil`" to "the UI may be served": key present and not soft-locked. It is set false on entering soft lock and true again only by `Unlock` and `CreateVault`.

### What "locked" means to each caller

`lockedLocked()` returns true when the vault has a KDF and either the key is absent or `softLocked` is set. Every UI-door path that goes through it, or through `Locked()` or `h.unlocked`, therefore refuses under soft lock with no further change: `term.open`, `files.*` (except the cancels), `tunnels.start`/`save`/`delete`, `audit.read`, `import.*`. Requests that do not check the lock today (`servers`, `pending`, `decide`, `denyAll`) answer under soft lock exactly as they do under a hard lock.

Three places read `deps.MasterKey` directly and must check `softLocked` themselves, returning `ErrLocked`:

- `writeKeyLocked` (`hosts.go`): `servers.save`, `servers.delete`, `servers.forgetHostKey`, `tunnels.save`/`delete`.
- `armLocked` (`autoallow.go`): arming or extending a grant.
- `autoEligibleLocked`: `servers.setAutoAllow` and `servers.autoAllowCheck`.

So under soft lock nothing can arm, extend, resume, or edit a grant or a server. A grant can only end.

The AI path is the one exception. `resolveForAI` and `autoStart` use a per-server rule: the server is locked when the key is absent, or when `softLocked` is set and `h.grants[name]` is nil. `autoEligibleLocked` shares `checkLocked` with them today; it must not get this exception.

### Running an exec under soft lock

`Hub.Exec` keeps its order. Under soft lock:

1. `resolveForAI`: vault exists → visible → locked by the per-server rule above → pinned. A host with no grant returns `ErrLocked` here, unaudited, exactly as under a hard lock. Hidden and nonexistent servers still return their byte-identical error first.
2. Sanitize the command, validate the description.
3. `autoStart` applies every existing check: deadline, resolve, `snap`, the root and sudo opt-ins, at most 2 in flight. A failed check ends the grant as today.
4. If `autoStart` returns no run (sudo without the opt-in, a third in-flight run, or the grant just ended) and the hub is soft-locked, `Exec` returns `ErrLocked` at once and writes one audit record with `outcome: "error"` and that reason. It never reaches `broker.Submit`: nobody could decide, and the request would only block for 5 minutes.

A request already pending when the idle tick runs still holds off the idle lock, soft or hard, as today. A request that slips into the broker in the window between step 4's check and a soft lock stays pending until it expires or the human unlocks and decides. The post-approval re-check uses the strict `lockedLocked()`, so an approved run never starts under soft lock.

Auto runs still never count as UI activity and never increment `h.running`.

### Idle lock

`lockIfIdle` on each tick, with pending requests and `h.running` checked as today:

| Hub state | Idle period passed | Live grants | Action |
|---|---|---|---|
| Unlocked | no | any | nothing |
| Unlocked | yes | none | hard lock, as today |
| Unlocked | yes | one or more | enter soft lock; grants stay |
| Soft-locked | n/a | one or more | nothing (no repeated audit record or notification) |
| Soft-locked | n/a | none | hard lock |

`timedGrantLocked` goes away: no grant delays the idle lock. With `--idleLock` disabled (`idle <= 0`) there is no idle lock and so no soft lock.

The soft-locked-with-no-grants row is a backstop. The rule is stronger: whichever `h.mu` section ends the last grant while soft-locked also zeroes the key. That is `sweepGrants` (deadline) and `autoStart` (deadline, "server changed"). The key never outlives the last grant.

### Unlock and Lock

- `Unlock(pw)` under soft lock runs the full check as today (Argon2, verifier, MAC), replaces the key, clears `softLocked`, sets `h.unlocked`. A wrong password changes nothing.
- `Lock()` under soft lock zeroes the key and ends every grant with reason "locked", as it does when unlocked. The `lock` request is already answered while locked; no new method.

### MCP door

`ServersForMCP` reports a server `locked: false` when the key is present and either the hub is not soft-locked or the server has a live grant. This tells the AI which hosts have a grant, but only during a soft lock, and the AI learns the same by calling `exec`. Outside soft lock `listServers` still does not show auto-allow. The MCP door still has no method that reads or changes a grant.

### Notifications to a locked UI

The rule "nothing with content goes to a locked UI" holds under soft lock:

- `audit.appended`: not sent (`h.unlocked` is false).
- `autoAllow.ran`: not sent while soft-locked. It carries the command. Runs made under soft lock are in the audit log and do not appear in the Auto-allowed feed after unlock.
- `autoAllow.off {server, reason}`: sent. It carries a server name only, and the unlock screen uses it to drop the host from its list.
- `locked`: see Protocol 9.

## Audit

Two new `kind: "config"` actions:

- `softLock`, with `servers`: the names of the hosts whose grants are live when the soft lock begins.
- `softLockEnd`, with `reason`: `unlock`, `manual` (the `lock` request), or `grantsEnded`.

Auto runs under soft lock are audited exactly like any other auto run. The refusal in "Running an exec" step 4 is audited as an exec record with `outcome: "error"`.

## Protocol 9

`ProtocolVersion` becomes 9.

- `status` gains `autoHosts: [name, …]`, present only while soft-locked. `locked` is true in that state, so `screenFor` is unchanged.
- The `locked` notification gains `soft: true` when a soft lock begins (`reason: "idle"`). When a soft lock hardens it is sent again with `reason: "grantsEnded"` or `"manual"` and no `soft`.

No new methods.

## Desktop

- `Unlock.tsx`: when `status.autoHosts` is non-empty, the card shows "AI auto-allow is still running on: `<names>`" (through `displayText`) and a button **Stop auto-allow and lock**. The button calls `hub.lock()`. It needs no password and no 500 ms delay: it only moves to the safer state, like `tunnels.stop` and `files.cancel`, which also work while locked.
- The list is kept current without polling: `locked` already triggers a `status` refresh (`status` is not UI activity), and `autoAllow.off` removes a name in renderer memory. The renderer makes no other hub call from a notification or a timer.
- `App.tsx` maps `locked` reasons: `idle` and `grantsEnded` both show "Locked after inactivity."; anything else is manual.
- The existing sentence "AI requests are refused until you unlock." is shown only when `autoHosts` is empty.
- `AutoAllowDialog`: the duration text for every mode gains "When the app locks from inactivity, the AI keeps running on this host. Lock by hand to stop it." The forever text drops the mention of Resume after an idle lock; Resume is still needed after a manual lock or a restart.
- The "Auto-allow is paused" banner and Resume are unchanged; they now appear only after a hard lock or a hub restart.

## Security rules

- Under soft lock the master key is in memory while the app shows the unlock screen. This is not worse than today for a timed grant, which keeps the key in memory with the whole UI open until its deadline. For a forever grant it is new: the key stays until the human stops the grant, locks, or quits.
- The idle lock is never delayed by anything. A live grant only changes what the lock does.
- Under soft lock the UI door can do nothing it cannot do under a hard lock. In particular it cannot arm, extend, or resume a grant, edit a server, or decide a request.
- The AI cannot cause, extend, or detect its way into a soft lock: only a grant armed by the human, before the lock, keeps a host running, and every per-run check (`snap`, opt-ins, deadline, 2 in flight) still applies.
- The key never outlives the last live grant.
- Stopping needs no password. Unlocking always does.
- Honest limit, restated: a forever grant on an unattended machine lets the AI run as that account for as long as the app is open. The dialog says so.

## Testing

Go, `internal/hub`:

- Idle with a live timed grant → soft-locked: key present, `Locked()` true, `status.autoHosts` lists the host, one `softLock` audit record, one `locked {reason: "idle", soft: true}`; a second tick adds nothing.
- Idle with a live forever grant → the same; after `Unlock` the grant is still armed and not paused.
- Idle with no grant → hard lock, unchanged.
- Under soft lock: plain exec on the granted host runs with `approval: "auto"`; exec on a visible host with no grant returns `ErrLocked` and the broker never sees it; `sudoExec` on a granted host without `AutoAllowSudo`, and a third concurrent run, return `ErrLocked` at once with an audited `error` record.
- Hidden and nonexistent servers return the same bytes under soft lock as when unlocked.
- UI-door conformance: every registered request is called once under a hard lock and once under soft lock, and the two replies must be equal (`status` aside, which differs only by `autoHosts`). `servers.save`, `servers.delete`, `servers.forgetHostKey`, `servers.setAutoAllow`, `servers.autoAllowCheck`, `tunnels.save`, `term.open`, `files.plan`, `audit.read`, and `import.scan` must be refusals in both.
- A request approved with `decide` under soft lock does not run: the post-approval re-check returns `ErrLocked`.
- The last grant expiring under soft lock zeroes the key in the same step, writes `softLockEnd {reason: "grantsEnded"}`, and sends `locked {reason: "grantsEnded"}`. The same when `autoStart` ends it for "server changed".
- `lock` under soft lock: key zeroed, grants ended with "locked", `softLockEnd {reason: "manual"}`.
- `Unlock` with a wrong password under soft lock: still soft-locked, grant still runs.
- `ServersForMCP` under soft lock: granted host `locked: false`, others `locked: true`.
- `autoAllow.ran` and `audit.appended` are not sent under soft lock; `autoAllow.off` is.
- `TestTimedGrantHoldsIdleLock` is replaced by the soft-lock test above; `TestAutoRunsDoNotHoldIdleLock`, `TestAutoAllowEndsOnLock`, and `TestAutoExecCancelledOnLock` (manual lock) stay as they are.

Desktop:

- Vitest: `screenFor` with `autoHosts`; `Unlock` renders the host list and the stop button only when `autoHosts` is non-empty; `autoAllow.off` drops a name.
- Playwright, `autoallow-mcp.spec.ts` with a short `SSHGATE_IDLE_LOCK`: arm a grant, wait for the unlock screen, run execs through the real MCP bridge, check each against the audit log, press **Stop auto-allow and lock**, check the next exec fails.

## ROADMAP, PRODUCT, README, CLAUDE.md changes

- README "Locking": the idle lock always fires; with a host on auto-allow the app locks but the AI keeps running on that host until the grant ends or you stop it. Drop "A timed auto-allow grant holds this off". Update the `list-servers` row and the "only while the vault is unlocked" line. "Until turned off" needs Resume after a manual lock or a restart, not after an idle lock.
- PRODUCT: the auto-allow sentences ("with a Resume after each unlock") and the fixed-security list (the stop button on the unlock screen needs no password).
- CLAUDE.md: the Auto-allow paragraph (idle sentences), the `Hub.Exec` order, the `h.unlocked` definition, `listServers`, protocol 9, and the "refuses every AI exec while the vault is locked" sentence.
- ROADMAP: record soft lock as a decision that changes the auto-allow rule.
- `2026-09-28-auto-allow-design.md`: add a line under Status pointing here.

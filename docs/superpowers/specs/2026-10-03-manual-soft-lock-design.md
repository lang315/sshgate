# sshgate: The Lock button keeps auto-allow running — Design

Date: 2026-10-03
Status: Approved in chat 2026-10-03 (option A of three).
Depends on: `2026-10-02-soft-lock-design.md`. Everything there still holds unless this document changes it by name; where they disagree, this document wins.

This spec reverses these statements of the soft-lock spec:

- Scope, Out: "Soft lock from the Lock button: never. Manual Lock stays a hard lock that ends every grant; it is the kill switch."
- Decisions, Manual Lock: "Hard lock, ends every grant, as today".
- Decisions, `servers.setAutoAllow` during soft lock: "`lock` is the one stop". The stop is now `lock {stopAuto: true}`.
- Scope, In: "A third hub state, soft-locked, entered only by the idle lock". The Lock button enters it too.
- Forever: "Resume ... remains after a manual lock". It does not: a manual Lock with a live grant soft-locks and needs no Resume; Resume remains after **Stop auto-allow and lock** and after a restart.

## Goal

Let the author press Lock before walking away while Claude Code works on a host that is on auto-allow, without the lock stopping that work.

Why: today only the idle lock soft-locks. An author who locks by hand, which is the safer habit, ends every grant and finds the task stopped on return. The only way to leave the work running is to leave the app open for 15 minutes and let the idle lock fire.

The change: the Lock button does what the idle lock does. With at least one live grant it soft-locks; with none it hard-locks, as today. A grant past its deadline that the sweep has not reached is not live: `LockKeepAuto` runs the grant sweep first (such a grant ends with reason "expired", as on the idle tick), then decides in one `h.mu` section from what is left, so the `softLock` record and `status.autoHosts` list live grants only. Stopping auto-allow becomes an explicit choice, `lock {stopAuto: true}`, made by the unlock screen's existing **Stop auto-allow and lock** button.

What this protects is unchanged from the soft-lock spec: the app's window. The key stays in memory for up to 24 hours while a grant is live, as it already does after an idle lock.

Exit gate: with a forever grant on a real host, the author presses Lock and leaves a Claude Code task running. The unlock screen names the host; `exec` calls keep running with no click, each in `audit.jsonl` with `approval: "auto"` after a `softLock` record with `reason: "manual"`. **Stop auto-allow and lock** then makes the next `exec` fail with the locked error.

## Scope

In:
- `lock` takes `{stopAuto?: boolean}`. Without it, `lock` soft-locks when a grant is live.
- The unlock screen's stop button and renderer-crash recovery send `stopAuto: true`.
- The `softLock` audit record says who locked: `reason: "manual"` or `"idle"`.
- UI-door protocol 10.

Out:
- A choice at lock time (a menu, or a second Lock button): rejected in chat (options B and C). One click locks, as before.
- `hub --cli`: it has no lock command and no grants. Unchanged.
- Stopping auto-allow while unlocked: unchanged. "Stop all auto-allow" in the AI column and Stop on a host card still work.

## Behaviour

| State when `lock` arrives | `lock {}` | `lock {stopAuto: true}` |
|---|---|---|
| Unlocked, at least one live grant | Soft lock, `locked {reason: "manual"}` | Hard lock, every grant ended with reason "locked", `locked {reason: "manual"}` |
| Unlocked, no grant | Hard lock, `locked {reason: "manual"}`, as today | Same |
| Soft-locked | Nothing | Hard lock, every grant ended with reason "locked", `locked {reason: "manual"}` |
| Hard-locked | Nothing | Nothing |

Entering soft lock from the Lock button is the same step the idle lock takes (`enterSoftLocked`): the key moves from `deps.MasterKey` to `autoKey`, the lock generation is bumped, one `softLock` audit record is written, and the 24-hour ceiling counts from that moment. Every rule of the soft-lock spec then applies unchanged, whichever lock entered it: the grant rule for AI execs, refusals at once and unaudited for anything else, `servers.setAutoAllow` refused, hardening when the last grant ends, the ceiling, the vault-file sweep, `ranWhileLocked` on unlock, and no Resume needed for a forever grant after unlock.

`lock` with no params, or with `{}`, is `stopAuto: false`. Params are decoded strictly (`strictParams`): any key but `stopAuto` is invalid params (-32602). `stopAuto` must be a boolean: `null`, a string or any other type is invalid params. Fail closed: on invalid params the hub hard-locks first (`Hub.Lock`, every grant ended with reason "locked"), then returns -32602, so a malformed request from the Lock button's path never leaves the UI unlocked.

`lock` still counts as UI activity, as every UI-door request but `status` does; it has no effect on a lock that already happened.

## Audit

The `softLock` config record gains `reason`: `"manual"` when the Lock button entered it, `"idle"` when the idle lock did. `servers` is unchanged. Ending grants with `stopAuto` writes the same `autoAllowOff` records, reason "locked", that `lock` writes today.

## Desktop

- `hub.lock(stopAuto?: boolean)` in `transport.ts` sends `{stopAuto: true}` only when asked.
- The Lock button calls `hub.lock()`. While a host is on auto-allow its `title` is "Auto-allow keeps running while locked".
- `stopAndLock` (the unlock screen's **Stop auto-allow and lock**) calls `hub.lock(true)`. Its text, layout and error line are unchanged.
- `recoverRenderer` (`window.ts`) calls `lock` with `{stopAuto: true}`: a renderer crash still ends every grant, as the soft-lock spec's Known limits say.
- The unlock screen lists `status.autoHosts` with the stop button for any soft lock. `lockKind(reason, previous)` keeps the stored reason for `grantsEnded` and `softLockLimit` (they only arrive during a soft lock that an idle or manual lock began), so a manual soft lock that later hardens still reads as a manual lock; with none stored it reads as idle.
- The Lock button's handler does not call `refresh()` itself: the `locked` notification that every state-changing lock sends does, and a second concurrent refresh could re-add a host that `autoAllow.off` had removed from the unlock screen.
- Consent dialog copy: Lock, by hand or from inactivity, does not stop a grant; **Stop auto-allow and lock**, Stop on the host card or Stop all auto-allow does. A forever grant waits for Resume after a restart, the 24-hour limit or **Stop auto-allow and lock**. The paused banner says the AI keeps running "while the app is locked".

## Protocol

`ProtocolVersion` goes from 9 to 10. A renderer built for 9 would call `lock` for its stop button and soft-lock instead of stopping; the version check in `hello` refuses that pairing.

## Testing

Go (`internal/hub`):
- Lock with a live grant soft-locks: `autoKey` set, `deps.MasterKey` nil, the grant still live, `locked` sent with reason `manual`, a `softLock` record with `reason: "manual"` and the host in `servers`.
- Lock with no grant hard-locks, as today.
- `stopAuto` from unlocked with a grant, and from soft lock: hard lock, no grant left, an `autoAllowOff` record with reason "locked" per grant.
- `lock {}` while soft-locked changes nothing: the grant is still live and no second `softLock` record is written.
- An auto exec runs after a manual soft lock, and a host with no grant is refused with the locked error.
- `lock` with an unknown key, a non-boolean `stopAuto` or `stopAuto: null` is -32602 and leaves the hub hard-locked.
- A forever grant plus an expired, unswept one: soft lock, and the `softLock` record's `servers` lists only the live host; an expired grant alone hard-locks and is ended with reason "expired".
- The idle path's `softLock` record carries `reason: "idle"`.

E2e (`desktop/e2e/manuallock.spec.ts`, its own launch with the default idle lock, so the idle lock cannot fire first; `softlock.spec.ts` runs a 5 s idle lock): with a forever grant on `box`, press Lock; the unlock screen names `box`; an `exec` through the real MCP bridge succeeds and is audited `approval: "auto"` after a `softLock` record with `reason: "manual"`; **Stop auto-allow and lock** then makes the next `exec` fail. The existing idle-lock tests stay in `softlock.spec.ts`.

Unit (`desktop/test`): the Lock button's title with and without hosts on auto-allow.

## Documentation

README, PRODUCT.md and CLAUDE.md change every statement that the Lock button ends auto-allow or that only the idle lock soft-locks. The soft-lock spec gets a Status line pointing here.

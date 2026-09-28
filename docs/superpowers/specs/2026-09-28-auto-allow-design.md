# sshgate: Per-host Auto-allow for AI exec — Design

Date: 2026-09-28
Status: Approved 2026-09-28.
Depends on: `2026-09-24-desktop-app-design.md` (slice 1), `2026-09-25-desktop-slice2a-design.md` (slice 2a), `2026-09-27-slice3b-port-forwarding-design.md` (slice 3b). Everything there still holds unless this document changes it by name.

This spec reverses a standing decision. ROADMAP: "Every AI command is approved by a human. No auto-approval rules of any kind until a spec argues otherwise." Slice 1's decision table: "No auto-approval rules". This document is that argument, and it replaces both with the rule in "ROADMAP, PRODUCT, README, CLAUDE.md changes".

## Goal

Let the author put one host on auto-allow for a while, so plain `exec` calls from the AI run on that host without a click, while every other gate stays and the human can see and stop it at any time.

Why: the first real use (2026-09-28) took 15–80 s per approval. In a long agentic session on a host the author trusts, a click per command is the main cost of using sshgate at all.

Exit gate: on a real host, the author enables auto-allow for 15 minutes, runs a Claude Code task that issues several `exec` calls with no click, sees them in the Auto-allowed feed and in `audit.jsonl` (`approval: "auto"`), stops it, and the next `exec` waits in the approval column.

## Scope

In:
- Per-host grants: 15 min, 30 min, 60 min, 2 h, 4 h, or "until turned off" (forever).
- Plain `exec` only. `sudo-exec` always asks.
- Hosts that are refused: login user `root`, a stored su password, a stored sudo password. A host whose account has passwordless sudo gets a "this is root access" warning.
- Forever is stored in the vault and survives restarts, but after every unlock it is paused until the human clicks Resume.
- A timed grant holds off the idle lock until its deadline.
- The Auto-allowed feed, Stop per host, Stop all, and audit.

Out:
- Grants in `hub --cli`: every exec asks there; nothing arms a grant without the UI door.
- Rules by command pattern, "allow next N", per-session grants: not built. Pattern rules are unsafe (shell parsing); a session id would come from the AI side.
- Letting AI activity count as UI activity for the idle lock: never. A grant's deadline, fixed when the human enables it, is the only thing that holds the lock off.
- Grants that survive a lock: not built. Locking stops every grant (see "Lock").

## Decisions made in chat (2026-09-28)

| Question | Decision |
|---|---|
| Scope | Per host |
| Durations | 15/30/60 min, 2 h, 4 h, and forever |
| Forever | Stored in the vault (inside the MAC); survives restart; paused after each unlock until Resume |
| sudo | `sudo-exec` never auto-allowed |
| Root-equivalent hosts | Refuse `root` login, stored su password, stored sudo password; warn on passwordless sudo |
| Visibility | Auto-allowed feed in the AI column plus audit; no OS notification per command |
| Idle lock | A timed grant holds the idle lock off until its deadline; forever does not; manual Lock always stops everything |
| Control | Host card, not the host editor (the editor's fields are a draft applied on Save) |

A four-reviewer critique (security, architecture, UX, product) of the first draft produced most of the rules below; each rule says what it closes.

## Data model

- `config.Server.AutoAllow bool` (`json:"autoAllow,omitempty"`): forever is on. It is inside the vault MAC. It is written only by `servers.setAutoAllow` through `config.Update` with the vault unlocked.
  - `ApplyServer` does not copy it, so every `servers.save` clears it (see "Turning off").
  - `dialChanged` ignores it, like `AIVisible` and `Tunnels`, so the write never closes the connection.
  - `vault.create` clears it on kept servers, with `aiVisible` and the pins: a KDF-less file was never MAC'd.
  - Import creates servers without it.
- Hub memory, guarded by `h.mu`: `grants map[string]*grant`, keyed by server name.

```go
type grant struct {
	until    time.Time             // zero: forever
	snap     grantSnap             // the server as it was when the human enabled it
	inflight map[uint64]context.CancelFunc
}
type grantSnap struct {
	target, hostKey, hostKeyAlgo, auth string // target is user@host:port
}
```

A forever server with no entry in `grants` is **paused**: its flag is set in the vault but nothing runs without a click.

## Hub rules

### Enabling

`servers.setAutoAllow {server, mode}` with `mode` one of `off`, `15m`, `30m`, `60m`, `2h`, `4h`, `forever`. Strict params (`strictParams`). UI door only.

For any mode but `off`, all of these must hold, checked in one `h.mu` section, else the call fails and nothing changes:
1. A vault exists and is unlocked.
2. The server exists and is AI-visible. Hidden and nonexistent give byte-identical errors (`serverNotFound`).
3. It has a pinned host key.
4. `User != "root"`, and no `EncSuPassword` or `EncSudoPassword`. Why: with a stored su password plain exec runs in a root su shell; with a stored sudo password, a command planted during a grant (a `sudo` shell function in `~/.bashrc` or `~/.zshenv`) could capture it the next time the human approves a `sudo-exec`, and then use it through plain exec.

Then:
- Timed: `grants[server] = {until: now + d, snap}`; a timed mode replaces any earlier grant on that server and clears the vault flag if it was set (one `config.Update`).
- Forever: set the vault flag (one `config.Update`, then reload), then arm `grants[server] = {until: zero, snap}`. On a server whose flag is already set, `forever` only arms it (this is Resume) and writes nothing.
- Audit `kind: "config"`: action `autoAllowOn` with `until` (RFC 3339) or `forever: true`, or `autoAllowResume` for a Resume.

`servers.autoAllowCheck {server}` (strict params, UI door only) runs, on the host's pinned connection with a 10 s timeout, the fixed command `id -u; sudo -n true 2>/dev/null && echo nopasswd` and returns `{uid: number, passwordlessSudo: boolean}` or an error. The app calls it when the Auto-allow dialog opens and shows "This is root access" when `uid == 0` or `passwordlessSudo`. It checks the same conditions 1–4 first. It writes an audit record `autoAllowCheck`. It is advice for the human, not a gate: the account can change after the check.

### Running an exec

`Hub.Exec` gains one step, before `broker.Submit`, in a single `h.mu` section (`autoRun`), after the existing checks (vault exists, visible, unlocked, pinned, command sanitized, description valid):

1. If the request is `sudoExec`, or there is no grant, go to approval as today.
2. If the grant is timed and `now >= until`: end it (reason `expired`) and go to approval.
3. Resolve the server now and compare `target`, `hostKey`, `hostKeyAlgo`, `auth` with `snap`; also re-check condition 4. Any mismatch: end the grant (reason `server changed`) and go to approval. This catches every way the server can change (a save, a reload after an outside write, delete and re-create under the same name, a new su password), so the "turning off" list below is a convenience, not the safeguard.
4. If the grant already has 2 runs in flight, go to approval. Why: skipping the broker skips its cap of 5 pending; unbounded parallel channels on the shared client would exhaust sshd's `MaxSessions` and lock the human out of a terminal on that host.
5. Register the run's cancel func in `grant.inflight` and run it with the `DialConfig` resolved in step 3 (no second resolve: there was no wait to re-check across).

An auto run:
- Does **not** increment `h.running`. Why: `lockIfIdle` backs off while anything runs, so back-to-back auto runs (up to 600 s each) would keep the vault unlocked forever.
- Uses the same timeout clamp, redaction, output cap, and generic errors as an approved run.
- Is audited like an approved exec with `approval: "auto"` (see "Audit"), then sends `autoAllow.ran` after the audit write.
- Is cancelled when its grant ends, for any reason, and its audit record then says `cancelled_running` with `approval: "auto"`.

### Turning off

A grant ends, its in-flight runs are cancelled, and one `autoAllowOff` config record (with `reason`) plus one `autoAllow.off {server, reason}` notification go out, when:
- `off` from the app (`turned off`). For forever this also clears the vault flag. If that write fails, the grant still ends in memory and the error is returned.
- Stop all from the app: `servers.setAutoAllow` with `off` for each armed or paused server (`turned off`).
- It expires (`expired`): at the next exec on that server (exact), or at the hub's periodic idle check (at most one tick late, 1 minute at the default 15-minute idle lock), whichever comes first. The sweep is what cancels runs still going at the deadline.
- The vault locks, manually or idle (`locked`): `zeroKeyLocked` clears every grant. Forever flags stay in the vault; after unlock those servers are paused.
- `servers.save` of that server, including a rename (on the original name) (`saved`); `servers.delete` (`deleted`); `servers.forgetHostKey` (`server changed`). Every save turns it off, both kinds, whatever changed: simplest fail-closed rule, and the only place the editor draft and a live grant could disagree.
- `vault.create` (`vault created`).
- `snap` mismatch at exec (`server changed`).
- Hub stop: timed grants are gone; forever servers come back paused.

A reload never arms a grant. Only `servers.setAutoAllow` does.

### Lock and idle lock

- Manual Lock ends every grant, always.
- `lockIfIdle` does not fire while any timed grant has `until > now`. Once the last timed deadline passes, the normal rule applies (15 minutes since the last UI activity, nothing pending, nothing approved running). The deadline is fixed when the human enables the grant; nothing the AI does moves it.
- A forever grant does not hold off the idle lock. Forever therefore means "whenever the vault is unlocked and the human has clicked Resume".
- MCP-door calls still never count as UI activity.

### MCP door

`listServers` does not show auto-allow. The MCP door has no method that reads or changes it.

## Audit

- `broker.AuditRecord` gains:
  - `Approval string json:"approval,omitempty"`: `"auto"` for an auto run, on every outcome (allowed, error, timeout, cancelled). A separate field, because the error and cancel branches overwrite `Outcome`.
  - `WaitMs int64 json:"waitMs,omitempty"`: time from submit to the human's decision, for approved and denied requests. It is the evidence for whether longer grants are needed.
- `broker.ConfigRecord` gains `Until string json:"until,omitempty"`, `Forever bool json:"forever,omitempty"`, `Reason string json:"reason,omitempty"`. Actions: `autoAllowOn`, `autoAllowResume`, `autoAllowOff`, `autoAllowCheck`.

## Protocol 6

`ProtocolVersion` in `idle.go` and `PROTOCOL_VERSION` in `protocol.ts` become 6.

- Requests (UI door only): `servers.setAutoAllow {server, mode}` → `{}`; `servers.autoAllowCheck {server}` → `{uid, passwordlessSudo}`.
- `servers` result: each server gains `autoAllow?: {until?: string} | {forever: true, paused?: true}`, and `autoAllowRefused?: string` (why the Auto-allow button is disabled: "not visible to AI", "no pinned host key", "root login", "has an su password", "has a sudo password").
- Notifications:
  - `autoAllow.ran {server, command, description, exitCode?, error?, time}`: `command` is redacted and cut to 1000 bytes with `truncated: <n>` for the rest; `error` is the generic text the AI gets.
  - `autoAllow.off {server, reason}`.
- The renderer must not call the hub in response to either notification (idle-lock rule: every call but `status` counts as UI activity).

## Desktop

- **Host card:**
  - An **Auto-allow** action button in `.hostcard-actions`, next to Files and Tunnels. It is always enabled; when the host is refused, the dialog it opens says why (`autoAllowRefused`, as visible text) and Enable stays disabled. A disabled button could not show the reason except as a tooltip.
  - While on: a chip "Auto 42m" (renderer countdown from `until`, no polling), "Auto ∞", or "Auto paused". The chip is decorative (`aria-hidden`, inside the card button like the other chips) and uses a new token `--auto` (not amber, never pulsing: amber means "requests waiting, act now"). The action button becomes **Stop auto-allow**.
- **Auto-allow dialog** (opened by the button):
  - Duration radios: 15 min, 30 min, 60 min, 2 h, 4 h, Until turned off.
  - It calls `servers.autoAllowCheck` on open. On `uid == 0` or `passwordlessSudo` it shows "This is root access: the AI can do anything on this host." On error: "Could not check this host's sudo access."
  - It lists this host's running remote tunnels, if any: "Remote tunnels running on this host reach your machine."
  - Copy: "The AI runs any command this account can run on <host> without asking, including commands planted by what it reads (prompt injection), and can leave things that run later (cron jobs, SSH keys, shell startup files). sudo-exec still asks." Timed adds: "Ends at <time>, when you stop it, or when you lock the vault. The vault will not auto-lock before then." Forever adds: "Stays set after restart. After each unlock it waits for you to click Resume."
  - Cancel is the default and has focus. **Enable** is mouse-only (`tabIndex=-1`, Enter/Space guarded) and disabled for 500 ms after the dialog appears or its content changes (`ListChanges`). Until turned off also requires typing the host name, as the Files tab's delete does.
- **After unlock with paused forever hosts:** a banner at the top of the Hosts tab and of the AI column: "Auto-allow is paused on A, B." with **Resume** (mouse-only, 500 ms delay; calls `setAutoAllow forever` per host) and **Stop** (calls `off`).
- **Tab bar:** the AI button is shown while any host is on auto-allow or paused, with a separate non-pulsing "auto N" segment. The amber/pulse state stays tied only to pending requests. A terminal, Files, or Tunnels tab whose host is on auto-allow shows a small `--auto` dot next to its name.
- **AI column:** an "Auto-allowed" section below the pending requests:
  - **Stop all auto-allow**, always rendered, disabled when nothing is on or paused (the same no-shift rule as Deny all).
  - Newest first, the last 50 runs in renderer memory, cleared when the hub stops running (like pending requests); the audit log is the record. Each row: time, host, command (same rendering as an approval card, non-ASCII highlighted, "truncated N bytes" when cut), description, exit code or error.
  - Auto runs never open the column and never raise an OS notification.
- The renderer drops every `until`/chip on the `locked` notification and on hub restart, and marks forever hosts paused.

## Security rules

- The AI (MCP door) can neither read nor change auto-allow. A file-level attacker cannot turn it on without the master key (MAC), and a reload never arms a grant.
- A grant is tied to the server as it was when enabled (`snap`), checked at every auto run.
- No auto-allow on root-equivalent hosts known to the hub; passwordless sudo is warned about, not detectable by the hub from the vault alone.
- Auto runs cannot hold off the idle lock; only a human-set deadline can, and only until that deadline.
- At most 2 auto runs in flight per host; the rest wait for a human.
- Every grant change and every auto run is audited; the feed and the host-card chip make an active grant visible from every tab.
- Honest limit: during a grant the AI can do anything the account can, including persistence (`authorized_keys`, cron, `systemd --user`, shell startup files) that outlives the grant. The dialog says so; stopping the grant does not undo it.

## Testing

Go (`internal/hub`, `internal/config`, `internal/broker`, `cmd/sshgate`):
- An auto-allowed exec runs with no pending request, is audited with `approval: "auto"`, and sends `autoAllow.ran` after the audit.
- `sudoExec` on a granted host still waits; `setAutoAllow` refuses root login, su password, sudo password, locked, no vault, hidden, unpinned; hidden and nonexistent give identical errors; strict params refuse extra and duplicate keys and bad modes.
- Error, timeout, and cancelled auto runs keep `approval: "auto"`.
- Expiry: at exec (exact) and by the sweep (in-flight run cancelled, `autoAllowOff expired`).
- Lock (manual and idle) ends every grant and cancels in-flight runs; forever comes back paused after unlock; `forever` on a paused server arms it without a write and audits `autoAllowResume`.
- Idle lock: does not fire before a timed grant's deadline; does fire after it; a forever grant does not hold it off; back-to-back auto runs do not hold it off (auto runs do not count in `running`).
- Cap: a third concurrent auto run on one host goes to approval.
- `snap` mismatch (external write changing host or pin, delete and re-create with the same name and key, a new su password) ends the grant at exec and the request goes to approval.
- Every `servers.save` (including one that only changes the label, and a rename) turns off both kinds; `delete`, `forgetHostKey`, `vault.create` too; `dialChanged` ignores `AutoAllow` (turning auto-allow on or off keeps the connection and tunnels).
- `ApplyServer` drops the flag; `vault.create` clears it on kept servers; the MAC covers it (a tampered flag fails to load).
- A reload never arms a grant.
- `autoAllowCheck` returns uid and passwordless sudo from sshtest; errors on an unreachable host; audits.
- `listServers` does not expose auto-allow; the MCP door has no auto-allow method.
- `WaitMs` is set on approved and denied records.
- `cmd/sshgate/e2e_test.go`: bridge → hub → sshd exec on a granted host returns with no approval.
- `hub --cli`: a forever host stays paused; every exec asks.

Desktop (vitest):
- Chip label and countdown; dropping chips on `locked`; paused state.
- Feed reducer: newest first, cap 50, cleared on hub stop, truncated label.
- Dialog: the refusal reason shown and Enable disabled for a refused host; Enable disabled for 500 ms and after content changes, mouse-only, typed host name for forever, root warning from the check result, remote tunnels listed.
- No hub call is made in response to `autoAllow.ran` or `autoAllow.off`.

Playwright (sshtestd):
- Enable 15 min from the host card; an exec from a bridge client runs without a click and shows in the feed; Stop; the next exec waits in the column; enable again, Lock, unlock: chip gone and the next exec waits.
- Enable forever (typed name); lock; unlock; the banner shows paused; an exec waits; Resume; the next exec runs. (After a hub restart the hub is in the same state as after a lock: no grants, the flag in the vault; the Go tests cover a new `Hub` on the same file.)

## ROADMAP, PRODUCT, README, CLAUDE.md changes

- ROADMAP standing decision becomes: "Every AI command is approved by a human by default. The human may put one host on auto-allow (plain exec only, never sudo-exec, never on root-equivalent hosts) for a set time, or until turned off with a Resume after each unlock; it is visible while on, audited, and stoppable at any time." Add a finding: a host on auto-allow sends its command output to the AI unreviewed, which bears on slice 4's entry gate.
- PRODUCT.md Purpose, Positioning, and principles: per-command approval is the default, not an absolute; state the auto-allow limits in the words above.
- README: replace "There are no auto-approval rules" and "no allow-list, no 'always allow', and no auto-approval of any kind" with the same rule and the honest limit (a grant is not a privilege boundary; persistence outlives it).
- `2026-09-24-desktop-app-design.md` Threat Model: note that on a host with a grant, a same-uid process that reaches the MCP door gets remote exec without a human, and that approval is no longer the only egress control there; point to this spec.
- CLAUDE.md: "Every MCP exec goes through `broker.Submit`" and the `Hub.Exec` order gain the auto-allow step; the UI-door method list, protocol 6, and the Desktop section gain the auto-allow pieces.

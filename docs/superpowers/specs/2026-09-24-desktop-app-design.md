# ssh-mcp Desktop: AI-Aware SSH Client — Design (Slice 1)

Date: 2026-09-24
Status: Draft (sections approved in brainstorming; pending review of this document)

## Goal

Build a cross-platform desktop SSH client (macOS, Windows, Linux as equal
targets), in the spirit of Termius. It shares its host vault with `ssh-mcp` so
AI agents can run commands on saved hosts **only with per-command human
approval**. The differentiator is the AI integration: an approval flow, a live
feed of AI commands, and an audit log. Being "yet another SSH client" is not
the goal.

This document covers **slice 1** only: the desktop app shell, terminal tabs,
the hub process, the MCP bridge, and the approval flow. Later slices get their
own spec: full terminal features, SFTP and port forwarding, packaging and
signing.

**Audience for slice 1 is the author alone.** The app is built for personal
daily use first. Code signing, notarization, auto-update, installers, and
plugin-store distribution are out of scope until there is a second user.
Unsigned local builds (`electron-builder --dir` or `npm start`) are the
deliverable. Security controls are not relaxed by this: the AI-facing
boundary is the point of the project.

## Decisions

| Topic | Decision |
|---|---|
| Targets | macOS, Windows, Linux, all first-class |
| UI shell | Electron (consistent Chromium on all three OSes; same model Termius uses) |
| Frontend | React + TypeScript + xterm.js |
| Core | Go, reusing `internal/config` and `internal/sshx`; runs as an Electron child process |
| Rejected: Wails v3 | Three different web engines; WebKitGTK terminal performance risk on Linux; v3 still beta |
| Rejected: Swift | No UI path to Windows or Linux |
| Rejected: gotk4 | No terminal widget outside Linux (VTE); non-native look on macOS and Windows |
| Rejected: TypeScript core | Throws away the Go vault and hardening; two SSH cores to maintain |
| Rejected: Rust / Tauri | Same WebKitGTK risk as Wails; full rewrite |
| Rejected: HTTP/SSE localhost for MCP door | No process boundary; any localhost process or web page can POST. Token/auth on HTTP is difficult to enforce per-peer. Unix socket 0600 + UID check is simpler. |
| Process model | The app hosts the hub. The `ssh-mcp` binary gains a `hub` subcommand (backend for Electron) and a bridge mode (MCP stdio → hub) |
| AI execution | Separate `exec` channel, never the user's PTY. A "Send to tab" option pastes the command into the user's terminal without pressing Enter |
| App closed | The bridge refuses with "Open the app to approve commands". There is no headless vault access |
| `--host` CLI mode | Unchanged; standalone, no app needed |
| Repo layout | Same repo and Go module; Electron app in `desktop/` with its own `package.json` |

### Breaking change

Headless vault access through `SSH_MCP_MASTER_PASSWORD_FILE` is **removed**. It
is an approval bypass. Vault-backed servers are reachable from MCP only
through a running app. Explicit `--host` mode is unaffected.

## Threat Model

Inherits the model of the 2026-07-31 spec: encryption at rest protects against
file-level exposure, and same-uid code execution is out of scope.

New in-scope adversary: **the AI agent itself**, for example after prompt
injection. It may submit malicious commands and write misleading
`description` text. Controls:

- Every vault command requires human approval. `sudo` is never
  auto-approved.
- The approval UI shows the exact command bytes. Characters that can make the
  displayed command differ from what runs (C0/C1 controls, Unicode format and
  bidi characters) are rejected before display. Non-ASCII characters are
  highlighted to expose homoglyphs.
- `description` is shown as unverified AI text, visually subordinate to the
  command.
- The MCP door of the hub cannot unlock the vault, approve commands, or read
  secrets.
- The MCP door cannot read terminal buffer or scrollback from user tabs. No
  tool exposes this channel; it is an egress path that bypasses approval.
- Only servers explicitly marked `aiVisible` are reachable from the MCP door
  (see §Data Model). A prompt-injected agent cannot discover or target hosts
  the user never opted in.

**Server output is an egress channel to the LLM.** Everything a command
prints goes into the AI transcript, which may be stored by a third party.
The vault `Redactor` masks only vault secrets; secrets that live on the
remote host (`/etc/shadow`, `env`, private keys, tokens in config files)
are not covered. Slice 1 mitigates this only with the output cap and the
approval step, where the user sees the command before it runs.
Pattern-based output redaction (private-key blocks, `password=`, bearer
tokens) is deferred to a later slice and listed under Known Risks.

The MCP socket or pipe accepts only same-uid peers. A same-uid attacker can
submit requests, but they still require approval; beyond that, same-uid is out
of scope as before.

## Architecture

```
Claude / MCP client
   │ stdio (MCP)
   ▼
ssh-mcp (bridge mode) ──socket/pipe (MCP door)──┐
                                                ▼
Electron main ◄──stdio (UI door, JSON-RPC)──► ssh-mcp hub
   │ preload (contextBridge, typed API)        ├─ internal/broker   (approval, policy, audit)
   ▼                                           ├─ internal/sshx     (Manager, Registry, TermSession)
Renderer: React + xterm.js                     └─ internal/config   (vault, sanitize, redact)
   (all Go traffic via transport.ts)
```

### Components

- **`cmd/ssh-mcp`**: one binary, three modes:
  - `--host …`: standalone, unchanged.
  - No flags: bridge mode. Connects to the hub's MCP door. If the hub is
    unreachable, every tool call returns "Open the app to approve commands".
  - `hub`: backend for Electron. Speaks JSON-RPC on stdio and listens on the
    MCP door.
- **`internal/config`**: existing. `SanitizeCommand` is tightened (see
  §Command Validation). A shared output-cap helper is added.
- **`internal/sshx`**: existing. Adds `Manager.OpenChannel()` and
  `TermSession` (see §Terminal Sessions).
- **`internal/broker`** (new): approval queue, policy, and audit log. Core API:
  `Submit(ctx, Request) (Decision, error)`, which blocks until the request is
  decided, times out, or is cancelled. It emits events to a subscriber (the
  UI door) and has no dependency on Electron or the UI.
- **`internal/hub`** (new): owns the unlocked master key, the Registry, the
  term sessions, and the broker. It exposes two doors with different
  privileges:
  1. **UI door** (stdio to Electron main), full privilege: unlock/lock,
     list hosts, open/close terminals, terminal I/O, and approve/deny.
  2. **MCP door** (Unix socket with mode 0600 plus a peer-UID check on
     macOS and Linux; a named pipe with a current-user ACL on Windows):
     only `listServers`, `exec`, and `sudoExec`. Both exec calls always go
     through the broker.

     **MCP-door rules** (apply to any tool added later):
     - No tool on this door builds a shell string from its arguments. The
       only thing that reaches a remote shell is the user-approved
       `command` byte string, unchanged. Convenience tools that interpolate
       a "service name" or "path" into a template are how
       mcp-ssh-manager's `readonly` mode was bypassed
       (GHSA-m793-whw6-f537, August 2026).
     - No tool on this door reads or writes the vault, host records, or
       settings.
     - Every tool that causes remote execution goes through the broker.
       There is no "read-only" shortcut around approval.
     - No tool on this door reads a user tab's screen or scrollback. The
       AI sees only the output of commands it was approved to run.
       (`tabby-mcp-server` exposes terminal buffers; that is an egress path
       that bypasses approval.)
- **`desktop/`** (Electron):
  - main: spawns the hub, restarts it on crash, kills it on quit, relays
    RPC, shows a tray badge with the pending count, and raises OS
    notifications.
  - preload: exposes a minimal typed API through `contextBridge`. Uses
    `contextIsolation: true`, `sandbox: true`, `nodeIntegration: false`,
    and a strict CSP.
  - renderer: React + xterm.js. **Only `transport.ts` talks to Go.**

### Data Model

`config.Server` gains one field:

```go
AIVisible bool `json:"aiVisible,omitempty"`
```

- Default `false`. Existing vaults load with every server hidden from the MCP
  door; there is no migration.
- `listServers` on the MCP door returns only servers with `AIVisible == true`.
  `exec` and `sudoExec` on a hidden server return `server "x" not found`,
  the same error as a nonexistent server, so the door does not leak which
  hosts exist.
- The field is plain JSON, not encrypted. It is covered by the file MAC like
  every other field, so a file-level attacker cannot flip it without the
  master key.
- Slice 1 edits it in `ssh-mcp web` (a checkbox on the server form, off by
  default). The app shows a badge on AI-visible hosts.

### Vault lifecycle

- The master key lives only in hub memory. The bridge and the MCP client never
  see it.
- The vault is unlocked in the app UI. It locks on app quit and after 15
  minutes with no UI input and no pending or running AI requests. Open
  terminal tabs keep their SSH connections, but new connections need an
  unlock.
- Slice 1 keeps host CRUD in `ssh-mcp web`. The hub reloads the vault when the
  on-disk `Revision` changes. Concurrent writes are already serialized by
  `flock`. Known cost: the vault must be unlocked twice (once in the app, once
  in the web UI) until CRUD moves into the app.

### Hub ↔ Electron protocol

Newline-delimited JSON-RPC over stdio. Terminal bytes are base64-encoded
inside JSON. Slice 1 measures throughput (§Terminal Sessions). If the
criteria fail, the channel switches to length-prefixed binary frames. That
change touches only the hub and Electron main.

## AI Command Flow

1. The MCP client calls `exec` or `sudo-exec` with `server`, `command`, and
   `description`. The bridge validates nothing. It forwards the request to the
   MCP door with the MCP `clientInfo` name. The hub is the single validation
   point.
2. The hub checks the peer UID, that the server exists, and that the vault is
   unlocked. A locked vault is an immediate error; requests are not queued
   waiting for unlock.
3. Command validation runs (§Command Validation).
4. Broker policy:
   - Default: ask.
   - "Always allow" is an **exact command string on an exact server**.
     Pattern rules are rejected for slice 1 because they are trivially
     bypassed (`ls; rm -rf ~`, `$(…)`, `find -delete`, `less` then `!sh`).
   - `sudoExec` is never auto-approved.
5. The hub emits a `pending` event on the UI door. If the window is
   unfocused, Electron raises an OS notification; clicking it focuses the
   dialog. The tray badge shows the pending count. Multiple pending requests
   are listed and decided independently.
6. The approval dialog shows:
   - Server name and host.
   - The full command in monospace, never truncated, with non-ASCII
     highlighted.
   - The `description`, labelled "AI's description (unverified)".
   - The client name and received time.
   - Actions:
     - **Allow**.
     - **Deny**, with an optional reason that is returned to the AI.
     - **Always allow this command**.
     - **Send to tab**: pastes the command into that server's most recent
       tab, opening one if needed and pasting after its first output. Uses
       `terminal.paste()`, which applies bracketed paste when the shell
       supports it, so the command never runs without the user pressing
       Enter. The AI receives "User chose to run this in their terminal; no
       output captured".
7. Execution uses the existing `Registry.Get` and `Exec`/`ExecSudo`. Output is
   redacted with `Redactor`, then capped (§Output Cap).
8. The result returns to the AI. An audit record is appended.

### Timeouts and cancellation

- Approval wait: 5 minutes, then auto-deny with "approval timed out".
  This is separate from the execution timeout.
- Execution: the existing 60 s timeout, starting at approval.
- MCP client tool-call timeouts may be shorter than the approval window.
  Verify Claude Code's actual limit and document it (open item).
- MCP `notifications/cancelled` or a bridge disconnect:
  - A pending request is withdrawn and its dialog closes.
  - A running request's context is cancelled, which triggers the existing
    abort path.

### Command Validation

`SanitizeCommand` currently rejects only `\n`, `\r`, and `\0`. That lets ESC,
other C0/C1 controls, and bidi overrides (e.g. U+202E) through, all of which
can make the approval dialog display something other than what executes. The
new rule rejects:

- every rune where `unicode.IsControl` is true, except `\t`;
- every rune in Unicode category `Cf` (format characters, including bidi
  controls and zero-width characters).

The change is in the shared function, so `--host` mode gets it too. The
`description` field gets the same rule.

### Output Cap

There is currently no cap, so a large `cat` floods the AI's context.
Output is capped at a fixed 64 KB: the first 32 KB and the last 32 KB, joined
by a `… [truncated N bytes] …` marker. The cap is not configurable until
someone needs it.

### Audit log

JSONL, mode 0600, next to `servers.json`. Each record contains: time, client,
server, redacted command, decision, decided-by (user or rule), exit code,
duration, and output byte count. **Output content is never logged.** There is
no rotation in slice 1.

### Errors returned to the AI

| Situation | Message |
|---|---|
| App or hub not running | "Open the app to approve commands" |
| Vault locked | "Vault is locked; unlock it in the app" |
| Unknown server | `server "x" not found` |
| Forbidden character | Names the character class and position |
| Denied or approval timeout | "Denied by user" plus the reason, if given |
| Host key mismatch | Generic failure; fingerprint detail is shown only in the app |
| Auth failure or exec timeout | Redacted error |
| Hub crash mid-request | "App closed or crashed"; Electron restarts the hub and pending requests are lost |

`listServers` needs no approval. It returns names and lock status only, and
only for servers with `AIVisible` set.

## Terminal Sessions

- One `TermSession` per tab. Each opens a **new channel on the server's
  existing `ssh.Client`**, then calls `RequestPty("xterm-256color", rows,
  cols)` and `Shell()`. One TCP connection and one authentication are shared
  by all tabs and AI execs for that server.
- `Manager.Exec` currently holds the manager mutex for the whole command.
  `OpenChannel()` holds it only while ensuring the client exists, then
  returns an independent channel. Long-lived PTYs never hold the mutex.
- Tabs get a plain login shell. The su-elevated shell is only for AI `exec`.
- Editing a host with open tabs closes them, because the Registry replaces the
  manager. The app warns before saving: "Saving will close N open tabs".
  There is no refcounting.

### Data path

```
input:  xterm.onData → transport.ts → preload → main → stdio → hub → ssh stdin
output: ssh stdout → hub (32 KB reads, batched ~8 ms or 64 KB) → stdio → main
        → renderer → xterm.write(Uint8Array)
```

Bytes are carried end to end; no layer decodes them to strings. xterm.js
handles UTF-8 sequences that are split across chunks.

### Flow control

- The renderer acks processed bytes through the `xterm.write` callback.
- The hub stops reading the SSH channel when unacked bytes exceed about 1 MB,
  and resumes below about 256 KB.
- While the hub is not reading, the SSH channel window fills and the remote
  slows down. No layer buffers without bound.

### Lifecycle

- Resize: fit addon `onResize`, debounced ~50 ms, then `term.resize`, then
  `WindowChange`.
- Tab close: `term.close`, then the session is closed.
- Remote shell exit: `term.exit {code}`. The tab shows "[exited]" and a
  Reconnect button.
- Keepalive (`keepalive@openssh.com`) every 30 s. On failure, all sessions
  on that client exit with a reason, and the Registry evicts the manager.
- Hub crash: all tabs exit. Electron restarts the hub, and tabs offer
  Reconnect.
- Host keys: the existing silent TOFU, equivalent to
  `StrictHostKeyChecking=accept-new`. An interactive fingerprint prompt is
  deferred.

### Throughput criteria

Run `cat` on a 50 MB file and `yes | head -c 200M` through the app, compared
with `ssh` in the native terminal. Pass requires all of:

- no UI freeze longer than 200 ms;
- typing stays responsive during the flood;
- renderer and main RSS stay bounded rather than growing.

If any criterion fails, switch the stdio channel to binary frames.

## Testing

- **Go unit (no Docker):**
  - Sanitize table: ESC, U+202E, zero-width characters, and tab allowed.
  - Output cap: head, tail, and marker.
  - Broker, with a fake clock and a fake decider: exact-match allow, sudo
    never auto-approved, approval timeout, cancellation, concurrent pending
    requests.
  - **Hub security:** MCP-door calls to `unlock`, `approve`, or any
    UI-only method are rejected. A foreign-UID peer is rejected. A server
    with `AIVisible == false` is absent from `listServers` and `exec` on it
    returns the same error as a nonexistent server.
- **Go integration (existing testcontainers sshd):**
  - A `TermSession` echo round-trip.
  - `stty size` after resize.
  - Flow control: with a slow consumer, unacked bytes stay within the high
    watermark plus one chunk.
  - Keepalive: stopping the container yields an exit event.
- **Go end-to-end:**
  - `ssh-mcp hub` runs with a fake auto-approving UI. A go-sdk MCP client
    goes through the bridge, runs `exec` against the container, and gets
    redacted output.
  - With the hub stopped, the bridge returns "Open the app…" and nothing
    executes.
- **Electron:** one Playwright smoke test:
  1. Launch the app and unlock the vault.
  2. Open a tab and run `echo`.
  3. Trigger an MCP `exec` and check that the dialog appears.
  4. Approve and check that the output is returned.
- **Manual, per OS:**
  - Vietnamese IME: macOS Telex, Windows Unikey, and ibus and fcitx5 on
    Linux.
  - Throughput criteria.
  - Clicking a notification focuses the right dialog.
- **CI:** the existing Go job, plus a `desktop` job (`npm ci`, typecheck,
  Playwright under xvfb on Linux) and unsigned builds for all three OSes.

## Success Criteria (Slice 1)

1. From Claude Code, `exec` on a vault server raises an OS notification.
   Approving returns the output to the AI; denying returns the reason.
2. With the app closed, the AI gets "Open the app to approve commands" and
   nothing executes.
3. Tests prove the MCP door cannot unlock the vault, approve commands, or read
   secrets.
4. Throughput criteria pass.
5. The manual checklist passes on macOS, Windows, and Linux.

## Known Risks

- Base64-in-JSON terminal transport may be too slow. The mitigation (binary
  frames) is pre-planned and local to two components.
- MCP client timeouts may be shorter than the 5-minute approval window.
- Electron bundles Chromium. Security releases must be tracked and shipped
  promptly.
- Command output is an unfiltered egress channel to the LLM. Remote-host
  secrets that a command prints are not redacted in slice 1; only the
  output cap and the approval step stand in the way.
- Double unlock (app plus web UI) in slice 1 is a known UX cost.

## Open Items

- Verify Claude Code's MCP tool-call timeout and document it.
- Choose the MCP door path per OS (e.g. under the user config dir on
  macOS and Linux; a named pipe name derived from the user SID on Windows).

## Out of Scope (Slice 1)

SFTP, port forwarding, ProxyJump, ssh-agent on Windows (OpenSSH pipe and
Pageant), split panes, local terminal (`node-pty`), host CRUD in the app,
the host-key fingerprint prompt, pattern-based approval rules, audit
rotation, sync, mobile, code signing, notarization, and auto-update.

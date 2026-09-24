# ssh-mcp Desktop: AI-Aware SSH Client — Design (Slice 1)

Date: 2026-09-24
Status: Approved. Slice 1a (Go core) implemented; amended 2026-09-24 to match it (see §Amendments).

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
| No persistent AI shell | Every `exec` is a fresh SSH channel: `cd`, environment variables, and virtualenvs do not carry over. The AI must write `cd /app && ./run` in one command. The command runs under the user's login shell with `-c` (non-interactive: `~/.bashrc` is usually skipped and `PATH` may differ from an interactive tab). The su-elevated path keeps its existing persistent root shell |
| No auto-approval rules | "Always allow" is dropped from slice 1. Exact-string matching is a TOCTOU on remote state (`./deploy.sh` runs whatever the file contains today), and a rule store outside the vault MAC would let a file-level attacker bypass approval. Every request is approved by hand |
| AI never first to a host | `exec` from the MCP door on a server with no pinned `HostKey` is refused. The user must connect once from the app, which pins the key under TOFU. The AI cannot be the party that accepts an unknown host key |
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
  2. **MCP door**: only `listServers`, `exec`, and `sudoExec`. Both exec
     calls always go through the broker. Transport per OS:
     - macOS and Linux: Unix socket, mode 0600, at
       `<base>/ssh-mcp/hub.sock`. `<base>` is `$SSH_MCP_RUNTIME_DIR` if set
       (development and tests), else `/run/user/<uid>` on Linux when it
       exists and is owned by the user, else `/tmp` with the directory
       named `ssh-mcp-<uid>`. The path never depends on `TMPDIR` or
       `XDG_RUNTIME_DIR`, because MCP clients may start the bridge with a
       minimal environment. The directory must be a real directory owned by
       the user with mode 0700; otherwise the hub refuses to start. Not under
       the config directory: macOS limits `sun_path` to 104 bytes and a
       long username under `~/Library/Application Support/` overflows it.
       The hub checks the peer UID (`SO_PEERCRED` / `LOCAL_PEERCRED`) on
       every connection.
     - Windows: named pipe `\\.\pipe\ssh-mcp-hub-<user SID>` with a DACL
       granting access to the current user only, created with
       `FILE_FLAG_FIRST_PIPE_INSTANCE` so a squatter cannot pre-create the
       name (verify that `go-winio` sets this; add it if not). The
       **bridge** verifies the pipe server before sending anything:
       `GetNamedPipeServerProcessId`, open the process, and compare its
       token user SID with its own. A mismatch is a hard error.

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
  - main: spawns the hub, kills it on quit, relays RPC, shows a tray badge
    with the pending count, and raises OS notifications. On hub crash it
    restarts with backoff (1 s, 3 s, 10 s); after three crashes within a
    minute it stops and shows an error with the hub's last stderr. A
    restarted hub starts **locked**: the master key lived only in the dead
    process.
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
2. The hub checks, in order: the peer UID; that the server exists and has
   `AIVisible` set (otherwise `server "x" not found`); that the vault is
   unlocked (otherwise an immediate error, requests are not queued waiting
   for unlock); that the server has a pinned `HostKey` (otherwise "connect
   to this server from the app once first"); and that fewer than 5 requests
   are pending (otherwise "too many pending requests, try again later").
3. Command validation runs (§Command Validation). The optional `timeoutSec`
   argument is clamped to [1, 600]; the default is 60.
4. Broker policy: **every request is asked.** There are no auto-approval
   rules in slice 1 (see Decisions). `sudoExec` is asked like any other
   request and is labelled as sudo in the panel.
5. The hub emits a `pending` event on the UI door. If the window is
   unfocused, Electron raises an OS notification; clicking it focuses the
   window. The tray badge shows the pending count. Multiple pending requests
   are listed and decided independently.
6. **The approval panel is non-modal.** It is a side panel, not a dialog. It
   never takes keyboard focus from the terminal, so keystrokes the user is
   typing cannot land on an approval button. Within the panel:
   - Server name and host.
   - The full command in monospace, never truncated, with non-ASCII
     highlighted.
   - The requested timeout.
   - The `description`, rendered as plain text, labelled "AI's description
     (unverified)". It is validated with the same character rules as the
     command and capped at 500 characters. The hub never executes it: it
     is metadata that is shown and audited only.
   - The client name, labelled "(unverified)": any same-uid process can
     claim any name. The received time.
   - Actions:
     - **Deny** is the default action and the only one reachable by Enter.
       It has an optional reason field that is returned to the AI.
     - **Allow** has no keyboard shortcut and is disabled for 500 ms after
       the request appears.
     - **Deny all** clears every pending request.
     - **Send to tab**: pastes the command into that server's most recent
       tab, opening one if needed and pasting after its first output. Uses
       `terminal.paste()`, which applies bracketed paste when the shell
       supports it, so the command never runs without the user pressing
       Enter. The AI receives "User chose to run this in their terminal; no
       output captured".
7. Immediately before execution the broker re-checks the request context.
   If the MCP client cancelled while the user was deciding, the command
   does not run and the audit record says `approved_but_cancelled`.
   Execution uses the existing `Registry.Get` and `Exec`/`ExecSudo`.
   stdout and stderr are captured separately, each redacted with
   `Redactor` and capped independently (§Output Cap).
8. The result returns to the AI as structured text: exit code, then stdout,
   then stderr. An audit record is appended.

### Timeouts and cancellation

- Approval wait: 5 minutes, then the request expires with "approval timed
  out" (distinct from a deny, so the agent may retry later). This is
  separate from the execution timeout.
- Execution: `timeoutSec` from the request (default 60, max 600), starting
  at approval. The value is shown in the panel so the user approves it with
  the command.
- MCP client tool-call timeouts may be shorter than the approval window.
  While a request is pending or running, the bridge sends
  `notifications/progress` every 5 s if the client supplied a
  `progressToken`; the MCP spec lets clients reset their timeout on
  progress. Whether Claude Code does so, and its `MCP_TOOL_TIMEOUT` setting,
  remain to be verified (open item).
- MCP `notifications/cancelled` or a bridge disconnect:
  - A pending request is withdrawn and removed from the panel.
  - A running request's context is cancelled. The hub sends
    `Session.Signal(SIGKILL)` and then closes the channel. Servers may
    ignore the signal and there is no PTY, so no SIGHUP is delivered; the
    audit record and the AI's error both say "cancelled; the remote process
    may still be running".

### Command Validation

`SanitizeCommand` currently rejects only `\n`, `\r`, and `\0`. That lets ESC,
other C0/C1 controls, and bidi overrides (e.g. U+202E) through, all of which
can make the approval dialog display something other than what executes. The
new rule rejects:

- every rune where `unicode.IsControl` is true, **including `\t`** (a tab
  pasted into a shell without bracketed paste triggers completion and
  changes the command; use `$'\t'` when a literal tab is needed);
- every rune in Unicode category `Cf` (format characters, including bidi
  controls and zero-width characters).

The change is in the shared function, so `--host` mode gets it too. The
`description` field gets the same rule plus the existing 500-character cap.

### Output Cap

There is currently no cap, so a large `cat` floods the AI's context.
stdout and stderr are capped **separately**, each at 64 KB: the first 32 KB
and the last 32 KB, joined by a `… [truncated N bytes] …` marker. Capping
after merging would let stderr push stdout out of the window. The cap is
not configurable until someone needs it. `Manager.runOnce` currently merges
both streams into one buffer and must be changed.

### Audit log

JSONL, mode 0600, next to `servers.json`. Each record contains: time, client
name (as claimed), server, redacted command, the description verbatim (it
is already capped at 500 characters and is evidence of what the AI claimed),
requested timeout, decision (`allowed`, `denied`, `expired`,
`approved_but_cancelled`, `sent_to_tab`, `cancelled_running`), deny reason,
exit code, duration, and stdout/stderr byte counts. **Output content is
never logged.** There is no rotation in slice 1.

### Errors returned to the AI

| Situation | Message |
|---|---|
| App or hub not running | "Open the app to approve commands" |
| Vault locked | "Vault is locked; unlock it in the app" |
| Unknown or hidden server | `server "x" not found` (byte-identical in both cases) |
| No pinned host key | "connect to this server from the app once first" |
| Too many pending | "too many pending requests, try again later" |
| Forbidden character | Names the character class and position |
| Denied | "Denied by user" plus the reason, if given |
| Approval expired | "Approval timed out after 5 minutes; you may retry" |
| Cancelled while running | "Cancelled; the remote process may still be running" |
| Host key mismatch | "host key verification failed; check the server in the app"; fingerprint detail goes to the audit reason and the hub's stderr |
| Connection, auth, or resolve failure | "connection to server failed; see the app for details"; detail goes to the audit reason and stderr (never hosts, ports, or paths to the AI) |
| Exec timeout | Redacted timeout error |
| Hub crash mid-request | "App closed or crashed"; Electron restarts the hub and pending requests are lost |

`listServers` needs no approval and **works while the vault is locked**
(names are plaintext). It returns, for servers with `AIVisible` set, the
name and a `locked` boolean so the agent can tell the user to unlock the
app. It never returns hosts, users, or secrets.

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
- Hub crash: all tabs exit and the vault is locked. Electron restarts the
  hub with backoff; tabs offer Reconnect, which first prompts for unlock.
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
  - Sanitize also rejects `\t`, and the error names the character class
    and position.
  - Output cap applies to stdout and stderr independently: a 1 MB stderr
    does not evict stdout.
  - Broker, with a fake clock and a fake decider: approval expiry returns
    `expired` not `denied`; cancellation while pending removes the request;
    **approve after cancel does not execute** and audits
    `approved_but_cancelled`; the 6th concurrent request is refused while 5
    are pending and accepted once one is decided; "Deny all" resolves every
    pending request as denied; `timeoutSec` is clamped to [1, 600].
  - **Hub security:** MCP-door calls to `unlock`, `approve`, or any
    UI-only method are rejected. A foreign-UID peer is rejected. A server
    with `AIVisible == false` is absent from `listServers`, and `exec` on
    it returns a byte-identical error to `exec` on a nonexistent server.
    `exec` on an AI-visible server with an empty `HostKey` is refused
    without dialing. `listServers` succeeds while locked and reports
    `locked: true`.
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
3. Tests prove the MCP door cannot unlock the vault, approve commands, read
   secrets, see hidden servers, or reach a server whose host key is not yet
   pinned.
4. A flood of requests from a misbehaving client is capped at 5 pending and
   can be cleared with one action; typing in a terminal while a request
   arrives never approves it.
5. Throughput criteria pass.
6. The manual checklist passes on macOS, Windows, and Linux.

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

- Verify empirically whether Claude Code resets its tool-call timeout on
  `notifications/progress`, and what `MCP_TOOL_TIMEOUT` controls. Document
  the finding in the README.
- Verify that `go-winio`'s `ListenPipe` sets `FILE_FLAG_FIRST_PIPE_INSTANCE`.
- Verify how the target sshd versions treat `Session.Signal(SIGKILL)`.

## Out of Scope (Slice 1)

SFTP, port forwarding, ProxyJump, ssh-agent on Windows (OpenSSH pipe and
Pageant), split panes, local terminal (`node-pty`), host CRUD in the app,
the host-key fingerprint prompt, any auto-approval rules (exact or pattern), audit
rotation, sync, mobile, code signing, notarization, and auto-update.

## Amendments

Changes made after slice 1a was implemented, so this document matches the
code. Each came from a review finding.

- Socket path: see §Components, MCP door. It no longer uses `TMPDIR` or
  `XDG_RUNTIME_DIR` (MCP clients may pass a minimal environment).
- The description is never appended to the executed command. It could run
  as code if the command ended in `\` or left a quote open.
- A vault with a KDF and no key in memory is locked for every AI request,
  including key-only and agent servers; the on-disk file is not verified
  while locked. `listServers` still works and reports `locked: true`.
- AI-facing connection errors are generic (see the errors table).
- `hub --insecureIgnoreHostKey` is refused; host-key checking cannot be
  turned off for AI execs.
- UI-door protocol facts for the desktop app: integer JSON-RPC ids;
  `term.write`, `term.ack` and `term.resize` are notifications, every
  other method is a request; `term.open` takes a client-chosen id (1–64
  characters of `[A-Za-z0-9_-]`); send nothing to a terminal before its
  `term.open` reply; ignore data for unknown ids; `decided` events carry
  outcomes `allowed`, `denied`, `sent_to_tab`, `expired`, and `withdrawn`;
  `term.dropped {id, bytes}` reports input dropped because the queue was
  full.
- Not yet implemented, scheduled for slice 1b: the 15-minute idle
  auto-lock (§Vault lifecycle) and a `hello` method that returns the hub
  protocol version.

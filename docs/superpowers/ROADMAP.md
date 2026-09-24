# ssh-mcp Roadmap

Long-lived plan across every slice. Each slice gets its own spec in
`specs/` and its own implementation plan in `plans/`, written when that
slice starts. This file only fixes order, gates, and cross-slice decisions.

Updated: 2026-09-24

## Standing decisions

These hold across all slices. Changing one needs a spec revision, not a
plan note.

- One Go binary (`ssh-mcp`) is the core: standalone `--host` mode, MCP
  bridge, and `hub`. The UI is a client of the hub, never the other way.
- Desktop UI is Electron + React + xterm.js. The renderer talks to Go only
  through one transport module.
- The MCP door is a Unix socket or named pipe with a same-user check. Never
  HTTP or SSE on localhost.
- Every AI command is approved by a human. No auto-approval rules of any
  kind until a spec argues otherwise.
- The AI never sees a host the user did not mark `AIVisible`, and never
  connects first to a host whose key is not pinned.
- Vault format: argon2id, AES-GCM per field with AAD, whole-file MAC. Any
  new persisted setting that affects security goes inside the MAC.
- Audit never stores command output.
- Audience is the author until a second user exists. No signing,
  notarization, or auto-update before then.

## Slices

| # | Slice | Status | Spec | Depends on | Entry gate | Exit gate |
|---|---|---|---|---|---|---|
| 0 | Go conversion + web config UI | Done | `specs/2026-07-31-go-conversion-web-ui-design.md` | — | — | Merged on `feat/go-conversion`; CI runs `go test` |
| 1 | Desktop app: hub, broker, MCP door, Electron shell, terminal tabs, approval panel | 1a (Go core) done; 1b (Electron) not planned | `specs/2026-09-24-desktop-app-design.md` | 0 | Spec approved | Success criteria 1–6 in the spec; author uses it daily |
| 2 | Terminal completeness: host CRUD in the app (retire `ssh-mcp web`), host-key fingerprint prompt, split panes, `~/.ssh/config` import, ProxyJump, Windows agent (OpenSSH pipe, Pageant), local shell via `node-pty` | Not specced | — | 1 | Slice 1 used daily for two weeks; throughput criteria passed; stdio transport decision settled | Author no longer opens `ssh-mcp web` or another terminal for SSH work |
| 3 | SFTP and port forwarding (local, remote, dynamic) | Not specced | — | 2 | Slice 2 done | File browser and tunnels usable from a saved host |
| 4 | Egress and audit: pattern redaction of command output (private keys, `password=`, bearer tokens), audit rotation, audit viewer in the app | Not specced | — | 1 | A real incident, or a host with secrets the AI must query | Redaction tests pass on a corpus of real outputs |
| 5 | Distribution: code signing, notarization, auto-update, installers, CI release builds | Not specced | — | 2 | A second user asks for a build | Signed builds for all three OSes from CI |
| — | Sync between machines, mobile, plugin API, Tabby plugin | Unscheduled | — | — | Explicit decision | — |

## Rules for this file

- A slice moves to "In progress" only after its spec is approved and its
  plan exists in `plans/`.
- An entry gate is a fact to verify, not a date. If the fact is not true,
  the slice waits.
- Findings from one slice that change another slice's assumptions are
  recorded here under the affected slice before that slice is specced.

## Findings carried forward

- Slice 1 → 2: whether base64-in-JSON stdio is fast enough, or binary
  frames were needed.
- Slice 1 → 2: whether Claude Code resets its tool timeout on
  `notifications/progress`, and the value of `MCP_TOOL_TIMEOUT`.
- Slice 1 → 2: `go-winio` v0.6.2 gets first-instance semantics from `NtCreateNamedPipeFile` with `FILE_CREATE` (`pipe.go:378-381`) rather than the flag. Windows runtime behaviour (DACL, SID checks) is still untested on a real Windows machine.
- Slice 1 → 4: which remote-host secrets actually appeared in command
  output during daily use; drives the redaction pattern list.
- Slice 1a → 1b: the spec's 15-minute idle auto-lock is not implemented. It is hub-side (the hub knows pending/running requests and UI-door activity) and belongs in the 1b plan.
- Slice 1a → 1b: UI-door protocol facts the Electron app must follow: integer JSON-RPC ids; `term.write`/`term.ack`/`term.resize` are notifications, everything else is a request; client-chosen terminal ids; do not send to a terminal before its `term.open` reply; ignore data for unknown ids; decisions include `withdrawn` and `expired`; add a `hello`/`version` method before 1b starts.
- Slice 1a → 1b: the spec lags the code in three places (auth failures now return a generic "connection to server failed"; the socket path is `$SSH_MCP_RUNTIME_DIR` → `/run/user/<uid>` → `/tmp/ssh-mcp-<uid>`; the description is metadata and never executed). Amend the spec before writing plan 1b.
- Slice 1a → 2: in the UI door, `servers` and `term.open` still allow key-only servers while an encrypted vault is locked (the AI path refuses them). Decide whether the human path should match.

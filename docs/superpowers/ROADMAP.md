# ssh-mcp Roadmap

Long-lived plan across every slice. Each slice gets its own spec in
`specs/` and its own implementation plan in `plans/`, written when that
slice starts. This file only fixes order, gates, and cross-slice decisions.

Updated: 2026-09-25 (slice 2 specced)

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
| 1 | Desktop app: hub, broker, MCP door, Electron shell, terminal tabs, approval panel | 1a and 1b done; manual checklist pending | `specs/2026-09-24-desktop-app-design.md` | 0 | Spec approved | Success criteria 1–6 in the spec; author uses it daily |
| 2 | Terminal completeness: host CRUD in the app (delete `ssh-mcp web`), host-key fingerprint prompt, split panes, `~/.ssh/config` import, ProxyJump, Windows agent (OpenSSH pipe, Pageant), local shell via a hub-side PTY (`go-pty`; replaces `node-pty`) | Spec drafted; entry gate overridden by the author 2026-09-25 | `specs/2026-09-25-desktop-slice2-design.md` | 1 | Slice 1 used daily for two weeks; throughput criteria passed; stdio transport decision settled | Author no longer opens `ssh-mcp web` or another terminal for SSH work |
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

- Slice 1 → 2 (answered): base64-in-JSON stdio is fast enough; binary
  frames are not needed for now. Measured on macOS with
  `desktop/e2e/throughput.spec.ts`: 47–49 MB/s, 18 ms max frame gap
  (`checklists/slice1-manual.md` item 2).
- Slice 1 → 2 (answered): Claude Code's `MCP_TOOL_TIMEOUT` is a hard
  wall-clock limit, default 100000000 ms (~28 h); progress notifications do
  not extend it. A separate idle limit (`CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT`,
  30 min for stdio) is reset by progress. The 5-minute approval expiry is
  under both, so the bridge sends no progress. Documented in the README.
- Slice 1 → 2: `go-winio` v0.6.2 gets first-instance semantics from `NtCreateNamedPipeFile` with `FILE_CREATE` (`pipe.go:378-381`) rather than the flag. Windows runtime behaviour (DACL, SID checks) is still untested on a real Windows machine.
- Slice 1 → 4: which remote-host secrets actually appeared in command
  output during daily use; drives the redaction pattern list.
- Slice 1b → 2 (answered): CI now also runs on pushes to `feat/go-conversion`. Its first run found a real bug (su elevation could block forever on a silent shell, fixed in `278454b`) and a hung-poll flake in the smoke test (fixed in `10d1d5e`); the `desktop` job then passed, with 38 MB/s throughput on the runner.

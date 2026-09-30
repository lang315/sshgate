# sshgate Roadmap

Long-lived plan across every slice. Each slice gets its own spec in
`specs/` and its own implementation plan in `plans/`, written when that
slice starts. This file only fixes order, gates, and cross-slice decisions.

Updated: 2026-09-30 (renamed ssh-mcp → sshgate, own public repo lang315/sshgate; slice 2a closed, `sshgate web` removed; 2b split, 2b-1 import done; 3a done, exit gate met on buildpc; 3b done, exit gate met on buildpc; tunnel half-close fixed; per-host auto-allow; slice 4 split into 4a output redaction and 4b audit viewer, audit rotation deferred)

## Standing decisions

These hold across all slices. Changing one needs a spec revision, not a
plan note.

- One Go binary (`sshgate`) is the core: standalone `--host` mode, MCP
  bridge, and `hub`. The UI is a client of the hub, never the other way.
- Desktop UI is Electron + React + xterm.js. The renderer talks to Go only
  through one transport module.
- The MCP door is a Unix socket or named pipe with a same-user check. Never
  HTTP or SSE on localhost.
- Every AI command is approved by a human by default. The human may put one
  host on auto-allow (plain exec by default; root hosts and sudo-exec only
  when the human opts in per host) for a set time, or until turned off with a
  Resume after each unlock; it is visible while on, audited, and stoppable at
  any time (`specs/2026-09-28-auto-allow-design.md`).
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
| 2a | Host management in the app (create vault, host CRUD, Forget), host-key fingerprint prompt, no silent TOFU on any hub path, safe vault writes (`config.Update`) | Done 2026-09-26: exit gate waived by the author, `sshgate web` removed | `specs/2026-09-25-desktop-slice2a-design.md` | 1 | Author override (unlocking twice for app + web blocks daily use) | Author manages hosts only in the app for a week; then delete `sshgate web` |
| 2b-1 | `~/.ssh/config` import with `known_hosts` pins | Done 2026-09-26: the author's 3 real hosts imported pinned and opened with no Trust prompt (`TestLiveVaultConnect`); two needed an auth edit after import (an encrypted key held by ssh-agent, fixed in `2515af9`; one host that only takes a password) | `specs/2026-09-26-slice2b-ssh-config-import-design.md` | 2a | A real config to import | The author's hosts import pinned and open with no Trust prompt |
| 2b-2 | ProxyJump (one hop first) | Not specced | — | 2b-1 | The author has a real host behind a bastion | Bastion host connects and runs an approved AI command |
| 2c | Split panes, local shell, Windows agent (OpenSSH pipe, Pageant) | Not needed now (2026-09-26) | — | 2a | Daily use shows the need (panes, local shell); a Windows machine to test on (agent) | Author does not open another terminal for SSH work |
| 3a | SFTP file browser (list, upload/download incl. folders, mkdir, rename, recursive delete, Finder drop) | Done 2026-09-27 (PR #2); exit gate met on buildpc (Windows OpenSSH) by the opt-in live e2e `desktop/e2e/live-files.spec.ts` | `specs/2026-09-26-slice3a-sftp-design.md` | 2a | Slice 2a done | The author browses, uploads a folder, downloads it back, and deletes it on a real host |
| 3b | Port forwarding (local, remote, dynamic) | Done 2026-09-28 (PR #3); exit gate met on buildpc (Windows OpenSSH) by the opt-in live e2e `desktop/e2e/live-tunnels.spec.ts`: a local and a SOCKS5 tunnel carried the host's sshd banner, kept working while locked, and stopped | `specs/2026-09-27-slice3b-port-forwarding-design.md` | 3a | Slice 3a done | Tunnels usable from a saved host |
| 4a | Output redaction: a fixed pattern set masks private keys, passwords, secrets, credential headers, URL passwords and known tokens in AI exec output (approved and auto-allowed, and `--host` mode), with a note to the AI and per-kind counts in the audit record | In progress | `specs/2026-09-30-slice4a-output-redaction-design.md` | 1 | Met: the audit log shows the AI asked to `cat /etc/mwtn.env` (passwords) on `fviainboxes-db`, and auto-allow can send such output unreviewed | Corpus tests pass; the author runs `TestLiveRedact` on the real hosts with no tripwire hit and plausible counts (`/etc/mwtn.env` on `fviainboxes-db` yields at least 2 `password` masks) |
| 4b | Audit viewer in the app | Built 2026-09-30: Audit tab, `audit.read`, `audit.appended` (UI-door protocol 8); exit gate pending: on the author's real vault the tab shows the day's AI requests with outcomes, Auto filters to auto-allowed runs only, and a new request appears while the tab is open | `specs/2026-09-30-slice4b-audit-viewer-design.md` | 4a | Slice 4a done | The Audit tab shows the day's AI requests with outcomes on the real vault; the Auto filter shows only auto-allowed runs; a new request appears without a refresh |
| 5 | Distribution: code signing, notarization, auto-update, installers, CI release builds | Not needed now (2026-09-26) | — | 2a | A second user asks for a build | Signed builds for all three OSes from CI |
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
- Slice 1b → 2 (answered): CI now also runs on pushes to `feat/go-conversion`. Its first runs found two real bugs, both fixed: su elevation could block forever on a silent shell (`0fb3fac`), and keepalive never reported a connection that died before its first tick (`d0e2494`). They also found a hung-poll flake in the smoke test (`34fcb48`). Both jobs, Docker integration tests included, pass as of run 36083544252; throughput on the runner was 38 MB/s.
- Slice 2 review → 2b-2 (2026-09-25, four-agent review of the bundled draft):
  - Answered by 2b-1: `Host *`, `Include`, alias as hostname, keep-or-refuse `ProxyJump`, name validation and dedupe, `known_hosts` fingerprints. The jump-chain findings below stay for 2b-2.
  - A tunnelled dial has no handshake timeout. `ClientConfig.Timeout` covers only the TCP dial, and `ensure` holds `m.mu`. Bound `DialContext` + `NewClientConn` and give the whole chain one budget.
  - `redactorFor` must walk the bastion's secrets.
  - Changing `jump` must clear the pin.
  - Start with one hop.
  - The import must honour `Host *` defaults and `Include` (OrbStack/Colima add one) and use the alias as the hostname when there is no `HostName`.
  - The import must keep `ProxyJump` or refuse the host rather than dial it directly.
  - The import must validate names and dedupe.
  - The import must offer `known_hosts` fingerprints instead of blind Trust prompts.
- Slice 2 review → 2c:
  - Never bind Ctrl+D, Ctrl+W, or bare Ctrl+Alt+Arrow for panes on Windows/Linux; `checklists/slice1-manual.md` item 9 needs Ctrl+W in the shell.
  - The go-pty `LocalTerm` must be closed on process exit, or `newTerm`'s read-to-EOF never ends.
  - Local-shell input must not count as idle activity.
  - "Send to tab" must never target a local shell.
  - A local shell while locked widens what a compromised renderer can do; justify it or refuse it.
  - `go-pageant` has had no release since 2021.
- Slice 3a review → later (2026-09-26, four-agent review of the SFTP spec): open the Files tab at a terminal's current directory (needs OSC 7 from the shell); download by double-click, drag-out to Finder, copy path; sudo/root file access. Add them only if daily use hits them.
- Slice 2 review → later: keyboard-interactive/2FA auth, agent forwarding, and host list search came up as daily-use gaps. Add them only if daily use hits them.
- Slice 2a → follow-ups (security minors from the 2a reviews) — done:
  - A KDF-less store (stripped `kdf`, or never a vault) is "no vault" to the hub: `listServers` is empty, AI exec fails with "No vault yet; open the app and create one" (audited, never dialled), and `term.open` fails with "create a vault first". No out-of-file state was needed (`35c330b`).
  - The hub remembers the last reload error; `status` returns it as `storeError` (fixed text), the app shows a red banner, and `servers.*` writes return a failed post-write reload (`f0d9363`).
  - A vault file deleted while the hub runs is a `storeError` too (same fixed text); the hub keeps serving the last good copy, writes fail closed, and restoring the file clears it. A missing file with no vault ever loaded stays "no vault". `vault.create` is refused meanwhile ("a vault already exists"), so the in-memory vault cannot be replaced.
  - A trusted `term.open` whose post-pin reload fails still opens (the pin is written, the key verified) and sets `storeError`; pinned by a test.
  - The web UI's stale-pin and `If-Match` guards (`f65f583`) went away with the web UI (removed 2026-09-26).
  - `vault.create` is asserted to derive the key once and never run Unlock's derivation (`newKDF`/`deriveKey` seams, `5fb34f3`).
- Slice 2a → later (2026-09-26, web UI removed): `hub --cli` cannot create a vault or edit hosts, and nothing imports or exports servers any more. Headless machines get a vault by copying `servers.json` from the app's machine. Add a CLI path or import/export only if that copy stops being enough.
- Slice 3b → later (2026-09-27): auto-start tunnels on unlock or app start, LAN sharing (binding beyond loopback), reconnect after the connection drops, and a remote dynamic forward. Add them only if daily use hits them.
- Slice 3b, half-close → fixed 2026-09-28: tunnels now half-close (a direction's clean EOF calls `CloseWrite` on its destination so the other direction can still carry a reply); a direction's error, or a destination with no `CloseWrite`, still closes both ends.
- Local macOS bundle → 5 (2026-09-28, review of `desktop/scripts/package-mac.sh`): the bundle is stock Electron, ad-hoc signed without the hardened runtime, and its fuses are unchanged (RunAsNode, `NODE_OPTIONS`, `--inspect`, `--remote-debugging-port` all allowed). A process running as the same user can relaunch the app with a debugger port and, after the user unlocks, call the UI door (`decide`) to approve its own AI requests. The same process can also overwrite the user-writable `/Applications/sshgate.app` or use `~/.ssh` and the ssh-agent directly, so closing this needs slice 5's signing, notarization, hardened runtime, and flipped fuses (`@electron/fuses`), not a patch. Already done: the bundle ignores every `SSHGATE_*` knob (`hubLaunch`), so `open --env SSHGATE_BIN=… -a sshgate` cannot swap the hub; running `electron <the bundle's app dir>` still honours them.
- Auto-allow → 4 (2026-09-28): a host on auto-allow sends its command output to the AI unreviewed; putting a host with secrets on auto-allow meets slice 4's entry gate.
- Slice 4 split (2026-09-30): audit log rotation is deferred until the log's size matters (the real log is 14 KB); its trigger is a whole-file read that shows in the audit viewer. User-defined patterns, an "Allow unredacted" bypass or per-host opt-out, and entropy-based detection are out of 4a (`specs/2026-09-30-slice4a-output-redaction-design.md`). When 4a's exit gate is met, its row becomes "Done <date of the live check>".
- 4b → later (2026-09-30): audit rotation stays deferred. Trigger: the audit file is large enough that `audit.read`'s whole-file scan shows in the Audit tab; rotation then comes with a backwards reader from EOF (the `ponytail:` note on `broker.Audit.Read`). Not planned: export (the file is already JSONL, and the tab's footer shows its path), viewing while locked, and MCP-door access (the AI never reads the audit log).

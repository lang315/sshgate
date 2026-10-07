# sshgate: `exec` takes an optional stdin — Design

Date: 2026-10-07
Status: Approved in chat 2026-10-07 (sections 1–3), option 1 of three.

## Goal

Let Claude Code and other AI coding agents edit a project that lives on a remote host through sshgate, keeping approval, grants, audit and redaction as they are.

Why: today an agent can read files with `exec` (`sed -n`, `grep`), but cannot write a multi-line file. `SanitizeCommand` rejects every control character, `\n` included (`internal/config/sanitize.go:14-19`), so a heredoc is impossible and a write has to be base64 or `printf` escapes, which a human approver cannot read.

The change: `exec` gains an optional `stdin` string, piped to the command's standard input. An agent edits with `exec {command: "git apply", stdin: "<unified diff>"}` and creates files with `exec {command: "cat > path", stdin: "..."}`. The approver sees the diff itself in the approval card, with no diff-rendering code.

In practice a coding loop runs under an auto-allow grant: one click per command does not keep up (cap 5 pending, 5-minute expiry, `internal/broker/broker.go:76-80`). A stdin exec is an `exec`, so it runs under the existing grant with no new auto-approval rule.

Exit gate (manual, on a real host): with a grant on the host, an agent driving the real MCP bridge creates a file with `cat > f` and edits it with `git apply`, each call recorded in `audit.jsonl` with `approval: "auto"` and its `stdin`. Without a grant the approval card shows the stdin block and Send to tab is disabled.

## Rejected alternatives (evaluated 2026-10-07)

- A JetBrains-Gateway-like "Remote Development" screen: targets a human IDE, not an agent; Gateway is being folded into Toolbox; an IDE backend's own AI runs outside the broker.
- `sshgate proxy <server>` (ProxyCommand): a ProxyCommand cannot reuse vault credentials, and any same-UID process (the agent's own shell tool) could use it to get an ungated shell.
- MCP file tools (`read-file`, `edit-file`, `write-file` over SFTP): deferred until stdin exec proves too slow in real use. If built, they must refuse files where any redaction hit, redact the whole file before slicing, refuse su hosts, and need a spec change to run under a grant.
- Local mirror of the remote project: the agent's native Read would see raw `.env` files.
- Running the agent on the host (Claude Desktop SSH sessions, `claude` in a Remote-SSH terminal): no code, but no gating or audit. Documented as the baseline for dev boxes, not built.

## Scope

In:
- `stdin` on the `exec` MCP tool, in both the bridge and standalone `--host` mode.
- Validation, early refusals, broker and audit fields, approval card, CLI approver, auto-allow feed.
- Vault-secret masking (`***`) counted as kind `secret`.
- UI-door protocol 11.

Out:
- `stdin` on `sudo-exec`: `sudo -S` already uses stdin for the password.
- Hosts with a su password: plain exec runs inside a persistent su shell that frames commands over its own stdin (`internal/sshx/manager.go:421-441`).
- Streaming or binary stdin. Stdin is a UTF-8 string.
- MCP-door protocol changes: the bridge and the hub ship as one binary.

## Behaviour

### MCP tool

`exec` gains `stdin` (string, optional, `omitempty`). The tool description adds:

- stdin is piped to the command; use `git apply` or `patch -p1` to edit files and `cat > path` to create them;
- at most 256 KiB; `\n`, `\t`, `\r` allowed, other control and format characters rejected;
- not supported by `sudo-exec` or on servers configured with a su password;
- never write back output that contained masked values (`***` or `[REDACTED:…]`): the file would be corrupted.

`sudo-exec`'s schema is unchanged.

### Hub path

The bridge forwards `stdin` in the MCP door's `exec` params without checking it. `execParams` (`internal/hub/mcpdoor.go:16`) and `ExecRequest` (`internal/hub/hub.go:89`) gain `Stdin`.

`Hub.Exec` keeps its order (CLAUDE.md, "`Hub.Exec` order"). The step "sanitize command, validate description" adds `config.ValidateStdin`:

- allowed: any rune `forbiddenRune` allows, plus `\n`, `\t`, `\r`;
- rejected: every other control character, every `unicode.Cf` rune, more than 256 KiB, and the substring `[REDACTED:`.

The `[REDACTED:` refusal stops an agent from writing back a file it read through pattern masking. It cannot catch `***` (legitimate text); counting `***` (below) tells the agent instead.

Right after validation, before `autoStart` and before `broker.Submit`:

- `Sudo && Stdin != ""` fails with `stdin is not supported with sudo-exec`;
- `dc.SuPassword != ""` with stdin fails with `stdin is not supported on this server`.

Like the existing validation errors, these refusals are not audited and never take a pending slot.

`broker.Request` gains `Stdin` (JSON `stdin,omitempty`). A grant runs a stdin exec on the auto path exactly as it runs a plain exec.

`decide` with `sent_to_tab` on a request whose `Stdin` is non-empty is refused with the error `send to tab is not available for a command with stdin`: pasting the command alone would run a different command.

### SSH layer

`Executor` and `sshx.Manager` gain `ExecStdin(ctx, cmd, stdin string) (ExecResult, error)`: `OpenSession`, then `runOnce(ctx, sess, cmd, stdin)` (`internal/sshx/manager.go:162`), which already pipes a non-empty stdin. It returns an error when `SuPassword` is set, as a second guard. `Hub.run` calls it when stdin is non-empty and `Exec` otherwise.

### Redaction

`Redactor.Redact` replaces vault secrets with `***` (`internal/config/redact.go:17-21`) and nothing counts it. `RedactCapStreams` now counts those replacements as kind `secret`, so the AI's `note:` line and the audit record's `redacted` report them. The redaction corpus goldens are regenerated with `-update` and every changed `# counts:` line reviewed.

### Approval card (`ApprovalPanel.tsx`)

- Below the command, a `<pre className="stdin">` labelled `stdin · N bytes, M lines`, at most about 40% of the viewport high, scrollable.
- The same `highlightNonAscii` marking as the command; `\r` shown as `␍`.
- Send to tab is disabled when the request has stdin.
- The 500 ms Allow delay already restarts when the card's height changes (`ListChanges`, `ResizeObserver`); nothing new.

### CLI approver (`internal/hub/cliui.go`)

Prints `stdin (N bytes):`, then each line prefixed `| `, `\r` shown as `␍`. The `s` command reports the same error as the hub's `sent_to_tab` refusal.

### Auto-allow feed

`autoAllow.ran` gains `stdinBytes` (a count, never content). The Auto-allowed card shows a `stdin N B` tag. The content is in the Audit tab.

### Audit

`AuditRecord.Stdin` (JSON `stdin,omitempty`) is `red.Redact(stdin)`, the same masking as `command`, at most 256 KiB (validation already bounds it). The no-vault path caps it at 4096 bytes like the other AI-supplied fields. `AuditTable` shows it in the expanded row in a `<pre>` through `displayText`. `audit.read`'s `text` search and the renderer's `matches` already walk every string value, so they cover `stdin` unchanged.

### Standalone `--host` mode

`runExec` (`internal/mcpserver/tools.go:27`) applies `ValidateStdin` and the same sudo and su refusals, then calls `ExecStdin`. The description is still appended to the command as a shell comment; stdin is separate.

### Protocol

UI-door `ProtocolVersion` 10 → 11 (`internal/hub/idle.go`). Required: a protocol-10 renderer would ignore `stdin` and let the approver allow a request without seeing it; `hello` now refuses that app. `shared/protocol.ts`: `PendingRequest.stdin?: string`, auto-run `stdinBytes?: number`.

## Testing

The in-process SSH server (`internal/sshx/sshtest`) ignores exec stdin today and writes the command back. It gains one sentinel: an exec of `stdin-echo` writes its stdin instead. Every other command behaves as before.

Go (`go test -race ./...`):

- `internal/config`: `TestValidateStdin` table (`\n`, `\t`, `\r`, empty allowed; ESC, U+202E, U+200B, 256 KiB + 1, `[REDACTED:` rejected); `***` counted as `secret`; corpus goldens regenerated.
- `internal/sshx`: `ExecStdin` against `stdin-echo`; refused with `SuPassword`; a Docker test piping stdin to `cat` on real OpenSSH (skips without Docker).
- `internal/hub`: stdin reaches the executor on the approved and the auto path; the `pending` event carries it; sudo with stdin and a su host are refused before the broker with no pending entry and no audit; `sent_to_tab` refused with stdin; the audit record carries `stdin` with a vault secret as `***`; `ProtocolVersion == 11`.
- `cliui_test.go`: the stdin block with `| ` and `␍`; `s` refused.
- `internal/mcpserver`: the bridge forwards `stdin`; standalone `runExec` with stdin, and its sudo and su refusals.
- `cmd/sshgate/e2e_test.go`: bridge → hub → in-process sshd with a stdin exec, checking the returned stdout.

Desktop (`npm run typecheck && npm test && npm run e2e`):

- vitest: `ApprovalPanel` shows the stdin block with byte and line counts and `␍`, and disables Send to tab; the auto-run card shows the `stdin` tag.
- Playwright: `autoallow-mcp.spec.ts` adds a stdin exec under a grant through the real MCP bridge and checks its audit record (`stdin`, `approval: "auto"`); one step in an existing spec shows a pending request's stdin block with Send to tab disabled.

Docs: CLAUDE.md (`ExecRequest`, `Hub.Exec` order, protocol 11, the `***` count) and README (editing files with `git apply` and stdin).

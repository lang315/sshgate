# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`ssh-mcp`: a single Go binary, three modes. `ssh-mcp --host=...` is a standalone MCP stdio server exposing `exec`, `sudo-exec`, and `list-servers` over one CLI-configured connection; it never touches the on-disk vault. Without `--host`, `ssh-mcp` is a stateless bridge that forwards those same three tools over stdio to `ssh-mcp hub`, a separate process that owns the vault, the SSH connections, and an approval `broker` that blocks every AI-issued command on a human decision; `ssh-mcp hub --cli` is a terminal stand-in for that approval UI until a real desktop app exists. `ssh-mcp web` serves a localhost config UI for managing saved connections, shared by all modes. The project was ported from TypeScript; the TS code is gone (`eb25b3e`).

## Commands

```bash
go build -o ssh-mcp ./cmd/ssh-mcp        # binary is gitignored
go test -short ./...                     # unit tests only; skips SSH integration tests
go test ./...                            # also runs internal/sshx integration tests (needs Docker, via testcontainers)
go test -race ./...                      # same, with the race detector; what CI runs
go test ./internal/sshx -run TestExecEcho -v   # single test
go vet ./...
go run ./cmd/ssh-mcp hub --cli           # terminal approver; the bridge (no --host) needs a hub running to do anything
docker compose up                        # local openssh-server (test/secret on :2222) + ssh-mcp against it
```

If Docker is unavailable, integration tests skip themselves instead of failing. They start `lscr.io/linuxserver/openssh-server` and connect with `Insecure: true`. `cmd/ssh-mcp/e2e_test.go` is the exception: it drives a real bridge → hub → sshd approval flow but fakes the SSH server in-process (`internal/sshx/sshtest`), so it has no `testing.Short()` guard and needs no Docker.

## Architecture

`cmd/ssh-mcp/main.go` routes on the first argument: `web` goes to `runWeb` (`web.go`), `hub` goes to `runHub` (`hub.go`), and anything else starts `runMCP` — standalone with `--host`, or a bridge to the hub without it. All three share the same store at `~/.config/ssh-mcp/servers.json`.

**Hub and bridge** (`internal/hub`, `internal/broker`, `internal/rpc`, `internal/mcpserver/bridge.go`):
- `ssh-mcp hub` owns the unlocked vault, the `sshx.Registry`, and the approval `broker`. `ServeUIDoor` serves a full-privilege JSON-RPC "UI door" on stdio (`status`, `unlock`, `lock`, `servers`, `pending`, `decide`, `denyAll`, `term.*`) for a future desktop app; `hub --cli` instead runs `RunCLIApprover`, a separate plain-text terminal protocol over the same `Hub`, not JSON-RPC. `ServeMCPDoor` serves a restricted "MCP door" on a per-user Unix socket / named pipe (`SocketPath`: `$SSH_MCP_RUNTIME_DIR/ssh-mcp`, else `/run/user/<uid>/ssh-mcp` if owned by us, else `/tmp/ssh-mcp-<uid>`; never `TMPDIR`/`XDG_RUNTIME_DIR`, and tests isolate via `SSH_MCP_RUNTIME_DIR`), peer-checked both directions (UID on Unix, SID on Windows): only `listServers`/`exec`/`sudoExec` (request-only, so a long approval wait can't stall the read loop) and `cancel` (a notification, so it can't get stuck behind one) are registered — nothing there can unlock, decide, or read secrets.
- Without `--host`, `ssh-mcp` is a stateless bridge (`BuildBridgeServer`): MCP stdio in, one fresh MCP-door connection per tool call. It validates nothing and holds no secrets; the hub is the single policy point. `server` has no default here — empty fails the same as an unknown name.
- Every MCP exec goes through `broker.Submit` and blocks for a human decision (cap 5 pending, 5-minute expiry). `OnEvent` runs synchronously on the caller's goroutine with the broker's lock released, and `Decide` blocks until the matching "pending" event has gone out — a handler that calls `Decide` from inside its own "pending" callback deadlocks. No auto-approval rules exist; do not add any without a spec change.
- `Hub.Exec` order: visible → unlocked → host key pinned → sanitize command, validate description (control/format runes rejected, 500-byte description cap, no `--maxChars` equivalent; the description is metadata for the approver and audit and is never appended to the command) → `broker.Submit` blocks for approval → re-check ctx and re-verify the server (state can change during the wait) → run with a per-request timeout → redact and cap each stream → audit every branch, including denied/expired/cancelled. Hidden and nonexistent servers return byte-identical errors. SSH and resolve failures reach the AI only as generic text (`ErrHostKeyFailed`, `ErrConnFailed`; timeouts, `sshx.ErrTimeout`, pass through redacted); the detail goes to the audit reason and stderr. The bridge lays the already-processed result out as `exit code:`/`stdout:`/`stderr:` (`layoutExec`) without reprocessing it.

**mcpserver.Deps holds one connection source at a time** (`internal/mcpserver/resolve.go`):
- Standalone `--host` mode: `buildDeps` builds `Deps.CLI` from `config.ParseArgv`'s `--key=value` flags (`--host`, `--user`, `--password`, `--key`, `--sudoPassword`, `--suPassword`, ...) as the `"(default)"` server; it never reads the on-disk store (`Deps.File` stays nil).
- `ssh-mcp hub` builds `Deps.File` from the on-disk store and never sets `Deps.CLI`; there's no `"(default)"` server through the hub.
- `Deps.Resolve(name)` still has both branches (shared code) and turns either source into a `sshx.DialConfig`, decrypting secrets on demand. Without a master key, encrypted servers are "locked" and fail with a clear error. Key/agent-only servers still resolve there, but the hub refuses every AI exec while an encrypted vault (one with a KDF) is locked — its MAC is unchecked until unlock — and `listServers` then reports every server locked.

**Store crypto** (`internal/config/crypto.go`, `store.go`):
- Argon2id derives the master key, and the KDF params plus a verifier blob are stored in the file.
- Each secret field uses AES-GCM with an HKDF subkey labelled `"<serverName>/<field>"`. Its AAD binds version, name, field, host, port, user, and auth (`AADFor`). Renaming a server or changing host/port/user/auth therefore invalidates its ciphertexts, and the web handlers must re-encrypt (`preserveSecrets`).
- A whole-file HMAC (`MAC`) is checked on load when a master key is present. `Save` bumps `Revision`, recomputes the MAC, and writes atomically (temp file, then rename, mode 0600).
- Writers serialize through `withFlock`. The helper is duplicated in `internal/config` and `internal/web`, with a unix and a non-unix variant of each.
- A corrupt or unreadable store is a hard error in both modes and is never overwritten.
- The master key is never read from an environment variable or file (`SSH_MCP_MASTER_PASSWORD_FILE` is gone): `ssh-mcp hub` gets it only from an explicit `unlock` call (the UI door's `unlock` method, or the CLI approver's `u` command). `--host` mode never touches the vault at all.

**SSH layer** (`internal/sshx`):
- `Registry` caches one `Manager` per server name, keyed by a hash of the `DialConfig` (secrets and `HostKey` included, callbacks excluded). A changed config closes the old connection and dials fresh. The comparison uses the manager's current pin, so a manager that just learned a key by TOFU is kept when the store comes back with that same pin.
- `Manager` holds one lazily dialed `ssh.Client` behind a mutex. When `SuPassword` is set, plain `Exec` runs inside a persistent `su` root shell, framing each command with a random nonce marker (`FrameSuCommand`). A wrong su password must fail closed, never falling back to an unprivileged shell. `ExecSudo` wraps commands with `sudo -S` or `sudo -n` (`wrap.go`).
- Host keys use TOFU (`hostkey.go`). An empty pin records the fingerprint back to the store through `OnLearnHostKey` and `config.RecordHostKey`, which only writes if the server has no pin yet. The `Manager` also keeps the learned key as its own pin, so every redial verifies it. A mismatch hard-fails. `--insecureIgnoreHostKey` skips checks and warns on stderr. The hub's approval path refuses an AI-visible server with no pin (`ErrNoHostKey`) rather than learning one on the fly; today only `term.open` (UI door) and the web UI's `/api/test-connection` handler actually dial and learn a key — neither is reachable without a client for them yet, so pinning a new server means pasting its fingerprint into the web form by hand.

**MCP tools** (`internal/mcpserver/tools.go`): the `--host`-mode tool set. `runExec` validates the command (`SanitizeCommand` rejects control characters and enforces `--maxChars`), appends the description as a shell comment, resolves the server, and runs it. Output and errors pass through `config.Redactor`, which masks every secret in the `DialConfig`. Keep that redaction on every path that returns text.

**Web UI** (`internal/web`, static assets embedded from `static/`):
- The server binds `127.0.0.1:8422` only. `securityMiddleware` enforces a Host allowlist and sets CSP and no-store headers.
- Mutating endpoints go through `writeGuard`, which checks Origin, requires JSON content-type, and compares the `X-CSRF-Token` header against the session.
- On first run, a bootstrap token printed to stderr is needed to set the master password. Unlock has attempt-based lockout.
- `testconn.go` returns deliberately generic failure messages so SSH error details do not leak.

## Conventions

- Security posture is intentional (fail closed, generic errors, redaction, no secret persistence outside the encrypted store). Recent commits (`fix: refuse corrupt config store...`, TOFU host keys) show the expected bar. Add a test when you change any of these paths.
- Design spec and implementation plan for the Go port are in `docs/superpowers/specs/` and `docs/superpowers/plans/`.

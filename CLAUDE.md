# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`ssh-mcp`: a single Go binary (MCP stdio server) that exposes `exec`, `sudo-exec`, and `list-servers` tools for running shell commands over SSH. A second mode, `ssh-mcp web`, serves a localhost config UI for managing saved connections. The project was ported from TypeScript; the TS code is gone (`eb25b3e`), but `.github/workflows/ci.yml` and `publish.yml` still run `npm`, so they are stale and do not test the Go code.

## Commands

```bash
go build -o ssh-mcp ./cmd/ssh-mcp        # binary is gitignored
go test -short ./...                     # unit tests only; skips SSH integration tests
go test ./...                            # also runs internal/sshx integration tests (needs Docker, via testcontainers)
go test ./internal/sshx -run TestExecEcho -v   # single test
go vet ./...
docker compose up                        # local openssh-server (test/secret on :2222) + ssh-mcp against it
```

If Docker is unavailable, integration tests skip themselves instead of failing. They start `lscr.io/linuxserver/openssh-server` and connect with `Insecure: true`.

## Architecture

`cmd/ssh-mcp/main.go` routes on the first argument: `web` goes to `runWeb` (`web.go`), and anything else starts the MCP server. Both modes use the same store at `~/.config/ssh-mcp/servers.json`.

**Two connection sources, merged in `mcpserver.Deps`** (`internal/mcpserver/resolve.go`):
- CLI flags (`--host`, `--user`, `--password`, `--key`, `--sudoPassword`, `--suPassword`, ...) form the `"(default)"` server. They are parsed by `config.ParseArgv` using `--key=value` syntax.
- The on-disk store (`config.File`) holds named servers. Tools pick one with the `server` argument, and an empty `server` means `(default)`.
- `Deps.Resolve(name)` turns either source into a `sshx.DialConfig`, decrypting secrets on demand. Without a master key, encrypted servers are "locked" and fail with a clear error. Key/agent-only servers still work.

**Store crypto** (`internal/config/crypto.go`, `store.go`):
- Argon2id derives the master key, and the KDF params plus a verifier blob are stored in the file.
- Each secret field uses AES-GCM with an HKDF subkey labelled `"<serverName>/<field>"`. Its AAD binds version, name, field, host, port, user, and auth (`AADFor`). Renaming a server or changing host/port/user/auth therefore invalidates its ciphertexts, and the web handlers must re-encrypt (`preserveSecrets`).
- A whole-file HMAC (`MAC`) is checked on load when a master key is present. `Save` bumps `Revision`, recomputes the MAC, and writes atomically (temp file, then rename, mode 0600).
- Writers serialize through `withFlock`. The helper is duplicated in `internal/config` and `internal/web`, with a unix and a non-unix variant of each.
- A corrupt or unreadable store is a hard error in both modes and is never overwritten.
- Headless MCP gets the master password only from `SSH_MCP_MASTER_PASSWORD_FILE`, and the file must have no group or other permissions.

**SSH layer** (`internal/sshx`):
- `Registry` caches one `Manager` per server name, keyed by a hash of the full `DialConfig`. A changed config closes the old connection and dials fresh.
- `Manager` holds one lazily dialed `ssh.Client` behind a mutex. When `SuPassword` is set, plain `Exec` runs inside a persistent `su` root shell, framing each command with a random nonce marker (`FrameSuCommand`). A wrong su password must fail closed, never falling back to an unprivileged shell. `ExecSudo` wraps commands with `sudo -S` or `sudo -n` (`wrap.go`).
- Host keys use TOFU (`hostkey.go`). An empty pin records the fingerprint back to the store through `OnLearnHostKey` and `config.RecordHostKey`, which only writes if the server has no pin yet. A mismatch hard-fails. `--insecureIgnoreHostKey` skips checks and warns on stderr.

**MCP tools** (`internal/mcpserver/tools.go`): `runExec` validates the command (`SanitizeCommand` rejects control characters and enforces `--maxChars`), appends the description as a shell comment, resolves the server, and runs it. Output and errors pass through `config.Redactor`, which masks every secret in the `DialConfig`. Keep that redaction on every path that returns text.

**Web UI** (`internal/web`, static assets embedded from `static/`):
- The server binds `127.0.0.1:8422` only. `securityMiddleware` enforces a Host allowlist and sets CSP and no-store headers.
- Mutating endpoints go through `writeGuard`, which checks Origin, requires JSON content-type, and compares the `X-CSRF-Token` header against the session.
- On first run, a bootstrap token printed to stderr is needed to set the master password. Unlock has attempt-based lockout.
- `testconn.go` returns deliberately generic failure messages so SSH error details do not leak.

## Conventions

- Security posture is intentional (fail closed, generic errors, redaction, no secret persistence outside the encrypted store). Recent commits (`fix: refuse corrupt config store...`, TOFU host keys) show the expected bar. Add a test when you change any of these paths.
- Design spec and implementation plan for the Go port are in `docs/superpowers/specs/` and `docs/superpowers/plans/`.

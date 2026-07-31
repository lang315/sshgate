# ssh-mcp: Go Conversion + Web Config UI — Design

Date: 2026-07-31
Status: Approved (revised after multi-agent security/architecture/scope review)

## Goal

Convert the TypeScript ssh-mcp MCP server to Go, and add a localhost web UI for
full connection management: create, edit, delete, import, and export SSH
connections. The existing single-host CLI mode must keep working unchanged.

## Decisions

| Topic | Decision |
|---|---|
| Server selection | MCP tools take a `server` (connection name) parameter; new `list-servers` tool |
| Import sources | `~/.ssh/config` and JSON export/import (paste-ssh-command dropped) |
| Storage | JSON file, secret fields encrypted at rest |
| Key management | Master password → argon2id → AES-256-GCM; master password read from `SSH_MCP_MASTER_PASSWORD_FILE` (0600) or TTY prompt, **never** from argv |
| UI stack | Vanilla HTML/JS embedded via `go:embed`, no Node build |
| CLI compat | All existing flags kept; CLI host acts as implicit default server |
| MCP transport | stdio only |
| Repo layout | Replace TS in-place; delete TS only after Go tests pass |
| Process model | One binary, two subcommands: `ssh-mcp` (MCP stdio) and `ssh-mcp web` (config UI) |
| Per-server overrides | Dropped — `timeout`, `maxChars`, `disableSudo` are global flags only |

## Threat Model

Encryption at rest protects against **file-level exposure**: `servers.json`
copied to a backup, synced to a dotfiles repo, pulled from a stolen/lost disk,
or pasted into a chat. It does **not** protect against an attacker with
same-uid code execution (they can read the master-password file, the SSH keys,
and process memory) — that is out of scope, same as `~/.ssh`.

The localhost web UI treats **hostile web pages in the operator's browser** as
in-scope adversaries (CSRF, DNS rebinding) and defends against them explicitly.
Other local users are in-scope for file permissions (0600/0700) but loopback
traffic is not confidential to root/BPF — accepted, hence no TLS.

Secrets must never leak into the LLM transcript or logs (redaction, §Secret
Redaction).

## Architecture

One Go module `github.com/lang315/ssh-mcp`, one binary:

- `ssh-mcp [flags]` — MCP server on stdio (default mode, same as today)
- `ssh-mcp web [--port 8422]` — config UI on `127.0.0.1:8422`

Dependencies: official `github.com/modelcontextprotocol/go-sdk` (verified:
v1.7.0, `mcp.AddTool[In,Out]` + `mcp.StdioTransport`; note pre-1.0-style
API churn, pin the version), `golang.org/x/crypto` (ssh, ssh/agent,
ssh/knownhosts, argon2, hkdf). Web server is stdlib `net/http` with static
assets embedded via `go:embed`. Test-only: `testcontainers-go` (not a runtime
dep). No other runtime dependencies.

Layout:

```
cmd/ssh-mcp/main.go       entry point, subcommand dispatch, flag parsing
internal/config/          store: load/save servers.json, atomic writes, crypto, MAC
internal/sshx/            connection manager: persistent conn, su elevation, sudo wrap
internal/mcpserver/       MCP tools: exec, sudo-exec, list-servers
internal/web/             HTTP server, REST API, import parsers
internal/web/static/      index.html, app.js, style.css (embedded — must live under the package dir; go:embed cannot escape via ..)
```

Both modes read the same config store. The web UI is the writer; MCP processes
reload when the file's `revision` counter changes (§Reload). No daemon, no port
conflicts when multiple Claude sessions each spawn an MCP process.

## Data Model

`~/.config/ssh-mcp/servers.json`, dir mode 0700, file mode 0600, written
atomically (temp file created 0600 → write → fsync → rename in same dir):

```json
{
  "version": 1,
  "revision": 7,
  "kdf": { "alg": "argon2id", "v": 1, "salt": "…", "time": 3, "memoryKiB": 65536, "parallelism": 4, "keyLen": 32, "verifier": "…" },
  "mac": "base64(HMAC-SHA256 over canonicalized file minus mac field)",
  "servers": [
    {
      "name": "prod-1",
      "host": "1.2.3.4",
      "port": 22,
      "user": "root",
      "auth": "password",
      "keyPath": "~/.ssh/id_ed25519",
      "hostKey": "sha256:… (pinned fingerprint, TOFU on first connect)",
      "encPassword": "base64(nonce+ciphertext)",
      "encSuPassword": "…",
      "encSudoPassword": "…",
      "encKeyPassphrase": "…"
    }
  ]
}
```

- `auth` is one of `password | key | agent`.
- Only secret fields are encrypted (`encPassword`, `encSuPassword`,
  `encSudoPassword`, `encKeyPassphrase`). Everything else is plaintext JSON so
  the file stays greppable.
- **KDF**: master password → argon2id with all params read from `kdf` (nothing
  hardcoded), salt 16 bytes from `crypto/rand`. Params: `time=3, memoryKiB=65536,
  parallelism=4, keyLen=32` (RFC 9106 second profile). Refuse to load unknown
  `alg`/`v`. Per-record subkeys via `HKDF(masterKey, name||field)` so one nonce
  collision is scoped.
- **Encryption**: AES-256-GCM. `Encrypt` draws 12 fresh random bytes on **every**
  call — nonces are never stored in a reusable struct nor counter-derived.
  Unchanged fields are copied as opaque base64, never re-encrypted.
- **AAD binding**: each field's GCM AAD is a canonical string over
  `version|name|field|host|port|user|auth`. Editing `host`/`user`/etc. without
  the master password makes decryption fail — blocks the "swap host, keep
  ciphertext" attack.
- **File MAC**: HMAC-SHA256 (key = `HKDF(masterKey, "file-mac")`) over the
  canonicalized file minus the `mac` field. Load refuses on mismatch; blocks
  offline tampering and rollback of unencrypted fields. (For key/agent-only
  vaults with no master password set, the MAC is omitted and tamper protection
  relies on file perms — documented.)
- `kdf.verifier` is a GCM-encrypted known constant to check the entered master
  password.
- **Host key**: `hostKey` pins the server's SSH host key fingerprint. First
  connect uses TOFU (web UI shows the fingerprint to confirm; MCP mode records
  it and errors on later change). See §SSH Host Key Verification.
- Server names unique, validated `^[A-Za-z0-9._-]{1,64}$`.

## MCP Tools

- `exec(server, command, description?)` — run command on the named server.
- `sudo-exec(server, command, description?)` — sudo variant; the tool is hidden
  entirely when the global `--disableSudo` flag is set (as today).
- `list-servers()` — returns name, host, port, user, auth type. Never secrets.

Behavior ported from TS, with the fixes below:

- `description` appended as ` # comment`. Reject `\n`/`\r`/NUL in both `command`
  and `description` (comment-newline injection breaks audit integrity and the
  su path); cap `description` length.
- Command sanitization: trim, reject empty, enforce global `maxChars`
  (default 1000; `0`/negative/`"none"` disables). Range-validate.
- sudo **without** password: `sudo -n sh -c '<cmd>'` (single quotes escaped).
- sudo **with** password: send the password on the SSH session's **stdin**
  (`session.Stdin = pw+"\n"`) with `sudo -k -S -p '' sh -c '<cmd>'` — `-k` forces
  a prompt so sudo always consumes the stdin line; wrap inner as
  `exec <cmd> </dev/null` so a stdin-reading command can't capture the password.
  **Never put the password in the command string** (avoids remote `ps`/audit
  exposure and stdin-passthrough leak into tool output).
- su elevation (when suPassword set): open PTY shell with `LANG=C LC_ALL=C`,
  send `su -`, wait for a real password prompt (echo-off), send password, then
  set a random sentinel prompt (`PS1='__SSHMCP_<nonce>__'`). Do **not** scrape
  for bare `#`. Every subsequent command is framed `<cmd>; echo <nonce>:$?` and
  read until the exact nonce — this recovers the exit code the TS su path loses
  and prevents `#`-in-output truncation. **Elevation failure fails closed**
  (return an error; never silently fall back to unprivileged execution).
- Timeouts: dial+handshake via `ssh.ClientConfig.Timeout` (30s); command
  timeout hand-rolled (goroutine + `select`, `session.Close()` on expiry, 60s
  default from global `--timeout`); su-shell timeout tears down and re-elevates
  the shell (a half-finished command would corrupt the next capture); 10s
  elevation.
- Persistent connection per server, lazy connect on first use, reconnect when
  dropped (watcher goroutine on `conn.Wait()` marks the manager dead; redial on
  next use).
- Do **not** port the `pkill -f '<command>'` abort (`src/index.ts:622`) — an
  LLM-chosen pattern can kill arbitrary remote processes. Cancel via
  context/session close instead.

**Error model**: the Go SDK packages a handler's returned `error` as a *tool
error* (`CallToolResult{IsError:true}`), not a JSON-RPC protocol error like
TS's thrown `McpError`. Sanitization failures, vault-locked, and SSH failures
become `IsError` tool results (the model sees the message — MCP-preferred).
Schema validation produces protocol `InvalidParams` automatically before the
handler runs.

**Vault locking**: servers with encrypted secrets need the master key, derived
once at process start from `SSH_MCP_MASTER_PASSWORD_FILE` (mode-checked 0600,
refuse if group/other-readable) or a TTY prompt. If absent at start, the
process runs but tools targeting encrypted-secret servers return an `IsError`
"vault locked; provide SSH_MCP_MASTER_PASSWORD_FILE or use key/agent auth".
Servers using `key` (no passphrase) or `agent` auth work without any master
password. `list-servers` marks which servers are locked so the model avoids
them. The master key is held as `[]byte`, zeroed after use, never a `string`.

## SSH Host Key Verification

`golang.org/x/crypto/ssh` requires an explicit `HostKeyCallback` — there is no
"accept anything" default to inherit. Design:

- Default: verify against `~/.ssh/known_hosts` plus the per-server pinned
  `hostKey` fingerprint (`golang.org/x/crypto/ssh/knownhosts` + `FixedHostKey`).
- First connect (unknown host): TOFU. Web UI displays the fingerprint for the
  operator to accept, then persists it to `hostKey`. MCP mode records the
  fingerprint on first successful connect and hard-fails on any later change.
- `--insecureIgnoreHostKey` exists but defaults off and logs a warning on every
  use. Legacy `ssh-rsa`/`diffie-hellman-group1` are not re-enabled.

## CLI Compatibility

All existing flags kept: `--host --port --user --password --key --suPassword
--sudoPassword --disableSudo --timeout --maxChars`.

When `--host` is given, it defines an implicit in-memory server (not persisted)
that becomes the default: the `server` tool parameter is then optional, and
existing MCP client configs work with zero changes. `validateConfig` semantics
kept: `--host`/`--user` required in CLI mode, `--port` numeric. (These CLI
secrets are still plaintext in the MCP config — unchanged from today, and
documented as such; the encrypted store is the recommended alternative.)

## Web UI (`ssh-mcp web`)

Vanilla JS single page, embedded assets, binds `127.0.0.1` only.

Screens:

1. **First run** — a one-time bootstrap token is printed to the terminal on
   startup; the browser must present it before any endpoint answers (blocks a
   local race/rebinding page from claiming setup). Then set the master password.
2. **Unlock** — enter master password, verified against `kdf.verifier`. Session
   held in memory (cookie), dies with the process. Idle timeout 15 min, absolute
   expiry, `POST /api/lock`.
3. **Server list** — table of connections; add/edit/delete forms (name, host,
   port, user, auth, key path, passwords, host-key confirm); test-connection
   button dials a **saved** server and reports ok/fail.
4. **Import wizard** — pick source → preview → select → apply.
   - Sources: `~/.ssh/config` (parse Host/HostName/Port/User/IdentityFile; skip
     `Include`/`Match`/wildcard entries with a note rather than guessing), JSON
     file upload (this tool's export format).
   - Preview: parsed entries in a table with per-row checkboxes and a conflict
     badge on name collision (rename or overwrite). ssh_config carries no
     passwords → rows show "no password set"; user fills them via edit.
5. **Export** — `POST` (not `GET`) with one-time download token,
   `Content-Disposition: attachment`, `Cache-Control: no-store`. Two modes:
   without secrets (safe to share), or **with** secrets — which **re-encrypts**
   under a separate user-supplied export passphrase (fresh salt, `time≥3`) in a
   labeled envelope, and requires re-entering the master password. Import never
   adopts a foreign file's `kdf`; foreign encrypted fields require that file's
   export passphrase for re-encryption.

### Web Security (applies regardless of crypto)

- **Host header** must be exactly `127.0.0.1:<port>` or `localhost:<port>` → else
  403 (DNS-rebinding defense).
- **Origin** must equal `http://127.0.0.1:<port>` (or absent for same-origin GET)
  on all state-changing requests.
- **CSRF**: per-session double-submit token in a non-simple header
  (`X-CSRF-Token`); require `Content-Type: application/json` on writes; no
  `Access-Control-Allow-*` ever, reject foreign preflights.
- Cookie `HttpOnly; SameSite=Strict; Path=/`, ≥128-bit `crypto/rand`,
  constant-time compare.
- Headers on `/api/*`: `Content-Security-Policy: default-src 'self'; script-src
  'self'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`, `Cache-Control: no-store`.
- **XSS**: `app.js` builds the DOM with `textContent` only — no
  `innerHTML`/`insertAdjacentHTML`. Import-preview fields are attacker-controlled.
- Server-side validation before preview: `name` regex, `host` hostname/IP,
  `port` 1-65535, `keyPath` constrained (expand `~`, `filepath.Abs` +
  `EvalSymlinks`, regular file only, 64 KiB cap, never echo file content/paths
  in errors).
- `http.MaxBytesReader` (1 MiB) on every handler; cap import preview at ~500
  entries; set `ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout` on `http.Server`.
- **Unlock rate limit**: max 1 in-flight argon2 derivation (prevents memory
  OOM), 5 attempts then exponential lockout, generic fixed-cost failure.
- `test-connection`: requires CSRF token, saved-server (`{name}`) only, one
  concurrent, 5/min, fixed short dial timeout, generic `{"ok":false}` to the
  browser (detail only on the local terminal) so it can't be a network scanner.
- Every `/api/*` route requires an authenticated session (a locked vault does
  not permit CRUD on `key`/`agent` servers either).
- Master-password field `autocomplete="current-password"`; SSH secret fields
  `type=password autocomplete="off"`.
- `GET /api/servers` reports secret **presence** only (never length/ciphertext).

REST API:

```
POST /api/first-run         {bootstrapToken, masterPassword}
POST /api/unlock            {masterPassword}
POST /api/lock
GET  /api/servers           list (no secrets)
POST /api/servers           create
PUT  /api/servers/{name}    update (If-Match: revision — optimistic concurrency)
DELETE /api/servers/{name}  delete
POST /api/import/preview    {source, payload} → parsed entries + conflicts
POST /api/import/apply      {entries[]}
POST /api/export            {secrets: bool, exportPassphrase?} → one-time token
POST /api/test-connection   {name} → ok/error (generic)
```

## Reload & Concurrency

- **Reload trigger**: monotonic `revision` integer in the file, not mtime (mtime
  is 1s-granular and moves backward on restores). MCP re-reads and verifies the
  MAC per tool call (file is tiny); refuses a file whose `revision` decreased.
  On a config change to a live server (host/creds), close and re-dial its
  persistent connection (managers keyed by name + config hash).
- **Writer side**: `flock` around the web store's read-modify-write; atomic
  rename covers reader-vs-writer; `PUT`/`DELETE` use `If-Match: <revision>` for
  optimistic concurrency (lost-update protection between two writers).
- **MCP handler concurrency** (the Go SDK does not serialize handlers): registry
  `map[name]*Manager` under a `sync.Mutex`; per-manager mutex, dial under lock
  with double-check; **the su PTY has one shared read buffer → serialize command
  execution per server** (hold the per-manager mutex for the whole exec).
- **stdio discipline**: in MCP mode nothing writes to stdout except the SDK; all
  logging goes to stderr (`slog` on stderr or `ServerOptions.Logger`).

## Secret Redaction

A single redaction pass wraps every path that returns or logs text — tool
output, tool errors, web API errors, and any log line: all known secret values
(passwords, passphrases) are replaced with `***`. This is the shared control
behind the sudo/su/keyPath leak fixes.

## Error Handling

SSH command failures → `IsError` tool result with exit code (from
`*ssh.ExitError`; handle `ExitMissingError`). Note the su-PTY path merges
stdout/stderr and cannot separate an exit code except via the sentinel framing.
Decide and document the ported quirk where TS errors on *any* stderr even at
exit 0 (recommend: only treat non-zero exit as error). Web API returns JSON
errors with appropriate status; unlock failures generic.

## Known Risks (heuristics that stay fragile)

- **su prompt / completion detection** is heuristic even with the sentinel
  approach: a host with an exotic PTY or banner can still mis-sequence. The
  sentinel + `LC_ALL=C` + echo-off password write is the mitigation, not a cure.
- Localized `su`/`sudo` prompt strings — pinned to `C` locale to keep parity.
- Loopback traffic is visible to root/BPF on the box — accepted (no TLS).

## Testing

Port the existing vitest suites to Go:

- **Unit** (pure functions, no live connection): command sanitization + maxChars
  matrix, argv parsing, description-comment escaping + newline rejection, crypto
  roundtrip (encrypt/decrypt, wrong password fails, AAD mismatch fails, same
  plaintext → different nonce, MAC tamper detection), ssh_config parser,
  store load/save atomicity + revision. Keep sudo/su command-wrapping as pure
  functions in `internal/sshx` so they're unit-testable.
- **Integration** (`testcontainers-go`, SSH server container): exec over
  persistent connection, reconnect after drop, sudo with/without password
  (assert password never appears in output), su elevation flow + fail-closed on
  wrong su password, host-key TOFU + change detection.
- **Web** (`httptest`): REST CRUD, unlock flow + rate limit, import
  preview/apply, CSRF/Host/Origin rejection, XSS-safe rendering.

TS sources and Node tooling are deleted only after the Go test suite is green.

## Out of Scope (v1)

- MCP over HTTP transport
- OS keychain integration (the "correct" encryption story — v2)
- Paste-`ssh`-command import parser
- Per-server timeout/maxChars/disableSudo overrides
- Per-server MCP exposure allowlist / audit log (noted as a hardening follow-up)
- Multi-user or remote (non-localhost) web access

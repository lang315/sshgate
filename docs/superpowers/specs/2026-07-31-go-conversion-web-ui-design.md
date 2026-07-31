# ssh-mcp: Go Conversion + Web Config UI — Design

Date: 2026-07-31
Status: Approved

## Goal

Convert the TypeScript ssh-mcp MCP server to Go, and add a localhost web UI for
full connection management: create, edit, delete, import, and export SSH
connections. The existing single-host CLI mode must keep working unchanged.

## Decisions (from brainstorming)

| Topic | Decision |
|---|---|
| Server selection | MCP tools take a `server` (connection name) parameter; new `list-servers` tool |
| Import sources | `~/.ssh/config`, JSON export/import, pasted `ssh` command; Termius-style wizard UX |
| Storage | JSON file with encrypted secret fields |
| Key management | Master password → argon2id → AES-256-GCM; `SSH_MCP_MASTER_PASSWORD` env for headless |
| UI stack | Vanilla HTML/JS embedded via `go:embed`, no Node build |
| CLI compat | All existing flags kept; CLI host acts as implicit default server |
| MCP transport | stdio only |
| Repo layout | Replace TS in-place; delete TS only after Go tests pass |
| Process model | One binary, two subcommands: `ssh-mcp` (MCP stdio) and `ssh-mcp web` (config UI) |

## Architecture

One Go module `github.com/lang315/ssh-mcp`, one binary:

- `ssh-mcp [flags]` — MCP server on stdio (default mode, same as today)
- `ssh-mcp web [--port 8422]` — config UI on `127.0.0.1:8422`

Dependencies: official `github.com/modelcontextprotocol/go-sdk`,
`golang.org/x/crypto` (ssh, argon2). Web server is stdlib `net/http` with
static assets embedded via `go:embed`. No other dependencies.

Layout:

```
cmd/ssh-mcp/main.go       entry point, subcommand dispatch, flag parsing
internal/config/          store: load/save servers.json, atomic writes, crypto
internal/sshx/            connection manager: persistent conn, su elevation, sudo wrap
internal/mcpserver/       MCP tools: exec, sudo-exec, list-servers
internal/web/             HTTP server, REST API, import parsers
web/static/               index.html, app.js, style.css (embedded)
```

Both modes read the same config store. The web UI is the writer; MCP processes
re-read the file when its mtime changes (checked per tool call). No fsnotify,
no daemon, no port conflicts when multiple Claude sessions each spawn an MCP
process.

## Data Model

`~/.config/ssh-mcp/servers.json`, file mode 0600, written atomically
(temp file + rename):

```json
{
  "version": 1,
  "kdf": { "salt": "…", "time": 1, "memoryKiB": 65536, "verifier": "…" },
  "servers": [
    {
      "name": "prod-1",
      "host": "1.2.3.4",
      "port": 22,
      "user": "root",
      "auth": "password",
      "keyPath": "~/.ssh/id_ed25519",
      "encPassword": "base64(nonce+ciphertext)",
      "encSuPassword": "…",
      "encSudoPassword": "…",
      "encKeyPassphrase": "…",
      "disableSudo": false,
      "timeoutMs": 60000,
      "maxChars": 1000
    }
  ]
}
```

- `auth` is one of `password | key | agent`.
- Only secret fields are encrypted (`encPassword`, `encSuPassword`,
  `encSudoPassword`, `encKeyPassphrase`). Everything else is plaintext JSON so
  the file stays greppable.
- Encryption: master password → argon2id (salt + params stored in `kdf`) →
  32-byte key → AES-256-GCM per field with a random nonce, stored as
  base64(nonce || ciphertext).
- `kdf.verifier` is a GCM-encrypted known constant used to check whether an
  entered master password is correct.
- Server names are unique; they are the `server` parameter value for MCP tools.

## MCP Tools

- `exec(server, command, description?)` — run command on the named server.
- `sudo-exec(server, command, description?)` — sudo variant. The CLI
  `--disableSudo` flag hides the tool entirely (as today); per-server
  `disableSudo` cannot hide a registered tool, so it makes `sudo-exec` return
  an InvalidParams error for that server instead.
- `list-servers()` — returns name, host, port, user, auth type. Never secrets.

Behavior ported 1:1 from the TS implementation:

- `description` appended to the command as ` # comment` (with `#` in the
  description escaped).
- Command sanitization: trim, reject empty, enforce `maxChars` (default 1000;
  `0`, negative, or `"none"` disables the limit).
- sudo without password: `sudo -n sh -c '<cmd>'` (single quotes escaped).
- sudo with password: `printf '%s\n' '<pw>' | sudo -p "" -S sh -c '<cmd>'`.
- su elevation (when suPassword set): open interactive shell, send `su -`,
  wait for password prompt, send password, wait for `#` root prompt; commands
  then run in that elevated shell. Auth failure patterns rejected.
- Timeouts: 30s connect, 60s default command (per-server / `--timeout`
  override), 10s elevation.
- Persistent connection per server, lazy connect on first use, reconnect when
  dropped.

Vault locking: servers with encrypted secrets require the master password,
taken from `SSH_MCP_MASTER_PASSWORD` env or `--masterPassword` flag. If absent,
tools targeting such a server return an error ("vault locked; set
SSH_MCP_MASTER_PASSWORD or use key/agent auth"). Servers using `key` (no
passphrase) or `agent` auth work without unlock.

## CLI Compatibility

All existing flags are kept: `--host --port --user --password --key
--suPassword --sudoPassword --disableSudo --timeout --maxChars`.

When `--host` is given, it defines an implicit in-memory server (not persisted)
which becomes the default: the `server` tool parameter is then optional, and
existing MCP client configs continue to work with zero changes. Config file is
not required in this mode. `validateConfig` semantics kept: `--host` and
`--user` required (in CLI mode), `--port` must be numeric.

## Web UI (`ssh-mcp web`)

Vanilla JS single page, embedded assets. Binds `127.0.0.1` only.

Screens:

1. **First run** — set master password (creates `kdf` block).
2. **Unlock** — enter master password, verified against `kdf.verifier`.
   Session held in memory (cookie), dies with the process.
3. **Server list** — table of connections; add/edit/delete forms with all
   fields (name, host, port, user, auth type, key path, passwords, disableSudo,
   timeout, maxChars); test-connection button performs a real SSH dial and
   reports success/failure.
4. **Import wizard** (Termius-style): pick source → preview → select → apply.
   - Sources: `~/.ssh/config` (parse Host/HostName/Port/User/IdentityFile),
     JSON file upload (this tool's export format), pasted `ssh` command
     (e.g. `ssh -p 2222 -i ~/.ssh/key user@host`).
   - Preview: parsed entries in a table with per-row checkboxes and a conflict
     badge when a name already exists (imported row can rename or overwrite).
   - ssh_config carries no passwords: after import, rows show "no password
     set"; user fills them via edit.
5. **Export** — download JSON; user chooses with secrets (encrypted fields
   as-is) or without secrets.

REST API (all under the unlock session):

```
POST /api/unlock            {masterPassword}
GET  /api/servers           list (no secrets)
POST /api/servers           create
PUT  /api/servers/{name}    update
DELETE /api/servers/{name}  delete
POST /api/import/preview    {source, payload} → parsed entries + conflicts
POST /api/import/apply      {entries[]}
GET  /api/export?secrets=bool
POST /api/test-connection   {name | full config} → ok/error
```

## Error Handling

Mirrors TS semantics: SSH stderr output → tool error including exit code;
connection/command/elevation timeouts as above; MCP errors use the Go SDK's
error equivalents of `McpError(InvalidParams | InternalError)`. Web API returns
JSON errors with appropriate HTTP status; unlock failures are generic (no
oracle beyond wrong-password).

## Testing

Port the existing vitest suites to Go:

- **Unit**: command sanitization + maxChars matrix (positive/0/negative/none),
  argv parsing, description-comment escaping, crypto roundtrip
  (encrypt/decrypt, wrong password fails), ssh_config parser, ssh-command
  paste parser, store load/save atomicity.
- **Integration** (`testcontainers-go`, SSH server container): exec over
  persistent connection, reconnect after drop, sudo with/without password,
  su elevation flow. Mirrors `persistent-connection.test.ts`,
  `sudo-exec.test.ts`, `smoke.ssh.test.ts`.
- **Web**: `httptest` for REST CRUD, unlock flow, import preview/apply.

TS sources and Node tooling are deleted only after the Go test suite is green.

## Out of Scope

- MCP over HTTP transport
- OS keychain integration
- Multi-user web UI / remote (non-localhost) access
- Migration tool for old CLI-arg-only setups (nothing to migrate — flags keep working)

# ssh-mcp Desktop: Terminal Completeness — Design (Slice 2)

Date: 2026-09-25
Status: Draft, awaiting review.
Depends on: `2026-09-24-desktop-app-design.md` (slice 1). Everything there
still holds unless this document changes it by name.

## Goal

Make the desktop app the only tool the author needs for SSH work:

- manage hosts inside the app, and delete `ssh-mcp web`;
- confirm host keys by fingerprint on first connect instead of trusting
  them silently;
- import hosts from `~/.ssh/config`;
- reach hosts behind a bastion (ProxyJump);
- use ssh-agent on Windows (OpenSSH agent pipe, Pageant);
- split a tab into panes;
- open a local shell in a tab.

Exit gate (ROADMAP): the author no longer opens `ssh-mcp web` (it no longer
exists) or another terminal for SSH work.

## Entry gate override

The ROADMAP entry gate for slice 2 ("slice 1 used daily for two weeks") was
not met when this spec was written. The author chose on 2026-09-25 to spec
slice 2 anyway. The two other gate facts hold: throughput passed (47–49 MB/s
on macOS, 38 MB/s on the CI runner), and the stdio transport decision is
settled (base64 JSON stays).

## Decisions

| Topic | Decision |
|---|---|
| Scope | One spec for all seven features, one plan. The author declined splitting into 2a/2b/2c |
| `ssh-mcp web` | Deleted: `internal/web`, the `web` subcommand, its README section. The hub is the only writer of the vault |
| Host CRUD | New UI-door methods on the hub. Secrets are write-only over the wire |
| JSON import/export | Dropped with the web UI. `servers.json` is itself the portable file |
| "Test connection" button | Dropped. Opening a tab is the test, and it is where the host-key prompt lives |
| Host key on first connect | Interactive: the app shows the SHA256 fingerprint, the user clicks Trust, the hub pins only the key matching that fingerprint |
| Host key mismatch | Hard refusal showing the pinned and presented fingerprints. No replace button in the dialog. "Forget host key" in the host editor clears the pin; the next connect prompts again |
| AI and unpinned hosts | Unchanged: `ErrNoHostKey`. Now applies to every hop of a jump chain |
| `~/.ssh/config` | One-time import with a preview. The hub reads the file itself |
| ProxyJump | `Server.Jump` names another saved host. Chains allowed, cycles refused, 8 hops max |
| Windows agent | `SSH_AUTH_SOCK` (pipe path), then `\\.\pipe\openssh-ssh-agent`, then Pageant |
| Local shell | PTY opened by the hub (`github.com/aymanbagabas/go-pty`), reusing the `term.*` path. Replaces `node-pty` in the ROADMAP |
| Split panes | A layout tree per tab, up to 4 panes, not persisted |
| Protocol version | `hello` returns `protocol: 2`; the app refuses a hub that answers 1 |

Rejected:

- **`node-pty` in Electron main.** A second terminal data path (flow control,
  ack, resize, tests) and a native module to build per Electron version and
  per OS. Main would hold terminal state, which slice 1 kept out of it. The
  cost the hub path pays instead: a hub crash kills local shells, as it
  already kills SSH tabs.
- **`-J user@host:port` strings for jumps.** A bastion outside the vault has
  no credentials of its own and no pinned key.
- **Live `~/.ssh/config` hosts next to vault hosts.** Two sources, and no
  place to store `AIVisible` for the file-backed ones.
- **A Replace button on mismatch.** One click under a MITM would pin the
  attacker's key. Forget is a separate action in a separate screen.

## Threat model changes

- The hub gains write methods on the UI door. The UI door is already
  full-privilege (it can unlock and decide), so this adds no new trust. The
  MCP door gets none of these methods; a test asserts the MCP door's method
  set is unchanged.
- Secrets travel renderer → hub on save, the same direction as the master
  password on unlock. They never travel hub → renderer: `servers` keeps
  returning no secret field, only `hasPassword`-style booleans.
- The local shell runs as the user, which the user can do anyway. It is on
  the UI door only. The AI cannot reach it: `term.*` is not on the MCP door.
- Deleting `ssh-mcp web` removes an HTTP listener, its CSRF and session code,
  and the bootstrap token.
- `~/.ssh/config` is read by the hub from a fixed path. The renderer never
  sends file contents or a path, so a compromised renderer cannot make the
  hub read arbitrary files through this method.

## Hub: new and changed UI-door methods

All are request-only. All count as UI activity for the idle lock, like
every other request except `status`. All that touch the store follow the
same write path: `withFlock`, `config.Load`, verify MAC, mutate,
`config.Save` (bumps `Revision`, recomputes MAC, atomic write), then
`h.Reload()`.

`h.Reload()` already runs on `status` and `servers`; after a write it runs
immediately so the Registry and the AI see the change.

### `vault.create {password}`

- Allowed only when the store has no KDF: either no file, or a file written
  before any secret existed. Otherwise `-32001 "vault already exists"`.
- Password minimum 8 characters, as the web UI enforced.
- Existing servers in a KDF-less file are kept (they hold no secrets).
- On success the hub is unlocked with the new key, exactly as after
  `unlock`.

### `servers.save {original?, server}`

`server` fields: `name, host, port, user, auth ("password"|"key"|"agent"),
keyPath, jump, aiVisible, password, suPassword, sudoPassword,
keyPassphrase`.

- `original` absent: create. A name that already exists fails.
- `original` present: update the server of that name, and rename it if
  `server.name` differs.
- Secret fields: `null` or absent keeps the stored value; `""` clears it; any
  other string sets it. Keeping a value across a change of name, host, port,
  user, or auth re-encrypts it under the new AAD (the logic of the web
  UI's `preserveSecrets`, moved into `internal/config`).
- Any secret field set, or kept, requires a KDF and an unlocked hub.
  Otherwise `-32002 "create a vault first"` or the locked error.
- Validation (all `-32602` with a field-specific message):
  - name: 1–64 characters of `A-Z a-z 0-9 . _ -`;
  - host: non-empty, no whitespace or control characters;
  - port: 1–65535;
  - user: non-empty, no whitespace or control characters;
  - auth: one of the three values; `key` requires `keyPath`;
  - jump: empty, or the name of another saved server; the resulting chain has
    no cycle and at most 8 hops.
- `hostKey` is not a field of `servers.save`. It changes only through the
  trust flow or `servers.forgetHostKey`. An edit of `host` or `port` clears
  the pin, because the pin belongs to the old endpoint.
- Renaming a server updates every other server's `jump` that named it.
- Saving closes open terminals of that server, as slice 1 specified; the
  app warns first.

### `servers.delete {name}`

Refused while another server's `jump` names it:
`-32003 "used as jump host by: a, b"`. Closes its open terminals.

### `servers.forgetHostKey {name}`

Clears the pin. The next `term.open` prompts.

### `servers` (changed)

Adds `jump`, `keyPath`, and four booleans: `hasPassword`, `hasSuPassword`,
`hasSudoPassword`, `hasKeyPassphrase`. Still never a secret.

### `sshconfig.preview {}` and `sshconfig.import {names}`

- The hub reads `~/.ssh/config` (`os.UserHomeDir()`). No parameters.
- `preview` returns `{entries: [{name, host, port, user, auth, keyPath,
  jump, exists}], notes: [string]}`. `exists` marks names already in the
  vault; those are never imported.
- Parsing is the web UI's `ParseSSHConfig`, moved to `internal/config`, with
  `ProxyJump` added:
  - a single alias maps to `jump`;
  - a `user@host:port` value, or a comma-separated list, is not mapped, and
    a note explains why.
- `Host` with wildcards or several patterns, `Match`, and `Include` are
  skipped with a note.
- `IdentityFile` becomes `auth: key` with `~` expanded; without one,
  `auth: agent`.
- `import` re-reads and re-parses the file (the renderer only sends names),
  then adds the selected entries in one save. Imported hosts have
  `aiVisible: false` and no pin.
- A `jump` naming an alias that is neither in the vault nor in the selection
  is dropped, with a note in the reply.

### `term.open` (changed)

New params: `local` (bool) and `trustHostKey` (`{server, fingerprint}`).

- `local: true` ignores `server` and opens a local shell (below). It is
  allowed while the vault is locked and when no store exists. This is the
  only `term.open` that is allowed while locked.
- For SSH, every hop is dialled in order, bastion first. For each hop:
  - **Pinned, matching key:** continue.
  - **Pinned, different key:** fail with `-32011 hostKeyMismatch`, data
    `{server, pinned, presented}`. Nothing is recorded.
  - **Not pinned, and `trustHostKey` names this server with this exact
    fingerprint:** accept, record the pin (`config.RecordHostKey`), continue.
  - **Not pinned otherwise:** fail with `-32010 hostKeyUnknown`, data
    `{server, fingerprint, keyType}`. The connection is closed; nothing is
    recorded.
- The renderer retries `term.open` once per prompt, with the same terminal
  id. A chain with two unpinned hops prompts twice, bastion first.
- If the key changes between the prompt and the retry, the fingerprint no
  longer matches, and the user gets a fresh `hostKeyUnknown` prompt showing
  the new fingerprint.
- Silent TOFU (`OnLearnHostKey` without a matching `trustHostKey`) is removed
  from the UI-door path. It remains in `--host` mode, which never touches the
  vault.

### `hello`

Returns `{protocol: 2}`. `desktop/src/shared/protocol.ts` bumps
`PROTOCOL_VERSION` to 2 and adds the new methods to `REQUEST_METHODS`.

## SSH layer

### ProxyJump

- `config.Server` gains `Jump string \`json:"jump,omitempty"\``. It is
  covered by the whole-file MAC. It is not part of any secret's AAD: a
  secret belongs to its own hop, not to the route.
- `sshx.DialConfig` gains `Via *DialConfig`. `Deps.Resolve` builds the chain
  from the store, decrypting each hop's secrets with that hop's AAD. It
  refuses cycles and chains longer than 8 hops.
- `Manager.ensure` dials `Via` first (recursively), then
  `viaClient.Dial("tcp", host:port)`, then `ssh.NewClientConn` and
  `ssh.NewClient` on that connection. Closing the manager closes the whole
  chain.
- `// ponytail:` each jumped host dials its own bastion connection. It does
  not share the bastion's own `Manager`. Share it if the count of bastion
  connections becomes a problem.
- The Registry's config hash covers the whole chain, so editing a bastion
  replaces every manager that goes through it.
- Host keys: each hop has its own pin and its own `HostKeyCallback`.
- `Hub.Exec` refuses with `ErrNoHostKey` unless every hop is pinned. The
  error reaching the AI stays generic. The audit reason names the unpinned
  hop.
- The bastion does not need `AIVisible`: the AI gets no shell on it. Its
  name also does not appear in anything the MCP door returns.

### Windows agent

`auth: agent` on Windows tries, in order:

1. `SSH_AUTH_SOCK` if set: dialled as a named pipe when it starts with
   `\\.\pipe\`, else as a Unix socket;
2. `\\.\pipe\openssh-ssh-agent` via `go-winio.DialPipe` (already a
   dependency);
3. Pageant via `github.com/davidmz/go-pageant`.

It uses the first that answers. If none answers, it fails with
"no ssh-agent found (tried SSH_AUTH_SOCK, OpenSSH agent pipe, Pageant)".
Unix behaviour is unchanged. The Windows code lives in `agent_windows.go`
behind a build tag. CI adds `GOOS=windows go vet ./...`. Runtime behaviour
on real Windows stays unverified; the ROADMAP finding stays open.

### Local shell

- `sshx` gains `LocalTerm`, which has the same surface `registerTermMethods`
  uses from `TermSession`: write, resize, ack, close, and an output/exit
  stream. Flow-control thresholds are identical. `term.go` switches on
  `local` and otherwise does not care which one it holds.
- It uses `go-pty`: a Unix PTY, or ConPTY on Windows.
- The shell is `$SHELL` with `-l`, falling back to `/bin/sh`. On Windows it
  is `%COMSPEC%`, falling back to `cmd.exe`.
- The working directory is the user's home.
- The environment is the hub's, minus every `ELECTRON_*` and `SSH_MCP_*`
  variable, with `TERM=xterm-256color`.
- There is no redaction and no audit: this is the user's own terminal.
- `term.exit` carries the shell's exit code.
- `term.closeAll` (renderer crash) closes local shells too.

## Renderer

### New screens

- **Create vault.** Shown when `status.hasStore` is false or the store has no
  KDF. Fields: master password and confirmation; minimum 8 characters.
  Calls `vault.create`.
- **Host editor.** Opens from a host's context menu or from a "New host"
  button.
  - Fields: name, host, port, user, auth, and keyPath (for key auth).
  - Password, su password, sudo password, key passphrase: empty inputs with
    "saved" placeholder text when the matching `has*` flag is set; a Clear
    link per field sends `""`.
  - A jump dropdown of other hosts, and an AI-visible checkbox.
  - The pinned fingerprint is shown read-only with a "Forget host key"
    button.
  - Save shows "Saving will close N open tabs" when N > 0.
  - Delete asks for confirmation and shows the jump-host refusal inline.
- **Import from ~/.ssh/config.** Lists the preview entries with checkboxes
  (existing names disabled) and the notes, then an Import button.
- **Host key prompt.** Shown on `hostKeyUnknown`:
  - it shows the server name, key type, and `SHA256:…` fingerprint;
  - Cancel is the default and the focused button;
  - Trust is mouse-only and disabled for 500 ms, like Allow in the approval
    panel.
- **Host key mismatch.** Shown on `hostKeyMismatch`:
  - it shows both fingerprints and the warning that someone may be
    intercepting the connection;
  - it has one Close button;
  - the text points to Forget in the host editor.

### Split panes

- A tab holds a layout tree:
  `type Layout = { pane: string } | { split: 'h' | 'v'; ratio: number; a: Layout; b: Layout }`.
  The pane id is the terminal id.
- There are at most 4 panes per tab. A split beyond that is ignored.
- The layout logic is a pure reducer in `layout.ts`: split, close, resize,
  and focus neighbour. Vitest covers it.
- Shortcuts:
  - Cmd/Ctrl+D: split right;
  - Cmd/Ctrl+Shift+D: split down;
  - Cmd/Ctrl+W: close the focused pane, and the tab when it was the last;
  - Cmd/Ctrl+Alt+Arrow: move focus.
- Dragging a divider sets `ratio`, clamped to 0.15–0.85.
- A new pane opens on the same host as the focused pane. A picker in the new
  pane's header can switch it to another host or to local before it
  connects.
- Each pane is its own terminal session with its own id; slice 1's
  per-terminal rules (ack, resize debounce, survive lock, Reconnect) apply
  per pane unchanged.
- "Send to tab" targets the most recently focused pane connected to the
  request's server.
- Layout is not persisted across app restarts.

### Local tabs

- A "Local shell" button sits in the host list.
- A local tab shows "local" as its title.
- A local tab survives lock like any tab. Reconnect opens a fresh shell.

## Removed

- `internal/web/` (all files), the `web` case in `cmd/ssh-mcp/main.go`, and
  `runWeb`.
- README's "Multi-server + web config" section, rewritten around the app.
- CLAUDE.md's Web UI section and the `/api/test-connection` mention.
- `ParseSSHConfig` and `preserveSecrets` survive, moved into
  `internal/config` with their tests.

## Testing

Go (all without Docker, using `internal/sshx/sshtest`):

- Every new UI-door method:
  - happy path;
  - each validation error;
  - locked and no-KDF refusals;
  - MAC and `Revision` bump after each write;
  - rename carries secrets and rewrites dependent `jump`s;
  - host/port edit clears the pin.
- MCP door method set unchanged (explicit list assertion).
- Host key flow on `term.open`: unknown, then trust with the right
  fingerprint pins; trust with a wrong fingerprint does not pin; mismatch
  after `RotateHostKey`; forget, then prompt again.
- ProxyJump:
  - two `sshtest` servers, with the target reachable only through the
    bastion (`sshtest` gains `direct-tcpip` forwarding);
  - a chain of 2 with one unpinned hop prompts for that hop only;
  - cycle and depth refusals;
  - `Hub.Exec` refuses when the bastion is unpinned.
- Local shell (Unix only; skipped on Windows): echo round-trip, resize,
  exit code, environment scrubbed of `SSH_MCP_*`, allowed while locked.
- `ParseSSHConfig`: existing cases plus `ProxyJump` forms and `Include` note.
- `GOOS=windows go vet ./...` in CI.

Desktop:

- Vitest:
  - layout reducer (split limit, close collapses the parent, ratio clamp,
    focus neighbour);
  - host editor secret-field states (keep / set / clear);
  - host key dialog's Trust delay.
- Playwright e2e, extending `smoke.spec.ts` or adding `hosts.spec.ts`:
  1. create a vault on an empty store;
  2. add a host pointing at `sshtestd`;
  3. connect, see the fingerprint prompt, Trust, and get a shell;
  4. split right, and see both panes echo;
  5. open a local shell and run `echo local-ok`;
  6. forget the host key, reconnect, and see the prompt again.

## Success criteria

1. Every host operation the author used `ssh-mcp web` for works in the app,
   and `ssh-mcp web` is gone.
2. First connect to a new host shows its fingerprint and pins only after
   Trust. A changed key is refused with both fingerprints shown.
3. A host behind a bastion connects from the app and runs an AI command
   after approval.
4. `~/.ssh/config` hosts import in one pass.
5. A tab splits into up to 4 panes mixing hosts and local shells.
6. `go test -race ./...`, the desktop unit tests, and e2e pass in CI.
7. The author uses the app for a week without opening another terminal for
   SSH work. This is the ROADMAP exit gate, checked by the author.

## Known risks

- **Windows agent and ConPTY are unverified at runtime.** Mitigation: build
  tags, `GOOS=windows` vet in CI. Real-machine testing stays a ROADMAP
  finding.
- **`go-pty` on macOS with the hub's inherited environment.** Login shells
  read the user's profile, so `PATH` differs from the hub's; this is
  intended.
- **Jump chains multiply connections.** Accepted; see the `ponytail` note.
- **Silent-TOFU removal changes existing behaviour.** Hosts pinned under
  slice 1 keep their pins. Only unpinned hosts now prompt.

## Out of scope

Changing the master password, persisting layouts, SFTP and port forwarding
(slice 3), agent forwarding, `Include` and `Match` in `~/.ssh/config`,
per-host terminal settings (font, colours), and connection sharing between
a bastion's own tabs and the hosts behind it.

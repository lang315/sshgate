# ssh-mcp Desktop: Host Management and Host-Key Prompt — Design (Slice 2a)

Date: 2026-09-25
Status: Draft, awaiting review. Rewritten the same day after a four-agent adversarial review of an earlier all-of-slice-2 draft (see §Why this is 2a only).
Depends on: `2026-09-24-desktop-app-design.md` (slice 1). Everything there still holds unless this document changes it by name.

## Goal

Stop needing `ssh-mcp web` day to day, and stop trusting host keys silently:

- create the vault and add, edit, and delete hosts inside the app;
- confirm a host key by its fingerprint on first connect, and forget a pin on purpose;
- make every vault write safe to do from the app (unlocked, MAC-protected, serialized).

Exit gate: the author goes one week managing hosts only in the app. Then `ssh-mcp web` is deleted in a small follow-up (§Web UI).

## Why this is 2a only

The first draft bundled seven features. Review found that only one of them was backed by evidence from daily use: having to unlock twice, once in the app and once in `ssh-mcp web`, whenever a host changes. Silent TOFU was the one known security gap. Everything else was speculative, and three of the extra features came with defects of their own:

- split panes bound Ctrl+D and Ctrl+W, which the shell needs;
- the go-pty local shell never reports exit;
- the jump-chain handshake had no timeout.

The ROADMAP gate for slice 2 ("slice 1 used daily for two weeks") is still unmet. The author overrode it for 2a only, because 2a removes the friction that keeps daily use from happening. The remaining features move to ROADMAP slices 2b and 2c, each with its own evidence gate and the review findings attached.

## Decisions

| Topic | Decision | Why |
|---|---|---|
| Host CRUD | New UI-door methods on the hub | The hub already owns the unlocked key; a second writer (web) means a second unlock |
| `ssh-mcp web` | Kept during 2a, deleted after the exit gate | Deleting later costs nothing; undeleting does. It is also still the only headless host editor |
| Every vault write | Requires an unlocked hub when the store has a KDF | A keyless save would drop the MAC (fixed in `223faa8`; now refused) |
| Write path | One exported `config.Update` with a file lock and a process mutex | `withFlock` is unexported and a no-op on Windows; `RecordHostKey` and a save can race |
| Secrets | Write-only on the wire; cleared when host or port changes unless re-supplied | The AAD binds secrets to the endpoint. Carrying them to a new host hands them to whoever answers there |
| Host key on first connect | App shows the fingerprint; Trust pins exactly that key | The AI is already refused on unpinned hosts; now the human sees what they trust |
| Silent TOFU | Removed from every hub path, AI exec included | A learner closure left on any path is a way around the prompt |
| Host key mismatch | Hard refusal with both fingerprints; Forget lives in the host editor | One click under a MITM must not pin the attacker's key |
| Key algorithm | Pinned alongside the fingerprint | Otherwise a server adding an ed25519 key looks like a MITM, which trains blind re-trust |
| Prompt transport | `term.open` resolves with a status, never an error, for host-key outcomes | Error `code`/`data` are dropped by `hubProcess.ts`, IPC, and `transport.ts` |
| Closing tabs on edit | Only when the dial config changes (not on an AIVisible toggle) | Toggling AI visibility must not kill a working session |
| Approvals across edits | Saving or deleting a server denies its pending AI requests; an approved exec re-checks the endpoint | An approval was for `user@host:port` as shown, not for whatever the name points at later |
| Protocol | `hello` → `protocol: 2` | `term.open` changes meaning (no silent TOFU, status results) |

## Hub

### `config.Update(path, masterKey, fn func(*File) error) error`

- This becomes the only way to write the store. It takes a package-level `sync.Mutex`, then the file lock (`withFlock`; on non-Unix only the mutex), then:
  1. `Load`;
  2. if the file has a KDF, check `masterKey` and verify the MAC;
  3. run `fn`;
  4. `Save`.
- A KDF store without a key is refused (the `Save` rule from `223faa8`).
- `RecordHostKey` becomes a thin wrapper over it.
- The web UI's `saveLocked` also switches to it, so both writers serialize.

### UI-door methods

All are request-only and count as UI activity. Every write runs through `config.Update`, then `h.Reload()`. Error codes: `-32602` for invalid params, `-32000` with a message for everything else. The renderer only displays the message, so no other codes are needed.

**`status`** gains `hasVault` (the store has a KDF). The renderer shows "Create vault" when `hasVault` is false. A deliberately key-only store therefore sees that screen; creating a vault is then the only way forward, because slice 2a writes need a vault.

**`vault.create {password}`**
- Refused if a KDF exists. The password must be at least 8 characters.
- Servers already in a KDF-less file are kept; they hold no secrets.
- On success the hub is unlocked with the new key.

**`servers.save {original?, server}`**

`server` is a `config.ServerInput`: `name, host, port, user, auth, keyPath, aiVisible`, plus four `*string` secrets: `password, suPassword, sudoPassword, keyPassphrase`.

- With `original` absent it creates; a duplicate name fails. With `original` present it updates, and renames if the name differs.
- Secret values: `nil` keeps the stored value, `""` clears it, and any other value sets it.
- When a kept secret's AAD changes, it is re-encrypted. That happens on a change of name, user, or auth.
- A change of **host or port** clears the pin (`hostKey`, `hostKeyAlgo`) and every secret the request does not re-supply. The editor tells the user so before saving.
- Validation, each failure with a field-specific message:
  - name: 1–64 characters of `A-Z a-z 0-9 . _ -`;
  - host and user: non-empty, with no whitespace or control characters;
  - port: 1–65535;
  - auth: `password`, `key`, or `agent`, where `key` requires `keyPath`.
- `hostKey` is not an input.
- Afterwards the hub does two things:
  1. `Registry.Close(original or name)`, but only if the saved dial config differs from before. Differences are host, port, user, auth, keyPath, any secret, or the pin. A rename always closes.
  2. It denies that server's pending AI requests with the reason "server changed".

**`servers.delete {name}`**: `Registry.Close(name)`, and deny its pending requests.

**`servers.forgetHostKey {name}`**: clears `hostKey` and `hostKeyAlgo`, then `Registry.Close(name)`.

**`servers`** adds `keyPath`, `hostKeyAlgo`, and the booleans `hasPassword`, `hasSuPassword`, `hasSudoPassword`, `hasKeyPassphrase`. It still returns no secret.

### `Registry.Close(name)`

A new method that closes and removes one manager. Its terminals see EOF and send `term.exit`, which the tabs already handle. Save, delete, forget, and trust call it.

### Host keys

- `config.Server` gains `HostKeyAlgo string` (for example `ssh-ed25519`). It is covered by the MAC.
- `sshx.HostKeyCallback` gains a strict mode, used on every hub path:
  - an empty pin returns `*HostKeyUnknownError{Fingerprint, KeyType}` and never accepts;
  - a different key returns `*HostKeyMismatchError{Pinned, Presented}`, which wraps `ErrHostKeyMismatch`.
- The learner closure is removed from the hub path, both from `resolveLocked` (`hub.go:359-364`) and from the file branch of `Deps.Resolve`. `--host` mode keeps its TOFU.
- When a pin has an algorithm, the dial sets `ClientConfig.HostKeyAlgorithms` to that algorithm's family. Old pins without one negotiate as today.
- The Registry's substitution of an empty pin with the cached one (`registry.go:34-36`) stays for `--host` mode only. The hub path always passes the stored pin, and a forget has already closed the old manager.

### `term.open` (changed)

New optional param: `trustHostKey: {fingerprint}`.

The reply is always a result:

- `{status: "open"}`: the session is open, and `term.data` follows as before.
- `{status: "hostKeyUnknown", server, fingerprint, keyType}`: nothing was opened or recorded.
- `{status: "hostKeyMismatch", server, pinned, presented}`: nothing was opened or recorded.

Other failures stay errors, as today.

The trusted retry works as follows:

1. The hub sets `dc.HostKey = trustHostKey.fingerprint` before `Registry.Get`. The config hash differs, so a fresh manager dials and the normal pin check enforces that exact key.
2. After the handshake succeeds, the hub records `hostKey` and `hostKeyAlgo` through `config.Update`. If recording fails, the open fails and `Registry.Close(name)` runs.
3. If the server presents a different key than the one trusted, the hub closes that manager and replies `hostKeyUnknown` with the new fingerprint. It never replies `hostKeyMismatch`, because the user never confirmed that pin.

The renderer retries once per prompt, reusing the terminal id. The hub releases the id when it returns a non-`open` status.

### AI exec

- The path is unchanged, except that the endpoint is bound to the approval.
- `Hub.Exec` records `user@host:port`, and the pin, before `broker.Submit`.
- The approval card shows `user@host:port` next to the name.
- After approval, the re-resolve must match all four values, or the exec fails with the generic "server changed" error and is audited.
- An unpinned server is still refused with `ErrNoHostKey`, and nothing on this path can learn a key.

## Renderer

- **Create vault screen**, shown when `hasVault` is false. It asks for a password and its confirmation, minimum 8 characters.
- **Host list**: a "New host" button, and Edit and Delete on each row.
- **Host editor**:
  - Fields: name, host, port, user, auth, keyPath (key auth only), and an AI-visible checkbox.
  - The four secrets show as empty inputs. When the matching `has*` is true, the placeholder reads "saved". A Clear link sends `""`.
  - When host or port changes, the editor shows: "Changing host or port forgets the host key and saved passwords unless you re-enter them."
  - The pinned fingerprint and algorithm are shown read-only, with a "Forget host key" button.
  - Save shows "Saving will close N open tabs" when the change will close tabs. N is counted in the renderer from tabs on that server.
  - Delete asks for confirmation.
- **Host key prompt**, shown on `hostKeyUnknown`:
  - It shows the server, key type, and `SHA256:…` fingerprint.
  - Cancel is the default and has focus.
  - Trust is mouse-only and disabled for 500 ms, the same rule as Allow.
  - Trust retries `term.open` with `trustHostKey`.
- **Host key mismatch dialog**:
  - It shows both fingerprints and says the connection may be intercepted.
  - It has two buttons: Close, and "Open host editor", which jumps to the Forget button.
  - The dialog itself cannot re-pin.

## Web UI

- It stays through 2a.
- Its writes switch to `config.Update`, so it inherits the unlocked-only rule and serializes with the hub.
- Its "Test connection" handler keeps TOFU, which is web-only and deleted with it.
- After the exit gate, a follow-up deletes:
  - `internal/web`, the `web` subcommand, and their docs;
  - the README section on the web UI, rewritten around the app.
- That follow-up must also say what happens to headless use. `hub --cli` cannot create a vault or pin a key; either that is accepted, or `hub --cli` gains the missing commands.

## Testing

**Go** (no Docker; uses `sshtest`):

- `config.Update`:
  - it serializes concurrent writers;
  - it refuses a KDF store without a key;
  - it verifies the MAC before writing.
- `servers.save`:
  - every validation error;
  - `nil`/`""`/value semantics for all four secrets;
  - a rename re-encrypts the secrets;
  - a host or port change clears the pin and any secret not re-supplied;
  - an AIVisible-only change keeps open terminals;
  - a dial-config change closes them;
  - pending requests for that server are denied.
- `servers.delete` and `forgetHostKey`: each closes its manager, and the next `term.open` returns `hostKeyUnknown`.
- The trust flow:
  - unknown host, then trust with the right fingerprint, pins it along with its algorithm;
  - trust with a wrong fingerprint returns `hostKeyUnknown` carrying the real fingerprint, and records nothing;
  - `RotateHostKey` then gives `hostKeyMismatch`;
  - forget, then another open, prompts again.
- An AI exec on an unpinned server never writes the store (a spy on `config.Update`).
- An endpoint edit between approval and run fails the exec with "server changed".
- The set of MCP-door methods is unchanged (an explicit list is asserted).

**Desktop**:

- Vitest:
  - the host editor's secret-field states;
  - the host-or-port-change warning;
  - the host key dialog's Trust delay;
  - `screenFor` with `hasVault`.
- Playwright `hosts.spec.ts`, run in order:
  1. create a vault on an empty store;
  2. add a host pointing at `sshtestd`;
  3. connect, see the prompt, Trust, and get a shell;
  4. edit the port, and the tab closes;
  5. forget the key, reconnect, and see the prompt again.

## Success criteria

1. The app creates the vault and adds, edits, and deletes hosts. The author does not open `ssh-mcp web` for a week.
2. First connect shows the fingerprint, and the key is pinned only after Trust. A changed key is refused with both fingerprints shown.
3. No hub path learns a host key silently.
4. Every write to an encrypted store keeps its MAC and happens only while unlocked.
5. `go test -race ./...`, the desktop unit tests, and e2e pass in CI.

## Out of scope

Everything moved to the ROADMAP (slices 2b and 2c):

- ProxyJump;
- `~/.ssh/config` and `known_hosts` import;
- split panes;
- the local shell;
- the Windows agent.

Also out: changing the master password, and keyboard-interactive / 2FA auth.

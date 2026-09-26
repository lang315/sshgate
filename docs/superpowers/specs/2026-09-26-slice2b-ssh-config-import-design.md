# sshgate: Import Hosts from `~/.ssh/config` — Design (Slice 2b, part 1)

Date: 2026-09-26
Status: Approved 2026-09-26; implemented (plan `plans/2026-09-26-slice2b-ssh-config-import.md`).
Depends on: `2026-09-24-desktop-app-design.md` (slice 1) and `2026-09-25-desktop-slice2a-design.md` (slice 2a). Everything there still holds unless this document changes it by name.

## Goal

Add the hosts the author already reaches with `ssh` to the vault in one step, with their host keys pinned from `known_hosts`, instead of retyping each one in the host editor and confirming each key on first connect.

Exit gate: the author imports their real `~/.ssh/config`. `fviainboxes-server`, `fviainboxes-db`, and `buildpc` arrive pinned and each opens a terminal tab with no Trust prompt; `orb` (pulled in by OrbStack's `Include`, reached through a `ProxyCommand`) is listed as skipped with its reason.

## Scope

In: reading `~/.ssh/config` (with `Include`), pinning host keys from the `known_hosts` files OpenSSH would use, and adding new hosts to the vault from the app.

Out, recorded in ROADMAP:

- **ProxyJump** (the other half of ROADMAP slice 2b). The author has no host behind a bastion today, so its entry gate is unmet. Imported hosts that need a jump are skipped, never dialled directly.
- Updating hosts that already exist in the vault. Import only adds.
- Writing the vault back out to `~/.ssh/config`.
- Passwords. `ssh_config` holds none; password hosts get their password in the editor afterwards.

## User flow

1. On the Hosts home tab, next to **New host**, an **Import from SSH config** button. It works only while the vault is unlocked (the same rule as every host write).
2. It opens a right-hand sheet, like the host editor. One row per alias:
   - alias, `user@host:port`, and the auth it will use (key path, or `agent`);
   - the host key it will pin, as `<algo> SHA256:…`, or "not in known_hosts" (that host gets the usual Trust prompt on first connect);
   - a status: **Ready** (a checkbox, checked by default), **Already in vault**, or **Skipped** with a reason.
   - A Ready row whose key file needs a passphrase says so: "Key has a passphrase: add it in the editor after import."
3. Above the list: "Host keys come from your known_hosts. With `StrictHostKeyChecking accept-new`, OpenSSH accepted them without asking you; compare them with the server if unsure."
4. **Import N hosts** adds the checked rows and closes the sheet. The new hosts appear in the host grid with AI access off. A row that stopped being Ready between scan and import (for example, a host added meanwhile under the same name) is reported back and not written.

## Security rules

- **The renderer sends only alias names.** `import.apply` re-runs the scan for those aliases in the hub and writes what the hub computed. Host, port, user, key path, and host key never come from the renderer, so a compromised renderer cannot plant a host or a pin through import.
- **One write.** All imported servers and their pins go into the vault in a single `config.Update`: either all of them land or none do.
- **No network.** Import never dials. It reads files and runs `ssh -G`, which does not connect.
- **`ssh -G` runs the author's config as `ssh` would**, including `Match exec` commands. That is the same trust the author gives the config every time they type `ssh`.
- **Never dial around a proxy.** A host with `ProxyJump` or `ProxyCommand` is skipped: adding it without the proxy would connect somewhere the author's `ssh` never does.
- **Pins only from a known_hosts match**, never from a connection. A `@revoked` key is never pinned. `@cert-authority` lines are ignored (certificates are not supported).
- **Audit.** Each imported server writes a `kind: "config"` record with `action: "import"`, the server, host, port, and the pinned fingerprint and algorithm (empty when not pinned). No secret, no config text.
- **Not on the MCP door.** `import.scan` and `import.apply` are UI-door only, like every other host write.

## Components

### `internal/sshconfig` (new package, no hub dependency)

- `Aliases(path string) ([]string, error)`
  - Reads the file and every file it `Include`s: globs expanded, relative paths resolved against `~/.ssh` as OpenSSH does, depth limit 16 (OpenSSH's own).
  - Collects the names on `Host` lines. A name containing `*`, `?`, or a leading `!` is a pattern, not a host, and is dropped.
  - Duplicates are dropped; first appearance wins the order.
  - A missing top-level file returns an empty list and no error.
- `Resolve(ctx, sshBin, configPath, alias string) (Resolved, error)`
  - Runs `ssh -G [-F configPath] -- <alias>` with a 5-second timeout.
  - Parses `hostname`, `port`, `user`, every `identityfile` line, `proxyjump`, `proxycommand`, `userknownhostsfile` (one line, space-separated), and `hostkeyalias`. Keys are matched case-insensitively. `ssh -G` omits `proxycommand` when it is unset.
  - The binary is found with `exec.LookPath`, so a shell alias or function named `ssh` is never used.
  - `ssh -G` already applies `Host *` defaults, `Match`, and `%` tokens, and uses the alias as the hostname when there is no `HostName`.
- `Candidate(r Resolved, alias string, exists func(string) bool) Candidate` applies the rules:

| Condition | Result |
|---|---|
| `proxyjump` or `proxycommand` set (other than `none`) | Skipped: "needs ProxyJump" / "needs ProxyCommand" |
| alias fails the server-name rule (`[A-Za-z0-9._-]{1,64}`) | Skipped: "name: use 1-64 characters of A-Z a-z 0-9 . _ -" |
| resolved server fails `ServerInput.Validate` | Skipped: the validation message |
| a vault server already has this name | Already in vault |
| otherwise | Ready |

Auth: the first `identityfile` that exists on disk (after `~` expansion) → `auth: "key"` with that path, kept in `~/…` form (the hub already expands it when dialling). `ssh -G` lists the default key files when none is configured, in OpenSSH's own order, so no separate default list is needed. No existing file → `auth: "agent"`. If the chosen key is encrypted (`ssh.ParseRawPrivateKey` returns `*ssh.PassphraseMissingError`), the candidate carries `needsPassphrase: true`.

### `internal/sshx`: `KnownHostKey(files []string, host string, port int) (algo, fingerprint string, ok bool)`

- Builds a `knownhosts` callback from the files that exist and calls it with a throwaway key; `*knownhosts.KeyError.Want` then lists the keys on record for `host:port` (the library handles `[host]:port`).
- Picks one: ed25519, then ecdsa (any size), then rsa. Returns `ok: false` when none is on record or the only match is revoked.
- The lookup name is `hostkeyalias` when set, else `hostname`. The files are the ones `ssh -G` lists in `userknownhostsfile`.

### Hub

- `import.scan` → `{ candidates: Candidate[], note?: string }`. For each alias from `Aliases`: `Resolve`, `Candidate`, then `KnownHostKey` for Ready rows.
  - Needs an unlocked vault (`writeKey`: `ErrLocked`, or "create a vault first").
  - `note` is "No ~/.ssh/config" when the file is missing.
  - A missing `ssh` binary fails the call with "OpenSSH client (ssh) not found".
  - A single alias whose `ssh -G` fails becomes Skipped: "ssh -G failed"; the others still list.
- `import.apply { aliases: string[] }` → `{ imported: string[], skipped: { alias, reason }[] }`.
  - Needs an unlocked vault.
  - Re-resolves each alias, then in one `config.Update` runs `config.ApplyServer` for each Ready candidate (as a new server, `aiVisible: false`, no secrets) and sets `HostKey`/`HostKeyAlgo` on it when a key was found.
  - Aliases that are not Ready at that moment land in `skipped`.
  - Then `Reload`, and one audit record per imported server. A failed post-write reload is returned like `servers.save` does; the write itself stands.
- `Options.SSHConfigPath` (`""` = OpenSSH's default, no `-F`) and `Options.SSHBin` (`""` = `ssh` from `PATH`). `sshgate hub --sshConfig=<path>` sets the first. It is a dev/test knob, like `--store`: OpenSSH finds `~` from the account database, not `$HOME`, so tests cannot redirect it any other way.
- `ProtocolVersion` becomes 3.

### Desktop

- `shared/protocol.ts`: `import.scan` and `import.apply` added to `REQUEST_METHODS`; `PROTOCOL_VERSION = 3`; `Candidate` type.
- `main.ts`: `SSHGATE_SSH_CONFIG` becomes `hub --sshConfig=`, like `SSHGATE_STORE`.
- `ImportSheet.tsx`: the sheet above, reusing the host editor's sheet layout and tokens. Import is a normal submit button and is disabled while nothing is checked. Esc closes the sheet.
- `HostList.tsx`: the **Import from SSH config** button.

## Errors

| Situation | What the author sees |
|---|---|
| Vault locked / no vault | The button is disabled; the hub also refuses (`ErrLocked`, "create a vault first") |
| No `ssh` on `PATH` | Sheet shows "OpenSSH client (ssh) not found" |
| No `~/.ssh/config` | Empty list with "No ~/.ssh/config" |
| One alias fails `ssh -G` | That row is Skipped: "ssh -G failed" |
| Row changed between scan and import | Listed in the result as skipped with its reason |
| Post-write reload fails | The usual `storeError` banner; the hosts are written |

## Testing

Go unit tests run the real `ssh -G` with `-F <temp file>`; the CI runners for Ubuntu and macOS ship an OpenSSH client.

- `Aliases`: `Include` by glob and by absolute path, an `Include` cycle stopping at depth 16, pattern names dropped, duplicates dropped in order, missing file returns empty.
- `Resolve` + `Candidate`:
  - `Host *` supplies the user;
  - an alias without `HostName` resolves to itself;
  - the first existing `IdentityFile` wins, and `agent` is used when none exists;
  - `ProxyJump` and `ProxyCommand` are skipped;
  - an invalid name is skipped;
  - an existing name is "Already in vault";
  - an encrypted key sets `needsPassphrase`.
- `KnownHostKey` against a fixture file:
  - `[host]:2222`;
  - several key types for one host picks ed25519;
  - `hostkeyalias`;
  - a host that is absent;
  - a `@revoked` key is not pinned.
- Hub (`internal/hub`):
  - `import.apply` creates the servers and pins in one write;
  - an alias already in the vault is skipped;
  - locked vault and no vault are refused;
  - the method takes names only, and a planted `host` field in params is ignored;
  - one `import` audit record per server;
  - `import.*` is rejected on the MCP door (added to the existing forbidden-methods test).
- Desktop:
  - vitest (static render): a Ready row has a checked box, a Skipped row shows its reason, the `accept-new` note is present, a passphrase note appears when flagged.
  - Playwright e2e:
    - `sshtestd` gains `-write-known-hosts=<path>`, which writes its host key as a `known_hosts` line.
    - The test points `SSHGATE_SSH_CONFIG` at a fixture config whose alias reaches `sshtestd`, with `UserKnownHostsFile` set to that file, and a second alias that uses `ProxyCommand`.
    - The test imports, sees the first alias pinned in the grid and the second skipped, and opens a tab on the first with no Trust prompt.

## ROADMAP changes

- Slice 2b row: split into "2b-1 SSH config import" (this spec) and "2b-2 ProxyJump" (not specced, entry gate unchanged: a real host behind a bastion).
- Slice 2c and 5: the author does not need them now (2026-09-26).
- The 2b findings about the import (`Host *`, `Include`, alias as hostname, keep-or-refuse `ProxyJump`, name validation and dedupe, `known_hosts` fingerprints) are answered by this spec; the jump-chain findings (handshake timeout, bastion secrets in `redactorFor`, clearing the pin on a `jump` change, one hop first) stay for 2b-2.

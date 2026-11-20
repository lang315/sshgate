# Inbound SSH: who may log in to this Mac, and a record of each login

Date: 2026-11-20. Status: approved in conversation (approach and the per-key IP list); the details marked "my call" are the author's.

## Why

The person wants to let one device (the Herdr app on an iPhone, over Tailscale) open SSH sessions on the Mac that runs sshgate, and wants sshgate's audit log to show every such login. They also want to allow, block and unblock a source IP by typing it.

Today sshgate only connects out. Nothing in it listens for or knows about inbound SSH. Remote Login is off on the Mac.

## Goals

- A list of devices that may log in over SSH, each with its public key and its own source IPs.
- Allow, block and unblock one IP of a device by typing the IP.
- One audit record for every SSH session a listed device opens: shell, command, SFTP and scp.
- No listed device can open a session, or a port forward, that leaves no record.

## Non-goals

- sshgate does not listen on a network port and is not an SSH server.
- sshgate does not turn Remote Login on, and does not edit `sshd_config`. Both need `sudo`; the person does them by hand.
- No Tailscale policy edit and no Tailscale API credential.
- No record of failed or refused login attempts, no logout record, no session recording.
- No live Accept/Deny prompt per connection.
- Lines in `authorized_keys` that sshgate did not write are left alone: not restricted, not recorded.
- macOS only in the app. The hub code builds everywhere.

## Anti-goals

- A login that fails because sshgate could not write the record must not fall back to an unrecorded session.
- The feature must never remove or change a line of `authorized_keys` that sshgate did not write.
- The AI (MCP door) must not be able to list, add, block or unblock anything here.

## Approaches

1. **Forced command per key in `~/.ssh/authorized_keys` (chosen).** Each device is one line: `restrict,pty,from="<IPs>",command="<sshgate> inbound <name>" <key>`. sshd runs `sshgate inbound <name>` in place of the session; it appends the audit record and then runs what the client asked for. No root, no listener. It is the pattern gitolite, GitLab and Borg use. A dry run on a private sshd (OpenSSH 9.9p2, macOS 15.8.3) recorded all six session kinds (command, exit code, tty shell, SFTP, scp, legacy scp) and `restrict` refused a forward-only connection.
2. **`~/.ssh/rc`.** Rejected: the dry run showed sshd does not run it for SFTP or modern scp, so those sessions leave no record.
3. **Tailscale policy edit.** Rejected: needs a tailnet admin credential, has no deny rule, covers only tailnet traffic, and records nothing.
4. **Endpoint Security `openssh_login` events.** Would also record failed attempts and logouts, but needs a root process. Left for later.

## Design

### Data

`config.File` gets `Inbound []InboundDevice` (`json:"inbound,omitempty"`), under the vault MAC like everything else:

```go
type InboundDevice struct {
    Name      string   `json:"name"`      // ValidKeyName rule: 1-64 of A-Za-z0-9._-
    PublicKey string   `json:"publicKey"` // one authorized_keys key: "<type> <base64>", no options, no comment
    Allowed   []string `json:"allowed,omitempty"` // IPs or CIDR prefixes
    Blocked   []string `json:"blocked,omitempty"`
    Created   string   `json:"created"`
}
```

The vault is the source of truth; `authorized_keys` is rendered from it. An IP is in exactly one of `Allowed` and `Blocked`. `vault.create` clears `Inbound` (as it clears keys' trust state) and rewrites the file. The vault `Version` is not bumped; the field is `omitempty`.

### The rendered file

On every inbound write, and on `vault.create`, the hub rewrites `~/.ssh/authorized_keys`:

- The hub writes its lines between two marker comments, `# sshgate inbound begin` and `# sshgate inbound end`, and replaces only that block. Everything outside the block is kept byte for byte. (My call: markers, since matching on a line prefix can misfire on a hand-written line.)
- One line per device that has at least one allowed IP: `restrict,pty,from="<Allowed joined by ,>",command="<abs path of the running sshgate binary> inbound <name>" <PublicKey>`. A device with no allowed IP gets no line, so it cannot log in.
- The file is written atomically (temp file in `~/.ssh`, rename), mode 0600; `~/.ssh` is created 0700 when missing. A file that is a symlink, or not owned by the user, is refused.
- The path is `Options.AuthorizedKeysPath` (hub flag `--authorizedKeys`, default `~/.ssh/authorized_keys`), so tests never touch the real file.
- The binary path comes from `os.Executable()`, overridable for tests. A path with a `"`, a `\` or a newline is refused.

### Validation (trust boundary)

- `name`: `config.ValidKeyName`; unique among devices.
- `publicKey`: parsed with `ssh.ParseAuthorizedKey`; anything with options is refused; the stored form is `ssh.MarshalAuthorizedKey` trimmed, so no comment, quote or newline can reach the file. A key already on another device is refused.
- `ip`: `netip.ParseAddr` or `netip.ParsePrefix`, stored in its canonical string. `0.0.0.0/0` and `::/0` are refused (my call: "any address" defeats the list; type real addresses). At most 32 entries per device.

### UI door (protocol 15)

All need an unlocked vault (`ErrLocked`, or "create a vault first"), strict params, `h.mu` across `config.Update`, the reload and the file render; each writes one `kind: "config"` audit record. None is on the MCP door.

- `inbound.list` → `{devices: [{name, publicKey, fingerprint, allowed, blocked, created}], authorizedKeys: <path>}`.
- `inbound.add {name, publicKey, ip}`: new device with one allowed IP. Action `inboundAdd`.
- `inbound.delete {name}`. Action `inboundDelete`.
- `inbound.setIP {name, ip, state}` with `state` one of `allowed`, `blocked`, `removed`: adds the IP in that state, moves it, or forgets it. Actions `inboundAllow`, `inboundBlock`, `inboundForget`, with `ip`.

If the vault write succeeds and the file render fails, the call returns an error that starts `saved, but authorized_keys was not updated: ` (the same shape as the auto-allow save), and `inbound.list` reports `fileError`.

### The forced command: `sshgate inbound <name>`

Routed in `cmd/sshgate/main.go`. It never reads the vault.

1. Read `SSH_CONNECTION` (client IP and port) and `SSH_ORIGINAL_COMMAND`. With no `SSH_CONNECTION` it exits 1: it was not started by sshd.
2. Append one line to `audit.jsonl` next to the default store (`SSHGATE_STORE` honoured as elsewhere), opened `O_APPEND|O_WRONLY|O_CREATE` 0600, one `Write` call:
   `{"time", "kind": "login", "device": <name>, "from": "<ip>:<port>", "session": "shell"|"command"|"sftp", "tty": bool}`.
   `session` is `shell` with no original command, `sftp` when the original command's base name is `sftp-server` or `internal-sftp`, else `command`. The command text is not recorded (my call: it can hold secrets; the person can ask for it later).
3. If the write fails, print `sshgate: could not record this login` to stderr and exit 1. No session runs.
4. Otherwise replace the process (`syscall.Exec`) with the user's login shell: `$SHELL -l` for a shell session, `$SHELL -c "$SSH_ORIGINAL_COMMAND"` otherwise. `SHELL` falls back to `/bin/sh`.

The hub tolerates the outside append (it recounts when the file size changed, `TestAuditSeqSurvivesExternalEdits`). The app shows the record after Refresh; it is not pushed live.

### App

- A sidebar item "Inbound" after Keychain (`model.InboundTab`), macOS only, built like the Keychain view: `inbound.list` is called only from a user action (entering the view, after a write), never from `build`, a timer or a redraw.
- A fixed note at the top: `These devices can log in only when Remote Login is on. The list protects this Mac only when password login is off.`
- One card per device: name, key type and fingerprint, each IP with its state and a button (`Block` on an allowed IP, `Unblock` on a blocked one, `Remove`), a field `IP address` with `Allow` and `Block` buttons, and `Delete device` (a `decisionButton` with `toneDangerFill`, in a confirm dialog whose focused default is Cancel, as for a Keychain key).
- `New device` dialog: name, public key (multi-line paste), first IP.
- Audit view: a `Logins` filter chip (`Kind: "login"`), and a row for the record: main text `<device> from <ip>`, detail session kind and tty.

### Tests

- `config`: IP and key validation; the allowed/blocked invariant; `vault.create` clears the list.
- Hub: each door method, locked and no-vault refusals, strict params, the MCP door has no inbound method, audit records carry no key material beyond the fingerprint; the rendered file keeps foreign lines byte for byte, replaces only the block, omits a device with no allowed IP, refuses a symlink.
- `sshgate inbound`: writes the record and execs (shell, command, sftp); exits 1 with no session when `SSH_CONNECTION` is missing or the audit file cannot be written.
- One end-to-end test with the system `sshd` on loopback when `/usr/sbin/sshd` exists (skipped otherwise): a listed key logs in and the record appears; an IP not in `from=` is refused; a forward-only connection carries no data.
- App: the view's flows with a fake door; no `inbound.list` from `build`.

## Left to the builder

File and function names beyond those above, the card layout details within DESIGN.md's rules, and the wording of error strings not fixed here.

## Hand checks (cannot be automated)

With Remote Login on and password login off: Herdr on the iPhone connects from an allowed IP and the Audit view shows the login; after Block it is refused; a key added by hand outside the block still works and is not recorded.

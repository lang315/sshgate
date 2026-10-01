# sshgate

**sshgate** lets an AI agent (Claude Code or any MCP client) run shell commands on your SSH servers, but only after you approve each command in a desktop app by default (see [Auto-allow](#auto-allow) for the one opt-in exception). It is one Go binary plus an Electron app:

- the **bridge** (`sshgate`), which your MCP client starts;
- the **hub** (`sshgate hub`), which holds an encrypted vault of saved servers, the SSH connections, and the approval queue;
- the **desktop app** (`desktop/`), which runs the hub and gives you SSH terminal tabs next to the AI's pending requests.

Every AI command is approved by a human by default. You may put one host on auto-allow (plain exec by default; root hosts and sudo-exec only when you opt in per host) for a set time, or until turned off with a Resume after each unlock; it is visible while on, audited, and stoppable at any time. The AI only sees servers you mark visible, and only connects to a server whose host key you have pinned.

sshgate started as a Go rewrite of [tufantunc/ssh-mcp](https://github.com/tufantunc/ssh-mcp) (MIT) and is now a separate project, not affiliated with or endorsed by it.

## Contents

- [How it works](#how-it-works)
- [Quick start](#quick-start)
- [The desktop app](#the-desktop-app)
- [Safety model](#safety-model)
- [Tools the AI gets](#tools-the-ai-gets)
- [Approving without the app: `hub --cli`](#approving-without-the-app-hub---cli)
- [Standalone mode: `--host`](#standalone-mode---host)
- [Files and environment variables](#files-and-environment-variables)
- [Moving from the `ssh-mcp` name](#moving-from-the-ssh-mcp-name)
- [Development](#development)
- [License](#license)

## How it works

```
Claude Code ──stdio──▶ sshgate (bridge) ──per-user socket──▶ sshgate hub ──SSH──▶ your servers
                                                                  ▲
                                          desktop app ──stdio─────┘  (unlock, approve, terminals)
```

1. The AI calls `exec` through the bridge. The bridge holds no secrets and makes no decisions; it forwards the call to the hub over a socket only your user can open.
2. The hub checks that the server exists, is visible to AI, has a pinned host key, and that the vault is unlocked. Then it queues the request, unless the host is on [auto-allow](#auto-allow), in which case it runs at once.
3. The app shows the request: server, `user@host:port`, the exact command, and the AI's description (marked unverified). You **Deny**, **Allow**, or **Send to tab**.
4. On Allow, the hub runs the command over its cached SSH connection, masks every saved secret and every value that looks like a secret (private keys, passwords, tokens) in the output, caps each stream at 64 KiB, writes an audit record, and returns the result to the AI.

## Quick start

**Prerequisites:** Go (the version in `go.mod`), Node.js 22.12 or newer (Electron 44 requires it), and an MCP client such as Claude Code.

1. Build the binary and start the app:

        go build -o sshgate ./cmd/sshgate
        cd desktop && npm ci && npm start

   Put a copy of `sshgate` on your `PATH` too (for example `cp sshgate ~/go/bin/`), so your MCP client can start the bridge.

   On macOS you can instead build an app bundle with `cd desktop && npm ci && ./scripts/package-mac.sh` and copy `desktop/out/sshgate.app` to `/Applications` (quit sshgate first when replacing an installed copy). `sshgate --version` prints the version it was built with. It carries its own `sshgate` binary (`sshgate.app/Contents/Resources/sshgate`), which the bridge can use too. The bundle is ad-hoc signed for your own machine only; it is not notarized.

2. **Create your vault.** The first launch asks for a master password (at least 8 characters). It cannot be recovered; lose it and the vault cannot be opened.

3. **Add a host.** On the **Hosts** tab click **New host**: Address, optional Label (defaults to the address), port, User, and Password. `+ Key or agent` switches to key or agent authentication.

   Already use `ssh`? Click **Import from SSH config** instead. It lists the hosts in `~/.ssh/config` and pins each host key from your user known_hosts files (`UserKnownHostsFile`, normally `~/.ssh/known_hosts`), so the first connect asks nothing. Hosts behind `ProxyJump` or `ProxyCommand` are skipped. Passwords are not in `ssh_config`: add them in the editor afterwards.

4. **Connect once and trust the key.** Click the host card. The app shows the server's `SHA256:` fingerprint, its key type, and whether `~/.ssh/known_hosts` lists the same key. Check it, then click **Trust and connect** (mouse only; **Cancel** is the default). The key is now pinned.

5. **Let the AI see it.** Edit the host, open **AI access**, and turn on **Visible to AI**.

6. **Register the bridge** with Claude Code, with no flags:

        claude mcp add --transport stdio sshgate -- sshgate

7. Ask Claude to run something on that host, then approve it in the app.

Keep the app open while the AI works. When the app is closed, every tool call fails with "Open the app to approve commands".

## The desktop app

- **Hosts** is the first tab: a searchable grid of host cards. Click a card to open a terminal. Chips along the bottom of a card mark servers visible to **AI**, servers with a **New key** (not pinned yet), running tunnels, and an auto-allow grant (**Auto 42m**, **Auto ∞**, **Auto paused**); the Files, Tunnels, Edit and Delete buttons appear beside them on hover, without moving anything. The footer shows the vault file's path; copying that file is your backup.
- **Terminal tabs** open from a host card. They keep their SSH sessions when the vault locks, and offer **Reconnect** if the hub restarts.
- **Files** opens an SFTP browser tab from a host card: list, upload and download files or whole folders (or drop them from Finder), make folders, rename, and recursive delete. Everything runs as the host's login user, and every change and transfer is audited.
- **Tunnels** opens a tab per host for SSH port forwards, saved with the host in the vault: **Local** (a port on this machine reaches a host and port as seen from the server), **Remote** (a port on the server reaches one on this machine), and **Dynamic** (a SOCKS5 proxy here; connections leave from the server). Local listeners bind to loopback only. Tunnels keep running while the vault is locked, stop when the connection drops or the host is edited, and every start and stop is audited. Tunnels are yours alone: the AI cannot see or start them.
- **Audit** is the second fixed tab, after Hosts: the audit log (`audit.jsonl`) newest first, with every AI request and its outcome, auto-allowed runs, host and auto-allow changes, file operations, and tunnels. Filter by host, by the **Exec**, **Auto**, **Denied**, **Config**, **Files** and **Tunnels** chips (several on shows all of them), or by text; click a row for the full command, the AI's description, and the raw record. New records appear while the tab is open. It needs an unlocked vault, forgets what it showed when the vault locks, and changes nothing; the footer shows the file's path.
- **AI requests** is a column on the right. It opens itself when a request arrives and closes only when you close it; closing it never denies or drops a request, and the **AI** button in the tab bar turns amber while requests wait.
  - **Deny** is the default: Enter in the reason field denies, and the reason goes back to the AI.
  - **Allow** and **Send to tab** need a mouse click and stay disabled for 500 ms after anything in the list changes or scrolls, so nothing is clickable the instant it moves under your cursor.
  - **Send to tab** pastes the command into your own terminal for that server instead of running it.
  - Sudo requests have a red edge. Non-ASCII characters in a command are highlighted with their code points (`U+0456`), so a look-alike `gіthub.com` stands out.
  - From a terminal, `Ctrl+Shift+A` (`Cmd+Shift+A` on macOS) jumps to the oldest request's reason field; `Esc` returns to the terminal.
  - Below the requests, **Auto-allowed** lists the last 50 commands that ran on auto-allow: server, a **SUDO** tag for sudo-exec, exit status, time, the full command, and the AI's description. **Stop all auto-allow** ends every grant.
- **Host editor.** It never shows a saved password: leave a field empty to keep it, or click **Clear** to remove it. Changing the address or port forgets the pinned key and every password you don't re-enter, closes that server's tabs, and denies its waiting requests.
- **Host key changed.** A server that presents a different key is refused, and the app shows both fingerprints. If the change was expected, click **Forget host key** in the editor and connect again.
- **Themes.** Dark, light, or **Auto** (follows the OS), from the ☾ / ☀ / Auto control.
- **Locking.** After 15 minutes with no activity and nothing pending, the vault locks itself; **Lock** does it by hand. A timed auto-allow grant holds this off until its deadline (a "Until turned off" grant does not). While locked, the AI's calls fail and terminals keep running.
- **Notifications.** When the window is not focused, a new request shows an OS notification and a count on the tray icon.

### Auto-allow

In a host's editor, open **AI access** (the host must be visible to AI and have a pinned key) and pick a duration in the **Auto-allow** select: 15 min, 30 min, 60 min, 2 h, 4 h, or "Until turned off" (which needs the host name typed to confirm in the confirm dialog Save shows you), then Save. Plain `exec` calls run without a click for that long; the confirm dialog's **Enable** is mouse-only and waits 500 ms, like **Allow**. **Stop** on the host card (always shown while a grant is on), **Stop all auto-allow** in the AI column, locking the vault, or the timer running out all end it. Every run still shows in the AI column's Auto-allowed feed and in the audit log.

Two opt-ins in the same section, off by default and independent of each other, widen what a grant covers:

- **Allow on root hosts** lets a grant run on a `root` login, or a host with a stored su or sudo password. Honest risk: the AI runs as root, and a command it plants can capture the su or sudo password you type or store.
- **Also auto-allow sudo-exec** lets `sudo-exec` run without asking on a granted host, the same as plain exec. Honest risk: the AI has full root on that host for as long as the grant runs.

A grant "Until turned off" is paused after every unlock until you click **Resume**. If the hub refuses a grant when you save (for example a `root` host without **Allow on root hosts**), the host edits are still saved and the app tells you auto-allow was not turned on.

Closing the window quits the app and stops the hub. There is no Reload; if the renderer crashes, the app locks the vault and reloads at the unlock screen.

## Safety model

- A human decides every AI command by default. The one exception is a host put on auto-allow: a grant is not a privilege boundary, and anything it leaves running (cron jobs, SSH keys, shell startup files) outlives the grant. If sudoers keeps a global timestamp (`timestamp_type=global`, or an old sudo with `!tty_tickets`), a human running sudo in a terminal lets auto runs use `sudo -n` as root for that ticket's lifetime; sshgate cannot see this. A `sudo` function planted during a grant can capture a password the human types in a terminal on that host.
- The AI sees only servers marked **Visible to AI**, and only while the vault is unlocked.
- Every connection from the hub verifies a pinned host key. The hub never learns a key on its own; you pin it by clicking **Trust** after seeing the fingerprint.
- An approval is bound to the `user@host:port` and key you saw. If the server is edited while a request waits, the request fails with "server changed".
- The vault uses Argon2id for the master key, AES-GCM with per-field authenticated data for each secret, and an HMAC over the whole file. A tampered or corrupt vault file is refused, never overwritten.
- The master password is never read from a file or environment variable, only typed into the app (or `hub --cli`).
- Saved secrets are masked in all command output. The audit log (`audit.jsonl`, next to the vault) records every request and decision, never command output; an allowed run's record counts what was redacted, by kind.
- Redaction is a seatbelt, not a boundary, and it is always on with no opt-out. Before output reaches the AI, sshgate also masks:
  - private key blocks;
  - the value after a key whose name has a password word (`password`, `passwd`, `pwd`, `passphrase`, or `pass` last) or a secret word (`secret`, or `token`, `auth`, `apikey`, `credential(s)`, `api_key`, `access_key`, `private_key`, `secret_key`, `app_key`, `encryption_key`, `signing_key`, `master_key`, `session_key`, `hmac_key` last), plus redis `requirepass` and `masterauth`; other `*_KEY` names (`JWT_KEY`, `LICENSE_KEY`, `PIN`) are not covered. A key whose last word names a setting about a secret, not the secret, is kept (`secretName`, `secret_ref`, `password_file`, `password_min_length`, `PASSWORD_MAX_AGE`, and similar `name`, `ref`, `file`, `path`, `length`, `min`, `max`, `age`, `days`, `count`, `attempts`, `retries`, `policy`, `timeout`), and so is an nsswitch `passwd: files systemd` line. The same rules apply to JSON escaped inside a string (`{\"password\":\"x\"}`, as in `kubectl` annotations and docker logs) and to the quoted elements of a one-line array (`{"Authorization":["Bearer x"]}`);
  - credential headers (`Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key`; `Authorization` also as `HTTP_AUTHORIZATION=…` or `Authorization="…"`), URL passwords, and well-known token families: AWS (`AKIA`, `ASIA`), GitHub (`ghp_`… and `github_pat_`), GitLab (`glpat-`), Slack (`xox…`), `sk-`, Stripe secret and restricted keys (`sk_live_`, `sk_test_`, `rk_live_`, `rk_test_`), and JWTs. Stripe `whsec_` and `pk_` and any other prefix are not handled.

  It misses:
  - a secret with no key and no known prefix (a password alone on a line), `.pgpass` lines, secrets split across lines (except PEM blocks), and anything encoded (`base64`, `xxd`, `rev`, `gzip`);
  - WordPress `define('DB_PASSWORD', …)`, XML `<password>`, `curl -u user:pw`, `mysql -pPW`, short `-p value` flags (`sshpass -p`), a one-line `.netrc`, `/etc/shadow` hashes, and an unencoded `@` inside a URL password;
  - a value the keep rules leave alone: one starting with `$` and an identifier or with `%s`/`%d`/`%v`/`%q`, letters followed by `(` or `[`, an absolute or `~/` path (not one holding `+` or ending in `=`, which is base64), or a placeholder such as `changeme`, `none` or `yes`, so a real password like `$ecr3t!` or `Hunter[2x9` is not masked;
  - an unquoted value with spaces, masked only up to the first space;
  - a lone `-----END … PRIVATE KEY-----` whose BEGIN the output cap cut off: the text from the start of the kept tail through it is masked, which can hide ordinary text before it. On output the cap does not cut, a lone END (a `grep` hit, source code) is left alone;
  - arrays that span lines or nest;
  - `auth: {password hunter2x}` (a flow map holding a space-separated pair): the value stays in clear while a mask is counted.

  A prompt-injected AI can encode output to get it past redaction; the auto-allow warnings above still apply. To see a real value yourself, use **Send to tab**: the command then runs in your terminal and its output never reaches the AI.
- The socket between bridge and hub is per-user and checked both ways (UID on Unix, SID on Windows). The AI's side can list servers and submit commands; it cannot unlock, approve, or read secrets.

## Tools the AI gets

| Tool | Arguments | Notes |
|---|---|---|
| `list-servers` | none | Servers visible to AI, one per line; `[locked: unlock the app]` while the vault is locked. No approval needed. |
| `exec` | `server` (required, a name from `list-servers`), `command`, `description` (optional, at most 500 bytes), `timeoutSec` (1–600, default 60) | Waits for your decision for up to 5 minutes, then fails as expired. Runs at once on a host with an auto-allow grant. |
| `sudo-exec` | same as `exec` | Runs `sudo -S` with the saved sudo password, or `sudo -n` if none is saved. Always waits for you, unless the host has a grant and **Also auto-allow sudo-exec** is ticked. |

- Each command runs in a fresh non-interactive shell, so `cd` and environment variables do not carry over between calls; combine steps into one command. The exception is a server with a saved **su** password: its commands run inside one persistent root shell.
- The result is `exit code: N`, then `stdout:` and `stderr:` sections. A non-zero exit code is a normal result, not a tool error.
- Values that look like secrets are replaced by `[REDACTED:<kind>]`, where the kind is `private_key`, `password`, `secret`, `auth_header`, `url_password` or `token`; a key and its separator stay (`DB_PASSWORD=[REDACTED:password]`). The result then ends with one line such as `note: sshgate redacted 3 values (private_key ×1, password ×2); the values are withheld from AI clients`. Saved secrets show as `***` and are not counted. The same applies in standalone `--host` mode.
- The `description` is shown to you and written to the audit log. It is never executed.

**Client timeouts.** Claude Code does not give up before the 5-minute approval window: its hard per-call limit (`MCP_TOOL_TIMEOUT`) defaults to about 28 hours, and its idle limit for stdio servers is 30 minutes. Other clients may use shorter timeouts; raise them above 5 minutes if calls fail while a request is still waiting.

## Approving without the app: `hub --cli`

On a machine without a display, run the hub in a terminal and approve from there:

    sshgate hub --cli
    # a [id]=allow  d [id] [reason]=deny  D [reason]=deny all
    # s [id]=send to tab (prints the command for you to paste)
    # u=unlock  p=list pending  q=quit   (the id is needed only when 2+ requests wait)

`hub --cli` cannot create a vault or edit hosts. Create and fill the vault in the desktop app on any machine, then copy `servers.json` to the headless one: the file is portable and opens with the same master password (hosts that use a key file need it at the same path).

`sshgate hub` also accepts `--store=<path>` (a vault other than the default), `--sshConfig=<path>` (the SSH config to import from), and `--idleLock=<duration>` (a Go duration of at least `1s`, replacing the 15-minute default). It refuses `--insecureIgnoreHostKey`.

## Standalone mode: `--host`

For a single throwaway server with no vault, no hub, and **no approval step**, pass the connection on the command line:

    claude mcp add --transport stdio sshgate -- sshgate --host=192.168.1.100 --user=admin --password=secret

In this mode the AI's commands run immediately. Use it only where that is acceptable.

| Flag | Meaning |
|---|---|
| `--host`, `--user` | Required. |
| `--port` | Default 22. |
| `--password` or `--key` | Password, or path to a private key. |
| `--sudoPassword` | Password for `sudo-exec`. |
| `--suPassword` | Run `exec` inside a persistent `su` root shell. |
| `--disableSudo` | Remove the `sudo-exec` tool. |
| `--timeout` | Per-command timeout in milliseconds (default 60000). |
| `--maxChars` | Maximum command length (default 1000; `none` or `0` for no limit). |
| `--insecureIgnoreHostKey` | Skip host-key checks. Otherwise the first key seen is trusted for the rest of the session. |

In this mode, `exec` appends the `description` to the command as a shell comment, and `list-servers` shows the one connection.

The same flags work in any MCP client's JSON config:

```json
{
  "mcpServers": {
    "sshgate": {
      "command": "sshgate",
      "args": ["--host=1.2.3.4", "--user=root", "--key=/path/to/key"]
    }
  }
}
```

## Files and environment variables

| Path | What |
|---|---|
| `~/.config/sshgate/servers.json` | The vault (mode 0600). Copy it to back up. |
| `~/.config/sshgate/audit.jsonl` | Audit log, one JSON record per line: every request and its outcome (`waitMs` is how long it waited for you; auto-allowed runs carry `approval: "auto"`), host and auto-allow changes, file transfers, and tunnels. Never command output. |
| `$SSHGATE_RUNTIME_DIR/sshgate/hub.sock`, else `/run/user/<uid>/sshgate/hub.sock` (Linux), else `/tmp/sshgate-<uid>/hub.sock` | Bridge-to-hub socket. On Windows, the named pipe `\\.\pipe\sshgate-hub-<SID>`. |

| Variable | Read by | Effect |
|---|---|---|
| `SSHGATE_RUNTIME_DIR` | hub and bridge | Where the socket lives. Set the same value for both, including in the MCP client's environment. `TMPDIR` and `XDG_RUNTIME_DIR` are ignored on purpose. |
| `SSHGATE_BIN` | desktop app | The `sshgate` binary to run as the hub. Default: `sshgate` one directory above `desktop/` (or inside the macOS bundle), then `PATH`. |
| `SSHGATE_STORE` | desktop app | Passed to the hub as `--store=`. |
| `SSHGATE_SSH_CONFIG` | desktop app | Passed to the hub as `--sshConfig=`: the SSH config **Import from SSH config** reads (default `~/.ssh/config`). |
| `SSHGATE_IDLE_LOCK` | desktop app | Passed to the hub as `--idleLock=`, for testing. |

The installed macOS app ignores every `SSHGATE_*` variable above and removes them from the hub's environment, so whoever can set your environment cannot swap the hub that receives your master password.

## Moving from the `ssh-mcp` name

Earlier builds of this code were named `ssh-mcp`. To keep your vault and saved servers, move the config directory once and re-register the bridge:

    mv ~/.config/ssh-mcp ~/.config/sshgate
    claude mcp remove ssh-mcp; claude mcp add --transport stdio sshgate -- sshgate

The vault format is unchanged. Environment variables are now `SSHGATE_*` instead of `SSH_MCP_*`.

## Development

    go vet ./...
    go test -short ./...          # unit tests
    go test -race ./...           # plus SSH integration tests (Docker, via testcontainers); what CI runs
    cd desktop && npm run typecheck && npm test   # desktop unit tests
    cd desktop && npm run e2e                     # Playwright end-to-end tests (needs a display)

Without Docker, the integration tests skip themselves instead of failing. `go run ./internal/sshx/sshtest/sshtestd -write-store=<path> -password=<pw>` starts a fake SSH server with a ready vault for trying the app. Design specs, plans, and the roadmap are in `docs/superpowers/`; `CLAUDE.md` describes the architecture. See [CONTRIBUTING.md](./CONTRIBUTING.md) and the [Code of Conduct](./CODE_OF_CONDUCT.md).

## License

MIT, see [LICENSE](./LICENSE). Use at your own risk. sshgate is not affiliated with or endorsed by any SSH or MCP provider, or by the original ssh-mcp project.

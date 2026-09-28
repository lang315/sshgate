# sshgate: Port Forwarding — Design (Slice 3, part b)

Date: 2026-09-27
Status: Approved 2026-09-27; implemented (plan `plans/2026-09-27-slice3b-port-forwarding.md`).
Depends on: `2026-09-24-desktop-app-design.md` (slice 1), `2026-09-25-desktop-slice2a-design.md` (slice 2a), `2026-09-26-slice3a-sftp-design.md` (slice 3a). Everything there still holds unless this document changes it by name.

## Goal

Save tunnels on a host and start or stop them from the app, over the same SSH connection the host's terminal and Files tabs use: local (`ssh -L`), remote (`ssh -R`), and dynamic (`ssh -D`, SOCKS5).

Exit gate: on a real host (e.g. `buildpc`), from the app, the author saves a local tunnel and a dynamic tunnel, starts both, uses a real service through each, locks the vault and sees them keep working, then stops them.

## Scope

In: tunnels saved per host in the vault; a Tunnels tab per host (list, add, edit, delete, start, stop, live status and open-connection count); a running-tunnels badge on the host card; audit records for every save, delete, start, and stop.

Out, recorded in ROADMAP:

- Starting tunnels automatically (on unlock, on app start, on connect).
- Listening on anything but loopback, locally or on the server (`GatewayPorts`-style sharing).
- Remote dynamic forwarding (`ssh -R` with SOCKS), UDP, Unix sockets.
- AI access to tunnels. No `tunnels.*` method exists on the MCP door.
- Reconnecting a tunnel after its connection drops. The author starts it again.
- Opening a URL through a tunnel, per-tunnel byte counters, bandwidth limits.
- The host-key prompt from the Tunnels tab. A host whose key is not yet trusted is opened once from a terminal tab first.
- ProxyJump (slice 2b-2).

## Decisions made in chat

1. Tunnels are **saved per host**, not ad hoc.
2. Local and dynamic tunnels listen **only on loopback**; remote tunnels ask the server to listen only on its loopback.
3. Running tunnels **keep running when the vault locks** (manually or by the idle lock), like open terminal tabs. No new tunnel can start while locked.
4. Tunnels run **in the hub**, on the `sshx.Manager`'s shared `ssh.Client`. Not in Electron (a second SSH stack and secrets outside the hub) and not by spawning the system `ssh` (no vault, no pin).

## Data model

`config.Server` gains `Tunnels []Tunnel` (`json:"tunnels,omitempty"`):

```go
type Tunnel struct {
    ID         string `json:"id"`                   // 16 hex chars, chosen by the hub
    Kind       string `json:"kind"`                 // "local" | "remote" | "dynamic"
    ListenPort int    `json:"listenPort"`           // 1..65535
    TargetHost string `json:"targetHost,omitempty"` // empty for dynamic
    TargetPort int    `json:"targetPort,omitempty"` // 0 for dynamic
    Label      string `json:"label,omitempty"`
}
```

- **Meaning of each kind.**
  - `local`: the hub listens on `127.0.0.1:listenPort`, and each connection goes through the server to `targetHost:targetPort`, which is resolved and dialled by the server.
  - `remote`: the server listens on its `127.0.0.1:listenPort`, and each connection comes back to the hub, which dials `targetHost:targetPort` from the author's machine.
  - `dynamic`: the hub runs a SOCKS5 server on `127.0.0.1:listenPort`.
- **Tunnels are not secrets.** They are stored in plain text inside the MAC-covered file and have no AAD.
- **Validation** (`Tunnel.Validate`) names the first bad field:
  - `kind` must be one of the three.
  - Ports must be in range.
  - `local` and `remote` need a `targetHost` that passes `plainToken` and is at most 253 bytes, plus a `targetPort`. An IPv6 literal is written without brackets; the hub joins with `net.JoinHostPort`.
  - `dynamic` must have no target.
  - `label` is at most 64 bytes, with no control or format runes.
  - A server holds at most 32 tunnels.
  - Two tunnels of one server may not share a `kind` + `listenPort` pair. A clash with another server's tunnel shows up only at start, as "port in use".
- **`servers.save` never touches tunnels.** `config.ApplyServer` builds the new `Server` from `ServerInput`, which has no tunnels. It must copy `before.Tunnels` into `after`, so editing a host keeps its tunnels, including on a host or port change and on a rename.
- **`dialChanged` must ignore `Tunnels`.** `config.Server` stops being comparable once it holds a slice, so `dialChanged` clears `Tunnels` (and `AIVisible`) on both copies and compares the rest field by field.

## Hub: UI-door methods (protocol 5)

All params are decoded with `strictParams` (exact keys, no duplicates). `ProtocolVersion` becomes 5.

| Method | Kind | Needs | Does |
|---|---|---|---|
| `tunnels.list {}` | request | nothing | Every server's saved tunnels, each with its runtime `status` (`stopped`, `starting`, `running`, `error`), `error` text, and open `conns`. Works while locked and with no vault (then `[]`). |
| `tunnels.save {server, tunnel}` | request | unlocked vault | Adds (`id` empty: the hub picks one) or replaces a tunnel through `config.Update`, then `Reload`. Refuses to replace a tunnel that is running or starting ("stop the tunnel first"). Writes a `kind: "config"` audit record. |
| `tunnels.delete {server, id}` | request | unlocked vault | Stops the tunnel if it runs, then removes it through `config.Update`. Writes a `kind: "config"` audit record. |
| `tunnels.start {server, id}` | request | unlocked vault, pinned host key | Checks in this order: vault exists, unlocked, server exists, tunnel exists and is not running, host key pinned. Then resolves and dials strict through the `Registry` (`Manager.ensure`), binds, and answers once the tunnel is listening; a failure answers as an error and leaves the tunnel `error`. |
| `tunnels.stop {server, id}` | notification | nothing | Closes the listener and every open connection of the tunnel. Works while locked. |
| `tunnels.state {server, id, status, error?, conns}` | hub → UI | — | Sent on every status change, and at most once a second while only `conns` changes. |

- **Errors from start, shown to the author as they are:**
  - "port 5433 is already in use" (the local bind failed);
  - "the server refused the remote forward" (`tcpip-forward` was rejected);
  - "open a terminal to this host once to trust its host key" (no pin, or `*HostKeyUnknownError`);
  - "host key changed" (`*HostKeyMismatchError`);
  - "connection failed".

  The detail goes to stderr and the audit reason. It is the same detail the terminal tab already shows the author, so nothing is hidden here that the author does not already see.
- **Idle lock.** Every `tunnels.*` request counts as UI activity. `tunnels.stop` does not count. Traffic through a tunnel never counts.

## Hub: runtime

A new `internal/tunnel` package holds everything that is not hub policy:

- `Local(client, listenAddr, target)` and `Remote(client, listenAddr, target)` return a running forward.
- `Dynamic(client, listenAddr)` returns a running SOCKS5 server. It supports version 5, method "no auth", and the `CONNECT` command only, with IPv4, IPv6, and domain-name addresses. A domain name is handed to the server unresolved, so DNS happens remotely, as with `ssh -D`. Anything else gets the matching SOCKS reply code and a close.
- Each forward can report its open-connection count, and can be stopped with `Close()`, which closes the listener and every open connection.
- Copying uses `io.Copy` in both directions. A direction that ends at a clean EOF half-closes its destination (`CloseWrite`), so the other direction can still carry a reply; an error, or a destination with no `CloseWrite`, closes both halves. `Close()` closes every accepted connection and its dialed peer, so a pipe stuck on a half-closed connection's peer still ends. Dials to the target time out after 10 s and are cancelled when the tunnel stops. Accept errors from EMFILE or ENFILE (out of file descriptors) are retried with exponential backoff (5 ms doubling to 1 s) instead of ending the tunnel. A half-closed connection keeps counting in `conns` until the other side closes.

The hub (`internal/hub/tunnels.go`) keeps a map from `server/id` to the running forward:

- **Dead connection.**
  - Starting a tunnel also starts the manager's keepalive (`StartKeepalive(30s, nil)`, as Files does).
  - A goroutine waits on the `ssh.Client` the tunnel was started on (`client.Wait()`). When that returns, the tunnel ends with `error` "connection lost".
- **Server changed.** `servers.save` (when `dialChanged`), `servers.delete`, and `servers.forgetHostKey` end that server's tunnels with `error` "server changed" before closing its connection, alongside the existing file-job ending.
- **Replaced connection.** A `Registry` that replaces a manager (its config changed) closes the old client. Any tunnel still on it then ends with "connection lost" through the `Wait` path, so no extra hook is needed.
- **Lock and unlock** leave running tunnels alone.
- **Hub exit.** When the hub exits (stdin closed, crash), the OS closes the listeners. The restarted hub has no running tunnels, and the renderer calls `tunnels.list` again after every restart.
- **Renderer reload.** `recoverRenderer` does not stop tunnels, since they do not belong to the renderer.

**Audit.** Each tunnel writes a `kind: "tunnel"` start record and a matching end record. The start record holds `server`, `target` (`user@host:port`), tunnel `kind`, `listen`, `to`, and `id`. The end record adds the reason and the total connections served. There is no per-connection record.

## Desktop

- **Tab.** `Tab.kind` gains `'tunnels'`.
  - Each host card gets a Tunnels button (a `⇄` icon, next to Files). It opens `⇄ <name>`, or switches to that host's Tunnels tab if one is already open.
  - Closing the tab does not stop its tunnels.
- **`TunnelsView.tsx`** lists the server's tunnels. Each row shows:
  - the label;
  - a one-line summary from `tunnels.ts` `summary()`: `L 127.0.0.1:5433 → db:5432`, `R server 127.0.0.1:8080 → localhost:3000`, or `D 127.0.0.1:1080 SOCKS5`;
  - the status (running, stopped, or error with its text);
  - `conns`;
  - Start or Stop.
- **Buttons.** Add, Edit, and Delete sit on the toolbar.
  - Start, Add, Edit, and Delete are disabled while locked.
  - Stop is never disabled.
  - Edit and Delete of a running tunnel are disabled ("stop it first").
- **`TunnelDialog.tsx`** has these fields: kind (three radio buttons), listen port, target host and port (hidden for dynamic), and label.
  - `tunnels.ts` `parseTunnelForm()` checks the same rules as the hub.
  - Save sends `tunnels.save`, and the list refreshes from its reply.
- **State.**
  - `App` holds all tunnel states. It calls `tunnels.list` once on mount and again after every hub restart, then applies `tunnels.state` notifications through the existing single subscription.
  - The renderer never polls.
- **Host card badge.** A card shows a green dot with the count of running tunnels, whether or not its Tunnels tab is open.
- **Plumbing.** `shared/protocol.ts` adds `tunnels.list`, `tunnels.save`, `tunnels.delete`, and `tunnels.start` to `REQUEST_METHODS`, and `tunnels.stop` to `NOTIFY_METHODS`. The hello check expects protocol 5. No local paths are involved, so main only relays.
- **Server text.** Labels and error text go through `displayText`.

## Security rules

- **No non-loopback listener, ever.**
  - The hub binds `127.0.0.1` (and nothing else) for local and dynamic tunnels.
  - A remote tunnel's `tcpip-forward` always asks for `127.0.0.1`.
  - The bind address is not a field, so it cannot be set to anything else.
- **Who can use a running tunnel.**
  - Any local process can use a local or dynamic tunnel while it runs, exactly as with `ssh -L` and `ssh -D`.
  - Any process on the server can reach a remote tunnel's target while it runs.
  - The Tunnels tab says this in one line under the list.
- **A tunnel starts only on a pinned host, dialled strict.** No path here learns a host key.
- **The AI cannot see, start, or use tunnels** through sshgate. The MCP door is unchanged.
- **Saved tunnels are written only through `config.Update`** while unlocked, and each save is audited.

## Testing

- **`internal/tunnel`**, tested against an in-process SSH server:
  - local and remote echo round trips;
  - SOCKS5 CONNECT by IPv4 and by domain name;
  - an unsupported SOCKS command gets reply `0x07`;
  - `Close` ends open connections;
  - the connection count.
- **`internal/sshx/sshtest`** gains `direct-tcpip` channels and `tcpip-forward` / `cancel-tcpip-forward` global requests (loopback only), so `sshtestd` supports tunnels for the e2e tests too.
- **`internal/hub`:**
  - save, list, and delete, including a refused edit of a running tunnel;
  - start while locked is refused;
  - an unpinned host is refused;
  - "port in use";
  - a tunnel keeps running across a lock;
  - `servers.save` of the host ends its tunnels with "server changed";
  - the client dying ends them with "connection lost";
  - `servers.save` keeps the tunnels;
  - strict params;
  - start and end audit records.
- **`internal/config`:** `Tunnel.Validate` cases; `ApplyServer` keeps `Tunnels`; `dialChanged` ignores them.
- **vitest:** `summary()` and `parseTunnelForm()`.
- **Playwright** (`e2e/tunnels.spec.ts`, against `sshtestd`):
  - add a local tunnel to a Node echo server, start it, send bytes through it, and see `conns` change;
  - add a dynamic tunnel and CONNECT through it with a hand-written SOCKS5 handshake;
  - lock the vault and check that the tunnel still echoes;
  - stop it.
- **Exit gate:** the author runs it by hand on `buildpc`. Optionally, `live-files.spec.ts` later gets a tunnels sibling.

## ROADMAP and PRODUCT changes

- ROADMAP row 3b: spec path, then Done when the exit gate is met. Later-list entries: auto-start, LAN sharing, reconnect, remote dynamic.
- PRODUCT.md: move port forwarding out of "Not built" once 3b ships.
- CLAUDE.md: the UI door method list (protocol 5), `internal/tunnel`, and the Tunnels tab.

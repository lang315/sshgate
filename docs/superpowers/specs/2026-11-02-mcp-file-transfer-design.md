# sshgate: `upload` and `download` MCP tools — Design

Date: 2026-11-02
Status: Approved in chat 2026-11-02 (sections 1–2), option A of three.
Amended 2026-11-02 while planning (selected symlinks refused, scan stops at the first hit, autoAllow.ran summary, view test).
Implemented on branch feat/mcp-file-transfer (plan `plans/2026-11-02-mcp-file-transfer.md`).

## Goal

Let an AI agent driving the MCP bridge copy files and folders between the project it works in and a remote host, both ways, binary and large ones included, with approval, grants, audit and secret protection as for `exec`.

Why: `exec` with `stdin` (spec `2026-10-07-exec-stdin-design.md`) writes text up to 256 KiB and reads through redacted output. It cannot move a build artifact, a tarball, or a binary, and it cannot bring a file back to the local project. That spec deferred "MCP file tools" and set four conditions, all kept here: refuse files where any redaction hit, look at the whole file, refuse su hosts, and a spec change to run under a grant (this spec is that change).

Exit gate (manual, on a real host): under a grant, Claude Code uploads a folder from its project to the host and downloads it back to another project folder, both recorded in `audit.jsonl` with `approval: "auto"`; a download of a file holding a secret (a `.env` with a password line) is refused and the AI is told the kind; without a grant the approval card shows the transfer block and Send to tab is disabled.

## Rejected alternatives (2026-11-02)

- B. A transfer spelled as an `exec` command (`scp`, `sftp -b`): it cannot carry a local binary, and the approver would read a command, not a transfer.
- C. A separate approval queue for transfers: duplicates the broker, its cap and expiry, and the approval UI.
- Upload and download in standalone `--host` mode: that mode has no approval; out.
- A transfer plan (file count, bytes, conflicts) on the approval card: planning before approval would let the AI learn whether remote paths exist without a decision. The card shows paths only.

## Scope

In: the two tools in the bridge; MCP-door methods `upload` and `download`; hub request path, grants, soft lock, audit; the download secret scan in `internal/files`; UI-door protocol 13; the app's approval card, activity row and audit row; the CLI approver.

Out: `--host` mode; sudo or su transfers; delete, mkdir, rename and list for the AI (it has `exec`); resuming a cut transfer; a size display before approval.

## Behaviour

### MCP tools (bridge only)

- `upload {server, local: [path, …], remote: "<remote folder>", overwrite?: bool, description, timeoutSec?}`: copy local files or folders into an existing remote folder.
- `download {server, remote: [path, …], local: "<local folder>", overwrite?: bool, description, timeoutSec?}`: copy remote files or folders into an existing local folder.

Local paths are relative to the bridge's working directory, the project root: Claude Code starts each stdio MCP server in the session's project folder (checked 2026-11-02 with `lsof -d cwd` on the running bridges, worktrees included). The bridge sends that folder as `root`, an absolute path, and the paths unchanged. Remote paths are absolute (`files.CheckAbs`). `overwrite` defaults to false: an existing destination file is skipped and counted. The tool descriptions say: paths are relative to the project; the destination folder must exist; downloads holding a secret are refused; `exec` with `stdin` is the way to write a small text file.

### Hub path

The MCP door decodes params strictly (`strictParams`), then `Hub.Transfer` follows `Hub.Exec`'s order (CLAUDE.md, "`Hub.Exec` order"), with these differences:

1. `resolveForAuto`, as for exec (vault, visible, unlocked or a live grant under soft lock, pin).
2. Validate: description (`config.ValidateDescription`); every local path relative, `filepath.IsLocal`; every remote path absolute; at least one source, at most 100; `root` absolute and not `/` and not the user's home folder (a session started in `~` would expose `~/.ssh` and the vault). Refuse a host with a su password (`ErrTransferSu`): the AI's `exec` runs as root there, SFTP as the login user. Unaudited, like the stdin refusals.
3. `autoStart(ctx, server, false)`: a live grant runs the transfer with no approval, under the same 2-per-host in-flight cap, shared with exec. A root-login host has a grant only with `AutoAllowRoot`, as today. Under a lock with no run: `ErrAutoBusy` or `ErrLocked`, unaudited.
4. Otherwise `broker.Submit` with `Request.Transfer` set (see Broker). Denied and expired are audited; a request the client withdraws while pending is not, as for exec.
5. After approval: re-check ctx, `resolveForAI`, target and pin must match the approved ones (`ErrServerChanged`). Count in `h.running` like an approved exec; an auto transfer does not, as an auto exec does not.
6. Open local paths only through `os.OpenRoot(root)`: each upload source and the download folder is reached with `Root.OpenRoot`/`Root.Lstat` from that root, so `..` and symlinks cannot leave the folder the bridge named (Go's `os.Root` refuses an escape at every step; see the boundary section for what that folder is worth). `internal/files` gains `PlanUploadIn(ctx, c, root *os.Root, sources []string, dest string)` and `PlanDownloadIn(ctx, c, sources []string, dest *os.Root)`; the existing `PlanUpload`/`PlanDownload` keep their absolute-path behaviour for the UI door. A selected local source that is a symlink is refused (`files.errSelectedLink`); symlinks inside a selected folder are skipped, as for the UI.
7. Plan, then run on its own SFTP channel (`Manager.NewSFTP`), with the run bounded by `timeoutSec` (1–3600, default 600). An approved transfer counts in `h.running`, so the idle lock can wait up to that hour. An MCP `cancel` or a closed connection cancels the run; after `cancelGrace` the channel is closed, as for a UI job. `servers.save`/`delete`/`forgetHostKey` end it with "server changed", like UI jobs (the job joins the hub's job set).
8. Audit (see Audit) and answer the AI.

### Download secret scan

`Plan.Run` takes an optional `Check func(r io.Reader) (map[string]int, error)` for downloads, called on the finished part file before `commitLocal`. The hub's check reads the whole file through the server's `config.Redactor` (vault secrets, counted as `secret`) and `config.RedactPatterns`, in windows of 1 MiB that overlap by 64 KiB so a PEM block across a window edge still matches. Any count above zero: the part file is removed, the file counts as refused with its kinds, and the run continues with the next file. The scan stops at the first window with a hit; the kinds reported are that window's, so a count is a lower bound, never double-counted across the overlap. A file that passes is committed unchanged; nothing is ever rewritten.

This scan stops accidents, not a determined agent: a compressed or encoded secret passes, as `exec base64 .env` passes today. The tool description promises only that "files holding a recognised secret are refused".

### What the project boundary is, and is not

The hub takes `root` from the MCP door, which checks only the peer's UID. Any process of the user can be that peer, the agent's own shell tool included, and an honest bridge can be started in any folder. So the `os.Root` confinement and the `/`/home refusal protect an honest bridge from `..` and symlink accidents; they are not a boundary against the agent. The checks that hold are: without a grant, the human reads the project folder, the sources and the destination on the approval card; under a grant, nothing new is allowed, since the grant already lets `exec` write any stdin the agent can read locally and print any remote file through redaction (`base64` included).

### Answer to the AI

The bridge lays out: `uploaded|downloaded N files, M folders, B bytes`; `skipped K existing files` (first 5 names) when not overwriting; `refused R files holding secrets: <path> (kind ×n, …)` (first 20); then errors, each `path: generic message`, redacted. Exit code style is not used. Errors that stop the whole transfer (folder missing, connection) come back as tool errors with the generic texts exec uses (`ErrConnFailed`, `ErrHostKeyFailed`).

### Broker

`broker.Request` gains `Transfer *Transfer` (`omitempty`): `{op: "upload"|"download", root, local []string, remote []string, overwrite}`, where for upload `remote` holds the one destination folder and for download `local` holds it. `Command` holds a one-line summary for code that prints commands (`upload 3 paths to /srv/app`), never executed. `Decide` with `sent_to_tab` on a transfer returns `ErrSendToTabTransfer`; the CLI approver's `s` prints the same.

### Audit

Each AI transfer writes one `kind: "file"` record when it ends, as exec does: `FileRecord` gains `client`, `description`, `outcome` (`allowed`, `denied`, `expired`, `approved_but_cancelled`, `cancelled_running`, `error`), `approval` (`"auto"`), `waitMs`, `refused` (file count) and `redacted` (kind counts over refused files, never a value). A no-vault refusal writes one record with every AI field capped, as for exec. A record with `client` set is an AI transfer; UI jobs keep their start and end records. `local` paths are stripped for the app as for every file record. `audit.read`'s `outcomes` filter matches file records that carry an `outcome`, with the same meaning as for exec (`auto` is `approval: "auto"`).

### UI door, protocol 13

- `pending` events carry `transfer`.
- `autoAllow.ran` carries `op` (`upload`/`download`) and, as `command`, the one-line summary.
- `decide` `sent_to_tab` on a transfer: `ErrSendToTabTransfer`.

### App

- Approval card: a transfer block in place of the command: op, the project folder, each source, the destination, and "overwrite existing files" in the warning colour when set. Send to tab is disabled.
- Activity row for `autoAllow.ran` with `op`: "upload"/"download" and the server.
- Audit tab: an AI file record shows client, outcome, approval and refused count beside the existing file fields.

### CLI approver

Prints `upload to <server> (<target>)`, the project folder, sources, destination, and `overwrite` when set; `s` is refused with `ErrSendToTabTransfer`.

## Testing

- `internal/files`: `PlanUploadIn`/`PlanDownloadIn` refuse `..`, an absolute path, and a symlink leaving the root, and refuse a selected symlink (`errSelectedLink`), including one that stays inside; the download check removes a refused part file and commits the others unchanged.
- `internal/hub` against `sshtest` SFTP: approved upload and download; denied and expired audited; a grant runs both with `approval: "auto"`, also under soft lock; the shared in-flight cap; su host refused; root `/` and home refused; server changed during the wait; `overwrite` false skips; a download of a file holding a vault secret and of one holding a PEM key across a 1 MiB window edge refused with kinds; audit fields; CLI approver block and `s` refusal.
- `internal/mcpserver`: both tools registered on the bridge, not in `--host` mode; `root` is the working directory; the answer layout.
- `cmd/sshgate` e2e: bridge to hub to `sshtest`, an approved upload.
- `app/view`: a view test (`approvals_test.go`, `colApp`) for the transfer approval card (Send to tab disabled) and the audit row.

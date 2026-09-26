# sshgate: SFTP File Browser — Design (Slice 3, part a)

Date: 2026-09-26
Status: Draft, awaiting the author's review.
Depends on: `2026-09-24-desktop-app-design.md` (slice 1), `2026-09-25-desktop-slice2a-design.md` (slice 2a) and `2026-09-26-slice2b-ssh-config-import-design.md` (slice 2b-1). Everything there still holds unless this document changes it by name.

## Goal

Browse and manage files on a saved host from the app, over the same SSH connection its terminal tabs use: list, upload, download (files and whole folders), make folders, rename, and delete. Uploads can also come from files dragged in from Finder.

Exit gate: on a real host (e.g. `buildpc`), the author browses to a folder, uploads a folder, downloads it back, and deletes it, from the app.

## Scope

In: a Files tab per host; list, mkdir, rename, recursive delete; recursive upload and download with progress, cancel, and a single conflict question per transfer; drag and drop from Finder; audit records for every change and every transfer.

Out, recorded in ROADMAP:

- **Port forwarding** (the other half of ROADMAP slice 3) becomes slice 3b with its own spec.
- AI access to files. No `files.*` method exists on the MCP door. Opening files to the AI would need a per-operation approval design first.
- Acting as root (sudo/su SFTP). Files are read and written as the login user.
- Opening or editing a remote file in a local editor, previews.
- chmod/chown from the UI.
- Resuming an interrupted transfer. A cancelled or failed transfer leaves no partial file (see Transfers) and is started again from the start.
- A local file pane (two-column browser). Local files come only from the system dialogs and Finder drops.
- SCP fallback for servers without the SFTP subsystem.

## User flow

1. Each host card on the Hosts tab gets a **Files** button next to opening a terminal. It opens a tab `⌸ <name>` in the same tab strip as terminal tabs. The first listing is the login user's home directory.
2. The tab shows a toolbar (Up, an editable path field, Refresh, New folder, Upload, Download, Rename, Delete), then a table (Name, Size, Modified, Permissions; folders first, sortable by column), then a transfer strip at the bottom.
3. Selection: click, Cmd/Ctrl-click, Shift-click. Double-click or Enter on a folder opens it; Backspace goes up.
4. **Upload** opens the system Open dialog (files and folders, several at once; on Windows and Linux, where one dialog cannot pick both, two buttons: Upload files and Upload folder). Dropping files or folders from Finder onto the tab does the same. Either way the upload goes into the folder being shown.
5. **Download** asks for a destination folder with the system dialog and copies the selection into it.
6. If any destination already exists, a dialog says how many and shows a few names: **Cancel** (default), **Skip existing**, **Overwrite all**. Nothing is written before the choice.
7. Each running transfer shows a progress bar, the current file, bytes done of total, and Cancel. When it ends: files copied, skipped, and up to 20 error lines.
8. **Delete** on files only lists them, with Cancel as the default button. If the selection holds a non-empty folder, the dialog first shows how many files and folders and how many bytes will go, and Delete stays disabled until the author types the folder's name (one item selected) or `delete` (several).
9. A host with no pinned key gets the existing Trust prompt from the first listing, as `term.open` does today. A key mismatch shows the existing mismatch dialog.
10. The tab never refreshes on its own: it lists again only after the author's own change or Refresh. (The renderer must not poll: see "Idle" below.)
11. Files tabs survive a lock and a hub restart like terminal tabs (hidden, `inert`). A running transfer keeps running through a lock, as an open terminal does; starting a new one needs the vault unlocked.

## Security rules

- **The renderer never names a local path.** Local paths exist only in Electron main, which creates them from the system Open/Save dialogs and from Finder drops, and hands the renderer an opaque token for each. Main swaps tokens for paths in `files.transfer` before relaying it to the hub; an unknown token, or a token of the wrong kind, fails the call. A compromised renderer therefore cannot read `~/.ssh/id_*` (or any other local file) and push it to a server, nor write into a local folder the author did not pick.
- **Drops are real files.** Preload turns a dropped `File` into a path with `webUtils.getPathForFile`; a `File` made by page script has an empty path and is refused. The renderer's main world has no `ipcRenderer`, so it cannot send a raw path to main.
- **Names from the server are untrusted.** Every name the server returns (listing or walking a folder) must be one path element: not empty, not `.` or `..`, no `/`, `\`, or NUL. A bad name is skipped and reported as an error. Before writing, the hub also checks that each local destination is inside the picked folder.
- **No writing through symlinks.** A download never writes through an existing local symlink (checked with `Lstat`): that destination is an error and is skipped. Uploads apply the same rule on the server. Recursive copies and deletes skip symlinks found while walking (reported as skipped); a symlink the author selected directly is copied as the file it points to, and deleted as the link itself.
- **No privilege bits.** Copied files keep their `rwx` bits for user, group, and other; setuid, setgid, and sticky are dropped. Ownership is never set.
- **Displayed names are sanitised** (`printable`) so a server cannot inject terminal or layout control characters into the UI.
- **UI door only.** Every `files.*` method needs an unlocked vault and a pinned host key, like `term.open`, and is audited as below.

## Components

### `internal/sshx`

- `Manager.SFTP() (*sftp.Client, error)`: opens the SFTP subsystem (`github.com/pkg/sftp`, a new dependency) on the manager's connection on first use, and keeps it until that connection closes. A server without the subsystem fails with `ErrNoSFTP` ("server has no SFTP subsystem").
- `sshtest`: the test server gains an SFTP subsystem (the `pkg/sftp` server, rooted in a temp directory), and a mode whose listings return hostile names (`../x`, `a/b`, `.`, empty) for the security tests. `sshtestd` gains `-sftp-root=<dir>`.

### Hub (`internal/hub/files.go`)

UI-door requests (protocol 4; `ProtocolVersion` in `idle.go`):

| Method | Params | Result |
|---|---|---|
| `files.list` | `{server, path, trustHostKey?}` | `{path, entries: [{name, size, mode, mtime, kind: "dir"\|"file"\|"link"\|"other"}]}`; `path` is the absolute, cleaned path listed (`""` means the home directory). May instead return `hostKeyUnknown` / `hostKeyMismatch` results exactly as `term.open` does. |
| `files.mkdir` | `{server, path}` | `{}` |
| `files.rename` | `{server, from, to}` | `{}`; refuses to replace an existing `to`. |
| `files.stat` | `{server, paths}` | `{files, dirs, bytes, links}`: a walk that does not follow symlinks; for the delete dialog. |
| `files.delete` | `{server, paths}` | `{deleted, errors}`: recursive, never follows a symlink. |
| `files.transfer` | `{id, server, direction: "up"\|"down", sources, dest, conflict: "ask"\|"overwrite"\|"skip"}` | `{status: "conflicts", count, sample}` or `{status: "started"}` |

Notifications from the hub: `files.progress {id, file, done, total}` (at most about 4 per second per transfer) and `files.done {id, copied, skipped, errors, cancelled}`. From the renderer: `files.cancel {id}`.

For uploads, `sources` are local paths (filled in by main) and `dest` is a remote folder; for downloads, `sources` are remote paths and `dest` is a local folder (filled in by main). Ids are chosen by the client, like terminal ids; a duplicate id is refused.

### Transfers

1. **Plan.** Walk the sources (symlinks inside folders skipped), collect files and folders with their sizes, and check every destination. If any exists and `conflict` is `ask`, answer `{status: "conflicts", count, sample}` (up to 5 names) and stop, having written nothing. The renderer asks the author and calls again with `overwrite` or `skip`.
2. **Copy**, in the background, one file at a time. Folders are created first. Each file is written to `<name>.sshgate-part` beside its destination, then renamed onto the final name (on the server: the `posix-rename@openssh.com` extension when offered, else remove then rename). No destination ever holds a half-written file under its real name.
3. **Errors** on one file (permission denied, a bad name, a symlink destination) are recorded and the transfer goes on. A lost connection ends the transfer. On cancel, failure, or a lost connection the hub removes the current `.sshgate-part` where it still can.
4. **Done**: `files.done` with counts and errors. Closing a Files tab cancels its running transfers.

### Audit

`broker.FileRecord`, `kind: "file"`: `{time, action: "upload"|"download"|"mkdir"|"rename"|"delete", server, host, port, paths (remote side, first 20), files, bytes, skipped, errors, cancelled}`. One record per operation, written when it ends. `files.list` and `files.stat` are not audited.

### Idle

Every `files.*` request counts as UI activity (as every UI-door request but `status` already does). `files.progress` does not. The renderer does not poll: a Files tab lists only after the author's own action.

### Desktop

- `main/grants.ts`: a map from random 128-bit tokens to `{path, kind: "read" | "writeDir"}`, cleared whenever the renderer is reloaded (`recoverRenderer`). IPC handlers, behind the existing `isTrusted` guard: `files:pickUpload` (Open dialog → `[{token, name}]`), `files:pickDownloadDir` (folder dialog, create allowed → `{token, name}`), `files:grantDropped` (paths from preload → `[{token, name}]`). `relayCall` swaps tokens in `files.transfer` (upload: every `sources` entry must be a `read` token; download: `dest` must be a `writeDir` token) and rejects the call otherwise.
- `preload.ts`: `grantDropped(files: File[])` calls `webUtils.getPathForFile` on each and sends the non-empty paths to main.
- `shared/protocol.ts`: the new methods and notifications; `PROTOCOL_VERSION = 4`.
- `terminals.ts`: a tab gains `kind: "term" | "files"`; `TerminalTabs.tsx` renders `FilesView` for files tabs.
- `HostList.tsx`: the Files button.
- `FilesView.tsx` (toolbar, table, selection, drop zone, transfer strip), `FileDialogs.tsx` (conflict and delete dialogs), `files.ts` (pure helpers: sorting, size formatting, selection, the delete-confirmation rule).

## Errors

- No SFTP subsystem: "server has no SFTP subsystem", shown in the tab.
- Locked vault, unknown server, connection failure: as for `term.open`.
- Per-file errors are listed in the transfer summary (up to 20 lines, then "and N more").
- `files.mkdir`/`rename`/`delete` errors are shown next to the toolbar. They carry remote paths and server messages, never a secret.

## Testing

- **Go, against `sshtest` with SFTP:** list (home, absolute path, kinds, sizes); mkdir; rename refusing to replace; delete of a nested folder that contains a symlink to a folder outside it (the link goes, its target stays); upload and download of a file and of a nested folder, content and `rwx` bits checked, setuid dropped; conflicts with `ask` (nothing written), `skip`, and `overwrite`; cancel mid-transfer leaves no `.sshgate-part` and no half file; hostile names never produce a file outside the picked folder; a local symlink at a download destination is not written through; one audit record per operation with the right counts; `files.*` refused while locked and absent from the MCP door; progress notifications rate-limited.
- **Go, against real OpenSSH (Docker, `internal/sshx`):** `Manager.SFTP` lists, uploads, and downloads through `sftp-server`.
- **Live:** `TestLiveVaultConnect` also calls `files.list` on each server.
- **Desktop unit (vitest):** grants (swap, unknown token, wrong kind, cleared on reload), sorting and size formatting, the delete-confirmation rule, conflict dialog default.
- **Desktop e2e (Playwright, `sshtestd -sftp-root`):** open a Files tab, list, make a folder, upload a folder and download it back (system dialogs replaced through `electronApp.evaluate`), the conflict dialog, recursive delete with the typed confirmation.

## ROADMAP changes

- Slice 3 row: split into "3a SFTP file browser" (this spec) and "3b Port forwarding (local, remote, dynamic)" (not specced; entry gate: 3a done).

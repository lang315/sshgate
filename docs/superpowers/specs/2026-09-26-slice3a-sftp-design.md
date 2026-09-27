# sshgate: SFTP File Browser — Design (Slice 3, part a)

Date: 2026-09-26
Status: Approved 2026-09-26; implemented (plan `plans/2026-09-26-slice3a-sftp.md`).
Depends on: `2026-09-24-desktop-app-design.md` (slice 1), `2026-09-25-desktop-slice2a-design.md` (slice 2a) and `2026-09-26-slice2b-ssh-config-import-design.md` (slice 2b-1). Everything there still holds unless this document changes it by name.

## Goal

Browse and manage files on a saved host from the app, over the same SSH connection its terminal tabs use: list, upload, download (files and whole folders), make folders, rename, and delete. Uploads can also come from files dragged in from Finder.

Exit gate: on a real host (e.g. `buildpc`), from the app, the author browses to a folder, uploads a folder, uploads it again and chooses **Skip existing** in the conflict dialog, downloads it back, renames it, and deletes it.

## Scope

In: a Files tab per host; list (hidden-files toggle, symlink targets), mkdir, rename, recursive delete; recursive upload and download with progress, cancel, and one conflict question per transfer; drag and drop from Finder; the last folder remembered per host; audit records for every change and every transfer.

Out, recorded in ROADMAP:

- **Port forwarding** (the other half of ROADMAP slice 3) becomes slice 3b with its own spec.
- AI access to files. No `files.*` method exists on the MCP door. Opening files to the AI would need a per-operation approval design first.
- Opening the Files tab at a terminal's current directory (needs the shell to report it, e.g. OSC 7).
- Acting as root (sudo/su SFTP). Files are read and written as the login user; a folder the user cannot read shows "Permission denied".
- Opening or editing a remote file in a local editor, previews; download by double-click; dragging files out to Finder; copying a path.
- chmod/chown from the UI.
- Resuming an interrupted transfer. It is started again from the start.
- A local file pane (two-column browser). Local files come only from the system dialogs and Finder drops.
- SCP fallback for servers without the SFTP subsystem.

## User flow

1. Each host card on the Hosts tab gets a **Files** button next to opening a terminal. It opens a tab `⌸ <name>` in the same tab strip as terminal tabs, at the last folder the author had open on that host (renderer `localStorage`, per server name), else the login user's home directory.
2. The tab shows a toolbar (Up, an editable path field, Refresh, Show hidden, New folder, Upload, Download, Rename, Delete), then the listing (Name, Size, Modified, Permissions; folders first; sortable by column; symlinks show `→ target`), then a transfer strip at the bottom. The listing is virtualised, so a large folder stays responsive. Hidden files (names starting with `.`) are off by default; the toggle is remembered.
3. Selection: click, Cmd/Ctrl-click, Shift-click. Double-click or Enter on a folder opens it; Backspace goes up; F2 renames; the Delete key opens the delete dialog (never deletes by itself).
4. **Upload** opens the system Open dialog, titled "Upload to `<server>:<folder>`" (files and folders, several at once; on Windows and Linux, where one dialog cannot pick both, two buttons: Upload files and Upload folder). Dropping files or folders from Finder onto the tab does the same. Either way the upload goes into the folder being shown.
5. **Download** asks for a destination folder with the system dialog, titled "Download from `<server>` to…", and copies the selection into it.
6. **Conflicts.** Existing folders merge; only existing files conflict. If any exist, the author is asked once per transfer how many and a few names: **Cancel** (default), **Skip existing**, **Overwrite all**. For an upload the app shows this dialog; for a download, Electron main shows it as a native dialog (see Security rules). Nothing is written before the choice.
7. Each running transfer shows a progress bar, the current file, bytes done of total, and Cancel. When it ends: files copied, skipped, and the first 20 errors ("and N more"). When an upload or delete ends in the folder being shown, the tab lists it again, once, unless a listing is loading or the author has since asked for another folder.
8. **Delete** first counts what will go (files, folders, bytes; symlinks are removed as links, never followed). Files and empty folders only: a list, with Cancel as the default button. If any selected folder has contents: the counts, and Delete stays disabled until the author types `delete`.
9. **Rename** refuses to replace an existing name and says so.
10. A folder the author cannot read shows "Permission denied", not an empty listing. A listing over 10,000 entries shows the first 10,000 and says it was cut.
11. A host with no pinned key gets the existing Trust prompt from the first listing, as `term.open` does today. A key mismatch shows the existing mismatch dialog.
12. The tab never refreshes on a timer: it lists again only after the author's own action or Refresh. (The renderer must not poll: see "Idle".)
13. Files tabs survive a lock like terminal tabs (hidden, `inert`). A running transfer keeps running through a lock, as an open terminal does, and can still be cancelled; starting one needs the vault unlocked. After a hub restart the transfer strip marks every transfer it was showing as "hub restarted".

## Security rules

- **The renderer never names a local path.** Local paths exist only in Electron main, which creates them from the system dialogs and from Finder drops and hands the renderer an opaque token for each. Main rebuilds every `files.plan` it relays from an exact allowlist of keys, swapping tokens for paths; it never forwards a key it did not build. The hub decodes `files.*` params strictly: object keys must match its allowlist exactly (case-sensitive; Go's `encoding/json` would otherwise match `Sources` to `sources`), with no unknown or duplicate keys.
- **Grants are narrow.** A token is bound to one server and one kind (`read` for upload sources, `writeDir` for a download destination), is used by at most one `files.plan`, expires after 5 minutes, and all tokens are dropped when the vault locks and when the renderer is reloaded. The dialogs name the server and folder, so a renderer cannot pass off a picker as something else.
- **Main decides download overwrites.** For a download plan with conflicts, the renderer can only ask main to resolve it; main shows the native Cancel / Skip existing / Overwrite all dialog and relays the author's choice. A compromised renderer cannot overwrite local files on its own.
- **Drops are real files.** Preload checks that it was given an array of `File` objects (at most 1,000), turns each into a path with `webUtils.getPathForFile`, and sends the non-empty paths to main. A `File` made by page script has an empty path and is refused. The renderer's main world has no `ipcRenderer`, so it cannot send a raw path to main. Any file the author drags into the window, or picks, can be granted; that is intended.
- **All local reads and writes go through `os.Root`.** A download writes only under an `os.Root` opened on the picked folder; an upload reads each source through an `os.Root` opened on its parent. `os.Root` refuses `..` and any symlink that leads outside, so containment is enforced by the standard library, not by comparing strings.
- **Names from the server are untrusted.** Every name the hub gets from the server must be valid UTF-8 and one path element: not empty, not `.` or `..`, no `/`, `\`, or NUL. A download also requires `filepath.IsLocal(name)` and `filepath.Localize(name) == name` (this rejects `CON`, `a:b`, trailing dots and spaces on Windows), and treats two names in one folder that differ only in case as a conflict (the second is an error), since the macOS and Windows defaults are case-insensitive. A bad name is skipped and reported as an error.
- **No writing through symlinks, and no silent replace.** Each file is written to a new part file `.<name>.<random>.sshgate-part` beside its destination, created exclusively (`O_CREATE|O_EXCL`, mode 0600) so a planted file or link of that name makes the create fail instead of being followed. It is then committed onto the destination: without overwrite, by a no-replace link (`Link` then remove the part file; on the server the `hardlink@openssh.com` extension), falling back to `Lstat` then rename where links are unsupported; with overwrite, by an atomic replacing rename (`posix-rename@openssh.com` on the server). A server that offers no replacing rename cannot overwrite: those files are per-file errors. A destination that is a symlink at commit time is an error and is skipped.
- **Recursive walks never follow symlinks.** Walks use `Lstat`. Recursive copies skip symlinks found inside folders (reported as skipped); a symlink the author selected directly is resolved once with `Stat` and copied as what it points to. Delete removes a symlink as the link itself, even when selected directly; `sftp.Client.RemoveAll` is not used, because it follows a selected symlink.
- **Mutations need real paths.** `files.mkdir`, `files.rename`, and delete and upload plans take absolute, cleaned remote paths only; delete and rename refuse `/` and the login user's home directory itself. The new name of a mkdir or a rename's `to` must pass the name rules above (so no `\`), or the next listing would hide it and it could never be selected again.
- **Permission bits.** Uploads keep the source's `rwx` bits (setuid, setgid, sticky dropped). Downloads keep at most `rwxr-xr-x` (group and other write dropped, as the server is not trusted to choose them). Folders are created `0700` while filled and get their final mode last, so a read-only folder does not block its own contents. Modification times are kept. Ownership is never set.
- **Displayed text is sanitised.** Every server-sourced string shown by the app (names, symlink targets, server error text, `files.progress.file`) has C0/C1 controls, bidi overrides and isolates (U+202A–202E, U+2066–2069), zero-width characters, and U+2028/2029 replaced with a visible escape (e.g. `‮`), so a server cannot make one name look like another.
- **Bounded work.** A listing returns at most 10,000 entries; a plan walks at most 100,000 entries and 64 levels; a job keeps its first 20 errors and a count. Every walk and copy runs under a cancellable context, and each job uses its own SFTP channel, so a stalled server blocks only that job and cancel closes the channel to unblock it (the whole channel, not just its stdin: the peer's SSH layer answers a channel close even when its SFTP server is stuck).
- **UI door only.** Every `files.*` method is on the UI door only, needs a pinned host key, and needs an unlocked vault, except `files.cancel` (always allowed) and `files.cancelAll` (Electron main only, like `term.closeAll`).
- **Known limits.** A job holds its own SSH channel from plan until `files.done`; OpenSSH's `MaxSessions` defaults to 10, so many jobs waiting on a choice can keep new terminals on that host from opening. A name longer than `NAME_MAX` minus the roughly 30-byte part-file suffix (`.<name>.<random>.sshgate-part`) fails as a per-file error. A remote folder swapped for a symlink between plan and run cannot be guarded against: SFTP is path-based and has no `*at` calls, so the run follows the path it is given.

## Components

### `internal/sshx`

- `Manager.SFTP() (*sftp.Client, error)`: the shared client for listings and single operations (`github.com/pkg/sftp`, a new dependency). It belongs to the current `*ssh.Client`: it is dropped when `ensure()` redials, and a watcher on the client's `Wait()` drops it when its channel dies while SSH lives on (the `ensureElevated` pattern). It is closed outside `m.mu`, so a hung close cannot block the manager. A server without the subsystem fails with `ErrNoSFTP` ("server has no SFTP subsystem").
- `Manager.NewSFTP() (*sftp.Client, error)`: a fresh client (its own channel) for one job; the job closes it. Uploads use `File.ReadFromWithConcurrency`, downloads `File.WriteTo`, so one round trip per 32 KiB does not cap throughput. A large transfer shares the TCP connection with the host's terminals, so typing may lag during it; that is accepted.
- `files.*` calls start the manager's keepalive, as `term.open` does.

### Hub (`internal/hub/files.go`)

UI-door requests (protocol 4: `ProtocolVersion` in `idle.go` and `PROTOCOL_VERSION` in `desktop/src/shared/protocol.ts` move together):

| Method | Params | Result |
|---|---|---|
| `files.list` | `{server, path, trustHostKey?}` | `{status: "listed", path, entries: [{name, size, mode, mtime, kind: "dir"\|"file"\|"link"\|"other", target?}], truncated, bad}`; `path` is the absolute, cleaned folder listed (`""` means home); `target` is a link's target; `bad` counts names left out because they failed the name rules. May instead return `hostKeyUnknown` / `hostKeyMismatch` exactly as `term.open` does. A read error "permission denied" is its own error. |
| `files.mkdir` | `{server, path}` | `{}` |
| `files.rename` | `{server, from, to}` | `{}`; never replaces `to`: `Lstat`, then a plain SFTP rename, for files and folders alike (OpenSSH's plain rename refuses to replace an existing name, covering the window after the `Lstat`; verified in CI by `TestSFTPAgainstOpenSSH`). |
| `files.plan` | `{id, server, op: "upload"\|"download"\|"delete", sources, dest?}` | `{}` at once; the walk runs in the background and ends with `files.planned`. |
| `files.run` | `{id, conflict: "overwrite"\|"skip"}` | `{}`; starts a planned job. |
| `files.cancel` | `{id}` (notification) | Cancels a planning, planned, or running job. |
| `files.cancelAll` | `{}` | Main only: cancels every job of this door. |

Notifications from the hub: `files.planned {id, files, dirs, bytes, links, conflicts: {count, sample}, errorCount, errors}`, `files.progress {id, file, done, total}` (at most about 4 per second per job, never acknowledged: the renderer only shows the latest), and `files.done {id, op, copied, skipped, deleted, bytes, errorCount, errors, cancelled, reason}`.

For uploads, `sources` are local paths (filled in by main) and `dest` is a remote folder; for downloads, `sources` are remote paths and `dest` is a local folder (filled in by main); for delete, `sources` are remote paths. Ids are chosen by the client and belong to the door that planned them; a duplicate live id is refused. A planned job not run within 10 minutes is dropped.

**Why plan, then run.** Planning a large tree, counting a delete, or deleting it can take longer than the desktop's 60-second call timeout (`hubProcess.ts`). No request waits for a walk: `files.plan` answers at once, results come as notifications, and every stage can be cancelled. `files.planned` also feeds the delete dialog's counts, so no separate stat method is needed.

### Jobs

1. **Plan.** Resolve selected symlinks once with `Stat`, then walk with `Lstat` (symlinks inside folders skipped), within the limits above. Collect files and folders with sizes, check names, and find conflicts: an existing file at a file destination. An existing folder at a folder destination merges; a file where a folder goes (or the reverse) is a per-file error.
2. **Run** (upload, download). Folders first (`0700`), then files one at a time through part files as above, `rwx` bits and modification time set on the part file through its open handle before the commit, then folder modes last. Conflicts found at plan time follow the author's choice; a destination that appeared after the plan is never replaced unless the choice was overwrite (the no-replace commit fails and it becomes a per-file error).
3. **Run** (delete). Depth first with `Lstat`: files and links, then folders bottom-up.
4. **Errors.** A per-file error (permission denied, a bad name, a symlink destination) is recorded and the job goes on. Fatal (`isFatal`, `internal/files/fatal_unix.go`): a lost or closed connection (`sftp.ErrSSHFxConnectionLost`, `sftp.ErrSSHFxNoConnection`, `io.ErrUnexpectedEOF`, `net.ErrClosed`) and a full local disk (`ENOSPC`, `EDQUOT`; `fatal_other.go` has no `EDQUOT`).
5. **Cancel, failure, or a fatal error.** Stop at the next chunk, close the remote and local handles, remove the current part file where still possible, write the end audit record, then send `files.done`. A part file left by a lost connection or a killed hub is recognisable by its name; no destination ever holds a half-written file under its real name.
6. **Ends of the connection.** `servers.save` (when it closes the connection), `servers.forgetHostKey`, and `servers.delete` end that server's jobs with `cancelled: true, reason: "server changed"`; the editor's "this will close N tabs" warning also counts running transfers. Closing a Files tab cancels its jobs. A renderer crash: `recoverRenderer` calls `files.cancelAll` next to `term.closeAll`. Hub shutdown cancels every job and waits up to 5 seconds for cleanup and audit before closing connections.

### Audit

`broker.FileRecord`, `kind: "file"`, written twice per job, at start and at end, and once for mkdir and rename: `{time, phase: "start"|"end", action: "upload"|"download"|"delete"|"mkdir"|"rename", server, host, port, remote (paths, first 20), local (paths, first 20, transfers only), from, to (rename), conflict, files, bytes, skipped, errorCount, cancelled, reason}`. A job cut off by a crash leaves its start record. `files.list` and `files.plan`'s walk are not audited.

### Idle

Every `files.*` request counts as UI activity (as every UI-door request but `status` already does). `files.progress`, `files.planned`, `files.done`, and `files.cancel` do not. The renderer does not poll.

### Desktop

- `main/files.ts` (`Grants`, `FilesRelay`): random 128-bit tokens → `{path, kind, server, expires}`; single use; cleared on the `locked` notification (a lock reaches main as `locked`, not as `hub:state`), on any non-`running` hub state, and on renderer reload. IPC handlers behind the existing `isTrusted` guard: `files:pickUpload {server, folder}`, `files:pickDownloadDir {server}`, `files:grantDropped {server, paths}` (from preload). Every dialog is called as `dialog.showOpenDialog(...)` (a property access), so e2e can replace it.
- `main/files.ts`'s `FilesRelay`: rebuilds `files.plan` from its allowlist with tokens swapped (upload: every source a `read` token for that server; download: `dest` a `writeDir` token for that server; delete: no tokens), anything else rejected; tracks every relayed job id from plan to `files.done` (refuses a plan whose id is still live, and a run for an id it never planned). It remembers each download job's conflict count from `files.planned`; `files.run` for a download with conflicts shows the native dialog and relays the author's choice (Cancel sends `files.cancel`); for uploads and deletes the renderer's `conflict` is relayed as is. `main/ipc.ts` only routes `files.plan`/`files.run` to the relay and guards the new IPC handlers (`isTrusted`).
- `preload.ts`: `grantDropped(files, server)`.
- `shared/protocol.ts`: the new methods and notifications (`files.cancelAll` not in the renderer whitelist); `PROTOCOL_VERSION = 4`.
- `terminals.ts`: a tab gains `kind: "term" | "files"`; `TerminalTabs.tsx` renders `FilesView` for files tabs.
- `HostList.tsx`: the Files button. `HostEditor.tsx`: the close warning counts transfers.
- `FilesView.tsx` (toolbar, virtualised listing, selection, drop zone, transfer strip), `FileDialogs.tsx` (upload conflict and delete dialogs; Overwrite all, Skip existing, and Delete are mouse-only and disabled for 500 ms after the dialog, keyed per job, appears, so a click meant for one job's dialog cannot confirm the next one's), `files.ts` (pure helpers: sorting, size formatting, selection, the delete-confirmation rule, display sanitising, last-folder memory).

## Errors

- No SFTP subsystem: "server has no SFTP subsystem", shown in the tab.
- Locked vault, unknown server, connection failure: as for `term.open`.
- Permission denied on a listing: shown in place of the listing.
- Per-file errors: the first 20 in the transfer summary, then "and N more".
- `files.mkdir`/`rename` errors and plan failures are shown next to the toolbar. They carry remote paths and server messages (sanitised), never a secret.

## Testing

Test SFTP server: `sshtest` gains an SFTP subsystem built on `sftp.NewRequestServer` with handlers that join every path under a temp root and refuse anything that escapes it (`sftp.NewServer` is not a chroot: absolute paths would reach the developer's whole filesystem). It has three modes: normal; **hostile** (a fake lister returning names that survive the client's `path.Base`: `x/..`, `a\b`, a NUL byte, invalid UTF-8, `A` and `a` together, and lying sizes); and **gated** (each read or write waits on a `Release`-style channel, as `sshtest` exec already does), so cancel and progress tests are deterministic. `sshtestd` gains `-sftp-root=<dir>`.

- **Go, against `sshtest`:** list (home, absolute path, kinds, link targets, 10,000 cap, permission denied); mkdir; rename refusing to replace a file and a folder; delete of a nested folder containing a symlink to a folder outside it, and of a directly selected symlink to a folder (in both, the target survives); upload and download of a file and of a nested folder with content, bits (setuid dropped; group/other write dropped on download), mtime, and a `0555` folder checked; conflicts: `skip`, `overwrite`, a destination that appears between plan and run (not replaced under `skip`), a folder merge, a file-versus-folder error; part files are random and created with `O_EXCL` (tested), and a destination swapped to a symlink is not written through; hostile names never produce a file outside the picked folder; case duplicates on download; plan limits; cancel (gated) leaves no part file and sends `files.done` after cleanup; progress rate (gated); a job keeps running through a lock and `files.cancel` works while locked (`TestJobCancelWhileRunningAndWhileLocked`); `servers.save` ends a running job with "server changed"; `files.cancelAll`; strict params (a `Sources` key, an unknown key, a duplicate key are all refused); two start/end audit records per job, local paths included, rename from/to; `files.*` absent from the MCP door.
- **Go, unit:** the name rules (`filepath.IsLocal`/`Localize`, UTF-8, case folding), the permission rules.
- **Go, against real OpenSSH (Docker, `internal/sshx`):** `Manager.SFTP` lists, uploads, downloads, and renames without replacing through `sftp-server`. It keeps the `:latest` image (no tag can be verified offline): when the server offers no SFTP subsystem it skips locally but fails when `CI` is set, so CI always proves the no-replace plain rename that `files.rename` relies on.
- **Live:** `TestLiveVaultConnect` also calls `files.list` on each server.
- **Desktop unit (vitest):** grants (swap; unknown token; wrong kind; wrong server; raw path passed as a token; reused token; expired token; cleared on lock and on reload); `files.plan` rebuilt from the allowlist (extra and case-variant keys dropped); the download conflict path goes through main's dialog; preload `grantDropped` with a mocked `webUtils` (non-array, non-`File`, empty path, over 1,000); sorting, size formatting, sanitising (bidi, zero-width), the delete-confirmation rule.
- **Desktop e2e (Playwright, `sshtestd -sftp-root`):** open a Files tab, list, make a folder, upload a folder and download it back (dialogs replaced through `electronApp.evaluate`), the upload conflict dialog, the native download conflict dialog (replaced), rename, recursive delete with the typed confirmation. A transfer running through a lock is not in the e2e (`sshtestd` has no gate flag); it is covered by the Go test `TestJobCancelWhileRunningAndWhileLocked` and by the manual checklist.
- **Manual (release checklist):** drag a folder from Finder into a Files tab (Playwright cannot produce a real dropped file); upload on Windows/Linux with the two buttons.

## ROADMAP and PRODUCT changes

- ROADMAP slice 3 row: split into "3a SFTP file browser" (this spec) and "3b Port forwarding (local, remote, dynamic)" (not specced; entry gate: 3a done). Later-list entries: open Files at the terminal's directory (OSC 7); download by double-click, drag-out to Finder, copy path.
- PRODUCT.md: move SFTP from "Not built" once 3a ships.

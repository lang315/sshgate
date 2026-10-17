# sshgate: Desktop app on mygo (native Go UI) — Design

Date: 2026-10-14
Status: Approved in chat 2026-10-14 (motivation, terminal library, platform scope, migration, sections 1–2). Revised the same day after three reviews (spec vs code, red team, native architecture): the hub moved from in-process to a subprocess, and the terminal security, stop gate, slice and files sections were added or fixed. Implemented in slices 0–7 (PRs #23–#27, #30, #32, #33, merged 2026-10-16 to 2026-10-17); slice 7's hand checks are not all done (see `ROADMAP.md`).

## Goal

Replace the Electron desktop app (`desktop/`) with a native Go app built on mygo (`github.com/egoist/mygo`, fork `lang315/mygo`) and drawn with mygo's `ui` package. The app spawns `sshgate hub` and speaks the existing UI door, as the Electron app does.

Why (all three hold):
- (a) Size and memory: `desktop/out/sshgate.app` is 298 MB, about 288 MB of it Electron.
- (b) One language: the desktop app is Go, like the rest of sshgate. No TypeScript, React, Node toolchain or xterm.js.
- (c) Dogfood mygo's native UI, where the owner's mygo work already is.

Exit gate (whole project): on the author's Mac, the packaged mygo app replaces the Electron app for daily use, with every feature the Electron app has, including:
- creating and unlocking a vault
- approving and denying AI requests through the real MCP bridge
- auto-allow and soft lock
- terminals, files, tunnels, audit, settings
- Touch ID and menu-bar background mode

Every assertion of the Electron e2e specs listed in Slices must have a Go test that passes.

## Scope

In:
- A new Go module at `app/` (`github.com/lang315/sshgate/app`) holding the desktop app.
- macOS as the shipped target. The code builds and its tests run on Linux and Windows in CI.
- A script that builds libghostty-vt from Ghostty's sources, and a CI job that runs it.
- Small changes to the `lang315/mygo` fork where this spec names them (Terminal security, Terminal).

Out:
- Any change to `internal/hub`, the UI door protocol (12), the MCP door, the bridge, the vault, the audit format or `hub --cli`. One exception, approved 2026-10-14: `internal/config`'s KDF returns Argon2id's work area to the OS after each derive (`debug.FreeOSMemory`), because the hub otherwise keeps about 71 MiB after unlock in both desktop apps; on darwin, `GOMAXPROCS` is lowered to the KDF parallelism around the derive and release (empirical; mechanism unknown), serialized by a mutex; derived keys and the vault format are unchanged.
- Packaging or shipping the mygo app on Windows or Linux. Electron stays the desktop app there until a later spec.
- Deleting `desktop/`. Decided separately after slice 7.
- Moving door-only policy into `*hub.Hub`, so the UI door, the CLI approver and any future in-process caller share it. Worth doing, but it is a separate refactor. Today the UI door refuses Allow while locked (`internal/hub/uidoor.go:341`) and the CLI approver does not (`internal/hub/cliui.go:58`). That is not a hole, since `resolveForAI` re-checks after approval, but it is the kind of divergence that refactor would end.
- Hardened runtime and notarization. The Electron bundle is ad-hoc signed without them today (`desktop/scripts/package-mac.sh`), and this spec keeps that level.
- Contributing changes back to upstream mygo.

## Decisions

| Question | Decision | Rejected |
|---|---|---|
| UI technology | mygo native `ui` | mygo webview keeping React (meets (a) only); stay on Electron |
| Terminal | `mygo/plugins/terminal` (libghostty-vt), library built from Ghostty source at a pinned commit, `ReleaseSafe` | prebuilt library from egoist's releases; xterm.js in `ui.WebView` |
| Hub placement | subprocess `sshgate hub`, UI door over stdio, as today | in-process calling `*Hub` (about 900 lines of door code would have to move into exported `*Hub` methods; shared crash domain and address space); in-process `ServeUIDoor` over `io.Pipe` (loses crash and memory isolation for about 20 MB of RSS) |
| Module | nested module `app/`, with its own `go.mod` (`go 1.27.1`, `replace github.com/egoist/mygo => <fork>`) | the root module (would move the CLI to Go 1.27.1 and put mygo's `replace` into it) |
| Migration | native from the start, one slice at a time; Electron stays the daily app until slice 7 | React in `ui.WebView` first; big-bang |
| Platforms | macOS first; Linux/Windows build and test only | all three at once; macOS only |

## Architecture

### Processes

```
sshgate.app
  Contents/MacOS/sshgate-app      mygo app: window, tray, menu, views, door client, terminal library
  Contents/Resources/sshgate      hub (spawned as `sshgate hub`) and MCP bridge
  Contents/Resources/sshgate-touchid
  Contents/Resources/libghostty-vt.dylib
```

- **The hub keeps the key.** Only the hub process holds the master key, the vault and the broker. libghostty-vt and the GPU stack live in the app process, never in the hub's.
- **What the app process can still do.** It sees what the human types, including the master password at unlock, and the UI door gives it full privilege. Compromising the app process therefore controls an unlocked vault, as compromising the Electron app does today. What it does not get is the key's bytes, which stay in the hub.
- **Single instance.** Unchanged: the hub's socket check (`internal/hub/listen_unix.go:26`, "another sshgate hub is already running") makes this app, the Electron app and `hub --cli` mutually exclusive.

### Layout

```
app/
  go.mod                         module github.com/lang315/sshgate/app
  cmd/sshgate-app/main.go        mygo.App, window, tray, Edit menu, hub launch
  hubproc/                       spawn, env, restart with backoff, hello check (port of hubProcess.ts)
  door/                          newline JSON-RPC client for the UI door: requests, notifications
  termconn/                      io.ReadWriteCloser over term.* for plugins/terminal
  ui/                            state and views (one package, as examples/account-manager)
scripts/build-ghostty-vt.sh      pinned Ghostty commit → libghostty-vt, ReleaseSafe
```

### Hub process (`hubproc`)

Port of `desktop/src/main/hubProcess.ts`:

- **Binary.** `Contents/Resources/sshgate` when packaged, else `SSHGATE_BIN`, else `sshgate` beside the module root, else `PATH`.
- **Knobs.** `SSHGATE_STORE` becomes `--store=`, `SSHGATE_SSH_CONFIG` becomes `--sshConfig=`, `SSHGATE_IDLE_LOCK` becomes `--idleLock=`.
- **Packaged.** `mygo.App.IsPackaged()` decides it (`mygo/app.go:316`). When packaged, every `SSHGATE_*` knob is ignored and stripped from the hub's environment.
- **Startup.** `hello` on every spawn; a protocol other than 12 is a fatal error shown in the window.
- **Restart.** After a hub crash, restart with backoff (1 s, 3 s, 10 s). Give up after a 4th crash within 60 s and show the last stderr lines. A restarted hub starts locked.
- **App death.** When the app dies, the hub sees EOF on stdin, `ServeUIDoor` returns, and the hub exits (`cmd/sshgate/hub.go:88`). The key dies with it. Slice 0 adds a test for this.

### Door client (`door`)

- **Methods.** Requests and notifications use the method names and params of protocol 12 (`CLAUDE.md`, UI door list). `term.closeAll` and `files.cancelAll` are not called (see Crash → Quit). Amended 2026-10-15 (slice 4, plan Decision 2).
- **Threading.** The client's read loop never blocks on the UI. It hands each notification to the UI thread with `Window.Update(fn)`. That queue is FIFO and never blocks (`mygo/loop.go:19-31`), so the order the hub sent is the order the UI sees.
- **No unbounded queue on the hub side.** The hub already bounds `audit.appended` (256 slots, drop) and flow-controls `term.data` with `term.ack`.
- **Activity rule.** Every request except `status` counts as UI activity for the idle lock (`internal/hub/uidoor.go:155-162`).
  - Views call the hub only from a real UI action.
  - The one exception is `status`, which does not count as activity. It may be called on `locked` and on `pending`, as `App.tsx` does today, since the soft-lock screen learns `autoHosts` only from it.
  - Files follow-ups, the other exception: a file job the user started may be carried on from its own notifications, as `FilesView.tsx` does, only while the ready screen shows (unlocked, same hub) and at most once each per job id this app planned. On `files.planned`: `files.run` (an upload with no conflicts: `skip`; a download with no conflicts: `skip`; with conflicts, the user's choice in the app's dialog), or `files.cancel` again for a Cancel pressed while planning. On `files.done` of an upload or delete: one `files.list` of the folder shown (`relistAfterJob`). `files.run` and `files.list` count as activity, so a long job that ends restarts the idle timer once, as in Electron. A job planned while locked is cancelled with `files.cancel` (a notification, not activity). Amended 2026-10-15 (slice 4, plan Decision 1).
  - Nothing calls the hub from a timer.
  - A test fails if any view code path calls a method other than `status` outside an input handler, except the Files follow-ups above, which a test bounds to planned job ids.

### Terminal (`termconn`)

Each tab is a `terminal.New` with `Options.Conn` set to a `termconn` for one hub terminal id:

- **Read.** Returns bytes from `term.data` notifications. It sends `term.ack` for what it returned, so a slow parser stalls the hub's reader at its high-water mark (1 MiB, `internal/sshx/term.go:24-27`) instead of growing a buffer. `term.ack` stays.
- **Write.** Sends `term.write`. The `user: true` flag is set only for bytes that come from a real key, paste or IME commit:
  - Keys and paste are caught with `Options.OnKey` and `Options.OnPaste` (`plugins/terminal/terminal.go:116-124`).
  - IME commits and Linux plain-text keys need an `OnInput` hook in the fork; `OnKey` misses them (`terminal.go:122-123`).
  - Query replies and focus reports are sent without `user`, so a remote program cannot keep the vault unlocked.
  - Write never returns an error while the tab is open, because mygo's input queue closes for good on a write error (`terminal.go:805-808`). The hub's `term.dropped` is shown in the tab.
- **Resize.** mygo calls `Resize(cols, rows)`; `term.resize` takes rows and cols. The adapter swaps them and clamps to 1–1000.
- **Close.** Sends `term.close`; `term.exit` closes the Conn.
- **Host keys.** `hostKeyUnknown` and `hostKeyMismatch` results of `term.open` show the same prompts as today. The retry sends `trustHostKey` to the same id.
- **Lock.** Tabs stay open across lock. A hidden tab's terminal is not drawn but keeps reading.

### Terminal security

The Electron app loads only `addon-fit` (`desktop/src/renderer/TermView.tsx:3,32`) and blocks navigation. The mygo terminal does more by default, and the attacker here is a malicious or compromised remote host. AI exec output reaches a pane only through Send to tab, and Send to tab pastes a command; it never runs output.

| Feature | Rule |
|---|---|
| OSC 52 clipboard write | off. There is no option today (`terminal.go:217-224`); the fork adds one, default off. |
| Links (OSC 8 and matched text) | `OpenLink` takes every link and refuses it, as the Electron app opens none. Amended 2026-10-14 (slice 2, plan Decision 15): opening `http`/`https` after a confirm that shows the full URL is deferred to a later slice; everything else (`file:` included) stays refused. |
| OSC 9/777 notifications | ignored |
| Window/tab title from the remote | not shown: the tab label is the server name only, as in Electron, and a remote title never becomes the window title or a notification. Amended 2026-10-14 (slice 2, plan Decision 14). |
| Query replies | sent without `user`, as above |
| Library build | `zig build -Demit-lib-vt -Doptimize=ReleaseSafe` (bounds and overflow checks kept) at the Ghostty commit mygo binds (`befcdfd2c3a1`, `plugins/terminal/internal/libbuild/main.go`), with Zig 0.16.0 (Ghostty's `minimum_zig_version`) and Xcode's linker, on a macOS CI runner. The build is not bit-reproducible (absolute paths in the debug map; the linker version), so egoist's prebuilt library cannot be verified by rebuilding it and is never used. |
| Library load | Loading a library is refused unless all of these hold: the packaging job builds the library, records its SHA-256 and embeds it into the app in the same job (a rebuild gives a different hash, so the hash binds the app to the exact library shipped beside it); a packaged app calls `os.Unsetenv("MYGO_GHOSTTY_VT")` before `terminal.Load`, checks that `terminal.LibraryPath()` is inside `Contents/Resources`, and compares the file's SHA-256 with the embedded one. Tests and CI set `MYGO_GHOSTTY_VT` to the source-built library, so nothing downloads egoist's prebuilt build (`plugins/terminal/internal/library/library.go:89-98`). |

Without hardened runtime, `DYLD_INSERT_LIBRARIES` can still inject code into the app process (and into the hub, as today). This spec does not change that (see Out).

### Approval column

The rules of `ApprovalPanel.tsx` and `approvals.ts` carry over, built on mygo primitives:

- **Allow and Send to tab are mouse-only.** `ui.Button` is always focusable, and Enter or Space press it (`mygo/ui/base.go:26`, `ui/api_gen.go:2610-2611`). These two are a custom element with no `Focusable`, with a `Role`, a `Label` and `Disabled`. A test checks that Tab never reaches them and that Enter never allows.
- **Each 500 ms guard has a listed source of change:**
  - the set of request ids
  - the list's height (`Element.Bounds()`, from the previous frame)
  - the scroll offset (`ScrollState.Y`)
  - the column being mounted

  `Disabled` is computed from these before `Clicked()` is read in the same frame, and `c.After(remaining)` redraws when the delay ends.
- **Deny default.** Enter in the reason field submits Deny (`OnSubmit`).
- **Focus.** An arriving request never takes focus. Cmd+Shift+A in a terminal is caught by a `Shortcut` on a container around the terminal view (never window-level, so no other key is taken from the remote; a container shortcut claims the key before the terminal sees it) and focuses the oldest reason field. Amended 2026-10-14 (slice 2, plan Decision 5): this replaces "caught with the terminal's `OnKey`". Esc there returns focus to the tab.
- **Clock.** The app owns its clock (`now func() time.Time`), because `ui.Tester` has no public fake clock.
- **Same rules elsewhere.** The other guarded dialogs (host key trust, file conflict and delete, auto-allow Enable, Settings Save) use the same mouse-only element and guard.

### Files

Paths are chosen with mygo's native dialogs (`mygo/dialog.go`) or dropped (`ui/drop.go`, `DroppedFiles()`) and go straight to `files.plan`.

The single-use grant tokens of `desktop/src/main/files.ts:11-30` existed only to keep paths out of the renderer. No renderer exists now, so they are dropped. These rules of `files.ts` stay:

- A download whose plan found no conflicts runs with `skip`. With conflicts, the app shows its conflict dialog and sends the user's choice, `overwrite` or `skip` (the hub takes no other value; Electron's `ask` lived only between its renderer and main). The UI cannot choose overwrite for a download without that dialog (`files.ts:140-141`). Amended 2026-10-15 (slice 4, plan Decision 3).
- The conflict dialog shows the destination folder and a sample of the conflicting names (`files.ts:143-147`).
- The choice applies to the job the dialog shows, and only while that job still waits for it (`files.ts:148-151`); a job the hub ended meanwhile takes no `files.run`.
- A plan whose id is still live is refused, and `files.run` is sent only for an id this app planned.
- Cancel pressed while planning is sent again when `files.planned` arrives (`plannedAction`).

### Crash

- **App panic.** Any panic in the app process ends it. The hub then exits on stdin EOF, and the key goes with it. There is no recover-then-lock: `recover` covers only its own goroutine, and a fault in native code cannot be caught. Running tunnels and terminals end with the hub, as when the Electron main process dies today.
- **Hub crash.** The app restarts the hub with backoff (see Hub process). The UI shows the locked screen, and terminal tabs show that their session ended.
- **Quit.** Close the hub's stdin. The hub then ends its terminals and file jobs itself (it audits the jobs with the reason `app closed`) and exits. Neither `term.closeAll` nor `files.cancelAll` is sent: `files.cancelAll` audits the reason `renderer reloaded`, which would be untrue here. Amended 2026-10-15 (slice 4, plan Decision 2).

### macOS integration

- **Tray.** `mygo.NewTray` with the template shield PNG and `SetTitle(pending count)`.
- **Background mode.** On close the window hides, and `Dock.Hide()` runs (`mygo/app.go:566`, activation policy Accessory) unless the app is quitting. Tray Show, a notification click and `OnActivate` show the window, bring it to the front with focus, and call `Dock.Show()`.
- **Menu.** Edit menu only, replacing mygo's default, which has View → Reload (`mygo/menu.go:662`).
- **Notifications** show only from the packaged app (mygo limitation); they are checked by hand.
- **Touch ID.** `sshgate-touchid` stays a Swift subprocess, and the app sends the password to the hub's `unlock` itself, as `desktop/src/main/touchid.ts` does. It keeps the same `touchid.json` rules (spec `2026-10-12-touchid-unlock-design.md`).
- **Version.** The app reads it from `mygo.App.Version()` (`mygo.json`). The packaging script stamps the same version into `sshgate` with `-X internal/mcpserver.Version`.

## Slices

Each slice is done when three things hold:
- `go test -race ./...` passes in the `app` module in CI on macOS, Linux and Windows.
- The Go tests porting the listed Electron assertions pass.
- The author has checked it by hand on macOS.

The root module's CI is unchanged.

| # | Slice | Electron spec assertions ported |
|---|---|---|
| 0 | **Spike and skeleton.** Covers:<br>• module, mygo `replace`, `build-ghostty-vt.sh` and its CI job<br>• `hubproc` + `door` with `hello`<br>• one terminal tab to `sshtestd` through `termconn`<br>• the fork's OSC 52 and `OnInput` hooks<br>• one mouse-only guarded button under `ui.NewTester`<br>• the password field (IME; VoiceOver reads it as secure)<br>• the app-death test (hub exits on EOF) | `throughput`, `ime`, `clipboard` |
| 1 | **Approval core.** Create vault, unlock and lock, idle lock, soft-lock screen (auto hosts, Stop auto-allow and lock), host list (read-only), AI requests column with all guards, tray count, notifications, store-error banner, hub restart. | `smoke` (non-terminal parts), `ui` (non-terminal parts) |
| 2 | **Terminals.** Tabs, host key prompts, Send to tab, tabs across lock, Cmd+Shift+A and Esc. | `smoke`, `idle`, `keyboard` (terminal parts), `ui` (terminal parts) |
| 3 | **Host management.** Host editor, SSH config import, auto-allow dialog and chips, Settings, paused and ran-while-locked banners. | `hosts`, `import`, `keyboard` (editor), `autoallow`, `autoallow-mcp`, `settings`, `manuallock`, `softlock` |
| 4 | **Files.** List, mkdir, rename, jobs, conflict and delete dialogs, file drop. | `files`, `live-files` |
| 5 | **Tunnels.** | `tunnels`, `live-tunnels` |
| 6 | **Audit.** Table, filters, Load older, "N new" pill. | `audit` |
| 7 | **macOS finish.** Touch ID, background mode, `.app` packaging with `sshgate`, `sshgate-touchid` and libghostty-vt, version stamp. The mygo app becomes the author's default. | `touchid`, `background` |

Slice 1 tests that need a live grant arm it with a test-side door call (`servers.save` with `autoAllow`), as `desktop/e2e/autoHelpers.ts` does through the UI today. The UI for arming comes in slice 3.

Tests that need an unpinned host write their own store, because `sshtestd -write-store` writes a pinned one.

### Stop gate after slice 0

Measure the packaged slice-0 app and the packaged Electron app on the author's Mac. Memory is `phys_footprint` (the `footprint` tool; Activity Monitor's Memory column), summed over every process of each app, the hub included, with both apps in the same state: vault unlocked, one terminal tab open, idle. Stop the project and stay on Electron if any of these fails:

| Measure | Threshold |
|---|---|
| Bundle size | ≤ 40 MB |
| Idle memory (`phys_footprint`), total | ≤ 120 MB and ≤ 40% of the Electron app's |
| `flood` throughput in a terminal tab | ≥ 80% of the Electron app's |
| `ime.spec.ts` and `clipboard.spec.ts` assertions | all pass in Go tests or by hand |
| `build-ghostty-vt.sh` in CI | builds the pinned commit, under 15 minutes, and the plugin's tests pass with that library |

The slice-0 report records all the numbers for both apps.

Amended 2026-10-14, after the first slice-0 measurement: the memory row used `ps` RSS. On macOS, Go returns freed pages with `MADV_FREE_REUSABLE`; they stay in RSS although the system can take them back, so RSS overstated the hub (about 70 MiB after the master-key KDF, in both apps) and was taken with the two apps in different states. The owner chose `phys_footprint` with both apps unlocked and one tab open; the thresholds are unchanged.

### Spike measurements, 2026-10-14

Measured on the author's Mac (Apple M1 Pro, 32 GB, macOS 24.6) with throwaway programs, before slice 0. They make the gate plausible; slice 0 measures the real app.

| Measure | Electron app | mygo | Notes |
|---|---|---|---|
| Bundle | 298 MB | about 24–31 MB, estimated | probe app 13 MB stripped, `sshgate` 8.8 MB, libghostty-vt ReleaseSafe 2.26 MB, `sshgate-touchid` 0.12 MB |
| Idle RSS | 335 MB (installed app, hub 18 MB included) | 78 MB, plus the hub's 18 MB | mygo probe: one window, one terminal, no other views |
| Peak RSS, 200 MB flood | main 174 MB + renderer 211 MB | 97 MB, flat | |
| Flood throughput | 47 MB/s end to end (`throughput.spec.ts`, 200 MiB in 4.46 s) | 85 MB/s through a local pty | not yet through the hub |
| Hub → UI door → Go client, no UI | 210 MB/s | 210 MB/s | the same for both apps, so the hub is not Electron's bottleneck |
| libghostty-vt parser (`BenchmarkOutput`) | — | 348 MB/s ReleaseFast, 267 MB/s ReleaseSafe | ReleaseSafe costs 23% at the parser and 1.4% per frame |
| libghostty-vt build | — | 64 s cold, 38 s ReleaseSafe with a warm package cache | Zig 0.16.0, Xcode 26; the plugin's 35 tests pass with the ReleaseSafe library |

Not measured: IME, clipboard and VoiceOver in the mygo terminal, and the end-to-end flood through the hub.

Alternatives checked and set aside:
- Pure-Go engines: `charmbracelet/x/vt` runs at 5 MB/s on mixed output and 0.07 MB/s on a `yes` flood; `hinshun/vt10x` has no scrollback.
- Opening hosts in Terminal.app: either exposes vault secrets or needs a hub socket any same-user process could use to skip approval.
- The fallbacks, in order:
  - a dylib built on the author's Mac and committed with its hash (no Zig in CI);
  - xterm.js in `ui.WebView` for the terminal pane only.

## Testing

Playwright `_electron` has no equivalent. Coverage moves to three layers:

- **Views:** `ui.NewTester`, headless, with a fake `door` and the app-owned clock. The pure-logic TS modules (`approvals`, `shell`, `terminals`, `files`, `audit`, `autoallow`, `settings`, `hostForm`, `hostkeys`, `tunnels`, `shared/display`) port to Go one to one, and their vitest cases become Go table tests.
- **Door client and terminal adapter:** against a real `sshgate hub` subprocess and `sshtestd`. Tests cover the ack backpressure, the swap and clamp of `Resize`, and that `user: true` is set only for key, paste and IME input.
- **End to end:** MCP bridge → hub → `sshtestd`, driven through the app's door client and checked against the audit log, as `cmd/sshgate/e2e_test.go` does. This replaces `autoallow-mcp.spec.ts`.
- **Stress:** an account-manager style `-stress` run that fires `pending`, `audit.appended` and `locked` under `-race`.

The hub's own tests are unchanged and keep covering every door rule.

## Risks

- **mygo is 18 days old.** It is at v0.4.x, and 297 of its 393 human commits are by one author, so expect API churn. The fork is pinned with `replace`, and upgrades are deliberate.
- **Fork changes.** OSC 52 option and `OnInput` hook. Each is small; each must be carried until upstream takes it.
- **Widgets to build.** Mouse-only button (about 30 lines), closable tab strip on `TabsBase` (about 80), audit list on a virtualized `ui.List` (about 150), side sheet and typed-name confirm (about 50).
- **Zig in CI.** Building Ghostty's VT library adds Zig 0.16.0 and a macOS runner with Xcode to CI. Fallback: commit a library built on the author's Mac with its hash.
- **Two desktop apps for months.** Electron stays the daily app until slice 7. Protocol changes in that time must land in both.

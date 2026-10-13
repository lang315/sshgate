# sshgate: Desktop app on mygo (native Go UI) — Design

Date: 2026-10-14
Status: Approved in chat 2026-10-14 (motivation, terminal library, platform scope, hub placement, migration, sections 1–2); this document awaits review.

## Goal

Replace the Electron desktop app (`desktop/`) with a native Go app built on mygo (`github.com/egoist/mygo`, fork `lang315/mygo`), drawn with mygo's `ui` package, with the hub running in the same process.

Why (all three hold):
- (a) Size and memory: `desktop/out/sshgate.app` is 298 MB, about 288 MB of it Electron.
- (b) One language: the desktop app is Go, like the rest of sshgate. No TypeScript, React, Node toolchain or xterm.js.
- (c) Dogfood mygo's native UI, where the owner's mygo work already is.

Exit gate (whole project): on the author's Mac, the packaged mygo app replaces the Electron app for daily use: create or unlock a vault, approve and deny AI requests through the real MCP bridge, auto-allow and soft lock, terminals, files, tunnels, audit, settings, Touch ID and menu-bar background mode all work, and every assertion of the Electron e2e specs listed in Slices has a Go test that passes.

## Scope

In:
- A new app, `cmd/sshgate-app`, and its code in `internal/app/`.
- macOS as the shipped target. The code builds and its tests run on Linux and Windows in CI.
- Building libghostty-vt from Ghostty's sources in this repo's CI.

Out:
- Packaging or shipping the mygo app on Windows or Linux. Electron stays the desktop app there until a later spec.
- Deleting `desktop/`. Decided separately after slice 7.
- Contributing changes back to upstream mygo.
- Any change to the hub's policy, the MCP door, the bridge, the vault format, the audit format or `hub --cli`.

## Decisions

| Question | Decision | Rejected |
|---|---|---|
| UI technology | mygo native `ui` | mygo webview keeping React (meets (a) only); stay on Electron |
| Terminal | `mygo/plugins/terminal` (libghostty-vt), library built from Ghostty source at a pinned commit in CI, signed with the bundle | prebuilt library from egoist's releases; xterm.js in `ui.WebView` |
| Hub placement | in-process, the UI calls `*hub.Hub` directly through a facade | subprocess speaking the UI door; in-process `ServeUIDoor` over `io.Pipe` |
| Migration | native from the start, one slice at a time; Electron stays the daily app until slice 7 | React in `ui.WebView` first; big-bang |
| Platforms | macOS first; Linux/Windows build and test only | all three at once; macOS only |

## Architecture

### Module and layout

The app lives in this module: `internal/hub` is `internal`, so no other module can import it.

- `go.mod` moves to `go 1.27.1` (mygo's minimum) and gains `require github.com/egoist/mygo` with a `replace` to the `lang315/mygo` fork.
- The `sshgate` CLI binary does not import mygo, so it does not link it.

```
cmd/sshgate-app/        main: mygo.App, window, tray, menu, single instance
internal/app/
  facade.go             the only code that calls *hub.Hub; holds the door-layer invariants
  views/...             unlock, hosts, approvals, terminal, files, tunnels, audit, settings
internal/hub/           unchanged; ServeUIDoor and hub --cli stay for headless use
```

### Data flow

- View → facade → `*hub.Hub`, direct Go calls, no JSON-RPC.
- Hub events (pending, locked, `audit.appended`, `autoAllow.ran`/`off`, `files.*`, tunnel ends, terminal exit) reach the facade through callbacks. The facade hands them to mygo's UI thread.
- Views are functions of app state, in mygo's `ui.View(s.view)` style.

### Facade invariants

The UI door (`internal/hub/uidoor.go`) enforces rules that a direct caller would bypass. The facade takes them over, each with a test:

1. **Idle activity.** Every facade method except `Status` calls `h.touch()`, as `uidoor.go:155-159` does for every request but `status`. Terminal input counts only when it comes from a real key or paste event, the equivalent of `term.write` with `user: true`. Resize does not count.
2. **No polling.** Nothing calls the facade from a timer or in response to a hub event. Only real UI actions call it. A test fails if a view package imports `internal/hub`.
3. **Lock ordering.** `audit.appended` and `autoAllow.ran` never reach the UI after `locked`. They are delivered only while `h.unlocked` is true, under one mutex that the `locked` delivery also takes, and `locked` is dropped when its `lockGen` is no longer current. This mirrors `sendMu` (`uidoor.go:94-142`).
4. **Same refusals.** The facade exposes the same operations as the UI door's method list, with the same parameter rules where they carry policy (for example, `Lock(stopAuto bool)`). It exposes nothing the door does not. `term.closeAll` and `files.cancelAll` are called only by the app's crash path.

If a rule lives in `uidoor.go` rather than in `*Hub`, slice 1 either moves it into `*Hub`, so both doors share it, or duplicates it in the facade with a test. Moving it into `*Hub` is preferred.

### Terminal

- Each tab is a `terminal.New` with `Options.Conn` set to an `io.ReadWriteCloser` over the hub's SSH terminal session.
- `term.ack` flow control goes away, because the plugin's read loop blocks while it parses, which gives backpressure.
- Tabs stay open across lock, as today.
- libghostty-vt is built by a script in this repo (`scripts/build-ghostty-vt.sh`) from a pinned Ghostty commit with `zig build -Demit-lib-vt -Doptimize=ReleaseFast`, then bundled and signed inside the app.

### Process and packaging

One process holds the hub, the MCP door socket, the UI and the terminal library. `sshgate-touchid` stays a Swift subprocess. The bundle still ships the `sshgate` binary as the MCP bridge.

- **Single instance.** The hub's existing socket check (`internal/hub/listen_unix.go`, "another sshgate hub is already running") makes the mygo app, the Electron app and `hub --cli` mutually exclusive.
- **Packaged gating.** A packaged build (build tag or `-ldflags -X`) ignores every `SSHGATE_*` knob and `$MYGO_GHOSTTY_VT`. In the installed app, whoever sets the environment could otherwise point the vault-holding process at another store, another helper or another native library.
- **Version.** The version reaches `internal/mcpserver.Version` with `-X`. The build script passes it, since mygo's CLI documents no `-ldflags` passthrough.

## Security changes

These are accepted trade-offs of in-process placement. They must be recorded in `CLAUDE.md` when the app ships.

- **Shared crash domain.** A panic in Go UI code or a fault in libghostty-vt kills the vault, the broker and every SSH session together. There is no more hub restart with backoff. A restarted app starts locked, as a restarted hub does today.
  - Mitigation: the app recovers panics at UI goroutine boundaries. On a recovered panic it hard-locks (`Lock(stopAuto: true)`) before showing the error.
- **Shared memory.** The master key shares an address space with third-party native code (libghostty-vt, and WebKit if `ui.WebView` is ever used).
  - Mitigation: build that library from source at a pinned commit, sign it with the app, and refuse to load any other copy in a packaged build.
- **Kept.** The master key is never read from env or a file. The MCP door still cannot unlock, decide or read secrets. Every hub policy is unchanged.

## Slices

Each slice is done when three things hold:
- `go test -race ./...` passes in CI on macOS, Linux and Windows.
- The assertions of the Electron specs listed for it have Go equivalents that pass.
- The author has checked it by hand on macOS.

| # | Slice | Electron specs whose assertions it ports |
|---|---|---|
| 0 | **Spike and skeleton.** `go.mod` bump, mygo via `replace`, empty window, Edit-only menu with no Reload, packaged gating, the libghostty-vt build script, one terminal tab to `sshtestd`. Measure bundle size, idle RSS, `flood` throughput, IME and paste against the Electron app. | `throughput`, `ime`, `clipboard` (as measurements) |
| 1 | **Approval core.** Facade, create vault, unlock and lock, idle lock, soft lock (unlock screen lists auto hosts, Stop auto-allow and lock), host list (read-only), AI requests column (Deny default, mouse-only Allow, 500 ms guard, Deny all), tray with pending count, notifications. | `smoke`, `idle`, `manuallock`, `softlock`, `keyboard`, `ui` |
| 2 | **Terminals.** Tabs, host key prompts (unknown, mismatch), Send to tab, tabs kept across lock. | `hosts` (terminal parts) |
| 3 | **Host management.** Host editor, SSH config import, auto-allow dialog, Settings (soft-lock limit), paused and ran-while-locked banners, store error banner. | `hosts`, `import`, `autoallow`, `autoallow-mcp`, `settings` |
| 4 | **Files.** List, mkdir, rename, upload, download and delete jobs, conflict dialog, OS file drop (`ui/drop.go`). | `files`, `live-files` |
| 5 | **Tunnels.** | `tunnels`, `live-tunnels` |
| 6 | **Audit.** Table, filters, Load older, "N new" pill. | `audit` |
| 7 | **macOS finish.** Touch ID, menu-bar background mode (`Dock.Hide`, activation policy), `.app` packaging with `sshgate`, `sshgate-touchid` and libghostty-vt, version stamp. The mygo app becomes the author's default. | `touchid`, `background` |

### Stop gate after slice 0

Stop the project and stay on Electron if any of these holds:
- The terminal is clearly worse than xterm.js in Electron at `flood` throughput, IME or paste.
- libghostty-vt cannot be built from source in CI.
- Bundle size or idle RSS does not drop substantially.

The slice 0 report records the measured numbers for both apps.

## Testing

Playwright `_electron` has no equivalent here. Coverage moves to three layers:

- **Views:** `ui.NewTester`, headless. The anti-misclick guards (500 ms delay after a change, mouse-only Allow and confirm buttons, typed-name confirm, Deny as the default) become unit tests.
- **Facade:** runs against a real `*hub.Hub` with `internal/sshx/sshtest`. Tests cover the `touch()` rule, no `audit.appended` after `locked`, and parity of refusals with the UI door.
- **End to end:** MCP bridge → app facade → `sshtestd`, checked against the audit log, as `cmd/sshgate/e2e_test.go` does. This replaces `autoallow-mcp.spec.ts`.

## Risks

- **mygo is 18 days old.** It is at v0.4.x with mostly one author, so expect API churn. The fork is pinned with `replace`; upgrades are deliberate.
- **Notifications do not show from an unpackaged binary on macOS.** They can only be checked in the packaged app.
- **Zig in CI.** Building Ghostty's VT library adds Zig to the toolchain, on macOS at least.
- **File drop.** It goes through `ui/drop.go`'s `DroppedFiles()`, not a web `File`. The single-use grant tokens become unnecessary, since paths never leave Go. The upload confirmation flow must still show the paths before any job runs.

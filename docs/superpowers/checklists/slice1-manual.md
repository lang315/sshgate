# Slice 1 manual checklist

These are the checks from `docs/superpowers/specs/2026-09-24-desktop-app-design.md`
(§Testing, "Manual, per OS", and Success Criterion 6) that automated tests
cannot cover: real IMEs, real OS notification/tray chrome, real throughput
against a real server, and real Windows named-pipe security. Run all of
them on macOS, Windows, and Linux before calling slice 1 done, unless a
step says otherwise.

Build first: `go build -o ssh-mcp ./cmd/ssh-mcp` at the repo root, then
`cd desktop && npm ci && npm start`.

- [ ] **1. Vietnamese IME in a terminal**

  Open a terminal tab to any server. Using the OS's Vietnamese input method,
  type `xin chào`:
  - macOS: Telex
  - Windows: Unikey
  - Linux: ibus, and separately fcitx5

  Pass: the remote shell (echo it back, e.g. `cat` or your prompt) shows
  exactly `xin chào`, with the correct diacritics, on all four
  IME/OS combinations tried.

- [ ] **2. Throughput**

  **Automated part:** `desktop/e2e/throughput.spec.ts` floods a tab with
  200 MiB from `sshtestd` (`flood <N>`) and checks the largest
  requestAnimationFrame gap (≤ 200 ms), that every keystroke typed during
  the flood reaches main as `term.write` (sshtestd echoes typed input only
  after the flood, so echo latency itself is not measured), and that the
  main and renderer working sets in the last third of the flood are ≤ 1.5×
  those in the first third. It writes its numbers to
  `desktop/test-results/throughput.json`. **Still manual:** a real server
  over a real network, the comparison against a native terminal, and
  dragging the window.

  Measured 2026-09-25, macOS 15 (Darwin 24.6.0), Apple M1 Pro, 32 GB:

  | Flood | Elapsed | Throughput | Max frame gap | Main WS first → last third | Renderer WS first → last third | Renderer JS heap after forced GC |
  |---|---|---|---|---|---|---|
  | 200 MiB (spec default) | 4.43 s | 47.4 MB/s | 19 ms | 159 → 174 MB | 298 → 535 MB | 273 MB |
  | 1 GB (`FLOOD_BYTES=1000000000`) | 18.25 s | 54.8 MB/s | 25 ms | 176 → 204 MB | 664 → 1863 MB | 1282 MB |
  | 2 GB (`FLOOD_BYTES=2000000000`) | 33.96 s | 58.9 MB/s | 59 ms | 182 → 230 MB | 1097 → 3494 MB | (not sampled) |
  | 200 MiB, after the fix below | 4.43 s | 47.4 MB/s | 18 ms | 159 → 173 MB | 185 → 206 MB | 5 MB |
  | 1 GiB (`FLOOD_BYTES=1073741824`), after the fix | 21.82 s | 49.2 MB/s | 18 ms | 177 → 202 MB | 221 → 255 MB | 5 MB |

  Throughput, frame gaps and keystroke handling (≤ 16 ms per key, every
  key reached main) pass. The first three rows show a renderer leak (about
  1.3 bytes of JS heap per flooded byte, kept after a forced GC). A heap
  snapshot traced it to `App.tsx` calling `setItems` for every hub event,
  including every `term.data`. React keeps each no-op update, along with the
  event and its base64 chunk, in the hook queue until App re-renders again,
  which does not happen during a flood. Since the fix, only approval events
  update `items`, and renderer memory stays bounded.

  In a terminal tab to a real server (not `sshtestd`), run:
  - `cat` on a 50 MB file
  - `yes | head -c 200M`

  Compare against the same commands over `ssh` in your native terminal
  app. While the flood is running:
  - Drag the window around — it must not visibly freeze for more than
    200 ms.
  - Keep typing in the terminal — keystrokes must keep echoing without a
    long pause.
  - Watch renderer and main process RSS (Activity Monitor on macOS, Task
    Manager on Windows, `ps` on Linux) — both must stay bounded, not grow
    without bound for the duration of the flood.

  Pass: all three hold. If any fails, it is not a slice-1 blocker by
  itself, but note it — the pre-planned mitigation is switching the hub↔
  Electron stdio channel from base64-in-JSON to length-prefixed binary
  frames (see the spec's Known Risks).

- [ ] **3. Notification click focuses the app**

  Unfocus (or minimize) the app window. From another machine or process,
  submit an AI `exec`/`sudo-exec` against an AI-visible, host-key-pinned
  server (e.g. via the MCP bridge, or `doorCall` as in `desktop/e2e/doorClient.ts`).
  An OS notification should appear. Click it.

  Pass: the app window is focused/restored, and the approval panel shows
  the request that triggered the notification.

- [ ] **4. Windows: MCP door pipe DACL and SID checks**

  Windows only. The MCP door is a named pipe `\\.\pipe\ssh-mcp-hub-<SID>`
  (`internal/hub/listen_windows.go`), created with an owner-only DACL; the
  bridge verifies the pipe's server process SID against its own before
  sending anything (`checkSID`, `internal/hub/peer_windows.go`). There is
  no automated coverage of this on Windows — the Playwright smoke test
  skips `win32` (`desktop/e2e/smoke.spec.ts:12`) and the CI `desktop` job
  runs on Ubuntu only — so this step is the only check of it.

  With the app (and hub) running as your user:
  - Connect the bridge (`ssh-mcp` with no `--host`, or `claude mcp add
    --transport stdio ssh-mcp -- ssh-mcp`) as the **same** Windows user.
    Pass: `exec`/`list-servers` work normally.
  - Connect the bridge as a **different** Windows user (e.g. a second
    local account, or a service account). Pass: the connection is
    refused — the bridge reports a pipe/connection error, not a hang or a
    successful exec.

- [x] **5. Idle auto-lock**

  **Covered by e2e:** `desktop/e2e/idle.spec.ts` runs with
  `SSH_MCP_IDLE_LOCK=3s` (`hub --idleLock=3s`). It checks the lock, the
  message, that the tab keeps its shell across unlock, and that a pending
  AI request holds the lock off for 6 s and the lock follows once the
  request is denied. The full 15-minute default is not exercised.

  Unlock the app, then leave it alone — no keyboard/mouse input in the
  app, no terminal typing, and no AI request pending or running — for 15
  minutes (the default in `internal/hub/idle.go`).

  Pass: the app returns to the unlock screen on its own and shows "Locked
  after inactivity." Any terminal tab that was open before
  the lock is still present afterward (still shows its prior output) and,
  once you unlock, accepts typing again in the same shell without any
  reconnect — its SSH connection was never dropped.

- [ ] **6. Allow cannot be triggered from the keyboard**

  **Mostly covered by e2e:** `desktop/e2e/smoke.spec.ts` focuses Allow and
  presses Space and Enter (the request stays pending), then checks that
  Enter in the reason field submits Deny and that Allow is disabled after
  the list shifts. **Still manual:** tabbing through every stop in the
  panel and pressing Enter and Space at each one.

  Trigger an AI approval request so it appears in the panel. Without
  clicking, press Tab repeatedly through the panel, then press Enter and
  Space at each stop; separately, focus the Allow button directly (e.g. by
  tabbing into the reason field first, then Tab once more) and press
  Enter and Space.

  Pass: the request is never allowed by Tab, Enter, or Space alone. Only activating
  the Allow button itself (a click) approves it, and only after its 500 ms
  delay.
  Enter in the reason field, by contrast, submits Deny — confirm that
  still works as the keyboard's only reachable action.

- [x] **7. Tabs survive Lock/unlock**

  **Covered by e2e:** `desktop/e2e/smoke.spec.ts` (R15(b)) types into a
  tab, clicks Lock, unlocks, and checks that the same tab keeps its output
  and its shell (the next typing continues the same input line).
  `idle.spec.ts` does the same across an idle lock.

  Open a terminal tab and run a long-lived or stateful command (e.g. `top`,
  or `cd /tmp && pwd`). Click **Lock**. Confirm the unlock screen appears.
  Unlock again.

  Pass: the same tab is still there, still showing its prior output, and
  you can keep typing in it (e.g. `pwd` still shows `/tmp`) without
  reconnecting — the SSH session was never interrupted by the lock.

- [ ] **8. Tray count and OS notifications**

  With the app unfocused, trigger two or more pending AI requests at once
  (e.g. two overlapping `exec` calls from a script).

  Pass, per OS:
  - macOS: the tray icon's title text shows the pending count (e.g. `2`).
  - Windows and Linux: the tray icon's tooltip (hover text) shows the
    pending count (the count is not in visible title text on these two;
    tray title text is macOS-only).
  - All three: one OS notification appears per request while the window
    is unfocused, and clicking the tray icon's "Show" menu item (or the
    icon itself, where the OS supports it) restores/focuses the app.

- [ ] **9. Windows and Linux: terminal keys are not menu shortcuts**

  Windows and Linux only (macOS keeps Cmd+C/V for the clipboard). In a
  terminal tab, press Ctrl+R, Ctrl+W, and Ctrl+C.

  Pass: each reaches the remote shell (Ctrl+R starts reverse history
  search, Ctrl+W deletes the previous word, Ctrl+C interrupts); the window
  never reloads or closes, and the tab keeps its session.

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

- [ ] **5. Idle auto-lock**

  Unlock the app, then leave it alone — no keyboard/mouse input in the
  app, no terminal typing, and no AI request pending or running — for 15
  minutes (the default in `internal/hub/idle.go`).

  Pass: the app returns to the unlock screen on its own and shows "Locked
  after 15 minutes of inactivity." Any terminal tab that was open before
  the lock is still present afterward (still shows its prior output) and,
  once you unlock, accepts typing again in the same shell without any
  reconnect — its SSH connection was never dropped.

- [ ] **6. Allow cannot be triggered from the keyboard**

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

- [ ] **7. Tabs survive Lock/unlock**

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

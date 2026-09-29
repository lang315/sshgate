# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

(Electron desktop app, `desktop/`: React + xterm.js in a Chromium renderer. Web design language, desktop window; no mobile target.)

## Users

One user: the author, a developer/ops person managing a handful to a few dozen of their own servers. The app stays open all day. They use it for two jobs at once: working in SSH terminal tabs by hand, and approving or denying shell commands that Claude Code (through the sshgate MCP bridge) asks to run on those servers. A second user does not exist yet; design for this one person until one does.

## Product Purpose

sshgate lets an AI agent run commands on real servers with a human decision on every single command by default. The human may put one host on auto-allow (plain exec by default; root hosts and sudo-exec only when the human opts in per host) for a set time, or until turned off with a Resume after each unlock; it is visible while on, audited, and stoppable at any time. The desktop app is that human's control surface: it holds the unlocked vault, the SSH terminals, and the approval queue. Success means the author does their SSH work in this app instead of another terminal, and never approves an AI command they did not mean to.

## Positioning

Every AI-issued command blocks on an explicit human Allow by default. The one exception is a host the human has put on auto-allow: a grant is not a privilege boundary, and anything it leaves running (cron jobs, SSH keys, shell startup files) outlives the grant. The AI only sees hosts marked visible and only connects to hosts whose key is pinned. A plain SSH client (Termius, a terminal) has no approval gate; an AI shell tool has no human in the loop. This app is both a daily SSH client and the gate.

## Operating Context

- Claude Code runs in another window; its `exec`/`sudo-exec` calls arrive as pending requests (at most 5, expiring after 5 minutes) while the user may be typing in a terminal tab (plain exec on a host with an auto-allow grant runs without one).
- The vault is encrypted (master password, argon2id); the app starts locked and auto-locks after 15 minutes idle, unless a timed auto-allow grant is holding that off until its deadline. Terminal tabs survive lock and hub restarts.
- Hosts are added and edited in the app (slice 2a). First connection to a host asks the user to trust its key fingerprint.
- OS notifications and a tray count signal pending requests when the window is unfocused.

## Capabilities and Constraints

- Screens that exist: create vault, unlock, host list with New/Edit/Delete, host editor (secrets write-only, Forget host key), terminal tabs, Files tab per host (listing, transfer strip) with its New folder, Rename, Delete files, and Files already exist dialogs, AI approval panel, host-key trust and mismatch dialogs, hub starting/restarting/failed, store-error banner.
- Security behaviour is fixed and must survive any redesign: Deny is the default and keyboard-reachable; Allow is mouse-only and disabled for 500 ms after anything in the list changes; "Deny all" is always rendered so the list never shifts; the host-key Trust button, the auto-allow dialog's Enable, and the paused banner's Resume follow the same rules, as do the Files dialogs' Overwrite all, Skip existing, and Delete (Cancel is their default); secrets are never displayed; the renderer never polls the hub (only `status`), or the idle lock never fires.
- Not built and not to be designed as if present: Keychain, snippets, ProxyJump, local shell, split panes, sync (later roadmap slices, each behind an entry gate).
- UI language: English.
- Themes: dark, light, and Auto (follows the OS), chosen with a ☾ / ☀ / Auto control; Auto is the default, and the choice is a per-machine display preference.
- Playwright e2e tests select on current roles, labels and a few class names (`nav.hosts`, `.tabbar .tab`, `.approvals`, `.approval`, `button.allow`, `button.denyall`); a redesign updates them in the same change.

## Brand Commitments

- Name: sshgate (renamed from ssh-mcp on 2026-09-26, when the project left the tufantunc/ssh-mcp fork).
- No logo for now (the user skipped it). Colours are delegated to the design work; no separate brand palette exists.
- Standing visual preference (2026-09-25): the user's own design (`ssh-mcp Desktop UI.html`): warm-neutral dark, teal focus/link accent, Termius-like structure (host cards, top tabs) at Termius's craft level. System fonts. Not Termius's name, logo, or assets.

## Evidence on Hand

None. No screenshots, testimonials, or usage numbers exist; do not fabricate any.

## Product Principles

1. A human decides every AI command by default; the interface never makes Allow easier than Deny. The one exception a human can opt one host into, auto-allow, stays visible, audited, and stoppable at any time, and is never a privilege boundary.
2. Nothing moves under the cursor: layout changes near a decision button reset its delay.
3. The terminal is the daily workspace; everything else yields space to it.
4. Show only what exists; no placeholder features.
5. Fail visibly: errors from the hub or the vault are shown, never swallowed.

## Accessibility & Inclusion

Keyboard use must reach everything except the deliberately mouse-only Allow, Trust, the auto-allow dialog's Enable, the paused banner's Resume, and the Files dialogs' Overwrite all, Skip existing, and Delete. Vietnamese input (IME) must work in terminals.

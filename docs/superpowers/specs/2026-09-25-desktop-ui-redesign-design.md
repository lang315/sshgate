# Desktop UI redesign (Hosts home tab, docked AI column, dark/light/Auto)

Date: 2026-09-25. Status: approved design (rev 2), not yet planned.
Product context: `PRODUCT.md`. Visual reference: the user's design `ssh-mcp Desktop UI.html` (7 boards: workspace, unlock, create vault, host editor, unknown host key, host key changed, hub states), reviewed by four agents against rev 1 of this spec; this revision merges the two.

## Goal

Replace the desktop app's placeholder look with a finished SSH client shell: one tab strip with a fixed Hosts home tab, a searchable host card grid, a docked AI approval column that opens itself and collapses when idle, a sectioned host editor, clearer host-key dialogs and lock screens, and dark, light and Auto themes. The terminal gets the full window width whenever nothing needs the user.

No hub, protocol, IPC or main-process change. Every approval and host-key safety rule survives, and four are added (§Safety).

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Scope | Only existing features | PRODUCT.md: Keychain, snippets, forwarding, SFTP belong to gated slices. |
| Hosts | Home tab (`⌂ Hosts`) with card grid and search; no sidebar | A permanent sidebar plus a permanent AI column leaves ~94 terminal columns at 1440 px (74 at 1280); the design's own screenshot clips log lines. Switching hosts is occasional, approving is constant. |
| AI approvals | Docked right column, collapsible, opens itself on a new request | An overlay drawer (rev 1) would appear under the cursor over the terminal being used; Send to tab has no delay today, so a stray click could paste the AI's command into the user's prompt. A docked column shrinks the terminal (xterm reflows) instead of hiding output. |
| Look | The user's design: warm-neutral dark, teal focus/link accent, design's card, editor and dialog layouts | The user drew it. Replaces rev 1's navy canon (PRODUCT.md updated). |
| Themes | Dark (design), light (derived, contrast-checked), Auto; ☾ / ☀ / Auto control; default Auto | The user asked for both themes and the control; the design is dark-only. |
| Fonts | System UI stack and system mono | No bundled files; no risk of losing Vietnamese glyphs in the terminal grid. |
| Dependencies | None added; inline SVG icons | Nothing here needs a package. |
| Dropped from the design | App bar, terminal status bar, expiry countdown, "locks after 15 min idle", restart segments, store-error banner on hub screens, Allow fill bar | No data behind them (expiry and idle duration are not in the protocol; the restart maximum is not sent), always-true text ("Vault unlocked", "pinned"), or an impossible state (no `status` while the hub is down). The fill bar draws the eye to Allow. |

## Structure

`App.tsx` keeps its data flow (status refresh on `locked`/`pending` events only, no polling, one `servers` fetch per screen change or edit, approval reducer). Only the rendered tree changes.

### Ready screen

```
┌ tab strip (40 px) ───────────────────────────────────────────────────────────┐
│ [⌂ Hosts] [● prod-web-01 ×] [● db-primary ×] [○ staging · exited Reconnect ×] │
│                                        [AI 2] [☾ ☀ Auto] [Lock]              │
├──────────────────────────────────────────────────────────────┬───────────────┤
│ store-error banner (only when status.storeError)             │               │
├──────────────────────────────────────────────────────────────┤ AI requests   │
│ Hosts view  OR  the active terminal                          │ column 380 px │
│                                                              │ (collapsible) │
└──────────────────────────────────────────────────────────────┴───────────────┘
```

**Tab strip** (`TerminalTabs.tsx` renders it; `App` passes the right cluster as a `ReactNode` prop):
- `⌂ Hosts` home tab, class `hometab` (not `tab`, so `.tabbar .tab` still means terminal tabs). Active when `TabSet.active` is undefined. Clicking it calls a new `TabSet.showHome()`. Closing the last terminal tab already leaves `active` undefined, which now shows Hosts.
- Terminal tabs keep today's markup: `.tab` containing a name `<button>` first (not `role="tab"`; e2e takes `.tabbar .tab` → first button), `Reconnect` when exited, `×` with `aria-label="Close"`. Add a 7 px status dot (connected `--ok`, exited `--muted` ring) and ` · exited` text. Active tab: `--surface` background joined to the content below. Tab `title` = `user@host:port`.
- Right cluster: AI button (`aria-label="AI requests"`, shows the count; toggles the column; amber when requests wait and the column is collapsed; hidden when the count is 0 and the column is collapsed), theme control, `Lock` button (icon + text).

**Theme control:** segmented, `role="radiogroup"` `aria-label="Theme"`, three `role="radio"` buttons `☾` (`aria-label="Dark"`), `☀` (`Light`), `Auto`.

### Hosts view (`HostList.tsx`, root stays `nav.hosts`)

- Header: search input (`placeholder="Search hosts"`, `aria-label="Search hosts"`), case-insensitive substring over name, host and user, renderer-only (no hub call, so it cannot touch the idle lock); `+ New host` (accessible name `New host`).
- Section label `Hosts · n`.
- Grid `repeat(auto-fill, minmax(260px, 1fr))`, 12 px gap. Card: 36 px letter tile (`--accent-tint` background, `--accent` letter; one colour for all hosts), name (600), `user@host:port` in mono `--muted`, chips `AI` (accent tint) and `NEW KEY` (amber tint, when no pin). The card body is the open button (accessible name = host name). `Edit <name>` and `Delete <name>` icon buttons are always in the tab order; visible on hover and on focus-within, and and shown whenever focus is inside the card.
- Empty states: no hosts → "No hosts yet" + New host; no matches → `No hosts match "<query>"`.
- Footer: `Vault file: <storePath> — copy it to back up.`

### Terminal area

Unchanged behaviour: tabs stay mounted (hidden, `inert`) through lock and hub restarts. xterm font: `ui-monospace, "SF Mono", Menlo, Consolas, "DejaVu Sans Mono", monospace`, 13 px, line height 1.4; colours from theme tokens (§Themes). Opening or collapsing the AI column resizes the terminal through the existing fit/resize path.

### AI requests column (`ApprovalPanel.tsx`)

- Docked in the layout grid (`grid-template-columns: 1fr 380px` when open, `1fr 0` when collapsed); it never overlays anything.
- **Open state lives in `App`** (`aiOpen`). Opens when a `pending` event brings an id not seen before, and on the AI button. Collapses only from its header `×` (`aria-label="Close AI requests"`) or the AI button. Collapsing never denies or dismisses anything; requests keep waiting and the AI button turns amber with the count.
- **Mounted only while open**, so the existing "Allow disabled 500 ms after the panel mounts" rule covers the column appearing.
- Header: `AI requests` + count badge, `Deny all` (always rendered; disabled below 2), and one help line: `Every command waits for you. Enter in a request denies; Allow takes a mouse click.`
- Request card (from the design):
  - Line 1: server name (600), `SUDO` chip when sudo, received time right-aligned in mono `--muted`.
  - Line 2: `user@host:port` target in mono.
  - Command block: mono on `--term-bg`, wraps, non-ASCII runs in `<mark>` with `unicode-bidi: isolate`.
  - When the command has non-ASCII: warning line `⚠ N non-ASCII characters highlighted (U+0456, …)`, at most 5 code points then `+N more`.
  - Description box labelled `AI's description · unverified` (only when present).
  - Meta line: `timeout Ns · client <name> (unverified)`.
  - `Reason (optional)` as a visible `<label>` for the input; the input keeps `placeholder="Reason (optional)"` so e2e selectors hold.
  - Actions row: `Deny` (filled `--danger-fill`, white label, `<kbd aria-hidden="true">↵</kbd>` hint, the form's only submit), `Send to tab` (neutral outline, middle), `Allow` (outline `--ok`, right; never filled, including on hover). Send to tab sits between Deny and Allow so a click that misses Deny does not land on Allow.
- Card left edge 3 px: `--wait` normally, `--danger` for sudo; the `SUDO` chip is danger-tinted (not amber: amber already means "waiting").

### Host editor (`HostEditor.tsx`)

Right slide-in panel, 600 px (full width below 700 px window width), over a dimmed backdrop; `role="dialog"` `aria-label="Host editor"`. Scrolling body, fixed header and footer, so Save never moves when a warning appears.
- Header: `Edit <name>` or `New host`, `×` with `aria-label="Close host editor"` (not `Close`: the footer owns that name).
- **Connection:** Name | User; Host (with `· changed` marker, `aria-hidden`, linked by `aria-describedby`) | Port (same marker); Auth as a segmented `password / key / agent` radio group | Key path (cell reserved for all auth modes; field shown only for key, so switching auth never shifts the layout).
- **Secrets:** note `Never shown. Leave empty to keep what is saved.`; 2×2 grid Password, su password, sudo password, Key passphrase (shown per auth as today); placeholder `saved` when stored, `will be cleared` when the endpoint changed or Clear was pressed; `Clear` link beside the label.
- **AI:** `Visible to AI` switch in a neutral panel with `AI clients can see this server and ask to run commands. Each command still waits for your approval.` plus `Needs a pinned host key.` when unpinned.
- **Host key:** `<algo> SHA256:<fingerprint>` in full, mono, selectable (never the base64 key, never truncated); `Forget host key` (danger outline) with the note `Takes effect immediately.`; unpinned: `Not pinned. You will be asked to confirm it on the next connect.`
- Warnings, grouped in one `role="status"` box above the footer: the host/port-change warning and `Saving will close N open tabs.` (existing text).
- Footer: `Close` (secondary), `Save` (primary: `--text` fill with `--bg` label, as in the design). Esc closes unless saving. Error line above the footer.

### Host-key dialogs (`HostKeyDialog.tsx`)

Centered modal cards, 520 px, same accessible names (`Unknown host key`, `Host key mismatch`).
- **Unknown:** title `Trust this host key?`; server and `user@host:port`; intro `No host key is pinned for this server yet. Check the fingerprint against one you got another way, from the server's console or its admin.`; block labelled with the key type as sent (not uppercased) and the full `SHA256:` fingerprint; known_hosts row in three states: match (neutral, check icon, not success-coloured: it is a hint), different (danger, existing warning text), absent (muted); line `Trust only if this matches the key you expect. Once trusted it is pinned, and a different key later is refused.`; buttons `Cancel` (filled, autofocus, submit, `↵` hint `aria-hidden`) and `Trust and connect` (outline, mouse-only, disabled 500 ms after content changes, as today).
- **Mismatch:** red top edge and banner `This server presented a different host key. Nothing was sent.`; `Pinned` and `Presented now` (danger) fingerprints; `Did the key change on purpose?` + `If the server was rebuilt or its keys rotated, confirm the new fingerprint with its admin, then forget the old key in the host editor and connect again.`; buttons `Open host editor` (outline) and `Close` (filled, autofocus). No Trust.

### Unlock, Create vault, hub screens

Page background `--bg`; one centered 400 px card on `--surface`, 10 px radius.
- Mark: a 28 px `--accent-tint` tile with a terminal glyph (inline SVG) and the `ssh-mcp` wordmark; no other logo.
- **Unlock:** `Unlock vault`; amber chip `Locked after inactivity.` (with the period) after an idle lock; after an idle or manual lock (not after a hub restart) the line `Open terminals stay connected while locked. AI requests are refused until you unlock.`; visible label `Master password`; `Unlock` full width; error slot; under the card `Vault file <storePath>`.
- **Create vault:** `Create your vault`, lead sentence, visible labels `New master password` / `Confirm master password`; live checklist `At least 8 characters`, `Both passwords match` (split from `vaultPasswordProblem`, with `aria-live="polite"`); amber box `The master password cannot be recovered. If you forget it, this vault cannot be unlocked and its saved passwords are lost.`; kept-servers box `These servers from your existing file are kept, with Visible to AI turned off and their host keys unpinned; you'll confirm each key on the next connect.`; `Create vault` disabled until the checklist passes; error slot.
- **Hub starting:** spinner, `Starting the hub…`.
- **Hub restarting:** `Hub crashed. Restarting in N s (attempt n).` with a local 1 s countdown, and `The vault will be locked again after the restart. Open terminals will end.`
- **Hub failed:** `The hub stopped`, `state.message` as sent, stderr in a mono scroll box.
- No store-error banner on these screens.

### Store-error banner

40 px strip under the tab strip, `--danger-tint` background, `--danger` icon and text, `role="alert"`, no close; first letter capitalised in CSS (`::first-letter`), text unchanged from the hub.

## Safety

Unchanged and restated: Deny is the default and the form's only submit; Enter in Reason denies; Allow and Send to tab are `tabIndex={-1}` with `blockKeyboardActivation`; the 500 ms Allow delay restarts after a request appears, after any change to the list's ids or height, and after the column mounts; the delay is **list-wide** (all Allows together), never per card; `Deny all` always rendered; description and client labelled unverified; target shown; host-key Trust mouse-only with a 500 ms delay and Cancel default; mismatch has no Trust; secrets never displayed; the renderer never polls.

Added:
1. **Send to tab gets the same list-wide 500 ms delay as Allow.** A mis-click pastes the AI's command into the user's own prompt.
2. **Scrolling the request list restarts the delay** (a `scroll` listener on the list calls the same change path as a height change). Tall cards make the list scroll from 3 requests, and scrolling moves a different Allow under a still cursor.
3. **An arriving request never takes focus.** Otherwise terminal typing (a sudo password) lands in Reason and goes to the AI with the denial.
4. **Keyboard path from a terminal to the approvals:** `Ctrl+Shift+A` (`Cmd+Shift+A` on macOS), handled in `TermView`'s existing custom key handler, opens the column if collapsed and focuses the oldest request's Reason field; `Esc` in Reason returns focus to the active terminal. xterm otherwise keeps Tab, so the keyboard could not reach Deny from a terminal.

## Visual system

All colour comes from CSS custom properties; components never use raw colours.

| Token | Dark | Light | Use |
|---|---|---|---|
| `--bg` | `#14171A` | `#f5f6fa` | window, Hosts view, inputs |
| `--surface` | `#1A1E22` | `#ffffff` | cards, AI column, editor, dialogs, active tab |
| `--raised` | `#21262B` | `#eef0f6` | secondary buttons, request cards, hover |
| `--line` | `#3A4148` | `#dde0ea` | borders |
| `--divider` | `#2B3137` | `#e6e8ef` | structural dividers |
| `--text` | `#ECE8E0` | `#1b1f2e` | primary text, primary button fill (dark) |
| `--muted` | `#8B908E` | `#62698a` | meta, addresses, section labels |
| `--label` | `#B3B5B1` | `#474e6b` | field labels |
| `--accent` | `#56BFB9` | `#12766F` | focus ring, links, host tile letter, AI chip text |
| `--accent-tint` | `#16312F` | `#dcf1ec` | host tile, AI chip background |
| `--danger` | `#F28B73` | `#B5412F` | danger text and outlines, sudo edge, mismatch |
| `--danger-fill` | `#B5412F` | `#B5412F` | Deny fill (white label 5.58:1) |
| `--danger-tint` | `#3D1D18` | `#fbe2dc` | store-error banner, mismatch banner, SUDO chip |
| `--wait` | `#E9A23B` | `#8B6123` | waiting requests, AI button when collapsed, NEW KEY chip |
| `--wait-tint` | `#3A2C14` | `#fcefdc` | amber boxes |
| `--ok` | `#6CC58F` | `#3F7A57` | connected dot, Allow outline and label |
| `--term-bg` | `#0E1012` | `#ffffff` | xterm background, command blocks, fingerprints |
| `--term-fg` | `#D8D4CC` | `#1b1f2e` | xterm foreground |

Every text/background pair above passes 4.5:1 and every UI pair 3:1 (checked by the token review). State colours are law: red = deny or danger, amber = waiting on the user, green = connected or allow, teal = focus and links only (never "safe"). Nothing decorative uses them.

Type: `system-ui, -apple-system, "Segoe UI", sans-serif`; sizes 12 (meta), 13 (body), 14 (buttons, inputs), 15 (card titles), 18 (dialog titles), 22 (lock-card title); weights 400/500/600. Mono stack as above. Radii 6 px controls, 8 px cards and inputs, 10 px dialogs and panels. Spacing on a 4 px scale (the design's 5/7/3 px values round to it). Focus: 2 px `--accent` outline, 2 px offset. Motion: column and editor slide 160 ms ease-out; none under `prefers-reduced-motion`. Icons: `icons.tsx`, inline SVG, 16 px, `currentColor`, `aria-hidden` (home, plus, edit, trash, lock, close, sun, moon, terminal, warning, check, shield).

## Themes

- `theme.ts`: `type ThemePref = 'dark' | 'light' | 'auto'`; `resolveTheme(pref, prefersDark)`; `loadPref()`/`savePref()` wrap `localStorage` key `ssh-mcp.theme` in try/catch, falling back to `auto`.
- `App` holds the pref, listens to `matchMedia('(prefers-color-scheme: dark)')`, and sets `document.documentElement.dataset.theme`. `index.html` loads a tiny `theme-boot.js` (from `'self'`: the CSP is `script-src 'self'`, so no inline script) that sets it before React renders, so there is no flash.
- xterm: `TermView` sets `term.options.theme` from the computed `--term-bg`, `--term-fg`, `--accent` (cursor) and a light ANSI set in light mode, and re-applies on theme change.
- Display preference only, so `localStorage` is right (PRODUCT.md).

## What must not change

- Hub, protocol, IPC whitelist, main process, CSP.
- `App.tsx` data flow and the no-polling rule.
- Terminals mounted through lock and hub restart; `inert` when hidden.
- Accessible names and selectors the e2e tests use: `Master password`, `Unlock`, `New host`, `Edit <name>`, `Delete <name>`, host open button named by the host, `Lock`, `Allow`, `Deny` (exact; the `↵` is `aria-hidden`), `Deny all`, `Send to tab`, placeholder `Reason (optional)`, `Nothing waiting.`, `Locked after inactivity.`, `Unknown host key`, `Host editor`, `Save`, `Close` (editor footer only), `Cancel`, `Trust` (substring of `Trust and connect`), `Forget host key`, `Create vault`, `New master password`, `Confirm master password`, `SHA256:` in the editor; classes `nav.hosts`, `.tabbar .tab` (name button first), `.approvals`, `.approval`, `button.allow`, `button.denyall`, `.xterm`.

## Testing

- Vitest: `resolveTheme` and storage fallback; host filter; `TabSet.showHome()`; non-ASCII code-point summary (cap at 5); the password checklist split.
- E2e, existing specs keep passing; changes:
  - `smoke.spec.ts`: the column opens itself on the request.
  - `keyboard.spec.ts`: column stops are Reason, Deny, the column's `×`; never Allow or Send to tab. New case with a terminal open: `Ctrl+Shift+A` focuses Reason, `Esc` returns to the terminal, and typing in the terminal while a request arrives never lands in Reason.
  - New `ui.spec.ts`: collapsing with a request pending keeps it pending and turns the AI button amber; reopening shows Allow and Send to tab disabled for 500 ms; scrolling a 3-request list re-disables Allow; the Hosts tab shows after closing the last terminal; search filters cards; the theme control flips `data-theme` and writes `ssh-mcp.theme`.
- Screenshots of Hosts, terminal with the column open, editor, trust dialog and unlock card, dark and light, written to `desktop/test-results/` for the visual check (not asserted).

## Out of scope

Sidebar, app bar, status bar, expiry countdown and idle-duration text (need protocol fields), Keychain, snippets, forwarding, SFTP, split panes, host groups or tags, per-host colours, tab drag, settings screen, bundled fonts, logo.

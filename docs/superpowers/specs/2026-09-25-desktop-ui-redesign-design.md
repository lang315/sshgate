# Desktop UI redesign (Termius-style shell, dark/light/Auto)

Date: 2026-09-25. Status: approved design, not yet planned.
Product context: `PRODUCT.md` (Users, Capabilities and Constraints, Brand Commitments).

## Goal

Replace the desktop app's placeholder look (52 lines of VS Code-grey CSS, three fixed columns) with a Termius-like SSH client shell: one tab strip with a fixed Hosts home tab, a host card grid, a slide-in host editor, an overlay AI approval drawer, and dark, light and Auto themes. The terminal gets the full window width whenever nothing needs the user.

Nothing about what the app can do changes. No hub, protocol, IPC or main-process change. Every approval and host-key safety rule survives unchanged, and one is added (drawer mount restarts the Allow delay, which the existing mount rule already gives us).

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Scope | Termius layout, only existing features | The user chose it. Keychain, snippets, forwarding, SFTP belong to gated roadmap slices; PRODUCT.md forbids designing them as present. |
| Navigation | No sidebar; one tab strip, Hosts is the first tab (layout B) | The only nav item would be Hosts. A sidebar is added when a second section exists. |
| AI approvals | Right drawer, overlays content, auto-opens on a new request | Terminal keeps the width when idle; a new request is still impossible to miss. |
| Visual world | Category standard, Termius-like (canon), at Termius craft level | The user asked for it; recorded in PRODUCT.md Brand Commitments. |
| Themes | Dark, light, Auto (OS); a ☾ / ☀ / Auto control; default Auto | The user asked for both themes and the control. |
| Dependencies | None added. System fonts, inline SVG icons | Nothing here needs a package. |

## Structure

`App.tsx` keeps its data flow (status refresh on events only, no polling, one `servers` fetch per screen change or edit, approval reducer). Only the rendered tree changes.

### Ready screen

```
┌ tab strip ─────────────────────────────────────────────────────────────┐
│ [⌂ Hosts] [● prod-db ×] [● box ×]              [AI 2] [☾ ☀ Auto] [🔒] │
├────────────────────────────────────────────────────────────────────────┤
│ store-error banner (only when status.storeError)                       │
├────────────────────────────────────────────────────────────────────────┤
│ Hosts view  OR  the active terminal                    ┊ AI drawer     │
│                                                        ┊ (overlay,     │
│                                                        ┊  360 px)      │
└────────────────────────────────────────────────────────────────────────┘
```

- **Tab strip** (`TerminalTabs.tsx` owns it; the home tab is rendered by it too):
  - `⌂ Hosts` home tab, class `hometab` (not `tab`, so `.tabbar .tab` still means terminal tabs), active when `TabSet.active` is undefined. Clicking it calls a new `TabSet.showHome()` (sets `active` to undefined). Closing the last terminal tab already leaves `active` undefined, which now shows Hosts.
  - Terminal tabs as today (`.tab`, name button, Reconnect when exited, `×` with `aria-label="Close"`), plus a 7 px status dot: connected green, exited grey. Active tab: surface colour and a 2 px accent top edge.
  - Right cluster, rendered by `App` into the strip through a prop (a `ReactNode` slot): AI badge button, theme control, Lock button.
- **AI badge** (`aria-label="AI requests"`, shows the count): hidden at 0. Clicking toggles the drawer. When the drawer is collapsed with requests waiting, the badge pulses amber (CSS animation, disabled under `prefers-reduced-motion`).
- **Theme control**: a three-button segmented control, `role="radiogroup"` `aria-label="Theme"`, buttons `☾` Dark, `☀` Light, `Auto`, each with an `aria-label`.
- **Lock**: icon button, `aria-label="Lock"`.

### Hosts view (`HostList.tsx`, root stays `nav.hosts`)

- Header row: search input (`placeholder="Search hosts"`, `aria-label="Search hosts"`) filtering by name, host and user, case-insensitive substring, in the renderer only (no hub call, so it cannot touch the idle lock); `+ New host` primary button (accessible name stays `New host`).
- Section label `Hosts · n` (n = matches).
- Card grid: `repeat(auto-fill, minmax(240px, 1fr))`, 10 px gap. Each card:
  - a 36 px rounded tile with the name's first letter on the accent tint (one colour for all hosts: per-host colours would compete with state colours);
  - name, and `user@host:port` in muted text;
  - chips: `AI` when `aiVisible`, amber `new` when no host key is pinned (same meaning as today);
  - the whole card is the open button (accessible name = host name, as `e2e` expects `button {name: 'box'}`); Edit (`aria-label="Edit <name>"`) and Delete (`aria-label="Delete <name>"`) icon buttons appear on hover and on keyboard focus within the card, and are always in the tab order.
- Empty states: no hosts → "No hosts yet" with the New host button; no matches → "No hosts match "<query>"".
- Footer: `Vault file: <storePath> — copy it to back up.` (unchanged copy).

### Terminal area

Unchanged behaviour: tabs stay mounted (hidden, `inert`) through lock and hub restarts; Hosts view and terminals are siblings in the work area, only one visible. xterm font: system mono stack `ui-monospace, "SF Mono", Menlo, Consolas, "DejaVu Sans Mono", monospace`, 13 px.

### AI drawer (`ApprovalPanel.tsx`)

- Fixed overlay under the tab strip, right edge, 360 px wide, full height, shadow on its left edge. It does not resize or move the terminal.
- **Open state lives in `App`** (`drawerOpen`). It opens when a `pending` event brings a request id not seen before, and when the user clicks the AI badge. It closes only from its ✕ (`aria-label="Close AI requests"`) or the badge. When requests remain, closing collapses it (badge pulses); nothing is denied or dismissed by closing.
- **`ApprovalPanel` is mounted only while the drawer is open.** Its existing rule "Allow is disabled for 500 ms after the panel mounts" therefore covers the new risk: opening the drawer puts Allow under the cursor, and it cannot be clicked for 500 ms. All other rules stay as implemented: Allow and Send to tab `tabIndex={-1}` with `blockKeyboardActivation`, the 500 ms delay after any list id or height change, Deny as the submit default, `Deny all` always rendered (disabled below 2).
- Visual: each request is a card with a 3 px left edge, amber normally, red for `sudo` (with the existing `SUDO` badge). Deny is a filled red button; Allow is an outlined green button; Send to tab is a neutral outline. The command block is monospace on the terminal background; non-ASCII marks keep their highlight.
- Header: `AI requests (n)`, `Deny all`, ✕. Empty state text stays `Nothing waiting.`

### Host editor (`HostEditor.tsx`)

Right slide-in panel, 400 px, over a dimmed backdrop, `role="dialog"` `aria-label="Host editor"` (unchanged name). Fields, write-only secrets, Clear, Forget host key, the host/port-change warning and the tab-count warning are unchanged. Sections: Connection (name, host + port on one row, user), Authentication, AI (Visible to AI switch), Host key (fingerprint in mono, Forget). Footer: Cancel, Save (primary). Esc closes unless a save is in flight.

### Host-key dialogs (`HostKeyDialog.tsx`)

Centered modal cards (`.modal` / `.dialog`), 480 px, same accessible names (`Unknown host key`). Fingerprints in mono on the terminal background. Trust stays mouse-only and disabled for 500 ms after content changes; Cancel stays the default. Mismatch dialog gets a red top edge.

### Unlock, Create vault, hub screens

Full-window page background with one centered 320 px card: a small accent mark (the letters `ssh` in mono on an accent tile, no invented logo), the title, `Locked after inactivity.` when relevant, the password field(s) (labels unchanged: `Master password`, `New master password`, `Confirm master password`), primary button. Hub starting/restarting/failed use the same card; `stderr` in a mono scroll box.

### Store-error banner

Full-width strip under the tab strip, red tint background, red text, unchanged message.

## Visual system

All colour comes from CSS custom properties on `:root`. Components never use raw colours.

| Token | Dark | Light | Use |
|---|---|---|---|
| `--bg` | `#141724` | `#f5f6fa` | window background, Hosts view |
| `--surface` | `#1c2030` | `#ffffff` | cards, drawer, editor, active tab |
| `--raised` | `#252a3d` | `#eef0f6` | inputs, hover |
| `--line` | `#2c3248` | `#dde0ea` | borders, dividers |
| `--text` | `#e3e6f3` | `#1b1f2e` | primary text |
| `--muted` | `#8a90ad` | `#62698a` | secondary text |
| `--accent` | `#5b7cfa` | `#3f5ee8` | primary buttons, active tab edge, focus ring, host tile tint |
| `--danger` | `#e5534b` | `#d23c35` | Deny, sudo edge, errors, mismatch |
| `--wait` | `#f0a13a` | `#c77d0a` | pending requests, AI badge, `new` chip |
| `--ok` | `#35c28a` | `#1f9d6b` | connected dot, Allow outline |
| `--term-bg` | `#0f1220` | `#ffffff` | xterm background, command blocks |
| `--term-fg` | `#d6daea` | `#1b1f2e` | xterm foreground |

State colours are law: each means one thing on every screen. Red = deny or danger, amber = waiting on the user, green = connected or allow. They never decorate.

Type: `system-ui, -apple-system, "Segoe UI", sans-serif` for UI at 13 px base (12 px muted/meta, 15 px dialog titles, 18 px lock-card title); the mono stack above for commands, fingerprints and xterm. Radii: 6 px controls, 8 px cards, 10 px dialogs. Spacing on a 4 px scale. Focus: 2 px `--accent` outline, offset 2 px, on every focusable element. Motion: drawer and editor slide 160 ms ease-out, disabled under `prefers-reduced-motion`.

Icons: a small `icons.tsx` of inline SVG components (home, plus, edit, trash, lock, close, sun, moon), 16 px, `currentColor`, `aria-hidden`.

## Themes

- `theme.ts`: `type ThemePref = 'dark' | 'light' | 'auto'`; `resolveTheme(pref, prefersDark): 'dark' | 'light'`; `loadPref()` and `savePref()` wrap `localStorage` (key `ssh-mcp.theme`) in try/catch and fall back to `auto`.
- `App` holds the pref, listens to `matchMedia('(prefers-color-scheme: dark)')` changes, and sets `document.documentElement.dataset.theme` to the resolved value. CSS defines the dark tokens on `:root[data-theme="dark"]` and light on `:root[data-theme="light"]`; before React runs, `index.html` sets `data-theme` from the same logic inline so there is no flash.
- xterm: `TermView` reads `--term-bg`, `--term-fg` and `--accent` (cursor) from computed style and sets `term.options.theme`; it re-applies when the resolved theme changes (a `themechange` passed down as a prop value). ANSI colours use xterm's defaults in dark and a darker set in light so yellow and white stay readable on white.
- The theme is a display preference, never security state, so `localStorage` is correct here (PRODUCT.md).

## What must not change

- Hub, protocol, IPC whitelist, main process, CSP.
- `App.tsx` data flow: no polling, `status` refresh on `locked`/`pending` events, one `servers` fetch per screen change or edit.
- Terminals mounted through lock and hub restart; `inert` when hidden.
- Approval rules (above) and host-key prompt rules.
- Accessible names the e2e tests rely on: `Master password`, `Unlock`, `New host`, `Edit <name>`, `Delete <name>`, `box` (host open button), `Lock`, `Allow`, `Deny`, `Deny all`, `Send to tab`, `Reason (optional)`, `Nothing waiting.`, `Locked after inactivity.`, `Unknown host key`, `Host editor`, `Save`, `Cancel`, `Close`, `Trust`, `Forget host key`, `Create vault`, `New master password`, `Confirm master password`, plus classes `nav.hosts`, `.tabbar .tab`, `.approvals`, `.approval`, `button.allow`, `button.denyall`, `.xterm`.

## Testing

- Vitest: `theme.ts` (`resolveTheme` for all three prefs, storage failure falls back to `auto`); the host filter function (name/host/user match, case-insensitive, empty query returns all); `TabSet.showHome()`.
- E2e (existing specs keep passing; update only where the drawer changed behaviour):
  - `smoke.spec.ts`: the drawer is open when the request arrives (auto-open), the Allow flow is unchanged.
  - `keyboard.spec.ts`: the drawer's tab stops are now the reason field, Deny and the drawer's ✕; still never Allow or Send to tab.
  - New `ui.spec.ts`: closing the drawer with a request pending keeps the request pending and shows the badge; re-opening via the badge shows Allow disabled for 500 ms; the Hosts tab shows after closing the last terminal; the search filters cards; switching the theme control flips `data-theme` on the document and stores the choice under `ssh-mcp.theme`.
- Screenshots: the Hosts view, a terminal with the drawer open, the editor, and the unlock card, in dark and light, captured by the e2e run into `desktop/test-results/` for the visual check (not asserted).

## Out of scope

Sidebar, Keychain, snippets, port forwarding, SFTP, split panes, host groups or tags, per-host colours or OS icons, drag-to-reorder tabs, a settings screen, any logo.

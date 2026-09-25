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

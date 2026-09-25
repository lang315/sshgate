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

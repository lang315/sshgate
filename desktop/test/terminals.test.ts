import { describe, expect, it, vi } from 'vitest'
import { clipboardKey, Debouncer, Dispatcher, newTermId, printable, TabSet, isUserInput } from '../src/renderer/terminals'

describe('newTermId', () => {
  it('matches the hub id rules and is unique', () => {
    const a = newTermId(), b = newTermId()
    expect(a).toMatch(/^[A-Za-z0-9_-]{1,64}$/)
    expect(a).not.toBe(b)
  })
})

describe('TabSet', () => {
  it('counts the tabs still open per server', () => {
    const t = new TabSet()
    const a = t.open('box')
    t.open('box')
    t.open('other')
    t.exited(a.id, 'bye')
    expect(t.openCount('box')).toBe(1)
    expect(t.openCount('none')).toBe(0)
  })
  it('tracks lifecycle and activation order', () => {
    const t = new TabSet()
    const a = t.open('box')
    const b = t.open('box')
    const c = t.open('other')
    expect(t.active).toBe(c.id)
    t.activate(a.id)
    expect(t.mostRecentFor('box')?.id).toBe(a.id)
    t.opened(a.id); t.output(a.id)
    expect(t.tabs.find((x) => x.id === a.id)).toMatchObject({ state: 'open', sawOutput: true })
    t.exited(a.id, 'bye')
    expect(t.mostRecentFor('box')?.id).toBe(b.id)
    t.activate(c.id)
    t.close(c.id)
    expect(t.tabs.map((x) => x.id)).toEqual([a.id, b.id])
    expect(t.active).toBe(a.id) // previous in activation order
  })
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
})

describe('Debouncer', () => {
  it('fires once after quiet time', async () => {
    vi.useFakeTimers()
    const fn = vi.fn()
    const d = new Debouncer(50, fn)
    d.poke(); vi.advanceTimersByTime(30); d.poke(); vi.advanceTimersByTime(30)
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(30)
    expect(fn).toHaveBeenCalledTimes(1)
    vi.useRealTimers()
  })
})

describe('printable', () => {
  it('strips C0, DEL and C1 control characters', () => {
    expect(printable('bye\x1b]0;pwn\x07\r\n\x7f\x9b2Jok é')).toBe('bye]0;pwn2Jok é')
  })
})

describe('Dispatcher', () => {
  it('routes by id, ignores unknown ids, and unsubscribes', () => {
    const d = new Dispatcher<string>()
    const a = vi.fn(), b = vi.fn()
    const offA = d.on('a', a); d.on('b', b)
    d.emit('a', 'x'); d.emit('zzz', 'y')
    expect(a).toHaveBeenCalledWith('x'); expect(b).not.toHaveBeenCalled()
    d.emitAll('all')
    expect(b).toHaveBeenCalledWith('all')
    offA(); d.emit('a', 'z')
    expect(a).toHaveBeenCalledTimes(2)
  })
})

describe('isUserInput', () => {
  it('is true only within 1000 ms of real user input', () => {
    expect(isUserInput(10_000, 10_000)).toBe(true)
    expect(isUserInput(10_000, 10_999)).toBe(true)
    expect(isUserInput(10_000, 11_000)).toBe(false)
    expect(isUserInput(-Infinity, 5)).toBe(false) // no input yet
  })
})

describe('clipboardKey', () => {
  const k = (keyCode: number, mods: Partial<Record<'ctrlKey' | 'shiftKey' | 'altKey' | 'metaKey', boolean>> = {}) =>
    ({ keyCode, ctrlKey: false, shiftKey: false, altKey: false, metaKey: false, ...mods })
  const C = 67, V = 86, D = 68, W = 87
  it('copies and pastes on Ctrl+Shift+C/V off macOS', () => {
    for (const p of ['Win32', 'Linux x86_64']) {
      expect(clipboardKey(k(C, { ctrlKey: true, shiftKey: true }), p)).toBe('copy')
      expect(clipboardKey(k(V, { ctrlKey: true, shiftKey: true }), p)).toBe('paste')
    }
  })
  it('passes Ctrl without Shift, other keys, and extra modifiers', () => {
    for (const code of [C, V, D, W]) expect(clipboardKey(k(code, { ctrlKey: true }), 'Linux x86_64')).toBe('pass')
    expect(clipboardKey(k(D, { ctrlKey: true, shiftKey: true }), 'Win32')).toBe('pass')
    expect(clipboardKey(k(C, { shiftKey: true }), 'Win32')).toBe('pass')
    expect(clipboardKey(k(C, { ctrlKey: true, shiftKey: true, altKey: true }), 'Win32')).toBe('pass')
    expect(clipboardKey(k(V, { ctrlKey: true, shiftKey: true, metaKey: true }), 'Win32')).toBe('pass')
  })
  it('always passes on macOS', () => {
    for (const code of [C, V]) expect(clipboardKey(k(code, { ctrlKey: true, shiftKey: true }), 'MacIntel')).toBe('pass')
  })
})

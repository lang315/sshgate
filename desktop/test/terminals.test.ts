import { describe, expect, it, vi } from 'vitest'
import { Debouncer, Dispatcher, newTermId, printable, TabSet } from '../src/renderer/terminals'

describe('newTermId', () => {
  it('matches the hub id rules and is unique', () => {
    const a = newTermId(), b = newTermId()
    expect(a).toMatch(/^[A-Za-z0-9_-]{1,64}$/)
    expect(a).not.toBe(b)
  })
})

describe('TabSet', () => {
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

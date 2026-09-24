import { describe, expect, it, vi } from 'vitest'
import { allowEnabled, blockKeyboardActivation, highlightNonAscii, mergeSeed, reduceApprovals, seed } from '../src/renderer/approvals'
import type { ApprovalRequest } from '../src/shared/protocol'

const req = (id: string): ApprovalRequest => ({
  id, client: 'claude-code', server: 'box', command: 'ls', description: '', sudo: false, timeoutSec: 60, receivedAt: '',
})

describe('reduceApprovals', () => {
  it('adds pending once and removes on any decided outcome', () => {
    let s = seed([], 0)
    s = reduceApprovals(s, { method: 'pending', params: { request: req('a') } }, 10)
    s = reduceApprovals(s, { method: 'pending', params: { request: req('a') } }, 20)
    s = reduceApprovals(s, { method: 'pending', params: { request: req('b') } }, 30)
    expect(s.map((x) => x.request.id)).toEqual(['a', 'b'])
    for (const outcome of ['expired', 'withdrawn'] as const) {
      s = reduceApprovals(s, { method: 'decided', params: { request: req(s[0].request.id), decision: { outcome, reason: '' } } }, 40)
    }
    expect(s).toEqual([])
  })
})

describe('allowEnabled', () => {
  it('is false for the first 500 ms', () => {
    const [item] = seed([req('a')], 1000)
    expect(allowEnabled(item, 1499)).toBe(false)
    expect(allowEnabled(item, 1500)).toBe(true)
  })
  it('restarts the delay whenever the list changes or the panel mounts', () => {
    const [item] = seed([req('a')], 1000)
    expect(allowEnabled(item, 5000, 4800)).toBe(false)
    expect(allowEnabled(item, 5300, 4800)).toBe(true)
    expect(allowEnabled(item, 1400, 900)).toBe(false) // older change: shownAt still wins
  })
})

describe('highlightNonAscii', () => {
  it('marks non-ASCII runs, including homoglyphs', () => {
    expect(highlightNonAscii('rm -rf /tmp/а')).toEqual([
      { text: 'rm -rf /tmp/', nonAscii: false },
      { text: 'а', nonAscii: true },
    ])
    expect(highlightNonAscii('ls')).toEqual([{ text: 'ls', nonAscii: false }])
    expect(highlightNonAscii('')).toEqual([])
  })
})

describe('mergeSeed', () => {
  it('drops an id decided while the snapshot request was in flight (no ghost)', () => {
    // reduceApprovals already removed 'a' from `current` when the decided event arrived
    const merged = mergeSeed([], [req('a')], new Set(['a']), 100)
    expect(merged).toEqual([])
  })

  it('keeps an id that became pending after the snapshot was taken (not hidden)', () => {
    // reduceApprovals already appended 'x' to `current` from a live 'pending' event
    const current = seed([req('x')], 50)
    const merged = mergeSeed(current, [], new Set(), 999)
    expect(merged).toEqual(current)
  })

  it('unions snapshot and current, keeping the existing shownAt for ids already known', () => {
    const current = seed([req('a')], 10)
    const merged = mergeSeed(current, [req('a'), req('b')], new Set(), 999)
    expect(merged).toEqual([
      { request: req('a'), shownAt: 10 },
      { request: req('b'), shownAt: 999 },
    ])
  })
})

describe('blockKeyboardActivation', () => {
  it('preventDefaults Enter and Space, not other keys', () => {
    for (const key of ['Enter', ' ']) {
      const preventDefault = vi.fn()
      blockKeyboardActivation({ key, preventDefault })
      expect(preventDefault).toHaveBeenCalled()
    }
    const preventDefault = vi.fn()
    blockKeyboardActivation({ key: 'a', preventDefault })
    expect(preventDefault).not.toHaveBeenCalled()
  })
})

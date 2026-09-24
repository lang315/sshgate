import { describe, expect, it } from 'vitest'
import { allowEnabled, highlightNonAscii, reduceApprovals, seed } from '../src/renderer/approvals'
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

import { describe, expect, it } from 'vitest'
import { screenFor } from '../src/renderer/shell'

describe('screenFor', () => {
  it('shows the hub screen until the hub runs', () => {
    expect(screenFor({ kind: 'starting' })).toEqual({ kind: 'hub', state: { kind: 'starting' } })
    const failed = { kind: 'failed' as const, message: 'x', stderr: 'y' }
    expect(screenFor(failed, { locked: false, hasStore: true })).toEqual({ kind: 'hub', state: failed })
  })
  it('waits for status after the hub runs', () => {
    expect(screenFor({ kind: 'running' })).toEqual({ kind: 'hub', state: { kind: 'starting' } })
  })
  it('distinguishes no-store, locked, ready', () => {
    expect(screenFor({ kind: 'running' }, { locked: false, hasStore: false })).toEqual({ kind: 'no-store' })
    expect(screenFor({ kind: 'running' }, { locked: true, hasStore: true }, 'wrong master password'))
      .toEqual({ kind: 'locked', error: 'wrong master password' })
    expect(screenFor({ kind: 'running' }, { locked: false, hasStore: true })).toEqual({ kind: 'ready' })
  })
})

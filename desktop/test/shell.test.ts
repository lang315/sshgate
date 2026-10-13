import { describe, expect, it } from 'vitest'
import { autoTouch, lockKind, lockTitle, screenFor } from '../src/renderer/shell'

describe('screenFor', () => {
  it('shows the hub screen until the hub runs', () => {
    expect(screenFor({ kind: 'starting' })).toEqual({ kind: 'hub', state: { kind: 'starting' } })
    const failed = { kind: 'failed' as const, message: 'x', stderr: 'y' }
    expect(screenFor(failed, { locked: false, hasVault: true })).toEqual({ kind: 'hub', state: failed })
  })
  it('waits for status after the hub runs', () => {
    expect(screenFor({ kind: 'running' })).toEqual({ kind: 'hub', state: { kind: 'starting' } })
  })
  it('asks to create a vault when the store has none, then locked, then ready', () => {
    expect(screenFor({ kind: 'running' }, { locked: false, hasVault: false })).toEqual({ kind: 'create-vault' })
    expect(screenFor({ kind: 'running' }, { locked: true, hasVault: true }, 'wrong master password'))
      .toEqual({ kind: 'locked', error: 'wrong master password' })
    expect(screenFor({ kind: 'running' }, { locked: false, hasVault: true })).toEqual({ kind: 'ready' })
  })
})

describe('lockKind', () => {
  it('reads idle as idle and manual or unknown as manual', () => {
    expect(lockKind('idle')).toBe('idle')
    expect(lockKind('manual')).toBe('manual')
    expect(lockKind(undefined)).toBe('manual')
  })
  it('keeps the stored reason when a soft lock hardens, and claims no inactivity with none', () => {
    for (const r of ['grantsEnded', 'softLockLimit']) {
      expect(lockKind(r, 'manual')).toBe('manual')
      expect(lockKind(r, 'idle')).toBe('idle')
      expect(lockKind(r)).toBe('manual') // the origin's note was dropped as stale: do not guess idle
    }
  })
})

describe('lockTitle', () => {
  it('says auto-allow keeps running only while a host is on it', () => {
    expect(lockTitle(1)).toBe('Auto-allow keeps running while locked')
    expect(lockTitle(0)).toBeUndefined()
  })
})

describe('autoTouch', () => {
  const on = { supported: true, available: true, enabled: true }
  const base = { locked: true, touch: on, asked: false, everReady: false, focused: true }
  it('asks at app start with the window focused, and on a focus while locked', () => {
    expect(autoTouch({ ...base, trigger: 'start' })).toBe(true)
    expect(autoTouch({ ...base, trigger: 'focus', everReady: true })).toBe(true)
  })
  it('never at the moment of locking: no start trigger once the app has been unlocked', () => {
    expect(autoTouch({ ...base, trigger: 'start', everReady: true })).toBe(false)
  })
  it('not at start without focus, and once per episode', () => {
    expect(autoTouch({ ...base, trigger: 'start', focused: false })).toBe(false)
    expect(autoTouch({ ...base, trigger: 'focus', asked: true })).toBe(false)
  })
  it('only when locked, enabled and available', () => {
    expect(autoTouch({ ...base, trigger: 'focus', locked: false })).toBe(false)
    expect(autoTouch({ ...base, trigger: 'focus', touch: { ...on, enabled: false } })).toBe(false)
    expect(autoTouch({ ...base, trigger: 'focus', touch: { ...on, available: false } })).toBe(false)
    expect(autoTouch({ ...base, trigger: 'focus', touch: undefined })).toBe(false)
  })
})

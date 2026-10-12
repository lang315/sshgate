import { describe, expect, it } from 'vitest'
import { LIMITS, limitChange, limitHelp, limitNotice, saveAllowed, settingsChangeKey } from '../src/renderer/settings'
import { limitText } from '../src/renderer/autoallow'

describe('limitChange', () => {
  it('orders 24h < 3d < 7d < none', () => {
    expect(limitChange('24h', '24h')).toBe('same')
    expect(limitChange('24h', '3d')).toBe('higher')
    expect(limitChange('3d', '7d')).toBe('higher')
    expect(limitChange('7d', '3d')).toBe('lower')
    expect(limitChange('none', '24h')).toBe('lower')
    expect(limitChange('24h', 'none')).toBe('none')
    expect(limitChange('none', 'none')).toBe('same')
  })
})

describe('copy', () => {
  it('lists the four options in order with the spec labels', () => {
    expect(LIMITS.map((l) => [l.value, l.label])).toEqual([
      ['24h', '24 hours (default)'], ['3d', '3 days'], ['7d', '7 days'], ['none', 'No limit']])
  })
  it('help text depends on whether there is a limit', () => {
    for (const l of ['24h', '3d', '7d'] as const) expect(limitHelp(l)).toBe('Then the app locks fully and auto-allow stops. Forever grants wait for Resume.')
    expect(limitHelp('none')).toBe('Auto-allow stops only with Stop auto-allow and lock, a restart or a crash.')
  })
  it('the notice names the new limit and the same-user risk', () => {
    expect(limitNotice('7d')).toBe('While the app is locked, the AI keeps running on hosts with auto-allow for up to 7 days. Any program running as your user can use that access too.')
    expect(limitNotice('none')).toBe('While the app is locked, the AI keeps running on hosts with auto-allow for as long as the app runs. Any program running as your user can use that access too.')
  })
})

describe('saveAllowed', () => {
  const base = { change: 'higher' as const, busy: false, openedAt: 1000, changedAt: 0, now: 1000 }
  it('waits ALLOW_DELAY_MS after the dialog opens or the notice changes', () => {
    expect(saveAllowed({ ...base, now: 1499 })).toBe(false)
    expect(saveAllowed({ ...base, now: 1500 })).toBe(true)
    expect(saveAllowed({ ...base, changedAt: 2000, now: 2499 })).toBe(false)
    expect(saveAllowed({ ...base, changedAt: 2000, now: 2500 })).toBe(true)
  })
  it('is false with nothing to save, or while a save is in flight', () => {
    expect(saveAllowed({ ...base, change: 'same', now: 9999 })).toBe(false)
    expect(saveAllowed({ ...base, busy: true, now: 9999 })).toBe(false)
    expect(saveAllowed({ ...base, change: 'lower', now: 9999 })).toBe(true)
  })
})

describe('settingsChangeKey', () => {
  it('changes with the choice, the host list and an error, so Save waits again', () => {
    const k = settingsChangeKey('none', ['box'])
    expect(settingsChangeKey('7d', ['box'])).not.toBe(k)
    expect(settingsChangeKey('none', ['box', 'web'])).not.toBe(k)
    expect(settingsChangeKey('none', ['box'], 'Vault is locked')).not.toBe(k)
    expect(settingsChangeKey('none', ['box'])).toBe(k)
  })
})

describe('limitText', () => {
  it('keeps today\'s 24-hour copy for 24h and an unknown limit', () => {
    const t = limitText('24h')
    expect(t).toContain('the AI keeps running on this host for up to 24 hours.')
    expect(t).toContain('After a restart, the 24-hour limit, or Stop auto-allow and lock, it waits for you to click Resume.')
    expect(limitText(undefined)).toBe(t)
  })
  it('names 3 and 7 days', () => {
    expect(limitText('3d')).toContain('for up to 3 days.')
    expect(limitText('3d')).toContain('the 3-day limit')
    expect(limitText('7d')).toContain('the 7-day limit')
  })
  it('drops the limit under none', () => {
    const t = limitText('none')
    expect(t).toContain('the AI keeps running on this host while the app is locked, with no time limit.')
    expect(t).toContain('After a restart or Stop auto-allow and lock, it waits for you to click Resume.')
    expect(t).not.toMatch(/24|hour|day/)
  })
})

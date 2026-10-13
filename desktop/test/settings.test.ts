import { describe, expect, it } from 'vitest'
import { LIMITS, limitChange, limitHelp, limitNotice, limitText, saveAllowed, saveSettings, settingsChangeKey, touchChange } from '../src/renderer/settings'

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

describe('Touch ID in Settings', () => {
  const base = { change: 'same' as const, busy: false, openedAt: 1000, changedAt: 1000, now: 9999 }
  it('touchChange', () => {
    expect(touchChange(false, false)).toBe('same')
    expect(touchChange(false, true)).toBe('on')
    expect(touchChange(true, false)).toBe('off')
  })
  it('a Touch ID change alone enables Save; turning on needs a password', () => {
    expect(saveAllowed({ ...base })).toBe(false)
    expect(saveAllowed({ ...base, touch: 'off' })).toBe(true)
    expect(saveAllowed({ ...base, touch: 'on', password: '' })).toBe(false)
    expect(saveAllowed({ ...base, touch: 'on', password: 'pw' })).toBe(true)
    expect(saveAllowed({ ...base, touch: 'on', password: 'pw', busy: true })).toBe(false)
  })
  it('turning Touch ID on moves Save, so it restarts the delay', () => {
    expect(settingsChangeKey('24h', [], undefined, true)).not.toBe(settingsChangeKey('24h', [], undefined, false))
  })
  it('saveSettings does Touch ID first and leaves the limit unsaved when it fails', async () => {
    const calls: string[] = []
    const api = {
      enable: async (pw: string) => { calls.push(`enable ${pw}`); if (pw === 'bad') throw new Error('wrong master password') },
      disable: async () => { calls.push('disable') },
      setLimit: async (l: string) => { calls.push(`limit ${l}`) },
    }
    await expect(saveSettings({ change: 'higher', limit: '7d', touch: 'on', password: 'bad' }, api)).rejects.toThrow('wrong master password')
    expect(calls).toEqual(['enable bad'])
    calls.length = 0
    await saveSettings({ change: 'higher', limit: '7d', touch: 'on', password: 'pw' }, api)
    expect(calls).toEqual(['enable pw', 'limit 7d'])
    calls.length = 0
    await saveSettings({ change: 'same', limit: '24h', touch: 'off', password: '' }, api)
    expect(calls).toEqual(['disable'])
  })
})

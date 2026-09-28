import { describe, expect, it, vi } from 'vitest'
import type { AutoAllowRan, HubEvent, ServerInfo } from '../src/shared/protocol'
import { applyOff, autoHosts, chipLabel, commandLabel, dropOnLock, enableAllowed, FEED_CAP, handleAutoEvent, isActive, pausedHosts, pushFeed } from '../src/renderer/autoallow'

// The renderer must never call the hub in response to autoAllow.ran or
// autoAllow.off (the idle-lock rule: every call but status counts as UI
// activity). Mocking transport so every hub method throws makes App's event
// dispatch for these two methods fail loudly if it ever reaches into hub
// instead of routing through handleAutoEvent.
vi.mock('../src/renderer/transport', () => ({
  hub: new Proxy({}, { get: () => { throw new Error('handleAutoEvent must never call the hub') } }),
}))

describe('handleAutoEvent', () => {
  it('applies autoAllow.off via applyOff and touches only setServers', () => {
    const servers: ServerInfo[] = [{ name: 'a', autoAllow: { forever: true } } as ServerInfo]
    let updated: ServerInfo[] | undefined
    const setServers = vi.fn((fn: (cur: ServerInfo[]) => ServerInfo[]) => { updated = fn(servers) })
    const setAutoFeed = vi.fn()
    const e: HubEvent = { method: 'autoAllow.off', params: { server: 'a', reason: 'turned off' } }
    expect(() => handleAutoEvent(e, setServers, setAutoFeed)).not.toThrow()
    expect(setServers).toHaveBeenCalledTimes(1)
    expect(setAutoFeed).not.toHaveBeenCalled()
    expect(updated).toEqual(applyOff(servers, 'a'))
  })

  it('pushes autoAllow.ran onto the feed via pushFeed and touches only setAutoFeed', () => {
    const feed: AutoAllowRan[] = []
    let updated: AutoAllowRan[] | undefined
    const setAutoFeed = vi.fn((fn: (f: AutoAllowRan[]) => AutoAllowRan[]) => { updated = fn(feed) })
    const setServers = vi.fn()
    const ran: AutoAllowRan = { server: 'a', command: 'ls', description: '', exitCode: 0, time: new Date(now).toISOString() }
    const e: HubEvent = { method: 'autoAllow.ran', params: ran }
    expect(() => handleAutoEvent(e, setServers, setAutoFeed)).not.toThrow()
    expect(setAutoFeed).toHaveBeenCalledTimes(1)
    expect(setServers).not.toHaveBeenCalled()
    expect(updated).toEqual(pushFeed(feed, ran))
  })

  it('ignores every other event', () => {
    const setServers = vi.fn()
    const setAutoFeed = vi.fn()
    handleAutoEvent({ method: 'pending', params: { request: {} } } as unknown as HubEvent, setServers, setAutoFeed)
    handleAutoEvent({ method: 'locked', params: { reason: 'idle' } } as HubEvent, setServers, setAutoFeed)
    expect(setServers).not.toHaveBeenCalled()
    expect(setAutoFeed).not.toHaveBeenCalled()
  })
})

const now = Date.parse('2026-09-28T10:00:00Z')
const at = (min: number) => new Date(now + min * 60_000).toISOString()
const srv = (name: string, autoAllow?: ServerInfo['autoAllow']) => ({ name, autoAllow }) as ServerInfo

describe('chipLabel', () => {
  it('counts down a timed grant in whole minutes, rounding up', () => {
    expect(chipLabel({ until: at(14.2) }, now)).toBe('Auto 15m')
    expect(chipLabel({ until: at(0.1) }, now)).toBe('Auto 1m')
    expect(chipLabel({ until: at(125) }, now)).toBe('Auto 2h 5m')
    expect(chipLabel({ until: at(120) }, now)).toBe('Auto 2h')
  })
  it('is gone once the deadline passes', () => expect(chipLabel({ until: at(-1) }, now)).toBeUndefined())
  it('shows forever and paused', () => {
    expect(chipLabel({ forever: true }, now)).toBe('Auto ∞')
    expect(chipLabel({ forever: true, paused: true }, now)).toBe('Auto paused')
    expect(chipLabel(undefined, now)).toBeUndefined()
  })
})

describe('host sets', () => {
  const list = [srv('a', { until: at(5) }), srv('b', { forever: true }), srv('c', { forever: true, paused: true }), srv('d', { until: at(-1) }), srv('e')]
  it('isActive: timed before its deadline or armed forever', () => {
    expect(list.map((s) => isActive(s.autoAllow, now))).toEqual([true, true, false, false, false])
  })
  it('autoHosts has active and paused hosts', () => expect([...autoHosts(list, now)].sort()).toEqual(['a', 'b', 'c']))
  it('pausedHosts', () => expect(pausedHosts(list)).toEqual(['c']))
  it('dropOnLock drops timed grants and pauses forever', () => {
    expect(dropOnLock(list).map((s) => s.autoAllow)).toEqual([undefined, { forever: true, paused: true }, { forever: true, paused: true }, undefined, undefined])
  })
  it('applyOff clears one host', () => expect(applyOff(list, 'b').find((s) => s.name === 'b')!.autoAllow).toBeUndefined())
})

describe('feed', () => {
  const ran = (i: number): AutoAllowRan => ({ server: 'a', command: `c${i}`, description: '', exitCode: 0, time: at(i) })
  it('is newest first and capped', () => {
    let feed: AutoAllowRan[] = []
    for (let i = 0; i < FEED_CAP + 5; i++) feed = pushFeed(feed, ran(i))
    expect(feed).toHaveLength(FEED_CAP)
    expect(feed[0].command).toBe(`c${FEED_CAP + 4}`)
  })
  it('labels a cut command', () => {
    expect(commandLabel({ ...ran(1), truncated: 42 })).toBe('c1 … (truncated 42 bytes)')
    expect(commandLabel(ran(1))).toBe('c1')
  })
})

describe('enableAllowed', () => {
  const base = { mode: '15m' as const, typed: '', host: 'box', refused: undefined, openedAt: now, changedAt: now, now: now + 600 }
  it('waits 500 ms after opening or any change', () => {
    expect(enableAllowed(base)).toBe(true)
    expect(enableAllowed({ ...base, now: now + 499 })).toBe(false)
    expect(enableAllowed({ ...base, changedAt: now + 200 })).toBe(false)
  })
  it('forever needs the host name typed exactly', () => {
    expect(enableAllowed({ ...base, mode: 'forever' })).toBe(false)
    expect(enableAllowed({ ...base, mode: 'forever', typed: 'box' })).toBe(true)
    expect(enableAllowed({ ...base, mode: 'forever', typed: 'Box' })).toBe(false)
  })
  it('never for a refused host', () => expect(enableAllowed({ ...base, refused: 'root login' })).toBe(false))
})

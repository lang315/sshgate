import { describe, expect, it } from 'vitest'
import * as path from 'node:path'
import { HubProcess } from '../src/main/hubProcess'
import type { HubState } from '../src/shared/protocol'

const fixture = path.join(__dirname, 'fixtures', 'fakeHub.mjs')

function hub(mode: string, extra: Partial<ConstructorParameters<typeof HubProcess>[0]> = {}) {
  return new HubProcess({
    command: process.execPath, args: [fixture],
    env: { ...process.env, FAKE_HUB_MODE: mode },
    backoffMs: [20, 40, 80], crashWindowMs: 5000, maxCrashes: 3, ...extra,
  })
}

function waitState(h: HubProcess, kind: HubState['kind'], ms = 3000): Promise<HubState> {
  return new Promise((resolve, reject) => {
    if (h.state.kind === kind) return resolve(h.state)
    const t = setTimeout(() => reject(new Error(`timeout waiting for ${kind}, at ${h.state.kind}`)), ms)
    h.on('state', (s: HubState) => { if (s.kind === kind) { clearTimeout(t); resolve(s) } })
  })
}

describe('HubProcess', () => {
  it('handshakes, calls, and relays notifications', async () => {
    const h = hub('ok')
    h.start()
    await waitState(h, 'running')
    expect(await h.call('status')).toEqual({ locked: true, hasStore: true, pending: 0 })
    await expect(h.call('fail')).rejects.toThrow('nope')
    const note = new Promise<[string, unknown]>((r) => h.once('notification', (m, p) => r([m, p])))
    h.notify('term.write', { id: 't1', data: 'aGk=' })
    expect(await note).toEqual(['term.data', { id: 't1', data: 'aGk=' }])
    await h.stop()
  })

  it('fails on a protocol mismatch', async () => {
    const h = hub('badproto')
    h.start()
    const s = await waitState(h, 'failed')
    expect(s.kind === 'failed' && s.message).toContain('protocol')
    await h.stop()
  })

  it('restarts with backoff, then gives up with stderr after three crashes', async () => {
    const h = hub('crash')
    const seen: string[] = []
    h.on('state', (s: HubState) => seen.push(s.kind))
    h.start()
    const s = await waitState(h, 'failed')
    expect(seen.filter((k) => k === 'restarting').length).toBe(2)
    expect(s.kind === 'failed' && s.stderr).toContain('boom')
    await h.stop()
  })

  it('rejects in-flight calls when the hub exits', async () => {
    const h = hub('ok')
    h.start()
    await waitState(h, 'running')
    await expect(h.call('exit').then(() => h.call('status', undefined, 1000))).rejects.toThrow()
    await h.stop()
  })

  it('stop() does not trigger a restart', async () => {
    const h = hub('ok')
    h.start()
    await waitState(h, 'running')
    const seen: string[] = []
    h.on('state', (s: HubState) => seen.push(s.kind))
    await h.stop()
    await new Promise((r) => setTimeout(r, 200))
    expect(seen).not.toContain('restarting')
  })
})

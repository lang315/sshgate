import { describe, expect, it, vi } from 'vitest'
import * as path from 'node:path'
import { HubProcess } from '../src/main/hubProcess'
import type { HubState } from '../src/shared/protocol'

const fixture = path.join(__dirname, 'fixtures', 'fakeHub.mjs')

function hub(mode: string, extra: Partial<ConstructorParameters<typeof HubProcess>[0]> = {}) {
  return new HubProcess({
    command: process.execPath, args: [fixture],
    env: { ...process.env, FAKE_HUB_MODE: mode },
    backoffMs: [20, 40, 80], crashWindowMs: 5000, maxCrashes: 4, ...extra,
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

  it('restarts through all three backoff steps, then gives up with stderr after a fourth crash', async () => {
    const h = hub('crash')
    const restarts: number[] = []
    h.on('state', (s: HubState) => { if (s.kind === 'restarting') restarts.push(s.inMs) })
    h.start()
    const s = await waitState(h, 'failed')
    expect(restarts).toEqual([20, 40, 80])
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

  it('a missing binary restarts through backoff, then fails with ENOENT in stderr', async () => {
    const h = new HubProcess({
      command: '/nonexistent/definitely-not-a-real-binary-xyz',
      args: [],
      backoffMs: [20, 40, 80],
      crashWindowMs: 5000,
    })
    const restarts: number[] = []
    h.on('state', (s: HubState) => { if (s.kind === 'restarting') restarts.push(s.inMs) })
    h.start()
    const s = await waitState(h, 'failed')
    expect(restarts).toEqual([20, 40, 80])
    expect(s.kind === 'failed' && s.stderr).toMatch(/ENOENT/)
    await h.stop()
  })

  it('keeps the hub\'s last stderr line even behind a large burst before it', async () => {
    const h = hub('crashBig')
    const restarts: number[] = []
    h.on('state', (s: HubState) => { if (s.kind === 'restarting') restarts.push(s.inMs) })
    h.start()
    const s = await waitState(h, 'failed')
    expect(restarts).toEqual([20, 40, 80])
    expect(s.kind === 'failed' && s.stderr).toContain('LAST-LINE')
    await h.stop()
  })

  it('a hello that never answers restarts, then eventually fails', async () => {
    const h = hub('silent', { helloTimeoutMs: 200 })
    h.start()
    await waitState(h, 'restarting')
    const s = await waitState(h, 'failed', 5000)
    expect(s.kind).toBe('failed')
    await h.stop()
  })

  it('does not crash when writing to a stdin whose child just died', async () => {
    const h = hub('ok')
    h.start()
    await waitState(h, 'running')
    const child = (h as any).child
    child.kill('SIGKILL')
    // Write in the SAME synchronous tick as kill(): this is the actual race window
    // (confirmed with a standalone Node probe) -- the OS pipe is already broken, but
    // Node hasn't destroyed the stdin stream yet (that happens once 'exit' is
    // processed), so the write reaches the fd and raises EPIPE asynchronously.
    // A large payload matters: it must reach the syscall instead of staying buffered.
    expect(() => h.notify('term.write', { id: 't1', data: 'x'.repeat(65536) })).not.toThrow()
    await expect(h.call('status')).rejects.toThrow()
    await h.stop()
  })

  it('start() is a no-op while a child is already running', async () => {
    const h = hub('ok')
    h.start()
    await waitState(h, 'running')
    const before = (h as any).child
    h.start()
    await new Promise((r) => setTimeout(r, 50))
    expect((h as any).child).toBe(before)
    await h.stop()
  })

  it('stop() during a scheduled restart reports failed instead of a stale countdown', async () => {
    const h = hub('crash', { backoffMs: [5000] })
    h.start()
    await waitState(h, 'restarting')
    await h.stop()
    expect(h.state).toEqual({ kind: 'failed', message: 'hub stopped', stderr: '' })
  })

  it('a call with no timeout rejects after the 60 s default', async () => {
    const h = hub('ok')
    h.start()
    await waitState(h, 'running')
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    try {
      const hung = h.call('never')
      const settled = expect(hung).rejects.toThrow('hub did not answer in 60 s')
      vi.advanceTimersByTime(59_999)
      await expect(Promise.race([hung.then(() => 'done', () => 'done'), Promise.resolve('pending')])).resolves.toBe('pending')
      vi.advanceTimersByTime(1)
      await settled
    } finally {
      vi.useRealTimers()
    }
    await h.stop()
  })

  it('start() after failing gets the full crash budget and a fresh stderr tail', async () => {
    const h = hub('crash')
    const restarts: number[] = []
    h.on('state', (s: HubState) => { if (s.kind === 'restarting') restarts.push(s.inMs) })
    h.start()
    await waitState(h, 'failed')
    restarts.length = 0
    h.start()
    await waitState(h, 'restarting')
    const s = await waitState(h, 'failed')
    expect(restarts).toEqual([20, 40, 80])
    expect(s.kind === 'failed' && s.stderr.match(/boom/g)?.length).toBe(4)
    await h.stop()
  })
})

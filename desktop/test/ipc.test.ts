import { describe, expect, it, vi } from 'vitest'

vi.mock('electron', () => ({
  ipcMain: { handle: vi.fn(), on: vi.fn() },
}))

import { ipcMain } from 'electron'
import { guard, registerIpc, relayCall, relayNotify } from '../src/main/ipc'

const fakeHub = () => ({ call: vi.fn(async () => ({ ok: true })), notify: vi.fn() })

describe('ipc whitelist', () => {
  it('relays whitelisted requests', async () => {
    const h = fakeHub()
    expect(await relayCall(h, 'servers', {})).toEqual({ ok: true })
    expect(h.call).toHaveBeenCalledWith('servers', {})
  })
  it('rejects anything else', async () => {
    const h = fakeHub()
    await expect(relayCall(h, 'exec', {})).rejects.toThrow('not allowed')
    await expect(relayCall(h, 42, {})).rejects.toThrow('not allowed')
    expect(h.call).not.toHaveBeenCalled()
  })
  it('relays only whitelisted notifications', () => {
    const h = fakeHub()
    relayNotify(h, 'term.write', { id: 'a', data: '' })
    relayNotify(h, 'unlock', { password: 'x' })
    expect(h.notify).toHaveBeenCalledTimes(1)
    expect(h.notify).toHaveBeenCalledWith('term.write', { id: 'a', data: '' })
  })
})

describe('sender guard', () => {
  it('rejects an untrusted sender without calling fn', async () => {
    const fn = vi.fn(async () => 'ok')
    await expect(guard(() => false, {} as never, fn)).rejects.toThrow('untrusted sender')
    expect(fn).not.toHaveBeenCalled()
  })
  it('allows a trusted sender through', async () => {
    const fn = vi.fn(async () => 'ok')
    await expect(guard(() => true, {} as never, fn)).resolves.toBe('ok')
    expect(fn).toHaveBeenCalledTimes(1)
  })
})

describe('registerIpc hub:notify', () => {
  it('drops a notification from an untrusted sender and relays a trusted one', () => {
    const h = { ...fakeHub(), on: vi.fn(), state: { kind: 'running' } }
    const trusted = { sender: 'app' }
    registerIpc(h as never, () => undefined, (e) => e === (trusted as never))
    const on = vi.mocked(ipcMain.on).mock.calls.find(([ch]) => ch === 'hub:notify')![1] as (e: unknown, m: unknown, p: unknown) => void
    on({ sender: 'evil' }, 'term.write', { id: 'a', data: '' })
    expect(h.notify).not.toHaveBeenCalled()
    on(trusted, 'term.write', { id: 'a', data: '' })
    expect(h.notify).toHaveBeenCalledTimes(1)
  })
})

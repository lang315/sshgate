import { describe, expect, it, vi } from 'vitest'

vi.mock('electron', () => ({
  ipcMain: { handle: vi.fn(), on: vi.fn() },
  BrowserWindow: class {},
}))

import { relayCall, relayNotify } from '../src/main/ipc'

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

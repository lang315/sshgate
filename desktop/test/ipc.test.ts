import { describe, expect, it, vi } from 'vitest'

vi.mock('electron', () => ({
  ipcMain: { handle: vi.fn(), on: vi.fn() },
}))

import { ipcMain } from 'electron'
import { guard, isMainFrameOf, registerIpc, relayCall, relayNotify } from '../src/main/ipc'

const fakeHub = () => ({ call: vi.fn(async () => ({ ok: true })), notify: vi.fn() })

describe('ipc whitelist', () => {
  it('relays whitelisted requests', async () => {
    const h = fakeHub()
    expect(await relayCall(h, 'servers', {})).toEqual({ ok: true })
    expect(h.call).toHaveBeenCalledWith('servers', {})
    expect(await relayCall(h, 'servers.save', { server: {} })).toEqual({ ok: true })
  })
  it('rejects anything else', async () => {
    const h = fakeHub()
    await expect(relayCall(h, 'exec', {})).rejects.toThrow('not allowed')
    await expect(relayCall(h, 42, {})).rejects.toThrow('not allowed')
    // main calls term.closeAll itself after a renderer crash; the renderer may not.
    await expect(relayCall(h, 'term.closeAll', {})).rejects.toThrow('not allowed')
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

describe('isMainFrameOf', () => {
  const mainFrame = { name: 'main' }
  const wc = { mainFrame }
  const win = (destroyed = false) => ({ isDestroyed: () => destroyed, webContents: wc })
  it('trusts the window\'s own main frame', () => {
    expect(isMainFrameOf(win(), { sender: wc, senderFrame: mainFrame })).toBe(true)
  })
  it('rejects another webContents', () => {
    expect(isMainFrameOf(win(), { sender: { mainFrame }, senderFrame: mainFrame })).toBe(false)
  })
  it('rejects a subframe of the same webContents', () => {
    expect(isMainFrameOf(win(), { sender: wc, senderFrame: { name: 'sub' } })).toBe(false)
    expect(isMainFrameOf(win(), { sender: wc, senderFrame: null })).toBe(false)
  })
  it('rejects a destroyed or missing window', () => {
    expect(isMainFrameOf(win(true), { sender: wc, senderFrame: mainFrame })).toBe(false)
    expect(isMainFrameOf(undefined, { sender: wc, senderFrame: mainFrame })).toBe(false)
  })
})

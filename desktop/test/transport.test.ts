import { beforeEach, describe, expect, it, vi } from 'vitest'

const bridge = {
  call: vi.fn(async () => ({})),
  notify: vi.fn(),
  getState: vi.fn(async () => ({ kind: 'running' })),
  onEvent: vi.fn(() => () => {}),
  onState: vi.fn(() => () => {}),
}
;(globalThis as any).window = { sshmcp: bridge }

const { hub, toBase64, fromBase64, cleanError } = await import('../src/renderer/transport')

describe('transport', () => {
  beforeEach(() => { bridge.call.mockClear(); bridge.notify.mockClear() })

  it('base64 round-trips arbitrary bytes, including invalid UTF-8', () => {
    const b = new Uint8Array([0, 255, 0xc3, 0x28, 10, 13, 0xe2, 0x82])
    expect(fromBase64(toBase64(b))).toEqual(b)
    expect(toBase64(new TextEncoder().encode('xin chào'))).toBe(Buffer.from('xin chào').toString('base64'))
  })

  it('base64 round-trips a buffer larger than the chunk size', () => {
    const b = new Uint8Array(70000)
    for (let i = 0; i < b.length; i++) b[i] = i % 256
    expect(fromBase64(toBase64(b))).toEqual(b)
  })

  it('termWrite sends base64 and the user flag as a notification', () => {
    hub.termWrite('t1', new TextEncoder().encode('ls\r'), true)
    expect(bridge.notify).toHaveBeenCalledWith('term.write', { id: 't1', data: Buffer.from('ls\r').toString('base64'), user: true })
  })

  it('decide passes outcome and reason', async () => {
    await hub.decide('abc', 'denied', 'no')
    expect(bridge.call).toHaveBeenCalledWith('decide', { id: 'abc', outcome: 'denied', reason: 'no' })
  })

  it('termOpen sends the client-chosen id', async () => {
    await hub.termOpen('t2', 'box', 24, 80)
    expect(bridge.call).toHaveBeenCalledWith('term.open', { id: 't2', server: 'box', rows: 24, cols: 80 })
  })

  it('cleanError strips the electron invoke prefix, with or without the inner "Error: "', () => {
    expect(cleanError(new Error("Error invoking remote method 'hub:call': Error: boom")).message).toBe('boom')
    expect(cleanError(new Error("Error invoking remote method 'hub:call': boom")).message).toBe('boom')
    expect(cleanError(new Error('boom')).message).toBe('boom')
    expect(cleanError('boom').message).toBe('boom')
  })
})

import { describe, expect, it, vi } from 'vitest'

const exposed: Record<string, Record<string, (...a: unknown[]) => unknown>> = {}
const invoke = vi.fn(async (..._a: unknown[]) => [])
vi.mock('electron', () => ({
  contextBridge: { exposeInMainWorld: (k: string, v: Record<string, (...a: unknown[]) => unknown>) => { exposed[k] = v } },
  ipcRenderer: { invoke, send: vi.fn(), on: vi.fn(), removeListener: vi.fn() },
  webUtils: { getPathForFile: (f: File) => (f.name === 'real' ? '/Users/me/real' : '') },
}))

await import('../src/preload/preload')

describe('preload grantDropped', () => {
  const grant = (...a: unknown[]) => exposed.sshmcp.grantDropped(...a) as Promise<unknown>
  it('sends only real file paths', async () => {
    await grant([new File([], 'real'), new File([], 'made-by-script')], 'box')
    expect(invoke).toHaveBeenCalledWith('files:grantDropped', { server: 'box', paths: ['/Users/me/real'] })
  })
  it('refuses anything but an array of Files, and too many', async () => {
    await expect(grant('x', 'box')).rejects.toThrow()
    await expect(grant([{ name: 'real' }], 'box')).rejects.toThrow()
    await expect(grant(Array(1001).fill(new File([], 'real')), 'box')).rejects.toThrow()
  })
})

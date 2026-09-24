import { describe, expect, it, vi } from 'vitest'

vi.mock('electron', () => ({ Menu: { setApplicationMenu: vi.fn(), buildFromTemplate: vi.fn() } }))

import { menuTemplate, recoverRenderer } from '../src/main/window'

const roles = (t: unknown): string[] => JSON.stringify(t).match(/"role":"[^"]+"/g)?.map((r) => r.slice(8, -1)) ?? []

describe('menuTemplate', () => {
  it('has no menu at all on Windows and Linux', () => {
    expect(menuTemplate('win32')).toBeNull()
    expect(menuTemplate('linux')).toBeNull()
  })
  it('on macOS keeps only app and clipboard items: no reload, close, devtools or undo', () => {
    const r = roles(menuTemplate('darwin'))
    expect(r).toEqual(expect.arrayContaining(['quit', 'copy', 'paste', 'selectAll']))
    for (const bad of ['reload', 'forceReload', 'close', 'toggleDevTools', 'viewMenu', 'windowMenu', 'fileMenu']) expect(r).not.toContain(bad)
  })
})

describe('recoverRenderer', () => {
  it('locks the hub before reloading, and still reloads if lock fails', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const order: string[] = []
    const win = { isDestroyed: () => false, reload: () => order.push('reload') }
    await recoverRenderer({ call: async (m) => { order.push(m) } }, win, 'crashed')
    expect(order).toEqual(['lock', 'reload'])
    order.length = 0
    await recoverRenderer({ call: async () => { throw new Error('hub down') } }, win, 'oom')
    expect(order).toEqual(['reload'])
  })
  it('does not reload a destroyed window', async () => {
    const reload = vi.fn()
    await recoverRenderer({ call: async () => {} }, { isDestroyed: () => true, reload }, 'killed')
    expect(reload).not.toHaveBeenCalled()
  })
})

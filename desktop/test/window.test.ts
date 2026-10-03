import { describe, expect, it, vi } from 'vitest'

vi.mock('electron', () => ({ Menu: { setApplicationMenu: vi.fn(), buildFromTemplate: vi.fn() } }))

import { CrashPolicy, hubGone, menuTemplate, recoverRenderer } from '../src/main/window'

const roles = (t: unknown): string[] => JSON.stringify(t).match(/"role":"[^"]+"/g)?.map((r) => r.slice(8, -1)) ?? []

describe('menuTemplate', () => {
  it('has no menu at all on Windows and Linux', () => {
    expect(menuTemplate('win32')).toBeNull()
    expect(menuTemplate('linux')).toBeNull()
  })
  it('on macOS keeps only app and edit items: no reload, close or devtools', () => {
    const r = roles(menuTemplate('darwin'))
    expect(r).toEqual(expect.arrayContaining(['quit', 'undo', 'redo', 'cut', 'copy', 'paste', 'selectAll']))
    for (const bad of ['reload', 'forceReload', 'close', 'toggleDevTools', 'viewMenu', 'windowMenu', 'fileMenu']) expect(r).not.toContain(bad)
  })
})

describe('CrashPolicy', () => {
  it('reloads twice, then gives up on the 3rd crash within 60 s', () => {
    const p = new CrashPolicy()
    expect(p.record(0)).toBe('reload')
    expect(p.record(10_000)).toBe('reload')
    expect(p.record(59_999)).toBe('error')
  })
  it('forgets crashes older than 60 s', () => {
    const p = new CrashPolicy()
    expect(p.record(0)).toBe('reload')
    expect(p.record(30_000)).toBe('reload')
    expect(p.record(60_000)).toBe('reload') // the crash at 0 has aged out
    expect(p.record(89_999)).toBe('error')
  })
})

describe('recoverRenderer', () => {
  const fakeWin = (order: string[], destroyed = false) => ({
    isDestroyed: () => destroyed,
    reload: () => { order.push('reload') },
    loadURL: async (u: string) => { order.push('error:' + decodeURIComponent(u.slice(u.indexOf(',') + 1))) },
  })
  const okHub = (order: string[]) => ({ call: async (m: string) => { order.push(m) } })

  it('locks the hub and closes its terminals, then reloads', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const order: string[] = []
    const params: unknown[] = []
    const hub = { call: async (m: string, p?: unknown) => { order.push(m); params.push(p) } }
    await recoverRenderer(hub, fakeWin(order), 'crashed', new CrashPolicy())
    expect(order).toEqual(['lock', 'term.closeAll', 'files.cancelAll', 'reload'])
    expect(params[0]).toEqual({ stopAuto: true }) // a crash stops auto-allow
  })
  it('shows an error page instead of reloading when lock fails', async () => {
    const order: string[] = []
    const hub = { call: async (m: string) => { order.push(m); if (m === 'lock') throw new Error('lock timed out') } }
    await recoverRenderer(hub, fakeWin(order), 'oom', new CrashPolicy())
    expect(order).toEqual(['lock', 'term.closeAll', 'files.cancelAll', "error:Could not lock the vault after a crash; quit the app to lock it."])
  })
  it('treats a dead hub as locked and reloads', async () => {
    for (const msg of ['hub is not running', 'hub restarted']) {
      const order: string[] = []
      const hub = { call: async (m: string) => { order.push(m); throw new Error(msg) } }
      await recoverRenderer(hub, fakeWin(order), 'crashed', new CrashPolicy())
      expect(order).toEqual(['lock', 'term.closeAll', 'files.cancelAll', 'reload'])
    }
  })
  it('still reloads if only term.closeAll fails', async () => {
    const order: string[] = []
    const hub = { call: async (m: string) => { order.push(m); if (m === 'term.closeAll') throw new Error('x') } }
    await recoverRenderer(hub, fakeWin(order), 'oom', new CrashPolicy())
    expect(order).toEqual(['lock', 'term.closeAll', 'files.cancelAll', 'reload'])
  })
  it('shows an error page on the 3rd crash within 60 s', async () => {
    const order: string[] = []
    const policy = new CrashPolicy()
    for (let i = 0; i < 3; i++) await recoverRenderer(okHub(order), fakeWin(order), 'crashed', policy)
    expect(order.filter((o) => o !== 'lock' && o !== 'term.closeAll' && o !== 'files.cancelAll')).toEqual(
      ['reload', 'reload', 'error:The window crashed repeatedly. Quit and restart the app.'])
  })
  it('does not touch a destroyed window', async () => {
    const order: string[] = []
    await recoverRenderer(okHub(order), fakeWin(order, true), 'killed', new CrashPolicy())
    expect(order).toEqual(['lock', 'term.closeAll', 'files.cancelAll'])
  })
  it('drops file grants after a crash', async () => {
    const order: string[] = []
    const reset = vi.fn()
    await recoverRenderer(okHub(order), fakeWin(order), 'crashed', new CrashPolicy(), reset)
    expect(reset).toHaveBeenCalledTimes(1)
  })
})

describe('hubGone', () => {
  it('matches only rejections from a hub process that is not there', () => {
    expect(hubGone(new Error('hub is not running'))).toBe(true)
    expect(hubGone(new Error('hub restarted'))).toBe(true)
    expect(hubGone(new Error('hub did not answer in 5 s'))).toBe(false)
    expect(hubGone(new Error('wrong master password'))).toBe(false)
    expect(hubGone(undefined)).toBe(false)
  })
})

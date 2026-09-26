import { describe, expect, it, vi } from 'vitest'
import { FilesRelay, Grants, GRANT_TTL_MS, MAX_DROP } from '../src/main/files'
import { displayText } from '../src/shared/display'

const fakeHub = () => ({ call: vi.fn(async (_m: string, _p?: unknown) => ({})), notify: vi.fn() })
const fakeDialog = (open: string[] = [], response = 0) => ({
  showOpenDialog: vi.fn(async (_win: unknown, _o: unknown) => ({ canceled: open.length === 0, filePaths: open })),
  showMessageBox: vi.fn(async (_win: unknown, _o: unknown) => ({ response, checkboxChecked: false })),
})
const win = {} as never

const chr = (n: number): string => String.fromCharCode(n)

describe('displayText', () => {
  it('escapes controls, bidi overrides/isolates, zero-width and line separators, leaves plain text alone', () => {
    expect(displayText('a' + chr(0x202e) + 'b')).toBe('a\\u202eb')
    expect(displayText('x' + chr(0x0000) + chr(0x009f) + chr(0x200b) + chr(0x2028) + chr(0xfeff) + 'y'))
      .toBe('x\\u0000\\u009f\\u200b\\u2028\\ufeffy')
    expect(displayText('plain')).toBe('plain')
  })
})

describe('Grants', () => {
  it('are single-use, bound to kind and server, and expire', () => {
    let now = 0
    const g = new Grants(() => now)
    const t = g.add('/a', 'read', 'box')
    expect(() => g.take(t, 'writeDir', 'box')).toThrow('not granted')
    const u = g.add('/a', 'read', 'box')
    expect(() => g.take(u, 'read', 'other')).toThrow('not granted')
    const v = g.add('/a', 'read', 'box')
    expect(g.take(v, 'read', 'box')).toBe('/a')
    expect(() => g.take(v, 'read', 'box')).toThrow('not granted')
    const w = g.add('/a', 'read', 'box')
    now = GRANT_TTL_MS + 1
    expect(() => g.take(w, 'read', 'box')).toThrow('not granted')
    expect(() => g.take('/etc/passwd', 'read', 'box')).toThrow('not granted')
    const x = g.add('/a', 'read', 'box')
    g.clear()
    expect(() => g.take(x, 'read', 'box')).toThrow('not granted')
  })
})

describe('FilesRelay', () => {
  it('names the server and folder in the upload dialog and hands out tokens', async () => {
    const d = fakeDialog(['/Users/me/proj'])
    const r = new FilesRelay(fakeHub(), d, () => win)
    const picks = await r.pickUpload({ server: 'box', folder: '/home/u', mode: 'folder' })
    expect(d.showOpenDialog.mock.calls[0][1]).toMatchObject({ title: 'Upload to box:/home/u', properties: ['openDirectory', 'multiSelections'] })
    expect(picks).toEqual([{ token: expect.stringMatching(/^g-[0-9a-f]{32}$/), name: 'proj' }])
    expect(picks[0].token).not.toContain('/Users')
  })

  it('rebuilds files.plan from its allowlist and swaps tokens', async () => {
    const h = fakeHub()
    const r = new FilesRelay(h, fakeDialog(['/Users/me/a']), () => win)
    const [{ token }] = await r.pickUpload({ server: 'box', folder: '/home/u', mode: 'files' })
    await r.call('files.plan', { id: 'j1', server: 'box', op: 'upload', sources: [token], dest: '/home/u', Sources: ['/etc/passwd'], extra: 1 })
    expect(h.call).toHaveBeenCalledWith('files.plan', { id: 'j1', server: 'box', op: 'upload', sources: ['/Users/me/a'], dest: '/home/u' })
  })

  it('refuses raw paths, reused tokens, and tokens for another server', async () => {
    const h = fakeHub()
    const r = new FilesRelay(h, fakeDialog(['/Users/me/a']), () => win)
    await expect(r.call('files.plan', { id: 'j', server: 'box', op: 'upload', sources: ['/Users/me/.ssh/id_ed25519'], dest: '/tmp' })).rejects.toThrow('not granted')
    const [{ token }] = await r.pickUpload({ server: 'box', folder: '/', mode: 'files' })
    await expect(r.call('files.plan', { id: 'j', server: 'other', op: 'upload', sources: [token], dest: '/tmp' })).rejects.toThrow('not granted')
    await expect(r.call('files.plan', { id: 'j', server: 'box', op: 'upload', sources: [token], dest: '/tmp' })).rejects.toThrow('not granted')
    await expect(r.call('files.plan', { id: 'j', server: 'box', op: 'download', sources: ['/etc'], dest: '/Users/me' })).rejects.toThrow('not granted')
    expect(h.call).not.toHaveBeenCalled()
  })

  it('decides download conflicts in the native dialog', async () => {
    const h = fakeHub()
    const d = fakeDialog(['/Users/me/dl'], 2)
    const r = new FilesRelay(h, d, () => win)
    const dir = await r.pickDownloadDir({ server: 'box' })
    await r.call('files.plan', { id: 'd1', server: 'box', op: 'download', sources: ['/home/u/x'], dest: dir!.token })
    r.onNotification('files.planned', { id: 'd1', conflicts: { count: 3, sample: ['x/a', 'x/‮b'] } })
    await expect(r.call('files.run', { id: 'd1', conflict: 'overwrite' })).rejects.toThrow('native dialog')
    await r.call('files.run', { id: 'd1', conflict: 'ask' })
    expect(d.showMessageBox.mock.calls[0][1]).toMatchObject({ buttons: ['Cancel', 'Skip existing', 'Overwrite all'], defaultId: 0, cancelId: 0 })
    expect(String((d.showMessageBox.mock.calls[0][1] as { detail: unknown }).detail)).toContain(displayText('x/‮b'))
    expect(h.call).toHaveBeenLastCalledWith('files.run', { id: 'd1', conflict: 'overwrite' })
  })

  it('cancels a download when the native dialog is cancelled, and skips when nothing conflicts', async () => {
    const h = fakeHub()
    const r = new FilesRelay(h, fakeDialog(['/Users/me/dl'], 0), () => win)
    const a = await r.pickDownloadDir({ server: 'box' })
    await r.call('files.plan', { id: 'd2', server: 'box', op: 'download', sources: ['/x'], dest: a!.token })
    r.onNotification('files.planned', { id: 'd2', conflicts: { count: 1, sample: ['x'] } })
    expect(await r.call('files.run', { id: 'd2', conflict: 'ask' })).toEqual({ cancelled: true })
    expect(h.notify).toHaveBeenCalledWith('files.cancel', { id: 'd2' })
    const b = await r.pickDownloadDir({ server: 'box' })
    await r.call('files.plan', { id: 'd3', server: 'box', op: 'download', sources: ['/x'], dest: b!.token })
    r.onNotification('files.planned', { id: 'd3', conflicts: { count: 0, sample: [] } })
    await r.call('files.run', { id: 'd3', conflict: 'ask' })
    expect(h.call).toHaveBeenLastCalledWith('files.run', { id: 'd3', conflict: 'skip' })
  })

  it('relays upload and delete runs as asked, and only overwrite or skip', async () => {
    const h = fakeHub()
    const r = new FilesRelay(h, fakeDialog(), () => win)
    await r.call('files.run', { id: 'u1', conflict: 'overwrite', extra: 1 })
    expect(h.call).toHaveBeenLastCalledWith('files.run', { id: 'u1', conflict: 'overwrite' })
    await expect(r.call('files.run', { id: 'u1', conflict: 'ask' })).rejects.toThrow()
  })

  it('drops grants on lock, when the hub stops, and on reset', async () => {
    const r = new FilesRelay(fakeHub(), fakeDialog(['/a', '/b', '/c']), () => win)
    const [a] = await r.pickUpload({ server: 'box', folder: '/', mode: 'files' })
    r.onNotification('locked', { reason: 'idle' })
    expect(() => r.grants.take(a.token, 'read', 'box')).toThrow()
    const [b] = await r.pickUpload({ server: 'box', folder: '/', mode: 'files' })
    r.onState({ kind: 'restarting', attempt: 1, inMs: 1000 })
    expect(() => r.grants.take(b.token, 'read', 'box')).toThrow()
    const [c] = await r.pickUpload({ server: 'box', folder: '/', mode: 'files' })
    r.reset()
    expect(() => r.grants.take(c.token, 'read', 'box')).toThrow()
  })

  it('grants dropped paths only when absolute and not too many', () => {
    const r = new FilesRelay(fakeHub(), fakeDialog(), () => win)
    expect(r.grantDropped({ server: 'box', paths: ['/Users/me/a'] })).toEqual([{ token: expect.any(String), name: 'a' }])
    expect(() => r.grantDropped({ server: 'box', paths: ['rel'] })).toThrow()
    expect(() => r.grantDropped({ server: 'box', paths: Array(MAX_DROP + 1).fill('/a') })).toThrow()
    expect(() => r.grantDropped({ server: 'box', paths: '/a' })).toThrow()
  })
})

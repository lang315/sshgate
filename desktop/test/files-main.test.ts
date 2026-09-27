import { describe, expect, it, vi } from 'vitest'
import { FilesRelay, Grants, GRANT_TTL_MS, MAX_DROP } from '../src/main/files'
import { HubReplyError } from '../src/main/hubProcess'
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
    expect(d.showOpenDialog.mock.calls[0][1]).toMatchObject({ title: 'Upload to box:/home/u', message: 'Upload to box:/home/u', properties: ['openDirectory', 'multiSelections'] })
    expect(picks).toEqual([{ token: expect.stringMatching(/^g-[0-9a-f]{32}$/), name: 'proj' }])
    expect(picks[0].token).not.toContain('/Users')
  })

  it('names the server in the download dialog', async () => {
    const d = fakeDialog(['/Users/me/dl'])
    const r = new FilesRelay(fakeHub(), d, () => win)
    await r.pickDownloadDir({ server: 'box' })
    expect(d.showOpenDialog.mock.calls[0][1]).toMatchObject({ title: 'Download from box to…', message: 'Download from box to…', properties: ['openDirectory', 'createDirectory'] })
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
    const r = new FilesRelay(h, fakeDialog(['/Users/me/a']), () => win)
    const [{ token }] = await r.pickUpload({ server: 'box', folder: '/home/u', mode: 'files' })
    await r.call('files.plan', { id: 'u1', server: 'box', op: 'upload', sources: [token], dest: '/home/u' })
    await r.call('files.run', { id: 'u1', conflict: 'overwrite', extra: 1 })
    expect(h.call).toHaveBeenLastCalledWith('files.run', { id: 'u1', conflict: 'overwrite' })
    await expect(r.call('files.run', { id: 'u1', conflict: 'ask' })).rejects.toThrow()
  })

  it('refuses files.run for an id that was never planned', async () => {
    const h = fakeHub()
    const r = new FilesRelay(h, fakeDialog(), () => win)
    await expect(r.call('files.run', { id: 'never-planned', conflict: 'overwrite' })).rejects.toThrow('not granted')
    expect(h.call).not.toHaveBeenCalled()
  })

  it('refuses a live job id to be replanned, closing the reused-id race', async () => {
    const h = fakeHub()
    const d = fakeDialog(['/Users/me/dl'], 2)
    const r = new FilesRelay(h, d, () => win)
    // The old job under id 'x' is still live (no files.done yet)...
    await r.call('files.plan', { id: 'x', server: 'box', op: 'delete', sources: ['/tmp/a'] })
    // ...so an attacker (or a racing renderer) replanning the same id as a download is refused,
    // never reaching the hub, and never silently reusing the old job's bookkeeping.
    const dirDuringRace = await r.pickDownloadDir({ server: 'box' })
    await expect(r.call('files.plan', { id: 'x', server: 'box', op: 'download', sources: ['/y'], dest: dirDuringRace!.token }))
      .rejects.toThrow('already in use')
    expect(h.call).not.toHaveBeenCalledWith('files.plan', expect.objectContaining({ op: 'download' }))

    // Only once the old job's files.done arrives does 'x' become available again.
    r.onNotification('files.done', { id: 'x' })
    const dir = await r.pickDownloadDir({ server: 'box' })
    await r.call('files.plan', { id: 'x', server: 'box', op: 'download', sources: ['/y'], dest: dir!.token })
    r.onNotification('files.planned', { id: 'x', conflicts: { count: 2, sample: ['y/a'] } })
    // The new download job still has conflicts: overwrite must go through the native dialog, never silently.
    await expect(r.call('files.run', { id: 'x', conflict: 'overwrite' })).rejects.toThrow('native dialog')
    expect(h.call).not.toHaveBeenCalledWith('files.run', { id: 'x', conflict: 'overwrite' })
    await r.call('files.run', { id: 'x', conflict: 'ask' })
    expect(d.showMessageBox).toHaveBeenCalledTimes(1)
    expect(h.call).toHaveBeenLastCalledWith('files.run', { id: 'x', conflict: 'overwrite' })
  })

  it('drops a job entry the hub rejected (HubReplyError), so the same id can be replanned', async () => {
    const h = { call: vi.fn(async (): Promise<unknown> => { throw new HubReplyError('hub refused') }), notify: vi.fn() }
    const r = new FilesRelay(h, fakeDialog(), () => win)
    await expect(r.call('files.plan', { id: 'p1', server: 'box', op: 'delete', sources: ['/tmp/a'] })).rejects.toThrow('hub refused')
    h.call.mockImplementationOnce(async () => ({ ok: true }))
    await expect(r.call('files.plan', { id: 'p1', server: 'box', op: 'delete', sources: ['/tmp/a'] })).resolves.toEqual({ ok: true })
  })

  it('keeps a job entry when the hub call for files.plan merely rejects (e.g. a timeout), not just when it never resolves', async () => {
    const h = { call: vi.fn(async (): Promise<unknown> => { throw new Error('hub did not answer in 60 s') }), notify: vi.fn() }
    const d = fakeDialog(['/Users/me/dl'], 0)
    const r = new FilesRelay(h, d, () => win)
    const dir = await r.pickDownloadDir({ server: 'box' })
    await expect(r.call('files.plan', { id: 't1', server: 'box', op: 'download', sources: ['/x'], dest: dir!.token }))
      .rejects.toThrow('hub did not answer')
    // the entry is still live: a replan of the same id is refused...
    const dir2 = await r.pickDownloadDir({ server: 'box' })
    await expect(r.call('files.plan', { id: 't1', server: 'box', op: 'download', sources: ['/x'], dest: dir2!.token }))
      .rejects.toThrow('already in use')
    // ...and a run for it still goes through the download path (no files.planned arrived yet, so
    // conflicts read as 0 and it's forwarded as a skip) rather than being refused as "not granted".
    h.call.mockResolvedValueOnce({})
    await r.call('files.run', { id: 't1', conflict: 'ask' })
    expect(h.call).toHaveBeenLastCalledWith('files.run', { id: 't1', conflict: 'skip' })
  })

  it('ignores a stale dialog resolution once the same id has been replanned while it was open', async () => {
    const h = fakeHub()
    let resolveDialog!: (v: { response: number; checkboxChecked: boolean }) => void
    const dialog = {
      showOpenDialog: vi.fn(async (_w: unknown, _o: unknown) => ({ canceled: false, filePaths: ['/Users/me/dl'] })),
      showMessageBox: vi.fn(() => new Promise<{ response: number; checkboxChecked: boolean }>((resolve) => { resolveDialog = resolve })),
    }
    const r = new FilesRelay(h, dialog, () => win)
    const dir = await r.pickDownloadDir({ server: 'box' })
    await r.call('files.plan', { id: 'x', server: 'box', op: 'download', sources: ['/y'], dest: dir!.token })
    r.onNotification('files.planned', { id: 'x', conflicts: { count: 1, sample: ['y/a'] } })
    const running = r.call('files.run', { id: 'x', conflict: 'ask' }) // opens the dialog; does not resolve yet

    // While the dialog is open, the renderer cancels 'x', gets files.done, and replans the same id
    // into a different download.
    r.onNotification('files.done', { id: 'x' })
    const dir2 = await r.pickDownloadDir({ server: 'box' })
    await r.call('files.plan', { id: 'x', server: 'box', op: 'download', sources: ['/z'], dest: dir2!.token })
    r.onNotification('files.planned', { id: 'x', conflicts: { count: 0, sample: [] } })

    // The stale dialog now resolves with "Overwrite all" — it must not touch the new job.
    resolveDialog({ response: 2, checkboxChecked: false })
    expect(await running).toEqual({ cancelled: true })
    expect(h.call).not.toHaveBeenCalledWith('files.run', { id: 'x', conflict: 'overwrite' })
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

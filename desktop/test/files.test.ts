import { describe, expect, it } from 'vitest'
import type { FileEntry } from '../src/shared/protocol'
import {
  deleteNeedsTyping, doneSummary, formatMode, formatSize, hiddenPref, joinPath, lastFolder, nameError, newJobId,
  nextSelection, parentPath, plannedAction, relistAfterJob, sortEntries, visibleEntries, windowRange,
} from '../src/renderer/files'
import { displayText } from '../src/shared/display'

const e = (name: string, kind: FileEntry['kind'] = 'file', size = 0, mtime = 0, mode = 0o644): FileEntry => ({ name, kind, size, mtime, mode })

describe('listing helpers', () => {
  it('sorts folders first, then by the key', () => {
    const es = [e('b', 'file', 5), e('a', 'file', 9), e('z', 'dir'), e('c', 'dir')]
    expect(sortEntries(es, 'name', false).map((x) => x.name)).toEqual(['c', 'z', 'a', 'b'])
    expect(sortEntries(es, 'size', true).map((x) => x.name)).toEqual(['c', 'z', 'a', 'b'])
    expect(sortEntries(es, 'name', true).map((x) => x.name)).toEqual(['z', 'c', 'b', 'a'])
  })
  it('hides dotfiles unless asked', () => {
    expect(visibleEntries([e('.env'), e('a')], false).map((x) => x.name)).toEqual(['a'])
    expect(visibleEntries([e('.env'), e('a')], true)).toHaveLength(2)
  })
  it('formats sizes and modes', () => {
    expect(formatSize(0)).toBe('0 B')
    expect(formatSize(1023)).toBe('1023 B')
    expect(formatSize(1536)).toBe('1.5 KB')
    expect(formatSize(5 * 1024 ** 3)).toBe('5.0 GB')
    expect(formatMode('dir', 0o755)).toBe('drwxr-xr-x')
    expect(formatMode('link', 0o777)).toBe('lrwxrwxrwx')
    expect(formatMode('file', 0o640)).toBe('-rw-r-----')
  })
  it('joins and walks up remote paths', () => {
    expect(joinPath('/', 'a')).toBe('/a')
    expect(joinPath('/home/u', 'a')).toBe('/home/u/a')
    expect(parentPath('/home/u')).toBe('/home')
    expect(parentPath('/home')).toBe('/')
    expect(parentPath('/')).toBeUndefined()
    expect(parentPath('')).toBeUndefined()
  })
  it('checks new names', () => {
    // A backslash would be created, then hidden by the next listing as a bad name.
    for (const bad of ['', '.', '..', 'a/b', 'a\u0000b', 'a\\b']) expect(nameError(bad)).toBeTruthy()
    expect(nameError('ok name')).toBeUndefined()
  })
  it('windows the rows it renders', () => {
    expect(windowRange(0, 280, 28, 1000, 5)).toEqual([0, 15])
    expect(windowRange(28 * 500, 280, 28, 1000, 5)).toEqual([495, 515])
    expect(windowRange(28 * 999, 280, 28, 1000, 5)).toEqual([994, 1000])
  })
})

describe('selection', () => {
  const order = ['a', 'b', 'c', 'd']
  it('click selects one, toggle adds, shift selects a range from the anchor', () => {
    let s = nextSelection(new Set(), order, 'b', {})
    expect([...s.names]).toEqual(['b'])
    s = nextSelection(s.names, order, 'd', { toggle: true }, s.anchor)
    expect([...s.names].sort()).toEqual(['b', 'd'])
    s = nextSelection(s.names, order, 'c', { range: true }, 'a')
    expect([...s.names]).toEqual(['a', 'b', 'c'])
  })
})

describe('delete confirmation', () => {
  it('asks for the typed word only when a selected folder has contents', () => {
    expect(deleteNeedsTyping(false, 2, { files: 2, dirs: 0, links: 0, errorCount: 0 })).toBe(false)
    expect(deleteNeedsTyping(true, 1, { files: 0, dirs: 1, links: 0, errorCount: 0 })).toBe(false)
    expect(deleteNeedsTyping(true, 1, { files: 3, dirs: 1, links: 0, errorCount: 0 })).toBe(true)
    expect(deleteNeedsTyping(true, 2, { files: 1, dirs: 1, links: 1, errorCount: 0 })).toBe(true)
  })
  it('counts plan errors as contents, so an unreadable child still asks for the word', () => {
    expect(deleteNeedsTyping(true, 1, { files: 0, dirs: 1, links: 0, errorCount: 1 })).toBe(true)
    expect(deleteNeedsTyping(false, 1, { files: 0, dirs: 0, links: 0, errorCount: 1 })).toBe(false)
  })
})

describe('job flow', () => {
  it('relists after a job only in the folder shown, with no newer navigation', () => {
    expect(relistAfterJob('/a', '/a', '/a', false)).toBe(true)
    expect(relistAfterJob('/b', '/a', '/a', false)).toBe(false) // the job was elsewhere
    expect(relistAfterJob('/a', '/a', '/b', true)).toBe(false) // the user is loading /b
    expect(relistAfterJob('/a', '/a', '/a', true)).toBe(false) // a load is already in flight
    expect(relistAfterJob('/a', '/a', '/b', false)).toBe(false) // /b failed; /a is not what the user asked for
  })
  it('decides what a landed plan does; a cancel sent while planning wins', () => {
    expect(plannedAction('upload', 0, false)).toBe('skip')
    expect(plannedAction('upload', 2, false)).toBe('confirm')
    expect(plannedAction('download', 3, false)).toBe('ask') // main asks about download conflicts
    expect(plannedAction('delete', 0, false)).toBe('confirm')
    for (const op of ['upload', 'download', 'delete'] as const) expect(plannedAction(op, 0, true)).toBe('cancel')
    expect(plannedAction('delete', 5, true)).toBe('cancel')
  })
})

describe('summaries and memory', () => {
  it('summarises a finished job', () => {
    expect(doneSummary({ id: 'j', op: 'upload', copied: 3, skipped: 1, deleted: 0, bytes: 10, errorCount: 2, errors: [], cancelled: false, reason: '' }))
      .toBe('Uploaded 3 · skipped 1 · 2 errors')
    expect(doneSummary({ id: 'j', op: 'delete', copied: 0, skipped: 0, deleted: 4, bytes: 0, errorCount: 0, errors: [], cancelled: false, reason: '' }))
      .toBe('Deleted 4')
    expect(doneSummary({ id: 'j', op: 'download', copied: 1, skipped: 0, deleted: 0, bytes: 1, errorCount: 0, errors: [], cancelled: true, reason: 'server changed' }))
      .toBe('Cancelled: server changed · downloaded 1')
    expect(doneSummary(undefined, 'hub restarted')).toBe('hub restarted')
  })
  it('remembers the last folder per host and the hidden-files toggle', () => {
    const m = new Map<string, string>()
    const store = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => { m.set(k, v) } } as unknown as Storage
    expect(lastFolder.get(store, 'box')).toBeUndefined()
    lastFolder.set(store, 'box', '/srv')
    expect(lastFolder.get(store, 'box')).toBe('/srv')
    expect(lastFolder.get(undefined, 'box')).toBeUndefined()
    expect(hiddenPref.get(store)).toBe(false)
    hiddenPref.set(store, true)
    expect(hiddenPref.get(store)).toBe(true)
  })
  it('ids match the hub rules', () => {
    expect(newJobId()).toMatch(/^[A-Za-z0-9_-]{1,64}$/)
  })
  it('makes hidden characters visible', () => {
    expect(displayText('invoice‮fdp.exe')).toBe('invoice\\u202efdp.exe')
    expect(displayText('a​b\nc')).toBe('a\\u200bb\\u000ac')
  })
})

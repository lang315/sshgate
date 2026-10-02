import { describe, expect, it } from 'vitest'
import {
  appendLive, applyPage, clearOnLock, dayLabel, EMPTY, failLoad, formatTime, matches, mergeEntries, releaseHeld, rowView,
  startLoad, toggleChip, toQuery, type AuditList,
} from '../src/renderer/audit'
import { REQUEST_METHODS, type AuditEntry, type AuditRecord } from '../src/shared/protocol'

const T = new Date(2026, 8, 30, 14, 5, 9) // local time, so the test is TZ-independent
const iso = (d: Date) => d.toISOString()
const at = (ms: number) => iso(new Date(T.getTime() + ms))
const entry = (seq: number, record: Partial<AuditRecord> = {}): AuditEntry => ({ seq, record: { time: iso(T), ...record } })
const seqs = (l: AuditList) => l.entries.map((e) => e.seq)
const loaded = (entries: AuditEntry[]): AuditList => ({ ...EMPTY, status: 'loaded', entries })

describe('formatTime', () => {
  it('shows HH:MM:SS, and the day as a group label', () => {
    expect(formatTime(iso(T))).toBe('14:05:09')
    expect(formatTime('not a time')).toBe('')
    const day = (d: Date) => dayLabel(iso(d), T)
    expect(day(T)).toBe('Today')
    expect(day(new Date(T.getFullYear(), T.getMonth(), T.getDate() - 1, 23, 59, 1))).toBe('Yesterday')
    expect(day(new Date(2026, 0, 5, 12))).toBe('Mon Jan 05 2026')
    expect(dayLabel('not a time', T)).toBe('No date')
  })
})

describe('rowView', () => {
  it('exec: outcome, sudo and wait', () => {
    expect(rowView({ time: iso(T), server: 'box', command: 'rm -rf /tmp/x', outcome: 'denied', sudo: true, waitMs: 12_300 }, T)).toEqual({
      time: '14:05:09', day: 'Today', host: 'box', kind: 'exec', alert: true, main: 'rm -rf /tmp/x', side: ['wait 12 s'],
      badges: [{ text: 'denied', tone: 'danger' }, { text: 'sudo', tone: 'danger' }],
    })
  })
  it('exec: an auto run shows auto, its exit code and the masked count', () => {
    const v = rowView({ time: iso(T), server: 'box', command: 'cat .env', outcome: 'allowed', approval: 'auto', exitCode: 0, redacted: { password: 2, token: 1 } }, T)
    expect(v.badges).toEqual([{ text: 'auto', tone: 'auto' }])
    expect(v.side).toEqual(['3 masked'])
    expect(v).toMatchObject({ exit: 0, alert: false })
  })
  it('exec: every other outcome', () => {
    const badges = (r: Partial<AuditRecord>) => rowView({ time: iso(T), command: 'x', ...r }, T).badges.map((b) => b.text)
    expect(badges({ outcome: 'allowed' })).toEqual(['allowed'])
    expect(badges({ outcome: 'error', approval: 'auto' })).toEqual(['auto', 'error'])
    expect(badges({ outcome: 'expired' })).toEqual(['expired'])
    expect(badges({ outcome: 'approved_but_cancelled' })).toEqual(['cancelled'])
    expect(badges({ outcome: 'cancelled_running' })).toEqual(['cancelled'])
    expect(badges({ outcome: 'sent_to_tab' })).toEqual(['sent to tab'])
    expect(rowView({ time: iso(T), command: 'x', outcome: 'allowed', waitMs: 400 }, T).side).toEqual(['wait 400 ms'])
  })
  it('config: the detail, with the action as the badge', () => {
    const main = (r: Partial<AuditRecord>) => rowView({ time: iso(T), kind: 'config', ...r }, T).main
    expect(main({ action: 'autoAllowOn', until: at(15 * 60_000 - 800) })).toBe('15m')
    expect(main({ action: 'autoAllowOn', until: at(2 * 3_600_000) })).toBe('2h')
    expect(main({ action: 'autoAllowOn', forever: true })).toBe('forever')
    expect(main({ action: 'save', changed: ['host: a → 10.0.0.2', 'password'] })).toBe('host: a → 10.0.0.2, password')
    expect(main({ action: 'autoAllowOff', reason: 'locked' })).toBe('locked')
    expect(main({ action: 'delete' })).toBe('')
    expect(rowView({ time: iso(T), kind: 'config', action: 'save', server: 'box' }, T).badges).toEqual([{ text: 'save', tone: 'plain' }])
    expect(rowView({ time: iso(T), kind: 'config', action: 'forgetHostKey' }, T).badges).toEqual([{ text: 'forget host key', tone: 'plain' }])
  })
  it('shows the hosts of a softLock record', () => {
    const v = rowView({ kind: 'config', action: 'softLock', servers: ['box', 'db'], time: '2026-10-02T08:00:00Z' } as AuditRecord, new Date('2026-10-02T09:00:00Z'))
    expect(v.main).toBe('box, db')
    expect(v.host).toBe('box, db')
  })
  it('file: the action, the first path and the phase', () => {
    expect(rowView({ time: iso(T), kind: 'file', server: 'box', action: 'upload', phase: 'start', remote: ['/home/u/a.txt', '/home/u/b.txt'] }, T)).toEqual({
      time: '14:05:09', day: 'Today', host: 'box', kind: 'file', alert: false, badges: [{ text: 'upload', tone: 'plain' }], main: '/home/u/a.txt', side: ['start'],
    })
    expect(rowView({ time: iso(T), kind: 'file', action: 'rename', from: '/a', to: '/b' }, T).main).toBe('/a → /b')
  })
  it('tunnel: the phase and listen → target', () => {
    const v = rowView({ time: iso(T), kind: 'tunnel', server: 'box', phase: 'start', listen: '127.0.0.1:5433', to: 'db:5432' }, T)
    expect(v.badges).toEqual([{ text: 'start', tone: 'plain' }])
    expect(v.main).toBe('127.0.0.1:5433 → db:5432')
    expect(rowView({ time: iso(T), kind: 'tunnel', phase: 'end', listen: '127.0.0.1:1080' }, T).main).toBe('127.0.0.1:1080 SOCKS5')
  })
  it('never throws on a record with wrong field types', () => {
    const bad = { time: 5, command: 7, server: {}, outcome: null, redacted: { x: 'y' } } as unknown as AuditRecord
    expect(rowView(bad, T)).toMatchObject({ time: '', host: '', main: '' })
  })
})

describe('toQuery', () => {
  it('maps chips to a union of kinds and exec outcomes; none on is everything', () => {
    expect(toQuery({ server: '', chips: [], text: '' })).toEqual({})
    expect(toQuery({ server: '', chips: ['auto'], text: '' })).toEqual({ outcomes: ['auto'] })
    expect(toQuery({ server: '', chips: ['denied', 'exec'], text: '' })).toEqual({ kinds: ['exec'], outcomes: ['denied'] })
    expect(toQuery({ server: '', chips: ['tunnels', 'files', 'config'], text: '' })).toEqual({ kinds: ['config', 'file', 'tunnel'] })
    expect(toQuery({ server: 'box', chips: [], text: '  2>&1 ' })).toEqual({ server: 'box', text: '2>&1' })
  })
  it('toggles a chip', () => {
    expect(toggleChip(['exec'], 'auto')).toEqual(['exec', 'auto'])
    expect(toggleChip(['exec', 'auto'], 'exec')).toEqual(['auto'])
  })
})

describe('matches', () => {
  const auto: AuditRecord = { time: 't', server: 'box', command: 'tail 2>&1', outcome: 'allowed', approval: 'auto' }
  const denied: AuditRecord = { time: 't', server: 'vis', command: 'rm x', outcome: 'denied' }
  const save: AuditRecord = { time: 't', kind: 'config', server: 'vis', action: 'save' }
  const all = [auto, denied, save]
  it('mirrors the hub: a union of kinds and exec outcomes, narrowed by server and text', () => {
    expect(all.map((r) => matches(r, {}))).toEqual([true, true, true])
    expect(all.map((r) => matches(r, { outcomes: ['auto'] }))).toEqual([true, false, false])
    expect(all.map((r) => matches(r, { kinds: ['config'], outcomes: ['denied'] }))).toEqual([false, true, true])
    expect(all.map((r) => matches(r, { outcomes: ['allowed'] }))).toEqual([false, false, false])
    expect(all.map((r) => matches(r, { server: 'vis' }))).toEqual([false, true, true])
    expect(all.map((r) => matches(r, { text: 'TAIL 2>&1' }))).toEqual([true, false, false])
    // decoded values: quotes, backslashes and numbers match as shown; key names do not
    const q: AuditRecord = { time: 't', server: 'box', command: 'echo "hi" C:\\Users', timeoutSec: 30, outcome: 'allowed' }
    expect(matches(q, { text: 'echo "hi"' })).toBe(true)
    expect(matches(q, { text: 'C:\\Users' })).toBe(true)
    expect(matches(q, { text: '30' })).toBe(true)
    expect(matches(q, { text: 'command' })).toBe(false)
    expect(matches(q, { text: 'server' })).toBe(false)
    expect(matches({ time: 't', command: 'x', outcome: 'cancelled_running' }, { outcomes: ['cancelled'] })).toBe(true)
  })
})

describe('the record list', () => {
  it('merges by seq, newest first', () => {
    expect(mergeEntries([entry(5), entry(3)], [entry(4), entry(5), entry(1)]).map((e) => e.seq)).toEqual([5, 4, 3, 1])
  })
  it('applies a first page, then an older one', () => {
    let l = startLoad({ ...EMPTY, path: '/old' })
    expect(l).toMatchObject({ status: 'loading', entries: [], path: '/old' })
    l = applyPage(l, { records: [entry(9), entry(8)], next: 8, skipped: 1, path: '/a/audit.jsonl' })
    expect(l).toMatchObject({ status: 'loaded', next: 8, skipped: 1, path: '/a/audit.jsonl' })
    l = applyPage(l, { records: [entry(7)], skipped: 1, path: '/a/audit.jsonl' })
    expect(seqs(l)).toEqual([9, 8, 7])
    expect(l.next).toBeUndefined()
    expect(failLoad(l, 'boom')).toMatchObject({ status: 'error', error: 'boom', entries: l.entries })
  })
  it('inserts a live record at the top, once, only if it matches', () => {
    const l = loaded([entry(2), entry(1)])
    expect(seqs(appendLive(l, entry(3), {}, true))).toEqual([3, 2, 1])
    expect(appendLive(l, entry(2), {}, true)).toBe(l)
    expect(appendLive(l, entry(3, { kind: 'config' }), { kinds: ['exec'] }, true)).toBe(l)
  })
  it('prepends an in-order live record without re-sorting, and still merges one out of order', () => {
    let l = loaded([entry(5), entry(3)])
    l = appendLive(l, entry(6), {}, true)
    expect(seqs(l)).toEqual([6, 5, 3])
    l = appendLive(l, entry(4), {}, true) // out of order: merged
    expect(seqs(l)).toEqual([6, 5, 4, 3])
    l = appendLive(l, entry(8), {}, false)
    l = appendLive(l, entry(7), {}, false) // below held[0], above entries[0]
    expect(l.held.map((e) => e.seq)).toEqual([8, 7])
    expect(appendLive(l, entry(8), {}, true)).toBe(l)
  })
  it('holds live records as "N new" while scrolled away, then releases them', () => {
    let l = loaded([entry(1)])
    l = appendLive(l, entry(2), {}, false)
    l = appendLive(l, entry(3), {}, false)
    l = appendLive(l, entry(3), {}, false)
    expect(seqs(l)).toEqual([1])
    expect(l.held.map((e) => e.seq)).toEqual([3, 2])
    l = releaseHeld(l)
    expect(seqs(l)).toEqual([3, 2, 1])
    expect(l.held).toEqual([])
    expect(releaseHeld(l)).toBe(l)
  })
  it('keeps a live record that arrives while a read is in flight', () => {
    let l = appendLive(startLoad(EMPTY), entry(5), {}, true)
    l = applyPage(l, { records: [entry(5), entry(4)], skipped: 0, path: '/p' })
    expect(seqs(l)).toEqual([5, 4])
  })
  it('is cleared on lock, and ignores live records until the next read', () => {
    const l = clearOnLock()
    expect(l).toMatchObject({ status: 'cleared', entries: [], held: [], skipped: 0, path: '' })
    expect(appendLive(l, entry(9), {}, true)).toBe(l)
    expect(appendLive(EMPTY, entry(9), {}, true)).toBe(EMPTY)
  })
  it('audit.read is on Electron main\'s relay whitelist', () => {
    expect(REQUEST_METHODS).toContain('audit.read')
  })
})

describe('matches a softLock record by its servers', () => {
  const r = { time: iso(T), kind: 'config', action: 'softLock', servers: ['box', 'db'] } as AuditRecord
  it('true for a listed host, false for another', () => {
    expect(matches(r, { server: 'box' })).toBe(true)
    expect(matches(r, { server: 'x' })).toBe(false)
  })
})

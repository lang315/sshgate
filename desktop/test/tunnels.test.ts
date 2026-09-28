import { describe, expect, it } from 'vitest'
import { applyState, formFor, parseTunnelForm, replayStates, runningCount, summary } from '../src/renderer/tunnels'
import type { TunnelView } from '../src/shared/protocol'

describe('summary', () => {
  it('describes each kind', () => {
    expect(summary({ id: 'a', kind: 'local', listenPort: 5433, targetHost: 'db', targetPort: 5432 })).toBe('L 127.0.0.1:5433 → db:5432')
    expect(summary({ id: 'a', kind: 'remote', listenPort: 8080, targetHost: 'localhost', targetPort: 3000 })).toBe('R server 127.0.0.1:8080 → localhost:3000')
    expect(summary({ id: 'a', kind: 'dynamic', listenPort: 1080 })).toBe('D 127.0.0.1:1080 SOCKS5')
    expect(summary({ id: 'a', kind: 'local', listenPort: 1, targetHost: '::1', targetPort: 2 })).toBe('L 127.0.0.1:1 → [::1]:2')
  })
})

describe('parseTunnelForm', () => {
  const base = { kind: 'local' as const, listenPort: '5433', targetHost: 'db', targetPort: '5432', label: '' }
  it('accepts a local tunnel and trims', () => {
    expect(parseTunnelForm({ ...base, targetHost: ' db ', label: ' pg ' }, '')).toEqual(
      { ok: true, tunnel: { id: '', kind: 'local', listenPort: 5433, targetHost: 'db', targetPort: 5432, label: 'pg' } })
  })
  it('drops the target for dynamic', () => {
    expect(parseTunnelForm({ ...base, kind: 'dynamic' }, 'x')).toEqual(
      { ok: true, tunnel: { id: 'x', kind: 'dynamic', listenPort: 5433, targetHost: '', targetPort: 0, label: '' } })
  })
  it.each([
    [{ listenPort: '0' }, 'Listen port must be 1-65535'],
    [{ listenPort: '5x' }, 'Listen port must be 1-65535'],
    [{ targetHost: '' }, 'Target host is required, with no spaces or control characters'],
    [{ targetHost: 'a b' }, 'Target host is required, with no spaces or control characters'],
    [{ targetHost: 'db\u0007' }, 'Target host is required, with no spaces or control characters'],
    [{ targetHost: 'd\u200bb' }, 'Target host is required, with no spaces or control characters'],
    [{ targetPort: '70000' }, 'Target port must be 1-65535'],
    [{ label: 'x'.repeat(65) }, 'Label is at most 64 bytes, with no control characters'],
  ])('refuses %o', (patch, error) => {
    expect(parseTunnelForm({ ...base, ...patch }, '')).toEqual({ ok: false, error })
  })
  it('rejects labels with control characters', () => {
    expect(parseTunnelForm({ ...base, label: '\x07test' }, '')).toEqual(
      { ok: false, error: 'Label is at most 64 bytes, with no control characters' })
  })
  it('rejects labels with format runes', () => {
    expect(parseTunnelForm({ ...base, label: 'test​end' }, '')).toEqual(
      { ok: false, error: 'Label is at most 64 bytes, with no control characters' })
  })
  it('accepts 22 UTF-8 é characters (44 bytes)', () => {
    const label = 'é'.repeat(22)
    expect(new TextEncoder().encode(label).length).toBe(44)
    expect(parseTunnelForm({ ...base, label }, '')).toEqual(
      { ok: true, tunnel: { id: '', kind: 'local', listenPort: 5433, targetHost: 'db', targetPort: 5432, label } })
  })
  it('rejects 33 UTF-8 é characters (66 bytes)', () => {
    const label = 'é'.repeat(33)
    expect(new TextEncoder().encode(label).length).toBe(66)
    expect(parseTunnelForm({ ...base, label }, '')).toEqual(
      { ok: false, error: 'Label is at most 64 bytes, with no control characters' })
  })
  it('round-trips formFor', () => {
    expect(formFor({ id: 'i', kind: 'remote', listenPort: 1, targetHost: 'h', targetPort: 2, label: 'l' }))
      .toEqual({ kind: 'remote', listenPort: '1', targetHost: 'h', targetPort: '2', label: 'l' })
    expect(formFor()).toEqual({ kind: 'local', listenPort: '', targetHost: 'localhost', targetPort: '', label: '' })
  })
})

describe('state', () => {
  const list: TunnelView[] = [
    { server: 's', id: 'a', kind: 'dynamic', listenPort: 1, status: 'stopped', conns: 0 },
    { server: 's', id: 'b', kind: 'dynamic', listenPort: 2, status: 'running', conns: 1 },
    { server: 't', id: 'a', kind: 'dynamic', listenPort: 3, status: 'running', conns: 0 },
  ]
  it('applies a state to the matching row only', () => {
    const next = applyState(list, { server: 's', id: 'a', status: 'error', error: 'port 1 is already in use', conns: 0 })
    expect(next[0]).toMatchObject({ status: 'error', error: 'port 1 is already in use' })
    expect(next[2]).toBe(list[2])
    expect(applyState(list, { server: 'x', id: 'a', status: 'running', conns: 0 })).toBe(list)
  })
  it('clears a stale error on a new status', () => {
    const e = applyState(list, { server: 's', id: 'a', status: 'error', error: 'boom', conns: 0 })
    expect(applyState(e, { server: 's', id: 'a', status: 'running', conns: 0 })[0].error).toBeUndefined()
  })
  it('counts running per server', () => {
    expect(runningCount(list, 's')).toBe(1)
    expect(runningCount(list, 'u')).toBe(0)
  })
  it('replays buffered events onto a snapshot, newest per tunnel winning', () => {
    const snapshot: TunnelView[] = [{ server: 's', id: 'a', kind: 'dynamic', listenPort: 1, status: 'stopped', conns: 0 }]
    const next = replayStates(snapshot, [
      { server: 's', id: 'a', status: 'running', conns: 0 },
      { server: 's', id: 'a', status: 'running', conns: 2 },
    ])
    expect(next[0]).toMatchObject({ status: 'running', conns: 2 })
    expect(replayStates(snapshot, [{ server: 'x', id: 'a', status: 'running', conns: 0 }])).toBe(snapshot)
    expect(replayStates(snapshot, [])).toBe(snapshot)
  })
})

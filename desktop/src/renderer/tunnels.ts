import type { Tunnel, TunnelKind, TunnelState, TunnelView } from '../shared/protocol'

export interface TunnelForm { kind: TunnelKind; listenPort: string; targetHost: string; targetPort: string; label: string }

const hostPort = (h: string, p: number) => (h.includes(':') ? `[${h}]:${p}` : `${h}:${p}`)

export function summary(t: Tunnel): string {
  const listen = `127.0.0.1:${t.listenPort}`
  switch (t.kind) {
    case 'local': return `L ${listen} → ${hostPort(t.targetHost ?? '', t.targetPort ?? 0)}`
    case 'remote': return `R server ${listen} → ${hostPort(t.targetHost ?? '', t.targetPort ?? 0)}`
    default: return `D ${listen} SOCKS5`
  }
}

const port = (s: string) => (/^\d{1,5}$/.test(s.trim()) && +s >= 1 && +s <= 65535 ? +s : 0)

// Mirrors config.Tunnel.Validate; the hub checks again.
export function parseTunnelForm(f: TunnelForm, id: string): { ok: true; tunnel: Tunnel } | { ok: false; error: string } {
  const listenPort = port(f.listenPort)
  if (!listenPort) return { ok: false, error: 'Listen port must be 1-65535' }
  const label = f.label.trim()
  if (new TextEncoder().encode(label).length > 64 || /[\p{Cc}\p{Cf}]/u.test(label)) {
    return { ok: false, error: 'Label is at most 64 bytes, with no control characters' }
  }
  if (f.kind === 'dynamic') return { ok: true, tunnel: { id, kind: 'dynamic', listenPort, targetHost: '', targetPort: 0, label } }
  const targetHost = f.targetHost.trim()
  if (!targetHost || /[\s\p{Cc}\p{Cf}]/u.test(targetHost) || new TextEncoder().encode(targetHost).length > 253) {
    return { ok: false, error: 'Target host is required, with no spaces or control characters' }
  }
  const targetPort = port(f.targetPort)
  if (!targetPort) return { ok: false, error: 'Target port must be 1-65535' }
  return { ok: true, tunnel: { id, kind: f.kind, listenPort, targetHost, targetPort, label } }
}

export function formFor(t?: Tunnel): TunnelForm {
  if (!t) return { kind: 'local', listenPort: '', targetHost: 'localhost', targetPort: '', label: '' }
  return { kind: t.kind, listenPort: String(t.listenPort), targetHost: t.targetHost ?? '',
    targetPort: t.targetPort ? String(t.targetPort) : '', label: t.label ?? '' }
}

export function applyState(list: TunnelView[], s: TunnelState): TunnelView[] {
  const i = list.findIndex((t) => t.server === s.server && t.id === s.id)
  if (i < 0) return list
  const next = list.slice()
  next[i] = { ...list[i], status: s.status, error: s.error, conns: s.conns }
  return next
}

export const runningCount = (list: TunnelView[], server: string) =>
  list.filter((t) => t.server === server && t.status === 'running').length

// Replays events buffered while a tunnels.list request was in flight onto its
// reply, so a stale snapshot never overwrites a newer status.
export const replayStates = (list: TunnelView[], events: TunnelState[]) => events.reduce(applyState, list)

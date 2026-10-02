import type { AuditEntry, AuditKind, AuditOutcome, AuditPage, AuditQuery, AuditRecord } from '../shared/protocol'

export type Chip = 'exec' | 'auto' | 'denied' | 'config' | 'files' | 'tunnels'

// The filter chips, in display order. A kind chip adds that record kind;
// Auto and Denied add those exec outcomes. The hub ORs kinds and outcomes,
// so the chips that are on form a union, and none on means every record.
export const CHIPS: { id: Chip; label: string; kind?: AuditKind; outcome?: AuditOutcome }[] = [
  { id: 'exec', label: 'Exec', kind: 'exec' },
  { id: 'auto', label: 'Auto', outcome: 'auto' },
  { id: 'denied', label: 'Denied', outcome: 'denied' },
  { id: 'config', label: 'Config', kind: 'config' },
  { id: 'files', label: 'Files', kind: 'file' },
  { id: 'tunnels', label: 'Tunnels', kind: 'tunnel' },
]

export interface Filters { server: string; chips: Chip[]; text: string }
export const NO_FILTERS: Filters = { server: '', chips: [], text: '' }

export const toggleChip = (chips: Chip[], c: Chip): Chip[] =>
  chips.includes(c) ? chips.filter((x) => x !== c) : [...chips, c]

export function toQuery(f: Filters): AuditQuery {
  const q: AuditQuery = {}
  if (f.server) q.server = f.server
  const on = CHIPS.filter((c) => f.chips.includes(c.id))
  const kinds = on.flatMap((c) => (c.kind ? [c.kind] : []))
  const outcomes = on.flatMap((c) => (c.outcome ? [c.outcome] : []))
  if (kinds.length) q.kinds = kinds
  if (outcomes.length) q.outcomes = outcomes
  const text = f.text.trim()
  if (text) q.text = text
  return q
}

// broker's fields.outcomeIs, for records that arrive by audit.appended.
function outcomeIs(r: AuditRecord, o: AuditOutcome): boolean {
  switch (o) {
    case 'auto': return r.approval === 'auto'
    case 'allowed': return r.outcome === 'allowed' && r.approval !== 'auto'
    case 'cancelled': return r.outcome === 'approved_but_cancelled' || r.outcome === 'cancelled_running'
    default: return r.outcome === o
  }
}

// What a text search sees: every string and number value of the record,
// walked recursively and joined with "\n", like the hub's leaves(). Never a
// key name or an escape.
function leaves(v: unknown, out: string[]): string[] {
  if (typeof v === 'string' || typeof v === 'number') out.push(String(v))
  else if (Array.isArray(v)) v.forEach((x) => leaves(x, out))
  else if (v && typeof v === 'object') Object.values(v).forEach((x) => leaves(x, out))
  return out
}

// Whether r belongs in a list read with q: broker's ReadQuery.match, mirrored.
export function matches(r: AuditRecord, q: AuditQuery): boolean {
  if (q.server && r.server !== q.server && !(Array.isArray(r.servers) && r.servers.includes(q.server))) return false
  if (q.text && !leaves(r, []).join('\n').toLowerCase().includes(q.text.toLowerCase())) return false
  if (!q.kinds?.length && !q.outcomes?.length) return true
  const kind: AuditKind = r.kind || 'exec'
  if (q.kinds?.includes(kind)) return true
  return kind === 'exec' && !!q.outcomes?.some((o) => outcomeIs(r, o))
}

// The file can be edited by hand: never trust a field's type.
const str = (v: unknown): string => (typeof v === 'string' ? v : '')
const pad = (n: number) => String(n).padStart(2, '0')

// HH:MM:SS local time, with the date in front when it is not today.
export function formatTime(iso: string, now: Date): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const t = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  return d.toDateString() === now.toDateString() ? t : `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${t}`
}

const formatWait = (ms: number) => (ms < 1000 ? `${ms} ms` : `${Math.round(ms / 1000)} s`)

// A timed grant's length (15m, 2h), from its record's time to its deadline.
function grantLength(r: AuditRecord): string {
  if (r.forever === true) return 'forever'
  const min = Math.round((new Date(str(r.until)).getTime() - new Date(str(r.time)).getTime()) / 60_000)
  if (!Number.isFinite(min)) return ''
  return min >= 60 && min % 60 === 0 ? `${min / 60}h` : `${min}m`
}

export function maskedCount(r: AuditRecord): number {
  const red: unknown = r.redacted
  if (!red || typeof red !== 'object') return 0
  return Object.values(red).reduce<number>((n, v) => n + (typeof v === 'number' ? v : 0), 0)
}

export type Tone = 'ai' | 'auto' | 'danger' | 'wait' | 'plain'
export interface Badge { text: string; tone: Tone }
export interface RowView { time: string; host: string; badges: Badge[]; main: string; side: string[] }

const EXEC_BADGE: Record<string, Badge> = {
  allowed: { text: 'allowed', tone: 'ai' },
  denied: { text: 'denied', tone: 'danger' },
  expired: { text: 'expired', tone: 'wait' },
  sent_to_tab: { text: 'sent to tab', tone: 'plain' },
  approved_but_cancelled: { text: 'cancelled', tone: 'wait' },
  cancelled_running: { text: 'cancelled', tone: 'wait' },
  error: { text: 'error', tone: 'danger' },
}

// One table row: time, host, badges, the main text, and the side notes.
export function rowView(r: AuditRecord, now: Date): RowView {
  const base = { time: formatTime(str(r.time), now), host: str(r.server) }
  const list = (v: unknown): string[] => (Array.isArray(v) ? v.map(str) : [])
  if (!base.host) base.host = list(r.servers).join(', ')
  switch (r.kind) {
    case 'config': {
      const action = str(r.action)
      const changed = list(r.changed)
      const detail = action === 'autoAllowOn' ? grantLength(r)
        : action === 'softLock' ? list(r.servers).join(', ')
          : changed.length ? changed.join(', ')
            : str(r.reason) || str(r.fingerprint) || str(r.oldFingerprint)
      return { ...base, badges: [{ text: action, tone: 'plain' }], main: detail ? `${action} ${detail}` : action, side: [] }
    }
    case 'file': {
      const main = r.action === 'rename' ? `${str(r.from)} → ${str(r.to)}` : list(r.remote)[0] ?? ''
      return { ...base, badges: [{ text: str(r.action), tone: 'plain' }], main, side: r.phase ? [str(r.phase)] : [] }
    }
    case 'tunnel':
      return { ...base, badges: [{ text: str(r.phase), tone: 'plain' }],
        main: r.to ? `${str(r.listen)} → ${str(r.to)}` : `${str(r.listen)} SOCKS5`, side: [] }
  }
  const outcome = str(r.outcome)
  const auto = r.approval === 'auto'
  const badges: Badge[] = auto ? [{ text: 'auto', tone: 'auto' }] : []
  if (!auto || outcome !== 'allowed') badges.push(EXEC_BADGE[outcome] ?? { text: outcome, tone: 'plain' })
  if (r.sudo === true) badges.push({ text: 'sudo', tone: 'danger' })
  const side: string[] = []
  if (typeof r.exitCode === 'number') side.push(`exit ${r.exitCode}`)
  if (typeof r.waitMs === 'number' && r.waitMs > 0) side.push(`wait ${formatWait(r.waitMs)}`)
  const masked = maskedCount(r)
  if (masked > 0) side.push(`${masked} masked`)
  return { ...base, badges, main: str(r.command), side }
}

// The records the tab holds. 'cleared' follows a lock and never loads by
// itself; the view resets it to 'idle' when ready changes, and reads from
// 'idle' only when shown with ready.
export interface AuditList {
  status: 'idle' | 'loading' | 'loaded' | 'error' | 'cleared'
  entries: AuditEntry[] // newest first, one per seq
  held: AuditEntry[] // live records that arrived while scrolled away: "N new"
  next?: number
  skipped: number
  path: string
  error?: string
}

export const EMPTY: AuditList = { status: 'idle', entries: [], held: [], skipped: 0, path: '' }

export function mergeEntries(a: AuditEntry[], b: AuditEntry[]): AuditEntry[] {
  const bySeq = new Map<number, AuditEntry>()
  for (const e of [...a, ...b]) if (!bySeq.has(e.seq)) bySeq.set(e.seq, e)
  return [...bySeq.values()].sort((x, y) => y.seq - x.seq)
}

export const startLoad = (l: AuditList): AuditList => ({ ...EMPTY, status: 'loading', path: l.path })

export const applyPage = (l: AuditList, p: AuditPage): AuditList => ({
  ...l, status: 'loaded', entries: mergeEntries(l.entries, p.records), next: p.next, skipped: p.skipped, path: p.path, error: undefined,
})

export const failLoad = (l: AuditList, message: string): AuditList => ({ ...l, status: 'error', error: message })

// An audit.appended record: kept only while a list is loading or loaded, if
// it matches the query that list was read with and is not there yet; held
// while the table is scrolled away from the top, so rows never move under
// the cursor.
export function appendLive(l: AuditList, e: AuditEntry, q: AuditQuery, atTop: boolean): AuditList {
  if (l.status !== 'loading' && l.status !== 'loaded') return l
  if (!matches(e.record, q)) return l
  // The normal case is a seq above everything held: prepend, no sort. Out of
  // order falls back to the merge, which also dedupes.
  const fresh = e.seq > Math.max(l.entries[0]?.seq ?? 0, l.held[0]?.seq ?? 0)
  if (!fresh && (l.entries.some((x) => x.seq === e.seq) || l.held.some((x) => x.seq === e.seq))) return l
  const add = (list: AuditEntry[]) => (fresh ? [e, ...list] : mergeEntries(list, [e]))
  return atTop ? { ...l, entries: add(l.entries) } : { ...l, held: add(l.held) }
}

export const releaseHeld = (l: AuditList): AuditList =>
  l.held.length ? { ...l, entries: mergeEntries(l.entries, l.held), held: [] } : l

export const clearOnLock = (): AuditList => ({ ...EMPTY, status: 'cleared' })

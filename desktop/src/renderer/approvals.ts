import type { ApprovalRequest, HubEvent } from '../shared/protocol'

export interface PendingItem { request: ApprovalRequest; shownAt: number }
export const ALLOW_DELAY_MS = 500

export function seed(requests: ApprovalRequest[], now: number): PendingItem[] {
  return requests.map((request) => ({ request, shownAt: now }))
}

export function reduceApprovals(items: PendingItem[], e: HubEvent, now: number): PendingItem[] {
  if (e.method === 'pending') {
    if (items.some((i) => i.request.id === e.params.request.id)) return items
    return [...items, { request: e.params.request, shownAt: now }]
  }
  if (e.method === 'decided') return items.filter((i) => i.request.id !== e.params.request.id)
  return items
}

// Allow stays disabled for ALLOW_DELAY_MS after the item appeared and after the
// last change to the list (or the panel mounting), so nothing that shifts under
// the cursor is instantly clickable.
export function allowEnabled(item: PendingItem, now: number, listChangedAt = 0): boolean {
  return now - Math.max(item.shownAt, listChangedAt) >= ALLOW_DELAY_MS
}

// When the approval list last changed, for allowEnabled's listChangedAt: its ids
// changed, or its rendered height did (e.g. inline error text appearing in an item
// above shifts every item below it). The panel mounting counts as a change.
export class ListChanges {
  private height?: number
  constructor(private key: string, public at: number) {}
  setKey(key: string, now: number): void {
    if (key !== this.key) { this.key = key; this.at = now }
  }
  // Returns whether the height changed; the first measurement is not a change.
  setHeight(height: number, now: number): boolean {
    const changed = this.height !== undefined && height !== this.height
    this.height = height
    if (changed) this.at = now
    return changed
  }
  // Something moved the items without changing ids or height (scrolling the list).
  touch(now: number): void { this.at = now }
}

// Merges a hub.pending() snapshot with items already known from live events.
// decidedSince and pendingSince hold ids from events that arrived after the call
// started: a 'decided' event that overtakes the reply doesn't resurrect a ghost, a
// 'pending' event that overtakes it isn't hidden, and any other current item the
// snapshot lacks is stale and dropped.
export function mergeSeed(current: PendingItem[], snapshot: ApprovalRequest[], decidedSince: Set<string>, pendingSince: Set<string>, now: number): PendingItem[] {
  const byId = new Map(current.map((i) => [i.request.id, i]))
  const seen = new Set<string>()
  const out: PendingItem[] = []
  for (const request of snapshot) {
    if (decidedSince.has(request.id)) continue
    seen.add(request.id)
    const existing = byId.get(request.id)
    out.push({ request, shownAt: existing ? existing.shownAt : now })
  }
  for (const item of current) {
    if (seen.has(item.request.id) || decidedSince.has(item.request.id) || !pendingSince.has(item.request.id)) continue
    out.push(item)
  }
  return out
}

// Generation counter: replies from superseded calls are ignored.
export class Latest {
  private n = 0
  next(): number { return ++this.n }
  isCurrent(gen: number): boolean { return gen === this.n }
}

export function blockKeyboardActivation(e: { key: string; preventDefault: () => void }): void {
  if (e.key === 'Enter' || e.key === ' ') e.preventDefault()
}

export type Segment = { text: string; nonAscii: boolean }

export const isNonAscii = (cp: number) => cp > 0x7e || cp < 0x20

export function highlightNonAscii(s: string, plain = ''): Segment[] {
  const out: Segment[] = []
  for (const ch of s) {
    const nonAscii = isNonAscii(ch.codePointAt(0)!) && !plain.includes(ch)
    const last = out[out.length - 1]
    if (last && last.nonAscii === nonAscii) last.text += ch
    else out.push({ text: ch, nonAscii })
  }
  return out
}

// "N non-ASCII characters highlighted (U+0456, …)": the code points make a
// homoglyph (Cyrillic і in "gіthub") visible even where the glyphs look identical.
export function nonAsciiSummary(s: string, plain = ''): string | undefined {
  const cps: number[] = []
  let n = 0
  for (const ch of s) {
    const cp = ch.codePointAt(0)!
    if (!isNonAscii(cp) || plain.includes(ch)) continue
    n++
    if (!cps.includes(cp)) cps.push(cp)
  }
  if (n === 0) return undefined
  const shown = cps.slice(0, 5).map((cp) => 'U+' + cp.toString(16).toUpperCase().padStart(4, '0'))
  if (cps.length > 5) shown.push(`+${cps.length - 5} more`)
  return `${n} non-ASCII character${n === 1 ? '' : 's'} highlighted (${shown.join(', ')})`
}

// Stdin is file content: newlines and tabs are plain, a carriage return is
// shown as ␍ so it cannot hide text behind it.
export const stdinView = (s: string): Segment[] => highlightNonAscii(s.replaceAll('\r', '␍'), '\n\t')

export function stdinMeta(s: string): string {
  const bytes = new TextEncoder().encode(s).length
  const lines = s === '' ? 0 : s.split('\n').length - (s.endsWith('\n') ? 1 : 0)
  return `${bytes} byte${bytes === 1 ? '' : 's'}, ${lines} line${lines === 1 ? '' : 's'}`
}

// Click-time re-check for Allow/Send to tab: React may not have committed the
// disabled state yet right after a scroll/resize touches listChangedAt, so the
// click handler re-verifies at the moment of the click, not just at last render.
export function clickAllowed(item: PendingItem, now: number, listChangedAt: number, busy: boolean): boolean {
  return !busy && allowEnabled(item, now, listChangedAt)
}

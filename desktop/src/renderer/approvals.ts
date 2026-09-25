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

export function highlightNonAscii(s: string): Segment[] {
  const out: Segment[] = []
  for (const ch of s) {
    const nonAscii = ch.codePointAt(0)! > 0x7e || ch.codePointAt(0)! < 0x20
    const last = out[out.length - 1]
    if (last && last.nonAscii === nonAscii) last.text += ch
    else out.push({ text: ch, nonAscii })
  }
  return out
}

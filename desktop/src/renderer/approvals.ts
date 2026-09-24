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

// Merges a hub.pending() snapshot with items already known from live events,
// so a 'decided' event that overtakes the snapshot reply doesn't resurrect a
// ghost, and a 'pending' event that overtakes it isn't hidden by the stale
// snapshot overwriting the list.
export function mergeSeed(current: PendingItem[], snapshot: ApprovalRequest[], decidedSince: Set<string>, now: number): PendingItem[] {
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
    if (seen.has(item.request.id) || decidedSince.has(item.request.id)) continue
    out.push(item)
  }
  return out
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

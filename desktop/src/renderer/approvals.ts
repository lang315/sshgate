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

export function allowEnabled(item: PendingItem, now: number): boolean {
  return now - item.shownAt >= ALLOW_DELAY_MS
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

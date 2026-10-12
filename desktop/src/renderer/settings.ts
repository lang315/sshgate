import type { SoftLockLimit } from '../shared/protocol'
import { ALLOW_DELAY_MS } from './approvals'

// The Settings dialog's rules (spec 2026-10-12-soft-lock-limit-design.md,
// "UI and UX"). Pure: the dialog only renders what these return.

export const LIMITS: { value: SoftLockLimit; label: string }[] = [
  { value: '24h', label: '24 hours (default)' }, { value: '3d', label: '3 days' },
  { value: '7d', label: '7 days' }, { value: 'none', label: 'No limit' },
]
const RANK: Record<SoftLockLimit, number> = { '24h': 1, '3d': 3, '7d': 7, none: Infinity }
const PHRASE: Record<Exclude<SoftLockLimit, 'none'>, string> = { '24h': '24 hours', '3d': '3 days', '7d': '7 days' }

export type LimitChange = 'same' | 'lower' | 'higher' | 'none'

export function limitChange(current: SoftLockLimit, chosen: SoftLockLimit): LimitChange {
  if (chosen === current) return 'same'
  if (chosen === 'none') return 'none'
  return RANK[chosen] > RANK[current] ? 'higher' : 'lower'
}

export const limitHelp = (l: SoftLockLimit) => l === 'none'
  ? 'Auto-allow stops only with Stop auto-allow and lock, a restart or a crash.'
  : 'Then the app locks fully and auto-allow stops. Forever grants wait for Resume.'

export const limitNotice = (l: SoftLockLimit) =>
  `While the app is locked, the AI keeps running on hosts with auto-allow ${l === 'none' ? 'for as long as the app runs' : `for up to ${PHRASE[l]}`}. Any program running as your user can use that access too.`

// Anything that moves Save restarts its delay: the choice (it swaps the
// notice), the host list in the notice, and an error line appearing.
export const settingsChangeKey = (chosen: SoftLockLimit, hosts: string[], error?: string) => JSON.stringify([chosen, hosts, error ?? null])

export function saveAllowed(o: { change: LimitChange; busy: boolean; openedAt: number; changedAt: number; now: number }): boolean {
  if (o.change === 'same' || o.busy) return false
  return o.now - Math.max(o.openedAt, o.changedAt) >= ALLOW_DELAY_MS
}

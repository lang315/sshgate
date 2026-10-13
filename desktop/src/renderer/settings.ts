import type { SoftLockLimit } from '../shared/protocol'
import { delayElapsed } from './approvals'

// The Settings dialog's rules (spec 2026-10-12-soft-lock-limit-design.md,
// "UI and UX"). Pure: the dialog only renders what these return.

export const LIMITS: { value: SoftLockLimit; label: string }[] = [
  { value: '24h', label: '24 hours (default)' }, { value: '3d', label: '3 days' },
  { value: '7d', label: '7 days' }, { value: 'none', label: 'No limit' },
]
// How a limit reads in a sentence ("up to 3 days", "the 3-day limit"); No limit has none.
const FIXED: Record<Exclude<SoftLockLimit, 'none'>, { phrase: string; adj: string }> = {
  '24h': { phrase: '24 hours', adj: '24-hour' }, '3d': { phrase: '3 days', adj: '3-day' }, '7d': { phrase: '7 days', adj: '7-day' },
}
const rank = (l: SoftLockLimit) => LIMITS.findIndex((x) => x.value === l)

export type LimitChange = 'same' | 'lower' | 'higher' | 'none'

export function limitChange(current: SoftLockLimit, chosen: SoftLockLimit): LimitChange {
  if (chosen === current) return 'same'
  if (chosen === 'none') return 'none'
  return rank(chosen) > rank(current) ? 'higher' : 'lower'
}

export const limitHelp = (l: SoftLockLimit) => l === 'none'
  ? 'Auto-allow stops only with Stop auto-allow and lock, a restart or a crash.'
  : 'Then the app locks fully and auto-allow stops. Forever grants wait for Resume.'

export const limitNotice = (l: SoftLockLimit) =>
  `While the app is locked, the AI keeps running on hosts with auto-allow ${l === 'none' ? 'for as long as the app runs' : `for up to ${FIXED[l].phrase}`}. Any program running as your user can use that access too.`

// Anything that moves Save restarts its delay: the choice (it swaps the
// notice), the host list in the notice, an error line appearing, and the
// Touch ID password field appearing or going (touchOn: Touch ID being turned
// on; turning it off shows no field).
export const settingsChangeKey = (chosen: SoftLockLimit, hosts: string[], error?: string, touchOn = false) =>
  JSON.stringify([chosen, hosts, error ?? null, touchOn])

export type TouchChange = 'same' | 'on' | 'off'
export const touchChange = (enabled: boolean, chosen: boolean): TouchChange => (chosen === enabled ? 'same' : chosen ? 'on' : 'off')

export function saveAllowed(o: { change: LimitChange; touch?: TouchChange; password?: string; busy: boolean; openedAt: number; changedAt: number; now: number }): boolean {
  const touch = o.touch ?? 'same'
  if (o.busy || (o.change === 'same' && touch === 'same')) return false
  if (touch === 'on' && !o.password) return false
  return delayElapsed(o.openedAt, o.changedAt, o.now)
}

// Touch ID first: when it fails the limit is not saved, and the dialog stays
// open with the error (spec 2026-10-12-touchid-unlock-design.md).
export async function saveSettings(
  o: { change: LimitChange; limit: SoftLockLimit; touch: TouchChange; password: string },
  api: { enable(password: string): Promise<void>; disable(): Promise<void>; setLimit(l: SoftLockLimit): Promise<void> },
): Promise<void> {
  if (o.touch === 'on') await api.enable(o.password)
  if (o.touch === 'off') await api.disable()
  if (o.change !== 'same') await api.setLimit(o.limit)
}

// The forever grant's paragraph in AutoAllowDialog, from the vault's soft-lock
// limit (status.softLockLimit; absent reads as the 24-hour default).
export function limitText(limit: SoftLockLimit | undefined): string {
  const l = limit ?? '24h'
  const keep = l === 'none'
    ? 'the AI keeps running on this host while the app is locked, with no time limit'
    : `the AI keeps running on this host for up to ${FIXED[l].phrase}`
  const after = l === 'none'
    ? 'After a restart or Stop auto-allow and lock'
    : `After a restart, the ${FIXED[l].adj} limit, or Stop auto-allow and lock`
  return `Stays set after restart. Locking the app, by hand or from inactivity, does not stop it: ${keep}. To stop it, use Stop auto-allow and lock on the unlock screen, Stop on the host card, or Stop all auto-allow. ${after}, it waits for you to click Resume.`
}

import type { HubState } from '../shared/protocol'

export type Screen =
  | { kind: 'hub'; state: HubState }
  | { kind: 'create-vault' }
  | { kind: 'locked'; error?: string }
  | { kind: 'ready' }

// A store without a master password (none yet, or key/agent-only) can only
// go forward by creating a vault: every write from the app needs one.
export function screenFor(
  hub: HubState,
  status?: { locked: boolean; hasVault: boolean },
  unlockError?: string,
): Screen {
  if (hub.kind !== 'running') return { kind: 'hub', state: hub }
  if (!status) return { kind: 'hub', state: { kind: 'starting' } }
  if (!status.hasVault) return { kind: 'create-vault' }
  if (status.locked) return unlockError ? { kind: 'locked', error: unlockError } : { kind: 'locked' }
  return { kind: 'ready' }
}

// The unlock screen's wording. grantsEnded and softLockLimit only arrive while
// a soft lock is on, which an idle or manual lock began: the stored reason
// stays. With none (the soft lock's own note was dropped as stale) it reads as
// manual, which claims no inactivity, rather than guessing idle.
export const lockKind = (reason: string | undefined, previous?: 'idle' | 'manual'): 'idle' | 'manual' =>
  reason === 'idle' ? 'idle'
    : reason === 'grantsEnded' || reason === 'softLockLimit' ? previous ?? 'manual'
    : 'manual'

// The Lock button's tooltip: with a host on auto-allow, Lock soft-locks and
// the AI keeps running there (spec 2026-10-03).
export const lockTitle = (autoHosts: number): string | undefined =>
  autoHosts > 0 ? 'Auto-allow keeps running while locked' : undefined

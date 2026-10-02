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

// The unlock screen's wording: every lock that followed inactivity reads as
// idle, including a soft lock that later hardened.
export const lockKind = (reason: string | undefined): 'idle' | 'manual' =>
  reason === 'idle' || reason === 'grantsEnded' || reason === 'softLockLimit' ? 'idle' : 'manual'

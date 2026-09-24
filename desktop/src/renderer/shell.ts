import type { HubState } from '../shared/protocol'

export type Screen =
  | { kind: 'hub'; state: HubState }
  | { kind: 'no-store' }
  | { kind: 'locked'; error?: string }
  | { kind: 'ready' }

export function screenFor(
  hub: HubState,
  status?: { locked: boolean; hasStore: boolean },
  unlockError?: string,
): Screen {
  if (hub.kind !== 'running') return { kind: 'hub', state: hub }
  if (!status) return { kind: 'hub', state: { kind: 'starting' } }
  if (!status.hasStore) return { kind: 'no-store' }
  if (status.locked) return unlockError ? { kind: 'locked', error: unlockError } : { kind: 'locked' }
  return { kind: 'ready' }
}

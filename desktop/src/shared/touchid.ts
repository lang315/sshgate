import type { RanWhileLocked } from './protocol'

// Unlock with Touch ID (spec 2026-10-12-touchid-unlock-design.md): what main's
// touchid:* IPC answers. supported: a helper exists; available: Touch ID can
// be used now; enabled: touchid.json holds a password for the current vault.
export interface TouchStatus { supported: boolean; available: boolean; enabled: boolean }
export type TouchUnlockResult = { cancelled: true } | { ranWhileLocked?: RanWhileLocked[] }

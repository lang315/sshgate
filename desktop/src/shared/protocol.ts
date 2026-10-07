export type HubState =
  | { kind: 'starting' }
  | { kind: 'running' }
  | { kind: 'restarting'; attempt: number; inMs: number }
  | { kind: 'failed'; message: string; stderr: string }

export type Outcome = 'allowed' | 'denied' | 'expired' | 'withdrawn' | 'sent_to_tab'

export interface ApprovalRequest {
  id: string; client: string; server: string; target: string; command: string
  description: string; sudo: boolean; timeoutSec: number; receivedAt: string; stdin?: string
}

export type AutoAllowMode = 'off' | '15m' | '30m' | '60m' | '2h' | '4h' | 'forever'
export interface AutoAllowState { until?: string; forever?: boolean; paused?: boolean }

export interface ServerInfo {
  name: string; host: string; port: number; user: string; auth: string; keyPath: string
  hostKey: string; hostKeyAlgo: string; aiVisible: boolean; locked: boolean
  hasPassword: boolean; hasSuPassword: boolean; hasSudoPassword: boolean; hasKeyPassphrase: boolean
  autoAllow?: AutoAllowState; autoAllowRefused?: string; autoAllowRoot: boolean; autoAllowSudo: boolean
}

export interface AutoAllowRan {
  server: string; command: string; truncated?: number; description: string
  exitCode?: number; error?: string; time: string; sudo?: boolean; stdinBytes?: number
}
export interface AutoAllowCheck { uid: number; passwordlessSudo: boolean }

export type SecretField = 'password' | 'suPassword' | 'sudoPassword' | 'keyPassphrase'

// Secrets are write-only: an omitted one is kept, '' clears it.
export interface ServerInput {
  name: string; host: string; port: number; user: string; auth: string; keyPath: string; aiVisible: boolean
  autoAllowRoot: boolean; autoAllowSudo: boolean
  password?: string; suPassword?: string; sudoPassword?: string; keyPassphrase?: string
}

// storeError is set while the hub's last reload of the vault file was
// refused (tampered or unreadable); the hub keeps the last good copy.
// autoHosts is set only while soft-locked: the UI is locked, the AI still runs on these hosts.
export interface Status { locked: boolean; hasStore: boolean; hasVault: boolean; storePath: string; pending: number; storeError?: string; autoHosts?: string[] }
// Why the hub locked. grantsEnded and softLockLimit end a soft lock.
export type LockReason = 'idle' | 'manual' | 'grantsEnded' | 'softLockLimit'
// Auto runs that finished on a server while the UI was locked; in the unlock reply.
export interface RanWhileLocked { server: string; count: number }

export interface HostKeyUnknown {
  status: 'hostKeyUnknown'; server: string; host: string; port: number; user: string
  fingerprint: string; keyType: string; knownHosts: 'match' | 'different' | 'absent'
}

export interface HostKeyMismatch {
  status: 'hostKeyMismatch'; server: string; host: string; port: number; user: string
  pinned: string; presented: string
}

export type TermOpenResult = { status: 'open' } | HostKeyUnknown | HostKeyMismatch

// Import from ~/.ssh/config. The renderer sends only alias names back; the
// hub recomputes host, user, key path and pin itself.
export interface ImportCandidate {
  alias: string; host: string; port: number; user: string; auth: string; keyPath?: string
  needsPassphrase?: boolean; hostKey?: string; hostKeyAlgo?: string
  status: 'ready' | 'exists' | 'skipped'; reason?: string
}
export interface ImportScan { candidates: ImportCandidate[]; note?: string }
export interface ImportResult { imported: string[]; skipped: { alias: string; reason: string }[] }

// Files (slice 3a). Local paths never appear here: the renderer only holds
// tokens from Electron main (FileGrant); main swaps them for paths.
export interface FileEntry { name: string; size: number; mode: number; mtime: number; kind: 'dir' | 'file' | 'link' | 'other'; target?: string }
export interface FileListing { status: 'listed'; path: string; entries: FileEntry[]; truncated: boolean; bad: number }
export type FilesListResult = FileListing | HostKeyUnknown | HostKeyMismatch
export type FileOp = 'upload' | 'download' | 'delete'
export interface FilesPlanned {
  id: string; files: number; dirs: number; links: number; bytes: number
  conflicts: { count: number; sample: string[] }; errorCount: number; errors: string[]
}
export interface FilesProgress { id: string; file: string; done: number; total: number }
export interface FilesDone {
  id: string; op: FileOp; copied: number; skipped: number; deleted: number; bytes: number
  errorCount: number; errors: string[]; cancelled: boolean; reason: string
}
export interface FileGrant { token: string; name: string }

export type TunnelKind = 'local' | 'remote' | 'dynamic'
export interface Tunnel { id: string; kind: TunnelKind; listenPort: number; targetHost?: string; targetPort?: number; label?: string }
export type TunnelStatus = 'stopped' | 'starting' | 'running' | 'error'
export interface TunnelState { server: string; id: string; status: TunnelStatus; error?: string; conns: number }
export type TunnelView = Tunnel & TunnelState

// Audit log (slice 4b). A record is one audit.jsonl line as stored; exec
// records have no kind. Every field but time is optional per kind, and the
// renderer must not trust field types (the file can be edited by hand).
export type AuditKind = 'exec' | 'config' | 'file' | 'tunnel'
export type AuditOutcome = 'allowed' | 'auto' | 'denied' | 'expired' | 'cancelled' | 'error'
export interface AuditQuery { before?: number; limit?: number; server?: string; kinds?: AuditKind[]; outcomes?: AuditOutcome[]; text?: string }
export interface AuditRecord {
  time: string; kind?: 'config' | 'file' | 'tunnel'; server?: string; reason?: string
  // exec
  client?: string; command?: string; stdin?: string; description?: string; sudo?: boolean; timeoutSec?: number; outcome?: string
  exitCode?: number; durationMs?: number; approval?: string; waitMs?: number; redacted?: Record<string, number>
  // config
  action?: string; changed?: string[]; servers?: string[]; until?: string; forever?: boolean; fingerprint?: string; oldFingerprint?: string
  // file (action too)
  phase?: string; remote?: string[]; from?: string; to?: string
  // tunnel (phase and to too)
  listen?: string; tunnelKind?: string; target?: string; id?: string
}
export interface AuditEntry { seq: number; record: AuditRecord }
export interface AuditPage { records: AuditEntry[]; next?: number; skipped: number; path: string }

export type HubEvent =
  | { method: 'pending'; params: { request: ApprovalRequest } }
  | { method: 'decided'; params: { request: ApprovalRequest; decision: { outcome: Outcome; reason: string } } }
  | { method: 'locked'; params: { reason: LockReason } }
  | { method: 'term.data'; params: { id: string; data: string } }
  | { method: 'term.exit'; params: { id: string; code: number; reason: string } }
  | { method: 'term.dropped'; params: { id: string; bytes: number } }
  | { method: 'files.planned'; params: FilesPlanned }
  | { method: 'files.progress'; params: FilesProgress }
  | { method: 'files.done'; params: FilesDone }
  | { method: 'tunnels.state'; params: TunnelState }
  | { method: 'autoAllow.ran'; params: AutoAllowRan }
  | { method: 'autoAllow.off'; params: { server: string; reason: string } }
  | { method: 'audit.appended'; params: AuditEntry }

export const REQUEST_METHODS = ['hello', 'status', 'unlock', 'lock', 'servers', 'pending',
  'decide', 'denyAll', 'term.open', 'term.close',
  'vault.create', 'servers.save', 'servers.delete', 'servers.forgetHostKey',
  'import.scan', 'import.apply', 'files.list', 'files.mkdir', 'files.rename',
  'tunnels.list', 'tunnels.save', 'tunnels.delete', 'tunnels.start',
  'servers.setAutoAllow', 'servers.autoAllowCheck', 'audit.read'] as const
export type RequestMethod = (typeof REQUEST_METHODS)[number]
// Relayed through Electron main's FilesRelay (tokens → paths, native download
// conflicts), never straight through relayCall.
export const FILES_RELAYED = ['files.plan', 'files.run'] as const
export const NOTIFY_METHODS = ['term.write', 'term.ack', 'term.resize', 'files.cancel', 'tunnels.stop'] as const
export type NotifyMethod = (typeof NOTIFY_METHODS)[number]
export const PROTOCOL_VERSION = 11

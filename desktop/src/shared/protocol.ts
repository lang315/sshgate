export type HubState =
  | { kind: 'starting' }
  | { kind: 'running' }
  | { kind: 'restarting'; attempt: number; inMs: number }
  | { kind: 'failed'; message: string; stderr: string }

export type Outcome = 'allowed' | 'denied' | 'expired' | 'withdrawn' | 'sent_to_tab'

export interface ApprovalRequest {
  id: string; client: string; server: string; target: string; command: string
  description: string; sudo: boolean; timeoutSec: number; receivedAt: string
}

export interface ServerInfo {
  name: string; host: string; port: number; user: string; auth: string; keyPath: string
  hostKey: string; hostKeyAlgo: string; aiVisible: boolean; locked: boolean
  hasPassword: boolean; hasSuPassword: boolean; hasSudoPassword: boolean; hasKeyPassphrase: boolean
}

export type SecretField = 'password' | 'suPassword' | 'sudoPassword' | 'keyPassphrase'

// Secrets are write-only: an omitted one is kept, '' clears it.
export interface ServerInput {
  name: string; host: string; port: number; user: string; auth: string; keyPath: string; aiVisible: boolean
  password?: string; suPassword?: string; sudoPassword?: string; keyPassphrase?: string
}

// storeError is set while the hub's last reload of the vault file was
// refused (tampered or unreadable); the hub keeps the last good copy.
export interface Status { locked: boolean; hasStore: boolean; hasVault: boolean; storePath: string; pending: number; storeError?: string }

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

export type HubEvent =
  | { method: 'pending'; params: { request: ApprovalRequest } }
  | { method: 'decided'; params: { request: ApprovalRequest; decision: { outcome: Outcome; reason: string } } }
  | { method: 'locked'; params: { reason: 'idle' | 'manual' } }
  | { method: 'term.data'; params: { id: string; data: string } }
  | { method: 'term.exit'; params: { id: string; code: number; reason: string } }
  | { method: 'term.dropped'; params: { id: string; bytes: number } }
  | { method: 'files.planned'; params: FilesPlanned }
  | { method: 'files.progress'; params: FilesProgress }
  | { method: 'files.done'; params: FilesDone }

export const REQUEST_METHODS = ['hello', 'status', 'unlock', 'lock', 'servers', 'pending',
  'decide', 'denyAll', 'term.open', 'term.close',
  'vault.create', 'servers.save', 'servers.delete', 'servers.forgetHostKey',
  'import.scan', 'import.apply', 'files.list', 'files.mkdir', 'files.rename'] as const
export type RequestMethod = (typeof REQUEST_METHODS)[number]
// Relayed through Electron main's FilesRelay (tokens → paths, native download
// conflicts), never straight through relayCall.
export const FILES_RELAYED = ['files.plan', 'files.run'] as const
export const NOTIFY_METHODS = ['term.write', 'term.ack', 'term.resize', 'files.cancel'] as const
export type NotifyMethod = (typeof NOTIFY_METHODS)[number]
export const PROTOCOL_VERSION = 4

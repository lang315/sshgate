export type HubState =
  | { kind: 'starting' }
  | { kind: 'running' }
  | { kind: 'restarting'; attempt: number; inMs: number }
  | { kind: 'failed'; message: string; stderr: string }

export type Outcome = 'allowed' | 'denied' | 'expired' | 'withdrawn' | 'sent_to_tab'

export interface ApprovalRequest {
  id: string; client: string; server: string; command: string
  description: string; sudo: boolean; timeoutSec: number; receivedAt: string
}

export interface ServerInfo {
  name: string; host: string; port: number; user: string; auth: string
  hostKey: string; aiVisible: boolean; locked: boolean
}

export type HubEvent =
  | { method: 'pending'; params: { request: ApprovalRequest } }
  | { method: 'decided'; params: { request: ApprovalRequest; decision: { outcome: Outcome; reason: string } } }
  | { method: 'locked'; params: { reason: 'idle' | 'manual' } }
  | { method: 'term.data'; params: { id: string; data: string } }
  | { method: 'term.exit'; params: { id: string; code: number; reason: string } }
  | { method: 'term.dropped'; params: { id: string; bytes: number } }

export const REQUEST_METHODS = ['hello', 'status', 'unlock', 'lock', 'servers', 'pending',
  'decide', 'denyAll', 'term.open', 'term.close'] as const
export type RequestMethod = (typeof REQUEST_METHODS)[number]
export const NOTIFY_METHODS = ['term.write', 'term.ack', 'term.resize'] as const
export type NotifyMethod = (typeof NOTIFY_METHODS)[number]
export const PROTOCOL_VERSION = 1

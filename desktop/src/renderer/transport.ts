import type { ApprovalRequest, HubEvent, HubState, ServerInfo } from '../shared/protocol'

interface Bridge {
  call(method: string, params?: unknown): Promise<unknown>
  notify(method: string, params?: unknown): void
  getState(): Promise<HubState>
  onEvent(cb: (e: HubEvent) => void): () => void
  onState(cb: (s: HubState) => void): () => void
}

const bridge = (): Bridge => (window as unknown as { sshmcp: Bridge }).sshmcp

// Electron prefixes invoke errors with "Error invoking remote method 'hub:call': Error: ".
export function cleanError(e: unknown): Error {
  const msg = e instanceof Error ? e.message : String(e)
  return new Error(msg.replace(/^Error invoking remote method '[^']+': (Error: )?/, ''))
}

async function call<T>(method: string, params?: unknown): Promise<T> {
  try {
    return (await bridge().call(method, params)) as T
  } catch (e) {
    throw cleanError(e)
  }
}

export function toBase64(b: Uint8Array): string {
  let s = ''
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000))
  return btoa(s)
}

export function fromBase64(s: string): Uint8Array {
  const bin = atob(s)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export const hub = {
  hello: () => call<{ protocol: number }>('hello'),
  status: () => call<{ locked: boolean; hasStore: boolean; pending: number }>('status'),
  unlock: async (password: string) => { await call('unlock', { password }) },
  lock: async () => { await call('lock') },
  servers: () => call<ServerInfo[]>('servers'),
  pending: () => call<ApprovalRequest[]>('pending'),
  decide: async (id: string, outcome: 'allowed' | 'denied' | 'sent_to_tab', reason = '') => {
    await call('decide', { id, outcome, reason })
  },
  denyAll: async (reason = '') => { await call('denyAll', { reason }) },
  termOpen: async (id: string, server: string, rows: number, cols: number) => {
    await call('term.open', { id, server, rows, cols })
  },
  termClose: async (id: string) => { await call('term.close', { id }) },
  termWrite: (id: string, data: Uint8Array, user: boolean) => bridge().notify('term.write', { id, data: toBase64(data), user }),
  termAck: (id: string, n: number) => bridge().notify('term.ack', { id, n }),
  termResize: (id: string, rows: number, cols: number) => bridge().notify('term.resize', { id, rows, cols }),
  onEvent: (cb: (e: HubEvent) => void) => bridge().onEvent(cb),
  onState: (cb: (s: HubState) => void) => bridge().onState(cb),
  getState: () => bridge().getState(),
}

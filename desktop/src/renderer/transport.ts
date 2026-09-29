import type { ApprovalRequest, AutoAllowCheck, AutoAllowMode, FileGrant, FileOp, FilesListResult, HubEvent, HubState, ImportResult, ImportScan, ServerInfo, ServerInput, Status, TermOpenResult, Tunnel, TunnelView } from '../shared/protocol'

interface Bridge {
  call(method: string, params?: unknown): Promise<unknown>
  notify(method: string, params?: unknown): void
  getState(): Promise<HubState>
  onEvent(cb: (e: HubEvent) => void): () => void
  onState(cb: (s: HubState) => void): () => void
  pickUpload(server: string, folder: string, mode: 'files' | 'folder' | 'both'): Promise<FileGrant[]>
  pickDownloadDir(server: string): Promise<FileGrant | null>
  grantDropped(files: File[], server: string): Promise<FileGrant[]>
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
  status: () => call<Status>('status'),
  unlock: async (password: string) => { await call('unlock', { password }) },
  lock: async () => { await call('lock') },
  servers: () => call<ServerInfo[]>('servers'),
  pending: () => call<ApprovalRequest[]>('pending'),
  decide: async (id: string, outcome: 'allowed' | 'denied' | 'sent_to_tab', reason = '') => {
    await call('decide', { id, outcome, reason })
  },
  denyAll: async (reason = '') => { await call('denyAll', { reason }) },
  createVault: async (password: string) => { await call('vault.create', { password }) },
  saveServer: async (server: ServerInput, original?: string) => { await call('servers.save', { original, server }) },
  deleteServer: async (name: string) => { await call('servers.delete', { name }) },
  forgetHostKey: async (name: string) => { await call('servers.forgetHostKey', { name }) },
  importScan: () => call<ImportScan>('import.scan'),
  importApply: (aliases: string[]) => call<ImportResult>('import.apply', { aliases }),
  // A host-key outcome is a result, not an error; trustHostKey retries pinned to exactly that key.
  termOpen: (id: string, server: string, rows: number, cols: number, trustHostKey?: { fingerprint: string; keyType: string }) =>
    call<TermOpenResult>('term.open', trustHostKey ? { id, server, rows, cols, trustHostKey } : { id, server, rows, cols }),
  termClose: async (id: string) => { await call('term.close', { id }) },
  termWrite: (id: string, data: Uint8Array, user: boolean) => bridge().notify('term.write', { id, data: toBase64(data), user }),
  termAck: (id: string, n: number) => bridge().notify('term.ack', { id, n }),
  termResize: (id: string, rows: number, cols: number) => bridge().notify('term.resize', { id, rows, cols }),
  onEvent: (cb: (e: HubEvent) => void) => bridge().onEvent(cb),
  onState: (cb: (s: HubState) => void) => bridge().onState(cb),
  getState: () => bridge().getState(),
  filesList: (server: string, path: string, trustHostKey?: { fingerprint: string; keyType: string }) =>
    call<FilesListResult>('files.list', trustHostKey ? { server, path, trustHostKey } : { server, path }),
  filesMkdir: async (server: string, path: string) => { await call('files.mkdir', { server, path }) },
  filesRename: async (server: string, from: string, to: string) => { await call('files.rename', { server, from, to }) },
  // sources/dest carry grant tokens where they name local paths; main swaps them.
  filesPlan: async (id: string, server: string, op: FileOp, sources: string[], dest?: string) => {
    await call('files.plan', dest === undefined ? { id, server, op, sources } : { id, server, op, sources, dest })
  },
  filesRun: (id: string, conflict: 'skip' | 'overwrite' | 'ask') => call<{ cancelled?: boolean }>('files.run', { id, conflict }),
  filesCancel: (id: string) => bridge().notify('files.cancel', { id }),
  pickUpload: async (server: string, folder: string, mode: 'files' | 'folder' | 'both') => {
    try { return await bridge().pickUpload(server, folder, mode) } catch (e) { throw cleanError(e) }
  },
  pickDownloadDir: async (server: string) => {
    try { return await bridge().pickDownloadDir(server) } catch (e) { throw cleanError(e) }
  },
  grantDropped: async (files: File[], server: string) => {
    try { return await bridge().grantDropped(files, server) } catch (e) { throw cleanError(e) }
  },
  tunnelsList: () => call<TunnelView[]>('tunnels.list', {}),
  tunnelsSave: (server: string, tunnel: Tunnel) => call<Tunnel>('tunnels.save', {
    server, tunnel: { id: tunnel.id, kind: tunnel.kind, listenPort: tunnel.listenPort,
      targetHost: tunnel.targetHost ?? '', targetPort: tunnel.targetPort ?? 0, label: tunnel.label ?? '' },
  }),
  tunnelsDelete: async (server: string, id: string) => { await call('tunnels.delete', { server, id }) },
  tunnelsStart: async (server: string, id: string) => { await call('tunnels.start', { server, id }) },
  tunnelsStop: (server: string, id: string) => bridge().notify('tunnels.stop', { server, id }),
  setAutoAllow: async (server: string, mode: AutoAllowMode) => { await call('servers.setAutoAllow', { server, mode }) },
  autoAllowCheck: (server: string) => call<AutoAllowCheck>('servers.autoAllowCheck', { server }),
}

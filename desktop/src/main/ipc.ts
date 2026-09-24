import { BrowserWindow, ipcMain } from 'electron'
import { NOTIFY_METHODS, REQUEST_METHODS, type HubState } from '../shared/protocol'
import type { HubProcess } from './hubProcess'

export interface HubLike {
  call(method: string, params?: unknown, timeoutMs?: number): Promise<unknown>
  notify(method: string, params?: unknown): void
}

const requests = new Set<string>(REQUEST_METHODS)
const notifies = new Set<string>(NOTIFY_METHODS)

export function relayCall(hub: HubLike, method: unknown, params: unknown): Promise<unknown> {
  if (typeof method !== 'string' || !requests.has(method)) {
    return Promise.reject(new Error(`hub method not allowed: ${String(method)}`))
  }
  return hub.call(method, params ?? {})
}

export function relayNotify(hub: HubLike, method: unknown, params: unknown): void {
  if (typeof method === 'string' && notifies.has(method)) hub.notify(method, params ?? {})
}

export function registerIpc(hub: HubProcess, getWindow: () => BrowserWindow | undefined): void {
  ipcMain.handle('hub:call', (_e, method: unknown, params: unknown) => relayCall(hub, method, params))
  ipcMain.on('hub:notify', (_e, method: unknown, params: unknown) => relayNotify(hub, method, params))
  ipcMain.handle('hub:get-state', () => hub.state)
  hub.on('notification', (method: string, params: unknown) => {
    getWindow()?.webContents.send('hub:event', { method, params })
  })
  hub.on('state', (s: HubState) => getWindow()?.webContents.send('hub:state', s))
}

import { ipcMain, type BrowserWindow, type IpcMainEvent, type IpcMainInvokeEvent } from 'electron'
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

// Trusted means the app's own window and its main frame (not a subframe).
export type IsTrusted = (e: IpcMainEvent | IpcMainInvokeEvent) => boolean

export function isMainFrameOf(
  win: { isDestroyed(): boolean; webContents: { mainFrame: unknown } } | undefined,
  e: { sender: unknown; senderFrame: unknown },
): boolean {
  return !!win && !win.isDestroyed() && e.sender === win.webContents && e.senderFrame === win.webContents.mainFrame
}

// Rejects with "untrusted sender" instead of calling fn, so a page that isn't the app's
// own window (e.g. loaded via a followed link or a dropped URL) can't reach the hub.
export function guard(isTrusted: IsTrusted, e: IpcMainEvent | IpcMainInvokeEvent, fn: () => Promise<unknown>): Promise<unknown> {
  return isTrusted(e) ? fn() : Promise.reject(new Error('untrusted sender'))
}

export function registerIpc(hub: HubProcess, getWindow: () => BrowserWindow | undefined, isTrusted: IsTrusted): void {
  ipcMain.handle('hub:call', (e, method: unknown, params: unknown) =>
    guard(isTrusted, e, () => relayCall(hub, method, params)))
  ipcMain.on('hub:notify', (e, method: unknown, params: unknown) => {
    if (isTrusted(e)) relayNotify(hub, method, params)
  })
  ipcMain.handle('hub:get-state', (e) => guard(isTrusted, e, () => Promise.resolve(hub.state)))
  hub.on('notification', (method: string, params: unknown) => {
    getWindow()?.webContents.send('hub:event', { method, params })
  })
  hub.on('state', (s: HubState) => getWindow()?.webContents.send('hub:state', s))
}

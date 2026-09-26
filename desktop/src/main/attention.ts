import { app, Menu, nativeImage, Notification, Tray, type BrowserWindow } from 'electron'
import type { ApprovalRequest, HubState } from '../shared/protocol'
import type { HubProcess } from './hubProcess'

// 16x16 PNG, a filled blue circle on a transparent background.
export const ICON_PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAWklEQVR4nGJioBCwwBjIwHjm//8wNjI4m87ICGPDABOxmnHJMRFSQMgQJlwSxBrCRKpmdEOYsMiRBEYNgBqALYURAjA9TOgCpGhGMYBYQ9DVMBFSQKwc2QAwAHdCJCGXBVXNAAAAAElFTkSuQmCC'

export function trayTitle(count: number): string { return count === 0 ? '' : String(count) }

export function trayTooltip(count: number): string {
  if (count === 0) return 'sshgate'
  return `sshgate: ${count} request${count === 1 ? '' : 's'} waiting`
}

export function notificationText(req: ApprovalRequest): { title: string; body: string } {
  const title = req.sudo ? 'AI wants to run a command with sudo' : 'AI wants to run a command'
  const full = `${req.server}: ${req.command}`
  return { title, body: full.length > 120 ? full.slice(0, 120) + '…' : full }
}

export class PendingCounter {
  private ids = new Set<string>()
  get count(): number { return this.ids.size }
  reset(): void { this.ids.clear() }
  apply(method: string, params: unknown): number {
    const id = (params as { request?: { id?: string } } | undefined)?.request?.id
    if (id && method === 'pending') this.ids.add(id)
    if (id && method === 'decided') this.ids.delete(id)
    return this.ids.size
  }
}

// Notification instances must stay reachable until they close, or Electron/the OS
// can drop them (and their 'click' handler) to GC before the user acts on them.
const liveNotifications = new Set<Notification>()

export function setupAttention(hub: HubProcess, getWindow: () => BrowserWindow | undefined): void {
  const tray = new Tray(nativeImage.createFromDataURL(`data:image/png;base64,${ICON_PNG_BASE64}`))
  const show = () => {
    const w = getWindow()
    if (!w) return
    if (w.isMinimized()) w.restore()
    w.show()
    w.focus()
  }
  tray.setContextMenu(Menu.buildFromTemplate([{ label: 'Show', click: show }, { label: 'Quit', click: () => app.quit() }]))
  const counter = new PendingCounter()
  const render = () => {
    if (process.platform === 'darwin') tray.setTitle(trayTitle(counter.count))
    tray.setToolTip(trayTooltip(counter.count))
  }
  render()
  hub.on('notification', (method: string, params: unknown) => {
    const before = counter.count
    counter.apply(method, params)
    if (counter.count !== before) render()
    if (method !== 'pending') return
    const w = getWindow()
    if (w && w.isFocused()) return
    if (!Notification.isSupported()) return
    const n = new Notification(notificationText((params as { request: ApprovalRequest }).request))
    liveNotifications.add(n)
    const forget = () => liveNotifications.delete(n)
    n.on('click', () => { forget(); show() })
    n.on('close', forget)
    n.show()
  })
  hub.on('state', (s: HubState) => { if (s.kind !== 'running') { counter.reset(); render() } })
}

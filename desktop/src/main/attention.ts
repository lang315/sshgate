import { app, Menu, nativeImage, Notification, Tray, type BrowserWindow, type NativeImage } from 'electron'
import type { ApprovalRequest, HubState } from '../shared/protocol'
import type { HubProcess } from './hubProcess'

// 16x16 PNG, a filled blue circle on a transparent background.
export const ICON_PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAWklEQVR4nGJioBCwwBjIwHjm//8wNjI4m87ICGPDABOxmnHJMRFSQMgQJlwSxBrCRKpmdEOYsMiRBEYNgBqALYURAjA9TOgCpGhGMYBYQ9DVMBFSQKwc2QAwAHdCJCGXBVXNAAAAAElFTkSuQmCC'

// macOS menu bar icon by scale factor: a black shield with ">_" cut out, drawn
// as a template image so macOS tints it for a light or dark menu bar.
export const TRAY_TEMPLATE_PNG_BASE64: Record<string, string> = {
  1: 'iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAABNUlEQVR42nXTsUpDQRAF0JO8IGhlIXZaioWVINjbmV8IdgEtFNEPUbCysco3WIhWIkgsLcRO0C6NxkKMxlg4GzbPZOCyu/Pm3rdzd7fwF0VgYDTWsYCXLFdBLcZyPZhHAxf4DlxiK76NRCWwgU2sYQWzxscb7nGHc1ylDzexnYSv+HNaf5fWg+CohcB7FEAPM1lhJfyRiVfRFZPcmB5WsRe5n5JRSayWRGulHqewj50gnoRIIv+LJFDNcttYRB2fOEW/VDMUTAKdrGeYHnPuKZKZnVytmbl9FrndyD3jMfCUCTRzgblw9QPL4cMg+q9ndUvRTjc4w2sMB0F6jbEf4wOuA7eROyxxh5PWhIuUo1UmJ6OSy8dZcT/E0vooO7XKpHch3kU7I7YjV64bG0U2NgLFuG3DL0lZYuH5T/dXAAAAAElFTkSuQmCC',
  2: 'iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAC4UlEQVR42rWXv2sUQRTHP7t3FxWj+ANtRBCtRERERIhVOFEET2wUxULERm0UrPQPEJIiYCWCFmprF1GCioWcjTGp1C4IEcxZiEWimLvdtflOeAyzl709b2DYZebN+37nvbcz363QW4uAip6ZNxdrLuvFYaUgqHOeCiAD9gK3gBPAd6CleYAqfTS3y2qA4HrgHPAC6BgyCTAFXACGA5usmsjltljdb2uAA8BdYM6AZkBb3Y59BcaAg1rbFSfEaAuwDzgMjMjRbjOfGEdufWbCb6M2B8wC74EPwCfgZ6gOIqABvFUus0BvCzxbpSeBqLjeEkbDpJqaiIwZw1ROOgVBu5HpyJct4DFh1my1LmlBKlJ2Ls2pj9Wav6atsaWQQZxTqQ48o//mwh7nMczbxYw5fP4HEYoQSNWfAkeAm4MgEXcBj4BvwGUV0j3guklHNkgCDmQnMAmsVe7uA5fMfDrIFDiQk8BzHcMx8AQ469kMhICbbwN14CWwQePPgDPA737TERcgU1MNjABNYJfGJoHbWpeUxbUhbBVYuBlY533XZVrL3CErl8dRc3z6d0Cmi2SHcXJaJ1rHHNt+TwJHcyasFWy3i2FgwTN0z2lguwE/X+KOcPYLRjNEVQ1WgEXgFXDRfF4R8E67/aWxK8BDc1C1gC+eTHPv24D95pONhLEozMSXZqMy7mjBD0/h3DC7Wdb7oy65bhh/LgKjeXLQCYxpGS6LxGPdjHc8cq42HnQhcFw2f00qozxF5EJSB14LyF3JH4FDJoyRuSU/K6yhFOwBTolsDTgGvPHDH1LJEyYKiclhGVHiUjVRRIlbRdwMkOgmwUL9j2yaRZWxrYWtHomyO2/KV9SLqnKGG6X33U6LilJnNyUflJF0sXmOeydjqB5STw2Pez5KaziXs7o0vi/TfRk+K1t/fV/NVe6QDqP5QATmNTfUw38nZX9kNwFX9bczA1zTGIMCtympBsYwf8U9hfwf9KFWxMb2a20AAAAASUVORK5CYII=',
}

function trayImage(): NativeImage {
  if (process.platform !== 'darwin') return nativeImage.createFromDataURL(`data:image/png;base64,${ICON_PNG_BASE64}`)
  const img = nativeImage.createEmpty()
  for (const [scale, b64] of Object.entries(TRAY_TEMPLATE_PNG_BASE64)) img.addRepresentation({ scaleFactor: Number(scale), dataURL: `data:image/png;base64,${b64}` })
  img.setTemplateImage(true)
  return img
}

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

// Brings the window back, and on macOS the Dock icon a close hid with it.
export function showWindow(w: BrowserWindow | undefined): void {
  if (!w) return
  if (process.platform === 'darwin') void app.dock?.show()
  if (w.isMinimized()) w.restore()
  w.show()
  w.focus()
}

export function setupAttention(hub: HubProcess, getWindow: () => BrowserWindow | undefined): void {
  const tray = new Tray(trayImage())
  const show = () => showWindow(getWindow())
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

import { Menu, type MenuItemConstructorOptions } from 'electron'

// No Reload (it would drop the renderer without closing its hub terminals) and no
// Close-window accelerator; on Windows/Linux no menu at all, so Ctrl+R/W/C… reach
// the terminal. macOS keeps an app menu and the Edit roles text fields need there.
export function menuTemplate(platform: NodeJS.Platform): MenuItemConstructorOptions[] | null {
  if (platform !== 'darwin') return null
  return [
    { role: 'appMenu', submenu: [{ role: 'about' }, { type: 'separator' }, { role: 'hide' }, { role: 'hideOthers' }, { role: 'unhide' }, { type: 'separator' }, { role: 'quit' }] },
    { role: 'editMenu', submenu: [{ role: 'undo' }, { role: 'redo' }, { type: 'separator' }, { role: 'cut' }, { role: 'copy' }, { role: 'paste' }, { role: 'selectAll' }] },
  ]
}

export function installMenu(): void {
  const t = menuTemplate(process.platform)
  Menu.setApplicationMenu(t ? Menu.buildFromTemplate(t) : null)
}

export const CRASH_LIMIT = 3
export const CRASH_WINDOW_MS = 60_000

// Stops the crash→reload loop: the CRASH_LIMIT-th renderer crash within
// CRASH_WINDOW_MS gets an error page instead of another reload.
export class CrashPolicy {
  private crashes: number[] = []
  record(now = Date.now()): 'reload' | 'error' {
    this.crashes = this.crashes.filter((t) => now - t < CRASH_WINDOW_MS)
    this.crashes.push(now)
    return this.crashes.length >= CRASH_LIMIT ? 'error' : 'reload'
  }
}

// A call rejected because the hub process is gone (HubProcess: not running, or it
// exited mid-call). A dead hub holds no key: the vault is already locked.
export function hubGone(e: unknown): boolean {
  const m = (e as Error)?.message
  return m === 'hub is not running' || m === 'hub restarted'
}

export const CRASH_LOOP_TEXT = 'The window crashed repeatedly. Quit and restart the app.'
export const LOCK_FAILED_TEXT = 'Could not lock the vault after a crash; quit the app to lock it.'

// A crashed renderer is the only path that reloads the window. Lock the vault first,
// so the reloaded page starts at the unlock screen, and close the terminals the dead
// renderer opened (nothing could reach them again). If the lock fails, or the renderer
// keeps crashing, show a static text page instead: quitting stops the hub, which drops the key.
export async function recoverRenderer(
  hub: { call(method: string, params?: unknown, timeoutMs?: number): Promise<unknown> },
  win: { isDestroyed(): boolean; reload(): void; loadURL(url: string): Promise<void> },
  reason: string,
  policy: CrashPolicy,
): Promise<void> {
  console.error('sshgate: renderer gone:', reason)
  const decision = policy.record()
  const locked = await hub.call('lock', {}, 5000).then(() => true, (e) => {
    if (hubGone(e)) return true
    console.error('sshgate: lock after renderer crash failed:', (e as Error).message)
    return false
  })
  await hub.call('term.closeAll', {}, 5000).catch((e) => console.error('sshgate: term.closeAll after renderer crash failed:', (e as Error).message))
  if (win.isDestroyed()) return
  const text = !locked ? LOCK_FAILED_TEXT : decision === 'error' ? CRASH_LOOP_TEXT : undefined
  if (text) await win.loadURL('data:text/plain;charset=utf-8,' + encodeURIComponent(text)).catch(() => {})
  else win.reload()
}

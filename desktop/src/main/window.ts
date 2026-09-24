import { Menu, type MenuItemConstructorOptions } from 'electron'

// No Reload (it would drop the renderer without closing its hub terminals) and no
// Close-window accelerator; on Windows/Linux no menu at all, so Ctrl+R/W/C… reach
// the terminal. macOS keeps an app menu and Copy/Paste/Select All for the clipboard.
export function menuTemplate(platform: NodeJS.Platform): MenuItemConstructorOptions[] | null {
  if (platform !== 'darwin') return null
  return [
    { role: 'appMenu', submenu: [{ role: 'about' }, { type: 'separator' }, { role: 'hide' }, { role: 'hideOthers' }, { role: 'unhide' }, { type: 'separator' }, { role: 'quit' }] },
    { role: 'editMenu', submenu: [{ role: 'copy' }, { role: 'paste' }, { role: 'selectAll' }] },
  ]
}

export function installMenu(): void {
  const t = menuTemplate(process.platform)
  Menu.setApplicationMenu(t ? Menu.buildFromTemplate(t) : null)
}

// A crashed renderer is the only path that reloads the window: lock the vault first,
// so the reloaded page starts at the unlock screen. Terminals the dead renderer had
// open stay open in the hub, unreachable, until the hub restarts (known limitation).
export async function recoverRenderer(
  hub: { call(method: string, params?: unknown, timeoutMs?: number): Promise<unknown> },
  win: { isDestroyed(): boolean; reload(): void },
  reason: string,
): Promise<void> {
  console.error('ssh-mcp: renderer gone:', reason)
  await hub.call('lock', {}, 5000).catch((e) => console.error('ssh-mcp: lock after renderer crash failed:', (e as Error).message))
  if (!win.isDestroyed()) win.reload()
}

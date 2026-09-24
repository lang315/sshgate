import { app, BrowserWindow, session, type IpcMainEvent, type IpcMainInvokeEvent } from 'electron'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { setupAttention } from './attention'
import { HubProcess } from './hubProcess'
import { registerIpc } from './ipc'
import { installMenu, recoverRenderer } from './window'

let win: BrowserWindow | undefined

// Hub binary: SSH_MCP_BIN, else the repo-root build next to desktop/, else PATH.
function hubCommand(): string {
  if (process.env.SSH_MCP_BIN) return process.env.SSH_MCP_BIN
  const exe = process.platform === 'win32' ? 'ssh-mcp.exe' : 'ssh-mcp'
  const local = path.join(app.getAppPath(), '..', exe)
  return fs.existsSync(local) ? local : exe
}

function hubArgs(): string[] {
  const args = ['hub']
  if (process.env.SSH_MCP_STORE) args.push(`--store=${process.env.SSH_MCP_STORE}`)
  return args
}

const hubBin = hubCommand()
console.error('ssh-mcp: hub binary', hubBin)
const hub = new HubProcess({ command: hubBin, args: hubArgs(), env: process.env })

// Only a destroyed-safe reference to the app's own window ever reaches the hub relay.
function getWindow(): BrowserWindow | undefined {
  return win && !win.isDestroyed() ? win : undefined
}

function isTrusted(e: IpcMainEvent | IpcMainInvokeEvent): boolean {
  return !!win && !win.isDestroyed() && e.sender === win.webContents && e.senderFrame === win.webContents.mainFrame
}

function createWindow(): BrowserWindow {
  const w = new BrowserWindow({
    width: 1200,
    height: 800,
    webPreferences: {
      preload: path.join(__dirname, '..', 'preload', 'preload.js'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
    },
  })
  w.on('closed', () => { win = undefined })
  // Never let this window navigate to, or open, other content that could get window.sshmcp.
  w.webContents.on('will-navigate', (e) => e.preventDefault())
  w.webContents.setWindowOpenHandler(() => ({ action: 'deny' }))
  w.webContents.on('render-process-gone', (_e, d) => { void recoverRenderer(hub, w, d.reason) })
  w.loadFile(path.join(__dirname, '..', 'renderer', 'index.html'))
  return w
}

app.whenReady().then(() => {
  installMenu()
  session.defaultSession.setPermissionRequestHandler((_wc, _perm, cb) => cb(false))
  session.defaultSession.setPermissionCheckHandler(() => false)
  win = createWindow()
  registerIpc(hub, getWindow, isTrusted)
  setupAttention(hub, getWindow)
  hub.start()
})

let quitting = false
app.on('before-quit', (e) => {
  if (quitting) return
  e.preventDefault()
  quitting = true
  hub.stop().finally(() => app.quit())
})

app.on('window-all-closed', () => app.quit())

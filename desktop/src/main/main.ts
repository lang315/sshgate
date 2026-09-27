import { app, BrowserWindow, dialog, session, type IpcMainEvent, type IpcMainInvokeEvent } from 'electron'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { setupAttention } from './attention'
import { FilesRelay } from './files'
import { HubProcess } from './hubProcess'
import { isMainFrameOf, registerIpc } from './ipc'
import { CrashPolicy, installMenu, recoverRenderer } from './window'

let win: BrowserWindow | undefined
const crashPolicy = new CrashPolicy()

// Hub binary: SSHGATE_BIN, else the repo-root build next to desktop/, else PATH.
function hubCommand(): string {
  if (process.env.SSHGATE_BIN) return process.env.SSHGATE_BIN
  const exe = process.platform === 'win32' ? 'sshgate.exe' : 'sshgate'
  const local = path.join(app.getAppPath(), '..', exe)
  return fs.existsSync(local) ? local : exe
}

function hubArgs(): string[] {
  const args = ['hub']
  if (process.env.SSHGATE_STORE) args.push(`--store=${process.env.SSHGATE_STORE}`)
  if (process.env.SSHGATE_SSH_CONFIG) args.push(`--sshConfig=${process.env.SSHGATE_SSH_CONFIG}`)
  // Dev/test knob: a Go duration such as 3s (the hub rejects anything under 1s).
  if (process.env.SSHGATE_IDLE_LOCK) args.push(`--idleLock=${process.env.SSHGATE_IDLE_LOCK}`)
  return args
}

const hubBin = hubCommand()
console.error('sshgate: hub binary', hubBin)
const hub = new HubProcess({ command: hubBin, args: hubArgs(), env: process.env })
const files = new FilesRelay(hub, dialog, getWindow)

// Only a destroyed-safe reference to the app's own window ever reaches the hub relay.
function getWindow(): BrowserWindow | undefined {
  return win && !win.isDestroyed() ? win : undefined
}

function isTrusted(e: IpcMainEvent | IpcMainInvokeEvent): boolean {
  return isMainFrameOf(win, e)
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
  w.webContents.on('render-process-gone', (_e, d) => { void recoverRenderer(hub, w, d.reason, crashPolicy, () => files.reset()) })
  w.loadFile(path.join(__dirname, '..', 'renderer', 'index.html'))
  return w
}

app.whenReady().then(() => {
  installMenu()
  session.defaultSession.setPermissionRequestHandler((_wc, _perm, cb) => cb(false))
  session.defaultSession.setPermissionCheckHandler(() => false)
  win = createWindow()
  registerIpc(hub, getWindow, isTrusted, files)
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

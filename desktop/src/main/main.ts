import { app, BrowserWindow, dialog, session, type IpcMainEvent, type IpcMainInvokeEvent } from 'electron'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { setupAttention } from './attention'
import { FilesRelay } from './files'
import { HubProcess, hubLaunch } from './hubProcess'
import { isMainFrameOf, registerIpc } from './ipc'
import { CrashPolicy, installMenu, recoverRenderer } from './window'

let win: BrowserWindow | undefined
const crashPolicy = new CrashPolicy()

const launch = hubLaunch({ env: process.env, packaged: app.isPackaged || !process.defaultApp, appPath: app.getAppPath(), platform: process.platform, exists: fs.existsSync })
console.error('sshgate: hub binary', launch.command)
const hub = new HubProcess(launch)
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

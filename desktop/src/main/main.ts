import { app, BrowserWindow } from 'electron'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { HubProcess } from './hubProcess'
import { registerIpc } from './ipc'

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

const hub = new HubProcess({ command: hubCommand(), args: hubArgs(), env: process.env })

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
  w.loadFile(path.join(__dirname, '..', 'renderer', 'index.html'))
  return w
}

app.whenReady().then(() => {
  win = createWindow()
  registerIpc(hub, () => win)
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

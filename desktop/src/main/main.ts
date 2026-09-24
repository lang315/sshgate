import { app, BrowserWindow } from 'electron'
import * as path from 'node:path'

function createWindow(): BrowserWindow {
  const win = new BrowserWindow({
    width: 1200,
    height: 800,
    webPreferences: {
      preload: path.join(__dirname, '..', 'preload', 'preload.js'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
    },
  })
  win.loadFile(path.join(__dirname, '..', 'renderer', 'index.html'))
  return win
}

app.whenReady().then(() => {
  createWindow()
})

app.on('window-all-closed', () => app.quit())

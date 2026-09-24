import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron'

contextBridge.exposeInMainWorld('sshmcp', {
  call: (method: string, params?: unknown) => ipcRenderer.invoke('hub:call', method, params),
  notify: (method: string, params?: unknown) => ipcRenderer.send('hub:notify', method, params),
  getState: () => ipcRenderer.invoke('hub:get-state'),
  onEvent: (cb: (e: unknown) => void) => {
    const h = (_: IpcRendererEvent, e: unknown) => cb(e)
    ipcRenderer.on('hub:event', h)
    return () => ipcRenderer.removeListener('hub:event', h)
  },
  onState: (cb: (s: unknown) => void) => {
    const h = (_: IpcRendererEvent, s: unknown) => cb(s)
    ipcRenderer.on('hub:state', h)
    return () => ipcRenderer.removeListener('hub:state', h)
  },
})

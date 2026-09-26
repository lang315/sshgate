import { contextBridge, ipcRenderer, webUtils, type IpcRendererEvent } from 'electron'

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
  pickUpload: (server: string, folder: string, mode: string) => ipcRenderer.invoke('files:pickUpload', { server, folder, mode }),
  pickDownloadDir: (server: string) => ipcRenderer.invoke('files:pickDownloadDir', { server }),
  // Only real dropped files have a path; a File made by page script has none.
  grantDropped: (files: unknown, server: string) => {
    if (!Array.isArray(files) || files.length > 1000 || !files.every((f) => f instanceof File)) {
      return Promise.reject(new Error('invalid drop'))
    }
    const paths = files.map((f) => webUtils.getPathForFile(f)).filter((p) => p !== '')
    return ipcRenderer.invoke('files:grantDropped', { server, paths })
  },
})

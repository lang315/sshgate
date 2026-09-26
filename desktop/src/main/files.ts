import { randomBytes } from 'node:crypto'
import * as path from 'node:path'
import type { BrowserWindow, MessageBoxOptions, MessageBoxReturnValue, OpenDialogOptions, OpenDialogReturnValue } from 'electron'
import type { HubState } from '../shared/protocol'
import { displayText } from '../shared/display'

export type GrantKind = 'read' | 'writeDir'
interface Grant { path: string; kind: GrantKind; server: string; expires: number }

export const GRANT_TTL_MS = 5 * 60_000
export const MAX_DROP = 1000

// Grants are the only way a local path reaches the hub: the renderer holds
// opaque tokens, each good once, for one kind and one server, for 5 minutes.
export class Grants {
  private m = new Map<string, Grant>()
  constructor(private now: () => number = Date.now) {}
  add(p: string, kind: GrantKind, server: string): string {
    const t = 'g-' + randomBytes(16).toString('hex')
    this.m.set(t, { path: p, kind, server, expires: this.now() + GRANT_TTL_MS })
    return t
  }
  take(token: unknown, kind: GrantKind, server: string): string {
    const g = typeof token === 'string' ? this.m.get(token) : undefined
    if (typeof token === 'string') this.m.delete(token)
    if (!g || g.kind !== kind || g.server !== server || this.now() > g.expires) throw new Error('file access not granted')
    return g.path
  }
  clear(): void { this.m.clear() }
}

// Called as property accesses at call time, so e2e can replace them.
export interface DialogLike {
  showOpenDialog(win: BrowserWindow, o: OpenDialogOptions): Promise<OpenDialogReturnValue>
  showMessageBox(win: BrowserWindow, o: MessageBoxOptions): Promise<MessageBoxReturnValue>
}

interface HubLike {
  call(method: string, params?: unknown): Promise<unknown>
  notify(method: string, params?: unknown): void
}

const str = (v: unknown): v is string => typeof v === 'string'
const strs = (v: unknown): v is string[] => Array.isArray(v) && v.every(str)
const bad = () => new Error('invalid files request')

export class FilesRelay {
  readonly grants: Grants
  private downloads = new Map<string, { folder: string; conflicts: number; sample: string[] }>()

  constructor(private hub: HubLike, private dialog: DialogLike, private getWindow: () => BrowserWindow | undefined, now?: () => number) {
    this.grants = new Grants(now)
  }

  private win(): BrowserWindow {
    const w = this.getWindow()
    if (!w) throw new Error('no window')
    return w
  }

  async pickUpload(p: unknown): Promise<{ token: string; name: string }[]> {
    const { server, folder, mode } = (p ?? {}) as Record<string, unknown>
    if (!str(server) || !str(folder) || !['files', 'folder', 'both'].includes(mode as string)) throw bad()
    const properties: OpenDialogOptions['properties'] = mode === 'files' ? ['openFile', 'multiSelections']
      : mode === 'folder' ? ['openDirectory', 'multiSelections'] : ['openFile', 'openDirectory', 'multiSelections']
    const r = await this.dialog.showOpenDialog(this.win(), { title: `Upload to ${displayText(server)}:${displayText(folder)}`, buttonLabel: 'Upload', properties })
    if (r.canceled) return []
    return r.filePaths.map((fp) => ({ token: this.grants.add(fp, 'read', server), name: path.basename(fp) }))
  }

  async pickDownloadDir(p: unknown): Promise<{ token: string; name: string } | null> {
    const { server } = (p ?? {}) as Record<string, unknown>
    if (!str(server)) throw bad()
    const r = await this.dialog.showOpenDialog(this.win(), { title: `Download from ${displayText(server)} to…`, buttonLabel: 'Download here', properties: ['openDirectory', 'createDirectory'] })
    if (r.canceled || r.filePaths.length !== 1) return null
    return { token: this.grants.add(r.filePaths[0], 'writeDir', server), name: path.basename(r.filePaths[0]) }
  }

  grantDropped(p: unknown): { token: string; name: string }[] {
    const { server, paths } = (p ?? {}) as Record<string, unknown>
    if (!str(server) || !strs(paths) || paths.length > MAX_DROP || !paths.every((x) => path.isAbsolute(x))) throw bad()
    return paths.map((fp) => ({ token: this.grants.add(fp, 'read', server), name: path.basename(fp) }))
  }

  // files.plan rebuilt from its allowlist, tokens swapped for paths.
  private plan(params: unknown): Record<string, unknown> {
    const { id, server, op, sources, dest } = (params ?? {}) as Record<string, unknown>
    if (!str(id) || !str(server) || !strs(sources)) throw bad()
    if (op === 'upload' && str(dest)) {
      return { id, server, op, sources: sources.map((t) => this.grants.take(t, 'read', server)), dest }
    }
    if (op === 'download') {
      const folder = this.grants.take(dest, 'writeDir', server)
      this.downloads.set(id, { folder, conflicts: 0, sample: [] })
      return { id, server, op, sources, dest: folder }
    }
    if (op === 'delete') return { id, server, op, sources }
    throw bad()
  }

  async call(method: string, params: unknown): Promise<unknown> {
    if (method === 'files.plan') return this.hub.call('files.plan', this.plan(params))
    if (method !== 'files.run') throw bad()
    const { id, conflict } = (params ?? {}) as Record<string, unknown>
    if (!str(id)) throw bad()
    const d = this.downloads.get(id)
    if (!d) {
      if (conflict !== 'skip' && conflict !== 'overwrite') throw bad()
      return this.hub.call('files.run', { id, conflict })
    }
    if (d.conflicts === 0) return this.hub.call('files.run', { id, conflict: 'skip' })
    if (conflict !== 'ask') throw new Error('download conflicts are decided in the native dialog')
    const n = d.conflicts
    const r = await this.dialog.showMessageBox(this.win(), {
      type: 'question', buttons: ['Cancel', 'Skip existing', 'Overwrite all'], defaultId: 0, cancelId: 0, noLink: true,
      message: `${n} ${n === 1 ? 'file already exists' : 'files already exist'} in ${d.folder}`,
      detail: d.sample.map(displayText).join('\n'),
    })
    if (r.response === 1) return this.hub.call('files.run', { id, conflict: 'skip' })
    if (r.response === 2) return this.hub.call('files.run', { id, conflict: 'overwrite' })
    this.hub.notify('files.cancel', { id })
    return { cancelled: true }
  }

  onNotification(method: string, params: unknown): void {
    const p = (params ?? {}) as { id?: string; conflicts?: { count?: number; sample?: string[] } }
    if (method === 'files.planned' && p.id) {
      const d = this.downloads.get(p.id)
      if (d) { d.conflicts = p.conflicts?.count ?? 0; d.sample = p.conflicts?.sample ?? [] }
    }
    if (method === 'files.done' && p.id) this.downloads.delete(p.id)
    if (method === 'locked') this.grants.clear()
  }

  onState(s: HubState): void {
    if (s.kind !== 'running') { this.grants.clear(); this.downloads.clear() }
  }

  // The renderer was reloaded: its tokens die with it.
  reset(): void { this.grants.clear() }
}

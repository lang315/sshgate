import type { FileEntry, FileOp, FilesDone } from '../shared/protocol'

export function newJobId(): string {
  const b = new Uint8Array(8)
  crypto.getRandomValues(b)
  return 'j-' + Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

export type SortKey = 'name' | 'size' | 'mtime' | 'mode'

// Folders always first; then by key, descending if asked; ties by name, ascending.
export function sortEntries(es: FileEntry[], key: SortKey, desc: boolean): FileEntry[] {
  const byName = (a: FileEntry, b: FileEntry) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0)
  const cmp = (a: FileEntry, b: FileEntry) => {
    const d = key === 'name' ? byName(a, b) : (a[key] as number) - (b[key] as number)
    return (desc ? -d : d) || byName(a, b)
  }
  const dirs = es.filter((x) => x.kind === 'dir').sort(cmp)
  const rest = es.filter((x) => x.kind !== 'dir').sort(cmp)
  return [...dirs, ...rest]
}

export const visibleEntries = (es: FileEntry[], showHidden: boolean) => (showHidden ? es : es.filter((x) => !x.name.startsWith('.')))

export function formatSize(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(1)} ${units[i]}`
}

export function formatMode(kind: FileEntry['kind'], mode: number): string {
  const t = kind === 'dir' ? 'd' : kind === 'link' ? 'l' : '-'
  const bits = 'rwxrwxrwx'
  return t + Array.from(bits, (c, i) => (mode & (1 << (8 - i)) ? c : '-')).join('')
}

export function formatTime(sec: number): string {
  const d = new Date(sec * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export const joinPath = (dir: string, name: string) => (dir.endsWith('/') ? dir + name : `${dir}/${name}`)

export function parentPath(p: string): string | undefined {
  if (p === '' || p === '/') return undefined
  const i = p.lastIndexOf('/')
  return i <= 0 ? '/' : p.slice(0, i)
}

export function nameError(name: string): string | undefined {
  if (name === '' || name === '.' || name === '..') return 'Enter a name.'
  // The hub's listing hides a name with \ as unsafe, so it could never be selected again.
  if (/[/\\\u0000]/.test(name)) return 'A name cannot contain /, \\, or NUL.'
  return undefined
}

export const DELETE_WORD = 'delete'

// A selected folder has contents when the plan found more than was selected.
// Errors count as found: an unreadable child is not counted as a file or folder,
// and must not let a non-empty folder skip the typed confirmation.
export const deleteNeedsTyping = (hasFolder: boolean, selectedCount: number, p: { files: number; dirs: number; links: number; errorCount: number }) =>
  hasFolder && p.files + p.dirs + p.links + p.errorCount > selectedCount

// The automatic relist after a job ends in folder jobFolder. Skipped while any
// load is in flight, and unless the folder shown is both the job's and the one the
// user last asked for (a failed navigation leaves shown behind): it must never
// supersede the user's own newer navigation.
export const relistAfterJob = (jobFolder: string, shown: string, requested: string, loading: boolean) =>
  !loading && jobFolder === shown && requested === shown

// What a job does when files.planned lands. A cancel sent while planning can reach
// the hub before the job is registered and be lost, so it is sent again, and the
// job is never run or confirmed. Download conflicts are asked by main ('ask').
export function plannedAction(op: FileOp, conflicts: number, cancelRequested: boolean): 'cancel' | 'confirm' | 'skip' | 'ask' {
  if (cancelRequested) return 'cancel'
  if (op === 'delete' || (op === 'upload' && conflicts > 0)) return 'confirm'
  return op === 'download' ? 'ask' : 'skip'
}

export function windowRange(scrollTop: number, viewport: number, rowHeight: number, count: number, overscan = 10): [number, number] {
  const first = Math.floor(scrollTop / rowHeight)
  const start = Math.max(0, first - overscan)
  const end = Math.min(count, first + Math.ceil(viewport / rowHeight) + overscan)
  return [start, end]
}

export function nextSelection(names: Set<string>, order: string[], clicked: string,
  mods: { toggle?: boolean; range?: boolean }, anchor?: string): { names: Set<string>; anchor: string } {
  if (mods.range && anchor !== undefined && order.includes(anchor)) {
    const [a, b] = [order.indexOf(anchor), order.indexOf(clicked)].sort((x, y) => x - y)
    return { names: new Set(order.slice(a, b + 1)), anchor }
  }
  if (mods.toggle) {
    const n = new Set(names)
    if (n.has(clicked)) n.delete(clicked); else n.add(clicked)
    return { names: n, anchor: clicked }
  }
  return { names: new Set([clicked]), anchor: clicked }
}

const VERB = { upload: 'Uploaded', download: 'Downloaded', delete: 'Deleted' } as const

export function doneSummary(d?: FilesDone, error?: string): string {
  if (!d) return error ?? ''
  const n = d.op === 'delete' ? d.deleted : d.copied
  const parts = [`${VERB[d.op]} ${n}`]
  if (d.skipped) parts.push(`skipped ${d.skipped}`)
  if (d.errorCount) parts.push(`${d.errorCount} ${d.errorCount === 1 ? 'error' : 'errors'}`)
  if (!d.cancelled) return parts.join(' · ')
  parts[0] = parts[0].toLowerCase()
  return [`Cancelled${d.reason ? `: ${d.reason}` : ''}`, ...parts].join(' · ')
}

const get = (s: Storage | undefined, k: string) => { try { return s?.getItem(k) ?? undefined } catch { return undefined } }
const set = (s: Storage | undefined, k: string, v: string) => { try { s?.setItem(k, v) } catch { /* private mode: not remembered */ } }

export const lastFolder = {
  get: (s: Storage | undefined, server: string) => get(s, `sshgate.files.last.${server}`),
  set: (s: Storage | undefined, server: string, p: string) => set(s, `sshgate.files.last.${server}`, p),
}

export const hiddenPref = {
  get: (s: Storage | undefined) => get(s, 'sshgate.files.hidden') === '1',
  set: (s: Storage | undefined, on: boolean) => set(s, 'sshgate.files.hidden', on ? '1' : '0'),
}

export function localStore(): Storage | undefined {
  try { return window.localStorage } catch { return undefined }
}

import { useCallback, useEffect, useMemo, useReducer, useRef, useState, type DragEvent, type KeyboardEvent, type MouseEvent } from 'react'
import type { FileEntry, FileListing, FileOp, FilesDone, FilesPlanned, FilesProgress, HostKeyMismatch } from '../shared/protocol'
import { displayText } from '../shared/display'
import { hub } from './transport'
import type { HostKeyPrompts } from './hostkeys'
import type { Dispatcher, Tab } from './terminals'
import type { TermEvent } from './TermView'
import { ConflictDialog, DeleteDialog, NameDialog } from './FileDialogs'
import {
  deleteNeedsTyping, doneSummary, formatMode, formatSize, formatTime, hiddenPref, joinPath, lastFolder, localStore,
  newJobId, nextSelection, parentPath, sortEntries, visibleEntries, windowRange, type SortKey,
} from './files'
import { CloseIcon, DownloadIcon, FileIcon, FolderIcon, LinkIcon, PlusIcon, RefreshIcon, TrashIcon, UpIcon, UploadIcon, EditIcon } from './icons'

const ROW = 28

interface Job {
  id: string; op: FileOp; label: string; folder: string
  state: 'planning' | 'confirm' | 'running' | 'done'
  names?: string[]; hasFolder?: boolean
  planned?: FilesPlanned; progress?: FilesProgress; done?: FilesDone; error?: string
}

type Dialog =
  | { kind: 'conflict'; id: string; planned: FilesPlanned }
  | { kind: 'delete'; id: string; planned: FilesPlanned; names: string[]; hasFolder: boolean }
  | { kind: 'mkdir' }
  | { kind: 'rename'; from: string }

export function FilesView({ tab, visible, events, hostKeys, onMismatch, onTrusted, onJobs }: {
  tab: Tab; visible: boolean; events: Dispatcher<TermEvent>; hostKeys: HostKeyPrompts
  onMismatch: (m: HostKeyMismatch) => void; onTrusted: () => void; onJobs: (running: number) => void
}) {
  const server = tab.server
  const [listing, setListing] = useState<FileListing>()
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(false)
  const [pathInput, setPathInput] = useState('')
  const [showHidden, setShowHidden] = useState(() => hiddenPref.get(localStore()))
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({ key: 'name', desc: false })
  const [sel, setSel] = useState<{ names: Set<string>; anchor?: string }>({ names: new Set() })
  const [dialog, setDialog] = useState<Dialog>()
  const [scrollTop, setScrollTop] = useState(0)
  const [viewport, setViewport] = useState(400)
  const [dropping, setDropping] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)
  const shown = useRef('') // the folder listed now
  const jobs = useRef(new Map<string, Job>()).current
  const offs = useRef(new Map<string, () => void>()).current
  const [, bump] = useReducer((n: number) => n + 1, 0)

  const rows = useMemo(() => (listing ? sortEntries(visibleEntries(listing.entries, showHidden), sort.key, sort.desc) : []), [listing, showHidden, sort])
  const selected = rows.filter((r) => sel.names.has(r.name))
  const running = [...jobs.values()].filter((j) => j.state !== 'done').length
  useEffect(() => { onJobs(running) }, [running, onJobs])

  useEffect(() => {
    const el = listRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setViewport(el.clientHeight))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const load = useCallback(async (p: string): Promise<boolean> => {
    setLoading(true); setError(undefined); setPathInput(p)
    try {
      let r = await hub.filesList(server, p)
      while (r.status === 'hostKeyUnknown') {
        if (!(await hostKeys.ask(tab.id, r))) throw new Error('host key not trusted')
        r = await hub.filesList(server, p, { fingerprint: r.fingerprint, keyType: r.keyType })
        onTrusted()
      }
      if (r.status === 'hostKeyMismatch') { onMismatch(r); throw new Error('host key mismatch') }
      setListing(r); setPathInput(r.path); shown.current = r.path
      setSel({ names: new Set() }); setScrollTop(0); listRef.current?.scrollTo(0, 0)
      lastFolder.set(localStore(), server, r.path)
      return true
    } catch (e) {
      setListing(undefined); setError((e as Error).message)
      return false
    } finally { setLoading(false) }
  }, [server, tab.id, hostKeys, onMismatch, onTrusted])

  // Lists once when the tab opens (the last folder, else home), then only on
  // the author's own actions: never on a timer (see CLAUDE.md, idle lock).
  useEffect(() => {
    const last = lastFolder.get(localStore(), server)
    void load(last ?? '').then((ok) => { if (!ok && last) void load('') })
    return () => {
      hostKeys.drop(tab.id)
      for (const [id, j] of jobs) if (j.state !== 'done') hub.filesCancel(id) // closing the tab cancels its jobs
      for (const off of offs.values()) off()
    }
  }, []) // once per tab

  const patch = (id: string, p: Partial<Job>) => { const j = jobs.get(id); if (j) { jobs.set(id, { ...j, ...p }); bump() } }

  const finishJob = (id: string, done?: FilesDone, error?: string) => {
    const j = jobs.get(id)
    if (!j || j.state === 'done') return
    offs.get(id)?.(); offs.delete(id)
    patch(id, { state: 'done', done, error })
    setDialog((d) => (d && 'id' in d && d.id === id ? undefined : d))
    if (done && j.op !== 'download' && j.folder === shown.current) void load(shown.current)
  }

  const runJob = async (id: string, conflict: 'skip' | 'overwrite' | 'ask') => {
    patch(id, { state: 'running' })
    setDialog(undefined)
    try { await hub.filesRun(id, conflict) } catch (e) { hub.filesCancel(id); finishJob(id, undefined, (e as Error).message) }
  }

  const onJobEvent = (id: string, e: TermEvent) => {
    const j = jobs.get(id)
    if (!j) return
    if (e.method === 'files.planned') {
      const planned = e.params
      patch(id, { planned })
      if (j.op === 'delete') {
        patch(id, { state: 'confirm' })
        setDialog({ kind: 'delete', id, planned, names: j.names ?? [], hasFolder: !!j.hasFolder })
      } else if (j.op === 'upload' && planned.conflicts.count > 0) {
        patch(id, { state: 'confirm' })
        setDialog({ kind: 'conflict', id, planned })
      } else void runJob(id, j.op === 'download' ? 'ask' : 'skip') // main asks about download conflicts
    } else if (e.method === 'files.progress') patch(id, { progress: e.params })
    else if (e.method === 'files.done') finishJob(id, e.params)
    else if (e.method === 'hub.stopped') finishJob(id, undefined, 'hub restarted')
  }

  const startJob = async (op: FileOp, sources: string[], dest: string | undefined, label: string, extra: Partial<Job> = {}) => {
    const id = newJobId()
    jobs.set(id, { id, op, label, folder: shown.current, state: 'planning', ...extra }); bump()
    offs.set(id, events.on(id, (e) => onJobEvent(id, e)))
    try { await hub.filesPlan(id, server, op, sources, dest) } catch (e) { finishJob(id, undefined, (e as Error).message) }
  }

  const label = (n: string[]) => (n.length === 1 ? n[0] : `${n.length} items`)
  const upload = async (mode: 'files' | 'folder' | 'both') => {
    try {
      const picks = await hub.pickUpload(server, shown.current, mode)
      if (picks.length) await startJob('upload', picks.map((p) => p.token), shown.current, label(picks.map((p) => p.name)))
    } catch (e) { setError((e as Error).message) }
  }
  const download = async () => {
    if (!selected.length) return
    try {
      const d = await hub.pickDownloadDir(server)
      if (d) await startJob('download', selected.map((x) => joinPath(shown.current, x.name)), d.token, label(selected.map((x) => x.name)))
    } catch (e) { setError((e as Error).message) }
  }
  const remove = () => {
    if (!selected.length) return
    const names = selected.map((x) => x.name)
    void startJob('delete', names.map((n) => joinPath(shown.current, n)), undefined, label(names),
      { names, hasFolder: selected.some((x) => x.kind === 'dir') })
  }
  const drop = async (e: DragEvent) => {
    e.preventDefault(); setDropping(false)
    const fl = Array.from(e.dataTransfer.files)
    if (!fl.length || !listing) return
    try {
      const picks = await hub.grantDropped(fl, server)
      if (picks.length) await startJob('upload', picks.map((p) => p.token), shown.current, label(picks.map((p) => p.name)))
    } catch (err) { setError((err as Error).message) }
  }
  const open = (x: FileEntry) => { if (x.kind === 'dir' || x.kind === 'link') void load(joinPath(shown.current, x.name)) }
  const up = () => { const p = parentPath(listing ? shown.current : pathInput); if (p !== undefined) void load(p) }
  const click = (x: FileEntry, e: MouseEvent) => {
    setSel(nextSelection(sel.names, rows.map((r) => r.name), x.name, { toggle: e.metaKey || e.ctrlKey, range: e.shiftKey }, sel.anchor))
  }
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && selected.length === 1) { e.preventDefault(); open(selected[0]) }
    else if (e.key === 'Backspace') { e.preventDefault(); up() }
    else if (e.key === 'F2' && selected.length === 1) { e.preventDefault(); setDialog({ kind: 'rename', from: selected[0].name }) }
    else if (e.key === 'Delete') { e.preventDefault(); remove() }
  }
  const submitName = async (name: string): Promise<string | undefined> => {
    try {
      if (dialog?.kind === 'mkdir') await hub.filesMkdir(server, joinPath(shown.current, name))
      if (dialog?.kind === 'rename') await hub.filesRename(server, joinPath(shown.current, dialog.from), joinPath(shown.current, name))
      setDialog(undefined)
      await load(shown.current)
      return undefined
    } catch (e) { return (e as Error).message }
  }
  const sortBy = (key: SortKey) => setSort((s) => ({ key, desc: s.key === key ? !s.desc : false }))
  const mac = navigator.platform.startsWith('Mac')
  const [start, end] = windowRange(scrollTop, viewport, ROW, rows.length)
  const jobList = [...jobs.values()]

  return (
    <div className="filesview" style={{ display: visible ? 'flex' : 'none' }}>
      <div className="files-toolbar">
        <button type="button" className="icon" aria-label="Up" title="Up" onClick={up}><UpIcon /></button>
        <form className="files-path" onSubmit={(e) => { e.preventDefault(); void load(pathInput.trim()) }}>
          <input className="mono" aria-label="Path" value={pathInput} onChange={(e) => setPathInput(e.target.value)} spellCheck={false} />
        </form>
        <button type="button" className="icon" aria-label="Refresh" title="Refresh" onClick={() => void load(shown.current)}><RefreshIcon /></button>
        <label className="files-hidden"><input type="checkbox" checked={showHidden}
          onChange={(e) => { setShowHidden(e.target.checked); hiddenPref.set(localStore(), e.target.checked) }} />Show hidden files</label>
        <span className="spacer" />
        <button type="button" className="btn" disabled={!listing} onClick={() => setDialog({ kind: 'mkdir' })}><PlusIcon />New folder</button>
        {mac ? (
          <button type="button" className="btn" disabled={!listing} onClick={() => void upload('both')}><UploadIcon />Upload</button>
        ) : (<>
          <button type="button" className="btn" disabled={!listing} onClick={() => void upload('files')}><UploadIcon />Upload files</button>
          <button type="button" className="btn" disabled={!listing} onClick={() => void upload('folder')}><UploadIcon />Upload folder</button>
        </>)}
        <button type="button" className="btn" disabled={!selected.length} onClick={() => void download()}><DownloadIcon />Download</button>
        <button type="button" className="btn" disabled={selected.length !== 1} onClick={() => setDialog({ kind: 'rename', from: selected[0].name })}><EditIcon />Rename</button>
        <button type="button" className="btn danger-outline" disabled={!selected.length} onClick={remove}><TrashIcon />Delete</button>
      </div>
      <div className={'files-body' + (dropping ? ' dropping' : '')}
        onDragOver={(e) => { e.preventDefault(); setDropping(true) }} onDragLeave={() => setDropping(false)} onDrop={(e) => void drop(e)}>
        <div className="files-head" role="presentation">
          {(['name', 'size', 'mtime', 'mode'] as SortKey[]).map((k) => (
            <button key={k} type="button" className={'files-col ' + k} onClick={() => sortBy(k)}>
              {{ name: 'Name', size: 'Size', mtime: 'Modified', mode: 'Permissions' }[k]}{sort.key === k ? (sort.desc ? ' ↓' : ' ↑') : ''}
            </button>
          ))}
        </div>
        {error ? <p className="files-note error">{displayText(error)}</p> : null}
        {listing?.truncated && <p className="files-note muted">Showing the first 10,000 entries.</p>}
        {listing && listing.bad > 0 && <p className="files-note muted">{`${listing.bad} ${listing.bad === 1 ? 'name is' : 'names are'} not shown: not safe to display or copy.`}</p>}
        <div className="fileslist" ref={listRef} role="grid" aria-label={`Files on ${server}`} aria-busy={loading} tabIndex={0}
          onKeyDown={onKey} onScroll={(e) => setScrollTop(e.currentTarget.scrollTop)}>
          <div style={{ height: start * ROW }} />
          {rows.slice(start, end).map((x) => (
            <div key={x.name} role="row" data-name={x.name} aria-selected={sel.names.has(x.name)}
              className={'file-row' + (sel.names.has(x.name) ? ' selected' : '')}
              onClick={(e) => click(x, e)} onDoubleClick={() => open(x)}>
              <span className="files-col name">
                {x.kind === 'dir' ? <FolderIcon /> : x.kind === 'link' ? <LinkIcon /> : <FileIcon />}
                <span className="mono">{displayText(x.name)}</span>
                {x.target !== undefined && <span className="muted mono">{` → ${displayText(x.target)}`}</span>}
              </span>
              <span className="files-col size">{x.kind === 'dir' ? '—' : formatSize(x.size)}</span>
              <span className="files-col mtime">{formatTime(x.mtime)}</span>
              <span className="files-col mode mono">{formatMode(x.kind, x.mode)}</span>
            </div>
          ))}
          <div style={{ height: (rows.length - end) * ROW }} />
          {listing && rows.length === 0 && <p className="files-note muted">Empty folder.</p>}
        </div>
      </div>
      {jobList.length > 0 && (
        <ul className="transfers" aria-label="Transfers">
          {jobList.map((j) => (
            <li key={j.id} className="transfer">
              <span className="transfer-label">{`${{ upload: 'Upload', download: 'Download', delete: 'Delete' }[j.op]} ${displayText(j.label)}`}</span>
              {j.state === 'planning' && <span className="muted">Checking…</span>}
              {j.state === 'confirm' && <span className="muted">Waiting for you</span>}
              {j.state === 'running' && (
                <>
                  <progress max={j.progress?.total || j.planned?.bytes || 1} value={j.progress?.done ?? 0} />
                  <span className="muted mono">{j.progress ? `${formatSize(j.progress.done)} of ${formatSize(j.progress.total)} · ${displayText(j.progress.file)}` : ''}</span>
                </>
              )}
              {j.state === 'done' && <span>{displayText(doneSummary(j.done, j.error))}</span>}
              {j.state !== 'done'
                ? <button type="button" className="btn" onClick={() => hub.filesCancel(j.id)}>Cancel</button>
                : <button type="button" className="icon" aria-label="Dismiss" onClick={() => { jobs.delete(j.id); bump() }}><CloseIcon /></button>}
              {j.done && j.done.errors.length > 0 && (
                <ul className="transfer-errors mono">
                  {j.done.errors.map((m, i) => <li key={i}>{displayText(m)}</li>)}
                  {j.done.errorCount > j.done.errors.length && <li className="muted">{`and ${j.done.errorCount - j.done.errors.length} more`}</li>}
                </ul>
              )}
            </li>
          ))}
        </ul>
      )}
      {dialog?.kind === 'conflict' && (
        <ConflictDialog planned={dialog.planned} onChoice={(c) => {
          if (c === 'cancel') { hub.filesCancel(dialog.id); setDialog(undefined) } else void runJob(dialog.id, c)
        }} />
      )}
      {dialog?.kind === 'delete' && (
        <DeleteDialog names={dialog.names} planned={dialog.planned}
          needsTyping={deleteNeedsTyping(dialog.hasFolder, dialog.names.length, dialog.planned)}
          onDelete={() => void runJob(dialog.id, 'skip')} onCancel={() => { hub.filesCancel(dialog.id); setDialog(undefined) }} />
      )}
      {(dialog?.kind === 'mkdir' || dialog?.kind === 'rename') && (
        <NameDialog title={dialog.kind === 'mkdir' ? 'New folder' : 'Rename'} initial={dialog.kind === 'rename' ? dialog.from : ''}
          onSubmit={submitName} onCancel={() => setDialog(undefined)} />
      )}
    </div>
  )
}

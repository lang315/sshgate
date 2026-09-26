import { useEffect, useState, type FormEvent, type KeyboardEvent } from 'react'
import type { ImportCandidate, ImportResult, ImportScan } from '../shared/protocol'
import { CloseIcon } from './icons'

const LABEL = { ready: 'Ready', exists: 'Already in vault', skipped: 'Skipped' } as const

// The list alone, so it renders without a hub.
export function ImportRows({ candidates, checked, onToggle }: {
  candidates: ImportCandidate[]; checked: ReadonlySet<string>; onToggle: (alias: string) => void
}) {
  return (
    <ul className="importlist">
      {candidates.map((c) => (
        <li key={c.alias} className="importrow">
          <div className="importrow-main">
            {c.status === 'ready' && (
              <input type="checkbox" aria-label={`Import ${c.alias}`} checked={checked.has(c.alias)} onChange={() => onToggle(c.alias)} />
            )}
            <span className="importrow-name">{c.alias}</span>
            <span className={'chip ' + (c.status === 'ready' ? 'ok' : 'wait')}>{LABEL[c.status]}</span>
          </div>
          {c.status === 'skipped'
            ? <span className="muted">{c.reason}</span>
            : <span className="mono muted">{`${c.user}@${c.host}:${c.port} · ${c.auth === 'key' ? c.keyPath : 'agent'}`}</span>}
          {c.status === 'ready' && <code className="fp">{c.hostKey ? `${c.hostKeyAlgo} ${c.hostKey}` : 'not in known_hosts'}</code>}
          {c.status === 'ready' && c.needsPassphrase && <span className="muted">Key has a passphrase: add it in the editor after import.</span>}
        </li>
      ))}
    </ul>
  )
}

// The first scan checks every ready row; a rescan keeps the user's choice.
export function nextChecked(candidates: ImportCandidate[], prev?: ReadonlySet<string>): Set<string> {
  return new Set(candidates.filter((c) => c.status === 'ready' && (!prev || prev.has(c.alias))).map((c) => c.alias))
}

// The hub gets alias names only.
export function ImportSheet({ scan, apply, onImported, onClose }: {
  scan: () => Promise<ImportScan>; apply: (aliases: string[]) => Promise<ImportResult>
  onImported: () => Promise<void>; onClose: () => void
}) {
  const [list, setList] = useState<ImportScan>()
  const [checked, setChecked] = useState<Set<string>>(() => new Set())
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(true)
  const load = async (rescan = false) => {
    setBusy(true)
    try {
      const s = await scan()
      setList(s)
      setChecked((prev) => nextChecked(s.candidates, rescan ? prev : undefined))
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  useEffect(() => { load() }, [])
  const toggle = (alias: string) => setChecked((s) => {
    const n = new Set(s)
    if (!n.delete(alias)) n.add(alias)
    return n
  })
  const submit = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setError(undefined)
    try {
      const r = await apply([...checked])
      await onImported()
      if (r.skipped.length === 0) { onClose(); return }
      setError('Not imported: ' + r.skipped.map((s) => `${s.alias} (${s.reason})`).join('; '))
      await load(true)
    } catch (err) { setError((err as Error).message) } finally { setBusy(false) }
  }
  const escape = (e: KeyboardEvent) => { if (e.key === 'Escape' && !busy) { e.preventDefault(); onClose() } }
  const n = checked.size
  return (
    <div className="sheet-layer">
      <form className="sheet" role="dialog" aria-label="Import from SSH config" onSubmit={submit} onKeyDown={escape}>
        <header className="sheet-head">
          <h3>Import from SSH config</h3>
          <button type="button" className="icon" aria-label="Close import" title="Close" onClick={onClose}><CloseIcon /></button>
        </header>
        <div className="sheet-body">
          <p className="muted">Host keys come from your known_hosts. With <code>StrictHostKeyChecking accept-new</code>, OpenSSH accepted them without asking you; compare them with the server if unsure.</p>
          {list?.note && <p className="empty">{list.note}</p>}
          {list ? <ImportRows candidates={list.candidates} checked={checked} onToggle={toggle} />
            : busy && <p className="muted">Reading your SSH config…</p>}
        </div>
        <footer className="sheet-foot">
          {error && <p className="error">{error}</p>}
          <span className="spacer" />
          <button type="button" className="btn" autoFocus onClick={onClose}>Close</button>
          <button type="submit" className="btn primary" disabled={busy || n === 0}>{`Import ${n} ${n === 1 ? 'host' : 'hosts'}`}</button>
        </footer>
      </form>
    </div>
  )
}

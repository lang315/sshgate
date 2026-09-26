import { useState, type FormEvent } from 'react'
import type { FilesPlanned } from '../shared/protocol'
import { displayText } from '../shared/display'
import { DELETE_WORD, formatSize, nameError } from './files'
import { TrashIcon, WarningIcon } from './icons'

// Cancel is the default (Enter) in both risky dialogs.
export function ConflictDialog({ planned, onChoice }: { planned: FilesPlanned; onChoice: (c: 'cancel' | 'skip' | 'overwrite') => void }) {
  const n = planned.conflicts.count
  return (
    <div className="modal" role="dialog" aria-label="Files already exist">
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onChoice('cancel') }}>
        <div className="dialog-title"><span className="dialog-icon"><WarningIcon /></span>
          <h3>{`${n} ${n === 1 ? 'file already exists' : 'files already exist'}`}</h3></div>
        <ul className="files-sample mono">{planned.conflicts.sample.map((s) => <li key={s}>{displayText(s)}</li>)}</ul>
        {n > planned.conflicts.sample.length && <p className="muted">{`and ${n - planned.conflicts.sample.length} more`}</p>}
        <div className="dialog-actions">
          <button type="button" className="btn danger-outline" onClick={() => onChoice('overwrite')}>Overwrite all</button>
          <button type="button" className="btn" onClick={() => onChoice('skip')}>Skip existing</button>
          <button type="submit" className="btn primary" autoFocus>Cancel<kbd aria-hidden="true">↵</kbd></button>
        </div>
      </form>
    </div>
  )
}

export function DeleteDialog({ names, planned, needsTyping, onDelete, onCancel }: {
  names: string[]; planned: FilesPlanned; needsTyping: boolean; onDelete: () => void; onCancel: () => void
}) {
  const [typed, setTyped] = useState('')
  const ok = !needsTyping || typed === DELETE_WORD
  return (
    <div className="modal" role="dialog" aria-label="Delete files">
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onCancel() }}>
        <div className="dialog-title"><span className="dialog-icon danger"><TrashIcon /></span><h3>Delete for good?</h3></div>
        <ul className="files-sample mono">{names.slice(0, 10).map((n) => <li key={n}>{displayText(n)}</li>)}</ul>
        {names.length > 10 && <p className="muted">{`and ${names.length - 10} more`}</p>}
        <p>{`${planned.files} files, ${planned.dirs} folders, ${planned.links} links · ${formatSize(planned.bytes)}. There is no undo.`}</p>
        {needsTyping && (
          <label className="field">{`Type ${DELETE_WORD} to confirm`}
            <input value={typed} onChange={(e) => setTyped(e.target.value)} aria-label={`Type ${DELETE_WORD} to confirm`} />
          </label>
        )}
        <div className="dialog-actions">
          <button type="button" className="btn danger-outline" disabled={!ok} onClick={onDelete}>Delete</button>
          <button type="submit" className="btn primary" autoFocus={!needsTyping}>Cancel<kbd aria-hidden="true">↵</kbd></button>
        </div>
      </form>
    </div>
  )
}

export function NameDialog({ title, initial, onSubmit, onCancel }: {
  title: 'New folder' | 'Rename'; initial: string; onSubmit: (name: string) => Promise<string | undefined>; onCancel: () => void
}) {
  const [name, setName] = useState(initial)
  const [err, setErr] = useState<string>()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const bad = nameError(name)
    if (bad) return setErr(bad)
    setErr(await onSubmit(name))
  }
  return (
    <div className="modal" role="dialog" aria-label={title}>
      <form className="dialog" onSubmit={submit} onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }}>
        <div className="dialog-title"><h3>{title}</h3></div>
        <label className="field">Name<input value={name} onChange={(e) => setName(e.target.value)} autoFocus aria-label="Name" /></label>
        {err && <p className="error">{displayText(err)}</p>}
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={onCancel}>Cancel</button>
          <button type="submit" className="btn primary">{title === 'Rename' ? 'Rename' : 'Create'}</button>
        </div>
      </form>
    </div>
  )
}

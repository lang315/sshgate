import { useEffect, useId, useRef, useState } from 'react'
import type { SoftLockLimit } from '../shared/protocol'
import type { TouchStatus } from '../shared/touchid'
import { blockKeyboardActivation, ListChanges } from './approvals'
import { ChevronDownIcon, SettingsIcon } from './icons'
import { LIMITS, limitChange, limitHelp, limitNotice, saveAllowed, settingsChangeKey, touchChange, type TouchChange } from './settings'

// App settings (spec 2026-10-12-soft-lock-limit-design.md, "UI and UX"): one
// dialog of sections, each a heading and label/control rows; Save and Cancel
// cover the whole dialog. Today: Auto-allow, one row.
export function SettingsDialog({ limit, hosts, touch, onSave, onCancel }: {
  limit: SoftLockLimit; hosts: string[]; touch?: TouchStatus
  onSave: (limit: SoftLockLimit, touch: TouchChange, password: string) => Promise<void>; onCancel: () => void
}) {
  const id = useId()
  const [chosen, setChosen] = useState(limit)
  const [touchOn, setTouchOn] = useState(touch?.enabled ?? false)
  const [password, setPassword] = useState('')
  const tc = touchChange(touch?.enabled ?? false, touchOn)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [openedAt] = useState(Date.now())
  const [now, setNow] = useState(openedAt)
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 100); return () => clearInterval(t) }, [])
  const change = limitChange(limit, chosen)
  // Computed during render, as AutoAllowDialog does: an effect would leave one
  // painted frame where the notice moved Save but Save was still enabled.
  const key = settingsChangeKey(chosen, hosts, error, tc === 'on')
  const changes = useRef<ListChanges>(null)
  changes.current ??= new ListChanges(key, openedAt)
  changes.current.setKey(key, Date.now())
  const ok = saveAllowed({ change, touch: tc, password, busy, openedAt, changedAt: changes.current.at, now })
  const notice = change === 'higher' || change === 'none'
  const save = async () => {
    if (!ok) return
    setBusy(true); setError(undefined)
    try { await onSave(chosen, tc, password) } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  return (
    <div className="modal" role="dialog" aria-modal="true" aria-labelledby={`${id}-title`}
      onKeyDown={(e) => { if (e.key === 'Escape' && !busy) onCancel() }}>
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); if (!busy) onCancel() }}>
        <div className="dialog-title"><span className="dialog-icon"><SettingsIcon /></span><h3 id={`${id}-title`}>Settings</h3></div>
        {touch?.supported && (
          <section className="settings-section" aria-labelledby={`${id}-unlock`}>
            <h4 id={`${id}-unlock`}>Unlock</h4>
            <div className="switch">
              <label htmlFor={`${id}-touch`} className="switch-title">Unlock with Touch ID</label>
              <input id={`${id}-touch`} type="checkbox" role="switch" checked={touchOn}
                disabled={busy || (!touch.available && !touchOn)}
                onChange={(e) => { setTouchOn(e.target.checked); setPassword('') }} />
            </div>
            {!touch.available && <p className="muted">Touch ID is not available on this Mac right now.</p>}
            {tc === 'on' && (
              <div className="field">
                <label htmlFor={`${id}-pw`}>Master password</label>
                <input id={`${id}-pw`} type="password" autoFocus value={password}
                  onChange={(e) => setPassword(e.target.value)} disabled={busy} />
              </div>
            )}
          </section>
        )}
        <section className="settings-section" aria-labelledby={`${id}-auto`}>
          <h4 id={`${id}-auto`}>Auto-allow</h4>
          <div className="settings-row">
            <label htmlFor={`${id}-limit`}>Keep running while locked</label>
            <span className={'select' + (chosen === 'none' ? ' danger' : '')}>
              <select id={`${id}-limit`} value={chosen} onChange={(e) => setChosen(e.target.value as SoftLockLimit)}>
                {LIMITS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
              </select>
              <ChevronDownIcon />
            </span>
            <p className="muted">{limitHelp(chosen)}</p>
          </div>
        </section>
        <div aria-live="polite">
          {notice && (
            <div className={change === 'none' ? 'banner-danger' : 'banner-wait'}>
              <p>{`${limitNotice(chosen)} ${hosts.length ? 'On auto-allow now:' : 'No host is on auto-allow now. The limit applies from the next grant.'}`}</p>
              {hosts.length > 0 && <ul className="files-sample mono">{hosts.map((h) => <li key={h}>{h}</li>)}</ul>}
            </div>
          )}
        </div>
        {error && <p className="error">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className={'btn' + (notice ? ' danger-outline' : '')} tabIndex={-1} disabled={!ok}
            onKeyDown={blockKeyboardActivation} onClick={save}>Save</button>
          <button type="submit" className="btn primary" autoFocus disabled={busy}>Cancel<kbd aria-hidden="true">↵</kbd></button>
        </div>
      </form>
    </div>
  )
}

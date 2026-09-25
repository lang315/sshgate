import { useState, type FormEvent } from 'react'
import type { ServerInfo } from '../shared/protocol'
import { passwordChecks } from './hostForm'
import { CheckIcon, Mark, WarningIcon } from './icons'

export function CreateVault({ servers, onCreate }: { servers: ServerInfo[]; onCreate: (pw: string) => Promise<void> }) {
  const [pw, setPw] = useState('')
  const [again, setAgain] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const checks = passwordChecks(pw, again)
  const ok = checks.length && checks.match
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!ok) return
    setBusy(true); setError(undefined)
    try { await onCreate(pw) } catch (err) { setError((err as Error).message) } finally { setBusy(false) }
  }
  return (
    <div className="lockpage">
      <form className="lockcard" onSubmit={submit}>
        <Mark />
        <h1>Create your vault</h1>
        <p className="muted">The master password encrypts saved passwords and protects the host list.</p>
        <div className="field"><label htmlFor="cv-pw">New master password</label>
          <input id="cv-pw" type="password" autoFocus value={pw} onChange={(e) => setPw(e.target.value)} disabled={busy} /></div>
        <div className="field"><label htmlFor="cv-again">Confirm master password</label>
          <input id="cv-again" type="password" value={again} onChange={(e) => setAgain(e.target.value)} disabled={busy} /></div>
        <ul className="checklist" aria-live="polite">
          <li className={checks.length ? 'done' : ''}><CheckIcon />At least 8 characters{checks.length ? '' : ' (not yet)'}</li>
          <li className={checks.match ? 'done' : ''}><CheckIcon />Both passwords match{checks.match ? '' : ' (not yet)'}</li>
        </ul>
        <p className="box wait"><WarningIcon />The master password cannot be recovered. If you forget it, this vault cannot be unlocked and its saved passwords are lost.</p>
        {servers.length > 0 && (
          <div className="box">
            <p className="muted">These servers from your existing file are kept, with Visible to AI turned off and their host keys unpinned; you&apos;ll confirm each key on the next connect.</p>
            <ul className="keptlist">{servers.map((s) => <li key={s.name}>{s.name} <span className="mono muted">{`${s.user}@${s.host}:${s.port}`}</span></li>)}</ul>
          </div>
        )}
        {error && <p className="error">{error}</p>}
        <button type="submit" className="btn primary block" disabled={busy || !ok}>Create vault</button>
      </form>
    </div>
  )
}

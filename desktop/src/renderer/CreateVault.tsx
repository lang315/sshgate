import { useState, type FormEvent } from 'react'
import type { ServerInfo } from '../shared/protocol'
import { vaultPasswordProblem } from './hostForm'

export function CreateVault({ servers, onCreate }: { servers: ServerInfo[]; onCreate: (pw: string) => Promise<void> }) {
  const [pw, setPw] = useState('')
  const [again, setAgain] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const problem = vaultPasswordProblem(pw, again)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (problem) return
    setBusy(true); setError(undefined)
    try { await onCreate(pw) } catch (err) { setError((err as Error).message) } finally { setBusy(false) }
  }
  return (
    <form className="unlock" onSubmit={submit}>
      <h2>Create vault</h2>
      <p className="muted">The master password encrypts saved passwords and protects the host list. It cannot be recovered.</p>
      <input type="password" autoFocus aria-label="New master password" value={pw}
        onChange={(e) => setPw(e.target.value)} disabled={busy} />
      <input type="password" aria-label="Confirm master password" value={again}
        onChange={(e) => setAgain(e.target.value)} disabled={busy} />
      {pw !== '' && problem && <p className="muted">{problem}</p>}
      <button type="submit" disabled={busy || !!problem}>Create vault</button>
      {servers.length > 0 && (
        <div>
          <p className="muted">These servers are kept, with "Visible to AI" turned off; turn it back on in the host editor.</p>
          <ul>{servers.map((s) => <li key={s.name}>{s.name} <span className="muted">{`${s.user}@${s.host}:${s.port}`}</span></li>)}</ul>
        </div>
      )}
      {error && <p className="error">{error}</p>}
    </form>
  )
}

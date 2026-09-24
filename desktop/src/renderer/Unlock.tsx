import { useState, type FormEvent } from 'react'

export function Unlock({ onUnlock, error }: { onUnlock: (pw: string) => Promise<void>; error?: string }) {
  const [pw, setPw] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try { await onUnlock(pw) } finally { setBusy(false); setPw('') }
  }
  return (
    <form className="unlock" onSubmit={submit}>
      <h2>Unlock vault</h2>
      <input type="password" autoFocus aria-label="Master password" value={pw}
        onChange={(e) => setPw(e.target.value)} disabled={busy} />
      <button type="submit" disabled={busy || pw === ''}>Unlock</button>
      {error && <p className="error">{error}</p>}
    </form>
  )
}

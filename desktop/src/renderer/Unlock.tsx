import { useState, type FormEvent } from 'react'
import { Mark } from './icons'

// lockReason is set after an idle or manual lock of a running hub; after a hub
// restart it is undefined, because the old terminals have ended. autoHosts is
// non-empty under a soft lock: the AI still runs on those hosts, and onStop
// (lock with stopAuto) ends that without the password.
export function Unlock({ onUnlock, error, lockReason, storePath, autoHosts = [], onStop }: {
  onUnlock: (pw: string) => Promise<void>; error?: string; lockReason?: 'idle' | 'manual'; storePath?: string
  autoHosts?: string[]; onStop?: () => void
}) {
  const [pw, setPw] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try { await onUnlock(pw) } finally { setBusy(false); setPw('') }
  }
  return (
    <div className="lockpage">
      <form className="lockcard" onSubmit={submit}>
        <Mark />
        <h1>Unlock vault</h1>
        {lockReason === 'idle' && <span className="chip wait lockchip">Locked after inactivity.</span>}
        {lockReason && autoHosts.length === 0 && <p className="muted">Open terminals stay connected while locked. AI requests are refused until you unlock.</p>}
        {autoHosts.length > 0 && (
          <div className="banner auto" role="status">
            <span>{`AI auto-allow is still running on: ${autoHosts.join(', ')}`}</span>
            <button type="button" className="btn sm" onClick={onStop}>Stop auto-allow and lock</button>
          </div>
        )}
        <div className="field">
          <label htmlFor="unlock-pw">Master password</label>
          <input id="unlock-pw" type="password" autoFocus value={pw} onChange={(e) => setPw(e.target.value)} disabled={busy} />
        </div>
        {error && <p className="error">{error}</p>}
        <button type="submit" className="btn primary block" disabled={busy || pw === ''}>Unlock</button>
      </form>
      {storePath && <p className="lockpath muted">Vault file <code>{storePath}</code></p>}
    </div>
  )
}

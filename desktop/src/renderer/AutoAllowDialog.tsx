import { useEffect, useState } from 'react'
import type { AutoAllowCheck, AutoAllowMode, ServerInfo } from '../shared/protocol'
import { blockKeyboardActivation } from './approvals'
import { AUTO_MODES, enableAllowed } from './autoallow'

type Mode = Exclude<AutoAllowMode, 'off'>

export function AutoAllowDialog({ server, remoteTunnels, check, onEnable, onCancel }: {
  server: ServerInfo; remoteTunnels: string[]
  check: () => Promise<AutoAllowCheck>
  onEnable: (mode: Mode) => Promise<void>; onCancel: () => void
}) {
  const [mode, setMode] = useState<Mode>('15m')
  const [typed, setTyped] = useState('')
  const [checked, setChecked] = useState<AutoAllowCheck | 'error'>()
  const [openedAt] = useState(Date.now())
  const [changedAt, setChangedAt] = useState(openedAt)
  const [now, setNow] = useState(openedAt)
  const [error, setError] = useState<string>()
  const refused = server.autoAllowRefused
  useEffect(() => { if (!refused) check().then(setChecked, () => setChecked('error')) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  // Content that moves under the cursor restarts the Enable delay.
  useEffect(() => { setChangedAt(Date.now()) }, [mode, checked])
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 100); return () => clearInterval(t) }, [])
  const root = checked !== undefined && checked !== 'error' && (checked.uid === 0 || checked.passwordlessSudo)
  const ok = enableAllowed({ mode, typed, host: server.name, refused, openedAt, changedAt, now })
  const enable = () => { if (ok) onEnable(mode).catch((e) => setError((e as Error).message)) }
  return (
    <div className="modal" role="dialog" aria-modal="true" aria-label={`Auto-allow AI commands on ${server.name}`}>
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onCancel() }}>
        <div className="dialog-title"><h3>{`Auto-allow AI commands on ${server.name}`}</h3></div>
        {refused ? (
          <p className="error">{`Auto-allow is not available here: ${refused}`}</p>
        ) : (
          <>
            <p>{`The AI runs any command this account can run on ${server.name} without asking, including commands planted by what it reads (prompt injection), and can leave things that run later (cron jobs, SSH keys, shell startup files). sudo-exec still asks.`}</p>
            {root && <p className="error">This is root access: the AI can do anything on this host.</p>}
            {checked === 'error' && <p className="muted">Could not check this host&apos;s sudo access.</p>}
            {remoteTunnels.length > 0 && (
              <div className="banner-danger">
                <p>Remote tunnels running on this host reach your machine:</p>
                <ul className="files-sample mono">{remoteTunnels.map((t) => <li key={t}>{t}</li>)}</ul>
              </div>
            )}
            <fieldset className="kinds">
              <legend>For how long</legend>
              {AUTO_MODES.map((m) => (
                <label key={m.mode}><input type="radio" name="aa-mode" checked={mode === m.mode} onChange={() => setMode(m.mode)} />{m.label}</label>
              ))}
            </fieldset>
            <p className="muted">{mode === 'forever'
              ? 'Stays set after restart. After each unlock it waits for you to click Resume.'
              : 'Ends at its time, when you stop it, or when you lock the vault. The vault will not auto-lock before then.'}</p>
            {mode === 'forever' && (
              <label className="field">{`Type ${server.name} to confirm`}<input value={typed} onChange={(e) => setTyped(e.target.value)} /></label>
            )}
          </>
        )}
        {error && <p className="error">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="btn danger-outline" tabIndex={-1} disabled={!ok} onKeyDown={blockKeyboardActivation} onClick={enable}>Enable</button>
          <button type="submit" className="btn primary" autoFocus>Cancel<kbd aria-hidden="true">↵</kbd></button>
        </div>
      </form>
    </div>
  )
}

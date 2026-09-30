import { useEffect, useRef, useState } from 'react'
import type { AutoAllowCheck, AutoAllowMode, ServerInfo } from '../shared/protocol'
import { blockKeyboardActivation, ListChanges } from './approvals'
import { AUTO_MODES, dialogChangeKey, enableAllowed } from './autoallow'

type Mode = Exclude<AutoAllowMode, 'off'>

// Confirms the Host editor's Auto-allow choice before Save sends it (spec
// Amendment 2026-09-29): the duration comes from the editor, not a radio here.
export function AutoAllowDialog({ server, mode, typeName, rootNew, sudoNew, sudo, refused, remoteTunnels, check, onEnable, onCancel }: {
  server: ServerInfo; mode: Mode; typeName: boolean; rootNew: boolean; sudoNew: boolean; sudo: boolean; refused?: string
  remoteTunnels: string[]
  check: () => Promise<AutoAllowCheck>
  onEnable: () => Promise<void>; onCancel: () => void
}) {
  const [typed, setTyped] = useState('')
  const [checked, setChecked] = useState<AutoAllowCheck | 'error'>()
  const [openedAt] = useState(Date.now())
  const [now, setNow] = useState(openedAt)
  const [error, setError] = useState<string>()
  useEffect(() => { if (!refused) check().then(setChecked, () => setChecked('error')) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 100); return () => clearInterval(t) }, [])
  // Content that moves under the cursor restarts the Enable delay. Computed
  // during render, like ApprovalPanel's ListChanges, not in a useEffect: an
  // effect runs after paint, so the check() resolving would leave one painted
  // frame where the buttons already shifted but Enable was still enabled.
  const changeKey = dialogChangeKey(mode, rootNew, sudoNew, sudo, checked, error, remoteTunnels)
  const changes = useRef<ListChanges>(null)
  changes.current ??= new ListChanges(changeKey, openedAt)
  changes.current.setKey(changeKey, Date.now())
  const changedAt = changes.current.at
  const root = checked !== undefined && checked !== 'error' && (checked.uid === 0 || checked.passwordlessSudo)
  const modeInfo = AUTO_MODES.find((m) => m.mode === mode)!
  const endsAt = new Date(now + (modeInfo.ms ?? 0)).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const chosen = mode === 'forever' ? 'Until turned off' : `For ${modeInfo.label} (ends at ${endsAt})`
  const ok = enableAllowed({ typeName, typed, host: server.name, refused, openedAt, changedAt, now })
  const enable = () => { if (ok) onEnable().catch((e) => setError((e as Error).message)) }
  return (
    <div className="modal" role="dialog" aria-modal="true" aria-label={`Auto-allow AI commands on ${server.name}`}>
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onCancel() }}>
        <div className="dialog-title"><h3>{`Auto-allow AI commands on ${server.name}`}</h3></div>
        {refused ? (
          <p className="error">{`Auto-allow is not available here: ${refused}`}</p>
        ) : (
          <>
            <p>{`The AI runs any command this account can run on ${server.name} without asking, including commands planted by what it reads (prompt injection), and can leave things that run later (cron jobs, SSH keys, shell startup files).`}</p>
            {!sudo && <p>sudo-exec still asks.</p>}
            {rootNew && <p className="error">Allowing root: the AI runs as root, and a command it plants can capture sudo or su passwords you type or store</p>}
            {sudoNew && <p className="error">sudo-exec will run without asking: the AI has full root on this host</p>}
            {sudo && !sudoNew && <p>sudo-exec also runs without asking on this host, the same as plain exec.</p>}
            {root && <p className="error">This is root access: the AI can do anything on this host.</p>}
            {checked === 'error' && <p className="muted">Could not check this host&apos;s sudo access.</p>}
            {remoteTunnels.length > 0 && (
              <div className="banner-danger">
                <p>Remote tunnels running on this host reach your machine:</p>
                <ul className="files-sample mono">{remoteTunnels.map((t) => <li key={t}>{t}</li>)}</ul>
              </div>
            )}
            <p className="duration">{chosen}</p>
            <p className="muted">{mode === 'forever'
              ? 'Stays set after restart. After each unlock it waits for you to click Resume.'
              : `Ends at ${endsAt}, when you stop it, or when you lock the vault. The vault will not auto-lock before then.`}</p>
            {typeName && (
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

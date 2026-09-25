import { useEffect, useReducer, useRef, useState } from 'react'
import type { HostKeyMismatch, HostKeyUnknown } from '../shared/protocol'
import { ALLOW_DELAY_MS, blockKeyboardActivation, ListChanges } from './approvals'
import { promptKey, trustEnabled, type HostKeyPrompts } from './hostkeys'
import { CheckIcon, ShieldIcon, WarningIcon } from './icons'

const KNOWN_HOSTS = {
  match: { text: 'Your ~/.ssh/known_hosts lists this same key for this host.', icon: <CheckIcon /> },
  absent: { text: 'This host is not in your ~/.ssh/known_hosts.', icon: null },
  different: { text: 'Warning: your ~/.ssh/known_hosts lists a different key for this host. The connection may be intercepted.', icon: <WarningIcon /> },
}

// Cancel is the default (Enter) and has focus; Trust is mouse-only.
export function HostKeyPromptView({ info, trustEnabled: enabled, onTrust, onCancel }: {
  info: HostKeyUnknown; trustEnabled: boolean; onTrust: () => void; onCancel: () => void
}) {
  const kh = KNOWN_HOSTS[info.knownHosts]
  return (
    <div className="modal" role="dialog" aria-label="Unknown host key">
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onCancel() }}>
        <div className="dialog-title"><span className="dialog-icon"><ShieldIcon /></span><h3>Trust this host key?</h3></div>
        <p><strong>{info.server}</strong> <span className="mono muted">{`${info.user}@${info.host}:${info.port}`}</span></p>
        <p className="lead">No host key is pinned for this server yet. Check the fingerprint against one you got another way, from the server&apos;s console or its admin.</p>
        <div className="fpblock">
          <span className="fplabel">{`${info.keyType} fingerprint`}</span>
          <code className="fp">{info.fingerprint}</code>
        </div>
        <p className={`kh kh-${info.knownHosts}`}>{kh.icon}{kh.text}</p>
        <p className="muted">Trust only if this matches the key you expect. Once trusted it is pinned, and a different key later is refused.</p>
        <div className="dialog-actions">
          <button type="button" className="btn allow" tabIndex={-1} onKeyDown={blockKeyboardActivation}
            disabled={!enabled} onClick={onTrust}>Trust and connect</button>
          <button type="submit" className="btn primary" autoFocus>Cancel<kbd aria-hidden="true">↵</kbd></button>
        </div>
      </form>
    </div>
  )
}

export function HostKeyDialog({ prompts }: { prompts: HostKeyPrompts }) {
  const [, rerender] = useReducer((n: number) => n + 1, 0)
  useEffect(() => prompts.subscribe(rerender), [prompts])
  const p = prompts.current
  const changes = useRef<ListChanges>(null)
  changes.current ??= new ListChanges(promptKey(p), Date.now())
  changes.current.setKey(promptKey(p), Date.now())
  const at = changes.current.at
  const [now, setNow] = useState(Date.now())
  const enabled = trustEnabled(at, now)
  useEffect(() => {
    if (!p || enabled) return
    const t = setTimeout(() => setNow(Date.now()), Math.max(0, at + ALLOW_DELAY_MS - Date.now()) + 10)
    return () => clearTimeout(t)
  }, [p, enabled, at])
  if (!p) return null
  // Keyed per prompt so Cancel takes focus again when the next one shows.
  return <HostKeyPromptView key={promptKey(p)} info={p.info} trustEnabled={enabled}
    onTrust={() => prompts.answer(true)} onCancel={() => prompts.answer(false)} />
}

// A changed key is refused outright; the only way forward is Forget in the
// host editor, never a one-click re-pin here.
export function HostKeyMismatchDialog({ info, onClose, onEdit }: { info: HostKeyMismatch; onClose: () => void; onEdit: () => void }) {
  return (
    <div className="modal" role="dialog" aria-label="Host key mismatch">
      <div className="dialog mismatch">
        <div className="dialog-title"><span className="dialog-icon danger"><WarningIcon /></span><h3>Host key changed</h3></div>
        <p><strong>{info.server}</strong> <span className="mono muted">{`${info.user}@${info.host}:${info.port}`}</span></p>
        <p className="banner-danger">This server presented a different host key than the one pinned. The connection may be intercepted. Nothing was sent.</p>
        <div className="fpblock"><span className="fplabel">Pinned</span><code className="fp">{info.pinned}</code></div>
        <div className="fpblock"><span className="fplabel danger">Presented now</span><code className="fp danger">{info.presented}</code></div>
        <p className="lead"><strong>Did the key change on purpose?</strong> If the server was rebuilt or its keys rotated, confirm the new fingerprint with its admin, then forget the old key in the host editor and connect again.</p>
        <div className="dialog-actions">
          <button className="btn" onClick={onEdit}>Open host editor</button>
          <button className="btn primary" autoFocus onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  )
}

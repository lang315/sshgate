import { useEffect, useReducer, useRef, useState } from 'react'
import type { HostKeyMismatch, HostKeyUnknown } from '../shared/protocol'
import { ALLOW_DELAY_MS, blockKeyboardActivation, ListChanges } from './approvals'
import { promptKey, trustEnabled, type HostKeyPrompts } from './hostkeys'

const KNOWN_HOSTS_TEXT = {
  match: 'Your ~/.ssh/known_hosts lists this same key for this host.',
  absent: 'This host is not in your ~/.ssh/known_hosts.',
  different: 'Warning: your ~/.ssh/known_hosts lists a different key for this host. The connection may be intercepted.',
}

// Cancel is the default (Enter) and has focus; Trust is mouse-only.
export function HostKeyPromptView({ info, trustEnabled: enabled, onTrust, onCancel }: {
  info: HostKeyUnknown; trustEnabled: boolean; onTrust: () => void; onCancel: () => void
}) {
  return (
    <div className="modal" role="dialog" aria-label="Unknown host key">
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onCancel() }}>
        <h3>Trust this host key?</h3>
        <p><strong>{info.server}</strong> <span className="muted">{`${info.user}@${info.host}:${info.port}`}</span></p>
        <p className="muted">{info.keyType}</p>
        <pre className="cmd">{info.fingerprint}</pre>
        <p className={info.knownHosts === 'different' ? 'mismatch-text' : 'muted'}>{KNOWN_HOSTS_TEXT[info.knownHosts]}</p>
        <p className="muted">Trust only if this matches the key you expect for this server.</p>
        <div className="actions">
          <button type="submit" autoFocus>Cancel</button>
          <button type="button" className="allow" tabIndex={-1} onKeyDown={blockKeyboardActivation}
            disabled={!enabled} onClick={onTrust}>Trust</button>
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
        <h3>Host key changed</h3>
        <p><strong>{info.server}</strong> <span className="muted">{`${info.user}@${info.host}:${info.port}`}</span></p>
        <p className="mismatch-text">The server presented a different host key than the one pinned. The connection may be intercepted. Nothing was sent.</p>
        <p className="muted">Pinned</p>
        <pre className="cmd">{info.pinned}</pre>
        <p className="muted">Presented</p>
        <pre className="cmd">{info.presented}</pre>
        <p className="muted">If you know the key changed on purpose, forget the old key in the host editor and connect again.</p>
        <div className="actions">
          <button autoFocus onClick={onClose}>Close</button>
          <button onClick={onEdit}>Open host editor</button>
        </div>
      </div>
    </div>
  )
}

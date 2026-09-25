import { useId, useState, type FormEvent, type KeyboardEvent } from 'react'
import type { SecretField, ServerInfo, ServerInput } from '../shared/protocol'
import { closesTabs, draftFrom, endpointChanged, SECRET_FIELDS, secretPlaceholder, toInput, type HostDraft } from './hostForm'
import { CloseIcon, WarningIcon } from './icons'

export function EditorWarnings({ server, draft, openTabs }: { server?: ServerInfo; draft: HostDraft; openTabs: number }) {
  const moved = endpointChanged(server, draft)
  const closes = openTabs > 0 && closesTabs(server, draft)
  if (!moved && !closes) return null
  return (
    <div className="warnings" role="status">
      {moved && <p><WarningIcon />Changing host or port forgets the host key and saved passwords unless you re-enter them.</p>}
      {closes && <p><WarningIcon />{`Saving will close ${openTabs} open ${openTabs === 1 ? 'tab' : 'tabs'}.`}</p>}
    </div>
  )
}

const AUTHS = ['password', 'key', 'agent'] as const

// server undefined = a new host. Secrets are never shown: an empty field
// keeps the saved value, Clear removes it.
export function HostEditor({ server, openTabs, focusForget, onSave, onForget, onClose }: {
  server?: ServerInfo; openTabs: number; focusForget?: boolean
  onSave: (input: ServerInput, original?: string) => Promise<void>
  onForget: (name: string) => Promise<void>
  onClose: () => void
}) {
  const id = useId()
  const [draft, setDraft] = useState(() => draftFrom(server))
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const set = (patch: Partial<HostDraft>) => setDraft((d) => ({ ...d, ...patch }))
  const setSecret = (field: SecretField, value: string, cleared = false) =>
    setDraft((d) => ({ ...d, secrets: { ...d.secrets, [field]: { value, cleared } } }))
  const run = async (p: () => Promise<void>) => {
    setBusy(true); setError(undefined)
    try { await p() } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  const save = (e: FormEvent) => { e.preventDefault(); run(() => onSave(toInput(draft), server?.name)) }
  const escape = (e: KeyboardEvent) => { if (e.key === 'Escape' && !busy) { e.preventDefault(); onClose() } }
  const moved = endpointChanged(server, draft)
  const hostChanged = !!server && draft.host.trim() !== server.host
  const portChanged = !!server && Number(draft.port) !== server.port
  const field = (name: string) => `${id}-${name}`
  return (
    <div className="sheet-backdrop">
      <form className="sheet" role="dialog" aria-label="Host editor" onSubmit={save} onKeyDown={escape}>
        <header className="sheet-head">
          <h3>{server ? `Edit ${server.name}` : 'New host'}</h3>
          <button type="button" className="icon" aria-label="Close host editor" title="Close" onClick={onClose}><CloseIcon /></button>
        </header>
        <div className="sheet-body">
          <section>
            <h4 className="section-label">Connection</h4>
            <div className="grid2">
              <div className="field"><label htmlFor={field('name')}>Name</label>
                <input id={field('name')} value={draft.name} autoFocus={!focusForget} onChange={(e) => set({ name: e.target.value })} /></div>
              <div className="field"><label htmlFor={field('user')}>User</label>
                <input id={field('user')} className="mono" value={draft.user} onChange={(e) => set({ user: e.target.value })} /></div>
              <div className="field wide"><div className="labelrow"><label htmlFor={field('host')}>Host</label>
                {hostChanged && <span id={field('host-changed')} className="changed">· changed</span>}</div>
                <input id={field('host')} className={'mono' + (hostChanged ? ' is-changed' : '')} value={draft.host}
                  aria-describedby={hostChanged ? field('host-changed') : undefined} onChange={(e) => set({ host: e.target.value })} /></div>
              <div className="field narrow"><div className="labelrow"><label htmlFor={field('port')}>Port</label>
                {portChanged && <span id={field('port-changed')} className="changed">· changed</span>}</div>
                <input id={field('port')} className={'mono' + (portChanged ? ' is-changed' : '')} value={draft.port} inputMode="numeric"
                  aria-describedby={portChanged ? field('port-changed') : undefined} onChange={(e) => set({ port: e.target.value })} /></div>
              <div className="field"><span className="fieldlabel" id={field('auth')}>Auth</span>
                <div className="seg full" role="radiogroup" aria-label="Auth">
                  {AUTHS.map((a) => (
                    <button key={a} type="button" role="radio" aria-checked={draft.auth === a} onClick={() => set({ auth: a })}>{a}</button>
                  ))}
                </div></div>
              {/* The cell is kept for every auth, so switching never shifts the layout. */}
              <div className="field" style={{ visibility: draft.auth === 'key' ? 'visible' : 'hidden' }}>
                <label htmlFor={field('keypath')}>Key path</label>
                <input id={field('keypath')} className="mono" value={draft.keyPath} onChange={(e) => set({ keyPath: e.target.value })} /></div>
            </div>
          </section>
          <section>
            <div className="labelrow"><h4 className="section-label">Secrets</h4>
              <span className="muted">Never shown. Leave empty to keep what is saved.</span></div>
            <div className="grid2">
              {SECRET_FIELDS.map(({ field: f, label, has }) => {
                const saved = !!server && has(server)
                const edit = draft.secrets[f]
                return (
                  <div className="field" key={f}>
                    <div className="labelrow"><label htmlFor={field(f)}>{label}</label>
                      {saved && !edit.cleared && <button type="button" className="link" onClick={() => setSecret(f, '', true)}>Clear</button>}</div>
                    <input id={field(f)} type="password" autoComplete="off" value={edit.value}
                      placeholder={secretPlaceholder(saved, edit, moved)} onChange={(e) => setSecret(f, e.target.value)} />
                  </div>
                )
              })}
            </div>
          </section>
          <section className="aipanel">
            <label className="switch">
              <span>
                <span className="switch-title">Visible to AI</span>
                <span className="muted">AI clients can see this server and ask to run commands. Each command still waits for your approval.</span>
                {!server?.hostKey && <span className="muted">Needs a pinned host key.</span>}
              </span>
              <input type="checkbox" role="switch" checked={draft.aiVisible} onChange={(e) => set({ aiVisible: e.target.checked })} />
            </label>
          </section>
          {server && (
            <section>
              <h4 className="section-label">Host key</h4>
              {server.hostKey ? (
                <div className="keyrow">
                  <code className="fp">{`${server.hostKeyAlgo} ${server.hostKey}`.trim()}</code>
                  <button type="button" className="btn danger-outline" autoFocus={focusForget} disabled={busy}
                    onClick={() => run(() => onForget(server.name))}>Forget host key</button>
                  <span className="muted">Takes effect immediately.</span>
                </div>
              ) : (
                <p className="muted">Not pinned. You will be asked to confirm it on the next connect.</p>
              )}
            </section>
          )}
          <EditorWarnings server={server} draft={draft} openTabs={openTabs} />
        </div>
        <footer className="sheet-foot">
          {error && <p className="error">{error}</p>}
          <span className="spacer" />
          <button type="button" className="btn" onClick={onClose}>Close</button>
          <button type="submit" className="btn primary" disabled={busy}>Save</button>
        </footer>
      </form>
    </div>
  )
}

import { useId, useState, type FormEvent, type KeyboardEvent } from 'react'
import type { SecretField, ServerInfo, ServerInput } from '../shared/protocol'
import { arrowStep, closesTabs, draftFrom, endpointChanged, labelHint, SECRET_FIELDS, secretPlaceholder, toInput, type HostDraft } from './hostForm'
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
const ESCALATION: SecretField[] = ['suPassword', 'sudoPassword']

// server undefined = a new host. Only what a plain host needs is open:
// address, user, password. Key/agent auth, su/sudo and AI access are folded.
// Secrets are never shown: an empty field keeps the saved value, Clear removes it.
export function HostEditor({ server, openTabs, focusForget, onSave, onForget, onClose }: {
  server?: ServerInfo; openTabs: number; focusForget?: boolean
  onSave: (input: ServerInput, original?: string) => Promise<void>
  onForget: (name: string) => Promise<void>
  onClose: () => void
}) {
  const id = useId()
  const [draft, setDraft] = useState(() => draftFrom(server))
  const [showAuth, setShowAuth] = useState(() => draftFrom(server).auth !== 'password')
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
  const authKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const next = arrowStep(AUTHS, draft.auth as (typeof AUTHS)[number], e.key)
    if (!next) return
    e.preventDefault(); set({ auth: next })
    e.currentTarget.querySelector<HTMLElement>(`[data-auth="${next}"]`)?.focus()
  }
  const moved = endpointChanged(server, draft)
  const hostChanged = !!server && draft.host.trim() !== server.host
  const portChanged = !!server && Number(draft.port) !== server.port
  const field = (name: string) => `${id}-${name}`
  const saved = (f: SecretField) => !!server && SECRET_FIELDS.find((s) => s.field === f)!.has(server)
  const anySaved = SECRET_FIELDS.some(({ field: f }) => saved(f))
  const hint = labelHint(draft)

  const secret = (f: SecretField) => {
    const { label } = SECRET_FIELDS.find((s) => s.field === f)!
    const edit = draft.secrets[f]
    return (
      <div className="field" key={f}>
        <div className="labelrow"><label htmlFor={field(f)}>{label}</label>
          {saved(f) && !edit.cleared && <button type="button" className="link" onClick={() => setSecret(f, '', true)}>Clear</button>}</div>
        <input id={field(f)} type="password" autoComplete="off" value={edit.value}
          placeholder={secretPlaceholder(saved(f), edit, moved)} onChange={(e) => setSecret(f, e.target.value)} />
      </div>
    )
  }

  return (
    <div className="sheet-layer">
      <form className="sheet" role="dialog" aria-label="Host editor" onSubmit={save} onKeyDown={escape}>
        <header className="sheet-head">
          <h3>{server ? `Edit ${server.name}` : 'New host'}</h3>
          <button type="button" className="icon" aria-label="Close host editor" title="Close" onClick={onClose}><CloseIcon /></button>
        </header>
        <div className="sheet-body">
          <section>
            <div className="field">
              <div className="labelrow"><label htmlFor={field('host')}>Address</label>
                {hostChanged && <span id={field('host-changed')} className="changed">· changed</span>}</div>
              <input id={field('host')} className={'mono address' + (hostChanged ? ' is-changed' : '')} value={draft.host}
                placeholder="IP or hostname" autoFocus={!focusForget}
                aria-describedby={hostChanged ? field('host-changed') : undefined} onChange={(e) => set({ host: e.target.value })} />
            </div>
            <div className="field">
              <label htmlFor={field('name')}>Label</label>
              <input id={field('name')} value={draft.name} placeholder={draft.host.trim() || 'Defaults to the address'}
                aria-describedby={hint ? field('name-hint') : undefined} onChange={(e) => set({ name: e.target.value })} />
              {hint && <span id={field('name-hint')} className="muted">{hint}</span>}
            </div>
            <div className="inline-port">
              <label htmlFor={field('port')}>SSH on</label>
              <input id={field('port')} aria-label="Port" className={'mono' + (portChanged ? ' is-changed' : '')} value={draft.port}
                inputMode="numeric" aria-describedby={portChanged ? field('port-changed') : undefined}
                onChange={(e) => set({ port: e.target.value })} />
              <span>port</span>
              {portChanged && <span id={field('port-changed')} className="changed">· changed</span>}
            </div>
          </section>

          <section>
            <h4 className="section-label">Credentials</h4>
            {anySaved && <p className="muted">Saved secrets are never shown. Leave a field empty to keep it.</p>}
            <div className="field"><label htmlFor={field('user')}>User</label>
              <input id={field('user')} className="mono" value={draft.user} placeholder="Username" onChange={(e) => set({ user: e.target.value })} /></div>
            {(draft.auth === 'password' || saved('password')) && secret('password')}
            {showAuth ? (
              <>
                <div className="field"><span className="fieldlabel">Auth</span>
                  <div className="seg full" role="radiogroup" aria-label="Auth" onKeyDown={authKey}>
                    {AUTHS.map((a) => (
                      <button key={a} type="button" role="radio" aria-checked={draft.auth === a} data-auth={a}
                        tabIndex={draft.auth === a ? 0 : -1} onClick={() => set({ auth: a })}>{a}</button>
                    ))}
                  </div></div>
                {draft.auth === 'key' && (
                  <>
                    <div className="field"><label htmlFor={field('keypath')}>Key path</label>
                      <input id={field('keypath')} className="mono" value={draft.keyPath} placeholder="~/.ssh/id_ed25519"
                        onChange={(e) => set({ keyPath: e.target.value })} /></div>
                    {secret('keyPassphrase')}
                  </>
                )}
              </>
            ) : (
              <button type="button" className="link add" onClick={() => setShowAuth(true)}>+ Key or agent</button>
            )}
          </section>

          <details className="fold">
            <summary>Privilege escalation (su / sudo){ESCALATION.some(saved) ? ' · saved' : ''}</summary>
            <div className="fold-body">{ESCALATION.map(secret)}</div>
          </details>

          <details className="fold">
            <summary>{`AI access · ${draft.aiVisible ? 'On' : 'Off'}`}</summary>
            <div className="fold-body">
              <div className="switch">
                <label htmlFor={field('ai')} className="switch-title">Visible to AI</label>
                <input id={field('ai')} type="checkbox" role="switch" checked={draft.aiVisible} onChange={(e) => set({ aiVisible: e.target.checked })} />
              </div>
              <p className="muted">AI clients can see this server and ask to run commands. Each command still waits for your approval.</p>
              {!server?.hostKey && <p className="muted">Needs a pinned host key.</p>}
            </div>
          </details>

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

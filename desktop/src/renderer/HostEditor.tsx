import { useId, useState, type FormEvent, type KeyboardEvent } from 'react'
import type { AutoAllowCheck, AutoAllowMode, SecretField, ServerInfo, ServerInput } from '../shared/protocol'
import { AUTO_MODES, draftRefusal, saveConfirm, timedNote } from './autoallow'
import { AutoAllowDialog } from './AutoAllowDialog'
import { arrowStep, closesTabs, closeWarning, draftFrom, endpointChanged, labelHint, SECRET_FIELDS, secretPlaceholder, toInput, type HostDraft } from './hostForm'
import { ChevronDownIcon, CloseIcon, WarningIcon } from './icons'

export function EditorWarnings({ server, draft, openTabs, transfers }: { server?: ServerInfo; draft: HostDraft; openTabs: number; transfers: number }) {
  const moved = endpointChanged(server, draft)
  const closes = (openTabs > 0 || transfers > 0) && closesTabs(server, draft)
  if (!moved && !closes) return null
  return (
    <div className="warnings" role="status">
      {moved && <p><WarningIcon />Changing host or port forgets the host key and saved passwords unless you re-enter them.</p>}
      {closes && <p><WarningIcon />{closeWarning(openTabs, transfers)}</p>}
    </div>
  )
}

const AUTHS = ['password', 'key', 'agent'] as const
const ESCALATION: SecretField[] = ['suPassword', 'sudoPassword']

// server undefined = a new host. Only what a plain host needs is open:
// address, user, password. Key/agent auth, su/sudo and AI access are folded.
// Secrets are never shown: an empty field keeps the saved value, Clear removes it.
const OPT_INS: { key: 'autoAllowRoot' | 'autoAllowSudo'; label: string; hint: string }[] = [
  { key: 'autoAllowRoot', label: 'Allow on root hosts', hint: 'Lets a grant run on a root login, or a host with a stored su or sudo password.' },
  { key: 'autoAllowSudo', label: 'Also auto-allow sudo-exec', hint: 'sudo-exec runs without asking on a granted host, the same as plain exec.' },
]

export function HostEditor({ server, openTabs, transfers, focusForget, remoteTunnels, autoAllowCheck, onSave, onForget, onClose }: {
  server?: ServerInfo; openTabs: number; transfers: number; focusForget?: boolean
  remoteTunnels: string[]; autoAllowCheck: (name: string) => Promise<AutoAllowCheck>
  onSave: (input: ServerInput, original: string | undefined, autoAllow: AutoAllowMode) => Promise<void>
  onForget: (name: string) => Promise<void>
  onClose: () => void
}) {
  const id = useId()
  const [draft, setDraft] = useState(() => draftFrom(server))
  const [showAuth, setShowAuth] = useState(() => draftFrom(server).auth !== 'password')
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const set = (patch: Partial<HostDraft>) => setDraft((d) => ({ ...d, ...patch }))
  const setSecret = (field: SecretField, value: string, cleared = false) =>
    setDraft((d) => ({ ...d, secrets: { ...d.secrets, [field]: { value, cleared } } }))
  const run = async (p: () => Promise<void>) => {
    setBusy(true); setError(undefined)
    try { await p() } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  const doSave = () => onSave(toInput(draft), server?.name, draft.autoAllow)
  const save = (e: FormEvent) => {
    e.preventDefault()
    if (saveConfirm(server, draft).needed) setConfirm(true)
    else run(doSave)
  }
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
  const timedNoteText = timedNote(server, Date.now())
  const refusalHint = draftRefusal(server, draft)

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
    <>
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
            <summary>{`AI access · ${draft.aiVisible ? 'On' : 'Off'}${draft.autoAllow !== 'off' ? ' · auto' : ''}`}</summary>
            <div className="fold-body">
              <div className="switch">
                <label htmlFor={field('ai')} className="switch-title">Visible to AI</label>
                <input id={field('ai')} type="checkbox" role="switch" checked={draft.aiVisible} onChange={(e) => set({ aiVisible: e.target.checked })} />
              </div>
              <p className="muted">AI clients can see this server and ask to run commands. Commands wait for your approval unless auto-allow is on.</p>
              {!server?.hostKey && <p className="muted">Needs a pinned host key.</p>}

              <div className="field">
                <label htmlFor={field('autoallow')}>Auto-allow</label>
                <span className="select">
                  <select id={field('autoallow')} value={draft.autoAllow} disabled={!server}
                    onChange={(e) => set({ autoAllow: e.target.value as AutoAllowMode })}>
                    <option value="off">Off</option>
                    {AUTO_MODES.map((m) => <option key={m.mode} value={m.mode}>{m.label}</option>)}
                  </select>
                  <ChevronDownIcon />
                </span>
                {!server && <span className="muted">Save the host and trust its key first</span>}
              </div>
              {timedNoteText && <p className="muted">{timedNoteText}</p>}
              {/* The hint is the checkbox's description, not part of its name. */}
              <div className="optins">
                {OPT_INS.map((o) => (
                  <div key={o.key} className={'optin' + (draft.autoAllow === 'off' ? ' off' : '')}>
                    <input type="checkbox" id={field(o.key)} aria-describedby={field(`${o.key}-hint`)} checked={draft[o.key]} disabled={draft.autoAllow === 'off'}
                      onChange={(e) => set({ [o.key]: e.target.checked })} />
                    <div className="optin-text">
                      <label htmlFor={field(o.key)}>{o.label}</label>
                      <p id={field(`${o.key}-hint`)}>{o.hint}</p>
                    </div>
                  </div>
                ))}
              </div>
              {draft.autoAllow !== 'off' && refusalHint && <p className="hint-warn"><WarningIcon />{refusalHint}</p>}
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
          <EditorWarnings server={server} draft={draft} openTabs={openTabs} transfers={transfers} />
        </div>
        <footer className="sheet-foot">
          {error && <p className="error">{error}</p>}
          <span className="spacer" />
          <button type="button" className="btn" onClick={onClose}>Close</button>
          <button type="submit" className="btn primary" disabled={busy}>Save</button>
        </footer>
      </form>
    </div>
    {confirm && server && (() => {
      const sc = saveConfirm(server, draft)
      return (
        <AutoAllowDialog server={server} mode={draft.autoAllow as Exclude<AutoAllowMode, 'off'>}
          typeName={sc.typeName} rootNew={sc.rootNew} sudoNew={sc.sudoNew} sudo={draft.autoAllowSudo} refused={refusalHint}
          remoteTunnels={remoteTunnels} check={() => autoAllowCheck(server.name)}
          onEnable={doSave} onCancel={() => setConfirm(false)} />
      )
    })()}
    </>
  )
}

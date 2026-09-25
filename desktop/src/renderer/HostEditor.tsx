import { useState, type FormEvent } from 'react'
import type { SecretField, ServerInfo, ServerInput } from '../shared/protocol'
import { closesTabs, draftFrom, endpointChanged, SECRET_FIELDS, secretPlaceholder, toInput, type HostDraft } from './hostForm'

export function EditorWarnings({ server, draft, openTabs }: { server?: ServerInfo; draft: HostDraft; openTabs: number }) {
  return (
    <>
      {endpointChanged(server, draft) && (
        <p className="warn-text">Changing host or port forgets the host key and saved passwords unless you re-enter them.</p>
      )}
      {openTabs > 0 && closesTabs(server, draft) && (
        <p className="warn-text">{`Saving will close ${openTabs} open ${openTabs === 1 ? 'tab' : 'tabs'}.`}</p>
      )}
    </>
  )
}

// server undefined = a new host. Secrets are never shown: an empty field
// keeps the saved value, Clear removes it.
export function HostEditor({ server, openTabs, focusForget, onSave, onForget, onClose }: {
  server?: ServerInfo; openTabs: number; focusForget?: boolean
  onSave: (input: ServerInput, original?: string) => Promise<void>
  onForget: (name: string) => Promise<void>
  onClose: () => void
}) {
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
  return (
    <div className="modal">
      <form className="dialog" role="dialog" aria-label="Host editor" onSubmit={save}>
        <h3>{server ? `Edit ${server.name}` : 'New host'}</h3>
        <label>Name<input value={draft.name} autoFocus={!focusForget} onChange={(e) => set({ name: e.target.value })} /></label>
        <label>Host<input value={draft.host} onChange={(e) => set({ host: e.target.value })} /></label>
        <label>Port<input value={draft.port} inputMode="numeric" onChange={(e) => set({ port: e.target.value })} /></label>
        <label>User<input value={draft.user} onChange={(e) => set({ user: e.target.value })} /></label>
        <label>Auth
          <select value={draft.auth} onChange={(e) => set({ auth: e.target.value })}>
            <option value="password">password</option>
            <option value="key">key</option>
            <option value="agent">agent</option>
          </select>
        </label>
        {draft.auth === 'key' && (
          <label>Key path<input value={draft.keyPath} onChange={(e) => set({ keyPath: e.target.value })} /></label>
        )}
        {SECRET_FIELDS.map(({ field, label, has }) => {
          const saved = !!server && has(server)
          const edit = draft.secrets[field]
          return (
            <div className="row" key={field}>
              <label>{label}<input type="password" autoComplete="off" value={edit.value}
                placeholder={secretPlaceholder(saved, edit)} onChange={(e) => setSecret(field, e.target.value)} /></label>
              {saved && !edit.cleared && <button type="button" className="link" onClick={() => setSecret(field, '', true)}>Clear</button>}
            </div>
          )
        })}
        <label className="check">
          <input type="checkbox" checked={draft.aiVisible} onChange={(e) => set({ aiVisible: e.target.checked })} />
          Visible to AI
        </label>
        {server && (
          <div>
            <div className="muted">Host key</div>
            {server.hostKey ? (
              <>
                <pre className="cmd">{`${server.hostKeyAlgo} ${server.hostKey}`.trim()}</pre>
                <button type="button" autoFocus={focusForget} disabled={busy} onClick={() => run(() => onForget(server.name))}>Forget host key</button>
              </>
            ) : (
              <p className="muted">Not pinned. You will be asked to confirm it on the next connect.</p>
            )}
          </div>
        )}
        <EditorWarnings server={server} draft={draft} openTabs={openTabs} />
        {error && <p className="error">{error}</p>}
        <div className="actions">
          <button type="submit" disabled={busy}>Save</button>
          <button type="button" onClick={onClose}>Close</button>
        </div>
      </form>
    </div>
  )
}

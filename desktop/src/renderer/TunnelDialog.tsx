import { useState } from 'react'
import type { Tunnel, TunnelKind } from '../shared/protocol'
import { displayText } from '../shared/display'
import { formFor, parseTunnelForm, type TunnelForm } from './tunnels'

export function TunnelDialog({ server, tunnel, onSave, onClose }: {
  server: string; tunnel?: Tunnel; onSave: (t: Tunnel) => Promise<void>; onClose: () => void
}) {
  const [f, setF] = useState<TunnelForm>(() => formFor(tunnel))
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const title = tunnel ? 'Edit tunnel' : 'Add tunnel'
  const set = (patch: Partial<TunnelForm>) => setF((cur) => ({ ...cur, ...patch }))
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const r = parseTunnelForm(f, tunnel?.id ?? '')
    if (!r.ok) { setError(r.error); return }
    setBusy(true)
    try { await onSave(r.tunnel) } catch (err) { setError((err as Error).message); setBusy(false) }
  }
  const kinds: [TunnelKind, string, string][] = [
    ['local', 'Local', 'A port here reaches a host:port as seen from the server'],
    ['remote', 'Remote', 'A port on the server reaches a host:port as seen from here'],
    ['dynamic', 'Dynamic', 'A SOCKS5 proxy here; connections leave from the server'],
  ]
  return (
    <div className="modal" role="dialog" aria-label={title}>
      <form className="dialog" onSubmit={submit} onKeyDown={(e) => { if (e.key === 'Escape') onClose() }}>
        <div className="dialog-title"><h3>{title}</h3></div>
        <p className="muted">{`On ${displayText(server)}. Listens on 127.0.0.1 only.`}</p>
        <fieldset className="kinds">
          <legend>Kind</legend>
          {kinds.map(([k, name, hint]) => (
            <label key={k} title={hint}>
              <input type="radio" name="kind" checked={f.kind === k} onChange={() => set({ kind: k })} />{name}
            </label>
          ))}
        </fieldset>
        <label className="field">Listen port<input inputMode="numeric" autoFocus value={f.listenPort} onChange={(e) => set({ listenPort: e.target.value })} /></label>
        {f.kind !== 'dynamic' && (
          <div className="row">
            <label className="field">Target host<input value={f.targetHost} onChange={(e) => set({ targetHost: e.target.value })} /></label>
            <label className="field">Target port<input inputMode="numeric" value={f.targetPort} onChange={(e) => set({ targetPort: e.target.value })} /></label>
          </div>
        )}
        <label className="field">Label<input value={f.label} onChange={(e) => set({ label: e.target.value })} /></label>
        {error && <p className="error" role="alert">{displayText(error)}</p>}
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn primary" disabled={busy}>Save</button>
        </div>
      </form>
    </div>
  )
}

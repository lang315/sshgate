import { useState } from 'react'
import type { Tunnel, TunnelView } from '../shared/protocol'
import { displayText } from '../shared/display'
import { hub } from './transport'
import { summary } from './tunnels'
import { TunnelDialog } from './TunnelDialog'

export function TunnelsView({ server, visible, tunnels, locked, onChanged }: {
  server: string; visible: boolean; tunnels: TunnelView[]; locked: boolean; onChanged: () => void
}) {
  const [editing, setEditing] = useState<{ tunnel?: Tunnel }>()
  const [error, setError] = useState<string>()
  const mine = tunnels.filter((t) => t.server === server)
  const live = (t: TunnelView) => t.status === 'running' || t.status === 'starting'
  const act = async (fn: () => Promise<void>) => {
    setError(undefined)
    try { await fn() } catch (e) { setError((e as Error).message) }
  }
  return (
    <section className="tunnelsview" role="region" aria-label={`Tunnels on ${server}`} style={{ display: visible ? 'flex' : 'none' }}>
      <div className="toolbar">
        <button type="button" className="btn" disabled={locked} onClick={() => setEditing({})}>Add tunnel</button>
      </div>
      {error && <p className="error" role="alert">{displayText(error)}</p>}
      {mine.length === 0 ? (
        <p className="empty">No tunnels yet. Add one to reach a port on or through this host.</p>
      ) : (
        <table>
          <thead><tr><th>Label</th><th>Forward</th><th>Status</th><th>Connections</th><th /></tr></thead>
          <tbody>
            {mine.map((t) => (
              <tr key={t.id} data-id={t.id} data-status={t.status}>
                <td>{displayText(t.label ?? '')}</td>
                <td className="mono">{displayText(summary(t))}</td>
                <td className="status">{t.status === 'error' ? displayText(t.error ?? 'error') : t.status}</td>
                <td className="conns">{t.status === 'running' ? t.conns : ''}</td>
                <td className="rowactions">
                  {live(t) ? (
                    <button type="button" className="btn" onClick={() => hub.tunnelsStop(server, t.id)}>Stop</button>
                  ) : (
                    <button type="button" className="btn" disabled={locked} onClick={() => act(() => hub.tunnelsStart(server, t.id))}>Start</button>
                  )}
                  <button type="button" className="btn" disabled={locked || live(t)} title={live(t) ? 'Stop it first' : undefined}
                    onClick={() => setEditing({ tunnel: t })}>Edit</button>
                  <button type="button" className="btn" disabled={locked || live(t)} title={live(t) ? 'Stop it first' : undefined}
                    onClick={() => act(async () => { await hub.tunnelsDelete(server, t.id); onChanged() })}>Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <p className="muted">While a tunnel runs, any program on this computer can use its local port, and any program on the server can use a remote tunnel’s port.</p>
      {editing && (
        <TunnelDialog server={server} tunnel={editing.tunnel} onClose={() => setEditing(undefined)}
          onSave={async (t) => { await hub.tunnelsSave(server, t); setEditing(undefined); onChanged() }} />
      )}
    </section>
  )
}

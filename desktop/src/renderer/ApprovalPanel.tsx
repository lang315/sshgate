import { useEffect, useState, type FormEvent } from 'react'
import type { ServerInfo } from '../shared/protocol'
import { allowEnabled, highlightNonAscii, type PendingItem } from './approvals'

export function ApprovalPanel({ items, servers, onDecide, onDenyAll, onSendToTab }: {
  items: PendingItem[]
  servers: ServerInfo[]
  onDecide: (id: string, outcome: 'allowed' | 'denied', reason: string) => Promise<void>
  onDenyAll: () => Promise<void>
  onSendToTab: (item: PendingItem) => Promise<void>
}) {
  const [now, setNow] = useState(Date.now())
  const young = items.some((i) => !allowEnabled(i, now))
  useEffect(() => {
    if (!young) return
    const t = setInterval(() => setNow(Date.now()), 100)
    return () => clearInterval(t)
  }, [young])

  return (
    <aside className="approvals" aria-label="Approval requests">
      <h3>AI requests {items.length > 0 && `(${items.length})`}</h3>
      {items.length > 1 && <button className="denyall" onClick={() => onDenyAll()}>Deny all</button>}
      {items.length === 0 && <p className="muted">Nothing waiting.</p>}
      {items.map((item) => (
        <Item key={item.request.id} item={item} now={now}
          server={servers.find((s) => s.name === item.request.server)}
          onDecide={onDecide} onSendToTab={onSendToTab} />
      ))}
    </aside>
  )
}

function Item({ item, now, server, onDecide, onSendToTab }: {
  item: PendingItem; now: number; server?: ServerInfo
  onDecide: (id: string, outcome: 'allowed' | 'denied', reason: string) => Promise<void>
  onSendToTab: (item: PendingItem) => Promise<void>
}) {
  const r = item.request
  const [reason, setReason] = useState('')
  const [error, setError] = useState<string>()
  const act = (p: Promise<void>) => p.catch((e) => setError((e as Error).message))
  const deny = (e: FormEvent) => { e.preventDefault(); act(onDecide(r.id, 'denied', reason)) }
  return (
    <form className="approval" onSubmit={deny}>
      <div className="who">
        <strong>{r.server}</strong>
        {server && <span className="muted"> {server.user}@{server.host}:{server.port}</span>}
        {r.sudo && <span className="badge warn">SUDO</span>}
      </div>
      <pre className="cmd">
        {highlightNonAscii(r.command).map((s, i) => (s.nonAscii ? <mark key={i}>{s.text}</mark> : <span key={i}>{s.text}</span>))}
      </pre>
      <div className="muted">timeout: {r.timeoutSec}s</div>
      {r.description && <div className="desc">AI's description (unverified): {r.description}</div>}
      <div className="muted">client: {r.client} (unverified) · {new Date(r.receivedAt).toLocaleTimeString()}</div>
      <input placeholder="Reason (optional)" value={reason} onChange={(e) => setReason(e.target.value)} />
      <div className="actions">
        <button type="submit" className="deny">Deny</button>
        <button type="button" className="allow" disabled={!allowEnabled(item, now)}
          onClick={() => act(onDecide(r.id, 'allowed', ''))}>Allow</button>
        <button type="button" onClick={() => act(onSendToTab(item))}>Send to tab</button>
      </div>
      {error && <p className="error">{error}</p>}
    </form>
  )
}

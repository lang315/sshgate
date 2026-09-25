import { useEffect, useRef, useState, type FormEvent } from 'react'
import type { ServerInfo } from '../shared/protocol'
import { allowEnabled, blockKeyboardActivation, highlightNonAscii, ListChanges, type PendingItem } from './approvals'

export function ApprovalPanel({ items, servers, seedError, onDecide, onDenyAll, onSendToTab }: {
  items: PendingItem[]
  servers: ServerInfo[]
  seedError?: string
  onDecide: (id: string, outcome: 'allowed' | 'denied', reason: string) => Promise<void>
  onDenyAll: () => Promise<void>
  onSendToTab: (item: PendingItem) => Promise<void>
}) {
  const [now, setNow] = useState(Date.now())
  const [denyAllError, setDenyAllError] = useState<string>()
  // Mount time and every change to the list (ids or height) restart the Allow delay
  // (see allowEnabled).
  const listKey = items.map((i) => i.request.id).join('\n')
  const changes = useRef<ListChanges>(null)
  changes.current ??= new ListChanges(listKey, Date.now())
  changes.current.setKey(listKey, Date.now())
  const changedAt = changes.current.at
  const listRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const ro = new ResizeObserver(([e]) => {
      if (changes.current!.setHeight(e.contentRect.height, Date.now())) setNow(Date.now())
    })
    ro.observe(listRef.current!)
    return () => ro.disconnect()
  }, [])
  const young = items.some((i) => !allowEnabled(i, now, changedAt))
  useEffect(() => {
    if (!young) return
    const t = setInterval(() => setNow(Date.now()), 100)
    return () => clearInterval(t)
  }, [young])

  const denyAll = () => onDenyAll().then(() => setDenyAllError(undefined), (e) => setDenyAllError((e as Error).message))

  return (
    <aside className="approvals" aria-label="Approval requests">
      <h3>AI requests {items.length > 0 && `(${items.length})`}</h3>
      {/* Always rendered so the list never shifts when it appears or disappears. */}
      <button className="denyall" onClick={denyAll} disabled={items.length < 2}>Deny all</button>
      {/* Observed for height changes: anything that shifts the items lives in here. */}
      <div ref={listRef}>
        {denyAllError && <p className="error">{denyAllError}</p>}
        {items.length === 0 && (
          seedError
            ? <p className="error">Could not load pending requests: {seedError}</p>
            : <p className="muted">Nothing waiting.</p>
        )}
        {items.map((item) => (
          <Item key={item.request.id} item={item} now={now} changedAt={changedAt}
            server={servers.find((s) => s.name === item.request.server)}
            onDecide={onDecide} onSendToTab={onSendToTab} />
        ))}
      </div>
    </aside>
  )
}

export function Item({ item, now, changedAt = 0, server, onDecide, onSendToTab }: {
  item: PendingItem; now: number; changedAt?: number; server?: ServerInfo
  onDecide: (id: string, outcome: 'allowed' | 'denied', reason: string) => Promise<void>
  onSendToTab: (item: PendingItem) => Promise<void>
}) {
  const r = item.request
  const [reason, setReason] = useState('')
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const act = (p: Promise<void>) => {
    setBusy(true); setError(undefined)
    p.then(() => setBusy(false), (e) => { setBusy(false); setError((e as Error).message) })
  }
  const deny = (e: FormEvent) => { e.preventDefault(); if (!busy) act(onDecide(r.id, 'denied', reason)) }
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
        <button type="submit" className="deny" disabled={busy}>Deny</button>
        <button type="button" className="allow" tabIndex={-1} onKeyDown={blockKeyboardActivation}
          disabled={busy || !allowEnabled(item, now, changedAt)}
          onClick={() => { if (!busy) act(onDecide(r.id, 'allowed', '')) }}>Allow</button>
        <button type="button" tabIndex={-1} onKeyDown={blockKeyboardActivation} disabled={busy}
          onClick={() => { if (!busy) act(onSendToTab(item)) }}>Send to tab</button>
      </div>
      {error && <p className="error">{error}</p>}
    </form>
  )
}

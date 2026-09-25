import { useEffect, useRef, useState, type FormEvent } from 'react'
import { allowEnabled, blockKeyboardActivation, clickAllowed, highlightNonAscii, ListChanges, nonAsciiSummary, type PendingItem } from './approvals'
import { CloseIcon, WarningIcon } from './icons'

export function ApprovalPanel({ items, seedError, onDecide, onDenyAll, onSendToTab, onClose }: {
  items: PendingItem[]
  seedError?: string
  onDecide: (id: string, outcome: 'allowed' | 'denied', reason: string) => Promise<void>
  onDenyAll: () => Promise<void>
  onSendToTab: (item: PendingItem) => Promise<void>
  onClose: () => void
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

  const scrolled = () => { changes.current!.touch(Date.now()); setNow(Date.now()) }
  return (
    <aside className="approvals" aria-label="Approval requests">
      <div className="approvals-head">
        <h3>AI requests {items.length > 0 && <span className="count">{items.length}</span>}</h3>
        {/* Always rendered so the list never shifts when it appears or disappears. */}
        <button type="button" className="btn danger-outline denyall" onClick={denyAll} disabled={items.length < 2}>Deny all</button>
        <button type="button" className="icon" aria-label="Close AI requests" title="Close" onClick={onClose}><CloseIcon /></button>
      </div>
      <p className="approvals-help">Every command waits for you. Enter in a request denies; Allow takes a mouse click.</p>
      {/* Scrolling moves a different Allow under a still cursor: it restarts the delay. */}
      <div className="approvals-scroll" onScroll={scrolled}>
        {/* Observed for height changes: anything that shifts the items lives in here. */}
        <div ref={listRef}>
          {denyAllError && <p className="error">{denyAllError}</p>}
          {items.length === 0 && (
            seedError
              ? <p className="error">Could not load pending requests: {seedError}</p>
              : <p className="muted empty">Nothing waiting.</p>
          )}
          {items.map((item) => (
            <Item key={item.request.id} item={item} now={now} changedAt={changedAt}
              listChangedAt={() => changes.current!.at}
              onDecide={onDecide} onSendToTab={onSendToTab} />
          ))}
        </div>
      </div>
    </aside>
  )
}

export function Item({ item, now, changedAt = 0, listChangedAt = () => 0, onDecide, onSendToTab }: {
  item: PendingItem; now: number; changedAt?: number; listChangedAt?: () => number
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
  const summary = nonAsciiSummary(r.command)
  const received = r.receivedAt ? new Date(r.receivedAt).toLocaleTimeString() : ''
  return (
    <form className={'approval' + (r.sudo ? ' sudo' : '')} onSubmit={deny}>
      <div className="who">
        <strong>{r.server}</strong>
        {r.sudo && <span className="chip danger">SUDO</span>}
        <span className="when mono">{received}</span>
      </div>
      {/* The endpoint this approval is bound to; the hub refuses the run if it changes. */}
      <div className="target mono">{r.target}</div>
      <pre className="cmd">
        {highlightNonAscii(r.command).map((s, i) => (s.nonAscii ? <mark key={i}>{s.text}</mark> : <span key={i}>{s.text}</span>))}
      </pre>
      {summary && <p className="nonascii"><WarningIcon />{summary}</p>}
      {r.description && (
        <div className="desc"><span className="desc-label">AI&apos;s description · unverified</span>{r.description}</div>
      )}
      <div className="meta">{`timeout ${r.timeoutSec}s · client ${r.client} (unverified)`}</div>
      <label className="reason">Reason (optional)
        <input placeholder="Reason (optional)" value={reason} onChange={(e) => setReason(e.target.value)} />
      </label>
      <div className="actions">
        <button type="submit" className="btn deny" disabled={busy}>Deny<kbd aria-hidden="true">↵</kbd></button>
        {/* Between Deny and Allow, so a click that misses Deny does not land on Allow. */}
        <button type="button" className="btn" tabIndex={-1} onKeyDown={blockKeyboardActivation}
          disabled={busy || !allowEnabled(item, now, changedAt)}
          onClick={() => { if (clickAllowed(item, Date.now(), listChangedAt(), busy)) act(onSendToTab(item)) }}>Send to tab</button>
        <button type="button" className="btn allow" tabIndex={-1} onKeyDown={blockKeyboardActivation}
          disabled={busy || !allowEnabled(item, now, changedAt)}
          onClick={() => { if (clickAllowed(item, Date.now(), listChangedAt(), busy)) act(onDecide(r.id, 'allowed', '')) }}>Allow</button>
      </div>
      {error && <p className="error">{error}</p>}
    </form>
  )
}

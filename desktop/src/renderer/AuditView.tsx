import { Fragment, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { AuditQuery, AuditRecord, ServerInfo } from '../shared/protocol'
import { displayText } from '../shared/display'
import { hub } from './transport'
import { Latest } from './approvals'
import { Debouncer } from './terminals'
import {
  appendLive, applyPage, CHIPS, clearOnLock, EMPTY, failLoad, maskedCount, NO_FILTERS, releaseHeld, rowView,
  startLoad, toggleChip, toQuery, type AuditList, type Filters,
} from './audit'
import { ChevronDownIcon, RefreshIcon } from './icons'

// The fixed Audit tab. It calls audit.read only when it is shown with the
// vault unlocked and nothing loaded, or on a filter change, Enter or 300 ms
// after typing in search, Refresh, or Load older. audit.appended records are
// merged in live; a notification or timer never calls the hub.
export function AuditView({ visible, ready, servers }: { visible: boolean; ready: boolean; servers: ServerInfo[] }) {
  const [list, setList] = useState<AuditList>(EMPTY)
  const [filters, setFilters] = useState<Filters>(NO_FILTERS)
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set())
  const [older, setOlder] = useState(false) // a Load older call is in flight
  const gen = useRef(new Latest()).current // replies to superseded reads are dropped
  const query = useRef<AuditQuery>({}) // the query the shown list was read with
  const filtersNow = useRef(filters)
  filtersNow.current = filters
  const scroller = useRef<HTMLDivElement>(null)
  const savedTop = useRef(0)
  const atTop = useRef(true)

  const toTop = () => {
    if (scroller.current) scroller.current.scrollTop = 0
    savedTop.current = 0
    atTop.current = true
  }
  const load = async (f: Filters) => {
    const g = gen.next()
    query.current = toQuery(f)
    setOlder(false)
    setList(startLoad)
    toTop()
    try {
      const page = await hub.auditRead(query.current)
      if (gen.isCurrent(g)) setList((l) => applyPage(l, page))
    } catch (e) {
      if (gen.isCurrent(g)) setList((l) => failLoad(l, (e as Error).message))
    }
  }
  const loadOlder = async (before: number) => {
    const g = gen.next()
    setOlder(true)
    try {
      const page = await hub.auditRead({ ...query.current, before })
      if (gen.isCurrent(g)) setList((l) => applyPage(l, page))
    } catch (e) {
      if (gen.isCurrent(g)) setList((l) => failLoad(l, (e as Error).message))
    } finally {
      if (gen.isCurrent(g)) setOlder(false)
    }
  }
  // Follows the human's typing only; never a poll.
  const typing = useRef(new Debouncer(300, () => void load(filtersNow.current))).current
  useEffect(() => () => typing.cancel(), [typing])

  useEffect(() => hub.onEvent((e) => {
    if (e.method === 'locked') {
      gen.next()
      setOlder(false)
      setExpanded(new Set())
      setList(clearOnLock())
    } else if (e.method === 'audit.appended') {
      setList((l) => appendLive(l, e.params, query.current, atTop.current))
    }
  }), [gen])
  // Every change of ready (a lock, an unlock, a hub restart) starts over.
  useEffect(() => { gen.next(); setOlder(false); setList(EMPTY) }, [ready, gen])
  useEffect(() => {
    if (visible && ready && list.status === 'idle') void load(filtersNow.current)
  }, [visible, ready, list.status])

  const onScroll = () => {
    const el = scroller.current
    if (!visible || !el) return // display:none zeroes scrollTop; keep the saved one
    savedTop.current = el.scrollTop
    atTop.current = el.scrollTop <= 2
    if (atTop.current) setList(releaseHeld)
  }
  // Chromium drops scrollTop under display:none; put it back when shown.
  useLayoutEffect(() => {
    if (visible && scroller.current) scroller.current.scrollTop = savedTop.current
  }, [visible])

  const change = (f: Filters) => { typing.cancel(); setFilters(f); void load(f) }
  const toggle = (seq: number) => setExpanded((s) => {
    const n = new Set(s)
    if (!n.delete(seq)) n.add(seq)
    return n
  })
  const now = new Date()
  return (
    <section className="auditview" role="region" aria-label="Audit log" style={{ display: visible ? 'flex' : 'none' }}>
      <div className="audit-toolbar">
        <span className="select audit-host">
          <select aria-label="Host" value={filters.server} onChange={(e) => change({ ...filters, server: e.target.value })}>
            <option value="">All hosts</option>
            {servers.map((s) => <option key={s.name} value={s.name}>{displayText(s.name)}</option>)}
          </select>
          <ChevronDownIcon />
        </span>
        <div className="audit-chips">
          {CHIPS.map((c) => (
            <button key={c.id} type="button" className="filterchip" aria-pressed={filters.chips.includes(c.id)}
              onClick={() => change({ ...filters, chips: toggleChip(filters.chips, c.id) })}>{c.label}</button>
          ))}
        </div>
        <input type="search" aria-label="Search audit" placeholder="Search" value={filters.text}
          onChange={(e) => { setFilters({ ...filters, text: e.target.value }); typing.poke() }}
          onKeyDown={(e) => { if (e.key === 'Enter') { typing.cancel(); void load(filtersNow.current) } }} />
        <button type="button" className="btn" onClick={() => { typing.cancel(); void load(filters) }}><RefreshIcon />Refresh</button>
      </div>
      {list.error && <p className="error audit-error" role="alert">{displayText(list.error)}</p>}
      <div className="audit-body">
        {list.held.length > 0 && (
          <button type="button" className="btn sm newpill" onClick={() => { toTop(); setList(releaseHeld) }}>{`${list.held.length} new`}</button>
        )}
        <div className="audit-scroll" ref={scroller} onScroll={onScroll}>
          <table className="audittable">
            <tbody>
              {list.entries.map((e) => {
                const v = rowView(e.record, now)
                const open = expanded.has(e.seq)
                return (
                  <Fragment key={e.seq}>
                    <tr className="auditrow" data-seq={e.seq} tabIndex={0} aria-expanded={open} onClick={() => toggle(e.seq)}
                      onKeyDown={(k) => { if (k.key === 'Enter') { k.preventDefault(); toggle(e.seq) } }}>
                      <td className="when">{v.time}</td>
                      <td className="host">{displayText(v.host)}</td>
                      <td className="badges">{v.badges.map((b, i) => <span key={i} className={'chip ' + b.tone}>{displayText(b.text)}</span>)}</td>
                      <td className="main mono" title={displayText(v.main)}>{displayText(v.main)}</td>
                      <td className="side">{v.side.join(' · ')}</td>
                    </tr>
                    {open && <tr className="auditdetail"><td colSpan={5}><AuditDetail record={e.record} /></td></tr>}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
          {list.status === 'loading' && <p className="muted audit-note">Loading…</p>}
          {list.status === 'loaded' && list.entries.length === 0 && <p className="empty audit-note">No audit records match.</p>}
          {list.next !== undefined && (
            <button type="button" className="btn audit-older" disabled={older || list.status !== 'loaded'}
              onClick={() => { if (list.next !== undefined) void loadOlder(list.next) }}>Load older</button>
          )}
        </div>
      </div>
      <footer className="muted">
        {list.skipped > 0 && `${list.skipped} malformed line${list.skipped === 1 ? '' : 's'} skipped · `}
        {list.path && <>Audit file: <code>{list.path}</code></>}
      </footer>
    </section>
  )
}

function AuditDetail({ record: r }: { record: AuditRecord }) {
  const facts: [string, string][] = []
  const add = (label: string, v: unknown) => { if (typeof v === 'string' ? v !== '' : typeof v === 'number') facts.push([label, String(v)]) }
  add('Reason', r.reason)
  add('Client', r.client)
  if (typeof r.timeoutSec === 'number') add('Timeout', `${r.timeoutSec} s`)
  if (typeof r.durationMs === 'number') add('Duration', `${r.durationMs} ms`)
  const redacted = maskedCount(r) > 0 ? Object.entries(r.redacted ?? {}).filter(([, n]) => typeof n === 'number') : []
  return (
    <div className="audit-detail">
      {!r.kind && typeof r.command === 'string' && <pre className="cmd">{displayText(r.command)}</pre>}
      {typeof r.description === 'string' && r.description !== '' && (
        <div className="desc"><span className="desc-label">AI&apos;s description · unverified</span>{displayText(r.description)}</div>
      )}
      {facts.length > 0 && (
        <dl className="audit-facts">{facts.map(([k, v]) => <Fragment key={k}><dt>{k}</dt><dd>{displayText(v)}</dd></Fragment>)}</dl>
      )}
      {redacted.length > 0 && <p className="muted">{'Masked: ' + redacted.map(([k, n]) => `${displayText(k)} ×${n}`).join(', ')}</p>}
      <details>
        <summary>Raw JSON</summary>
        <pre className="cmd">{JSON.stringify(r, null, 2).split('\n').map(displayText).join('\n')}</pre>
      </details>
    </div>
  )
}

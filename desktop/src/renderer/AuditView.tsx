import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { AuditQuery, ServerInfo } from '../shared/protocol'
import { displayText } from '../shared/display'
import { hub } from './transport'
import { Latest } from './approvals'
import { Debouncer } from './terminals'
import {
  appendLive, applyPage, CHIPS, clearOnLock, EMPTY, failLoad, NO_FILTERS, releaseHeld,
  startLoad, toggleChip, toQuery, type AuditList, type Filters,
} from './audit'
import { AuditTable } from './AuditTable'
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
      typing.cancel() // a pending search must not read after a lock
      setOlder(false)
      setExpanded(new Set())
      setList(clearOnLock())
    } else if (e.method === 'audit.appended') {
      setList((l) => appendLive(l, e.params, query.current, atTop.current))
    }
  }), [gen, typing])
  // Every change of ready (a lock, an unlock, a hub restart) starts over.
  // Keep this declared before the auto-load effect: on a ready change the
  // reset must run first so the load sees the fresh idle list.
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
          <AuditTable entries={list.entries} expanded={expanded} onToggle={toggle} now={new Date()} />
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
        {list.path && <>Audit file: <code>{displayText(list.path)}</code></>}
      </footer>
    </section>
  )
}

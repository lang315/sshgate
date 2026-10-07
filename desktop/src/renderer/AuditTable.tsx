import { Fragment } from 'react'
import type { AuditEntry, AuditKind, AuditRecord } from '../shared/protocol'
import { displayText } from '../shared/display'
import { maskedCount, rowView } from './audit'
import { ChevronDownIcon } from './icons'

const KIND: Record<AuditKind, string> = { exec: 'Exec', config: 'Config', file: 'File', tunnel: 'Tunnel' }

// The Audit tab's rows, newest first, under one heading per day. It holds no
// state and never calls the hub: AuditView owns the list and what is expanded.
export function AuditTable({ entries, expanded, onToggle, now }: {
  entries: AuditEntry[]; expanded: ReadonlySet<number>; onToggle: (seq: number) => void; now: Date
}) {
  let day: string | undefined
  return (
    <table className="audittable">
      <thead>
        <tr>
          <th className="twisty" />
          <th className="when">Time</th>
          <th className="kind">Kind</th>
          <th className="host">Host</th>
          <th className="badges">Result</th>
          <th className="main">Command or change</th>
          <th className="side">Exit and notes</th>
        </tr>
      </thead>
      <tbody>
        {entries.map((e, i) => {
          const v = rowView(e.record, now)
          const open = expanded.has(e.seq)
          const heading = v.day !== day
          day = v.day
          return (
            <Fragment key={e.seq}>
              {heading && <tr className="auditday"><th colSpan={7} scope="rowgroup">{v.day}</th></tr>}
              <tr className={'auditrow' + (i % 2 ? ' alt' : '') + (v.alert ? ' alert' : '')} data-seq={e.seq} tabIndex={0} aria-expanded={open} onClick={() => onToggle(e.seq)}
                onKeyDown={(k) => { if (k.key === 'Enter') { k.preventDefault(); onToggle(e.seq) } }}>
                <td className="twisty"><ChevronDownIcon /></td>
                <td className="when">{v.time}</td>
                <td className="kind">{KIND[v.kind]}</td>
                <td className="host">{displayText(v.host)}</td>
                <td className="badges">{v.badges.map((b, n) => <span key={n} className={'chip ' + b.tone}>{displayText(b.text)}</span>)}</td>
                <td className={v.kind === 'exec' ? 'main mono' : 'main'} title={displayText(v.main)}>{displayText(v.main)}</td>
                <td className="side">
                  {v.exit !== undefined && <span className={v.exit === 0 ? 'exit' : 'exit bad'}>{`exit ${v.exit}`}</span>}
                  {v.exit !== undefined && v.side.length > 0 && ' · '}
                  {displayText(v.side.join(' · '))}
                </td>
              </tr>
              {open && <tr className="auditdetail"><td colSpan={7}><AuditDetail record={e.record} /></td></tr>}
            </Fragment>
          )
        })}
      </tbody>
    </table>
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
      {!r.kind && typeof r.stdin === 'string' && r.stdin !== '' && (
        <pre className="cmd stdin">{r.stdin.split('\n').map(displayText).join('\n')}</pre>
      )}
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

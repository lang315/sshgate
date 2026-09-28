import { useState } from 'react'
import type { ServerInfo, TunnelView } from '../shared/protocol'
import { filterHosts } from './hostForm'
import { runningCount } from './tunnels'
import { chipLabel, isActive } from './autoallow'
import { AutoAllowPaused } from './AutoAllowPaused'
import { BoltIcon, EditIcon, FolderIcon, PlusIcon, StopIcon, TrashIcon, TunnelIcon } from './icons'

export function HostList({ servers, storePath, tunnels, now, onOpen, onFiles, onTunnels, onNew, onEdit, onDelete, onImport, onAutoAllow, onStopAutoAllow, paused, onResume, onStopPaused }: {
  servers: ServerInfo[]; storePath: string; tunnels: TunnelView[]; now: number
  onOpen: (name: string) => void; onFiles: (name: string) => void; onTunnels: (name: string) => void
  onNew: () => void; onEdit: (name: string) => void; onDelete: (name: string) => void
  onImport: () => void
  onAutoAllow: (name: string) => void; onStopAutoAllow: (name: string) => void
  paused: string[]; onResume: () => void; onStopPaused: () => void
}) {
  const [query, setQuery] = useState('')
  const shown = filterHosts(servers, query)
  return (
    <nav className="hosts" aria-label="Hosts">
      <div className="hosts-head">
        <input type="search" placeholder="Search hosts" aria-label="Search hosts" value={query}
          onChange={(e) => setQuery(e.target.value)} />
        <button type="button" className="btn" onClick={onNew}><PlusIcon />New host</button>
        <button type="button" className="btn" onClick={onImport}>Import from SSH config</button>
      </div>
      <h2 className="section-label">{`Hosts · ${shown.length}`}</h2>
      <AutoAllowPaused hosts={paused} onResume={onResume} onStop={onStopPaused} />
      {servers.length === 0 ? (
        <p className="empty">No hosts yet. Add one with New host.</p>
      ) : shown.length === 0 ? (
        <p className="empty">{`No hosts match "${query.trim()}"`}</p>
      ) : (
        <ul className="hostgrid">
          {shown.map((s) => (
            <li key={s.name} className="hostcard">
              {/* The card is the open button; its name is exactly the host name. */}
              <button type="button" className="hostcard-open" aria-label={s.name} onClick={() => onOpen(s.name)}>
                <span className="tile" aria-hidden="true">{s.name.slice(0, 1).toUpperCase()}</span>
                <span className="hostcard-text">
                  <span className="hostcard-name">
                    {s.name}
                    {s.aiVisible && <span className="chip ai">AI</span>}
                    {!s.hostKey && <span className="chip wait">New key</span>}
                    {runningCount(tunnels, s.name) > 0 && <span className="chip tunnels" title="Running tunnels">{`${runningCount(tunnels, s.name)} ⇄`}</span>}
                    {chipLabel(s.autoAllow, now) && <span className="chip auto" aria-hidden="true">{chipLabel(s.autoAllow, now)}</span>}
                  </span>
                  <span className="hostcard-addr mono">{`${s.user}@${s.host}:${s.port}`}</span>
                </span>
              </button>
              <span className="hostcard-actions">
                {isActive(s.autoAllow, now) || s.autoAllow?.paused
                  ? <button type="button" className="icon" aria-label={`Stop auto-allow ${s.name}`} title="Stop auto-allow" onClick={() => onStopAutoAllow(s.name)}><StopIcon /></button>
                  : <button type="button" className="icon" aria-label={`Auto-allow ${s.name}`} title="Auto-allow" onClick={() => onAutoAllow(s.name)}><BoltIcon /></button>}
                <button type="button" className="icon" aria-label={`Files ${s.name}`} title="Files" onClick={() => onFiles(s.name)}><FolderIcon /></button>
                <button type="button" className="icon" aria-label={`Tunnels ${s.name}`} title="Tunnels" onClick={() => onTunnels(s.name)}><TunnelIcon /></button>
                <button type="button" className="icon" aria-label={`Edit ${s.name}`} title="Edit" onClick={() => onEdit(s.name)}><EditIcon /></button>
                <button type="button" className="icon" aria-label={`Delete ${s.name}`} title="Delete" onClick={() => onDelete(s.name)}><TrashIcon /></button>
              </span>
            </li>
          ))}
        </ul>
      )}
      <footer className="muted">Vault file: <code>{storePath}</code> — copy it to back up.</footer>
    </nav>
  )
}

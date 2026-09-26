import { useState } from 'react'
import type { ServerInfo } from '../shared/protocol'
import { filterHosts } from './hostForm'
import { EditIcon, PlusIcon, TrashIcon } from './icons'

export function HostList({ servers, storePath, onOpen, onNew, onEdit, onDelete, onImport }: {
  servers: ServerInfo[]; storePath: string
  onOpen: (name: string) => void; onNew: () => void; onEdit: (name: string) => void; onDelete: (name: string) => void
  onImport: () => void
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
                  </span>
                  <span className="hostcard-addr mono">{`${s.user}@${s.host}:${s.port}`}</span>
                </span>
              </button>
              <span className="hostcard-actions">
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

import type { ServerInfo } from '../shared/protocol'

export function HostList({ servers, storePath, onOpen, onNew, onEdit, onDelete }: {
  servers: ServerInfo[]; storePath: string
  onOpen: (name: string) => void; onNew: () => void; onEdit: (name: string) => void; onDelete: (name: string) => void
}) {
  return (
    <nav className="hosts">
      <h3>Servers</h3>
      <button className="new" onClick={onNew}>New host</button>
      <ul>
        {servers.map((s) => (
          <li key={s.name}>
            <button onClick={() => onOpen(s.name)} title={`${s.user}@${s.host}:${s.port}`}>
              {s.name}
            </button>
            {s.aiVisible && <span className="badge" title="Visible to AI">AI</span>}
            {!s.hostKey && <span className="badge warn" title="Host key not pinned yet">new</span>}
            <button className="small" aria-label={`Edit ${s.name}`} onClick={() => onEdit(s.name)}>Edit</button>
            <button className="small" aria-label={`Delete ${s.name}`} onClick={() => onDelete(s.name)}>Delete</button>
          </li>
        ))}
      </ul>
      <footer className="muted">Vault file: <code>{storePath}</code> — copy it to back up.</footer>
    </nav>
  )
}

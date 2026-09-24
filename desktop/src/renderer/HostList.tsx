import type { ServerInfo } from '../shared/protocol'

export function HostList({ servers, onOpen }: { servers: ServerInfo[]; onOpen: (name: string) => void }) {
  return (
    <nav className="hosts">
      <h3>Servers</h3>
      <ul>
        {servers.map((s) => (
          <li key={s.name}>
            <button onClick={() => onOpen(s.name)} title={`${s.user}@${s.host}:${s.port}`}>
              {s.name}
            </button>
            {s.aiVisible && <span className="badge" title="Visible to AI">AI</span>}
            {!s.hostKey && <span className="badge warn" title="Host key not pinned yet">new</span>}
          </li>
        ))}
      </ul>
    </nav>
  )
}

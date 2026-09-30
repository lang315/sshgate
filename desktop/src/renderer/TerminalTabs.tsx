import { forwardRef, useEffect, useImperativeHandle, useRef, useState, type ReactNode } from 'react'
import type { HostKeyMismatch, ServerInfo, TunnelView } from '../shared/protocol'
import { hub } from './transport'
import type { HostKeyPrompts } from './hostkeys'
import { CloseIcon, FolderIcon, HomeIcon, ListIcon, TunnelIcon } from './icons'
import { AUDIT_TAB, Dispatcher, TabSet } from './terminals'
import { TermView, type TermApi, type TermEvent } from './TermView'
import { FilesView } from './FilesView'
import { TunnelsView } from './TunnelsView'
import { AuditView } from './AuditView'
import type { Theme } from './theme'

export interface TerminalsHandle {
  open(server: string): void
  openFiles(server: string): void
  openTunnels(server: string): void
  sendToTab(server: string, text: string): Promise<void>
  openCount(server: string): number
  transferCount(server: string): number
  focusActive(): void
}

export const Terminals = forwardRef<TerminalsHandle, {
  theme: Theme; hostKeys: HostKeyPrompts; onMismatch: (m: HostKeyMismatch) => void; onTrusted: () => void
  home: ReactNode; actions: ReactNode; banner: ReactNode; servers: ServerInfo[]; onFocusApprovals: () => void
  tunnels: TunnelView[]; locked: boolean; onTunnelsChanged: () => void; autoHosts: Set<string>; ready: boolean
}>(function Terminals({ theme, hostKeys, onMismatch, onTrusted, home: homeContent, actions, banner, servers, onFocusApprovals, tunnels, locked, onTunnelsChanged, autoHosts, ready }, ref) {
  const tabs = useRef(new TabSet()).current
  const apis = useRef(new Map<string, TermApi>()).current
  const events = useRef(new Dispatcher<TermEvent>()).current
  const transfers = useRef(new Map<string, number>()).current
  useEffect(() => {
    const offEvent = hub.onEvent((e) => {
      const id = (e.params as { id?: unknown } | undefined)?.id
      if ((e.method.startsWith('term.') || e.method.startsWith('files.')) && typeof id === 'string') events.emit(id, e)
    })
    const offState = hub.onState((s) => { if (s.kind !== 'running') events.emitAll({ method: 'hub.stopped' }) })
    return () => { offEvent(); offState() }
  }, [events])
  const [, setVersion] = useState(0)
  const changed = () => setVersion((v) => v + 1)
  const register = (id: string, api: TermApi | undefined) => { if (api) apis.set(id, api); else apis.delete(id) }

  const waitReady = (id: string) => new Promise<void>((resolve, reject) => {
    const started = Date.now()
    const check = () => {
      const t = tabs.tabs.find((x) => x.id === id)
      if (!t || t.state === 'exited') return reject(new Error('terminal closed'))
      if (t.state === 'open' && t.sawOutput && apis.has(id)) return resolve()
      if (Date.now() - started > 15000) return reject(new Error('terminal did not become ready'))
      setTimeout(check, 50)
    }
    check()
  })

  useImperativeHandle(ref, () => ({
    open(server) { tabs.open(server); changed() },
    openFiles(server) { tabs.open(server, 'files'); changed() },
    openTunnels(server) { const t = tabs.findKind(server, 'tunnels'); if (t) tabs.activate(t.id); else tabs.open(server, 'tunnels'); changed() },
    async sendToTab(server, text) {
      let tab = tabs.mostRecentFor(server)
      if (!tab) tab = tabs.open(server)
      tabs.activate(tab.id); changed()
      await waitReady(tab.id)
      apis.get(tab.id)!.paste(text)
    },
    openCount: (server) => tabs.openCount(server),
    transferCount: (server) => tabs.tabs.filter((t) => t.server === server && t.kind === 'files').reduce((n, t) => n + (transfers.get(t.id) ?? 0), 0),
    focusActive() { if (tabs.active) apis.get(tabs.active)?.focus() },
  }))

  const home = tabs.active === undefined
  const audit = tabs.active === AUDIT_TAB
  const target = (server: string) => {
    const s = servers.find((x) => x.name === server)
    return s ? `${s.user}@${s.host}:${s.port}` : server
  }
  return (
    <div className="terms">
      <div className="tabbar">
        <button type="button" className={'hometab' + (home ? ' active' : '')} onClick={() => { tabs.showHome(); changed() }}>
          <HomeIcon />Hosts
        </button>
        <button type="button" className={'audittab' + (audit ? ' active' : '')} onClick={() => { tabs.showAudit(); changed() }}>
          <ListIcon />Audit
        </button>
        {tabs.tabs.map((t) => (
          <div key={t.id} className={'tab' + (t.id === tabs.active ? ' active' : '') + (t.state === 'exited' ? ' exited' : '')} data-state={t.state} data-kind={t.kind} title={target(t.server)}>
            <button type="button" className="tabname" onClick={() => { tabs.activate(t.id); changed() }}>
              {t.kind === 'files' ? <FolderIcon /> : t.kind === 'tunnels' ? <TunnelIcon /> : <span className="dot" aria-hidden="true" />}{t.server}
              {autoHosts.has(t.server) && <span className="autodot" role="img" aria-label="auto-allow on" />}
              {t.state === 'exited' ? ' · exited' : ''}
            </button>
            {t.kind === 'term' && t.state === 'exited' && (
              <button type="button" className="reconnect" onClick={() => { tabs.close(t.id); tabs.open(t.server); changed() }}>Reconnect</button>
            )}
            <button type="button" className="icon tabclose" title="Close" aria-label="Close" onClick={() => { transfers.delete(t.id); tabs.close(t.id); changed() }}><CloseIcon /></button>
          </div>
        ))}
        <div className="tabbar-actions">{actions}</div>
      </div>
      {banner}
      <div className="termarea">
        <div className="homeview" style={{ display: home ? 'block' : 'none' }}>{homeContent}</div>
        <AuditView visible={audit} ready={ready} servers={servers} />
        {tabs.tabs.map((t) => (
          t.kind === 'tunnels' ? (
            <TunnelsView key={t.id} server={t.server} visible={t.id === tabs.active} tunnels={tunnels} locked={locked} onChanged={onTunnelsChanged} />
          ) : t.kind === 'files' ? (
            <FilesView key={t.id} tab={t} visible={t.id === tabs.active} events={events} hostKeys={hostKeys}
              onMismatch={onMismatch} onTrusted={onTrusted} onJobs={(n) => transfers.set(t.id, n)} />
          ) : (
            <TermView key={t.id} tab={t} tabs={tabs} events={events} visible={t.id === tabs.active} onChange={changed} register={register}
              theme={theme} hostKeys={hostKeys} onMismatch={onMismatch} onTrusted={onTrusted} onFocusApprovals={onFocusApprovals} />
          )
        ))}
      </div>
    </div>
  )
})

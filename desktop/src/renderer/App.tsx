import { useCallback, useEffect, useRef, useState } from 'react'
import type { HubState, ServerInfo, Status } from '../shared/protocol'
import { hub } from './transport'
import { screenFor } from './shell'
import { Unlock } from './Unlock'
import { CreateVault } from './CreateVault'
import { HostList } from './HostList'
import { HostEditor } from './HostEditor'
import { Terminals, type TerminalsHandle } from './TerminalTabs'
import { ApprovalPanel } from './ApprovalPanel'
import { Latest, mergeSeed, reduceApprovals, type PendingItem } from './approvals'

export function App() {
  const [hubState, setHubState] = useState<HubState>({ kind: 'starting' })
  const [status, setStatus] = useState<Status>()
  const [unlockError, setUnlockError] = useState<string>()
  const [idleLocked, setIdleLocked] = useState(false)
  const [servers, setServers] = useState<ServerInfo[]>([])
  const terms = useRef<TerminalsHandle>(null)
  const [everReady, setEverReady] = useState(false)
  const [editing, setEditing] = useState<{ name?: string; focusForget?: boolean }>()

  const refresh = useCallback(async () => {
    try { setStatus(await hub.status()) } catch { setStatus(undefined) }
  }, [])

  useEffect(() => {
    hub.getState().then(setHubState).catch(() => {})
    const offState = hub.onState(setHubState)
    const offEvent = hub.onEvent((e) => {
      if (e.method === 'locked') { setIdleLocked(e.params?.reason === 'idle'); refresh() }
    })
    return () => { offState(); offEvent() }
  }, [refresh])

  useEffect(() => {
    if (hubState.kind === 'running') refresh()
    else { setStatus(undefined); setUnlockError(undefined); setIdleLocked(false) }
  }, [hubState, refresh])

  const screen = screenFor(hubState, status, unlockError)

  // One fetch per screen change or host edit, never a poll: every call but
  // status counts as UI activity and would hold off the idle lock.
  const reloadServers = useCallback(() => hub.servers().then(setServers).catch(() => setServers([])), [])
  useEffect(() => {
    if (screen.kind === 'ready' || screen.kind === 'create-vault') reloadServers()
    else setServers([])
  }, [screen.kind, reloadServers])

  const [items, setItems] = useState<PendingItem[]>([])
  const [seedError, setSeedError] = useState<string>()
  const decidedSince = useRef(new Set<string>())
  const pendingSince = useRef(new Set<string>())
  const seedGen = useRef(new Latest())
  useEffect(() => hub.onEvent((e) => {
    // Only approval events can change items. A no-op setItems still queues an update
    // (holding e) until App next renders, so calling it per term.data leaks every chunk.
    if (e.method !== 'pending' && e.method !== 'decided') return
    if (e.method === 'decided') decidedSince.current.add(e.params.request.id)
    else pendingSince.current.add(e.params.request.id)
    setItems((cur) => reduceApprovals(cur, e, Date.now()))
  }), [])
  useEffect(() => {
    const gen = seedGen.current.next() // a reply to any earlier pending() call is now stale
    if (screen.kind === 'ready') {
      decidedSince.current = new Set()
      pendingSince.current = new Set()
      hub.pending()
        .then((p) => {
          if (!seedGen.current.isCurrent(gen)) return
          setItems((cur) => mergeSeed(cur, p, decidedSince.current, pendingSince.current, Date.now())); setSeedError(undefined)
        })
        .catch((e) => { if (seedGen.current.isCurrent(gen)) setSeedError((e as Error).message) })
    }
    if (hubState.kind !== 'running') { setItems([]); setSeedError(undefined) }
  }, [screen.kind, hubState.kind])

  const unlock = async (pw: string) => {
    try { await hub.unlock(pw); setUnlockError(undefined); setIdleLocked(false) } catch (e) { setUnlockError((e as Error).message) }
    await refresh()
  }
  const createVault = async (pw: string) => { await hub.createVault(pw); await refresh() }
  const deleteHost = async (name: string) => {
    if (!window.confirm(`Delete ${name}? Its saved passwords and host key are removed and its open tabs close.`)) return
    try { await hub.deleteServer(name) } catch (e) { window.alert((e as Error).message) }
    await reloadServers()
  }

  const ready = screen.kind === 'ready'
  useEffect(() => { if (ready) setEverReady(true) }, [ready])

  // Once shown, terminals stay mounted (hidden and inert) through lock and hub restarts,
  // so their SSH sessions survive a lock and a restart can end them with Reconnect.
  return (
    <div className={ready ? 'layout' : 'app'}>
      {ready ? (
        <>
          <header>
            <span>ssh-mcp</span>
            <button onClick={async () => { try { await hub.lock(); setUnlockError(undefined); await refresh() } catch { /* the locked/hub-state events recover the UI */ } }}>Lock</button>
          </header>
          <HostList servers={servers} storePath={status?.storePath ?? ''} onOpen={(name) => terms.current?.open(name)}
            onNew={() => setEditing({})}
            onEdit={async (name) => { await reloadServers(); setEditing({ name }) }}
            onDelete={deleteHost} />
        </>
      ) : screen.kind === 'hub' ? (
        <HubScreen state={screen.state} />
      ) : screen.kind === 'create-vault' ? (
        <div className="center"><CreateVault servers={servers} onCreate={createVault} /></div>
      ) : (
        <div className="center">
          {idleLocked && <p className="muted">Locked after inactivity.</p>}
          <Unlock onUnlock={unlock} error={screen.error} />
        </div>
      )}
      {(ready || everReady) && (
        <main className="work" style={ready ? undefined : { display: 'none' }} inert={!ready}><Terminals ref={terms} /></main>
      )}
      {ready && (
        <ApprovalPanel items={items} seedError={seedError}
          onDecide={(id, outcome, reason) => hub.decide(id, outcome, reason)}
          onDenyAll={() => hub.denyAll('denied all by user')}
          onSendToTab={async (item) => {
            await terms.current!.sendToTab(item.request.server, item.request.command)
            await hub.decide(item.request.id, 'sent_to_tab')
          }} />
      )}
      {ready && editing && (
        <HostEditor key={editing.name ?? ''} server={servers.find((s) => s.name === editing.name)}
          openTabs={editing.name ? terms.current?.openCount(editing.name) ?? 0 : 0} focusForget={editing.focusForget}
          onSave={async (input, original) => { await hub.saveServer(input, original); await reloadServers(); setEditing(undefined) }}
          onForget={async (name) => { await hub.forgetHostKey(name); await reloadServers() }}
          onClose={() => setEditing(undefined)} />
      )}
    </div>
  )
}

function HubScreen({ state }: { state: HubState }) {
  if (state.kind === 'failed') {
    return (
      <div className="center">
        <h2>The hub stopped</h2>
        <p>{state.message}</p>
        <pre className="stderr">{state.stderr}</pre>
      </div>
    )
  }
  if (state.kind === 'restarting') {
    return <div className="center"><p>Hub crashed; restarting in {Math.round(state.inMs / 1000)} s (attempt {state.attempt}).</p></div>
  }
  return <div className="center"><p>Starting the hub…</p></div>
}

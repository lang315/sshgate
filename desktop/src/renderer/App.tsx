import { useCallback, useEffect, useRef, useState } from 'react'
import type { HubState, ServerInfo } from '../shared/protocol'
import { hub } from './transport'
import { screenFor } from './shell'
import { Unlock } from './Unlock'
import { HostList } from './HostList'
import { Terminals, type TerminalsHandle } from './TerminalTabs'

export function App() {
  const [hubState, setHubState] = useState<HubState>({ kind: 'starting' })
  const [status, setStatus] = useState<{ locked: boolean; hasStore: boolean }>()
  const [unlockError, setUnlockError] = useState<string>()
  const [servers, setServers] = useState<ServerInfo[]>([])
  const terms = useRef<TerminalsHandle>(null)
  const [everReady, setEverReady] = useState(false)

  const refresh = useCallback(async () => {
    try { setStatus(await hub.status()) } catch { setStatus(undefined) }
  }, [])

  useEffect(() => {
    hub.getState().then(setHubState).catch(() => {})
    const offState = hub.onState(setHubState)
    const offEvent = hub.onEvent((e) => { if (e.method === 'locked') refresh() })
    return () => { offState(); offEvent() }
  }, [refresh])

  useEffect(() => {
    if (hubState.kind === 'running') refresh()
    else { setStatus(undefined); setUnlockError(undefined) }
  }, [hubState, refresh])

  const screen = screenFor(hubState, status, unlockError)

  useEffect(() => {
    if (screen.kind === 'ready') hub.servers().then(setServers).catch(() => setServers([]))
    else setServers([])
  }, [screen.kind])

  const unlock = async (pw: string) => {
    try { await hub.unlock(pw); setUnlockError(undefined) } catch (e) { setUnlockError((e as Error).message) }
    await refresh()
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
          <HostList servers={servers} onOpen={(name) => terms.current?.open(name)} />
        </>
      ) : screen.kind === 'hub' ? (
        <HubScreen state={screen.state} />
      ) : screen.kind === 'no-store' ? (
        <div className="center"><p>No vault yet. Run <code>ssh-mcp web</code> to add servers, then restart the app.</p></div>
      ) : (
        <div className="center"><Unlock onUnlock={unlock} error={screen.error} /></div>
      )}
      {(ready || everReady) && (
        <main className="work" style={ready ? undefined : { display: 'none' }} inert={!ready}><Terminals ref={terms} /></main>
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

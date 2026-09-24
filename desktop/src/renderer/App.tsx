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

  switch (screen.kind) {
    case 'hub':
      return <HubScreen state={screen.state} />
    case 'no-store':
      return <div className="center"><p>No vault yet. Run <code>ssh-mcp web</code> to add servers, then restart the app.</p></div>
    case 'locked':
      return <div className="center"><Unlock onUnlock={unlock} error={screen.error} /></div>
    case 'ready':
      return (
        <div className="layout">
          <header>
            <span>ssh-mcp</span>
            <button onClick={async () => { try { await hub.lock(); setUnlockError(undefined); await refresh() } catch { /* the locked/hub-state events recover the UI */ } }}>Lock</button>
          </header>
          <HostList servers={servers} onOpen={(name) => terms.current?.open(name)} />
          <main className="work"><Terminals ref={terms} /></main>
        </div>
      )
  }
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

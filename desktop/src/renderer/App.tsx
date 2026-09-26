import { useCallback, useEffect, useRef, useState } from 'react'
import type { HostKeyMismatch, HubState, ServerInfo, Status } from '../shared/protocol'
import { hub } from './transport'
import { screenFor } from './shell'
import { Unlock } from './Unlock'
import { CreateVault } from './CreateVault'
import { HostList } from './HostList'
import { HostEditor } from './HostEditor'
import { ImportSheet } from './ImportSheet'
import { HostKeyDialog, HostKeyMismatchDialog } from './HostKeyDialog'
import { HostKeyPrompts } from './hostkeys'
import { Terminals, type TerminalsHandle } from './TerminalTabs'
import { ApprovalPanel } from './ApprovalPanel'
import { StoreErrorBanner } from './StoreError'
import { Latest, mergeSeed, reduceApprovals, type PendingItem } from './approvals'
import { loadPref, resolveTheme, savePref, type ThemePref } from './theme'
import { ThemeControl } from './ThemeControl'
import { LockIcon, Mark } from './icons'

export function App() {
  const [hubState, setHubState] = useState<HubState>({ kind: 'starting' })
  const [status, setStatus] = useState<Status>()
  const [unlockError, setUnlockError] = useState<string>()
  const [lockReason, setLockReason] = useState<'idle' | 'manual'>()
  const [servers, setServers] = useState<ServerInfo[]>([])
  const terms = useRef<TerminalsHandle>(null)
  const [everReady, setEverReady] = useState(false)
  const [editing, setEditing] = useState<{ name?: string; focusForget?: boolean }>()
  const [importing, setImporting] = useState(false)
  const hostKeys = useRef(new HostKeyPrompts()).current
  const [mismatch, setMismatch] = useState<HostKeyMismatch>()
  const [themePref, setThemePref] = useState<ThemePref>(() => loadPref())
  const [prefersDark, setPrefersDark] = useState(() => matchMedia('(prefers-color-scheme: dark)').matches)
  useEffect(() => {
    const m = matchMedia('(prefers-color-scheme: dark)')
    const on = (e: MediaQueryListEvent) => setPrefersDark(e.matches)
    m.addEventListener('change', on)
    return () => m.removeEventListener('change', on)
  }, [])
  const theme = resolveTheme(themePref, prefersDark)
  useEffect(() => { document.documentElement.dataset.theme = theme }, [theme])
  const chooseTheme = (p: ThemePref) => { setThemePref(p); savePref(p) }

  const refresh = useCallback(async () => {
    try { setStatus(await hub.status()) } catch { setStatus(undefined) }
  }, [])

  useEffect(() => {
    hub.getState().then(setHubState).catch(() => {})
    const offState = hub.onState(setHubState)
    const offEvent = hub.onEvent((e) => {
      if (e.method === 'locked') { setLockReason(e.params?.reason === 'idle' ? 'idle' : 'manual'); refresh() }
      // The MCP door reloads the vault file on every AI call: re-read status
      // so a refused reload shows its banner before the user decides.
      if (e.method === 'pending') refresh()
    })
    return () => { offState(); offEvent() }
  }, [refresh])

  useEffect(() => {
    if (hubState.kind === 'running') refresh()
    else { setStatus(undefined); setUnlockError(undefined); setLockReason(undefined) }
  }, [hubState, refresh])

  const screen = screenFor(hubState, status, unlockError)

  // One fetch per screen change or host edit, never a poll: every call but
  // status counts as UI activity and would hold off the idle lock. status
  // follows, so a refused reload of the vault file shows as storeError.
  const reloadServers = useCallback(() => hub.servers().then(setServers).catch(() => setServers([])).finally(refresh), [refresh])
  useEffect(() => {
    if (screen.kind === 'ready' || screen.kind === 'create-vault') reloadServers()
    else setServers([])
  }, [screen.kind, reloadServers])

  const [items, setItems] = useState<PendingItem[]>([])
  const [seedError, setSeedError] = useState<string>()
  const decidedSince = useRef(new Set<string>())
  const pendingSince = useRef(new Set<string>())
  const seedGen = useRef(new Latest())
  const [aiOpen, setAiOpen] = useState(false)
  const seenIds = useRef(new Set<string>())
  // Bumped by the terminal shortcut; the effect runs after the column (if it was
  // just opened) has mounted in the same commit. Arriving requests never focus.
  const [focusReason, setFocusReason] = useState(0)
  useEffect(() => {
    if (focusReason) (document.querySelector('.approvals .approval input') as HTMLInputElement | null)?.focus()
  }, [focusReason])
  const focusApprovals = useCallback(() => { setAiOpen(true); setFocusReason((n) => n + 1) }, [])
  useEffect(() => hub.onEvent((e) => {
    // Only approval events can change items. A no-op setItems still queues an update
    // (holding e) until App next renders, so calling it per term.data leaks every chunk.
    if (e.method !== 'pending' && e.method !== 'decided') return
    if (e.method === 'decided') decidedSince.current.add(e.params.request.id)
    else pendingSince.current.add(e.params.request.id)
    setItems((cur) => reduceApprovals(cur, e, Date.now()))
    // Collapsing is the user's choice; a new request reopens the column.
    if (e.method === 'pending' && !seenIds.current.has(e.params.request.id)) {
      seenIds.current.add(e.params.request.id)
      setAiOpen(true)
    }
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
          if (p.length > 0) { for (const r of p) seenIds.current.add(r.id); setAiOpen(true) }
        })
        .catch((e) => { if (seedGen.current.isCurrent(gen)) setSeedError((e as Error).message) })
    }
    if (hubState.kind !== 'running') { setItems([]); setSeedError(undefined) }
  }, [screen.kind, hubState.kind])

  const unlock = async (pw: string) => {
    try { await hub.unlock(pw); setUnlockError(undefined); setLockReason(undefined) } catch (e) { setUnlockError((e as Error).message) }
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

  const lock = async () => { try { await hub.lock(); setUnlockError(undefined); await refresh() } catch { /* the locked/hub-state events recover the UI */ } }
  const actions = (
    <>
      {(aiOpen || items.length > 0) && (
        <button type="button" className={'btn aibtn' + (!aiOpen && items.length > 0 ? ' waiting' : '')}
          aria-label="AI requests" aria-expanded={aiOpen} onClick={() => setAiOpen((o) => !o)}>
          AI <span className={'count' + (items.length > 0 ? ' waiting' : '')}>{items.length}</span>
        </button>
      )}
      <ThemeControl pref={themePref} onChange={chooseTheme} />
      <button type="button" className="btn" onClick={lock}><LockIcon />Lock</button>
    </>
  )
  const hostList = (
    <HostList servers={servers} storePath={status?.storePath ?? ''} onOpen={(name) => terms.current?.open(name)}
      onNew={() => { setImporting(false); setEditing({}) }}
      onEdit={async (name) => { setImporting(false); await reloadServers(); setEditing({ name }) }}
      onDelete={deleteHost}
      onImport={() => { setEditing(undefined); setImporting(true) }} />
  )

  // Once shown, the shell (tabs, terminals) stays mounted, hidden and inert, through
  // lock and hub restarts, so SSH sessions survive a lock and a restart can end them
  // with Reconnect.
  return (
    <div className="app">
      {!ready && (screen.kind === 'hub' ? (
        <HubScreen key={screen.state.kind === 'restarting' ? `r${screen.state.attempt}` : screen.state.kind} state={screen.state} />
      ) : screen.kind === 'create-vault' ? (
        <CreateVault servers={servers} onCreate={createVault} />
      ) : (
        <Unlock onUnlock={unlock} error={screen.error} lockReason={lockReason} storePath={status?.storePath} />
      ))}
      {(ready || everReady) && (
        <div className="shell" style={ready ? undefined : { display: 'none' }} inert={!ready}>
          <main className="work">
            <Terminals ref={terms} theme={theme} hostKeys={hostKeys} onMismatch={setMismatch} onTrusted={reloadServers}
              home={hostList} actions={actions} banner={<StoreErrorBanner message={status?.storeError} />} servers={servers}
              onFocusApprovals={focusApprovals} />
            {/* Inside the work area: the host list and the AI column stay usable beside it. */}
            {ready && editing && (
              <HostEditor key={editing.name ?? ''} server={servers.find((s) => s.name === editing.name)}
                openTabs={editing.name ? terms.current?.openCount(editing.name) ?? 0 : 0} focusForget={editing.focusForget}
                onSave={async (input, original) => { await hub.saveServer(input, original); await reloadServers(); setEditing(undefined) }}
                onForget={async (name) => { await hub.forgetHostKey(name); await reloadServers() }}
                onClose={() => setEditing(undefined)} />
            )}
            {ready && importing && (
              <ImportSheet scan={hub.importScan} apply={hub.importApply} onImported={reloadServers}
                onClose={() => setImporting(false)} />
            )}
          </main>
          {ready && aiOpen && (
            <ApprovalPanel items={items} seedError={seedError}
              onDecide={(id, outcome, reason) => hub.decide(id, outcome, reason)}
              onDenyAll={() => hub.denyAll('denied all by user')}
              onSendToTab={async (item) => {
                await terms.current!.sendToTab(item.request.server, item.request.command)
                await hub.decide(item.request.id, 'sent_to_tab')
              }}
              onClose={() => { setAiOpen(false); terms.current?.focusActive() }}
              onEscape={() => terms.current?.focusActive()} />
          )}
        </div>
      )}
      {ready && <HostKeyDialog prompts={hostKeys} />}
      {ready && mismatch && (
        <HostKeyMismatchDialog info={mismatch} onClose={() => setMismatch(undefined)}
          onEdit={async () => { const name = mismatch.server; setMismatch(undefined); setImporting(false); await reloadServers(); setEditing({ name, focusForget: true }) }} />
      )}
    </div>
  )
}

function HubScreen({ state }: { state: HubState }) {
  const [now, setNow] = useState(Date.now())
  const [since] = useState(Date.now())
  useEffect(() => {
    if (state.kind !== 'restarting') return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [state])
  if (state.kind === 'failed') {
    return (
      <div className="lockpage"><div className="lockcard wide">
        <Mark />
        <h1>The hub stopped</h1>
        <p>{state.message}</p>
        {state.stderr && <pre className="stderr">{state.stderr}</pre>}
      </div></div>
    )
  }
  if (state.kind === 'restarting') {
    const left = Math.max(0, Math.round((since + state.inMs - now) / 1000))
    return (
      <div className="lockpage"><div className="lockcard">
        <Mark />
        <h1>{`Hub crashed. Restarting in ${left} s (attempt ${state.attempt}).`}</h1>
        <p className="muted">The vault will be locked again after the restart. Open terminals will end.</p>
      </div></div>
    )
  }
  return (
    <div className="lockpage"><div className="lockcard">
      <Mark />
      <p className="starting"><span className="spinner" aria-hidden="true" />Starting the hub…</p>
    </div></div>
  )
}

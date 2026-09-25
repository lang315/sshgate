import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react'
import { hub } from './transport'
import { Dispatcher, TabSet } from './terminals'
import { TermView, type TermApi, type TermEvent } from './TermView'

export interface TerminalsHandle {
  open(server: string): void
  sendToTab(server: string, text: string): Promise<void>
  openCount(server: string): number
}

export const Terminals = forwardRef<TerminalsHandle>(function Terminals(_props, ref) {
  const tabs = useRef(new TabSet()).current
  const apis = useRef(new Map<string, TermApi>()).current
  const events = useRef(new Dispatcher<TermEvent>()).current
  useEffect(() => {
    const offEvent = hub.onEvent((e) => {
      const id = (e.params as { id?: unknown } | undefined)?.id
      if (e.method.startsWith('term.') && typeof id === 'string') events.emit(id, e)
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
    async sendToTab(server, text) {
      let tab = tabs.mostRecentFor(server)
      if (!tab) tab = tabs.open(server)
      tabs.activate(tab.id); changed()
      await waitReady(tab.id)
      apis.get(tab.id)!.paste(text)
    },
    openCount: (server) => tabs.openCount(server),
  }))

  return (
    <div className="terms">
      <div className="tabbar">
        {tabs.tabs.map((t) => (
          <div key={t.id} className={'tab' + (t.id === tabs.active ? ' active' : '')}>
            <button onClick={() => { tabs.activate(t.id); changed() }}>
              {t.server}{t.state === 'exited' ? ' (exited)' : ''}
            </button>
            {t.state === 'exited' && (
              <button onClick={() => { tabs.close(t.id); tabs.open(t.server); changed() }}>Reconnect</button>
            )}
            <button title="Close" aria-label="Close" onClick={() => { tabs.close(t.id); changed() }}>×</button>
          </div>
        ))}
      </div>
      <div className="termarea">
        {tabs.tabs.map((t) => (
          <TermView key={t.id} tab={t} tabs={tabs} events={events} visible={t.id === tabs.active} onChange={changed} register={register} />
        ))}
      </div>
    </div>
  )
})

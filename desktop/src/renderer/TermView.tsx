import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { hub, fromBase64 } from './transport'
import { Debouncer, printable, type Tab, type TabSet } from './terminals'

export interface TermApi { paste(text: string): void }

export function TermView({ tab, tabs, visible, onChange, register }: {
  tab: Tab; tabs: TabSet; visible: boolean; onChange: () => void
  register: (id: string, api: TermApi | undefined) => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal>(undefined)
  const visibleRef = useRef(visible)
  visibleRef.current = visible

  useEffect(() => { if (visible) termRef.current?.focus() }, [visible])

  useEffect(() => {
    const el = ref.current!
    const term = new Terminal({ convertEol: false, fontFamily: 'Menlo, Consolas, monospace', fontSize: 13 })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(el)
    fit.fit()
    termRef.current = term
    const enc = new TextEncoder()
    // Nothing is sent for this id before the term.open reply, nor after it ends.
    let phase: 'opening' | 'open' | 'ended' = 'opening'
    let disposed = false
    let heldAck = 0 // bytes rendered before the open reply; acked once it arrives
    let sent = { rows: term.rows, cols: term.cols }

    const end = (reason: string) => {
      if (phase === 'ended') return
      phase = 'ended'
      reason = printable(reason)
      term.write(`\r\n[exited: ${reason}]\r\n`)
      tabs.exited(tab.id, reason)
      onChange()
    }

    register(tab.id, { paste: (text) => term.paste(text) })

    const opening = hub.termOpen(tab.id, tab.server, sent.rows, sent.cols)
    opening.then(
      () => {
        if (disposed || phase !== 'opening') return
        phase = 'open'
        if (heldAck) { hub.termAck(tab.id, heldAck); heldAck = 0 }
        if (term.rows !== sent.rows || term.cols !== sent.cols) {
          sent = { rows: term.rows, cols: term.cols }
          hub.termResize(tab.id, sent.rows, sent.cols)
        }
        tabs.opened(tab.id)
        onChange()
        if (visibleRef.current) term.focus()
      },
      (e) => { if (!disposed) end(`open failed: ${(e as Error).message}`) },
    )

    const dataSub = term.onData((s) => { if (phase === 'open') hub.termWrite(tab.id, enc.encode(s)) })
    const off = hub.onEvent((e) => {
      if (!('params' in e) || (e.params as { id?: string }).id !== tab.id || phase === 'ended') return
      if (e.method === 'term.data') {
        const bytes = fromBase64(e.params.data)
        term.write(bytes, () => {
          if (disposed || phase === 'ended') return
          if (phase === 'open') hub.termAck(tab.id, bytes.length)
          else heldAck += bytes.length
        })
        if (!tab.sawOutput) { tabs.output(tab.id); onChange() }
      } else if (e.method === 'term.exit') {
        end(e.params.reason || `code ${e.params.code}`)
      } else if (e.method === 'term.dropped') {
        term.write(`\r\n[input dropped: ${e.params.bytes} bytes]\r\n`)
      }
    })
    const offState = hub.onState((s) => { if (s.kind !== 'running') end('hub restarted') })
    const resize = new Debouncer(50, () => {
      if (!el.clientHeight) return // hidden tab: fit would shrink the remote pty to its minimum
      fit.fit()
      if (phase === 'open' && (term.rows !== sent.rows || term.cols !== sent.cols)) {
        sent = { rows: term.rows, cols: term.cols }
        hub.termResize(tab.id, sent.rows, sent.cols)
      }
    })
    const ro = new ResizeObserver(() => resize.poke())
    ro.observe(el)

    return () => {
      disposed = true
      ro.disconnect(); resize.cancel(); dataSub.dispose(); off(); offState()
      register(tab.id, undefined)
      // Closing while opening waits for the reply; an ended session needs no close.
      if (phase !== 'ended') opening.then(() => hub.termClose(tab.id)).catch(() => {})
      termRef.current = undefined
      term.dispose()
    }
    // tab identity is fixed for the life of this component
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return <div className="term" ref={ref} style={{ display: visible ? 'block' : 'none' }} />
}

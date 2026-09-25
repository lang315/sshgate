import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { hub, fromBase64 } from './transport'
import type { HostKeyMismatch, HubEvent } from '../shared/protocol'
import type { HostKeyPrompts } from './hostkeys'
import { clipboardKey, Debouncer, isUserInput, printable, type Dispatcher, type Tab, type TabSet } from './terminals'
import { MONO_FONT, xtermTheme, type Theme } from './theme'

export interface TermApi { paste(text: string): void }
// A hub event for this tab's id, or 'hub.stopped' when the hub leaves the running state.
export type TermEvent = HubEvent | { method: 'hub.stopped' }

export function TermView({ tab, tabs, events, visible, onChange, register, theme, hostKeys, onMismatch, onTrusted }: {
  tab: Tab; tabs: TabSet; events: Dispatcher<TermEvent>; visible: boolean; onChange: () => void
  register: (id: string, api: TermApi | undefined) => void
  theme: Theme; hostKeys: HostKeyPrompts; onMismatch: (m: HostKeyMismatch) => void; onTrusted: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal>(undefined)
  const visibleRef = useRef(visible)
  visibleRef.current = visible

  useEffect(() => { if (visible) termRef.current?.focus() }, [visible])
  useEffect(() => { if (termRef.current) termRef.current.options.theme = xtermTheme(theme) }, [theme])

  useEffect(() => {
    const el = ref.current!
    const term = new Terminal({ convertEol: false, fontFamily: MONO_FONT, fontSize: 13, lineHeight: 1.25, theme: xtermTheme(theme) })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(el)
    fit.fit()
    termRef.current = term
    // Copy runs xterm's own copy handler; paste is left to Blink, whose paste event
    // xterm handles as a paste (bracketed). Neither sends the key itself to the shell.
    term.attachCustomKeyEventHandler((e) => {
      const a = clipboardKey(e, navigator.platform)
      if (a === 'copy' && e.type === 'keydown') document.execCommand('copy')
      return a === 'pass'
    })
    const enc = new TextEncoder()
    // Nothing is sent for this id before the term.open reply, nor after it ends.
    let phase: 'opening' | 'open' | 'ended' = 'opening'
    let disposed = false
    let heldAck = 0 // bytes rendered before the open reply; acked once it arrives
    let sent = { rows: term.rows, cols: term.cols }

    const end = (reason: string) => {
      if (phase === 'ended') return
      phase = 'ended'
      hostKeys.drop(tab.id) // a prompt for a dead tab must not stay queued
      reason = printable(reason)
      term.write(`\r\n[exited: ${reason}]\r\n`)
      tabs.exited(tab.id, reason)
      onChange()
    }

    register(tab.id, { paste: (text) => term.paste(text) })

    // An unknown host key goes to the user; each Trust retries once, with the
    // same id, pinned to exactly the confirmed key. A changed key is refused.
    const open = async (): Promise<void> => {
      let r = await hub.termOpen(tab.id, tab.server, sent.rows, sent.cols)
      let trusted = false
      while (r.status === 'hostKeyUnknown') {
        if (!(await hostKeys.ask(tab.id, r)) || disposed || phase !== 'opening') throw new Error('host key not trusted')
        r = await hub.termOpen(tab.id, tab.server, sent.rows, sent.cols, { fingerprint: r.fingerprint, keyType: r.keyType })
        trusted = true
      }
      if (r.status === 'hostKeyMismatch') {
        onMismatch(r)
        throw new Error('host key mismatch')
      }
      if (trusted) onTrusted()
    }
    const opening = open()
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
      (e) => {
        // A timed-out open may still finish in the hub later; close it so it is not orphaned.
        hub.termClose(tab.id).catch(() => {})
        if (!disposed) end(`open failed: ${(e as Error).message}`)
      },
    )

    // Real user input in this terminal; capture phase, since xterm stops some events.
    let lastInputAt = -Infinity
    const input = () => { lastInputAt = Date.now() }
    const inputEvents = ['keydown', 'paste', 'compositionend', 'mousedown'] as const
    for (const t of inputEvents) el.addEventListener(t, input, true)
    const dataSub = term.onData((s) => {
      if (phase === 'open') hub.termWrite(tab.id, enc.encode(s), isUserInput(lastInputAt, Date.now()))
    })
    const off = events.on(tab.id, (e) => {
      if (phase === 'ended') return
      if (e.method === 'hub.stopped') {
        end('hub restarted')
      } else if (e.method === 'term.data') {
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
      hostKeys.drop(tab.id)
      ro.disconnect(); resize.cancel(); dataSub.dispose(); off()
      for (const t of inputEvents) el.removeEventListener(t, input, true)
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

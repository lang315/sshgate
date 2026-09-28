export function newTermId(): string {
  const b = new Uint8Array(8)
  crypto.getRandomValues(b)
  return 't-' + Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

export interface Tab {
  id: string
  server: string
  kind: 'term' | 'files' | 'tunnels'
  state: 'opening' | 'open' | 'exited'
  exitReason?: string
  sawOutput: boolean
}

export class TabSet {
  tabs: Tab[] = []
  active?: string
  private order: string[] = [] // most recent last

  open(server: string, kind: Tab['kind'] = 'term'): Tab {
    const tab: Tab = { id: newTermId(), server, kind, state: kind === 'term' ? 'opening' : 'open', sawOutput: false }
    this.tabs.push(tab)
    this.activate(tab.id)
    return tab
  }
  private find(id: string) { return this.tabs.find((t) => t.id === id) }
  // One Tunnels tab per host: the existing one, if any.
  findKind(server: string, kind: Tab['kind']): Tab | undefined {
    return this.tabs.find((t) => t.server === server && t.kind === kind)
  }
  opened(id: string) { const t = this.find(id); if (t && t.state === 'opening') t.state = 'open' }
  output(id: string) { const t = this.find(id); if (t) t.sawOutput = true }
  exited(id: string, reason: string) { const t = this.find(id); if (t) { t.state = 'exited'; t.exitReason = reason } }
  activate(id: string) {
    this.active = id
    this.order = this.order.filter((x) => x !== id).concat(id)
  }
  // No terminal active: the Hosts home tab shows.
  showHome() { this.active = undefined }
  close(id: string) {
    this.tabs = this.tabs.filter((t) => t.id !== id)
    this.order = this.order.filter((x) => x !== id)
    if (this.active === id) this.active = this.order[this.order.length - 1]
  }
  mostRecentFor(server: string): Tab | undefined {
    for (let i = this.order.length - 1; i >= 0; i--) {
      const t = this.find(this.order[i])
      if (t && t.server === server && t.kind === 'term' && t.state !== 'exited') return t
    }
    return undefined
  }

  openCount(server: string): number {
    return this.tabs.filter((t) => t.server === server && t.kind === 'term' && t.state !== 'exited').length
  }
}

export class Debouncer {
  private timer?: ReturnType<typeof setTimeout>
  constructor(private readonly ms: number, private readonly fn: () => void) {}
  poke() { if (this.timer) clearTimeout(this.timer); this.timer = setTimeout(this.fn, this.ms) }
  cancel() { if (this.timer) clearTimeout(this.timer) }
}

export const USER_INPUT_WINDOW_MS = 1000

// Whether term.write data counts as user input for the hub's idle auto-lock: xterm
// also answers terminal queries (device attributes, cursor reports) through onData,
// and those replies must not keep the vault unlocked (R17).
// ponytail: time heuristic; an auto-reply within 1 s of real input still counts as
// input (at most 1 s of extra hold-off per keystroke). Upgrade path: tag xterm's own
// replies at the source if xterm ever exposes that.
export function isUserInput(lastInputAt: number, now: number): boolean {
  return now - lastInputAt < USER_INPUT_WINDOW_MS
}

// Hub-supplied text written into xterm must not carry escape sequences.
export const printable = (s: string) => s.replace(/[\x00-\x1f\x7f-\x9f]/g, '')

// Routes events to one handler per terminal id, so all tabs share a single
// hub:event / hub:state IPC listener instead of one each (max-listeners warning).
export class Dispatcher<E> {
  private handlers = new Map<string, (e: E) => void>()
  on(id: string, h: (e: E) => void): () => void {
    this.handlers.set(id, h)
    return () => { if (this.handlers.get(id) === h) this.handlers.delete(id) }
  }
  emit(id: string, e: E): void { this.handlers.get(id)?.(e) }
  emitAll(e: E): void { for (const h of [...this.handlers.values()]) h(e) }
}

// Ctrl+Shift+C/V copy and paste on Windows/Linux, like GNOME Terminal; keyCode, as
// Blink's own paste binding uses. macOS has Cmd+C/V through the Edit menu.
export function clipboardKey(
  e: { keyCode: number; ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean },
  platform: string,
): 'copy' | 'paste' | 'pass' {
  if (platform.startsWith('Mac') || !e.ctrlKey || !e.shiftKey || e.altKey || e.metaKey) return 'pass'
  return e.keyCode === 67 ? 'copy' : e.keyCode === 86 ? 'paste' : 'pass'
}

// Ctrl+Shift+A (Cmd+Shift+A on macOS) jumps from a terminal to the oldest AI
// request: xterm keeps Tab, so without it the keyboard cannot reach Deny.
// keyCode, like clipboardKey: a non-Latin layout still reports 65 for A.
export function approvalsKey(
  e: { type: string; keyCode: number; ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean },
  platform: string,
): boolean {
  const mac = platform.startsWith('Mac')
  const mod = mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey
  return mod && e.shiftKey && !e.altKey && e.keyCode === 65
}

export function newTermId(): string {
  const b = new Uint8Array(8)
  crypto.getRandomValues(b)
  return 't-' + Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

export interface Tab {
  id: string
  server: string
  state: 'opening' | 'open' | 'exited'
  exitReason?: string
  sawOutput: boolean
}

export class TabSet {
  tabs: Tab[] = []
  active?: string
  private order: string[] = [] // most recent last

  open(server: string): Tab {
    const tab: Tab = { id: newTermId(), server, state: 'opening', sawOutput: false }
    this.tabs.push(tab)
    this.activate(tab.id)
    return tab
  }
  private find(id: string) { return this.tabs.find((t) => t.id === id) }
  opened(id: string) { const t = this.find(id); if (t && t.state === 'opening') t.state = 'open' }
  output(id: string) { const t = this.find(id); if (t) t.sawOutput = true }
  exited(id: string, reason: string) { const t = this.find(id); if (t) { t.state = 'exited'; t.exitReason = reason } }
  activate(id: string) {
    this.active = id
    this.order = this.order.filter((x) => x !== id).concat(id)
  }
  close(id: string) {
    this.tabs = this.tabs.filter((t) => t.id !== id)
    this.order = this.order.filter((x) => x !== id)
    if (this.active === id) this.active = this.order[this.order.length - 1]
  }
  mostRecentFor(server: string): Tab | undefined {
    for (let i = this.order.length - 1; i >= 0; i--) {
      const t = this.find(this.order[i])
      if (t && t.server === server && t.state !== 'exited') return t
    }
    return undefined
  }
}

export class Debouncer {
  private timer?: ReturnType<typeof setTimeout>
  constructor(private readonly ms: number, private readonly fn: () => void) {}
  poke() { if (this.timer) clearTimeout(this.timer); this.timer = setTimeout(this.fn, this.ms) }
  cancel() { if (this.timer) clearTimeout(this.timer) }
}

// Hub-supplied text written into xterm must not carry escape sequences.
export const printable = (s: string) => s.replace(/[\x00-\x1f\x7f-\x9f]/g, '')

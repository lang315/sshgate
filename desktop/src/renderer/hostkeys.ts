import type { HostKeyUnknown } from '../shared/protocol'
import { ALLOW_DELAY_MS } from './approvals'

export interface Prompt { id: string; info: HostKeyUnknown; resolve: (trust: boolean) => void }

// Host-key prompts from every tab, shown one at a time in arrival order.
export class HostKeyPrompts {
  private q: Prompt[] = []
  private subs = new Set<() => void>()
  ask(id: string, info: HostKeyUnknown): Promise<boolean> {
    return new Promise((resolve) => { this.q.push({ id, info, resolve }); this.changed() })
  }
  get current(): Prompt | undefined { return this.q[0] }
  answer(trust: boolean): void {
    const p = this.q.shift()
    if (p) { p.resolve(trust); this.changed() }
  }
  // A tab closed while waiting: its prompt goes away unanswered.
  drop(id: string): void {
    const i = this.q.findIndex((p) => p.id === id)
    if (i < 0) return
    this.q.splice(i, 1)[0].resolve(false)
    this.changed()
  }
  subscribe(cb: () => void): () => void {
    this.subs.add(cb)
    return () => { this.subs.delete(cb) }
  }
  private changed() { for (const cb of [...this.subs]) cb() }
}

// Identifies what the dialog shows; a change restarts the Trust delay.
export const promptKey = (p?: Prompt) => (p ? `${p.id}|${p.info.fingerprint}` : '')

// Trust stays disabled for ALLOW_DELAY_MS after the dialog's content last
// changed: a queued prompt can replace the dialog under the cursor, the same
// hazard as the approval list shifting.
export const trustEnabled = (changedAt: number, now: number) => now - changedAt >= ALLOW_DELAY_MS

import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { EventEmitter } from 'node:events'
import * as readline from 'node:readline'
import { PROTOCOL_VERSION, type HubState } from '../shared/protocol'

export interface HubProcessOptions {
  command: string
  args: string[]
  env?: NodeJS.ProcessEnv
  backoffMs?: number[]          // default [1000, 3000, 10000]
  crashWindowMs?: number        // default 60000
  maxCrashes?: number           // default 4
  helloTimeoutMs?: number       // default 10000
}

interface Pending { resolve: (v: unknown) => void; reject: (e: Error) => void; timer?: NodeJS.Timeout }

const STDERR_TAIL_LINES = 50

export class HubProcess extends EventEmitter {
  private child?: ChildProcessWithoutNullStreams
  private nextId = 1
  private pending = new Map<number, Pending>()
  private crashes: number[] = []
  private stderrTail: string[] = []
  private stopping = false
  private restartTimer?: NodeJS.Timeout
  private _state: HubState = { kind: 'starting' }

  constructor(private readonly opts: HubProcessOptions) { super() }

  get state(): HubState { return this._state }

  private setState(s: HubState) { this._state = s; this.emit('state', s) }

  start(): void {
    if (this.child || this.restartTimer) return // already running, or a restart is already scheduled
    this.stopping = false
    this.spawnChild()
  }

  private pushStderr(line: string): void {
    this.stderrTail.push(line)
    if (this.stderrTail.length > STDERR_TAIL_LINES) this.stderrTail.shift()
  }

  private spawnChild(): void {
    this.setState({ kind: 'starting' })
    const child = spawn(this.opts.command, this.opts.args, { env: this.opts.env, stdio: 'pipe' })
    this.child = child
    readline.createInterface({ input: child.stdout }).on('line', (l) => this.onLine(l))
    readline.createInterface({ input: child.stderr }).on('line', (l) => this.pushStderr(l))
    child.stdin.on('error', () => { /* EPIPE/write-after-end from a dying child; the close handler reports it */ })
    child.on('error', (err) => this.pushStderr(String(err)))
    // 'close' (not 'exit') fires once stdio has fully drained, and also after a spawn error
    // (e.g. ENOENT) where 'exit' never fires at all.
    child.on('close', () => this.onClose(child))
    const helloTimeoutMs = this.opts.helloTimeoutMs ?? 10000
    this.call<{ protocol: number }>('hello', undefined, helloTimeoutMs).then((r) => {
      if (this.child !== child) return
      if (r.protocol !== PROTOCOL_VERSION) {
        this.fail(`hub protocol ${r.protocol}, app expects ${PROTOCOL_VERSION}`)
        child.kill()
        return
      }
      this.setState({ kind: 'running' })
    }, () => {
      // hello timed out (or the call was rejected because the child already closed).
      // If it's still our current child, kill it so the close handler counts the crash.
      if (this.child === child) child.kill()
    })
  }

  private onLine(line: string): void {
    let m: { id?: number; method?: string; params?: unknown; result?: unknown; error?: { message: string } }
    try { m = JSON.parse(line) } catch { return }
    if (typeof m.id === 'number') {
      const p = this.pending.get(m.id)
      if (!p) return
      this.pending.delete(m.id)
      if (p.timer) clearTimeout(p.timer)
      if (m.error) p.reject(new Error(m.error.message))
      else p.resolve(m.result)
    } else if (typeof m.method === 'string') {
      this.emit('notification', m.method, m.params)
    }
  }

  private onClose(child: ChildProcessWithoutNullStreams): void {
    if (this.child !== child) return
    this.child = undefined
    for (const p of this.pending.values()) { if (p.timer) clearTimeout(p.timer); p.reject(new Error('hub restarted')) }
    this.pending.clear()
    if (this.stopping || this._state.kind === 'failed') return
    const now = Date.now()
    const windowMs = this.opts.crashWindowMs ?? 60000
    this.crashes = this.crashes.filter((t) => now - t < windowMs)
    this.crashes.push(now)
    const max = this.opts.maxCrashes ?? 4
    if (this.crashes.length >= max) {
      this.fail(`the hub crashed ${this.crashes.length} times in ${Math.round(windowMs / 1000)} s`)
      return
    }
    const backoff = this.opts.backoffMs ?? [1000, 3000, 10000]
    const inMs = backoff[Math.min(this.crashes.length - 1, backoff.length - 1)]
    this.setState({ kind: 'restarting', attempt: this.crashes.length, inMs })
    this.restartTimer = setTimeout(() => {
      this.restartTimer = undefined
      if (!this.stopping) this.spawnChild()
    }, inMs)
  }

  private fail(message: string): void {
    this.setState({ kind: 'failed', message, stderr: this.stderrTail.join('\n') })
  }

  call<T = unknown>(method: string, params?: unknown, timeoutMs?: number): Promise<T> {
    const child = this.child
    if (!child) return Promise.reject(new Error('hub is not running'))
    const id = this.nextId++
    return new Promise<T>((resolve, reject) => {
      const p: Pending = { resolve: resolve as (v: unknown) => void, reject }
      if (timeoutMs) p.timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`${method} timed out`)) }, timeoutMs)
      this.pending.set(id, p)
      child.stdin.write(JSON.stringify({ jsonrpc: '2.0', id, method, params: params ?? {} }) + '\n')
    })
  }

  notify(method: string, params?: unknown): void {
    this.child?.stdin.write(JSON.stringify({ jsonrpc: '2.0', method, params: params ?? {} }) + '\n')
  }

  async stop(): Promise<void> {
    this.stopping = true
    if (this.restartTimer) {
      clearTimeout(this.restartTimer)
      this.restartTimer = undefined
      // A restart was scheduled but hasn't happened yet: nothing to kill, but say so
      // instead of leaving the UI showing a countdown that will never resolve.
      if (this._state.kind === 'restarting') this.setState({ kind: 'failed', message: 'hub stopped', stderr: '' })
    }
    const child = this.child
    if (!child) return
    await new Promise<void>((resolve) => {
      let termTimer: NodeJS.Timeout | undefined
      let killTimer: NodeJS.Timeout | undefined
      child.once('close', () => {
        if (termTimer) clearTimeout(termTimer)
        if (killTimer) clearTimeout(killTimer)
        resolve()
      })
      termTimer = setTimeout(() => {
        child.kill()
        killTimer = setTimeout(() => {
          child.kill('SIGKILL')
          resolve() // don't hang forever if stdio stays open (e.g. a grandchild holding the pipe)
        }, 2000)
      }, 3000)
      child.stdin.end()
    })
  }
}

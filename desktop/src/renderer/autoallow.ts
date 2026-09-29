import type { AutoAllowCheck, AutoAllowMode, AutoAllowRan, AutoAllowState, HubEvent, ServerInfo } from '../shared/protocol'
import { ALLOW_DELAY_MS } from './approvals'
import type { HostDraft } from './hostForm'

// Per-host auto-allow (spec 2026-09-28-auto-allow-design.md). Everything here
// is pure: the renderer never asks the hub on a timer or on an auto-allow event.

// ms is the grant's duration, for the dialog's "Ends at <time>" copy; forever has none.
export const AUTO_MODES: { mode: Exclude<AutoAllowMode, 'off'>; label: string; ms?: number }[] = [
  { mode: '15m', label: '15 min', ms: 15 * 60_000 }, { mode: '30m', label: '30 min', ms: 30 * 60_000 },
  { mode: '60m', label: '60 min', ms: 60 * 60_000 }, { mode: '2h', label: '2 h', ms: 2 * 60 * 60_000 },
  { mode: '4h', label: '4 h', ms: 4 * 60 * 60_000 }, { mode: 'forever', label: 'Until turned off' },
]

const left = (a: AutoAllowState | undefined, now: number) => (a?.until ? Date.parse(a.until) - now : 0)

export function chipLabel(a: AutoAllowState | undefined, now: number): string | undefined {
  if (a?.forever) return a.paused ? 'Auto paused' : 'Auto ∞'
  const ms = left(a, now)
  if (ms <= 0) return undefined
  const min = Math.ceil(ms / 60_000)
  if (min < 60) return `Auto ${min}m`
  const h = Math.floor(min / 60), m = min % 60
  return m ? `Auto ${h}h ${m}m` : `Auto ${h}h`
}

export const isActive = (a: AutoAllowState | undefined, now: number) => !!(a?.forever ? !a.paused : left(a, now) > 0)

// Hosts whose auto-allow is on or paused: they get the tab dot and count in "auto N".
export const autoHosts = (servers: ServerInfo[], now: number) =>
  new Set(servers.filter((s) => isActive(s.autoAllow, now) || s.autoAllow?.paused).map((s) => s.name))

export const pausedHosts = (servers: ServerInfo[]) => servers.filter((s) => s.autoAllow?.paused).map((s) => s.name)

// On the locked notification: timed grants are gone; forever ones wait for Resume.
export const dropOnLock = (servers: ServerInfo[]): ServerInfo[] =>
  servers.map((s) => ({ ...s, autoAllow: s.autoAllow?.forever ? { forever: true, paused: true } : undefined }))

export const applyOff = (servers: ServerInfo[], server: string): ServerInfo[] =>
  servers.map((s) => (s.name === server ? { ...s, autoAllow: undefined } : s))

// handleAutoEvent applies autoAllow.off/autoAllow.ran to renderer state and
// nothing else: it never calls the hub. Every call but status counts as UI
// activity and would hold off the idle lock, so App's event dispatch must
// route these two notifications through here rather than the hub.
// autoAllow.off with reason 'locked' is ignored: the 'locked' notification's
// dropOnLock already pauses forever hosts, and applyOff would wipe that
// (it clears autoAllow entirely, dropping `paused`), regardless of which of
// the two notifications the renderer happens to see first.
export function handleAutoEvent(e: HubEvent, setServers: (fn: (cur: ServerInfo[]) => ServerInfo[]) => void, setAutoFeed: (fn: (feed: AutoAllowRan[]) => AutoAllowRan[]) => void): void {
  if (e.method === 'autoAllow.off' && e.params.reason !== 'locked') setServers((cur) => applyOff(cur, e.params.server))
  if (e.method === 'autoAllow.ran') setAutoFeed((f) => pushFeed(f, e.params))
}

export const FEED_CAP = 50
export const pushFeed = (feed: AutoAllowRan[], r: AutoAllowRan) => [r, ...feed].slice(0, FEED_CAP)

export const commandLabel = (r: AutoAllowRan) => (r.truncated ? `${r.command} … (truncated ${r.truncated} bytes)` : r.command)

// Enable is mouse-only and waits ALLOW_DELAY_MS after the dialog opens or its
// content changes; typeName also needs the host name typed exactly.
export function enableAllowed(o: { typeName: boolean; typed: string; host: string; refused?: string; openedAt: number; changedAt: number; now: number }): boolean {
  if (o.refused) return false
  if (o.typeName && o.typed !== o.host) return false
  return o.now - Math.max(o.openedAt, o.changedAt) >= ALLOW_DELAY_MS
}

// AutoAllowDialog's ListChanges key: anything that shifts the buttons must
// restart the Enable delay — the mode and warnings chosen in the editor, the
// effective sudo opt-in (it swaps which sudo-exec sentence shows), the
// root-access check result (root access revealed, or the check failing), an
// enable error appearing, or the remote-tunnels list. Excludes `typed`, so
// confirming "forever" by typing the host name doesn't itself restart the wait.
export const dialogChangeKey = (mode: Exclude<AutoAllowMode, 'off'>, rootNew: boolean, sudoNew: boolean, sudo: boolean, checked: AutoAllowCheck | 'error' | undefined, error: string | undefined, remoteTunnels: string[]): string =>
  JSON.stringify([mode, rootNew, sudoNew, sudo, checked === undefined ? 'pending' : checked === 'error' ? 'error' : [checked.uid, checked.passwordlessSudo], error, remoteTunnels])

// The Host editor's Auto-allow controls (spec Amendment 2026-09-29).

export const SAVED_BUT = 'saved, but auto-allow was not turned on: '

// The editor's note under the Auto-allow select when a timed grant is running.
export function timedNote(s: ServerInfo | undefined, now: number): string | undefined {
  const ms = left(s?.autoAllow, now)
  if (ms <= 0) return undefined
  return `On for ${Math.ceil(ms / 60_000)} more min — saving ends it; pick a duration to keep auto-allow on`
}

// Whether Save must show the Auto-allow confirm dialog, and which warnings it needs.
export function saveConfirm(s: ServerInfo | undefined, d: HostDraft): { needed: boolean; typeName: boolean; rootNew: boolean; sudoNew: boolean } {
  const alreadyArmedForever = d.autoAllow === 'forever' && !!s?.autoAllow?.forever && !s.autoAllow.paused &&
    d.autoAllowRoot === s.autoAllowRoot && d.autoAllowSudo === s.autoAllowSudo
  return {
    needed: d.autoAllow !== 'off' && !alreadyArmedForever,
    typeName: d.autoAllow === 'forever' && !s?.autoAllow?.forever,
    rootNew: !!d.autoAllowRoot && !s?.autoAllowRoot,
    sudoNew: !!d.autoAllowSudo && !s?.autoAllowSudo,
  }
}

// Why the editor's Auto-allow controls would fail on save, shown ahead of time;
// the hub has the final say on the draft's other edits.
export function draftRefusal(s: ServerInfo | undefined, d: HostDraft): string | undefined {
  if (!s || !s.hostKey) return 'no pinned host key'
  if (!d.aiVisible) return 'not visible to AI'
  if (s.autoAllowRefused === 'root login' || s.autoAllowRefused === 'has an su password' || s.autoAllowRefused === 'has a sudo password') {
    return d.autoAllowRoot ? undefined : s.autoAllowRefused
  }
  return undefined
}

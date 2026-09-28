import type { AutoAllowCheck, AutoAllowMode, AutoAllowRan, AutoAllowState, ServerInfo } from '../shared/protocol'
import { ALLOW_DELAY_MS } from './approvals'

// Per-host auto-allow (spec 2026-09-28-auto-allow-design.md). Everything here
// is pure: the renderer never asks the hub on a timer or on an auto-allow event.

export const AUTO_MODES: { mode: Exclude<AutoAllowMode, 'off'>; label: string }[] = [
  { mode: '15m', label: '15 min' }, { mode: '30m', label: '30 min' }, { mode: '60m', label: '60 min' },
  { mode: '2h', label: '2 h' }, { mode: '4h', label: '4 h' }, { mode: 'forever', label: 'Until turned off' },
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

export const FEED_CAP = 50
export const pushFeed = (feed: AutoAllowRan[], r: AutoAllowRan) => [r, ...feed].slice(0, FEED_CAP)

export const commandLabel = (r: AutoAllowRan) => (r.truncated ? `${r.command} … (truncated ${r.truncated} bytes)` : r.command)

// Enable is mouse-only and waits ALLOW_DELAY_MS after the dialog opens or its
// content changes; forever also needs the host name typed exactly.
export function enableAllowed(o: { mode: Exclude<AutoAllowMode, 'off'>; typed: string; host: string; refused?: string; openedAt: number; changedAt: number; now: number }): boolean {
  if (o.refused) return false
  if (o.mode === 'forever' && o.typed !== o.host) return false
  return o.now - Math.max(o.openedAt, o.changedAt) >= ALLOW_DELAY_MS
}

// AutoAllowDialog's ListChanges key: the mode or the root-access check result
// changing (root access revealed, or the check failing) shifts the buttons and
// must restart the Enable delay. Excludes `typed`, so confirming "forever" by
// typing the host name doesn't itself restart the wait.
export const dialogChangeKey = (mode: Exclude<AutoAllowMode, 'off'>, checked: AutoAllowCheck | 'error' | undefined): string =>
  `${mode}|${checked === undefined ? 'pending' : checked === 'error' ? 'error' : `${checked.uid}:${checked.passwordlessSudo}`}`

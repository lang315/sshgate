import type { SecretField, ServerInfo, ServerInput } from '../shared/protocol'

export const SECRET_FIELDS: { field: SecretField; label: string; has: (s: ServerInfo) => boolean }[] = [
  { field: 'password', label: 'Password', has: (s) => s.hasPassword },
  { field: 'suPassword', label: 'su password', has: (s) => s.hasSuPassword },
  { field: 'sudoPassword', label: 'sudo password', has: (s) => s.hasSudoPassword },
  { field: 'keyPassphrase', label: 'Key passphrase', has: (s) => s.hasKeyPassphrase },
]

export interface SecretEdit { value: string; cleared: boolean }

export interface HostDraft {
  name: string; host: string; port: string; user: string; auth: string; keyPath: string; aiVisible: boolean
  secrets: Record<SecretField, SecretEdit>
}

const untouched = (): SecretEdit => ({ value: '', cleared: false })

export function draftFrom(s?: ServerInfo): HostDraft {
  return {
    name: s?.name ?? '', host: s?.host ?? '', port: String(s?.port ?? 22), user: s?.user ?? '',
    auth: s?.auth ?? 'password', keyPath: s?.keyPath ?? '', aiVisible: s?.aiVisible ?? false,
    secrets: { password: untouched(), suPassword: untouched(), sudoPassword: untouched(), keyPassphrase: untouched() },
  }
}

// Secrets are write-only: an untouched field is omitted (the hub keeps it),
// a cleared one is sent as '', a typed one as its value.
export function toInput(d: HostDraft): ServerInput {
  const input: ServerInput = {
    name: d.name.trim() || d.host.trim(), host: d.host.trim(), port: Number(d.port), user: d.user.trim(), auth: d.auth,
    keyPath: d.auth === 'key' ? d.keyPath.trim() : '', aiVisible: d.aiVisible,
  }
  for (const { field } of SECRET_FIELDS) {
    const e = d.secrets[field]
    if (e.cleared) input[field] = ''
    else if (e.value !== '') input[field] = e.value
  }
  return input
}

// A saved secret the hub will drop (Clear, or a host/port change without a new
// value) reads "will be cleared", so "saved" never promises what a save removes.
// The label is optional and defaults to the address; the hub's name rule
// (A-Z a-z 0-9 . _ -) rejects an IPv6 address, so that one needs a label.
export function labelHint(d: HostDraft): string | undefined {
  return !d.name.trim() && d.host.includes(':') ? 'An IPv6 address cannot be a name: add a label.' : undefined
}

export function secretPlaceholder(saved: boolean, e: SecretEdit, endpointChanged = false): string {
  if (e.cleared) return 'will be cleared'
  if (!saved) return ''
  return endpointChanged && e.value === '' ? 'will be cleared' : 'saved'
}

export function endpointChanged(s: ServerInfo | undefined, d: HostDraft): boolean {
  return !!s && (d.host.trim() !== s.host || Number(d.port) !== s.port)
}

// Mirrors the hub: everything but "Visible to AI" is part of the connection,
// so changing it closes the server's open tabs.
export function closesTabs(s: ServerInfo | undefined, d: HostDraft): boolean {
  if (!s) return false
  const i = toInput(d)
  return i.name !== s.name || i.host !== s.host || i.port !== s.port || i.user !== s.user || i.auth !== s.auth ||
    i.keyPath !== s.keyPath || SECRET_FIELDS.some(({ field }) => i[field] !== undefined)
}

// Create vault's live checklist; the button stays disabled until both hold.
export function passwordChecks(pw: string, again: string): { length: boolean; match: boolean } {
  return { length: pw.length >= 8, match: again !== '' && pw === again }
}

// The Hosts search: a substring of the name, host or user, ignoring case.
// Renderer-only, so typing never calls the hub (and never resets the idle lock).
export function filterHosts(servers: ServerInfo[], query: string): ServerInfo[] {
  const q = query.trim().toLowerCase()
  if (!q) return servers
  return servers.filter((s) => [s.name, s.host, s.user].some((f) => f.toLowerCase().includes(q)))
}

// Arrow keys move through a radiogroup and wrap, as in a native one.
export function arrowStep<T>(items: readonly T[], current: T, key: string): T | undefined {
  const d = key === 'ArrowRight' || key === 'ArrowDown' ? 1 : key === 'ArrowLeft' || key === 'ArrowUp' ? -1 : 0
  if (!d) return undefined
  return items[(items.indexOf(current) + d + items.length) % items.length]
}

export function closeWarning(openTabs: number, transfers: number): string {
  const tabs = `close ${openTabs} open ${openTabs === 1 ? 'tab' : 'tabs'}`
  const jobs = `cancel ${transfers} ${transfers === 1 ? 'transfer' : 'transfers'}`
  if (openTabs && transfers) return `Saving will ${tabs} and ${jobs}.`
  return `Saving will ${openTabs ? tabs : jobs}.`
}

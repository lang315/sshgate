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
    name: d.name.trim(), host: d.host.trim(), port: Number(d.port), user: d.user.trim(), auth: d.auth,
    keyPath: d.auth === 'key' ? d.keyPath.trim() : '', aiVisible: d.aiVisible,
  }
  for (const { field } of SECRET_FIELDS) {
    const e = d.secrets[field]
    if (e.cleared) input[field] = ''
    else if (e.value !== '') input[field] = e.value
  }
  return input
}

export function secretPlaceholder(saved: boolean, e: SecretEdit): string {
  if (e.cleared) return 'will be cleared'
  return saved ? 'saved' : ''
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

export function vaultPasswordProblem(pw: string, again: string): string | undefined {
  if (pw.length < 8) return 'Use at least 8 characters.'
  if (pw !== again) return 'The passwords do not match.'
  return undefined
}

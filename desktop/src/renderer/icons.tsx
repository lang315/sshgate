import type { ReactNode } from 'react'

// 16 px line icons drawn in currentColor; decorative, so hidden from assistive tech.
const icon = (body: ReactNode) => function Icon() {
  return (
    <svg className="ico" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">{body}</svg>
  )
}

export const HomeIcon = icon(<><path d="M3 11l9-8 9 8" /><path d="M5 10v10h14V10" /></>)
export const PlusIcon = icon(<path d="M12 5v14M5 12h14" />)
export const EditIcon = icon(<path d="M4 20h4L19 9l-4-4L4 16z" />)
export const TrashIcon = icon(<><path d="M4 7h16" /><path d="M9 7V4h6v3" /><path d="M6 7l1 13h10l1-13" /></>)
export const LockIcon = icon(<><rect x="5" y="11" width="14" height="9" rx="2" /><path d="M8 11V8a4 4 0 0 1 8 0v3" /></>)
export const CloseIcon = icon(<path d="M6 6l12 12M18 6L6 18" />)
export const SunIcon = icon(<><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></>)
export const MoonIcon = icon(<path d="M20 14.5A8 8 0 0 1 9.5 4 8 8 0 1 0 20 14.5z" />)
export const TerminalIcon = icon(<><path d="M5 8l4 4-4 4" /><path d="M12 16h7" /></>)
export const WarningIcon = icon(<><path d="M12 3l10 18H2z" /><path d="M12 10v5M12 18h.01" /></>)
export const CheckIcon = icon(<path d="M5 12l5 5 9-10" />)
export const ShieldIcon = icon(<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z" />)

// The app's only mark: a terminal glyph on the accent tile, then the name. No logo.
export function Mark() {
  return (
    <div className="mark">
      <span className="mark-tile"><TerminalIcon /></span>
      <span className="mark-word">sshgate</span>
    </div>
  )
}

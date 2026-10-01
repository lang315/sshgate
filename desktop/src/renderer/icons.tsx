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
export const FolderIcon = icon(<path d="M3 6h6l2 2h10v11H3z" />)
export const FileIcon = icon(<><path d="M6 3h8l4 4v14H6z" /><path d="M14 3v4h4" /></>)
export const LinkIcon = icon(<><path d="M10 14a4 4 0 0 0 6 0l3-3a4 4 0 0 0-6-6l-1 1" /><path d="M14 10a4 4 0 0 0-6 0l-3 3a4 4 0 0 0 6 6l1-1" /></>)
export const UpIcon = icon(<path d="M12 19V5M6 11l6-6 6 6" />)
export const RefreshIcon = icon(<><path d="M20 11a8 8 0 1 0-2.3 5.7" /><path d="M20 4v7h-7" /></>)
export const UploadIcon = icon(<><path d="M12 16V4M7 9l5-5 5 5" /><path d="M4 20h16" /></>)
export const DownloadIcon = icon(<><path d="M12 4v12M7 11l5 5 5-5" /><path d="M4 20h16" /></>)
export const TunnelIcon = icon(<><path d="M4 8h13l-3-3" /><path d="M20 16H7l3 3" /></>)
export const ChevronDownIcon = icon(<path d="M6 9l6 6 6-6" />)
export const StopIcon = icon(<rect x="5" y="5" width="14" height="14" rx="2" />)
export const ListIcon = icon(<path d="M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01" />)

// The in-app mark: a terminal glyph on the accent tile, then the name. The app
// icon (desktop/build/icon.png, a shield) is a different drawing, used only for
// the bundle.
export function Mark() {
  return (
    <div className="mark">
      <span className="mark-tile"><TerminalIcon /></span>
      <span className="mark-word">sshgate</span>
    </div>
  )
}

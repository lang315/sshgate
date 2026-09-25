import type { ReactNode } from 'react'
import type { ThemePref } from './theme'
import { MoonIcon, SunIcon } from './icons'

const OPTIONS: { value: ThemePref; label: string; content: ReactNode }[] = [
  { value: 'dark', label: 'Dark', content: <MoonIcon /> },
  { value: 'light', label: 'Light', content: <SunIcon /> },
  { value: 'auto', label: 'Auto', content: 'Auto' },
]

export function ThemeControl({ pref, onChange }: { pref: ThemePref; onChange: (p: ThemePref) => void }) {
  return (
    <div className="seg" role="radiogroup" aria-label="Theme">
      {OPTIONS.map((o) => (
        <button key={o.value} type="button" role="radio" aria-checked={pref === o.value} aria-label={o.label}
          title={o.label} onClick={() => onChange(o.value)}>{o.content}</button>
      ))}
    </div>
  )
}

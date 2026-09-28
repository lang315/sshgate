import { useEffect, useRef, useState } from 'react'
import { ALLOW_DELAY_MS, blockKeyboardActivation, ListChanges } from './approvals'

// Shown after an unlock while forever hosts wait for Resume (spec: Desktop).
export function AutoAllowPaused({ hosts, onResume, onStop }: { hosts: string[]; onResume: () => void; onStop: () => void }) {
  const key = hosts.join('\n')
  // Computed during render, not in a useEffect: an effect runs after paint, so
  // the host list changing would leave one painted frame with a stale delay.
  const changes = useRef<ListChanges>(null)
  changes.current ??= new ListChanges(key, Date.now())
  changes.current.setKey(key, Date.now())
  const since = changes.current.at
  const [now, setNow] = useState(Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 100); return () => clearInterval(t) }, [])
  if (hosts.length === 0) return null
  return (
    <div className="banner auto" role="status">
      <span>{`Auto-allow is paused on ${hosts.join(', ')}.`}</span>
      <button type="button" className="btn" tabIndex={-1} disabled={now - since < ALLOW_DELAY_MS} onKeyDown={blockKeyboardActivation} onClick={onResume}>Resume</button>
      <button type="button" className="btn" onClick={onStop}>Stop</button>
    </div>
  )
}

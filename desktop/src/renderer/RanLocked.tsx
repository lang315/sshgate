// Shown after an unlock that followed a soft lock: what the AI ran while the
// app was locked never reached the Auto-allowed feed (spec 2026-10-02).
export function RanLockedBanner({ text, onDismiss }: { text?: string; onDismiss: () => void }) {
  if (!text) return null
  return (
    <div className="banner auto" role="status">
      <span>{text}</span>
      <button type="button" className="btn" onClick={onDismiss}>Dismiss</button>
    </div>
  )
}

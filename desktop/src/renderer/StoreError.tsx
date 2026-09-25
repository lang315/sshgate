// Shown while the hub's last reload of the vault file was refused (status
// storeError); it has no close button and goes away once a reload succeeds.
export function StoreErrorBanner({ message }: { message?: string }) {
  if (!message) return null
  return <div className="store-error" role="alert">{message}</div>
}

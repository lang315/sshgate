package model

// TouchStatus is what the Touch ID helper says about this Mac and this
// vault. Supported is false with no helper (any system but macOS, or a
// missing binary): then nothing is shown and nothing is spawned.
type TouchStatus struct{ Supported, Available, Enabled bool }

// TouchChange is Settings' Unlock switch against the status it opened with:
// "same", "on" (Touch ID is being turned on, so the master password is
// asked again) or "off".
func TouchChange(enabled, on bool) string {
	switch {
	case on == enabled:
		return "same"
	case on:
		return "on"
	}
	return "off"
}

// AutoTouch is whether the unlock screen asks Touch ID by itself, once per
// locked episode: never when it was asked already (a cancel counts), never
// when locked is false or Touch ID is off or not usable, and only on the
// first focus of the window (trigger "focus") or at start (trigger "start")
// when the window has the focus and the ready screen was never shown. It is
// never asked at the moment of locking: the lock gives no trigger.
func AutoTouch(trigger string, locked bool, t *TouchStatus, asked, everReady, focused bool) bool {
	if !locked || t == nil || !t.Enabled || !t.Available || asked {
		return false
	}
	return trigger == "focus" || (!everReady && focused)
}

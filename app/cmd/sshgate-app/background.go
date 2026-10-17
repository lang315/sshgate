package main

import (
	"sync/atomic"

	"github.com/egoist/mygo"
)

// hidesOnClose is Electron's window.ts rule: on macOS closing the window
// hides it and the Dock icon, and the app, the hub and the unlocked vault run
// on behind the menu bar icon until Quit; elsewhere, and while quitting, a
// close closes.
func hidesOnClose(goos string, quitting bool) bool { return goos == "darwin" && !quitting }

// closeHook is the window's OnClose listener. quitting must be set by
// OnBeforeQuit: Quit closes every window, and a close that is always
// prevented would silently cancel Cmd+Q, the tray's Quit and SIGTERM.
func closeHook(goos string, quitting *atomic.Bool, hide func()) func(*mygo.CloseEvent) {
	return func(e *mygo.CloseEvent) {
		if hidesOnClose(goos, quitting.Load()) {
			e.PreventDefault()
			hide()
		}
	}
}

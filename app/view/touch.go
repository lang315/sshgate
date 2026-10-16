package view

import (
	"context"

	"github.com/lang315/sshgate/app/model"
	"github.com/lang315/sshgate/app/touchid"
)

// Toucher is Touch ID unlock: touchid.Manager, or a fake in tests. Its
// calls run in goroutines and come back through Update.
type Toucher interface {
	Status(ctx context.Context) model.TouchStatus
	Enable(ctx context.Context, password string) error
	Disable() error
	Unlock(ctx context.Context) (touchid.Result, error)
	Cancel(reason error)
}

// WindowFocus follows the window's focus (main feeds it from mygo's OnFocus
// and OnBlur): the first focus of a locked episode asks Touch ID by itself.
func (a *App) WindowFocus(focused bool) {
	a.init()
	a.focused = focused
	if focused {
		a.autoTouch("focus")
	}
}

// loadTouch reads Touch ID's state when the unlock screen shows. It spawns
// the helper but calls the hub only for status, so it is not UI activity.
func (a *App) loadTouch() {
	if a.Touch == nil {
		return
	}
	gen := a.hubGen
	go func() {
		st := a.Touch.Status(context.Background())
		a.Update(func() {
			if gen != a.hubGen {
				return
			}
			a.touch = &st
			a.autoTouch("start")
		})
	}()
}

// autoTouch asks Touch ID by itself when model.AutoTouch says so.
func (a *App) autoTouch(trigger string) {
	if a.Touch != nil && model.AutoTouch(trigger, a.screen == model.ScreenLocked, a.touch, a.touchAsked, a.everReady, a.focused) {
		a.touchUnlock()
	}
}

// touchUnlock is one Touch ID unlock, asked for by the button or by autoTouch.
// A cancel shows nothing; an error shows on the unlock screen, and the status
// is read again because a reset deleted touchid.json.
func (a *App) touchUnlock() {
	if a.Touch == nil || a.touchBusy {
		return
	}
	a.touchBusy, a.touchAsked = true, true
	gen := a.hubGen
	go func() {
		res, err := a.Touch.Unlock(context.Background())
		a.Update(func() {
			a.touchBusy = false
			if err != nil {
				a.unlockErr = err.Error() // also after a hub restart: Interrupted says why
				a.loadTouch()
				return
			}
			if res.Cancelled {
				return
			}
			if gen == a.hubGen {
				a.unlockErr, a.lockReason = "", ""
				a.ranLocked = model.KeepRan(a.ranLocked, res.Reply.RanWhileLocked)
			}
			a.refresh()
		})
	}()
}

// openSettings opens Settings on the limit in force and, with Touch ID,
// on its state as the helper reports it now.
func (a *App) openSettings() {
	limit := a.status.SoftLockLimit
	open := func() {
		st := &settingsState{limit: limit, chosen: limit}
		if a.touch != nil {
			st.touch, st.touchOn = *a.touch, a.touch.Enabled
		}
		a.settings = st
	}
	if a.Touch == nil {
		open()
		return
	}
	gen := a.hubGen
	go func() {
		ts := a.Touch.Status(context.Background())
		a.Update(func() {
			if !a.ready(gen) || a.settings != nil {
				return
			}
			a.touch = &ts
			open()
		})
	}()
}

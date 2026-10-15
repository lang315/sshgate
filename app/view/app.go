package view

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/model"
	"github.com/lang315/sshgate/app/termconn"
)

// Door is the part of the UI door client the views call.
type Door interface {
	Call(ctx context.Context, method string, params, result any) error
}

// App is the window's state and the port of the renderer's App.tsx. Build
// and every method run on the main thread; hub calls run in goroutines and
// come back through Update. Only status is called without a user action.
type App struct {
	Door   Door
	Update func(func())
	Now    func() time.Time
	// Dial opens hub terminal id on server, off the main thread; trust is
	// nil except on the retry after a host-key prompt.
	Dial  func(ctx context.Context, id, server string, trust *hubapi.Trust) (*termconn.Conn, error)
	Shell Shell
	// ThemeFile keeps the theme choice across launches; "" keeps it in memory.
	ThemeFile string

	hubState                                model.HubState
	hubSince                                time.Time // when restarting began, for the countdown
	hubGen                                  int       // bumped when the hub stops: replies from the old one are dropped
	refreshGen, refreshApplied              int       // status calls issued, and the newest whose reply was applied
	status                                  *hubapi.Status
	screen                                  model.Screen
	servers                                 []hubapi.Server
	lockHosts                               []string
	dropped                                 map[string]bool
	lockReason, unlockErr, createErr, alert string

	items        []model.PendingItem
	seen         map[string]bool
	decidedSince map[string]bool
	pendingSince map[string]bool
	seedGen      int
	seedErr      string
	aiOpen       bool
	itemBusy     map[string]bool
	itemErr      map[string]string
	denyAllErr   string
	feed         []hubapi.AutoRan
	counter      model.PendingCounter
	col          colState
	ranLocked    []hubapi.RanLocked // what auto-allow ran while locked, from the unlock reply
	// The paused banner's Resume delay on the Hosts tab and in the AI
	// column; nil while that banner is not drawn.
	pausedHome, pausedCol *model.ListChanges

	busy                   bool   // unlock, create vault or stop-and-lock in flight
	password, newPw, again string // field values
	query                  string // host search
	tabs                   model.TabSet
	terms                  map[string]*termTab // by tab id
	prompts                model.HostKeyPrompts
	mismatch               *hubapi.HostKey
	hk                     hkState
	sends                  []pendingSend
	sendSeq                int // numbers Send to tab attempts
	editor                 *editorState
	imp                    *importState
	del                    *deleteState
	sheetSeq               int       // numbers the sheets opened; keys the open one
	searchH                ui.Handle // the host search: the focus after a sheet closes on Hosts
	theme                  string    // "dark", "light" or "auto"
}

// callTimeout matches Electron's hubProcess.ts: a hung but running hub
// cannot hold an action (and busy) forever. A var so tests can shorten it.
var callTimeout = 60 * time.Second

func (a *App) hub() *hubapi.Client { return hubapi.New(timeoutDoor{a.Door}) }

type timeoutDoor struct{ Door }

func (d timeoutDoor) Call(ctx context.Context, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	err := d.Door.Call(ctx, method, params, result)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("hub did not answer in %d s", int(callTimeout.Seconds()))
	}
	return err
}

func (a *App) Screen() model.Screen       { return a.screen }
func (a *App) Items() []model.PendingItem { return a.items }
func (a *App) LockHosts() []string        { return a.lockHosts }
func (a *App) LockReason() string         { return a.lockReason }
func (a *App) UnlockErr() string          { return a.unlockErr }
func (a *App) Servers() []hubapi.Server   { return a.servers }
func (a *App) Feed() []hubapi.AutoRan     { return a.feed }
func (a *App) AIOpen() bool               { return a.aiOpen }

func (a *App) shell() Shell {
	if a.Shell == nil {
		return noShell{}
	}
	return a.Shell
}

type noShell struct{}

func (noShell) SetPending(int)                {}
func (noShell) Notify(string, string, string) {}
func (noShell) SetTheme(string)               {}

func (a *App) init() {
	if a.dropped == nil {
		a.dropped, a.seen = map[string]bool{}, map[string]bool{}
		a.decidedSince, a.pendingSince = map[string]bool{}, map[string]bool{}
		a.itemBusy, a.itemErr = map[string]bool{}, map[string]string{}
		a.hubState = model.HubState{Kind: "starting"}
		a.screen = model.ScreenHub
		a.terms = map[string]*termTab{}
		a.theme = LoadTheme(a.ThemeFile) // main applied it before the window opened
	}
}

// HubState follows the supervisor; a hub that is not running takes every
// client-side list with it (a restarted hub starts locked with no grants).
func (a *App) HubState(st hubproc.State) {
	a.init()
	a.hubState = model.HubStateFrom(st.Kind, st.Attempt, st.In, st.Message)
	if st.Kind == "restarting" {
		a.hubSince = a.Now()
	}
	if st.Kind != "running" {
		a.hubGen++
		a.endTabs()
		a.prompts, a.mismatch, a.sends = model.HostKeyPrompts{}, nil, nil // a dropped send's timer then finds nothing
		a.itemBusy = map[string]bool{}
		a.status, a.unlockErr, a.lockReason, a.seedErr = nil, "", "", ""
		a.lockHosts, a.items, a.feed = []string{}, nil, nil
		a.decidedSince, a.pendingSince, a.seen = map[string]bool{}, map[string]bool{}, map[string]bool{}
		a.prune()
		a.counter.Reset()
		a.shell().SetPending(0)
		a.setScreen()
		return
	}
	a.refresh()
}

// refresh is the one call allowed without a user action.
func (a *App) refresh() {
	a.dropped = map[string]bool{}
	a.refreshGen++
	gen, rgen := a.hubGen, a.refreshGen
	go func() {
		s, err := a.hub().Status(context.Background())
		a.Update(func() {
			if gen != a.hubGen || rgen < a.refreshApplied {
				return // the hub serves calls concurrently: an older reply may land last
			}
			a.refreshApplied = rgen
			if err != nil {
				a.status, a.lockHosts = nil, []string{}
			} else {
				a.status = &s
				a.lockHosts = model.LockHostsFrom(s.AutoHosts, a.dropped)
			}
			a.setScreen()
		})
	}()
}

func (a *App) setScreen() {
	next := model.ScreenFor(a.hubState, a.status)
	prev := a.screen
	a.screen = next
	if next == prev {
		return
	}
	switch next {
	case model.ScreenReady:
		a.reloadServers()
		a.seed()
		a.focusActive() // after unlock: nothing restores focus by itself
	case model.ScreenCreateVault:
		a.reloadServers()
	default:
		a.servers, a.ranLocked = nil, nil
		a.dropDialogs()
	}
}

func (a *App) reloadServers() {
	gen := a.hubGen
	go func() {
		s, err := a.hub().Servers(context.Background())
		a.Update(func() {
			if gen != a.hubGen {
				return
			}
			if err != nil {
				s = nil
			}
			a.servers = s
		})
	}()
}

func (a *App) seed() {
	a.seedGen++
	gen, hubGen := a.seedGen, a.hubGen
	a.decidedSince, a.pendingSince = map[string]bool{}, map[string]bool{}
	go func() {
		p, err := a.hub().Pending(context.Background())
		a.Update(func() {
			if gen != a.seedGen || hubGen != a.hubGen {
				return
			}
			if err != nil {
				a.seedErr = err.Error()
				return
			}
			a.items = model.MergeSeed(a.items, p, a.decidedSince, a.pendingSince, a.Now())
			a.prune()
			a.seedErr = ""
			for _, r := range p {
				a.seen[r.ID] = true
			}
			if len(p) > 0 {
				a.aiOpen = true
			}
		})
	}()
}

// Note handles a hub notification; nothing here calls the hub but status.
func (a *App) Note(method string, params json.RawMessage) {
	a.init()
	ev, ok := hubapi.Decode(method, params)
	if !ok {
		return
	}
	switch e := ev.(type) {
	case hubapi.LockedEvent:
		a.lockReason = model.LockKind(e.Reason, a.lockReason)
		a.failSends("vault locked") // nothing is pasted behind the lock screen
		a.servers = model.DropOnLock(a.servers)
		a.refresh()
	case hubapi.AutoOffEvent:
		a.dropped[e.Server] = true
		a.lockHosts = model.DropLockHost(a.lockHosts, e.Server)
		if e.Reason != "locked" {
			a.servers = model.ApplyOff(a.servers, e.Server)
		}
	case hubapi.AutoRanEvent:
		a.feed = model.PushFeed(a.feed, e.AutoRan)
	case hubapi.PendingEvent:
		id := e.Request.ID
		a.pendingSince[id] = true
		a.items = model.AddPending(a.items, e.Request, a.Now())
		if !a.seen[id] {
			a.seen[id] = true
			a.aiOpen = true
		}
		a.counter.Add(id)
		a.shell().SetPending(a.counter.Count())
		title, body := model.NotificationText(e.Request)
		a.shell().Notify(id, title, body)
		a.refresh() // a refused vault reload shows before the decision
	case hubapi.DecidedEvent:
		id := e.Request.ID
		a.decidedSince[id] = true
		a.items = model.RemoveDecided(a.items, id)
		a.prune()
		a.counter.Remove(id)
		a.shell().SetPending(a.counter.Count())
	}
}

func (a *App) unlock(pw string) {
	if a.busy {
		return
	}
	a.busy, a.password = true, ""
	gen := a.hubGen
	go func() {
		r, err := a.hub().Unlock(context.Background(), pw)
		a.Update(func() {
			a.busy = false
			if err != nil {
				a.unlockErr = err.Error()
			} else {
				a.unlockErr, a.lockReason = "", ""
			}
			if err == nil && gen == a.hubGen {
				a.ranLocked = model.KeepRan(a.ranLocked, r.RanWhileLocked)
			}
			a.refresh()
		})
	}()
}

// lock relies on the locked notification to refresh: a second refresh
// could re-add a host an autoAllow.off just dropped.
func (a *App) lock() {
	go func() {
		err := a.hub().Lock(context.Background(), false)
		a.Update(func() {
			if err == nil {
				a.unlockErr = ""
			}
		})
	}()
}

func (a *App) stopAndLock() {
	go func() {
		err := a.hub().Lock(context.Background(), true)
		a.Update(func() {
			if err != nil {
				a.unlockErr = "Could not stop auto-allow: " + err.Error()
			} else {
				a.unlockErr = ""
			}
			a.refresh()
		})
	}()
}

func (a *App) createVault(pw string) {
	if a.busy {
		return
	}
	a.busy, a.createErr = true, ""
	go func() {
		err := a.hub().CreateVault(context.Background(), pw)
		a.Update(func() {
			a.busy = false
			if err != nil {
				a.createErr = err.Error()
			} else {
				a.newPw, a.again = "", ""
			}
			a.refresh()
		})
	}()
}

func (a *App) decide(id, outcome, reason string) {
	if a.itemBusy[id] {
		return
	}
	a.itemBusy[id], a.itemErr[id] = true, ""
	go func() {
		err := a.hub().Decide(context.Background(), id, outcome, reason)
		a.Update(func() {
			delete(a.itemBusy, id)
			if err != nil {
				a.itemErr[id] = err.Error()
			}
			a.prune()
		})
	}()
}

// prune drops per-request state of requests no longer shown. seen is
// kept: a late duplicate pending for a decided id must not reopen the column.
func (a *App) prune() {
	shown := map[string]bool{}
	for _, it := range a.items {
		shown[it.Request.ID] = true
	}
	for id := range a.itemErr {
		if !shown[id] {
			delete(a.itemErr, id)
		}
	}
	for id := range a.col.reasons {
		if !shown[id] {
			delete(a.col.reasons, id)
		}
	}
	for id := range a.col.reasonH {
		if !shown[id] {
			delete(a.col.reasonH, id)
		}
	}
	if len(a.items) == 0 {
		a.denyAllErr = ""
	}
}

func (a *App) denyAll() {
	go func() {
		err := a.hub().DenyAll(context.Background(), "denied all by user")
		a.Update(func() {
			a.denyAllErr = ""
			if err != nil {
				a.denyAllErr = err.Error()
			}
		})
	}()
}

func (a *App) stopAll() { a.setAuto(model.AutoHostNames(a.servers, a.Now()), "off", "stop") }

// setAuto sets mode on each of names (Electron's setAllAuto): every host is
// tried after a failure, the host list is read again either way, and a
// failure shows in the alert row as "Could not <verb> auto-allow on …".
// The reply is dropped if its hub stopped or the ready screen went.
func (a *App) setAuto(names []string, mode, verb string) {
	gen := a.hubGen
	go func() {
		var failed []string
		var last string
		for _, n := range names {
			if err := a.hub().SetAutoAllow(context.Background(), n, mode); err != nil {
				failed, last = append(failed, n), err.Error()
			}
		}
		a.Update(func() {
			if !a.ready(gen) {
				return
			}
			a.alert = ""
			if len(failed) > 0 {
				a.alert = fmt.Sprintf("Could not %s auto-allow on %s: %s", verb, strings.Join(failed, ", "), last)
			}
			a.reloadServers()
			a.refresh()
		})
	}()
}

// Build routes to the screen ScreenFor picks; Tasks 6-8 draw them.
func (a *App) Build(c *ui.Context) {
	a.init()
	if a.screen != model.ScreenReady {
		a.col.mounted = false // re-entering ready is a fresh mount: the Allow delay restarts
		a.hk.mounted = false  // and so does the Trust delay of a prompt the lock screen hid
		a.pausedHome, a.pausedCol = nil, nil
	}
	switch a.screen {
	case model.ScreenHub:
		a.buildHub(c)
	case model.ScreenCreateVault:
		a.buildCreateVault(c)
	case model.ScreenLocked:
		a.buildUnlock(c)
	default:
		a.buildReady(c)
	}
}

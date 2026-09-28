package hub

import (
	"context"
	"fmt"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
)

// Auto-allow (spec 2026-09-28-auto-allow-design.md): per-host grants that let
// plain AI exec skip the approval broker. Grants live in h.grants under h.mu;
// forever is also the vault flag config.Server.AutoAllow. Only SetAutoAllow
// arms a grant: a reload or an unlock never does.

// maxAutoInflight caps auto runs per host; more go to approval. Skipping the
// broker skips its cap of 5 pending, and one shared ssh.Client would run out
// of sshd MaxSessions and lock the human out of a terminal.
const maxAutoInflight = 2

var autoModes = map[string]time.Duration{
	"15m": 15 * time.Minute, "30m": 30 * time.Minute, "60m": time.Hour, "2h": 2 * time.Hour, "4h": 4 * time.Hour,
}

type grant struct {
	until    time.Time // zero: forever
	snap     grantSnap // the server as it was when the human enabled it
	inflight map[uint64]context.CancelFunc
}

// grantSnap is what an auto run must still match; any difference ends the grant.
type grantSnap struct{ target, hostKey, hostKeyAlgo, auth string }

func snapOf(dc sshx.DialConfig) grantSnap {
	return grantSnap{target: target(dc), hostKey: dc.HostKey, hostKeyAlgo: dc.HostKeyAlgo, auth: dc.Auth}
}

// autoRefusal says why s can never be on auto-allow, or "". A root login, or a
// stored su or sudo password, is root access: a command planted during a
// grant could capture a sudo password the next time the human approves a
// sudo-exec.
func autoRefusal(s config.Server) string {
	switch {
	case !s.AIVisible:
		return "not visible to AI"
	case s.HostKey == "":
		return "no pinned host key"
	case s.User == "root":
		return "root login"
	case s.EncSuPassword != "":
		return "has an su password"
	case s.EncSudoPassword != "":
		return "has a sudo password"
	}
	return ""
}

// SetAutoAllow turns name's auto-allow on (a timed mode, or forever) or off.
// forever on a server whose vault flag is already set only arms it (Resume).
func (h *Hub) SetAutoAllow(name, mode string) error {
	if mode == "off" {
		return h.autoAllowOff(name, "turned off")
	}
	d, timed := autoModes[mode]
	h.mu.Lock()
	if err := h.checkLocked(name); err != nil {
		h.mu.Unlock()
		return err
	}
	s, _ := h.deps.File.FindServer(name)
	if why := autoRefusal(s); why != "" {
		h.mu.Unlock()
		return fmt.Errorf("auto-allow refused: %s", why)
	}
	dc, err := h.resolveLocked(name)
	h.mu.Unlock()
	if err != nil {
		return ErrConnFailed
	}
	resume := !timed && s.AutoAllow
	if timed == s.AutoAllow { // timed over forever clears the flag; a new forever sets it
		if err := h.writeAutoAllow(name, !timed); err != nil {
			return err
		}
	}
	g := &grant{snap: snapOf(dc), inflight: map[uint64]context.CancelFunc{}}
	if timed {
		g.until = time.Now().Add(d)
	}
	h.mu.Lock()
	if old := h.grants[name]; old != nil {
		g.inflight = old.inflight // runs already going stay counted and cancellable
	}
	h.grants[name] = g
	h.mu.Unlock()
	r := broker.ConfigRecord{Action: "autoAllowOn", Server: name}
	switch {
	case resume:
		r.Action = "autoAllowResume"
	case timed:
		r.Until = g.until.UTC().Format(time.RFC3339)
	default:
		r.Forever = true
	}
	h.auditConfig(r)
	return nil
}

// writeAutoAllow sets name's vault flag and reloads.
func (h *Hub) writeAutoAllow(name string, on bool) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				f.Servers[i].AutoAllow = on
				return nil
			}
		}
		return serverNotFound(name)
	})
	if err != nil {
		return err
	}
	return h.Reload()
}

// autoAllowOff ends name's grant and clears its flag. The grant ends in
// memory even if the write fails; the error is returned.
func (h *Hub) autoAllowOff(name, reason string) error {
	h.mu.Lock()
	had := h.endGrantLocked(name)
	flag := false
	if h.deps.File != nil {
		s, ok := h.deps.File.FindServer(name)
		flag = ok && s.AutoAllow
	}
	h.mu.Unlock()
	var err error
	if flag {
		err = h.writeAutoAllow(name, false)
	}
	if had || flag {
		h.grantEnded(name, reason)
	}
	return err
}

// endGrantLocked drops name's grant and cancels its runs; h.mu is held. The
// caller calls grantEnded after unlocking when it returns true.
func (h *Hub) endGrantLocked(name string) bool {
	g := h.grants[name]
	if g == nil {
		return false
	}
	for _, cancel := range g.inflight {
		cancel()
	}
	delete(h.grants, name)
	return true
}

// endAllGrantsLocked drops every grant (a lock); h.mu is held.
func (h *Hub) endAllGrantsLocked() []string {
	var names []string
	for name := range h.grants {
		h.endGrantLocked(name)
		names = append(names, name)
	}
	return names
}

// grantEnded audits, then tells the app; h.mu is not held.
func (h *Hub) grantEnded(name, reason string) {
	h.auditConfig(broker.ConfigRecord{Action: "autoAllowOff", Server: name, Reason: reason})
	h.notifyAuto("autoAllow.off", map[string]string{"server": name, "reason": reason})
}

// dropGrant ends name's grant after a vault write that already cleared its
// flag (save, delete, forget); hadFlag says the flag was set before it.
func (h *Hub) dropGrant(name, reason string, hadFlag bool) {
	h.mu.Lock()
	had := h.endGrantLocked(name)
	h.mu.Unlock()
	if had || hadFlag {
		h.grantEnded(name, reason)
	}
}

func (h *Hub) notifyAuto(method string, params any) {
	h.mu.Lock()
	sink := h.autoSink
	h.mu.Unlock()
	if sink != nil {
		sink(method, params)
	}
}

// setAutoSink installs f for auto-allow notifications; release works like
// setEventSink's.
func (h *Hub) setAutoSink(f func(method string, params any)) (release func()) {
	h.mu.Lock()
	h.autoSink = f
	h.autoSinkGen++
	gen := h.autoSinkGen
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		if h.autoSinkGen == gen {
			h.autoSink = nil
		}
		h.mu.Unlock()
	}
}

// uiAutoAllow is a server's auto-allow state for the app.
type uiAutoAllow struct {
	Until   string `json:"until,omitempty"`
	Forever bool   `json:"forever,omitempty"`
	Paused  bool   `json:"paused,omitempty"`
}

// autoStateLocked is name's state for serversForUI; h.mu is held.
func (h *Hub) autoStateLocked(s config.Server, now time.Time) *uiAutoAllow {
	g := h.grants[s.Name]
	switch {
	case g != nil && !g.until.IsZero() && now.Before(g.until):
		return &uiAutoAllow{Until: g.until.UTC().Format(time.RFC3339)}
	case s.AutoAllow:
		return &uiAutoAllow{Forever: true, Paused: g == nil}
	}
	return nil
}

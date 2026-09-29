package hub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

// errAutoResolve marks a resolve failure inside autoEligibleLocked, distinct
// from the checkLocked/refusal errors above it: AutoAllowCheck audits this
// one as a check failure (never the detail) and returns errAutoCheck;
// SetAutoAllow just maps it to ErrConnFailed.
var errAutoResolve = errors.New("resolve failed")

// autoEligibleLocked runs the checks SetAutoAllow and AutoAllowCheck share:
// checkLocked (vault exists, visible, unlocked, pinned), then autoRefusal,
// then resolve. h.mu must be held.
func (h *Hub) autoEligibleLocked(name string) (config.Server, sshx.DialConfig, error) {
	if err := h.checkLocked(name); err != nil {
		return config.Server{}, sshx.DialConfig{}, err
	}
	s, _ := h.deps.File.FindServer(name)
	if why := autoRefusal(s); why != "" {
		return config.Server{}, sshx.DialConfig{}, fmt.Errorf("auto-allow refused: %s", why)
	}
	dc, err := h.resolveLocked(name)
	if err != nil {
		return config.Server{}, sshx.DialConfig{}, errAutoResolve
	}
	return s, dc, nil
}

// errAutoAllowRace is returned when a concurrent vault write (a save,
// delete, forget) reaches the store between SetAutoAllow's own flag write
// and the point it would arm a grant; nothing is armed. SaveServer et al.
// take h.mu only for writeKey, Reload and dropGrant — their config.Update
// itself runs without it — so their write can land inside SetAutoAllow's
// single h.mu section despite the lock: the revision check inside the
// Update below, and the re-check after the reload that follows it, are what
// catch that, not the lock.
var errAutoAllowRace = errors.New("server changed; try again")

// SetAutoAllow turns name's auto-allow on (a timed mode, or forever) or off.
// forever on a server whose vault flag is already set and paused (no live
// grant) only arms it (Resume); forever on an already-armed forever grant is
// a no-op. Everything but off runs in one h.mu section, the same pattern
// CreateVault uses (lock order h.mu → config.Update is safe: no path takes
// h.mu from inside an Update): a flag write is followed by a reload and a
// fresh eligibility check before a grant is armed, so a Lock, save, delete,
// or forget landing mid-write is caught against the reloaded state instead
// of racing this call.
func (h *Hub) SetAutoAllow(name, mode string) error {
	if mode == "off" {
		return h.autoAllowOff(name, "turned off")
	}
	d, timed := autoModes[mode]

	h.mu.Lock()
	defer h.mu.Unlock()

	s, dc, err := h.autoEligibleLocked(name)
	if err != nil {
		if errors.Is(err, errAutoResolve) {
			return ErrConnFailed
		}
		return err
	}
	resume := !timed && s.AutoAllow && h.grants[name] == nil
	if !timed && s.AutoAllow && h.grants[name] != nil {
		return nil // already armed forever: nothing changed, nothing to audit
	}

	if timed == s.AutoAllow { // timed over forever clears the flag; a new forever sets it
		key := bytes.Clone(h.deps.MasterKey) // non-nil: autoEligibleLocked's checkLocked just passed
		defer clear(key)
		rev := h.deps.File.Revision
		if err := config.Update(h.o.StorePath, key, func(f *config.File) error {
			// A concurrent save/delete/forget's own config.Update runs
			// without h.mu, so it can land here despite us holding it: catch
			// it by revision instead of blindly overwriting its write.
			if f.Revision != rev {
				return errAutoAllowRace
			}
			for i := range f.Servers {
				if f.Servers[i].Name == name {
					f.Servers[i].AutoAllow = !timed
					return nil
				}
			}
			return serverNotFound(name)
		}); err != nil {
			return err
		}
		// The write stands from here on; every remaining failure must audit
		// the flag change it leaves behind instead of arming a grant.
		auditFlagChange := func() {
			if timed {
				h.auditConfig(broker.ConfigRecord{Action: "autoAllowOff", Server: name, Reason: "turned off"})
			} else {
				h.auditConfig(broker.ConfigRecord{Action: "autoAllowOn", Server: name, Forever: true})
			}
		}
		if err := h.reloadLocked(); err != nil {
			auditFlagChange()
			return err
		}
		// The file may have changed on disk beyond our own write (the same
		// concurrent write landing between it and this reload instead of
		// inside it): re-check eligibility against the reloaded state, and
		// that a forever request still sees its own flag set, before arming
		// anything.
		s, dc, err = h.autoEligibleLocked(name)
		switch {
		case err != nil:
			auditFlagChange()
			if errors.Is(err, errAutoResolve) {
				return ErrConnFailed
			}
			return err
		case !timed && !s.AutoAllow:
			auditFlagChange()
			return errAutoAllowRace
		}
	}

	g := &grant{snap: snapOf(dc), inflight: map[uint64]context.CancelFunc{}}
	if timed {
		g.until = time.Now().Add(d)
	}
	if old := h.grants[name]; old != nil {
		g.inflight = old.inflight // runs already going stay counted and cancellable
	}
	h.grants[name] = g

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

// writeAutoAllow sets name's vault flag and reloads; used only by
// autoAllowOff, which ends the grant under h.mu and writes the flag outside
// it.
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
	if had || (flag && err == nil) {
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

type autoRun struct {
	dc   sshx.DialConfig
	ctx  context.Context // cancelled when the grant ends
	done func()
}

// autoStart decides, in one h.mu section, whether name's exec runs under its
// grant, and registers the run. It resolves the server itself and compares
// it with the grant's snapshot, so the run uses exactly what it checked; any
// mismatch ends the grant and the request goes to approval. nil: approval.
func (h *Hub) autoStart(ctx context.Context, name string) *autoRun {
	h.mu.Lock()
	g := h.grants[name]
	if g == nil {
		h.mu.Unlock()
		return nil
	}
	// checkLocked failing (hidden, deleted, no pin, no vault) must fail
	// closed, not just skip this run and leave the grant armed: an identical
	// server returning later would otherwise resume auto runs with no human
	// action. ErrLocked is the one exception: a lock in progress ends every
	// grant itself, so this call must not race that with its own end.
	if err := h.checkLocked(name); err != nil {
		endedByLock := errors.Is(err, ErrLocked)
		if !endedByLock {
			h.endGrantLocked(name)
		}
		h.mu.Unlock()
		if !endedByLock {
			h.grantEnded(name, "server changed")
		}
		return nil
	}
	reason := ""
	s, _ := h.deps.File.FindServer(name)
	dc, err := h.resolveLocked(name)
	switch {
	case !g.until.IsZero() && !time.Now().Before(g.until):
		reason = "expired"
	case err != nil:
		reason = "server changed"
		fmt.Fprintf(os.Stderr, "hub: auto-allow resolve %q: %v\n", name, err)
	case autoRefusal(s) != "", snapOf(dc) != g.snap, g.until.IsZero() && !s.AutoAllow:
		reason = "server changed"
	case len(g.inflight) >= maxAutoInflight:
		h.mu.Unlock()
		return nil
	}
	if reason != "" {
		h.endGrantLocked(name)
		h.mu.Unlock()
		h.grantEnded(name, reason)
		return nil
	}
	h.grantSeq++
	id := h.grantSeq
	rctx, cancel := context.WithCancel(ctx)
	g.inflight[id] = cancel
	h.mu.Unlock()
	return &autoRun{dc: dc, ctx: rctx, done: func() {
		h.mu.Lock()
		delete(g.inflight, id)
		h.mu.Unlock()
		cancel()
	}}
}

// autoCmdCap bounds the command text sent to the app's feed.
const autoCmdCap = 1000

// autoExec runs an auto-allowed exec: not counted in h.running (so it never
// holds off the idle lock), audited with approval "auto", then reported to
// the app. dc is Exec's own resolve, from before autoStart's; both sets of
// secrets are masked, same as the approved path masks dc and dc2.
func (h *Hub) autoExec(ar *autoRun, dc sshx.DialConfig, r ExecRequest, cmd string, timeout int, base broker.AuditRecord) (ExecResponse, error) {
	red := redactorFor(dc, ar.dc)
	base.Approval = "auto"
	base.Command, base.Description = red.Redact(cmd), red.Redact(r.Description)
	resp, err := h.run(ar.ctx, r.Server, ar.dc, cmd, false, timeout, base, red)
	shown, cut := cutBytes(base.Command, autoCmdCap)
	ran := map[string]any{"server": r.Server, "command": shown, "description": base.Description,
		"time": time.Now().UTC().Format(time.RFC3339)}
	if cut > 0 {
		ran["truncated"] = cut
	}
	if err != nil {
		ran["error"] = err.Error()
	} else {
		ran["exitCode"] = resp.ExitCode
	}
	h.notifyAuto("autoAllow.ran", ran)
	return resp, err
}

// cutBytes cuts s to at most n bytes on a rune boundary; cut is how many
// bytes were dropped.
func cutBytes(s string, n int) (string, int) {
	if len(s) <= n {
		return s, 0
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], len(s) - n
}

// sweepGrants ends timed grants past their deadline, cancelling their runs.
// The idle loop calls it every tick; Exec also checks the deadline exactly.
func (h *Hub) sweepGrants(now time.Time) {
	h.mu.Lock()
	var ended []string
	for name, g := range h.grants {
		if !g.until.IsZero() && !now.Before(g.until) {
			h.endGrantLocked(name)
			ended = append(ended, name)
		}
	}
	h.mu.Unlock()
	for _, n := range ended {
		h.grantEnded(n, "expired")
	}
}

// timedGrantLocked reports a timed grant still before its deadline; h.mu is
// held. Such a grant holds off the idle lock: its deadline was fixed by the
// human, and nothing the AI does moves it.
func (h *Hub) timedGrantLocked(now time.Time) bool {
	for _, g := range h.grants {
		if !g.until.IsZero() && now.Before(g.until) {
			return true
		}
	}
	return false
}

// uiAutoAllow is a server's auto-allow state for the app.
type uiAutoAllow struct {
	Until   string `json:"until,omitempty"`
	Forever bool   `json:"forever,omitempty"`
	Paused  bool   `json:"paused,omitempty"`
}

// autoProbe is the fixed command autoAllowCheck runs: the account's uid, and
// "nopasswd" if sudo works without a password. Advice for the human only.
const autoProbe = "id -u; sudo -n true 2>/dev/null && echo nopasswd"

// AutoCheck is what autoProbe found.
type AutoCheck struct {
	UID              int  `json:"uid"`
	PasswordlessSudo bool `json:"passwordlessSudo"`
}

var errAutoCheck = errors.New("could not check this host")

// AutoAllowCheck runs autoProbe on name so the app can warn that auto-allow
// there is root access. It checks what SetAutoAllow checks first.
func (h *Hub) AutoAllowCheck(ctx context.Context, name string) (AutoCheck, error) {
	h.mu.Lock()
	_, dc, err := h.autoEligibleLocked(name)
	h.mu.Unlock()
	if err != nil {
		if !errors.Is(err, errAutoResolve) {
			return AutoCheck{}, err
		}
		// Past this point the probe was actually attempted: audit the
		// failure too, reason "failed" only, never the detail (that stays
		// on stderr).
		h.auditConfig(broker.ConfigRecord{Action: "autoAllowCheck", Server: name, Reason: "failed"})
		return AutoCheck{}, errAutoCheck
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := h.executor(name, dc).Exec(cctx, autoProbe)
	lines := strings.Fields(res.Stdout)
	if err != nil || len(lines) == 0 {
		if err != nil {
			fmt.Fprintf(os.Stderr, "hub: autoAllowCheck %q: %v\n", name, err)
		}
		h.auditConfig(broker.ConfigRecord{Action: "autoAllowCheck", Server: name, Reason: "failed"})
		return AutoCheck{}, errAutoCheck
	}
	uid, err := strconv.Atoi(lines[0])
	if err != nil {
		h.auditConfig(broker.ConfigRecord{Action: "autoAllowCheck", Server: name, Reason: "failed"})
		return AutoCheck{}, errAutoCheck
	}
	c := AutoCheck{UID: uid, PasswordlessSudo: slices.Contains(lines[1:], "nopasswd")}
	h.auditConfig(broker.ConfigRecord{Action: "autoAllowCheck", Server: name,
		Reason: fmt.Sprintf("uid %d, passwordless sudo %v", c.UID, c.PasswordlessSudo)})
	return c, nil
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

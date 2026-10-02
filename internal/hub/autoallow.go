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
// sudo-exec. Amendment 2026-09-29: AutoAllowRoot opts a host out of that
// refusal; the human has accepted the risk. Not AI-visible and no pinned
// host key are refused regardless.
func autoRefusal(s config.Server) string {
	switch {
	case !s.AIVisible:
		return "not visible to AI"
	case s.HostKey == "":
		return "no pinned host key"
	case s.AutoAllowRoot:
		return "" // opted in: root logins and stored su/sudo passwords allowed
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

// errAutoAllowRace is returned by SetAutoAllow's post-write eligibility
// recheck when the reload right after its own write shows a state that write
// didn't produce; nothing is armed. SaveServer, DeleteServer, ForgetHostKey
// (hosts.go) and autoAllowOff below now all hold h.mu across their own
// config.Update, reload and grant-end, so none of them can land inside this
// section any more — the lock alone rules that out. What the recheck still
// catches is a change from outside the hub entirely (another process
// editing the store file directly); tests reproduce it via the loadStore
// seam.
var errAutoAllowRace = errors.New("server changed; try again")

// endedNote is a grant-end notification armLocked couldn't send itself
// (notifyAuto takes h.mu, which the caller still holds): the caller sends it
// after unlocking.
type endedNote struct{ name, reason string }

// armLocked turns name's auto-allow on (a timed mode, or forever); h.mu must
// be held and mode must not be "off" (SetAutoAllow handles that itself).
// forever on a server whose vault flag is already set and paused (no live
// grant) only arms it (Resume); forever on an already-armed forever grant is
// a no-op. It is also called by SaveServerWithAutoAllow, right after that
// call's own write and reload, in the same h.mu section — the pattern is the
// one CreateVault uses (lock order h.mu → config.Update is safe: no path
// takes h.mu from inside an Update). SaveServer, DeleteServer and
// ForgetHostKey also hold h.mu across their own write, reload and grant-end
// (hosts.go), and SetAutoAllow's caller reloads before calling this, so none
// of them can land inside this section any more: only something outside the
// hub entirely (another process editing the store file) can. A flag write
// here is followed by another reload and a fresh eligibility check before a
// grant is armed, so such an outside change is caught against the reloaded
// state instead of arming on stale data.
func (h *Hub) armLocked(name, mode string) ([]endedNote, error) {
	d, timed := autoModes[mode]
	if !timed && mode != "forever" {
		return nil, fmt.Errorf("unknown auto-allow mode %q", mode)
	}

	s, dc, err := h.autoEligibleLocked(name)
	if err != nil {
		if errors.Is(err, errAutoResolve) {
			return nil, ErrConnFailed
		}
		return nil, err
	}
	resume := !timed && s.AutoAllow && h.grants[name] == nil
	if !timed && s.AutoAllow && h.grants[name] != nil {
		return nil, nil // already armed forever: nothing changed, nothing to audit
	}

	var notes []endedNote
	if timed == s.AutoAllow { // timed over forever clears the flag; a new forever sets it
		key := bytes.Clone(h.deps.MasterKey) // non-nil: autoEligibleLocked's checkLocked just passed
		defer clear(key)
		if err := config.Update(h.o.StorePath, key, func(f *config.File) error {
			for i := range f.Servers {
				if f.Servers[i].Name == name {
					f.Servers[i].AutoAllow = !timed
					return nil
				}
			}
			return serverNotFound(name)
		}); err != nil {
			return nil, err
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
			// timed just cleared a flag that was forever (the only way
			// into this branch with timed==true): if a forever grant was
			// still armed under that flag, it's no longer backed by
			// anything now that the clear stands but the reload couldn't
			// confirm the result — end it too, instead of leaving it armed
			// with the flag now false. auditFlagChange already wrote this
			// off's audit line, so end the grant directly (not via
			// grantEnded, which would write a second one) and note it for
			// the caller to notify once h.mu is released.
			if timed && h.endGrantLocked(name) {
				notes = append(notes, endedNote{name, "turned off"})
			}
			return notes, err
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
			if timed && h.endGrantLocked(name) {
				notes = append(notes, endedNote{name, "turned off"})
			}
			if errors.Is(err, errAutoResolve) {
				return notes, ErrConnFailed
			}
			return notes, err
		case !timed && !s.AutoAllow:
			auditFlagChange()
			return notes, errAutoAllowRace
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
	return notes, nil
}

// SetAutoAllow turns name's auto-allow on (a timed mode, or forever) or off.
// Everything but off runs in armLocked, in this function's own one-h.mu
// section, started with its own reload so a stale in-memory copy from before
// this call never causes a spurious refusal; off instead delegates to
// autoAllowOff below, which holds its own single h.mu section (write,
// reload, grant-end, audit) — the same pattern, just a separate function.
func (h *Hub) SetAutoAllow(name, mode string) error {
	if mode == "off" {
		return h.autoAllowOff(name, "turned off")
	}
	h.mu.Lock()
	if h.softLocked { // nothing arms, extends or resumes a grant behind a locked UI
		h.mu.Unlock()
		return ErrLocked
	}
	if err := h.reloadLocked(); err != nil {
		h.mu.Unlock()
		return err
	}
	notes, err := h.armLocked(name, mode)
	h.mu.Unlock()
	for _, n := range notes {
		h.notifyGrantEnded(n.name, n.reason)
	}
	return err
}

// autoAllowOff ends name's grant and clears its flag, all in one h.mu
// section — reload, write, reload, grant-end, audit — the same pattern
// hosts.go's SaveServer et al. use: a concurrent SetAutoAllow can't land in
// any gap here and leave a live grant behind an "off" audit record. It
// reloads first, like SetAutoAllow does, so it reads the flag fresh instead
// of a stale in-memory copy; if that reload fails, it still ends any grant
// in memory (fail closed) and still attempts the write if memory (stale or
// not) says the flag is set — config.Update loads the file fresh itself, so
// this is safe either way. The grant ends in memory even if the write
// fails; whether the write itself succeeded (not whether the reload that
// follows it does) is what "ended" and the audit line depend on, so a
// write that lands but whose confirming reload fails is still audited — the
// write stands, same as hosts.go's callers, and its reload error is what
// this call returns. No audit is written when there was nothing to end (no
// grant, and the flag was already false, or the write itself failed).
func (h *Hub) autoAllowOff(name, reason string) error {
	h.mu.Lock()
	// Under soft lock the one stop is lock, which ends every grant and zeroes
	// the key. Ending a single grant here could not clear its vault flag.
	if h.softLocked {
		h.mu.Unlock()
		return ErrLocked
	}
	err := h.reloadLocked() // best-effort; a failure doesn't stop the off below
	flag := false
	if h.deps.File != nil {
		s, ok := h.deps.File.FindServer(name)
		flag = ok && s.AutoAllow
	}
	wrote := false
	if flag {
		key, keyErr := h.writeKeyLocked()
		if keyErr != nil {
			err = keyErr
		} else {
			updateErr := config.Update(h.o.StorePath, key, func(f *config.File) error {
				for i := range f.Servers {
					if f.Servers[i].Name == name {
						f.Servers[i].AutoAllow = false
						return nil
					}
				}
				return serverNotFound(name)
			})
			clear(key)
			if updateErr != nil {
				err = updateErr
			} else {
				wrote = true
				err = h.reloadLocked() // returned to the caller; the write stands regardless
			}
		}
	}
	had := h.endGrantLocked(name)
	ended := had || wrote
	if ended {
		h.auditGrantEnded(name, reason)
	}
	h.mu.Unlock()

	if ended {
		h.notifyGrantEnded(name, reason)
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
	h.hardenLocked()
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

// auditGrantEnded writes name's grant-end audit record; auditConfig never
// takes h.mu, so this half may run with it held (hosts.go's SaveServer et
// al. do, to keep the audit truthfully ordered before anything else can
// re-arm the grant).
func (h *Hub) auditGrantEnded(name, reason string) {
	h.auditConfig(broker.ConfigRecord{Action: "autoAllowOff", Server: name, Reason: reason})
}

// notifyGrantEnded tells the app a grant ended; notifyAuto takes h.mu, so
// this half must run without it held.
func (h *Hub) notifyGrantEnded(name, reason string) {
	h.notifyAuto("autoAllow.off", map[string]string{"server": name, "reason": reason})
}

// grantEnded audits, then tells the app; h.mu is not held.
func (h *Hub) grantEnded(name, reason string) {
	h.auditGrantEnded(name, reason)
	h.notifyGrantEnded(name, reason)
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

// autoSkip is why autoStart returned no run. Exec needs it only while the UI
// is locked, to tell the AI something truer than "locked".
type autoSkip int

const (
	skipNoGrant autoSkip = iota
	skipBusy             // maxAutoInflight runs already going
	skipSudo             // sudo-exec without the host's opt-in
	skipEnded            // this call ended the grant
)

// autoStart decides, in one h.mu section, whether name's exec runs under its
// grant, and registers the run. It resolves the server itself and compares
// it with the grant's snapshot, so the run uses exactly what it checked; any
// mismatch ends the grant and the request goes to approval. nil: approval, or
// a refusal while the UI is locked (see Exec). A
// sudo request needs its own opt-in (AutoAllowSudo); that check runs after
// everything that can end the grant, so a changed or expired server still
// ends it even when the request is sudo.
func (h *Hub) autoStart(ctx context.Context, name string, sudo bool) (*autoRun, autoSkip) {
	h.mu.Lock()
	g := h.grants[name]
	if g == nil {
		h.mu.Unlock()
		return nil, skipNoGrant
	}
	// checkGrantLocked failing (hidden, deleted, no pin, no vault) must fail
	// closed, not just skip this run and leave the grant armed: an identical
	// server returning later would otherwise resume auto runs with no human
	// action. ErrLocked is the one exception: with a grant present the grant
	// rule returns it only when the key is gone or the ceiling has passed, and
	// a hard lock has ended or is ending every grant itself, so this call must
	// not race that with its own end.
	if err := h.checkGrantLocked(name); err != nil {
		endedByLock := errors.Is(err, ErrLocked)
		if !endedByLock {
			h.endGrantLocked(name)
			h.auditGrantEnded(name, "server changed")
		}
		h.mu.Unlock()
		if !endedByLock {
			h.notifyGrantEnded(name, "server changed")
		}
		return nil, skipEnded
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
	}
	if reason != "" {
		h.endGrantLocked(name)
		h.auditGrantEnded(name, reason)
		h.mu.Unlock()
		h.notifyGrantEnded(name, reason)
		return nil, skipEnded
	}
	if sudo && !s.AutoAllowSudo { // sudo-exec needs its own opt-in; the grant stays
		h.mu.Unlock()
		return nil, skipSudo
	}
	if len(g.inflight) >= maxAutoInflight { // after the sudo check: a sudo request can never run, whatever the load
		h.mu.Unlock()
		return nil, skipBusy
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
	}}, skipNoGrant
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
	resp, err := h.run(ar.ctx, r.Server, ar.dc, cmd, r.Sudo, timeout, base, red)
	shown, cut := cutBytes(base.Command, autoCmdCap)
	ran := map[string]any{"server": r.Server, "command": shown, "description": base.Description,
		"time": time.Now().UTC().Format(time.RFC3339)}
	if cut > 0 {
		ran["truncated"] = cut
	}
	if r.Sudo {
		ran["sudo"] = true
	}
	if err != nil {
		ran["error"] = err.Error()
	} else {
		ran["exitCode"] = resp.ExitCode
	}
	// Nothing with content goes to a locked UI: count the run instead, for
	// the unlock reply. ponytail: unlocked is read here and again in the
	// door's sink, so a run finishing exactly at an unlock can be neither
	// counted nor shown; the audit log has every run.
	if !h.unlocked.Load() {
		h.mu.Lock()
		h.ranLocked[r.Server]++
		h.mu.Unlock()
		return resp, err
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
// Under soft lock it also re-reads the vault file and ends any grant whose
// server no longer matches it: nothing else would, since such a server fails
// Exec's first resolve before autoStart looks at its grant, and the grant
// would hold the key. The idle loop calls it every tick; Exec also checks
// the deadline exactly.
func (h *Hub) sweepGrants(now time.Time) {
	type end struct{ name, reason string }
	failed := false
	h.mu.Lock()
	if h.softLocked {
		if err := h.reloadLocked(); err != nil {
			// Fail closed: the vault can no longer be checked, so no grant may
			// keep the key. The detail stays on stderr, never to the AI.
			fmt.Fprintf(os.Stderr, "hub: soft lock: vault reload failed: %v\n", err)
			failed = true
		}
	}
	var ended []end
	for name, g := range h.grants {
		reason := ""
		switch {
		case !g.until.IsZero() && !now.Before(g.until):
			reason = "expired"
		case failed, h.softLocked && h.grantStaleLocked(name, g):
			reason = "server changed"
		}
		if reason != "" {
			h.endGrantLocked(name)
			h.auditGrantEnded(name, reason)
			ended = append(ended, end{name, reason})
		}
	}
	h.mu.Unlock()
	for _, e := range ended {
		h.notifyGrantEnded(e.name, e.reason)
	}
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
	if h.softLocked { // a locked UI sees the vault flag only, as under a hard lock
		g = nil
	}
	switch {
	case g != nil && !g.until.IsZero() && now.Before(g.until):
		return &uiAutoAllow{Until: g.until.UTC().Format(time.RFC3339)}
	case s.AutoAllow:
		return &uiAutoAllow{Forever: true, Paused: g == nil}
	}
	return nil
}

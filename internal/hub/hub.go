// Package hub holds the unlocked vault and SSH connections and gates every
// AI command behind human approval through its broker.
package hub

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/mcpserver"
	"github.com/lang315/sshgate/internal/sshx"
)

// AI-facing error text; must match the spec's "Errors returned to the AI" table.
var (
	ErrLocked    = errors.New("Vault is locked; unlock it in the app")
	ErrNoHostKey = errors.New("connect to this server from the app once first")
	ErrExpired   = errors.New("Approval timed out after 5 minutes; you may retry")
	// ErrCancelledRunning replaces sshx.ErrCancelled toward the AI; the
	// detailed error goes to the audit reason.
	ErrCancelledRunning = errors.New("Cancelled; the remote process may still be running")
	// ErrHostKeyFailed and ErrConnFailed hide SSH and resolve detail (host,
	// port, fingerprints, key-file paths) from the AI; the detail goes to
	// the audit reason and stderr.
	ErrHostKeyFailed = errors.New("host key verification failed; check the server in the app")
	ErrConnFailed    = errors.New("connection to server failed; see the app for details")
	// ErrServerChanged: the server was edited between approval and run, so
	// the approval (for the old user@host:port and pin) no longer applies.
	ErrServerChanged = errors.New("server changed")
	// ErrNoVault: the store has no master password (no file, or a KDF-less
	// one whose flags and pins were never MAC'd), so the AI gets nothing.
	ErrNoVault = errors.New("No vault yet; open the app and create one")
)

// errStoreReload is what status shows while the last reload was refused; the
// detail (paths, parse errors) is never included.
var errStoreReload = errors.New("the vault file failed its integrity check or could not be read; the app keeps the last good copy")

// serverNotFound is used for both hidden and nonexistent servers so the two
// are byte-identical.
func serverNotFound(name string) error { return fmt.Errorf("server %q not found", name) }

type DeniedError struct{ Reason string }

func (e *DeniedError) Error() string {
	if e.Reason == "" {
		return "Denied by user"
	}
	return "Denied by user: " + e.Reason
}

type Executor interface {
	Exec(ctx context.Context, cmd string) (sshx.ExecResult, error)
	ExecSudo(ctx context.Context, cmd string) (sshx.ExecResult, error)
}

type Options struct {
	StorePath      string
	Audit          *broker.Audit
	ApprovalExpiry time.Duration // <= 0 means the broker default, 5 minutes
	OnEvent        func(broker.Event)
	Dialer         func(sshx.DialConfig) Executor // test seam; nil = real Registry
	IdleLock       time.Duration                  // 0 means 15 minutes; < 0 disables auto-lock
	KnownHostsPath string                         // "" means ~/.ssh/known_hosts; only a hint in the Trust prompt
}

type ServerInfo struct {
	Name   string `json:"name"`
	Locked bool   `json:"locked"`
}

type ExecRequest struct {
	Client, Server, Command, Description string
	Sudo                                 bool
	TimeoutSec                           int
}

type ExecResponse struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type Hub struct {
	o       Options
	mu      sync.Mutex // guards deps.File, deps.MasterKey, storeErr, sinks, lastActivity and running
	deps    *mcpserver.Deps
	sink    func(broker.Event)
	sinkGen uint64 // bumped on every setEventSink; lets release() no-op if superseded
	reg     *sshx.Registry
	broker  *broker.Broker
	audit   *broker.Audit

	storeErr     error // the last reload's error; guarded by h.mu
	lockSink     func(reason string)
	lockSinkGen  uint64
	lastActivity time.Time
	running      int // approved AI commands currently executing
	done         chan struct{}
	closeOnce    sync.Once
}

// New loads the store if present; a missing store is not an error.
func New(o Options) (*Hub, error) {
	h := &Hub{o: o, reg: sshx.NewRegistry(), audit: o.Audit, lastActivity: time.Now(), done: make(chan struct{})}
	h.deps = &mcpserver.Deps{Path: o.StorePath}
	f, err := config.Load(o.StorePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot read config store: %w", err)
	}
	h.deps.File = f
	h.broker = broker.New(broker.Options{MaxPending: 5, Expiry: o.ApprovalExpiry, OnEvent: h.emit})
	idle := o.IdleLock
	if idle == 0 {
		idle = defaultIdleLock
	}
	if idle > 0 {
		go h.idleLoop(idle)
	}
	return h, nil
}

// Close stops the idle auto-lock goroutine. It is safe to call more than once.
func (h *Hub) Close() { h.closeOnce.Do(func() { close(h.done) }) }

func (h *Hub) Broker() *broker.Broker   { return h.broker }
func (h *Hub) Registry() *sshx.Registry { return h.reg }
func (h *Hub) Deps() *mcpserver.Deps    { return h.deps }

// setEventSink installs f as the event sink and returns a release func that
// clears it again. Two callers may install a sink in sequence (e.g. a UI
// door session ending while a CLI approver is still up in tests); release
// only clears the sink if it is still the one this call installed, so an
// out-of-order release from an earlier install can't clobber a later one.
func (h *Hub) setEventSink(f func(broker.Event)) (release func()) {
	h.mu.Lock()
	h.sink = f
	h.sinkGen++
	gen := h.sinkGen
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		if h.sinkGen == gen {
			h.sink = nil
		}
		h.mu.Unlock()
	}
}

// setLockSink installs f to be told when the vault goes from unlocked to
// locked; release works like setEventSink's.
func (h *Hub) setLockSink(f func(reason string)) (release func()) {
	h.mu.Lock()
	h.lockSink = f
	h.lockSinkGen++
	gen := h.lockSinkGen
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		if h.lockSinkGen == gen {
			h.lockSink = nil
		}
		h.mu.Unlock()
	}
}

// emit runs on broker goroutines. No hub path holds h.mu while calling into
// the broker, so taking it here cannot deadlock.
func (h *Hub) emit(e broker.Event) {
	h.mu.Lock()
	sink := h.sink
	h.mu.Unlock()
	if sink != nil {
		sink(e)
	}
	if h.o.OnEvent != nil {
		h.o.OnEvent(e)
	}
}

// deriveKey is Unlock's key derivation; a test seam.
var deriveKey = (*config.KDF).DeriveKey

// Unlock derives the master key, checks it against the verifier, and checks
// the file MAC.
func (h *Hub) Unlock(pw string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.deps.File
	if f == nil || f.KDF == nil {
		return nil // no vault; the AI and terminals get nothing until vault.create
	}
	mk, err := deriveKey(f.KDF, pw)
	if err != nil {
		return err
	}
	if !f.KDF.Verify(mk) {
		clear(mk)
		return errors.New("wrong master password")
	}
	if err := f.VerifyMAC(mk); err != nil {
		clear(mk)
		return err
	}
	clear(h.deps.MasterKey)
	h.deps.MasterKey = mk
	h.lastActivity = time.Now()
	return nil
}

func (h *Hub) Lock() { h.lockWithReason("manual") }

// lockWithReason zeroes the key and, if the vault was unlocked, tells the
// lock sink. The sink runs outside h.mu.
func (h *Hub) lockWithReason(reason string) {
	h.mu.Lock()
	sink := h.zeroKeyLocked()
	h.mu.Unlock()
	if sink != nil {
		sink(reason)
	}
}

// zeroKeyLocked drops the master key with h.mu held. It returns the lock
// sink to call (outside h.mu) if the vault was unlocked, else nil.
func (h *Hub) zeroKeyLocked() func(string) {
	if h.deps.MasterKey == nil {
		return nil
	}
	clear(h.deps.MasterKey)
	h.deps.MasterKey = nil
	return h.lockSink
}

func (h *Hub) Locked() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lockedLocked()
}

// lockedLocked reports, with h.mu held, whether an encrypted vault has no
// key. Its MAC is unchecked then, so every server in it counts as locked,
// including ones with no encrypted fields.
func (h *Hub) lockedLocked() bool {
	f := h.deps.File
	return f != nil && f.KDF != nil && h.deps.MasterKey == nil
}

// noVaultLocked reports, with h.mu held, whether the store has no master
// password: no file, or a KDF-less one. Such a file was never MAC'd, so
// nothing in it is trusted for the AI or for terminals.
func (h *Hub) noVaultLocked() bool {
	return h.deps.File == nil || h.deps.File.KDF == nil
}

// Reload re-reads the store when its on-disk Revision differs. The master
// key is kept: the KDF params do not change on an ordinary save. If the MAC
// no longer verifies (e.g. the master password changed elsewhere), the old
// file stays in effect and the error is returned.
func (h *Hub) Reload() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reloadLocked()
}

// loadStore is config.Load; tests swap it to make a reload fail.
var loadStore = config.Load

// storeError returns errStoreReload's text if the last reload failed, else "".
func (h *Hub) storeError() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.storeErr == nil {
		return ""
	}
	return errStoreReload.Error()
}

// reloadLocked is Reload with h.mu held. Its result is remembered for status.
func (h *Hub) reloadLocked() (err error) {
	defer func() { h.storeErr = err }()
	f, err := loadStore(h.o.StorePath)
	if err != nil {
		if os.IsNotExist(err) {
			// A vault that vanished is an error, not "no vault": keep
			// serving the last good copy.
			if h.deps.File != nil && h.deps.File.KDF != nil {
				return errStoreReload
			}
			return nil
		}
		return err
	}
	if h.deps.File != nil && f.Revision == h.deps.File.Revision {
		return nil
	}
	// A vault never loses its KDF; one that did lost its MAC check with it.
	if f.KDF == nil && (h.deps.MasterKey != nil || h.deps.File != nil && h.deps.File.KDF != nil) {
		return errors.New("store lost its master password; it was tampered with")
	}
	if h.deps.MasterKey != nil {
		if err := f.VerifyMAC(h.deps.MasterKey); err != nil {
			return err
		}
	}
	h.deps.File = f
	return nil
}

// ServersForMCP lists AIVisible servers. It works while locked (names are
// plaintext); then every server is reported locked. With no vault it is
// empty.
func (h *Hub) ServersForMCP() []ServerInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []ServerInfo{}
	if h.noVaultLocked() {
		return out
	}
	for _, s := range h.deps.File.Servers {
		if s.AIVisible {
			out = append(out, ServerInfo{Name: s.Name, Locked: h.lockedLocked()})
		}
	}
	return out
}

func clampTimeout(sec int) int {
	switch {
	case sec == 0:
		return 60
	case sec < 1:
		return 1
	case sec > 600:
		return 600
	}
	return sec
}

func (h *Hub) executor(name string, dc sshx.DialConfig) Executor {
	if h.o.Dialer != nil {
		return h.o.Dialer(dc)
	}
	return h.reg.Get(name, dc)
}

func (h *Hub) record(r broker.AuditRecord) {
	if h.audit != nil {
		_ = h.audit.Write(r)
	}
}

// checkLocked validates the server with h.mu held: a vault exists, the
// server exists and is AIVisible, unlocked, host key pinned.
func (h *Hub) checkLocked(name string) error {
	if h.noVaultLocked() {
		return ErrNoVault
	}
	s, ok := h.deps.File.FindServer(name)
	if !ok || !s.AIVisible {
		return serverNotFound(name)
	}
	if h.lockedLocked() {
		return ErrLocked
	}
	if s.HostKey == "" {
		return ErrNoHostKey
	}
	return nil
}

// Resolve turns a stored server into a strict DialConfig (no learner: see
// Deps.Resolve). It runs under h.mu because it reads File and MasterKey; it
// is short (decrypt plus an optional key-file read).
func (h *Hub) Resolve(name string) (sshx.DialConfig, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.resolveLocked(name)
}

// resolveForTerm resolves a server for a new UI terminal. Like the AI path,
// a locked encrypted vault refuses every server, key-only and agent ones
// included (spec §Vault lifecycle: new connections need an unlock). With no
// vault there is nothing trusted to connect to.
func (h *Hub) resolveForTerm(name string) (sshx.DialConfig, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.noVaultLocked() {
		return sshx.DialConfig{}, errNoVault
	}
	if h.lockedLocked() {
		return sshx.DialConfig{}, ErrLocked
	}
	return h.resolveLocked(name)
}

func (h *Hub) resolveLocked(name string) (sshx.DialConfig, error) {
	return h.deps.Resolve(name)
}

// resolveForAI checks the server (exists, AIVisible, unlocked, pinned) and
// resolves it in one locked section. Exec calls it before approval, to build
// the audit redactor, and again after, since the vault may have been locked
// or reloaded during the wait.
func (h *Hub) resolveForAI(name string) (sshx.DialConfig, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkLocked(name); err != nil {
		return sshx.DialConfig{}, err
	}
	dc, err := h.resolveLocked(name)
	if err != nil {
		return sshx.DialConfig{}, &hiddenError{ai: ErrConnFailed, detail: err}
	}
	return dc, nil
}

// hiddenError shows the AI only ai; detail is for the audit and stderr.
type hiddenError struct{ ai, detail error }

func (e *hiddenError) Error() string { return e.ai.Error() }
func (e *hiddenError) Unwrap() error { return e.ai }

// forAI maps an exec error to what the AI may see. A timeout carries no
// host or path and passes through; every other SSH error is replaced.
func forAI(err error) error {
	switch {
	case errors.Is(err, sshx.ErrHostKeyMismatch):
		return ErrHostKeyFailed
	case errors.Is(err, sshx.ErrTimeout):
		return err
	}
	return ErrConnFailed
}

// detail is the full text of err for the audit and stderr.
func detail(err error) string {
	var he *hiddenError
	if errors.As(err, &he) {
		return he.detail.Error()
	}
	return err.Error()
}

func redactorFor(dcs ...sshx.DialConfig) *config.Redactor {
	var secrets []string
	for _, dc := range dcs {
		secrets = append(secrets, dc.Password, dc.SuPassword, dc.SudoPassword, dc.Passphrase)
	}
	return config.NewRedactor(secrets...)
}

// target is the endpoint an approval is for, as the approval card shows it.
func target(dc sshx.DialConfig) string {
	return dc.User + "@" + net.JoinHostPort(dc.Host, strconv.Itoa(dc.Port))
}

func (h *Hub) Exec(ctx context.Context, r ExecRequest) (ExecResponse, error) {
	dc, err := h.resolveForAI(r.Server)
	if err != nil {
		var he *hiddenError
		if errors.As(err, &he) {
			fmt.Fprintf(os.Stderr, "hub: resolve %q: %v\n", r.Server, he.detail)
		}
		if errors.Is(err, ErrNoVault) {
			// No vault means no secrets, so there is nothing to redact. Nothing
			// is validated yet either: cap every AI-supplied field so a call
			// cannot write megabytes to the audit log.
			c := config.CapOutput
			h.record(broker.AuditRecord{Time: time.Now(), Client: c(r.Client, 256), Server: c(r.Server, 256),
				Command: c(r.Command, 4096), Description: c(r.Description, 500), Sudo: r.Sudo, Outcome: "error", Reason: err.Error()})
		}
		return ExecResponse{}, err
	}
	red := redactorFor(dc)
	cmd, err := config.SanitizeCommand(r.Command, -1)
	if err != nil {
		return ExecResponse{}, err
	}
	// R39: the description is shown and audited, never executed.
	if err := config.ValidateDescription(r.Description); err != nil {
		return ExecResponse{}, err
	}
	timeout := clampTimeout(r.TimeoutSec)

	req := broker.Request{Client: r.Client, Server: r.Server, Target: target(dc), Command: cmd, Description: r.Description, Sudo: r.Sudo, TimeoutSec: timeout}
	base := broker.AuditRecord{Time: time.Now(), Client: r.Client, Server: r.Server, Command: red.Redact(cmd), Description: red.Redact(r.Description), Sudo: r.Sudo, TimeoutSec: timeout}

	d, err := h.broker.Submit(ctx, req)
	if err != nil {
		return ExecResponse{}, err // ErrTooManyPending, or ctx.Err() when withdrawn
	}
	switch d.Outcome {
	case broker.Expired:
		base.Outcome = string(broker.Expired)
		h.record(base)
		return ExecResponse{}, ErrExpired
	case broker.Denied:
		base.Outcome, base.Reason = string(broker.Denied), d.Reason
		h.record(base)
		return ExecResponse{}, &DeniedError{Reason: d.Reason}
	case broker.SentToTab:
		base.Outcome = string(broker.SentToTab)
		h.record(base)
		return ExecResponse{Stdout: "User chose to run this in their terminal; no output captured"}, nil
	}

	// Approved: count as running so the idle auto-lock waits for it. AI
	// traffic never touches lastActivity: the idle clock is UI input only.
	h.mu.Lock()
	h.running++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.running--
		h.mu.Unlock()
	}()

	if ctx.Err() != nil {
		base.Outcome = string(broker.ApprovedButCancelled)
		h.record(base)
		return ExecResponse{}, ctx.Err()
	}
	dc2, err := h.resolveForAI(r.Server)
	if err != nil {
		base.Outcome, base.Reason = "error", red.Redact(detail(err))
		h.record(base)
		fmt.Fprintf(os.Stderr, "hub: resolve %q: %s\n", r.Server, base.Reason)
		return ExecResponse{}, err
	}
	if target(dc2) != target(dc) || dc2.HostKey != dc.HostKey {
		base.Outcome = "error"
		base.Reason = fmt.Sprintf("server changed: approved %s %s, now %s %s", target(dc), dc.HostKey, target(dc2), dc2.HostKey)
		h.record(base)
		return ExecResponse{}, ErrServerChanged
	}
	// Secrets may have changed on a reload during the wait; mask both sets.
	red = redactorFor(dc, dc2)
	base.Command, base.Description = red.Redact(cmd), red.Redact(r.Description)
	ex := h.executor(r.Server, dc2)

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	start := time.Now()
	var res sshx.ExecResult
	if r.Sudo {
		res, err = ex.ExecSudo(runCtx, cmd)
	} else {
		res, err = ex.Exec(runCtx, cmd)
	}
	base.DurationMs = time.Since(start).Milliseconds()
	base.StdoutBytes, base.StderrBytes = len(res.Stdout), len(res.Stderr)
	if err != nil {
		base.Reason = red.Redact(err.Error())
		if errors.Is(err, sshx.ErrCancelled) {
			base.Outcome = "cancelled_running"
			h.record(base)
			return ExecResponse{}, ErrCancelledRunning
		}
		base.Outcome = "error"
		h.record(base)
		fmt.Fprintf(os.Stderr, "hub: exec on %q: %s\n", r.Server, base.Reason)
		if aiErr := forAI(err); aiErr != err {
			return ExecResponse{}, aiErr
		}
		return ExecResponse{}, errors.New(base.Reason)
	}
	code := res.ExitCode
	base.Outcome, base.ExitCode = string(broker.Allowed), &code
	h.record(base)
	return ExecResponse{
		ExitCode: res.ExitCode,
		Stdout:   config.CapOutput(red.Redact(res.Stdout), config.DefaultOutputCap),
		Stderr:   config.CapOutput(red.Redact(res.Stderr), config.DefaultOutputCap),
	}, nil
}

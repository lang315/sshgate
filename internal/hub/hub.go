// Package hub holds the unlocked vault and SSH connections and gates every
// AI command behind human approval through its broker.
package hub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/mcpserver"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

// AI-facing error text; must match the spec's "Errors returned to the AI" table.
var (
	ErrLocked    = errors.New("Vault is locked; unlock it in the app")
	ErrNoHostKey = errors.New("connect to this server from the app once first")
	ErrExpired   = errors.New("Approval timed out after 5 minutes; you may retry")
)

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
	Insecure       bool
	Dialer         func(sshx.DialConfig) Executor // test seam; nil = real Registry
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
	o      Options
	mu     sync.Mutex // guards deps.File, deps.MasterKey and sink
	deps   *mcpserver.Deps
	sink   func(broker.Event)
	reg    *sshx.Registry
	broker *broker.Broker
	audit  *broker.Audit
}

// New loads the store if present; a missing store is not an error.
func New(o Options) (*Hub, error) {
	h := &Hub{o: o, reg: sshx.NewRegistry(), audit: o.Audit}
	h.deps = &mcpserver.Deps{Path: o.StorePath, Insecure: o.Insecure}
	f, err := config.Load(o.StorePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot read config store: %w", err)
	}
	h.deps.File = f
	h.broker = broker.New(broker.Options{MaxPending: 5, Expiry: o.ApprovalExpiry, OnEvent: h.emit})
	return h, nil
}

func (h *Hub) Broker() *broker.Broker   { return h.broker }
func (h *Hub) Registry() *sshx.Registry { return h.reg }
func (h *Hub) Deps() *mcpserver.Deps    { return h.deps }

func (h *Hub) setEventSink(f func(broker.Event)) {
	h.mu.Lock()
	h.sink = f
	h.mu.Unlock()
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

// Unlock derives the master key, checks it against the verifier, and checks
// the file MAC.
func (h *Hub) Unlock(pw string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.deps.File
	if f == nil || f.KDF == nil {
		return nil // nothing encrypted; key/agent-only vault
	}
	mk, err := f.KDF.DeriveKey(pw)
	if err != nil {
		return err
	}
	if !f.KDF.Verify(mk) {
		return errors.New("wrong master password")
	}
	if err := f.VerifyMAC(mk); err != nil {
		return err
	}
	h.deps.MasterKey = mk
	return nil
}

func (h *Hub) Lock() {
	h.mu.Lock()
	defer h.mu.Unlock()
	clear(h.deps.MasterKey)
	h.deps.MasterKey = nil
}

func (h *Hub) Locked() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.deps.File
	return f != nil && f.KDF != nil && h.deps.MasterKey == nil
}

// Reload re-reads the store when its on-disk Revision differs. The master
// key is kept: the KDF params do not change on an ordinary save. If the MAC
// no longer verifies (e.g. the master password changed elsewhere), the old
// file stays in effect and the error is returned.
func (h *Hub) Reload() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, err := config.Load(h.o.StorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if h.deps.File != nil && f.Revision == h.deps.File.Revision {
		return nil
	}
	if h.deps.MasterKey != nil {
		if err := f.VerifyMAC(h.deps.MasterKey); err != nil {
			return err
		}
	}
	h.deps.File = f
	return nil
}

// ServersForMCP lists AIVisible servers. It works while locked: names are
// plaintext.
func (h *Hub) ServersForMCP() []ServerInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []ServerInfo{}
	if h.deps.File == nil {
		return out
	}
	for _, s := range h.deps.File.Servers {
		if s.AIVisible {
			out = append(out, ServerInfo{Name: s.Name, Locked: h.deps.IsLocked(s.Name)})
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

// check validates the server under the lock: exists and AIVisible, unlocked,
// host key pinned.
func (h *Hub) check(name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.checkLocked(name)
}

// checkLocked is check with h.mu already held.
func (h *Hub) checkLocked(name string) error {
	if h.deps.File == nil {
		return serverNotFound(name)
	}
	s, ok := h.deps.File.FindServer(name)
	if !ok || !s.AIVisible {
		return serverNotFound(name)
	}
	if h.deps.IsLocked(name) {
		return ErrLocked
	}
	if s.HostKey == "" {
		return ErrNoHostKey
	}
	return nil
}

// resolve re-checks the server (the vault may have been locked or reloaded
// during the approval wait) and resolves it. Resolve runs under h.mu because
// it reads File and MasterKey; it is short (decrypt plus an optional key-file
// read). Its OnLearnHostKey closure reads Deps unlocked, but it never fires
// here because check guarantees a pinned host key.
func (h *Hub) resolve(name string) (sshx.DialConfig, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkLocked(name); err != nil {
		return sshx.DialConfig{}, err
	}
	return h.deps.Resolve(name)
}

func (h *Hub) Exec(ctx context.Context, r ExecRequest) (ExecResponse, error) {
	if err := h.check(r.Server); err != nil {
		return ExecResponse{}, err
	}
	cmd, err := config.SanitizeCommand(r.Command, -1)
	if err != nil {
		return ExecResponse{}, err
	}
	cmd, err = config.AppendDescription(cmd, r.Description)
	if err != nil {
		return ExecResponse{}, err
	}
	timeout := clampTimeout(r.TimeoutSec)

	req := broker.Request{Client: r.Client, Server: r.Server, Command: cmd, Description: r.Description, Sudo: r.Sudo, TimeoutSec: timeout}
	base := broker.AuditRecord{Time: time.Now(), Client: r.Client, Server: r.Server, Command: cmd, Description: r.Description, Sudo: r.Sudo, TimeoutSec: timeout}

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

	if ctx.Err() != nil {
		base.Outcome = string(broker.ApprovedButCancelled)
		h.record(base)
		return ExecResponse{}, ctx.Err()
	}
	dc, err := h.resolve(r.Server)
	if err != nil {
		// No DialConfig, so nothing to redact with; these messages carry no secrets.
		base.Outcome, base.Reason = "error", err.Error()
		h.record(base)
		return ExecResponse{}, err
	}
	ex := h.executor(r.Server, dc)
	red := config.NewRedactor(dc.Password, dc.SuPassword, dc.SudoPassword, dc.Passphrase)
	base.Command = red.Redact(cmd)

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
			return ExecResponse{}, err // fixed text plus ctx.Err(); keeps errors.Is
		}
		base.Outcome = "error"
		h.record(base)
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

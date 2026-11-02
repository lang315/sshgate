package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/files"
	"github.com/lang315/sshgate/internal/sshx"
)

// TransferRequest is an AI upload or download (spec 2026-11-02).
type TransferRequest struct {
	Client, Server, Description string
	Op                          files.Op // files.OpUpload or files.OpDownload
	Root                        string   // the bridge's working directory, absolute
	Local                       []string // upload: sources; download: [destination folder]; relative to Root
	Remote                      []string // upload: [destination folder]; download: sources; absolute
	Overwrite                   bool
	TimeoutSec                  int
}

// TransferResponse is what the bridge lays out for the AI.
type TransferResponse struct {
	Op           files.Op        `json:"op"`
	Files        int             `json:"files"`
	Bytes        int64           `json:"bytes"`
	Skipped      int             `json:"skipped"`
	SkippedNames []string        `json:"skippedNames,omitempty"` // first 5, from the plan's conflicts
	Refused      []files.Refusal `json:"refused,omitempty"`
	RefusedCount int             `json:"refusedCount,omitempty"`
	Errors       []string        `json:"errors,omitempty"`
	ErrorCount   int             `json:"errorCount,omitempty"`
}

var (
	ErrTransferSu    = errors.New("file transfer is not supported on this server")
	ErrProjectFolder = errors.New("the project folder must be an absolute path other than / and your home folder")
)

const maxTransferSources = 100

func clampTransferTimeout(sec int) int {
	switch {
	case sec == 0:
		return 600
	case sec < 1:
		return 1
	case sec > 3600:
		return 3600
	}
	return sec
}

// validateTransfer checks everything that needs no server. Paths are shown
// to the approver, so control and format characters are refused too.
func validateTransfer(r TransferRequest) error {
	if r.Op != files.OpUpload && r.Op != files.OpDownload {
		return errors.New("unknown transfer")
	}
	if err := config.ValidateDescription(r.Description); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	clean := filepath.Clean(r.Root)
	if !filepath.IsAbs(r.Root) || clean == filepath.VolumeName(clean)+string(filepath.Separator) || home != "" && clean == filepath.Clean(home) {
		return ErrProjectFolder
	}
	srcs, dests := r.Local, r.Remote
	if r.Op == files.OpDownload {
		srcs, dests = r.Remote, r.Local
	}
	if len(srcs) == 0 || len(srcs) > maxTransferSources || len(dests) != 1 {
		return fmt.Errorf("give 1 to %d sources and one destination folder", maxTransferSources)
	}
	for _, p := range append(append([]string{}, r.Local...), r.Remote...) {
		if _, err := config.SanitizeCommand(p, -1); err != nil {
			return errors.New("a path contains a forbidden character")
		}
	}
	for _, p := range r.Local {
		if !filepath.IsLocal(p) {
			return errors.New("local paths must be relative to the project folder and stay inside it")
		}
	}
	for _, p := range r.Remote {
		if err := files.CheckAbs(p); err != nil {
			return errors.New("remote paths must be absolute and clean")
		}
	}
	return nil
}

// transferSummary is the one line that stands in for a command.
func transferSummary(r TransferRequest) string {
	if r.Op == files.OpUpload {
		return fmt.Sprintf("upload %d path(s) to %s", len(r.Local), r.Remote[0])
	}
	return fmt.Sprintf("download %d path(s) to %s", len(r.Remote), r.Local[0])
}

// Transfer is Exec's order for a file transfer (spec 2026-11-02).
func (h *Hub) Transfer(ctx context.Context, r TransferRequest) (TransferResponse, error) {
	dc, err := h.resolveForAuto(r.Server)
	if err != nil {
		var he *hiddenError
		if errors.As(err, &he) {
			fmt.Fprintf(os.Stderr, "hub: resolve %q: %v\n", r.Server, he.detail)
		}
		return TransferResponse{}, err
	}
	if err := validateTransfer(r); err != nil {
		return TransferResponse{}, err
	}
	if dc.SuPassword != "" {
		return TransferResponse{}, ErrTransferSu
	}
	timeout := clampTransferTimeout(r.TimeoutSec)
	red := redactorFor(dc)
	local := make([]string, len(r.Local))
	for i, l := range r.Local {
		local[i] = filepath.Join(r.Root, l)
	}
	conflict := "skip"
	if r.Overwrite {
		conflict = "overwrite"
	}
	base := broker.FileRecord{Action: string(r.Op), Server: r.Server, Host: dc.Host, Port: dc.Port, Remote: r.Remote, Local: local,
		Conflict: conflict, Client: r.Client, Description: red.Redact(r.Description)}

	ar, skip := h.autoStart(ctx, r.Server, false)
	if ar != nil {
		defer ar.done()
		red := redactorFor(dc, ar.dc)
		base.Approval, base.Description = "auto", red.Redact(r.Description)
		resp, err := h.runTransfer(ar.ctx, r, ar.dc, timeout, base, red)
		ran := map[string]any{"server": r.Server, "op": string(r.Op), "command": transferSummary(r),
			"description": base.Description, "time": time.Now().UTC().Format(time.RFC3339)}
		if err != nil {
			ran["error"] = err.Error()
		}
		h.reportAuto(r.Server, ran, err)
		return resp, err
	}
	// Unaudited, as for Exec: nobody can decide while the UI is locked.
	if h.Locked() {
		if skip == skipBusy {
			return TransferResponse{}, ErrAutoBusy
		}
		return TransferResponse{}, ErrLocked
	}

	req := broker.Request{Client: r.Client, Server: r.Server, Target: target(dc), Command: transferSummary(r), Description: r.Description, TimeoutSec: timeout,
		Transfer: &broker.Transfer{Op: string(r.Op), Root: r.Root, Local: r.Local, Remote: r.Remote, Overwrite: r.Overwrite}}
	submitted := time.Now()
	d, err := h.broker.Submit(ctx, req)
	if err != nil {
		return TransferResponse{}, err // ErrTooManyPending, or ctx.Err() when withdrawn
	}
	base.WaitMs = time.Since(submitted).Milliseconds()
	switch d.Outcome {
	case broker.Expired:
		base.Outcome = string(broker.Expired)
		h.auditFile(base)
		return TransferResponse{}, ErrExpired
	case broker.Denied:
		base.Outcome, base.Reason = string(broker.Denied), d.Reason
		h.auditFile(base)
		return TransferResponse{}, &DeniedError{Reason: d.Reason}
	case broker.SentToTab: // Decide refuses it; the hub fails closed anyway
		base.Outcome, base.Reason = "error", broker.ErrSendToTabTransfer.Error()
		h.auditFile(base)
		return TransferResponse{}, broker.ErrSendToTabTransfer
	}

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
		h.auditFile(base)
		return TransferResponse{}, ctx.Err()
	}
	dc2, err := h.resolveForAI(r.Server)
	if err != nil {
		base.Outcome, base.Reason = "error", red.Redact(detail(err))
		h.auditFile(base)
		fmt.Fprintf(os.Stderr, "hub: resolve %q: %s\n", r.Server, base.Reason)
		return TransferResponse{}, err
	}
	if target(dc2) != target(dc) || dc2.HostKey != dc.HostKey {
		base.Outcome = "error"
		base.Reason = fmt.Sprintf("server changed: approved %s %s, now %s %s", target(dc), dc.HostKey, target(dc2), dc2.HostKey)
		h.auditFile(base)
		return TransferResponse{}, ErrServerChanged
	}
	if dc2.SuPassword != "" { // saved during the wait
		base.Outcome, base.Reason = "error", ErrTransferSu.Error()
		h.auditFile(base)
		return TransferResponse{}, ErrTransferSu
	}
	// Secrets may have changed on a reload during the wait; mask both sets.
	red = redactorFor(dc, dc2)
	base.Description = red.Redact(r.Description)
	return h.runTransfer(ctx, r, dc2, timeout, base, red)
}

// aiTransfer lets a server change stop a running AI transfer (jobSet).
type aiTransfer struct {
	server string
	cancel context.CancelFunc
	mu     sync.Mutex
	reason string
}

func (t *aiTransfer) serverName() string { return t.server }

func (t *aiTransfer) end(reason string) {
	t.mu.Lock()
	if t.reason == "" {
		t.reason = reason
	}
	t.mu.Unlock()
	t.cancel()
}

// runTransfer plans and runs on its own SFTP channel, then audits base.
// Every return writes exactly one record.
func (h *Hub) runTransfer(ctx context.Context, r TransferRequest, dc sshx.DialConfig, timeout int, base broker.FileRecord, red *config.Redactor) (TransferResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	fail := func(outcome, reason string, aiErr error) (TransferResponse, error) {
		base.Outcome, base.Reason = outcome, red.Redact(reason)
		h.auditFile(base)
		return TransferResponse{}, aiErr
	}
	root, err := os.OpenRoot(r.Root)
	if err != nil {
		return fail("error", "project folder: "+err.Error(), ErrProjectFolder)
	}
	defer root.Close()
	c, err := h.Registry().Get(r.Server, dc).NewSFTP()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hub: transfer on %q: %s\n", r.Server, red.Redact(err.Error()))
		return fail("error", err.Error(), forAI(err))
	}
	done := make(chan struct{})
	defer c.Close()
	defer close(done) // runs first: the grace closer below must see done before c closes
	// ctx alone does not stop pkg/sftp's calls: after cancelGrace the
	// channel is closed, as for a UI job.
	stop := context.AfterFunc(ctx, func() {
		time.AfterFunc(cancelGrace, func() {
			select {
			case <-done:
			default:
				c.Close()
			}
		})
	})
	defer stop()
	t := &aiTransfer{server: r.Server, cancel: cancel}
	h.files.add(t)
	defer h.files.remove(t)

	var p *files.Plan
	if r.Op == files.OpUpload {
		p, err = files.PlanUploadIn(ctx, c, root, r.Local, r.Remote[0])
	} else {
		dest, oerr := root.OpenRoot(r.Local[0])
		if oerr != nil {
			return fail("error", "download folder: "+oerr.Error(), errors.New("the download folder is missing or not a folder"))
		}
		p, err = files.PlanDownloadIn(ctx, c, r.Remote, dest, filepath.Join(r.Root, r.Local[0]))
		if err == nil {
			p.Check = func(rd io.Reader) (map[string]int, error) { return config.ScanSecrets(red, rd) }
		}
	}
	if err != nil {
		return h.transferEnded(ctx, t, base, red, files.Result{}, err)
	}
	defer p.Close()
	res := p.Run(ctx, c, r.Overwrite, nil)
	resp, err := h.transferEnded(ctx, t, base, red, res, res.Fatal)
	resp.Op = r.Op
	if res.Skipped > 0 {
		resp.SkippedNames = p.Sample
	}
	return resp, err
}

// transferEnded audits a transfer that reached planning and builds the
// AI's answer. A stop (server change, cancel, timeout) wins over res.
func (h *Hub) transferEnded(ctx context.Context, t *aiTransfer, base broker.FileRecord, red *config.Redactor, res files.Result, fatal error) (TransferResponse, error) {
	t.mu.Lock()
	reason := t.reason
	t.mu.Unlock()
	base.Files, base.Bytes, base.Skipped, base.ErrorCount = res.Copied, res.Bytes, res.Skipped, res.Errors.Count
	base.Refused = res.RefusedCount
	for _, rf := range res.Refused {
		for k, n := range rf.Kinds {
			if base.Redacted == nil {
				base.Redacted = map[string]int{}
			}
			base.Redacted[k] += n
		}
	}
	var aiErr error
	switch {
	case reason != "":
		base.Outcome, base.Reason, aiErr = "error", reason, ErrServerChanged // the job set's only reason
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		base.Outcome, base.Reason, aiErr = "error", "timed out", sshx.ErrTimeout
	case ctx.Err() != nil:
		base.Outcome, aiErr = "cancelled_running", ErrCancelledRunning
	case fatal != nil:
		base.Outcome, base.Reason = "error", red.Redact(fatal.Error())
		aiErr = errors.New("transfer stopped: " + base.Reason)
	default:
		base.Outcome = string(broker.Allowed)
	}
	base.Cancelled = ctx.Err() != nil
	h.auditFile(base)
	errs := make([]string, len(res.Errors.List))
	for i, e := range res.Errors.List {
		errs[i] = red.Redact(e)
	}
	resp := TransferResponse{Files: res.Copied, Bytes: res.Bytes, Skipped: res.Skipped, Refused: res.Refused, RefusedCount: res.RefusedCount,
		Errors: errs, ErrorCount: res.Errors.Count}
	return resp, aiErr
}

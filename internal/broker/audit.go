package broker

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type AuditRecord struct {
	Time        time.Time `json:"time"`
	Client      string    `json:"client"`
	Server      string    `json:"server"`
	Command     string    `json:"command"` // already redacted by caller
	Description string    `json:"description,omitempty"`
	Sudo        bool      `json:"sudo,omitempty"`
	TimeoutSec  int       `json:"timeoutSec"`
	Outcome     string    `json:"outcome"` // Outcome values plus "cancelled_running"
	Reason      string    `json:"reason,omitempty"`
	ExitCode    *int      `json:"exitCode,omitempty"`
	DurationMs  int64     `json:"durationMs,omitempty"`
	StdoutBytes int       `json:"stdoutBytes,omitempty"`
	StderrBytes int       `json:"stderrBytes,omitempty"`
	Approval    string    `json:"approval,omitempty"` // "auto" when a grant allowed it (spec 2026-09-28)
	WaitMs      int64     `json:"waitMs,omitempty"`   // submit to the human's decision
}

// ConfigRecord is an audit line for a vault change made from the app. It
// never holds a secret: Changed names a changed secret field, nothing more.
type ConfigRecord struct {
	Time           time.Time `json:"time"`
	Kind           string    `json:"kind"`   // always "config"; exec records have none
	Action         string    `json:"action"` // trust, forgetHostKey, delete, vaultCreate, save, import, autoAllowOn, autoAllowResume, autoAllowOff, autoAllowCheck
	Server         string    `json:"server,omitempty"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	Fingerprint    string    `json:"fingerprint,omitempty"`
	Algo           string    `json:"algo,omitempty"`
	OldFingerprint string    `json:"oldFingerprint,omitempty"`
	KeptServers    []string  `json:"keptServers,omitempty"`
	Changed        []string  `json:"changed,omitempty"`
	Until          string    `json:"until,omitempty"`   // autoAllowOn: RFC 3339 deadline of a timed grant
	Forever        bool      `json:"forever,omitempty"` // autoAllowOn: a forever grant
	Reason         string    `json:"reason,omitempty"`  // autoAllowOff: why it ended; autoAllowCheck: the result
}

// FileRecord is an audit line for a file operation from the app. Transfers
// and deletes write two, at start and at end; mkdir and rename write one.
type FileRecord struct {
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"`            // always "file"
	Phase      string    `json:"phase,omitempty"` // start, end; empty for mkdir and rename
	Action     string    `json:"action"`          // upload, download, delete, mkdir, rename
	Server     string    `json:"server"`
	Host       string    `json:"host,omitempty"`
	Port       int       `json:"port,omitempty"`
	Remote     []string  `json:"remote,omitempty"` // first 20
	Local      []string  `json:"local,omitempty"`  // first 20; transfers only
	From       string    `json:"from,omitempty"`
	To         string    `json:"to,omitempty"`
	Conflict   string    `json:"conflict,omitempty"`
	Files      int       `json:"files,omitempty"`
	Bytes      int64     `json:"bytes,omitempty"`
	Skipped    int       `json:"skipped,omitempty"`
	ErrorCount int       `json:"errorCount,omitempty"`
	Cancelled  bool      `json:"cancelled,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

// TunnelRecord is an audit line for a port forward: one at start, one at end.
type TunnelRecord struct {
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"`  // always "tunnel"
	Phase      string    `json:"phase"` // start, end
	Server     string    `json:"server"`
	Target     string    `json:"target"` // user@host:port
	ID         string    `json:"id"`
	TunnelKind string    `json:"tunnelKind"` // local, remote, dynamic
	Listen     string    `json:"listen"`
	To         string    `json:"to,omitempty"`
	Conns      int       `json:"conns,omitempty"` // end: connections served
	Reason     string    `json:"reason,omitempty"`
}

// Audit appends one JSON object per line. Output content is never part of a
// record; only byte counts are.
type Audit struct {
	mu sync.Mutex
	f  *os.File
}

func OpenAudit(path string) (*Audit, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	// O_CREATE's mode only applies when the file is newly created; enforce
	// 0600 explicitly so a pre-existing file with looser permissions is
	// tightened too.
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	return &Audit{f: f}, nil
}

func (a *Audit) Write(r AuditRecord) error { return a.append(r) }

func (a *Audit) WriteConfig(r ConfigRecord) error {
	r.Kind = "config"
	return a.append(r)
}

func (a *Audit) WriteFile(r FileRecord) error {
	r.Kind = "file"
	return a.append(r)
}

func (a *Audit) WriteTunnel(r TunnelRecord) error {
	r.Kind = "tunnel"
	return a.append(r)
}

func (a *Audit) append(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err = a.f.Write(append(line, '\n'))
	return err
}

func (a *Audit) Close() error { return a.f.Close() }

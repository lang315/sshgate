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
}

// ConfigRecord is an audit line for a vault change made from the app. It
// never holds a secret: Changed names a changed secret field, nothing more.
type ConfigRecord struct {
	Time           time.Time `json:"time"`
	Kind           string    `json:"kind"`   // always "config"; exec records have none
	Action         string    `json:"action"` // trust, forgetHostKey, delete, vaultCreate, save, import
	Server         string    `json:"server,omitempty"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	Fingerprint    string    `json:"fingerprint,omitempty"`
	Algo           string    `json:"algo,omitempty"`
	OldFingerprint string    `json:"oldFingerprint,omitempty"`
	KeptServers    []string  `json:"keptServers,omitempty"`
	Changed        []string  `json:"changed,omitempty"`
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

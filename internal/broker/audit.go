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
	return &Audit{f: f}, nil
}

func (a *Audit) Write(r AuditRecord) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err = a.f.Write(append(line, '\n'))
	return err
}

func (a *Audit) Close() error { return a.f.Close() }

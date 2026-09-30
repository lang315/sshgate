package broker

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

type AuditRecord struct {
	Time        time.Time      `json:"time"`
	Client      string         `json:"client"`
	Server      string         `json:"server"`
	Command     string         `json:"command"` // already redacted by caller
	Description string         `json:"description,omitempty"`
	Sudo        bool           `json:"sudo,omitempty"`
	TimeoutSec  int            `json:"timeoutSec"`
	Outcome     string         `json:"outcome"` // Outcome values plus "cancelled_running"
	Reason      string         `json:"reason,omitempty"`
	ExitCode    *int           `json:"exitCode,omitempty"`
	DurationMs  int64          `json:"durationMs,omitempty"`
	StdoutBytes int            `json:"stdoutBytes,omitempty"`
	StderrBytes int            `json:"stderrBytes,omitempty"`
	Redacted    map[string]int `json:"redacted,omitempty"` // RedactPatterns counts by kind, both streams, including parts of long output the cap drops; never a value
	Approval    string         `json:"approval,omitempty"` // "auto" when a grant allowed it (spec 2026-09-28)
	WaitMs      int64          `json:"waitMs,omitempty"`   // submit to the human's decision
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
// record; only byte counts are. A record's seq is its 1-based line number.
type Audit struct {
	mu       sync.Mutex
	f        *os.File
	path     string
	n        int // lines in the file: the last record's seq
	onAppend func(seq int, line json.RawMessage)
}

func OpenAudit(path string) (*Audit, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
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
	a := &Audit{f: f, path: path}
	data, err := a.readAll()
	if err != nil {
		f.Close()
		return nil, err
	}
	a.n = bytes.Count(data, []byte{'\n'})
	// A line cut short (a crash mid-write) is ended here: it stays one
	// malformed line, and the next record starts on its own line.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if _, err := f.Write([]byte{'\n'}); err != nil {
			f.Close()
			return nil, err
		}
		a.n++
	}
	return a, nil
}

// OnAppend sets fn to be called after each record is written, with its seq
// and the line without its newline. It runs on the writer's goroutine with
// a.mu released, and must not block. The hub sets it once.
func (a *Audit) OnAppend(fn func(seq int, line json.RawMessage)) {
	a.mu.Lock()
	a.onAppend = fn
	a.mu.Unlock()
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
	_, err = a.f.Write(append(line, '\n'))
	if err == nil {
		a.n++
	}
	seq, fn := a.n, a.onAppend
	a.mu.Unlock()
	if err == nil && fn != nil {
		fn(seq, line)
	}
	return err
}

// readAll reads the whole file through a.f, the file being written to.
func (a *Audit) readAll() ([]byte, error) {
	return io.ReadAll(io.NewSectionReader(a.f, 0, math.MaxInt64))
}

const (
	DefaultReadLimit = 200
	MaxReadLimit     = 500
)

// ReadQuery selects records for Read. Kinds and Outcomes together pick a
// union: a record passes if its kind is in Kinds, or it is an exec record
// whose outcome is in Outcomes; both empty passes every record. Server and
// Text then narrow that.
type ReadQuery struct {
	Before   int      `json:"before"`   // only seq < Before; 0 means from the newest
	Limit    int      `json:"limit"`    // 0 means DefaultReadLimit; capped at MaxReadLimit
	Server   string   `json:"server"`   // an exact server name
	Kinds    []string `json:"kinds"`    // exec, config, file, tunnel
	Outcomes []string `json:"outcomes"` // allowed, auto, denied, expired, cancelled, error
	Text     string   `json:"text"`     // case-insensitive substring of the line
}

type Entry struct {
	Seq    int             `json:"seq"`
	Record json.RawMessage `json:"record"`
}

type ReadResult struct {
	Records []Entry `json:"records"`        // newest first
	Next    int     `json:"next,omitempty"` // the smallest seq returned, when older matches exist
	Skipped int     `json:"skipped"`        // lines in the whole file that did not parse
	Path    string  `json:"path"`
}

// fields are what Read filters on. An exec record has no kind.
type fields struct {
	Kind     string `json:"kind"`
	Server   string `json:"server"`
	Outcome  string `json:"outcome"`
	Approval string `json:"approval"`
}

func (f fields) outcomeIs(o string) bool {
	switch o {
	case "auto":
		return f.Approval == "auto"
	case "allowed":
		return f.Outcome == "allowed" && f.Approval != "auto"
	case "cancelled":
		return f.Outcome == "approved_but_cancelled" || f.Outcome == "cancelled_running"
	}
	return f.Outcome == o
}

// json.Marshal writes <, > and & as \u003c, \u003e and \u0026; Text matches what a person would type.
var unescapeHTML = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&")

// match reports whether a parsed line passes q; text is q.Text lowercased.
func (q ReadQuery) match(f fields, line []byte, text string) bool {
	if q.Server != "" && f.Server != q.Server {
		return false
	}
	if text != "" && !strings.Contains(strings.ToLower(unescapeHTML.Replace(string(line))), text) {
		return false
	}
	if len(q.Kinds) == 0 && len(q.Outcomes) == 0 {
		return true
	}
	kind := cmp.Or(f.Kind, "exec")
	if slices.Contains(q.Kinds, kind) {
		return true
	}
	return kind == "exec" && slices.ContainsFunc(q.Outcomes, f.outcomeIs)
}

// Read returns up to q.Limit records matching q, newest first, before
// q.Before. It holds a.mu only while reading, so it never sees a
// half-written line.
// ponytail: whole-file scan on every call. Switch to a backwards reader from
// EOF when the file is large; that is also when rotation is needed.
func (a *Audit) Read(q ReadQuery) (ReadResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	limit = min(limit, MaxReadLimit)
	a.mu.Lock()
	data, err := a.readAll()
	a.mu.Unlock()
	if err != nil {
		return ReadResult{}, err
	}
	res := ReadResult{Records: []Entry{}, Path: a.path}
	text := strings.ToLower(q.Text)
	lines := bytes.Split(data, []byte{'\n'})
	lines = lines[:len(lines)-1] // what follows the last '\n' is not a line yet
	more := false
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		var f fields
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &f) != nil {
			res.Skipped++
			continue
		}
		seq := i + 1
		if q.Before > 0 && seq >= q.Before || !q.match(f, line, text) {
			continue
		}
		if len(res.Records) == limit {
			more = true
			continue
		}
		res.Records = append(res.Records, Entry{Seq: seq, Record: line})
	}
	if more {
		res.Next = res.Records[len(res.Records)-1].Seq
	}
	return res, nil
}

func (a *Audit) Close() error { return a.f.Close() }

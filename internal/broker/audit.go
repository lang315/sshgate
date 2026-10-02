package broker

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
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
	Action         string    `json:"action"` // trust, forgetHostKey, delete, vaultCreate, save, import, autoAllowOn, autoAllowResume, autoAllowOff, autoAllowCheck, softLock
	Server         string    `json:"server,omitempty"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	Fingerprint    string    `json:"fingerprint,omitempty"`
	Algo           string    `json:"algo,omitempty"`
	OldFingerprint string    `json:"oldFingerprint,omitempty"`
	KeptServers    []string  `json:"keptServers,omitempty"`
	Changed        []string  `json:"changed,omitempty"`
	Servers        []string  `json:"servers,omitempty"` // softLock: the hosts whose grants were live when the UI locked
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
	n        int   // lines in the file: the last record's seq
	size     int64 // bytes in the file as of the last write through a; every byte below it is a whole line
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
	if err := a.recountLocked(); err != nil {
		f.Close()
		return nil, err
	}
	return a, nil
}

// recountLocked re-derives n and size from the file, with a.mu held (or
// before a is shared). It streams the file with a fixed buffer. A line cut
// short (a crash mid-write) is ended here: it stays one malformed line, and
// the next record starts on its own line.
func (a *Audit) recountLocked() error {
	st, err := a.f.Stat()
	if err != nil {
		return err
	}
	size := st.Size()
	n := 0
	buf := make([]byte, 64<<10)
	for off := int64(0); off < size; {
		m, err := a.f.ReadAt(buf[:min(int64(len(buf)), size-off)], off)
		n += bytes.Count(buf[:m], []byte{'\n'})
		off += int64(m)
		if err != nil && !(err == io.EOF && off == size) {
			return err
		}
	}
	if size > 0 {
		var last [1]byte
		if _, err := a.f.ReadAt(last[:], size-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			if _, err := a.f.Write([]byte{'\n'}); err != nil {
				return err
			}
			n++
			size++
		}
	}
	a.n, a.size = n, size
	return nil
}

// stripLocal drops a file record's local paths: they exist only in the
// desktop app's main process, so they never leave the broker (Read, OnAppend)
// and a text search never sees them. The file on disk keeps the field. Every
// other line is returned untouched.
func stripLocal(line []byte) []byte {
	if !bytes.Contains(line, []byte(`"local"`)) {
		return line
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(line, &m) != nil || string(m["kind"]) != `"file"` {
		return line
	}
	if _, ok := m["local"]; !ok {
		return line
	}
	delete(m, "local")
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return out
}

// OnAppend sets fn to be called after each record is written, with its seq
// and the line without its newline (a file record without its `local`). It runs on the writer's goroutine with
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
	// The file can change under us (a hand edit, a second hub): seq must stay
	// the line number Read reports, so recount when its size is not ours.
	if st, serr := a.f.Stat(); serr != nil {
		err = serr
	} else if st.Size() != a.size {
		err = a.recountLocked()
	}
	if err == nil {
		// On a failed write size stays put, so a partial line is seen as a
		// size mismatch and ended by the next append's recount.
		if _, err = a.f.Write(append(line, '\n')); err == nil {
			a.n++
			a.size += int64(len(line)) + 1
		}
	}
	seq, fn := a.n, a.onAppend
	a.mu.Unlock()
	if err == nil && fn != nil {
		fn(seq, stripLocal(line))
	}
	return err
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
	Text     string   `json:"text"`     // case-insensitive substring of the record's string and number values
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
	Kind     string   `json:"kind"`
	Server   string   `json:"server"`
	Servers  []string `json:"servers"` // softLock: the hosts whose grants were live
	Outcome  string   `json:"outcome"`
	Approval string   `json:"approval"`
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

// leaves joins, with "\n", every string and number value of a decoded JSON
// value, walked recursively: what a row shows, never a key name or an escape.
func leaves(v any, sb *strings.Builder) {
	switch v := v.(type) {
	case string:
		sb.WriteString(v)
		sb.WriteByte('\n')
	case json.Number:
		sb.WriteString(v.String())
		sb.WriteByte('\n')
	case []any:
		for _, x := range v {
			leaves(x, sb)
		}
	case map[string]any:
		for _, x := range v {
			leaves(x, sb)
		}
	}
}

// hasText reports whether text (lowercased) is in line's decoded leaf values.
func hasText(line []byte, text string) bool {
	var v any
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return false
	}
	var sb strings.Builder
	leaves(v, &sb)
	return strings.Contains(strings.ToLower(sb.String()), text)
}

// match reports whether a parsed line passes q; text is q.Text lowercased.
func (q ReadQuery) match(f fields, line []byte, text string) bool {
	if q.Server != "" && f.Server != q.Server && !slices.Contains(f.Servers, q.Server) {
		return false
	}
	if text != "" && !hasText(line, text) {
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

// linesBackward calls fn with each line in the first size bytes of r, last
// line first and without its newline, until fn returns false. Those bytes end
// with '\n' (Audit.size). It holds one 64 KiB chunk and the line it is
// assembling, never the file; line is valid only during the call.
func linesBackward(r io.ReaderAt, size int64, fn func(line []byte) bool) error {
	buf := make([]byte, 64<<10)
	var carry []byte // the end of a line whose start is in an earlier chunk
	tail := true     // still in what follows the last '\n', which is not a line yet
	for end := size; end > 0; {
		n := min(int64(len(buf)), end)
		end -= n
		chunk := buf[:n]
		if m, err := r.ReadAt(chunk, end); m < len(chunk) {
			return fmt.Errorf("audit file changed during the read: %w", err) // it shrank under us
		}
		for {
			i := bytes.LastIndexByte(chunk, '\n')
			if i < 0 {
				break
			}
			if !tail {
				line := chunk[i+1:]
				if !fn(append(line[:len(line):len(line)], carry...)) {
					return nil
				}
			}
			tail, carry, chunk = false, nil, chunk[:i]
		}
		if !tail {
			carry = append(bytes.Clone(chunk), carry...) // chunk is a view of buf, which the next read reuses
		}
	}
	if !tail {
		fn(carry) // the file's first line
	}
	return nil
}

// Read returns up to q.Limit records matching q, newest first, before
// q.Before. It reads backwards from the size recorded under a.mu, a chunk at
// a time, and stops at the first older match past the limit: it never sees a
// half-written line, never holds a.mu while reading, and never holds the
// file in memory. Skipped counts the unparsed lines in the span this call
// covers (from q.Before, or the newest line, down to the last record
// returned, or to the start of the file when no older match exists), so the
// counts of successive pages add up to the whole file's.
// ponytail: a page deep in the file still walks down from EOF to q.Before,
// without parsing. Return a byte offset with Next when that walk shows.
func (a *Audit) Read(q ReadQuery) (ReadResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	limit = min(limit, MaxReadLimit)
	// Bytes below a.size are whole lines and the file only grows through a,
	// so the read needs no lock: a slow read never holds up a writer. seq is
	// counted down from a.n, so a.n must be this file's line count: recount
	// after a hand edit, as append does.
	a.mu.Lock()
	var err error
	if st, serr := a.f.Stat(); serr != nil {
		err = serr
	} else if st.Size() != a.size {
		err = a.recountLocked()
	}
	seq, size := a.n+1, a.size
	a.mu.Unlock()
	if err != nil {
		return ReadResult{}, err
	}
	res := ReadResult{Records: []Entry{}, Path: a.path}
	text := strings.ToLower(q.Text)
	more := false
	below := 0 // unparsed lines below the last record returned
	err = linesBackward(a.f, size, func(line []byte) bool {
		seq--
		if q.Before > 0 && seq >= q.Before {
			return true
		}
		line = stripLocal(line) // strip before ANY use: matching, unmarshaling, returning
		var f fields
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &f) != nil {
			below++
			return true
		}
		if !q.match(f, line, text) {
			return true
		}
		if len(res.Records) == limit {
			more = true
			return false
		}
		res.Skipped += below
		below = 0
		res.Records = append(res.Records, Entry{Seq: seq, Record: bytes.Clone(line)}) // line is a view of the read buffer
		return true
	})
	if err != nil {
		return ReadResult{}, err
	}
	if more {
		res.Next = res.Records[len(res.Records)-1].Seq
	} else {
		res.Skipped += below // the scan reached the start of the file
	}
	return res, nil
}

func (a *Audit) Close() error { return a.f.Close() }

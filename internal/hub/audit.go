package hub

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/rpc"
)

// auditAppended is the audit log's OnAppend callback. It runs on whichever
// goroutine wrote the record, sometimes with h.mu held (CreateVault,
// SaveServer, SetAutoAllow, a grant ending), so it takes no hub lock: the
// lock state and the sink are atomics. Nothing is sent while locked.
// ponytail: the UI door's sink queues on a 256-slot channel drained by one
// goroutine, so this never waits on the pipe; when the reader is 256 records
// behind, new records are dropped (the renderer dedupes by seq and Refreshes).
func (h *Hub) auditAppended(seq int, line json.RawMessage) {
	if !h.unlocked.Load() {
		return
	}
	if f := h.auditSink.Load(); f != nil {
		(*f)(seq, stripLocal(line))
	}
}

// stripLocal drops a file record's local paths: they exist only in the
// desktop app's main process, never in the renderer. Every other line is
// returned untouched; the file on disk keeps the field.
func stripLocal(line json.RawMessage) json.RawMessage {
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

// setAuditSink installs f for audit.appended; release clears it only while
// it is still f, like setEventSink's.
func (h *Hub) setAuditSink(f func(seq int, line json.RawMessage)) (release func()) {
	p := &f
	h.auditSink.Store(p)
	return func() { h.auditSink.CompareAndSwap(p, nil) }
}

var (
	auditKinds    = []string{"exec", "config", "file", "tunnel"}
	auditOutcomes = []string{"allowed", "auto", "denied", "expired", "cancelled", "error"}
)

// auditQuery decodes audit.read's params strictly and checks every value.
func auditQuery(raw json.RawMessage) (broker.ReadQuery, error) {
	var q broker.ReadQuery
	if err := strictParams(raw, &q, "before", "limit", "server", "kinds", "outcomes", "text"); err != nil {
		return q, err
	}
	if q.Before < 0 {
		return q, &rpc.Error{Code: -32602, Message: "before must be a seq"}
	}
	if q.Limit < 0 || q.Limit > broker.MaxReadLimit {
		return q, &rpc.Error{Code: -32602, Message: "limit must be 0-" + strconv.Itoa(broker.MaxReadLimit)}
	}
	for _, k := range q.Kinds {
		if !slices.Contains(auditKinds, k) {
			return q, &rpc.Error{Code: -32602, Message: "unknown kind " + strconv.Quote(k)}
		}
	}
	for _, o := range q.Outcomes {
		if !slices.Contains(auditOutcomes, o) {
			return q, &rpc.Error{Code: -32602, Message: "unknown outcome " + strconv.Quote(o)}
		}
	}
	return q, nil
}

// ReadAudit serves audit.read. The log is shown only while the vault is
// unlocked.
func (h *Hub) ReadAudit(q broker.ReadQuery) (broker.ReadResult, error) {
	if !h.unlocked.Load() {
		return broker.ReadResult{}, ErrLocked
	}
	if h.audit == nil {
		return broker.ReadResult{Records: []broker.Entry{}}, nil
	}
	res, err := h.audit.Read(q)
	for i := range res.Records {
		res.Records[i].Record = stripLocal(res.Records[i].Record)
	}
	return res, err
}

package hub

import (
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
		(*f)(seq, line)
	}
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
	return h.audit.Read(q)
}

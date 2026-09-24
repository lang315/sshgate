package hub

import (
	"context"
	"encoding/json"
	"io"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/rpc"
)

// uiServer is the UI door's server listing shape: every stored server
// (visible or hidden), with connection metadata but never a secret field.
type uiServer struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Auth      string `json:"auth"`
	HostKey   string `json:"hostKey"`
	AIVisible bool   `json:"aiVisible"`
	Locked    bool   `json:"locked"`
}

// hasStore reports whether a store file was loaded, under h.mu.
func (h *Hub) hasStore() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.deps.File != nil
}

// serversForUI lists every stored server, unlike ServersForMCP which hides
// non-AIVisible ones. No secret field is ever included.
func (h *Hub) serversForUI() []uiServer {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []uiServer{}
	if h.deps.File == nil {
		return out
	}
	for _, s := range h.deps.File.Servers {
		out = append(out, uiServer{
			Name: s.Name, Host: s.Host, Port: s.Port, User: s.User, Auth: s.Auth,
			HostKey: s.HostKey, AIVisible: s.AIVisible, Locked: h.deps.IsLocked(s.Name),
		})
	}
	return out
}

// ServeUIDoor serves the full-privilege door on r/w (stdio for a desktop app
// later, an in-process pipe for tests and a CLI approver). Broker events are
// pushed as notifications for the lifetime of the call.
func ServeUIDoor(ctx context.Context, h *Hub, r io.Reader, w io.Writer) error {
	s := rpc.NewServer()
	release := h.setEventSink(func(e broker.Event) {
		switch e.Kind {
		case "pending":
			s.Notify("pending", map[string]any{"request": e.Request})
		case "decided":
			s.Notify("decided", map[string]any{"request": e.Request, "decision": e.Decision})
		}
	})
	defer release()

	empty := map[string]any{}
	// Every method here is request-only (term.write/resize/ack are the
	// exception, see term.go): a notification-form call is ignored
	// rather than running inline on the read loop. unlock runs argon2 and
	// must never block inbound dispatch that way.
	s.HandleRequest("status", func(context.Context, json.RawMessage) (any, error) {
		_ = h.Reload()
		return map[string]any{"locked": h.Locked(), "hasStore": h.hasStore(), "pending": len(h.Broker().Pending())}, nil
	})
	s.HandleRequest("unlock", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Password string `json:"password"`
		}
		json.Unmarshal(raw, &p)
		return empty, h.Unlock(p.Password)
	})
	s.HandleRequest("lock", func(context.Context, json.RawMessage) (any, error) {
		h.Lock()
		return empty, nil
	})
	s.HandleRequest("servers", func(context.Context, json.RawMessage) (any, error) {
		_ = h.Reload()
		return h.serversForUI(), nil
	})
	s.HandleRequest("pending", func(context.Context, json.RawMessage) (any, error) {
		p := h.Broker().Pending()
		if p == nil {
			p = []broker.Request{}
		}
		return p, nil
	})
	s.HandleRequest("decide", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID      string `json:"id"`
			Outcome string `json:"outcome"`
			Reason  string `json:"reason"`
		}
		json.Unmarshal(raw, &p)
		switch broker.Outcome(p.Outcome) {
		case broker.Allowed, broker.Denied, broker.SentToTab:
		default:
			return nil, &rpc.Error{Code: -32602, Message: "outcome must be allowed, denied, or sent_to_tab"}
		}
		return empty, h.Broker().Decide(p.ID, broker.Decision{Outcome: broker.Outcome(p.Outcome), Reason: p.Reason})
	})
	s.HandleRequest("denyAll", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Reason string `json:"reason"`
		}
		json.Unmarshal(raw, &p)
		h.Broker().DenyAll(p.Reason)
		return empty, nil
	})
	closeTerms := registerTermMethods(s, h)
	defer closeTerms() // after Serve: no term.open is still in flight
	return s.Serve(ctx, r, w)
}

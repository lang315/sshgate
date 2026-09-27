package hub

import (
	"context"
	"encoding/json"
	"io"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
)

// uiServer is the UI door's server listing shape: every stored server
// (visible or hidden), with connection metadata but never a secret field;
// has* only says whether one is stored.
type uiServer struct {
	Name             string `json:"name"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	User             string `json:"user"`
	Auth             string `json:"auth"`
	KeyPath          string `json:"keyPath"`
	HostKey          string `json:"hostKey"`
	HostKeyAlgo      string `json:"hostKeyAlgo"`
	AIVisible        bool   `json:"aiVisible"`
	Locked           bool   `json:"locked"`
	HasPassword      bool   `json:"hasPassword"`
	HasSuPassword    bool   `json:"hasSuPassword"`
	HasSudoPassword  bool   `json:"hasSudoPassword"`
	HasKeyPassphrase bool   `json:"hasKeyPassphrase"`
}

// hasStore reports whether a store file was loaded, under h.mu.
func (h *Hub) hasStore() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.deps.File != nil
}

// hasVault reports whether the store has a master password (a KDF), under h.mu.
func (h *Hub) hasVault() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.deps.File != nil && h.deps.File.KDF != nil
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
			Name: s.Name, Host: s.Host, Port: s.Port, User: s.User, Auth: s.Auth, KeyPath: s.KeyPath,
			HostKey: s.HostKey, HostKeyAlgo: s.HostKeyAlgo, AIVisible: s.AIVisible, Locked: h.deps.IsLocked(s.Name),
			HasPassword: s.EncPassword != "", HasSuPassword: s.EncSuPassword != "",
			HasSudoPassword: s.EncSudoPassword != "", HasKeyPassphrase: s.EncKeyPassphrase != "",
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
	releaseLock := h.setLockSink(func(reason string) {
		s.Notify("locked", map[string]string{"reason": reason})
	})
	defer releaseLock()
	// req registers a request that counts as UI activity for the idle
	// auto-lock. status does not: the desktop app polls it.
	req := func(name string, fn rpc.Handler) {
		s.HandleRequest(name, func(ctx context.Context, raw json.RawMessage) (any, error) {
			h.touch()
			return fn(ctx, raw)
		})
	}

	empty := map[string]any{}
	// Every method here is request-only (term.write/resize/ack are the
	// exception, see term.go): a notification-form call is ignored
	// rather than running inline on the read loop. unlock runs argon2 and
	// must never block inbound dispatch that way.
	s.HandleRequest("status", func(context.Context, json.RawMessage) (any, error) {
		_ = h.Reload() // a failure is remembered and shown as storeError
		st := map[string]any{"locked": h.Locked(), "hasStore": h.hasStore(), "hasVault": h.hasVault(),
			"storePath": h.o.StorePath, "pending": len(h.Broker().Pending())}
		if e := h.storeError(); e != "" {
			st["storeError"] = e
		}
		return st, nil
	})
	req("unlock", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Password string `json:"password"`
		}
		json.Unmarshal(raw, &p)
		return empty, h.Unlock(p.Password)
	})
	req("lock", func(context.Context, json.RawMessage) (any, error) {
		h.Lock()
		return empty, nil
	})
	req("vault.create", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Password string `json:"password"`
		}
		json.Unmarshal(raw, &p)
		if len(p.Password) < 8 {
			return nil, &rpc.Error{Code: -32602, Message: "password must be at least 8 characters"}
		}
		return empty, h.CreateVault(p.Password)
	})
	req("servers.save", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Original string             `json:"original"`
			Server   config.ServerInput `json:"server"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
		}
		if err := p.Server.Validate(); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: err.Error()}
		}
		return empty, h.SaveServer(p.Original, p.Server)
	})
	req("servers.delete", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
		}
		return empty, h.DeleteServer(p.Name)
	})
	req("servers.forgetHostKey", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
		}
		return empty, h.ForgetHostKey(p.Name)
	})
	req("import.scan", func(ctx context.Context, _ json.RawMessage) (any, error) {
		return h.ImportScan(ctx)
	})
	req("import.apply", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Aliases []string `json:"aliases"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
		}
		imported, skipped, err := h.ImportApply(ctx, p.Aliases)
		if err != nil {
			return nil, err
		}
		return map[string]any{"imported": imported, "skipped": skipped}, nil
	})
	req("servers", func(context.Context, json.RawMessage) (any, error) {
		_ = h.Reload()
		return h.serversForUI(), nil
	})
	req("pending", func(context.Context, json.RawMessage) (any, error) {
		p := h.Broker().Pending()
		if p == nil {
			p = []broker.Request{}
		}
		return p, nil
	})
	req("decide", func(_ context.Context, raw json.RawMessage) (any, error) {
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
	req("denyAll", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Reason string `json:"reason"`
		}
		json.Unmarshal(raw, &p)
		h.Broker().DenyAll(p.Reason)
		return empty, nil
	})
	req("hello", func(context.Context, json.RawMessage) (any, error) {
		return map[string]int{"protocol": ProtocolVersion}, nil
	})
	closeTerms := registerTermMethods(s, h)
	defer closeTerms() // after Serve: no term.open is still in flight
	closeFiles := registerFileMethods(s, h)
	defer closeFiles() // after Serve: cancels this door's jobs and waits up to 5 s
	releaseTunnels := registerTunnelMethods(s, h)
	defer releaseTunnels()
	return s.Serve(ctx, r, w)
}

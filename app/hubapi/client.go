package hubapi

import "context"

// Caller is the UI door client (door.Client) or a test fake.
type Caller interface {
	Call(ctx context.Context, method string, params, result any) error
}

type Client struct{ c Caller }

func New(c Caller) *Client { return &Client{c: c} }

func (h *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := h.c.Call(ctx, "status", nil, &s)
	return s, err
}

func (h *Client) Unlock(ctx context.Context, pw string) (UnlockReply, error) {
	var r UnlockReply
	err := h.c.Call(ctx, "unlock", map[string]string{"password": pw}, &r)
	return r, err
}

// Lock sends {} (a soft lock when a grant is live) or {"stopAuto":true}
// (hard lock, ends every grant); the hub refuses anything else.
func (h *Client) Lock(ctx context.Context, stopAuto bool) error {
	p := map[string]bool{}
	if stopAuto {
		p["stopAuto"] = true
	}
	return h.c.Call(ctx, "lock", p, nil)
}

func (h *Client) Servers(ctx context.Context) ([]Server, error) {
	s := []Server{}
	if err := h.c.Call(ctx, "servers", nil, &s); err != nil {
		return nil, err
	}
	if s == nil {
		s = []Server{}
	}
	return s, nil
}

func (h *Client) Pending(ctx context.Context) ([]Request, error) {
	p := []Request{}
	if err := h.c.Call(ctx, "pending", nil, &p); err != nil {
		return nil, err
	}
	if p == nil {
		p = []Request{}
	}
	return p, nil
}

func (h *Client) Decide(ctx context.Context, id, outcome, reason string) error {
	return h.c.Call(ctx, "decide", map[string]string{"id": id, "outcome": outcome, "reason": reason}, nil)
}

func (h *Client) DenyAll(ctx context.Context, reason string) error {
	return h.c.Call(ctx, "denyAll", map[string]string{"reason": reason}, nil)
}

func (h *Client) CreateVault(ctx context.Context, pw string) error {
	return h.c.Call(ctx, "vault.create", map[string]string{"password": pw}, nil)
}

func (h *Client) SetAutoAllow(ctx context.Context, server, mode string) error {
	return h.c.Call(ctx, "servers.setAutoAllow", map[string]string{"server": server, "mode": mode}, nil)
}

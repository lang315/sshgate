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

// ForgetHostKey unpins a server's host key; the hub closes its connection,
// so its terminals exit.
func (h *Client) ForgetHostKey(ctx context.Context, name string) error {
	return h.c.Call(ctx, "servers.forgetHostKey", map[string]string{"name": name}, nil)
}

// SaveServer creates a server (original "") or updates the one named
// original. autoAllow is the grant to arm after the write ("off" arms none);
// the hub ends the server's old grant on every save.
func (h *Client) SaveServer(ctx context.Context, original string, in ServerInput, autoAllow string) error {
	p := struct {
		Original  string      `json:"original,omitempty"`
		Server    ServerInput `json:"server"`
		AutoAllow string      `json:"autoAllow"`
	}{original, in, autoAllow}
	return h.c.Call(ctx, "servers.save", p, nil)
}

func (h *Client) DeleteServer(ctx context.Context, name string) error {
	return h.c.Call(ctx, "servers.delete", map[string]string{"name": name}, nil)
}

// ImportScan lists the aliases of the hub's SSH config; it never dials.
func (h *Client) ImportScan(ctx context.Context) (ImportScan, error) {
	var s ImportScan
	if err := h.c.Call(ctx, "import.scan", nil, &s); err != nil {
		return ImportScan{}, err
	}
	if s.Candidates == nil {
		s.Candidates = []Candidate{}
	}
	return s, nil
}

// ImportApply sends alias names only: the hub recomputes every field.
func (h *Client) ImportApply(ctx context.Context, aliases []string) (ImportResult, error) {
	var r ImportResult
	if err := h.c.Call(ctx, "import.apply", map[string][]string{"aliases": aliases}, &r); err != nil {
		return ImportResult{}, err
	}
	return r, nil
}

// AutoAllowCheck runs the hub's root check on the server's pinned
// connection; a host it cannot read answers "could not check this host".
func (h *Client) AutoAllowCheck(ctx context.Context, server string) (AutoCheck, error) {
	var c AutoCheck
	err := h.c.Call(ctx, "servers.autoAllowCheck", map[string]string{"server": server}, &c)
	return c, err
}

// SetSoftLockLimit stores how long auto-allow may run under a soft lock:
// "24h", "3d", "7d" or "none".
func (h *Client) SetSoftLockLimit(ctx context.Context, limit string) error {
	return h.c.Call(ctx, "settings.setSoftLockLimit", map[string]string{"limit": limit}, nil)
}

// FilesList lists path on server ("" is the login's home). trust is nil
// except on the retry after a host-key prompt; the hub pins that key only
// while the server has none. Entries is never nil.
func (h *Client) FilesList(ctx context.Context, server, path string, trust *Trust) (FileListing, error) {
	p := struct {
		Server       string `json:"server"`
		Path         string `json:"path"`
		TrustHostKey *Trust `json:"trustHostKey,omitempty"`
	}{server, path, trust}
	var l FileListing
	if err := h.c.Call(ctx, "files.list", p, &l); err != nil {
		return FileListing{}, err
	}
	if l.Entries == nil {
		l.Entries = []FileEntry{}
	}
	return l, nil
}

func (h *Client) FilesMkdir(ctx context.Context, server, path string) error {
	return h.c.Call(ctx, "files.mkdir", map[string]string{"server": server, "path": path}, nil)
}

// FilesRename never replaces: an existing to is refused.
func (h *Client) FilesRename(ctx context.Context, server, from, to string) error {
	return h.c.Call(ctx, "files.rename", map[string]string{"server": server, "from": from, "to": to}, nil)
}

// FilesPlan starts job id: op "upload" (local sources into the remote
// folder dest), "download" (remote sources into the local folder dest) or
// "delete" (remote sources, dest ""). The hub answers at once and walks in
// the background; files.planned, then files.done, follow.
func (h *Client) FilesPlan(ctx context.Context, id, server, op string, sources []string, dest string) error {
	p := struct {
		ID      string   `json:"id"`
		Server  string   `json:"server"`
		Op      string   `json:"op"`
		Sources []string `json:"sources"`
		Dest    string   `json:"dest,omitempty"`
	}{id, server, op, sources, dest}
	return h.c.Call(ctx, "files.plan", p, nil)
}

// FilesRun runs a planned job; conflict is "overwrite" or "skip" (the hub
// takes nothing else, and a delete needs one too).
func (h *Client) FilesRun(ctx context.Context, id, conflict string) error {
	return h.c.Call(ctx, "files.run", map[string]string{"id": id, "conflict": conflict}, nil)
}

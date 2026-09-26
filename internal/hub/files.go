package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"strconv"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/files"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshx"
)

var errBadParams = &rpc.Error{Code: -32602, Message: "invalid params"}

// strictParams decodes a JSON object into v after checking its top-level
// keys: each must be one of allowed, spelled exactly, and appear once.
// encoding/json alone would match "Sources" to "sources" and let the last
// duplicate win, which would let a renderer slip a key past Electron main.
func strictParams(raw json.RawMessage, v any, allowed ...string) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errBadParams
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		k, ok := tok.(string)
		if err != nil || !ok {
			return errBadParams
		}
		if !slices.Contains(allowed, k) || seen[k] {
			return &rpc.Error{Code: -32602, Message: "invalid params: unexpected key " + strconv.Quote(k)}
		}
		seen[k] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return errBadParams
		}
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return errBadParams
	}
	return nil
}

// fileErr keeps the app's message short for the common cases.
func fileErr(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return errors.New("permission denied")
	}
	return err
}

// sftpFor resolves server as term.open does (unlocked vault, known server)
// and returns its manager, with keepalive on.
func (h *Hub) sftpFor(server string) (*sshx.Manager, sshx.DialConfig, error) {
	_ = h.Reload()
	dc, err := h.resolveForTerm(server)
	if err != nil {
		return nil, dc, err
	}
	mgr := h.Registry().Get(server, dc)
	mgr.StartKeepalive(30*time.Second, nil)
	return mgr, dc, nil
}

// pinnedClient is sftpFor plus the shared client, for methods that need a
// pin already (everything but files.list).
func (h *Hub) pinnedClient(server string) (*sshx.Manager, sshx.DialConfig, error) {
	mgr, dc, err := h.sftpFor(server)
	if err != nil {
		return nil, dc, err
	}
	if dc.HostKey == "" {
		return nil, dc, errors.New("trust the host key first: open the Files tab")
	}
	return mgr, dc, nil
}

func first20(s []string) []string { return s[:min(len(s), 20)] }

// auditFile never takes h.mu.
func (h *Hub) auditFile(r broker.FileRecord) {
	if h.audit != nil {
		r.Time = time.Now()
		r.Remote, r.Local = first20(r.Remote), first20(r.Local)
		_ = h.audit.WriteFile(r)
	}
}

// listTimeout bounds one listing (see files.List's ponytail note).
var listTimeout = 30 * time.Second

// registerFileMethods adds the files.* methods for one UI door and returns
// a func that cancels its jobs and waits for them (Task 8 adds the jobs).
func registerFileMethods(s *rpc.Server, h *Hub) (closeAll func()) {
	empty := map[string]any{}
	s.HandleRequest("files.list", func(ctx context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			Server       string `json:"server"`
			Path         string `json:"path"`
			TrustHostKey *struct {
				Fingerprint string `json:"fingerprint"`
				KeyType     string `json:"keyType"`
			} `json:"trustHostKey"`
		}
		if err := strictParams(raw, &p, "server", "path", "trustHostKey"); err != nil {
			return nil, err
		}
		if tk := p.TrustHostKey; tk != nil && (tk.Fingerprint == "" || tk.KeyType == "") {
			return nil, &rpc.Error{Code: -32602, Message: "trustHostKey needs fingerprint and keyType"}
		}
		_ = h.Reload()
		dc, err := h.resolveForTerm(p.Server)
		if err != nil {
			return nil, err
		}
		trust := p.TrustHostKey != nil && dc.HostKey == ""
		if trust {
			dc.HostKey, dc.HostKeyAlgo = p.TrustHostKey.Fingerprint, p.TrustHostKey.KeyType
		}
		mgr := h.Registry().Get(p.Server, dc)
		mgr.StartKeepalive(30*time.Second, nil)
		c, err := mgr.SFTP()
		if err != nil {
			if res := h.hostKeyResult(p.Server, dc, trust, err); res != nil {
				return res, nil
			}
			return nil, err
		}
		if trust {
			if err := h.recordTrust(p.Server, dc); err != nil {
				h.Registry().Close(p.Server)
				return nil, err
			}
		}
		ctx, cancel := context.WithTimeout(ctx, listTimeout)
		defer cancel()
		l, err := files.List(ctx, c, p.Path)
		if err != nil {
			return nil, fileErr(err)
		}
		return map[string]any{"status": "listed", "path": l.Path, "entries": l.Entries, "truncated": l.Truncated, "bad": l.Bad}, nil
	})
	s.HandleRequest("files.mkdir", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			Server string `json:"server"`
			Path   string `json:"path"`
		}
		if err := strictParams(raw, &p, "server", "path"); err != nil {
			return nil, err
		}
		if err := files.CheckAbs(p.Path); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: err.Error()}
		}
		mgr, dc, err := h.pinnedClient(p.Server)
		if err != nil {
			return nil, err
		}
		c, err := mgr.SFTP()
		if err == nil {
			err = files.Mkdir(c, p.Path)
		}
		rec := broker.FileRecord{Action: "mkdir", Server: p.Server, Host: dc.Host, Port: dc.Port, Remote: []string{p.Path}}
		if err != nil {
			rec.Reason = err.Error()
		}
		h.auditFile(rec)
		if err != nil {
			return nil, fileErr(err)
		}
		return empty, nil
	})
	s.HandleRequest("files.rename", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			Server string `json:"server"`
			From   string `json:"from"`
			To     string `json:"to"`
		}
		if err := strictParams(raw, &p, "server", "from", "to"); err != nil {
			return nil, err
		}
		mgr, dc, err := h.pinnedClient(p.Server)
		if err != nil {
			return nil, err
		}
		c, err := mgr.SFTP()
		if err == nil {
			err = files.Rename(c, p.From, p.To)
		}
		rec := broker.FileRecord{Action: "rename", Server: p.Server, Host: dc.Host, Port: dc.Port, From: p.From, To: p.To}
		if err != nil {
			rec.Reason = err.Error()
		}
		h.auditFile(rec)
		if err != nil {
			return nil, fileErr(err)
		}
		return empty, nil
	})
	return func() {}
}

package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx"
	"golang.org/x/crypto/ssh"
)

var termIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

const maxTermDim = 1000

func validDims(rows, cols int) bool {
	return rows > 0 && cols > 0 && rows <= maxTermDim && cols <= maxTermDim
}

// termEntry is one terminal's slot in a door's map. It is inserted before
// the session starts (t is nil while opening); pointer identity tells a
// session's callbacks whether they still own the id.
type termEntry struct {
	t *sshx.TermSession
}

// registerTermMethods adds the term.* methods for one UI door connection and
// returns a func that closes every terminal opened through it.
//
// term.open and term.close are requests; the client chooses the id, so
// term.data may arrive before the term.open reply. term.write, term.resize
// and term.ack are sent as notifications so keystrokes apply in typing order;
// they run on the rpc read loop and must not block, so they only enqueue or
// adjust counters. An unknown id there is ignored: there is nobody to reply
// to. Data is base64 in JSON ([]byte fields).
func registerTermMethods(s *rpc.Server, h *Hub) (closeAll func() int) {
	var mu sync.Mutex
	terms := map[string]*termEntry{}
	get := func(id string) (*sshx.TermSession, error) {
		mu.Lock()
		defer mu.Unlock()
		e, ok := terms[id]
		if !ok || e.t == nil {
			return nil, fmt.Errorf("no terminal %q", id)
		}
		return e.t, nil
	}
	owns := func(id string, e *termEntry) bool {
		mu.Lock()
		defer mu.Unlock()
		return terms[id] == e
	}
	badNote := func(method string, err error) {
		fmt.Fprintf(os.Stderr, "%s: malformed notification: %v\n", method, err)
	}
	empty := map[string]any{}

	s.HandleRequest("term.open", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			ID           string `json:"id"`
			Server       string `json:"server"`
			Rows         int    `json:"rows"`
			Cols         int    `json:"cols"`
			TrustHostKey *struct {
				Fingerprint string `json:"fingerprint"`
				KeyType     string `json:"keyType"`
			} `json:"trustHostKey"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
		}
		if !termIDRe.MatchString(p.ID) {
			return nil, &rpc.Error{Code: -32602, Message: "id must be 1-64 characters of A-Z a-z 0-9 _ -"}
		}
		if !validDims(p.Rows, p.Cols) {
			return nil, &rpc.Error{Code: -32602, Message: fmt.Sprintf("rows and cols must be 1-%d", maxTermDim)}
		}
		if tk := p.TrustHostKey; tk != nil && (tk.Fingerprint == "" || tk.KeyType == "") {
			return nil, &rpc.Error{Code: -32602, Message: "trustHostKey needs fingerprint and keyType"}
		}
		e := &termEntry{}
		mu.Lock()
		_, dup := terms[p.ID]
		if !dup {
			terms[p.ID] = e
		}
		mu.Unlock()
		if dup {
			return nil, fmt.Errorf("terminal %q already open", p.ID)
		}
		release := func() {
			mu.Lock()
			if terms[p.ID] == e {
				delete(terms, p.ID)
			}
			mu.Unlock()
		}

		_ = h.Reload()
		dc, err := h.resolveForTerm(p.Server)
		if err != nil {
			release()
			return nil, err
		}
		// Trust applies only while the server has no pin; a pin set in the
		// meantime governs instead. The retry dials pinned to exactly the key
		// the user confirmed (a new config hash, so a fresh manager).
		trust := p.TrustHostKey != nil && dc.HostKey == ""
		if trust {
			dc.HostKey, dc.HostKeyAlgo = p.TrustHostKey.Fingerprint, p.TrustHostKey.KeyType
		}
		mgr := h.Registry().Get(p.Server, dc)
		mgr.StartKeepalive(30*time.Second, nil)
		t, err := mgr.OpenTerm(p.Rows, p.Cols,
			func(b []byte) {
				if owns(p.ID, e) {
					s.Notify("term.data", map[string]any{"id": p.ID, "data": b})
				}
			},
			func(code int, reason string) {
				// Only a terminal that exited on its own reports it; after
				// term.close the id may already belong to a new terminal.
				mu.Lock()
				owned := terms[p.ID] == e
				if owned {
					delete(terms, p.ID)
				}
				mu.Unlock()
				if owned {
					s.Notify("term.exit", map[string]any{"id": p.ID, "code": code, "reason": reason})
				}
			})
		if err != nil {
			release()
			if res := h.hostKeyResult(p.Server, dc, trust, err); res != nil {
				return res, nil
			}
			return nil, err
		}
		if trust {
			if err := h.recordTrust(p.Server, dc); err != nil {
				release() // first, so the close below sends no term.exit
				t.Close()
				h.Registry().Close(p.Server)
				return nil, err
			}
		}
		mu.Lock()
		gone := terms[p.ID] != e // closed (or exited) while opening
		if !gone {
			e.t = t
		}
		mu.Unlock()
		if gone {
			t.Close()
		}
		return map[string]string{"status": "open", "id": p.ID}, nil
	})
	s.Handle("term.write", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID   string `json:"id"`
			Data []byte `json:"data"`
			User bool   `json:"user"` // sent right after real input; xterm's own query replies are not
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			badNote("term.write", err)
			return nil, err
		}
		if p.User {
			h.touch()
		}
		t, err := get(p.ID)
		if err != nil {
			return nil, err
		}
		if err := t.Write(p.Data); err != nil {
			fmt.Fprintf(os.Stderr, "term.write %s: %v (%d bytes)\n", p.ID, err, len(p.Data))
			if errors.Is(err, sshx.ErrTermQueueFull) {
				s.Notify("term.dropped", map[string]any{"id": p.ID, "bytes": len(p.Data)})
			}
			return nil, err
		}
		return empty, nil
	})
	s.Handle("term.resize", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID   string `json:"id"`
			Rows int    `json:"rows"`
			Cols int    `json:"cols"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			badNote("term.resize", err)
			return nil, err
		}
		if !validDims(p.Rows, p.Cols) {
			err := fmt.Errorf("rows and cols must be 1-%d, got %dx%d", maxTermDim, p.Rows, p.Cols)
			badNote("term.resize", err)
			return nil, err
		}
		t, err := get(p.ID)
		if err != nil {
			return nil, err
		}
		if err := t.Resize(p.Rows, p.Cols); err != nil {
			fmt.Fprintf(os.Stderr, "term.resize %s: %v\n", p.ID, err)
			return nil, err
		}
		return empty, nil
	})
	s.Handle("term.ack", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID string `json:"id"`
			N  int    `json:"n"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			badNote("term.ack", err)
			return nil, err
		}
		t, err := get(p.ID)
		if err != nil {
			return nil, err
		}
		t.Ack(p.N)
		return empty, nil
	})
	s.HandleRequest("term.close", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			ID string `json:"id"`
		}
		json.Unmarshal(raw, &p)
		var t *sshx.TermSession
		mu.Lock()
		e, ok := terms[p.ID]
		if ok {
			t = e.t
			delete(terms, p.ID)
		}
		mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("no terminal %q", p.ID)
		}
		if t != nil { // nil while opening: term.open sees the slot gone and closes it
			t.Close()
		}
		return empty, nil
	})

	// closeAll counts terminals still opening too: term.open sees its slot
	// gone and closes the session itself.
	closeAll = func() int {
		var open []*sshx.TermSession
		mu.Lock()
		n := len(terms)
		for _, e := range terms {
			if e.t != nil {
				open = append(open, e.t)
			}
		}
		terms = map[string]*termEntry{}
		mu.Unlock()
		for _, t := range open {
			t.Close()
		}
		return n
	}
	// term.closeAll is for Electron main after a renderer crash; the
	// renderer's own whitelist does not include it.
	s.HandleRequest("term.closeAll", func(context.Context, json.RawMessage) (any, error) {
		return map[string]int{"closed": closeAll()}, nil
	})
	return closeAll
}

// hostKeyResult turns a host-key refusal into term.open's result; nil for
// any other error. Nothing was opened or recorded on these paths. After a
// trusted retry a different key is asked about again, never reported as a
// mismatch: the user never confirmed that pin.
func (h *Hub) hostKeyResult(name string, dc sshx.DialConfig, trusting bool, err error) map[string]any {
	var unknown *sshx.HostKeyUnknownError
	var mismatch *sshx.HostKeyMismatchError
	switch {
	case errors.As(err, &unknown):
		return h.unknownResult(name, dc, unknown.Fingerprint, unknown.KeyType, unknown.Key)
	case errors.As(err, &mismatch) && trusting:
		h.reg.Close(name)
		return h.unknownResult(name, dc, mismatch.Presented, mismatch.KeyType, mismatch.Key)
	case errors.As(err, &mismatch):
		return map[string]any{"status": "hostKeyMismatch", "server": name, "host": dc.Host, "port": dc.Port, "user": dc.User,
			"pinned": mismatch.Pinned, "presented": mismatch.Presented}
	}
	return nil
}

func (h *Hub) unknownResult(name string, dc sshx.DialConfig, fp, keyType string, key ssh.PublicKey) map[string]any {
	return map[string]any{"status": "hostKeyUnknown", "server": name, "host": dc.Host, "port": dc.Port, "user": dc.User,
		"fingerprint": fp, "keyType": keyType, "knownHosts": sshx.KnownHostsHint(h.knownHostsPath(), dc.Host, dc.Port, key)}
}

func (h *Hub) knownHostsPath() string {
	if h.o.KnownHostsPath != "" {
		return h.o.KnownHostsPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "known_hosts")
}

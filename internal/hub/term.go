package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

// registerTermMethods adds the term.* methods for one UI door connection and
// returns a func that closes every terminal opened through it.
//
// term.open and term.close are requests. term.write, term.resize and term.ack
// are sent as notifications so keystrokes apply in typing order; they run on
// the rpc read loop and must not block, so they only enqueue or adjust
// counters. An unknown id there is ignored: there is nobody to reply to.
// Data is base64 in JSON ([]byte fields).
func registerTermMethods(s *rpc.Server, h *Hub) (closeAll func()) {
	var mu sync.Mutex
	terms := map[string]*sshx.TermSession{}
	get := func(id string) (*sshx.TermSession, error) {
		mu.Lock()
		defer mu.Unlock()
		t, ok := terms[id]
		if !ok {
			return nil, fmt.Errorf("no terminal %q", id)
		}
		return t, nil
	}
	empty := map[string]any{}

	s.HandleRequest("term.open", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Server string `json:"server"`
			Rows   int    `json:"rows"`
			Cols   int    `json:"cols"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
		}
		_ = h.Reload()
		dc, err := h.Resolve(p.Server)
		if err != nil {
			return nil, err
		}
		mgr := h.Registry().Get(p.Server, dc)
		mgr.StartKeepalive(30*time.Second, nil)
		idb := make([]byte, 16)
		rand.Read(idb)
		id := hex.EncodeToString(idb)
		// A shell that exits at once may fire onExit before the id is in
		// the map; ready holds its delete until the insert below.
		ready := make(chan struct{})
		t, err := mgr.OpenTerm(p.Rows, p.Cols,
			func(b []byte) {
				s.Notify("term.data", map[string]any{"id": id, "data": b})
			},
			func(code int, reason string) {
				<-ready
				mu.Lock()
				delete(terms, id)
				mu.Unlock()
				s.Notify("term.exit", map[string]any{"id": id, "code": code, "reason": reason})
			})
		if err != nil {
			return nil, err
		}
		mu.Lock()
		terms[id] = t
		mu.Unlock()
		close(ready)
		return map[string]string{"id": id}, nil
	})
	s.Handle("term.write", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID   string `json:"id"`
			Data []byte `json:"data"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		t, err := get(p.ID)
		if err != nil {
			return nil, err
		}
		if err := t.Write(p.Data); err != nil {
			fmt.Fprintln(os.Stderr, "term.write:", err)
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
			return nil, err
		}
		t, err := get(p.ID)
		if err != nil {
			return nil, err
		}
		if err := t.Resize(p.Rows, p.Cols); err != nil {
			fmt.Fprintln(os.Stderr, "term.resize:", err)
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
		var p struct {
			ID string `json:"id"`
		}
		json.Unmarshal(raw, &p)
		mu.Lock()
		t, ok := terms[p.ID]
		delete(terms, p.ID)
		mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("no terminal %q", p.ID)
		}
		t.Close()
		return empty, nil
	})

	return func() {
		mu.Lock()
		all := terms
		terms = map[string]*sshx.TermSession{}
		mu.Unlock()
		for _, t := range all {
			t.Close()
		}
	}
}

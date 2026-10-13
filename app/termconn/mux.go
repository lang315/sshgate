// Package termconn connects a mygo terminal to a hub terminal over the UI
// door's term.* methods.
package termconn

import (
	"encoding/json"
	"strings"
	"sync"
)

// Mux routes the hub's term.* notifications to their Conn by id.
type Mux struct {
	mu    sync.Mutex
	conns map[string]*Conn
}

func NewMux() *Mux { return &Mux{conns: map[string]*Conn{}} }

func (m *Mux) add(c *Conn)      { m.mu.Lock(); m.conns[c.id] = c; m.mu.Unlock() }
func (m *Mux) remove(id string) { m.mu.Lock(); delete(m.conns, id); m.mu.Unlock() }

// Note handles a term.* notification and reports whether it was one.
// Data for an id no Conn has (a tab already closed) is dropped. It runs on
// the door's read goroutine and never blocks.
func (m *Mux) Note(method string, params json.RawMessage) bool {
	if !strings.HasPrefix(method, "term.") {
		return false
	}
	var p struct {
		ID     string `json:"id"`
		Data   []byte `json:"data"`
		Bytes  int    `json:"bytes"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(params, &p) != nil {
		return true
	}
	m.mu.Lock()
	c := m.conns[p.ID]
	m.mu.Unlock()
	if c == nil {
		return true
	}
	switch method {
	case "term.data":
		c.push(p.Data)
	case "term.exit":
		c.exit(p.Reason)
		m.remove(p.ID)
	case "term.dropped":
		c.dropped(p.Bytes)
	}
	return true
}

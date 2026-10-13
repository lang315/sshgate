package termconn

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// UserWindow is how long after a real key, paste or input-method commit a
// write still counts as the user's, for the hub's idle lock (Electron's
// USER_INPUT_WINDOW_MS).
const UserWindow = time.Second

const maxDim = 1000 // internal/hub/term.go maxTermDim

// Door is the part of the UI door client a Conn uses.
type Door interface {
	Call(ctx context.Context, method string, params, result any) error
	Notify(method string, params any) error
}

// OpenError is a term.open the hub answered with another status than
// "open", as hostKeyUnknown or hostKeyMismatch.
type OpenError struct {
	Status string
	Result json.RawMessage
}

func (e *OpenError) Error() string { return "terminal not opened: " + e.Status }

// Conn is a hub terminal as an io.ReadWriteCloser for terminal.Options.Conn.
// Read returns what the hub sent and acks it, so the hub's reader stalls at
// its high-water mark while the terminal parses: that is the backpressure.
type Conn struct {
	d   Door
	m   *Mux
	id  string
	now func() time.Time

	mu       sync.Mutex
	queue    [][]byte
	buf      []byte
	ended    bool
	closed   bool
	reason   string
	drop     int
	lastUser time.Time
	ready    chan struct{} // a token when queue or ended changed
}

// Open asks the hub for a terminal on server and registers it with m.
func Open(ctx context.Context, d Door, m *Mux, id, server string, rows, cols int) (*Conn, error) {
	c := &Conn{d: d, m: m, id: id, now: time.Now, ready: make(chan struct{}, 1)}
	m.add(c) // before term.open: data may arrive before its reply
	var res json.RawMessage
	err := d.Call(ctx, "term.open", map[string]any{"id": id, "server": server, "rows": clamp(rows), "cols": clamp(cols)}, &res)
	if err != nil {
		m.remove(id)
		return nil, err
	}
	var st struct{ Status string }
	json.Unmarshal(res, &st)
	if st.Status != "open" {
		m.remove(id)
		return nil, &OpenError{Status: st.Status, Result: res}
	}
	return c, nil
}

func clamp(n int) int { return max(1, min(n, maxDim)) }

func (c *Conn) signal() {
	select {
	case c.ready <- struct{}{}:
	default:
	}
}

func (c *Conn) push(b []byte) {
	c.mu.Lock()
	if !c.ended {
		c.queue = append(c.queue, b)
	}
	c.mu.Unlock()
	c.signal()
}

func (c *Conn) exit(reason string) {
	c.mu.Lock()
	c.ended, c.reason = true, reason
	c.mu.Unlock()
	c.signal()
}

func (c *Conn) dropped(n int) { c.mu.Lock(); c.drop += n; c.mu.Unlock() }

// Dropped is how many bytes of input the hub dropped (its queue was full).
func (c *Conn) Dropped() int { c.mu.Lock(); defer c.mu.Unlock(); return c.drop }

// ExitReason is the reason term.exit gave, empty before it.
func (c *Conn) ExitReason() string { c.mu.Lock(); defer c.mu.Unlock(); return c.reason }

func (c *Conn) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if len(c.buf) == 0 && len(c.queue) > 0 {
			c.buf, c.queue = c.queue[0], c.queue[1:]
		}
		if len(c.buf) > 0 {
			n := copy(p, c.buf)
			c.buf = c.buf[n:]
			c.mu.Unlock()
			c.d.Notify("term.ack", map[string]any{"id": c.id, "n": n})
			return n, nil
		}
		if c.ended {
			c.mu.Unlock()
			return 0, io.EOF
		}
		c.mu.Unlock()
		<-c.ready
	}
}

// MarkUser records a real key, paste or input-method commit: writes in
// the next UserWindow count as the user's.
func (c *Conn) MarkUser() { c.mu.Lock(); c.lastUser = c.now(); c.mu.Unlock() }

// Write sends to the hub. It never fails while the tab is open: mygo's
// terminal closes its input for good on a write error. A full hub queue
// comes back as term.dropped instead.
func (c *Conn) Write(p []byte) (int, error) {
	c.mu.Lock()
	closed := c.closed
	user := !c.lastUser.IsZero() && c.now().Sub(c.lastUser) < UserWindow
	c.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	// An error means the hub is gone; the supervisor reports that.
	_ = c.d.Notify("term.write", map[string]any{"id": c.id, "data": p, "user": user})
	return len(p), nil
}

// Resize is mygo's (cols, rows); the hub takes rows and cols, 1..1000.
func (c *Conn) Resize(cols, rows int) error {
	c.d.Notify("term.resize", map[string]any{"id": c.id, "rows": clamp(rows), "cols": clamp(cols)})
	return nil
}

// Close ends the hub terminal once.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed, c.ended = true, true
	c.mu.Unlock()
	c.m.remove(c.id)
	c.signal()
	c.d.Notify("term.close", map[string]any{"id": c.id})
	return nil
}

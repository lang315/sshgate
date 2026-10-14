package termconn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/lang315/sshgate/app/door"
	"github.com/lang315/sshgate/app/hubapi"
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
// "open": hostKeyUnknown or hostKeyMismatch, with the hub's details in Key.
type OpenError struct {
	Status string
	Key    hubapi.HostKey
}

func (e *OpenError) Error() string { return "terminal not opened: " + e.Status }

// chunk is output for the terminal: from the hub (acked once read), or a
// line the app writes itself (never acked: the hub did not send it).
type chunk struct {
	b     []byte
	local bool
}

// Conn is a hub terminal as an io.ReadWriteCloser for terminal.Options.Conn.
// Read returns what the hub sent and acks it, so the hub's reader stalls at
// its high-water mark while the terminal parses: that is the backpressure.
type Conn struct {
	d   Door
	m   *Mux
	id  string
	now func() time.Time
	out chan struct{} // closed at the hub's first output

	mu        sync.Mutex
	queue     []chunk
	buf       []byte
	bufLocal  bool
	ended     bool
	closed    bool
	sawOutput bool
	reason    string
	drop      int
	lastUser  time.Time
	ready     chan struct{} // a token when queue or ended changed
}

// Open asks the hub for a terminal on server and registers it with m.
// trust is nil, or the key the person accepted on a hostKeyUnknown answer,
// sent with the same id (the hub released it).
func Open(ctx context.Context, d Door, m *Mux, id, server string, rows, cols int, trust *hubapi.Trust) (*Conn, error) {
	c := &Conn{d: d, m: m, id: id, now: time.Now, out: make(chan struct{}), ready: make(chan struct{}, 1)}
	if !m.add(c) { // before term.open: data may arrive before its reply
		return nil, errors.New("terminal id already in use")
	}
	p := map[string]any{"id": id, "server": server, "rows": clamp(rows), "cols": clamp(cols)}
	if trust != nil {
		p["trustHostKey"] = trust
	}
	var res json.RawMessage
	err := d.Call(ctx, "term.open", p, &res)
	if err != nil {
		m.remove(id)
		var he *door.Error
		if !errors.As(err, &he) {
			// No hub answer (ctx ended, door closed): the hub may still have opened it.
			c.hubClose()
		}
		return nil, err
	}
	var k hubapi.HostKey
	if json.Unmarshal(res, &k) != nil || k.Status != "open" && k.Status != "hostKeyUnknown" && k.Status != "hostKeyMismatch" {
		m.remove(id)
		return nil, errors.New("terminal not opened: unexpected reply")
	}
	if k.Status != "open" {
		m.remove(id)
		return nil, &OpenError{Status: k.Status, Key: k}
	}
	return c, nil
}

func clamp(n int) int { return max(1, min(n, maxDim)) }

// printable drops C0, DEL and C1 controls, so a reason the hub relays from
// the remote cannot drive the terminal it is written into.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r >= 0x7f && r <= 0x9f {
			return -1
		}
		return r
	}, s)
}

func (c *Conn) signal() {
	select {
	case c.ready <- struct{}{}:
	default:
	}
}

func (c *Conn) push(b []byte) {
	c.mu.Lock()
	if !c.ended {
		c.queue = append(c.queue, chunk{b: b})
		if !c.sawOutput {
			c.sawOutput = true
			close(c.out)
		}
	}
	c.mu.Unlock()
	c.signal()
}

// Output is closed once the hub sent the first output: the remote is up.
func (c *Conn) Output() <-chan struct{} { return c.out }

// exit ends the terminal once, after what the hub sent, with the reason
// written into it as the Electron app writes it.
func (c *Conn) exit(reason string) {
	c.mu.Lock()
	if !c.ended {
		c.ended, c.reason = true, reason
		c.queue = append(c.queue, chunk{b: []byte("\r\n[exited: " + printable(reason) + "]\r\n"), local: true})
	}
	c.mu.Unlock()
	c.signal()
}

// End ends the terminal without asking the hub, as when the hub stopped.
func (c *Conn) End(reason string) {
	c.exit(reason)
	c.m.remove(c.id)
}

func (c *Conn) dropped(n int) {
	c.mu.Lock()
	c.drop += n
	if !c.ended {
		c.queue = append(c.queue, chunk{b: fmt.Appendf(nil, "\r\n[input dropped: %d bytes]\r\n", n), local: true})
	}
	c.mu.Unlock()
	c.signal()
}

// Dropped is how many bytes of input the hub dropped (its queue was full).
func (c *Conn) Dropped() int { c.mu.Lock(); defer c.mu.Unlock(); return c.drop }

// ExitReason is the reason the terminal ended with, empty before it.
func (c *Conn) ExitReason() string { c.mu.Lock(); defer c.mu.Unlock(); return c.reason }

func (c *Conn) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if len(c.buf) == 0 && len(c.queue) > 0 {
			c.buf, c.bufLocal, c.queue = c.queue[0].b, c.queue[0].local, c.queue[1:]
		}
		if len(c.buf) > 0 {
			n := copy(p, c.buf)
			c.buf = c.buf[n:]
			local := c.bufLocal
			c.mu.Unlock()
			if !local {
				c.d.Notify("term.ack", map[string]any{"id": c.id, "n": n})
			}
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

// Close ends the hub terminal once. One that already ended (term.exit, or
// End) is gone from the hub, so nothing is sent.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	ended := c.ended
	c.closed, c.ended = true, true
	c.mu.Unlock()
	c.m.remove(c.id)
	c.signal()
	if !ended {
		c.hubClose()
	}
	return nil
}

// hubClose ends the hub terminal. term.close is request-only on the hub, so
// a notification would be ignored. Best effort, never under c.mu.
func (c *Conn) hubClose() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.d.Call(ctx, "term.close", map[string]any{"id": c.id}, nil)
}

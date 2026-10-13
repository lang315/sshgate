// Package door is a client for sshgate hub's UI door: newline-delimited
// JSON-RPC 2.0 over the hub's stdio (protocol 12).
package door

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// Error is a refusal from the hub: it answered, and said no.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// ErrClosed is returned for calls the hub can no longer answer.
var ErrClosed = errors.New("hub connection closed")

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type reply struct {
	result json.RawMessage
	err    error
}

type Client struct {
	wmu sync.Mutex
	w   io.Writer

	mu      sync.Mutex
	next    int64
	pending map[int64]chan reply
	closed  bool

	onNote func(string, json.RawMessage)
	done   chan struct{}
}

// New starts reading r. onNote, when set, runs on the read goroutine for
// each notification, in the order the hub sent them, and must not block.
func New(r io.Reader, w io.Writer, onNote func(method string, params json.RawMessage)) *Client {
	c := &Client{w: w, pending: map[int64]chan reply{}, onNote: onNote, done: make(chan struct{})}
	go c.read(r)
	return c
}

// Done is closed when the read loop ends.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) read(r io.Reader) {
	defer c.closeAll()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.ID == nil {
			if m.Method != "" && c.onNote != nil {
				c.onNote(m.Method, m.Params)
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if m.Error != nil {
			ch <- reply{err: m.Error}
		} else {
			ch <- reply{result: m.Result}
		}
	}
}

func (c *Client) closeAll() {
	c.mu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		ch <- reply{err: ErrClosed}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.done)
}

func (c *Client) write(m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		return ErrClosed
	}
	return nil
}

func params(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// Call sends a request and waits for its answer, ctx, or the hub's end.
func (c *Client) Call(ctx context.Context, method string, p, result any) error {
	raw, err := params(p)
	if err != nil {
		return err
	}
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(message{JSONRPC: "2.0", ID: &id, Method: method, Params: raw}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if result == nil || len(r.result) == 0 {
			return nil
		}
		return json.Unmarshal(r.result, result)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Notify sends a notification; the hub answers none.
func (c *Client) Notify(method string, p any) error {
	raw, err := params(p)
	if err != nil {
		return err
	}
	return c.write(message{JSONRPC: "2.0", Method: method, Params: raw})
}

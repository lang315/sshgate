package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
)

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Server is a newline-delimited JSON-RPC 2.0 server. A Server serves exactly
// one connection: call Serve once; a second call returns an error.
type Server struct {
	mu          sync.Mutex // guards handlers and serving; independent of writeMu
	handlers    map[string]Handler
	requestOnly map[string]bool // methods registered via HandleRequest
	serving     bool

	writeMu sync.Mutex // guards w; separate from mu so a slow peer never blocks inbound dispatch
	w       io.Writer
}

func NewServer() *Server {
	return &Server{handlers: map[string]Handler{}, requestOnly: map[string]bool{}}
}

// Handle registers h for method. Handlers for requests (which carry an id)
// run concurrently, one goroutine per request. Handlers for notifications
// (no id) run synchronously on Serve's read loop, in the order the
// notifications were received, so inbound ordering is preserved (e.g.
// terminal keystrokes). A notification handler must therefore not block, or
// it stalls all further reads on the connection, including Serve's own
// reaction to ctx cancellation.
func (s *Server) Handle(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
	delete(s.requestOnly, method)
}

// HandleRequest registers h for method as request-only: it runs only for
// calls that carry an id. A notification to method is ignored, so a
// long-running handler can never block Serve's read loop.
func (s *Server) HandleRequest(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
	s.requestOnly[method] = true
}

// Methods lists every registered method, sorted. Tests use it to prove a
// table covers the whole door.
func (s *Server) Methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.handlers))
	for m := range s.handlers {
		out = append(out, m)
	}
	slices.Sort(out)
	return out
}

func (s *Server) write(m message) error {
	m.JSONRPC = "2.0"
	line, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.w == nil {
		return fmt.Errorf("rpc: not serving")
	}
	_, err = s.w.Write(append(line, '\n'))
	return err
}

// Notify sends a notification to the peer. It is safe to call from any
// goroutine once Serve has started, and returns an error if Serve has not
// started yet or has already returned.
func (s *Server) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return s.write(message{Method: method, Params: raw})
}

// Serve reads newline-delimited JSON-RPC messages from r and writes
// responses and notifications to w. It returns when r hits EOF, r errors, or
// ctx is done. On return, every in-flight request handler has finished and
// nothing further will be written to w.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.mu.Lock()
	if s.serving {
		s.mu.Unlock()
		return fmt.Errorf("rpc: Serve called twice; a Server serves exactly one connection")
	}
	s.serving = true
	s.mu.Unlock()

	s.writeMu.Lock()
	s.w = w
	s.writeMu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Read lines on a separate goroutine so Serve can select on ctx.Done()
	// even while a Read is blocked.
	lines := make(chan []byte)
	scanDone := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...) // sc.Bytes() is reused on next Scan
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		scanDone <- sc.Err()
	}()

	var wg sync.WaitGroup
	var retErr error
readLoop:
	for {
		select {
		case <-ctx.Done():
			retErr = ctx.Err()
			break readLoop
		case retErr = <-scanDone:
			break readLoop
		case line := <-lines:
			var m message
			if err := json.Unmarshal(line, &m); err != nil || m.Method == "" {
				continue // ignore garbage and responses; the server never calls out
			}
			s.mu.Lock()
			h := s.handlers[m.Method]
			reqOnly := s.requestOnly[m.Method]
			s.mu.Unlock()
			if m.ID == nil { // notification: dispatch inline to preserve inbound order
				if h != nil && !reqOnly {
					h(ctx, m.Params)
				}
				continue
			}
			wg.Add(1)
			go func(m message) {
				defer wg.Done()
				s.handleRequest(ctx, h, m)
			}(m)
		}
	}

	cancel()  // stop the scanning goroutine and unblock any handler waiting on ctx
	wg.Wait() // no request handler is writing to w past this point

	s.writeMu.Lock()
	s.w = nil // Notify after Serve returns fails closed instead of writing to a dead connection
	s.writeMu.Unlock()

	return retErr
}

func (s *Server) handleRequest(ctx context.Context, h Handler, m message) {
	if h == nil {
		s.write(message{ID: m.ID, Error: &Error{Code: -32601, Message: "method not found: " + m.Method}})
		return
	}
	res, err := h(ctx, m.Params)
	if err != nil {
		re, ok := err.(*Error)
		if !ok {
			re = &Error{Code: -32000, Message: err.Error()}
		}
		s.write(message{ID: m.ID, Error: re})
		return
	}
	raw, err := json.Marshal(res)
	if err != nil {
		s.write(message{ID: m.ID, Error: &Error{Code: -32000, Message: err.Error()}})
		return
	}
	s.write(message{ID: m.ID, Result: raw})
}

type Client struct {
	w        io.Writer
	wmu      sync.Mutex
	next     atomic.Int64
	mu       sync.Mutex
	waiting  map[int64]chan message
	onNotify func(string, json.RawMessage)
	closed   chan struct{}
	rCloser  io.Closer
	wCloser  io.Closer
}

// NewClient starts reading newline-delimited JSON-RPC messages from r and
// writes calls to w. If r and/or w implement io.Closer, Close closes them.
func NewClient(r io.Reader, w io.Writer, onNotify func(method string, params json.RawMessage)) *Client {
	c := &Client{w: w, waiting: map[int64]chan message{}, onNotify: onNotify, closed: make(chan struct{})}
	if rc, ok := r.(io.Closer); ok {
		c.rCloser = rc
	}
	if wc, ok := w.(io.Closer); ok {
		c.wCloser = wc
	}
	go c.readLoop(r)
	return c
}

func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		if m.ID == nil {
			if c.onNotify != nil && m.Method != "" {
				c.onNotify(m.Method, m.Params)
			}
			continue
		}
		c.mu.Lock()
		ch := c.waiting[*m.ID]
		delete(c.waiting, *m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
	close(c.closed)
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	id := c.next.Add(1)
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan message, 1)
	c.mu.Lock()
	c.waiting[id] = ch
	c.mu.Unlock()
	line, _ := json.Marshal(message{JSONRPC: "2.0", ID: &id, Method: method, Params: raw})
	c.wmu.Lock()
	_, err = c.w.Write(append(line, '\n'))
	c.wmu.Unlock()
	if err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if result != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.closed:
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
		return io.ErrUnexpectedEOF
	}
}

// Close closes the underlying reader and writer, if they implement
// io.Closer. Closing the reader unblocks readLoop, which then unblocks any
// Call waiting on a response.
func (c *Client) Close() error {
	var err error
	if c.rCloser != nil {
		err = c.rCloser.Close()
	}
	if c.wCloser != nil && c.wCloser != c.rCloser {
		if e := c.wCloser.Close(); err == nil {
			err = e
		}
	}
	return err
}

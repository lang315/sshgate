package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type Server struct {
	mu       sync.Mutex // guards handlers before Serve, and writes after
	handlers map[string]Handler
	w        io.Writer
}

func NewServer() *Server { return &Server{handlers: map[string]Handler{}} }

func (s *Server) Handle(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

func (s *Server) write(m message) error {
	m.JSONRPC = "2.0"
	line, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return fmt.Errorf("rpc: not serving")
	}
	_, err = s.w.Write(append(line, '\n'))
	return err
}

func (s *Server) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return s.write(message{Method: method, Params: raw})
}

func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.mu.Lock()
	s.w = w
	s.mu.Unlock()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil || m.Method == "" {
			continue // ignore garbage and responses; the server never calls out
		}
		s.mu.Lock()
		h := s.handlers[m.Method]
		s.mu.Unlock()
		if m.ID == nil { // notification: nothing to answer
			if h != nil {
				go h(ctx, m.Params)
			}
			continue
		}
		go func(m message) {
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
		}(m)
	}
	return sc.Err()
}

type Client struct {
	w        io.Writer
	wmu      sync.Mutex
	next     atomic.Int64
	mu       sync.Mutex
	waiting  map[int64]chan message
	onNotify func(string, json.RawMessage)
	closed   chan struct{}
	closer   io.Closer
}

func NewClient(r io.Reader, w io.Writer, onNotify func(method string, params json.RawMessage)) *Client {
	c := &Client{w: w, waiting: map[int64]chan message{}, onNotify: onNotify, closed: make(chan struct{})}
	if cl, ok := w.(io.Closer); ok {
		c.closer = cl
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
		return io.ErrUnexpectedEOF
	}
}

func (c *Client) Close() error {
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}

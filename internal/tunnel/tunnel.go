// Package tunnel runs port forwards over an SSH client: local (-L), remote
// (-R) and dynamic (-D, SOCKS5). It holds no policy; the hub decides who may
// start what and binds only loopback.
package tunnel

import (
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Forward is one running tunnel.
type Forward struct {
	ln      net.Listener
	onConns func()
	mu      sync.Mutex
	conns   map[net.Conn]net.Conn // accepted conn -> its dialed peer (nil until dialed)
	total   int
	closed  bool
	err     error
	done    chan struct{}
}

// serve accepts on ln and, for each connection, dials its peer and pipes them
// together. dial returning an error drops the connection with no pipe.
func serve(ln net.Listener, onConns func(), dial func(net.Conn) (net.Conn, error)) *Forward {
	f := &Forward{ln: ln, onConns: onConns, conns: map[net.Conn]net.Conn{}, done: make(chan struct{})}
	go func() {
		defer close(f.done)
		for {
			in, err := ln.Accept()
			if err != nil {
				f.mu.Lock()
				if !f.closed {
					f.err = err
				}
				f.mu.Unlock()
				f.Close()
				return
			}
			if !f.track(in) {
				in.Close()
				continue
			}
			go func() {
				defer f.untrack(in)
				out, err := dial(in)
				if err != nil {
					return
				}
				if !f.pair(in, out) {
					out.Close() // Close raced the dial
					return
				}
				pipe(in, out)
			}()
		}
	}()
	return f
}

func (f *Forward) track(c net.Conn) bool {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return false
	}
	f.conns[c] = nil
	f.total++
	f.mu.Unlock()
	f.changed()
	return true
}

// pair records in's dialed peer so Close can end both ends of a half-closed
// pipe, unless Close already ran.
func (f *Forward) pair(in, out net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false
	}
	f.conns[in] = out
	return true
}

func (f *Forward) untrack(c net.Conn) {
	c.Close()
	f.mu.Lock()
	out := f.conns[c]
	delete(f.conns, c)
	f.mu.Unlock()
	if out != nil {
		out.Close()
	}
	f.changed()
}

func (f *Forward) changed() {
	if f.onConns != nil {
		f.onConns()
	}
}

func (f *Forward) Addr() string          { return f.ln.Addr().String() }
func (f *Forward) Done() <-chan struct{} { return f.done }
func (f *Forward) Conns() int            { f.mu.Lock(); defer f.mu.Unlock(); return len(f.conns) }
func (f *Forward) Total() int            { f.mu.Lock(); defer f.mu.Unlock(); return f.total }
func (f *Forward) Err() error            { f.mu.Lock(); defer f.mu.Unlock(); return f.err }

// Close stops accepting and ends every open connection. Idempotent.
func (f *Forward) Close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	open := make([]net.Conn, 0, len(f.conns)*2)
	for in, out := range f.conns {
		open = append(open, in)
		if out != nil {
			open = append(open, out)
		}
	}
	f.mu.Unlock()
	f.ln.Close()
	for _, c := range open {
		c.Close()
	}
}

// pipe copies both ways. A direction that ends at a clean EOF half-closes its
// destination, so the other direction can still carry a reply; an error, or
// a destination with no CloseWrite, closes both ends. Forward.Close closes
// both the accepted conn and its dialed peer, so a pipe stuck reading a
// half-closed connection's peer still unblocks.
func pipe(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	half := func(dst, src io.ReadWriteCloser) {
		defer func() { done <- struct{}{} }()
		if _, err := io.Copy(dst, src); err == nil {
			if cw, ok := dst.(interface{ CloseWrite() error }); ok && cw.CloseWrite() == nil {
				return
			}
		}
		a.Close()
		b.Close()
	}
	go half(a, b)
	go half(b, a)
	<-done
	<-done
	a.Close()
	b.Close()
}

// Local listens on listen (this machine) and dials target through the server.
func Local(c *ssh.Client, listen, target string, onConns func()) (*Forward, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(net.Conn) (net.Conn, error) {
		return c.Dial("tcp", target)
	}), nil
}

// Remote asks the server to listen on listen and dials target from here.
func Remote(c *ssh.Client, listen, target string, onConns func()) (*Forward, error) {
	ln, err := c.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(net.Conn) (net.Conn, error) {
		return net.DialTimeout("tcp", target, 10*time.Second)
	}), nil
}

// Dynamic runs a SOCKS5 server (CONNECT only, no auth) on listen.
func Dynamic(c *ssh.Client, listen string, onConns func()) (*Forward, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(in net.Conn) (net.Conn, error) {
		return socks(in, func(addr string) (net.Conn, error) { return c.Dial("tcp", addr) })
	}), nil
}

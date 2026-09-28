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
	conns   map[net.Conn]struct{}
	total   int
	closed  bool
	err     error
	done    chan struct{}
}

func serve(ln net.Listener, onConns func(), handle func(net.Conn)) *Forward {
	f := &Forward{ln: ln, onConns: onConns, conns: map[net.Conn]struct{}{}, done: make(chan struct{})}
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
				handle(in)
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
	f.conns[c] = struct{}{}
	f.total++
	f.mu.Unlock()
	f.changed()
	return true
}

func (f *Forward) untrack(c net.Conn) {
	c.Close()
	f.mu.Lock()
	delete(f.conns, c)
	f.mu.Unlock()
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
	open := make([]net.Conn, 0, len(f.conns))
	for c := range f.conns {
		open = append(open, c)
	}
	f.mu.Unlock()
	f.ln.Close()
	for _, c := range open {
		c.Close()
	}
}

// pipe copies both ways; when either direction ends, both ends close.
func pipe(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
	<-done
}

// Local listens on listen (this machine) and dials target through the server.
func Local(c *ssh.Client, listen, target string, onConns func()) (*Forward, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(in net.Conn) {
		out, err := c.Dial("tcp", target)
		if err != nil {
			return
		}
		pipe(in, out)
	}), nil
}

// Remote asks the server to listen on listen and dials target from here.
func Remote(c *ssh.Client, listen, target string, onConns func()) (*Forward, error) {
	ln, err := c.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(in net.Conn) {
		out, err := net.DialTimeout("tcp", target, 10*time.Second)
		if err != nil {
			return
		}
		pipe(in, out)
	}), nil
}

// Dynamic runs a SOCKS5 server (CONNECT only, no auth) on listen.
func Dynamic(c *ssh.Client, listen string, onConns func()) (*Forward, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(in net.Conn) {
		out, err := socks(in, func(addr string) (net.Conn, error) { return c.Dial("tcp", addr) })
		if err != nil {
			return
		}
		pipe(in, out)
	}), nil
}

// Package tunnel runs port forwards over an SSH client: local (-L), remote
// (-R) and dynamic (-D, SOCKS5). It holds no policy; the hub decides who may
// start what and binds only loopback.
package tunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// dialTimeout bounds how long a dial may take before Local, Remote or
// Dynamic gives up on a connection.
var dialTimeout = 10 * time.Second

// Forward is one running tunnel.
type Forward struct {
	ln      net.Listener
	onConns func()
	cancel  context.CancelFunc
	mu      sync.Mutex
	conns   map[net.Conn]net.Conn // accepted conn -> its dialed peer (nil until dialed)
	total   int
	closed  bool
	err     error
	done    chan struct{}
}

// serve accepts on ln and, for each connection, dials its peer and pipes them
// together. dial returning an error drops the connection with no pipe.
// Accept errors from a transient EMFILE/ENFILE (out of file descriptors) are
// retried with a backoff instead of ending the tunnel.
func serve(ln net.Listener, onConns func(), dial func(context.Context, net.Conn) (net.Conn, error)) *Forward {
	ctx, cancel := context.WithCancel(context.Background())
	f := &Forward{ln: ln, onConns: onConns, cancel: cancel, conns: map[net.Conn]net.Conn{}, done: make(chan struct{})}
	go func() {
		defer close(f.done)
		backoff := 5 * time.Millisecond
		for {
			in, err := ln.Accept()
			if err != nil {
				if (errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE)) && ctx.Err() == nil {
					// ponytail: on Windows Winsock reports WSAEMFILE (10024), which syscall.EMFILE does not match, so there the tunnel still ends; match syscall.Errno(10024) in a _windows.go file if that ever bites.
					t := time.NewTimer(backoff)
					select {
					case <-ctx.Done():
						t.Stop()
					case <-t.C:
					}
					backoff = min(backoff*2, time.Second)
					continue
				}
				f.mu.Lock()
				if !f.closed {
					f.err = err
				}
				f.mu.Unlock()
				f.Close()
				return
			}
			backoff = 5 * time.Millisecond
			if !f.track(in) {
				in.Close()
				continue
			}
			go func() {
				defer f.untrack(in)
				out, err := dial(ctx, in)
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
	f.cancel()
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
	return serve(ln, onConns, func(ctx context.Context, _ net.Conn) (net.Conn, error) {
		dctx, cancel := context.WithTimeout(ctx, dialTimeout)
		defer cancel()
		return c.DialContext(dctx, "tcp", target)
	}), nil
}

// Remote asks the server to listen on listen and dials target from here.
func Remote(c *ssh.Client, listen, target string, onConns func()) (*Forward, error) {
	ln, err := c.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(ctx context.Context, _ net.Conn) (net.Conn, error) {
		return (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", target)
	}), nil
}

// Dynamic runs a SOCKS5 server (CONNECT only, no auth) on listen.
func Dynamic(c *ssh.Client, listen string, onConns func()) (*Forward, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(ctx context.Context, in net.Conn) (net.Conn, error) {
		return socks(in, func(addr string) (net.Conn, error) {
			dctx, cancel := context.WithTimeout(ctx, dialTimeout)
			defer cancel()
			return c.DialContext(dctx, "tcp", addr)
		})
	}), nil
}

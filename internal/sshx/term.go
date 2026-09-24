package sshx

import (
	"bytes"
	"errors"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

const (
	HighWater = 1 << 20
	LowWater  = 256 << 10
	termQueue = 1024 // pending input/resize ops per terminal
)

var (
	ErrTermQueueFull = errors.New("terminal input queue full; input dropped")
	ErrTermClosed    = errors.New("terminal closed")
)

// TermSession is one interactive PTY on a shared client. Output is pushed
// through onData; the consumer must Ack bytes it has rendered. Once more than
// HighWater bytes are unacked the reader stalls until acks bring it to
// LowWater or below. While stalled the SSH window fills and the remote side
// blocks: backpressure reaches the source instead of buffering anywhere.
//
// Write and Resize never block: they enqueue onto an ordered queue drained
// by one writer goroutine, so input is applied in call order.
type TermSession struct {
	stdin     io.Writer
	resize    func(rows, cols int) error
	closeFn   func()
	ops       chan func()
	done      chan struct{} // closed by Close; stops the writer
	closeOnce sync.Once

	mu      sync.Mutex
	cond    *sync.Cond
	unacked int
	closed  bool
}

func (m *Manager) OpenTerm(rows, cols int, onData func([]byte), onExit func(code int, reason string)) (*TermSession, error) {
	sess, err := m.OpenSession()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		sess.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, err
	}
	wait := func() (int, string) {
		err := sess.Wait()
		var ee *ssh.ExitError
		switch {
		case err == nil:
			return 0, ""
		case errors.As(err, &ee):
			return ee.ExitStatus(), ""
		default:
			return -1, err.Error()
		}
	}
	closeFn := func() {
		stdin.Close()
		sess.Close()
	}
	return newTerm(stdout, stdin, sess.WindowChange, wait, closeFn, onData, onExit), nil
}

// newTerm starts the reader and writer goroutines. wait is called once
// stdout ends and yields the exit code and reason for onExit.
func newTerm(stdout io.Reader, stdin io.Writer, resize func(rows, cols int) error,
	wait func() (int, string), closeFn func(), onData func([]byte), onExit func(int, string)) *TermSession {
	t := &TermSession{stdin: stdin, resize: resize, closeFn: closeFn,
		ops: make(chan func(), termQueue), done: make(chan struct{})}
	t.cond = sync.NewCond(&t.mu)

	go func() {
		for {
			select {
			case op := <-t.ops:
				op()
			case <-t.done:
				return
			}
		}
	}()

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				onData(bytes.Clone(buf[:n]))
				t.mu.Lock()
				t.unacked += n
				if t.unacked > HighWater {
					for t.unacked > LowWater && !t.closed {
						t.cond.Wait()
					}
				}
				t.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
		code, reason := wait()
		onExit(code, reason)
		t.Close() // stop the writer; the channel is already gone
	}()
	return t
}

func (t *TermSession) enqueue(op func()) error {
	select {
	case <-t.done:
		return ErrTermClosed
	default:
	}
	select {
	case t.ops <- op:
		return nil
	default:
		return ErrTermQueueFull
	}
}

// Write queues p for the PTY's stdin. It copies p and never blocks.
func (t *TermSession) Write(p []byte) error {
	p = bytes.Clone(p)
	return t.enqueue(func() { t.stdin.Write(p) })
}

// Resize queues a window change, ordered with Write.
func (t *TermSession) Resize(rows, cols int) error {
	return t.enqueue(func() { t.resize(rows, cols) })
}

// Ack records that the consumer processed n bytes; n <= 0 is ignored. It
// never blocks.
func (t *TermSession) Ack(n int) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	t.unacked = max(t.unacked-n, 0)
	if t.unacked <= LowWater {
		t.cond.Broadcast()
	}
	t.mu.Unlock()
}

// Close ends the session. It is idempotent, wakes a stalled reader, and
// stops the writer. onExit still fires once, from the reader.
func (t *TermSession) Close() {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.cond.Broadcast()
		t.mu.Unlock()
		close(t.done)
		t.closeFn()
	})
}

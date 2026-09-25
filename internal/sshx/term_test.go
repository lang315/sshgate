package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
)

func termManager(t *testing.T) *Manager {
	host, port, cleanup := startSSH(t)
	t.Cleanup(cleanup)
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 30000})
	t.Cleanup(m.Close)
	return m
}

func TestTermEchoAndResize(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	m := termManager(t)
	var mu sync.Mutex
	var buf bytes.Buffer
	exited := make(chan int, 1)
	ts, err := m.OpenTerm(24, 80, func(b []byte) { mu.Lock(); buf.Write(b); mu.Unlock() }, func(code int, _ string) { exited <- code })
	if err != nil {
		t.Fatal(err)
	}
	ts.Write([]byte("echo TERM-OK\n"))
	waitFor(t, &mu, &buf, "TERM-OK")
	if err := ts.Resize(50, 132); err != nil {
		t.Fatal(err)
	}
	ts.Write([]byte("stty size\n"))
	waitFor(t, &mu, &buf, "50 132")
	ts.Write([]byte("exit 7\n"))
	select {
	case code := <-exited:
		if code != 7 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no exit event")
	}
}

func TestTermFlowControlBoundsUnacked(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	m := termManager(t)
	var mu sync.Mutex
	total := 0
	ts, err := m.OpenTerm(24, 80, func(b []byte) { mu.Lock(); total += len(b); mu.Unlock() }, func(int, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	ts.Write([]byte("yes | head -c 20000000\n")) // 20 MB, nobody acks
	time.Sleep(3 * time.Second)
	mu.Lock()
	got := total
	mu.Unlock()
	if got > HighWater+64*1024 {
		t.Fatalf("delivered %d bytes without acks; high water is %d", got, HighWater)
	}
	ts.Ack(got) // drain; must resume
	time.Sleep(2 * time.Second)
	mu.Lock()
	after := total
	mu.Unlock()
	if after <= got {
		t.Fatal("reader did not resume after ack")
	}
}

func waitFor(t *testing.T, mu *sync.Mutex, buf *bytes.Buffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := strings.Contains(buf.String(), needle)
		mu.Unlock()
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("did not see %q in %q", needle, buf.String())
}

func TestKeepaliveReportsDeadConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 30000})
	defer m.Close()
	if _, err := m.Exec(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	dead := make(chan string, 1)
	m.StartKeepalive(500*time.Millisecond, func(r string) { dead <- r })
	cleanup() // stop the container
	select {
	case <-dead:
	case <-time.After(30 * time.Second):
		t.Fatal("keepalive did not report a dead connection")
	}
}

// --- unit tests (no Docker): drive a TermSession through newTerm with fakes.

// opLog records stdin writes and resizes in the order the writer goroutine
// applies them. If gate is non-nil, every op waits on it first.
type opLog struct {
	mu   sync.Mutex
	ops  []string
	gate chan struct{}
}

func (l *opLog) add(s string) {
	if l.gate != nil {
		<-l.gate
	}
	l.mu.Lock()
	l.ops = append(l.ops, s)
	l.mu.Unlock()
}

func (l *opLog) Write(p []byte) (int, error) { l.add("w:" + string(p)); return len(p), nil }
func (l *opLog) resize(r, c int) error       { l.add(fmt.Sprintf("r:%dx%d", r, c)); return nil }

func (l *opLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.ops)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// fakeTerm returns a TermSession whose output is fed through pw and whose
// close closes the pipe, as sess.Close would end the SSH channel.
func fakeTerm(l *opLog, onData func([]byte), onExit func(int, string)) (*TermSession, *io.PipeWriter) {
	pr, pw := io.Pipe()
	ts := newTerm(pr, l, l.resize, func() (int, string) { return 7, "" }, func() { pr.Close() }, onData, onExit)
	return ts, pw
}

func TestTermOpsApplyInOrder(t *testing.T) {
	l := &opLog{}
	ts, pw := fakeTerm(l, func([]byte) {}, func(int, string) {})
	defer pw.Close()
	defer ts.Close()
	var want []string
	for i := range 200 {
		if i%3 == 0 {
			if err := ts.Resize(i, i+1); err != nil {
				t.Fatal(err)
			}
			want = append(want, fmt.Sprintf("r:%dx%d", i, i+1))
			continue
		}
		p := []byte(fmt.Sprintf("k%d", i))
		if err := ts.Write(p); err != nil {
			t.Fatal(err)
		}
		p[0] = 'X' // Write must copy: the caller may reuse its buffer
		want = append(want, fmt.Sprintf("w:k%d", i))
	}
	eventually(t, "all ops", func() bool { return len(l.snapshot()) == len(want) })
	if got := l.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("ops out of order:\n got %v\nwant %v", got, want)
	}
}

func TestTermQueueFullDropsInsteadOfBlocking(t *testing.T) {
	l := &opLog{gate: make(chan struct{})}
	ts, pw := fakeTerm(l, func([]byte) {}, func(int, string) {})
	defer pw.Close()
	ok := 0
	var err error
	for range termQueue + 10 {
		if err = ts.Write([]byte("x")); err != nil {
			break
		}
		ok++
	}
	if !errors.Is(err, ErrTermQueueFull) {
		t.Fatalf("want ErrTermQueueFull after %d writes, got %v", ok, err)
	}
	if ok < termQueue || ok > termQueue+1 { // +1: the op the writer took and is stuck on
		t.Fatalf("accepted %d writes, queue is %d", ok, termQueue)
	}
	close(l.gate)
	ts.Close()
	if err := ts.Write([]byte("y")); !errors.Is(err, ErrTermClosed) {
		t.Fatalf("write after close: %v", err)
	}
	if err := ts.Resize(1, 1); !errors.Is(err, ErrTermClosed) {
		t.Fatalf("resize after close: %v", err)
	}
}

func TestTermFlowControlAndCloseUnit(t *testing.T) {
	var total atomic.Int64
	exits := make(chan int, 2)
	ts, pw := fakeTerm(&opLog{}, func(b []byte) { total.Add(int64(len(b))) }, func(code int, _ string) { exits <- code })
	go func() { // endless producer, like `yes`; ends when the pipe is closed
		chunk := bytes.Repeat([]byte("y\n"), 16*1024)
		for {
			if _, err := pw.Write(chunk); err != nil {
				return
			}
		}
	}()

	eventually(t, "high water", func() bool { return total.Load() > HighWater })
	time.Sleep(100 * time.Millisecond)
	stalled := total.Load()
	if stalled > HighWater+32*1024 {
		t.Fatalf("delivered %d bytes without acks; high water is %d", stalled, HighWater)
	}

	// Acking down to just above LowWater must not resume (hysteresis).
	ts.Ack(int(stalled) - LowWater - 1024)
	time.Sleep(100 * time.Millisecond)
	if got := total.Load(); got != stalled {
		t.Fatalf("resumed above low water: %d -> %d", stalled, got)
	}
	ts.Ack(2048) // now under LowWater
	eventually(t, "resume after ack", func() bool { return total.Load() > stalled })

	// Nobody acks again, so the reader stalls on the cond var; Close must
	// wake it and it must report exit exactly once.
	eventually(t, "second stall", func() bool { return total.Load() > stalled+HighWater-LowWater })
	ts.Close()
	select {
	case code := <-exits:
		if code != 7 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not unblock the stalled reader")
	}
	ts.Close() // idempotent
	select {
	case <-exits:
		t.Fatal("onExit called twice")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTermRemoteExitReportsOnceAndStopsWriter(t *testing.T) {
	exits := make(chan int, 2)
	ts, pw := fakeTerm(&opLog{}, func([]byte) {}, func(code int, _ string) { exits <- code })
	pw.Close() // remote side ends the channel
	select {
	case <-exits:
	case <-time.After(5 * time.Second):
		t.Fatal("no exit on EOF")
	}
	// The reader closes the session on EOF, so the writer is stopped and
	// further input is refused rather than queued forever.
	eventually(t, "writer stopped", func() bool { return errors.Is(ts.Write([]byte("x")), ErrTermClosed) })
}

// --- in-process SSH server tests (no Docker).

func fakeManager(t *testing.T, srv *sshtest.Server) *Manager {
	m := NewManager(DialConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", Auth: "password", Insecure: true, TimeoutMs: 30000})
	t.Cleanup(m.Close)
	return m
}

func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s blocked", what)
	}
}

// R28: a long Exec must not hold the manager lock, or it would block
// term.open, keepalive ticks and Close for up to 600 s.
func TestExecDoesNotBlockOpenSession(t *testing.T) {
	srv := sshtest.Start(t)
	m := fakeManager(t, srv)
	res := make(chan ExecResult, 1)
	go func() { r, _ := m.Exec(context.Background(), "long-running"); res <- r }()
	select {
	case <-srv.Execs:
	case <-time.After(5 * time.Second):
		t.Fatal("exec never reached the server")
	}
	within(t, "OpenSession during Exec", func() {
		sess, err := m.OpenSession()
		if err != nil {
			t.Error(err)
			return
		}
		sess.Close()
	})
	within(t, "StartKeepalive during Exec", func() { m.StartKeepalive(time.Hour, nil) })
	close(srv.Release)
	if r := <-res; r.Stdout != "long-running" {
		t.Fatalf("exec result %+v", r)
	}
}

// The su path serializes on suMu only; holding it (as a running su command
// does) must not block OpenSession or Close.
func TestSuLockDoesNotBlockOpenSessionOrClose(t *testing.T) {
	srv := sshtest.Start(t)
	m := fakeManager(t, srv)
	m.suMu.Lock()
	defer m.suMu.Unlock()
	within(t, "OpenSession while su busy", func() {
		sess, err := m.OpenSession()
		if err != nil {
			t.Error(err)
			return
		}
		sess.Close()
	})
	within(t, "Close while su busy", m.Close)
}

func TestTermEchoOverFakeServer(t *testing.T) {
	srv := sshtest.Start(t)
	m := fakeManager(t, srv)
	var mu sync.Mutex
	var buf bytes.Buffer
	exited := make(chan int, 1)
	ts, err := m.OpenTerm(24, 80, func(b []byte) { mu.Lock(); buf.Write(b); mu.Unlock() }, func(code int, _ string) { exited <- code })
	if err != nil {
		t.Fatal(err)
	}
	ts.Write([]byte("hello"))
	ts.Resize(50, 132)
	ts.Write([]byte(" world"))
	waitFor(t, &mu, &buf, "hello world")
	ts.Close()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("no exit after Close")
	}
}

// The hub passes timeouts through to the AI but hides every other SSH
// error, so a timeout must be recognisable.
func TestExecTimeoutIsErrTimeout(t *testing.T) {
	srv := sshtest.Start(t) // execs block until Release, which is never closed
	m := fakeManager(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := m.Exec(ctx, "sleep"); !errors.Is(err, ErrTimeout) || err.Error() != "command timed out: context deadline exceeded" {
		t.Fatalf("got %v", err)
	}
}

// A shell that never prints a su password prompt must fail elevation at the
// setup deadline, not block forever inside a read.
func TestSuWithoutPromptTimesOut(t *testing.T) {
	old := suSetupTimeout
	suSetupTimeout = 300 * time.Millisecond
	t.Cleanup(func() { suSetupTimeout = old })
	srv := sshtest.Start(t)
	m := NewManager(DialConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", SuPassword: "x", Auth: "password", Insecure: true, TimeoutMs: 30000})
	t.Cleanup(m.Close)
	within(t, "su elevation", func() {
		if _, err := m.Exec(context.Background(), "id -u"); err == nil {
			t.Error("elevation without a prompt succeeded")
		}
	})
}

// A connection that dies before the first keepalive tick must still be
// reported. CI hit this: the container stopped within one interval.
func TestKeepaliveReportsDeathBeforeFirstTick(t *testing.T) {
	m := fakeManager(t, sshtest.Start(t))
	m.mu.Lock()
	if err := m.ensure(); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	c := m.client
	m.mu.Unlock()
	dead := make(chan string, 1)
	m.StartKeepalive(100*time.Millisecond, func(r string) { dead <- r })
	c.Close()
	select {
	case <-dead:
	case <-time.After(2 * time.Second):
		t.Fatal("death before the first tick was not reported")
	}
}

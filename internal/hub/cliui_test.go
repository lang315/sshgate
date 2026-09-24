package hub

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeBuf is a bytes.Buffer a test can read from (String) while
// RunCLIApprover is still writing to it on another goroutine.
type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestCLIApproverAllowsAndDenies(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	pr, pw := io.Pipe()
	defer pw.Close() // let RunCLIApprover's goroutine see EOF and return, instead of leaking
	out := &safeBuf{}
	go RunCLIApprover(context.Background(), h, pr, out)
	// Wait for the banner: RunCLIApprover installs the event sink before
	// printing it, so once it's visible the sink is guaranteed installed and
	// a request submitted below can't race ahead of it.
	for !strings.Contains(out.String(), "Commands:") {
		time.Sleep(time.Millisecond)
	}

	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Client: "claude", Server: "vis", Command: "ls -la", Description: "list"})
		errc <- err
	}()
	for len(h.Broker().Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(out.String(), "ls -la") || !strings.Contains(out.String(), "(unverified)") {
		t.Fatalf("prompt missing command or unverified label: %q", out.String())
	}
	pw.Write([]byte("d not now\n"))
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "not now") {
		t.Fatalf("got %v", err)
	}
}

func TestCLIApproverUnlockUsesPlainLineWhenNoReadPassword(t *testing.T) {
	h, _, _ := newEncHub(t, &fakeExec{})
	pr, pw := io.Pipe()
	out := &safeBuf{}
	done := make(chan error, 1)
	go func() { done <- RunCLIApprover(context.Background(), h, pr, out) }()

	pw.Write([]byte("u\n"))
	pw.Write([]byte("pw\n"))
	deadline := time.After(2 * time.Second)
	for !strings.Contains(out.String(), "unlocked") {
		select {
		case <-deadline:
			t.Fatalf("never unlocked: %q", out.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if h.Locked() {
		t.Fatal("want unlocked")
	}
	pw.Write([]byte("q\n"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestCLIApproverUnlockUsesReadPasswordWhenSet covers R7: when ReadPassword
// is set (runHub does this for a real terminal), the "u" command reads the
// password through it instead of scanning another line from in.
func TestCLIApproverUnlockUsesReadPasswordWhenSet(t *testing.T) {
	h, _, _ := newEncHub(t, &fakeExec{})
	ReadPassword = func() (string, error) { return "pw", nil }
	t.Cleanup(func() { ReadPassword = nil })

	pr, pw := io.Pipe()
	out := &safeBuf{}
	done := make(chan error, 1)
	go func() { done <- RunCLIApprover(context.Background(), h, pr, out) }()

	pw.Write([]byte("u\n")) // no second line: the password comes from ReadPassword, not in
	deadline := time.After(2 * time.Second)
	for !strings.Contains(out.String(), "unlocked") {
		select {
		case <-deadline:
			t.Fatalf("never unlocked: %q", out.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if h.Locked() {
		t.Fatal("want unlocked")
	}
	pw.Write([]byte("q\n"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCLIApproverReturnsOnCtxCancelWhileIdle(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	pr, _ := io.Pipe() // never written to: input stays idle
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunCLIApprover(ctx, h, pr, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunCLIApprover did not return on ctx cancel while stdin idle")
	}
}

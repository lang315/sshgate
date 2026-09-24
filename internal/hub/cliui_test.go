package hub

import (
	"bytes"
	"context"
	"errors"
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

// TestCLIApproverBareCommandNeedsIDWithTwoPending covers R31: with 2+
// requests pending, a bare a/d/s (no id) must not guess which one the human
// meant, and an explicit id targets exactly that request, leaving the other
// pending untouched.
func TestCLIApproverBareCommandNeedsIDWithTwoPending(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	pr, pw := io.Pipe()
	defer pw.Close()
	out := &safeBuf{}
	go RunCLIApprover(context.Background(), h, pr, out)
	for !strings.Contains(out.String(), "Commands:") {
		time.Sleep(time.Millisecond)
	}

	errc1 := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "first"})
		errc1 <- err
	}()
	for len(h.Broker().Pending()) < 1 {
		time.Sleep(2 * time.Millisecond)
	}
	errc2 := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "second"})
		errc2 <- err
	}()
	for len(h.Broker().Pending()) < 2 {
		time.Sleep(2 * time.Millisecond)
	}
	pend := h.Broker().Pending()
	firstID, secondID := pend[0].ID, pend[1].ID

	// Bare "a" with two pending must decide nothing.
	pw.Write([]byte("a\n"))
	time.Sleep(30 * time.Millisecond)
	if got := len(h.Broker().Pending()); got != 2 {
		t.Fatalf("bare a with 2 pending decided something: %d left pending", got)
	}
	if !strings.Contains(out.String(), "several requests pending") {
		t.Fatalf("missing guidance for ambiguous bare command: %q", out.String())
	}

	// "a <id-of-second>" approves exactly the second; the first stays pending.
	pw.Write([]byte("a " + secondID + "\n"))
	if err := <-errc2; err != nil {
		t.Fatalf("second exec: %v", err)
	}
	left := h.Broker().Pending()
	if len(left) != 1 || left[0].ID != firstID {
		t.Fatalf("want only the first still pending, got %+v", left)
	}

	// "d <id> reason" denies that one, with the given reason.
	pw.Write([]byte("d " + firstID + " no thanks\n"))
	err := <-errc1
	var de *DeniedError
	if !errors.As(err, &de) || de.Reason != "no thanks" {
		t.Fatalf("got %v", err)
	}
}

// TestCLIApproverBareApprovesSolePending covers R31: a bare "a" is fine when
// there's no ambiguity, i.e. exactly one request pending.
func TestCLIApproverBareApprovesSolePending(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	pr, pw := io.Pipe()
	defer pw.Close()
	out := &safeBuf{}
	go RunCLIApprover(context.Background(), h, pr, out)
	for !strings.Contains(out.String(), "Commands:") {
		time.Sleep(time.Millisecond)
	}

	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "solo"})
		errc <- err
	}()
	for len(h.Broker().Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	pw.Write([]byte("a\n"))
	if err := <-errc; err != nil {
		t.Fatalf("bare a with one pending: %v", err)
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

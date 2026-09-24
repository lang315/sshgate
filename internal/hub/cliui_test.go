package hub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// waitOut waits until out contains want; test goroutine only.
func waitOut(t *testing.T, out *safeBuf, want string) {
	t.Helper()
	if !waitFor(t, fmt.Sprintf("output %q", want), func() bool { return strings.Contains(out.String(), want) }) {
		t.Fatalf("output so far: %q", out.String())
	}
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
	waitOut(t, out, "Commands:")

	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Client: "claude", Server: "vis", Command: "ls -la", Description: "list"})
		errc <- err
	}()
	waitPending(t, h.Broker(), 1)
	waitOut(t, out, "ls -la")
	if !strings.Contains(out.String(), "(unverified)") {
		t.Fatalf("prompt missing unverified label: %q", out.String())
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
	waitOut(t, out, "Commands:")

	errc1 := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "first"})
		errc1 <- err
	}()
	waitPending(t, h.Broker(), 1)
	errc2 := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "second"})
		errc2 <- err
	}()
	waitPending(t, h.Broker(), 2)
	pend := h.Broker().Pending()
	firstID, secondID := pend[0].ID, pend[1].ID

	// Bare "a" with two pending must decide nothing.
	pw.Write([]byte("a\n"))
	// The guidance is printed once "a" has been handled, so the pending
	// count checked after it is final.
	waitOut(t, out, "several requests pending")
	if got := len(h.Broker().Pending()); got != 2 {
		t.Fatalf("bare a with 2 pending decided something: %d left pending", got)
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
	waitOut(t, out, "Commands:")

	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "solo"})
		errc <- err
	}()
	waitPending(t, h.Broker(), 1)
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

// startCLI runs the approver on a pipe and waits for its banner.
func startCLI(t *testing.T, h *Hub) (io.Writer, *safeBuf) {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	out := &safeBuf{}
	go RunCLIApprover(context.Background(), h, pr, out)
	waitOut(t, out, "Commands:")
	return pw, out
}

// C2: "a <stale id>" must never approve a different request that happens to
// be the only one pending now.
func TestCLIApproverStaleIDDecidesNothing(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHubExpiry(t, fe, time.Minute)
	pw, out := startCLI(t, h)

	ctx, cancel := context.WithCancel(context.Background())
	errc1 := make(chan error, 1)
	go func() { _, err := h.Exec(ctx, ExecRequest{Server: "vis", Command: "first"}); errc1 <- err }()
	waitPending(t, h.Broker(), 1)
	staleID := h.Broker().Pending()[0].ID
	cancel() // the AI withdraws the one the human was reading
	<-errc1
	waitOut(t, out, "withdrawn")

	go h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "rm -rf /tmp/x"})
	waitPending(t, h.Broker(), 1)
	for _, cmd := range []string{"a", "s", "d"} {
		pw.Write([]byte(cmd + " " + staleID + "\n"))
	}
	pw.Write([]byte("p\n")) // handled after the three above
	waitFor(t, "three refusals", func() bool {
		return strings.Count(out.String(), "no pending request "+staleID) == 3
	})
	if n := len(h.Broker().Pending()); n != 1 {
		t.Fatalf("stale id decided the other request: %d pending", n)
	}
	if len(fe.calls) != 0 {
		t.Fatalf("executed: %v", fe.calls)
	}
}

func TestCLIApproverJunkWithNothingPending(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	pw, out := startCLI(t, h)
	pw.Write([]byte("a junk\n"))
	waitOut(t, out, "nothing pending")
}

// "d <reason>" with one pending still denies it, as long as the first word
// does not look like a request id.
func TestCLIApproverDenyReasonShorthand(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
	pw, out := startCLI(t, h)
	errc := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); errc <- err }()
	waitPending(t, h.Broker(), 1)
	pw.Write([]byte("d not now\n"))
	var de *DeniedError
	if err := <-errc; !errors.As(err, &de) || de.Reason != "not now" {
		t.Fatalf("got %v", err)
	}
	waitOut(t, out, "denied")
}

func TestCLIApproverPrintsExpired(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, 50*time.Millisecond)
	_, out := startCLI(t, h)
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); !errors.Is(err, ErrExpired) {
		t.Fatalf("got %v", err)
	}
	waitOut(t, out, "expired")
}

package broker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

const testWait = 2 * time.Second

func newTestBroker(expiry time.Duration) (*Broker, *[]Event, *sync.Mutex) {
	var events []Event
	var mu sync.Mutex
	b := New(Options{MaxPending: 5, Expiry: expiry, OnEvent: func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}})
	return b, &events, &mu
}

// waitForPending polls until at least n requests are pending, failing the
// test (instead of hanging forever) if that doesn't happen within testWait.
func waitForPending(t *testing.T, b *Broker, n int) []Request {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for {
		if p := b.Pending(); len(p) >= n {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d pending request(s), got %d", n, len(b.Pending()))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitForEvents polls until at least n events have been recorded, failing
// the test (instead of hanging forever) if that doesn't happen within
// testWait. Returns a snapshot copy, safe to inspect without the lock.
func waitForEvents(t *testing.T, mu *sync.Mutex, events *[]Event, n int) []Event {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for {
		mu.Lock()
		got := len(*events)
		if got >= n {
			out := append([]Event(nil), (*events)...)
			mu.Unlock()
			return out
		}
		mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d event(s), got %d", n, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSubmitBlocksUntilDecided(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	done := make(chan Decision, 1)
	go func() {
		d, err := b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
		if err != nil {
			t.Error(err)
		}
		done <- d
	}()
	pending := waitForPending(t, b, 1)
	if len(pending) != 1 || pending[0].ID == "" {
		t.Fatalf("pending = %+v", pending)
	}
	if err := b.Decide(pending[0].ID, Decision{Outcome: Denied, Reason: "no"}); err != nil {
		t.Fatal(err)
	}
	d := <-done
	if d.Outcome != Denied || d.Reason != "no" {
		t.Fatalf("got %+v", d)
	}
	if len(b.Pending()) != 0 {
		t.Fatal("request should be removed after decision")
	}
}

func TestExpiryIsExpiredNotDenied(t *testing.T) {
	b, _, _ := newTestBroker(50 * time.Millisecond)
	d, err := b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Outcome != Expired {
		t.Fatalf("got %+v", d)
	}
}

func TestExpiryEmitsDecidedEvent(t *testing.T) {
	b, events, mu := newTestBroker(50 * time.Millisecond)
	go b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
	// No waitForPending: with a 50 ms expiry the request may already be gone.
	got := waitForEvents(t, mu, events, 2)
	if len(got) != 2 || got[0].Kind != "pending" || got[1].Kind != "decided" || got[1].Decision.Outcome != Expired {
		t.Fatalf("events = %+v", got)
	}
}

func TestCancelWhilePendingRemovesRequest(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := b.Submit(ctx, Request{Server: "s", Command: "ls"}); errc <- err }()
	pending := waitForPending(t, b, 1)
	id := pending[0].ID
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if len(b.Pending()) != 0 {
		t.Fatal("cancelled request still pending")
	}
	if err := b.Decide(id, Decision{Outcome: Allowed}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("approve after cancel must be ErrNotFound, got %v", err)
	}
}

func TestCancelEmitsWithdrawnEvent(t *testing.T) {
	b, events, mu := newTestBroker(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := b.Submit(ctx, Request{Server: "s", Command: "ls"}); errc <- err }()
	waitForPending(t, b, 1)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	got := waitForEvents(t, mu, events, 2)
	if len(got) != 2 || got[0].Kind != "pending" || got[1].Kind != "decided" ||
		got[1].Decision.Outcome != Withdrawn || got[1].Decision.Reason != "cancelled by client" {
		t.Fatalf("events = %+v", got)
	}
}

func TestSixthRequestRefusedUntilOneDecided(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	for i := 0; i < 5; i++ {
		go b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
	}
	waitForPending(t, b, 5)
	if _, err := b.Submit(context.Background(), Request{Server: "s", Command: "ls"}); !errors.Is(err, ErrTooManyPending) {
		t.Fatalf("want ErrTooManyPending, got %v", err)
	}
	b.Decide(b.Pending()[0].ID, Decision{Outcome: Denied})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := b.Submit(ctx, Request{Server: "s", Command: "ls"}); errors.Is(err, ErrTooManyPending) {
		t.Fatal("slot freed but still refused")
	}
}

func TestDenyAllResolvesEverything(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	results := make(chan Decision, 3)
	for i := 0; i < 3; i++ {
		go func() { d, _ := b.Submit(context.Background(), Request{Server: "s", Command: "ls"}); results <- d }()
	}
	waitForPending(t, b, 3)
	b.DenyAll("cleared")
	for i := 0; i < 3; i++ {
		d := <-results
		if d.Outcome != Denied || d.Reason != "cleared" {
			t.Fatalf("got %+v", d)
		}
	}
}

func TestEventsEmitted(t *testing.T) {
	b, events, mu := newTestBroker(time.Minute)
	go b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
	pending := waitForPending(t, b, 1)
	b.Decide(pending[0].ID, Decision{Outcome: Allowed})
	got := waitForEvents(t, mu, events, 2)
	if len(got) != 2 || got[0].Kind != "pending" || got[1].Kind != "decided" {
		t.Fatalf("events = %+v", got)
	}
}

// TestPendingEventAlwaysBeforeDecided is the regression test for the fix
// round 1 ordering bug: a request became visible to Pending()/Decide()/
// DenyAll() the instant Submit unlocked b.mu, which was before Submit's own
// "pending" OnEvent call had run, so a fast concurrent decision could emit
// "decided" first. OnEvent here blocks on the "pending" call until released;
// while blocked, DenyAll runs concurrently. The fix (an "announced" gate that
// every "decided" emitter waits on) must still yield events in the order
// [pending, decided].
func TestPendingEventAlwaysBeforeDecided(t *testing.T) {
	var mu sync.Mutex
	var events []Event
	release := make(chan struct{})
	blocked := make(chan struct{})

	b := New(Options{MaxPending: 5, Expiry: time.Minute, OnEvent: func(e Event) {
		if e.Kind == "pending" {
			close(blocked)
			<-release
		}
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}})

	done := make(chan Decision, 1)
	go func() {
		d, _ := b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
		done <- d
	}()

	select {
	case <-blocked:
	case <-time.After(testWait):
		t.Fatal("timed out waiting for the pending OnEvent to block")
	}

	denyDone := make(chan struct{})
	go func() {
		b.DenyAll("cleared")
		close(denyDone)
	}()

	// Give DenyAll a chance to reach its announced-wait before releasing the
	// hook, so this actually exercises the race window rather than just
	// running DenyAll after the pending event has already landed.
	time.Sleep(20 * time.Millisecond)
	close(release)

	select {
	case <-denyDone:
	case <-time.After(testWait):
		t.Fatal("timed out waiting for DenyAll to finish")
	}
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0].Kind != "pending" || events[1].Kind != "decided" {
		t.Fatalf("events = %+v", events)
	}
}

func TestSubmitAlwaysAssignsFreshID(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	go b.Submit(context.Background(), Request{ID: "caller-supplied", Server: "s", Command: "ls"})
	pending := waitForPending(t, b, 1)
	if pending[0].ID == "caller-supplied" || pending[0].ID == "" {
		t.Fatalf("want a fresh ID, got %q", pending[0].ID)
	}
	b.Decide(pending[0].ID, Decision{Outcome: Denied})
}

func TestJSONTags(t *testing.T) {
	rb, err := json.Marshal(Request{ID: "x", TimeoutSec: 5, Target: "u@h:22"})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(rb); !strings.Contains(s, `"id":"x"`) || !strings.Contains(s, `"timeoutSec":5`) || !strings.Contains(s, `"target":"u@h:22"`) {
		t.Fatalf("Request tags: %s", s)
	}

	db, err := json.Marshal(Decision{Outcome: Denied, Reason: "no"})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(db); !strings.Contains(s, `"outcome":"denied"`) || !strings.Contains(s, `"reason":"no"`) {
		t.Fatalf("Decision tags: %s", s)
	}

	eb, err := json.Marshal(Event{Kind: "pending", Request: Request{ID: "x"}, Decision: Decision{Outcome: Allowed}})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(eb); !strings.Contains(s, `"kind":"pending"`) || !strings.Contains(s, `"request":{`) || !strings.Contains(s, `"decision":{`) {
		t.Fatalf("Event tags: %s", s)
	}
}

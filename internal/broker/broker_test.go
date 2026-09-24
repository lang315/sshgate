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
	var pending []Request
	for i := 0; i < 50 && len(pending) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		pending = b.Pending()
	}
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

func TestCancelWhilePendingRemovesRequest(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := b.Submit(ctx, Request{Server: "s", Command: "ls"}); errc <- err }()
	for len(b.Pending()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	id := b.Pending()[0].ID
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

func TestSixthRequestRefusedUntilOneDecided(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	for i := 0; i < 5; i++ {
		go b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
	}
	for len(b.Pending()) < 5 {
		time.Sleep(5 * time.Millisecond)
	}
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
	for len(b.Pending()) < 3 {
		time.Sleep(5 * time.Millisecond)
	}
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
	for len(b.Pending()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	b.Decide(b.Pending()[0].ID, Decision{Outcome: Allowed})
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(*events) != 2 || (*events)[0].Kind != "pending" || (*events)[1].Kind != "decided" {
		t.Fatalf("events = %+v", *events)
	}
}

func TestRequestJSONTags(t *testing.T) {
	b, err := json.Marshal(Request{ID: "x", TimeoutSec: 5})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"id":"x"`) {
		t.Fatalf("missing id tag: %s", s)
	}
	if !strings.Contains(s, `"timeoutSec":5`) {
		t.Fatalf("missing timeoutSec tag: %s", s)
	}
}

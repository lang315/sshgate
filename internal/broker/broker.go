package broker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

type Outcome string

const (
	Allowed              Outcome = "allowed"
	Denied               Outcome = "denied"
	Expired              Outcome = "expired"
	ApprovedButCancelled Outcome = "approved_but_cancelled"
	SentToTab            Outcome = "sent_to_tab"
	Withdrawn            Outcome = "withdrawn"
)

type Request struct {
	ID          string    `json:"id"`
	Client      string    `json:"client"`
	Server      string    `json:"server"`
	Target      string    `json:"target"` // user@host:port the approval is for
	Command     string    `json:"command"`
	Description string    `json:"description"`
	Stdin       string    `json:"stdin,omitempty"`
	Sudo        bool      `json:"sudo"`
	TimeoutSec  int       `json:"timeoutSec"`
	ReceivedAt  time.Time `json:"receivedAt"`
}

type Decision struct {
	Outcome Outcome `json:"outcome"`
	Reason  string  `json:"reason"`
}

type Event struct {
	Kind     string   `json:"kind"` // "pending" | "decided"
	Request  Request  `json:"request"`
	Decision Decision `json:"decision"`
}

type Options struct {
	MaxPending int
	Expiry     time.Duration
	Now        func() time.Time
	OnEvent    func(Event)
}

var (
	ErrTooManyPending = errors.New("too many pending requests, try again later")
	ErrNotFound       = errors.New("no such pending request")
	ErrSendToTabStdin = errors.New("send to tab is not available for a command with stdin")
)

type pending struct {
	req  Request
	done chan Decision
	// announced is closed by Submit right after it fires the "pending"
	// OnEvent for this request. Every path that emits the matching "decided"
	// event waits on it first, so a consumer never observes "decided" before
	// "pending" for the same request.
	announced chan struct{}
}

type Broker struct {
	o  Options
	mu sync.Mutex
	q  []*pending // insertion order
}

func New(o Options) *Broker {
	if o.MaxPending <= 0 {
		o.MaxPending = 5
	}
	if o.Expiry <= 0 {
		o.Expiry = 5 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.OnEvent == nil {
		o.OnEvent = func(Event) {}
	}
	return &Broker{o: o}
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (b *Broker) Submit(ctx context.Context, req Request) (Decision, error) {
	req.ID = newID() // always fresh; never trust a caller-supplied ID
	if req.ReceivedAt.IsZero() {
		req.ReceivedAt = b.o.Now()
	}
	p := &pending{req: req, done: make(chan Decision, 1), announced: make(chan struct{})}

	b.mu.Lock()
	if len(b.q) >= b.o.MaxPending {
		b.mu.Unlock()
		return Decision{}, ErrTooManyPending
	}
	b.q = append(b.q, p)
	b.mu.Unlock()
	b.o.OnEvent(Event{Kind: "pending", Request: req})
	close(p.announced)

	timer := time.NewTimer(b.o.Expiry)
	defer timer.Stop()
	select {
	case d := <-p.done:
		return d, nil
	case <-timer.C:
		if b.remove(p.req.ID) != nil {
			<-p.announced
			d := Decision{Outcome: Expired, Reason: "approval timed out"}
			b.o.OnEvent(Event{Kind: "decided", Request: req, Decision: d})
			return d, nil
		}
		return <-p.done, nil // decided in the same instant
	case <-ctx.Done():
		if b.remove(p.req.ID) != nil {
			<-p.announced
			d := Decision{Outcome: Withdrawn, Reason: "cancelled by client"}
			b.o.OnEvent(Event{Kind: "decided", Request: req, Decision: d})
			return Decision{}, ctx.Err()
		}
		return <-p.done, nil
	}
}

// remove takes a request out of the queue; nil if it was not there.
func (b *Broker) remove(id string) *pending {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, p := range b.q {
		if p.req.ID == id {
			b.q = append(b.q[:i], b.q[i+1:]...)
			return p
		}
	}
	return nil
}

func (b *Broker) Pending() []Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Request, 0, len(b.q))
	for _, p := range b.q {
		out = append(out, p.req)
	}
	return out
}

func (b *Broker) Decide(id string, d Decision) error {
	if d.Outcome == SentToTab {
		b.mu.Lock()
		for _, p := range b.q {
			if p.req.ID == id && p.req.Stdin != "" {
				b.mu.Unlock()
				return ErrSendToTabStdin
			}
		}
		b.mu.Unlock()
	}
	p := b.remove(id)
	if p == nil {
		return ErrNotFound
	}
	p.done <- d
	<-p.announced // never report "decided" before the matching "pending"
	b.o.OnEvent(Event{Kind: "decided", Request: p.req, Decision: d})
	return nil
}

func (b *Broker) DenyAll(reason string) {
	b.mu.Lock()
	all := b.q
	b.q = nil
	b.mu.Unlock()
	d := Decision{Outcome: Denied, Reason: reason}
	// Deliver decisions to every waiting Submit call first, so one request
	// whose "pending" OnEvent hasn't fired yet can't stall the others.
	for _, p := range all {
		p.done <- d
	}
	for _, p := range all {
		<-p.announced
		b.o.OnEvent(Event{Kind: "decided", Request: p.req, Decision: d})
	}
}

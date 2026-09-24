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
)

type Request struct {
	ID          string    `json:"id"`
	Client      string    `json:"client"`
	Server      string    `json:"server"`
	Command     string    `json:"command"`
	Description string    `json:"description"`
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
)

type pending struct {
	req  Request
	done chan Decision
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
	if req.ID == "" {
		req.ID = newID()
	}
	if req.ReceivedAt.IsZero() {
		req.ReceivedAt = b.o.Now()
	}
	p := &pending{req: req, done: make(chan Decision, 1)}

	b.mu.Lock()
	if len(b.q) >= b.o.MaxPending {
		b.mu.Unlock()
		return Decision{}, ErrTooManyPending
	}
	b.q = append(b.q, p)
	b.mu.Unlock()
	b.o.OnEvent(Event{Kind: "pending", Request: req})

	timer := time.NewTimer(b.o.Expiry)
	defer timer.Stop()
	select {
	case d := <-p.done:
		return d, nil
	case <-timer.C:
		if b.remove(p.req.ID) != nil {
			d := Decision{Outcome: Expired, Reason: "approval timed out"}
			b.o.OnEvent(Event{Kind: "decided", Request: req, Decision: d})
			return d, nil
		}
		return <-p.done, nil // decided in the same instant
	case <-ctx.Done():
		if b.remove(p.req.ID) != nil {
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
	p := b.remove(id)
	if p == nil {
		return ErrNotFound
	}
	p.done <- d
	b.o.OnEvent(Event{Kind: "decided", Request: p.req, Decision: d})
	return nil
}

func (b *Broker) DenyAll(reason string) {
	b.mu.Lock()
	all := b.q
	b.q = nil
	b.mu.Unlock()
	for _, p := range all {
		d := Decision{Outcome: Denied, Reason: reason}
		p.done <- d
		b.o.OnEvent(Event{Kind: "decided", Request: p.req, Decision: d})
	}
}

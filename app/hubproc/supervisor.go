package hubproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// State is what the supervisor is doing: "running", "restarting" (In is
// the delay before attempt Attempt), or "failed" (Message says why).
type State struct {
	Kind    string
	Attempt int
	In      time.Duration
	Message string
}

// Supervisor keeps a hub running: after a crash it restarts it with
// backoff (1 s, 3 s, 10 s), and gives up at the MaxCrashes-th crash within
// Window. A restarted hub starts locked.
type Supervisor struct {
	Launch     Launch
	OnNote     func(string, json.RawMessage)
	OnProc     func(*Proc) // a new hub is running
	OnState    func(State)
	Backoff    []time.Duration // default 1 s, 3 s, 10 s
	Window     time.Duration   // default 60 s
	MaxCrashes int             // default 4
}

func (s *Supervisor) state(st State) {
	if s.OnState != nil {
		s.OnState(st)
	}
}

// Run blocks until ctx ends (it then stops the hub) or it gives up.
func (s *Supervisor) Run(ctx context.Context) {
	backoff, window, max := s.Backoff, s.Window, s.MaxCrashes
	if backoff == nil {
		backoff = []time.Duration{time.Second, 3 * time.Second, 10 * time.Second}
	}
	if window == 0 {
		window = time.Minute
	}
	if max == 0 {
		max = 4
	}
	var crashes []time.Time
	var tail []string
	for {
		p, err := Start(ctx, s.Launch, s.OnNote)
		if err == nil {
			s.state(State{Kind: "running"})
			if s.OnProc != nil {
				s.OnProc(p)
			}
			select {
			case <-ctx.Done():
				p.Stop()
				return
			case <-p.Done():
				tail = p.StderrTail()
			}
		} else if ctx.Err() != nil {
			return
		} else if errors.Is(err, ErrProtocol) {
			s.state(State{Kind: "failed", Message: err.Error()})
			return
		} else {
			tail = []string{err.Error()}
		}
		now := time.Now()
		kept := crashes[:0]
		for _, c := range crashes {
			if now.Sub(c) < window {
				kept = append(kept, c)
			}
		}
		crashes = append(kept, now)
		if len(crashes) >= max {
			s.state(State{Kind: "failed", Message: fmt.Sprintf("the hub crashed %d times in %d s\n%s", len(crashes), int(window.Seconds()), strings.Join(tail, "\n"))})
			return
		}
		in := backoff[min(len(crashes)-1, len(backoff)-1)]
		s.state(State{Kind: "restarting", Attempt: len(crashes), In: in})
		select {
		case <-ctx.Done():
			return
		case <-time.After(in):
		}
	}
}

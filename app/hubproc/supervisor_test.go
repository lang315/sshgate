package hubproc

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestSupervisorGivesUp: a hub that dies at once is restarted with backoff
// and given up on at the 4th crash in the window.
func TestSupervisorGivesUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	var mu sync.Mutex
	var states []State
	s := &Supervisor{
		Launch:     Launch{Command: "/bin/sh", Args: []string{"-c", "exit 1"}},
		Backoff:    []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond},
		Window:     time.Minute,
		MaxCrashes: 4,
		OnState:    func(st State) { mu.Lock(); states = append(states, st); mu.Unlock() },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.Run(ctx)
	mu.Lock()
	defer mu.Unlock()
	if len(states) == 0 || states[len(states)-1].Kind != "failed" {
		t.Fatalf("states %+v", states)
	}
	restarts := 0
	for _, st := range states {
		if st.Kind == "restarting" {
			restarts++
		}
	}
	if restarts != 3 {
		t.Fatalf("%d restarts, want 3: %+v", restarts, states)
	}
}

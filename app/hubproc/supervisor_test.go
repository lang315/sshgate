package hubproc

import (
	"context"
	"runtime"
	"strings"
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

// TestSupervisorProtocolMismatchIsFatal: a hub with another protocol is not
// restarted.
func TestSupervisorProtocolMismatchIsFatal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	script := `read line; id=$(printf '%s' "$line" | sed 's/.*"id":\([0-9]*\).*/\1/'); printf '{"jsonrpc":"2.0","id":%s,"result":{"protocol":11}}\n' "$id"; sleep 5`
	var states []State
	s := &Supervisor{
		Launch:  Launch{Command: "/bin/sh", Args: []string{"-c", script}},
		Backoff: []time.Duration{time.Millisecond},
		OnState: func(st State) { states = append(states, st) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s.Run(ctx)
	if len(states) != 1 || states[0].Kind != "failed" || !strings.Contains(states[0].Message, "protocol 11") {
		t.Fatalf("states %+v", states)
	}
}

// TestSupervisorProcBeforeRunning: OnProc stores the new hub's client before
// the running state, whose handler calls that hub.
func TestSupervisorProcBeforeRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	script := `read line; id=$(printf '%s' "$line" | sed 's/.*"id":\([0-9]*\).*/\1/'); printf '{"jsonrpc":"2.0","id":%s,"result":{"protocol":12}}\n' "$id"; sleep 5`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var mu sync.Mutex
	var order []string
	s := &Supervisor{
		Launch: Launch{Command: "/bin/sh", Args: []string{"-c", script}},
		OnProc: func(*Proc) { mu.Lock(); order = append(order, "proc"); mu.Unlock() },
		OnState: func(st State) {
			mu.Lock()
			order = append(order, st.Kind)
			mu.Unlock()
			if st.Kind == "running" {
				cancel()
			}
		},
	}
	s.Run(ctx)
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 || order[0] != "proc" || order[1] != "running" {
		t.Fatalf("order %v, want proc then running", order)
	}
}

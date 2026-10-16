package view

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubproc"
)

type fakeDoor struct {
	mu    sync.Mutex
	calls []string
	pw    string
}

func (d *fakeDoor) Call(_ context.Context, method string, params, result any) error {
	d.mu.Lock()
	d.calls = append(d.calls, method)
	d.mu.Unlock()
	switch method {
	case "unlock":
		var p struct{ Password string }
		b, _ := json.Marshal(params)
		json.Unmarshal(b, &p)
		if p.Password != d.pw {
			return errors.New("wrong master password")
		}
	case "servers":
		return json.Unmarshal([]byte(`[{"name":"box"},{"name":"other"}]`), result)
	}
	return nil
}

// tester runs App headless; updates from goroutines queue until flush.
type tester struct {
	*ui.Tester
	t       *testing.T
	mu      sync.Mutex
	pending []func()
}

func newTester(t *testing.T, a *App) *tester {
	tt := &tester{t: t}
	a.Update = func(fn func()) { tt.mu.Lock(); tt.pending = append(tt.pending, fn); tt.mu.Unlock() }
	tt.Tester = ui.NewTester(a.Build, 800, 500)
	return tt
}

// flush runs queued updates (unlock runs off the UI thread) until none
// has come for 100 ms, or 2 s have passed.
func (tt *tester) flush() {
	deadline := time.Now().Add(2 * time.Second)
	for idle := 0; idle < 10 && time.Now().Before(deadline); {
		tt.mu.Lock()
		fns := tt.pending
		tt.pending = nil
		tt.mu.Unlock()
		for _, fn := range fns {
			fn()
		}
		tt.Frame()
		if len(fns) == 0 {
			idle++
		} else {
			idle = 0
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUnlockWrongPassword(t *testing.T) {
	d := &fakeDoor{pw: "right"}
	a := &App{Door: d, Now: time.Now}
	tt := newTester(t, a)
	tt.Type("wrong")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if !tt.HasText("wrong master password") {
		t.Fatalf("no error shown: %q", tt.Texts())
	}
	if !tt.Focused("Master password") {
		t.Fatal("the field lost the focus")
	}
	for _, s := range tt.Texts() {
		if s == "wrong" {
			t.Fatal("the password shows as text")
		}
	}
}

func TestUnlockOpensFirstServer(t *testing.T) {
	d := &fakeDoor{pw: "right"}
	var opened []string
	a := &App{Door: d, Now: time.Now, OpenTab: func(server string) (*terminal.Terminal, error) {
		opened = append(opened, server)
		return nil, errors.New("no terminal in this test")
	}}
	tt := newTester(t, a)
	tt.Type("right")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if len(opened) != 1 || opened[0] != "box" {
		t.Fatalf("opened %q", opened)
	}
	if !tt.HasText("no terminal in this test") {
		t.Fatalf("open error not shown: %q", tt.Texts())
	}
}

func TestHubFailureShownOnUnlockScreen(t *testing.T) {
	a := &App{Door: &fakeDoor{}, Now: time.Now}
	tt := newTester(t, a)
	a.HubState(hubproc.State{Kind: "restarting", Attempt: 2, In: time.Second})
	tt.Frame()
	if !tt.HasText("hub restarting (attempt 2)") {
		t.Fatalf("no restart note: %q", tt.Texts())
	}
	a.HubState(hubproc.State{Kind: "failed", Message: "hub protocol mismatch: hub speaks protocol 13, this app speaks 12"})
	tt.Frame()
	if !tt.HasText("hub protocol mismatch: hub speaks protocol 13, this app speaks 12") {
		t.Fatalf("failure not shown: %q", tt.Texts())
	}
	a.HubState(hubproc.State{Kind: "failed", Message: "the hub crashed 4 times in 60 s\nlast stderr line"})
	tt.Frame()
	if !tt.HasText("last stderr line") {
		t.Fatalf("stderr tail not shown: %q", tt.Texts())
	}
	a.HubState(hubproc.State{Kind: "running"})
	tt.Frame()
	if tt.HasText("last stderr line") {
		t.Fatal("running left the failure on screen")
	}
}

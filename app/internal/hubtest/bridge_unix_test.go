//go:build unix

package hubtest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/internal/hubtest"
)

// The bridge reaches the hub's MCP door: a locked hub lists box locked, an
// exec waits for the decision, and a denial comes back as the tool's error.
func TestBridgeRunsToolsThroughTheHub(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
	env := hubtest.Env(t)
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: []string{"hub", "--store=" + store}, Env: env}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx := context.Background()
	h := hubapi.New(p.Client())
	b := hubtest.StartBridge(t, env)
	if r := <-b.Tool("list-servers", map[string]any{}); r.Err != nil || r.Stdout != "- box (127.0.0.1) [locked: unlock the app]\n" {
		t.Fatalf("list-servers while locked: %+v", r)
	}
	if _, err := h.Unlock(ctx, hubtest.Password); err != nil {
		t.Fatal(err)
	}
	decide := func(outcome, reason string) {
		t.Helper()
		var reqs []hubapi.Request
		for deadline := time.Now().Add(5 * time.Second); len(reqs) == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			reqs, _ = h.Pending(ctx)
		}
		if len(reqs) != 1 {
			t.Fatalf("pending %+v", reqs)
		}
		if err := h.Decide(ctx, reqs[0].ID, outcome, reason); err != nil {
			t.Fatal(err)
		}
	}
	res := b.Tool("exec", map[string]any{"server": "box", "command": "echo approved", "description": "bridge test"})
	decide(hubapi.Allowed, "")
	if r := <-res; r.Err != nil || r.ExitCode != 0 || !strings.Contains(r.Stdout, "echo approved") {
		t.Fatalf("%+v", r)
	}
	res = b.Tool("exec", map[string]any{"server": "box", "command": "rm -rf /", "description": "bridge test"})
	decide(hubapi.Denied, "not today")
	if r := <-res; r.Err == nil || r.Err.Error() != "Denied by user: not today" {
		t.Fatalf("%+v", r)
	}
}

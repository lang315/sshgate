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

func TestExecWaitsForApprovalThenRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
	env, dir, _ := hubtest.Runtime(t)
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: []string{"hub", "--store=" + store}, Env: env}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx := context.Background()
	h := hubapi.New(p.Client())
	if _, err := h.Unlock(ctx, hubtest.Password); err != nil {
		t.Fatal(err)
	}
	res := hubtest.Exec(hubtest.MCPSocket(dir), map[string]any{"requestId": "r1", "client": "test", "server": "box", "command": "echo approved"})
	var reqs []hubapi.Request
	for deadline := time.Now().Add(5 * time.Second); len(reqs) == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		reqs, _ = h.Pending(ctx)
	}
	if len(reqs) != 1 || reqs[0].Command != "echo approved" {
		t.Fatalf("pending %+v", reqs)
	}
	if err := h.Decide(ctx, reqs[0].ID, hubapi.Allowed, ""); err != nil {
		t.Fatal(err)
	}
	r := <-res
	if r.Err != nil || r.ExitCode != 0 || !strings.Contains(r.Stdout, "echo approved") {
		t.Fatalf("%+v", r)
	}
	res = hubtest.Exec(hubtest.MCPSocket(dir), map[string]any{"client": "test", "server": "box", "command": "rm -rf /"})
	reqs = nil
	for deadline := time.Now().Add(5 * time.Second); len(reqs) != 1 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		reqs, _ = h.Pending(ctx)
	}
	if len(reqs) != 1 {
		t.Fatalf("second request not pending: %+v", reqs)
	}
	if err := h.Decide(ctx, reqs[0].ID, hubapi.Denied, "not today"); err != nil {
		t.Fatal(err)
	}
	if r := <-res; r.Err == nil || !strings.Contains(r.Err.Error(), "Denied by user: not today") {
		t.Fatalf("%+v", r)
	}
}

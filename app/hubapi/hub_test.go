package hubapi

import (
	"context"
	"testing"

	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/internal/hubtest"
)

func TestAgainstRealHub(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: []string{"hub", "--store=" + store}, Env: hubtest.Env(t)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx := context.Background()
	h := New(p.Client())
	s, err := h.Status(ctx)
	if err != nil || !s.Locked || !s.HasVault || s.StorePath == "" {
		t.Fatalf("status %+v %v", s, err)
	}
	if _, err := h.Unlock(ctx, "wrong"); err == nil || err.Error() != "wrong master password" {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := h.Unlock(ctx, hubtest.Password); err != nil {
		t.Fatal(err)
	}
	servers, err := h.Servers(ctx)
	if err != nil || len(servers) != 1 || servers[0].Name != "box" || !servers[0].AIVisible || servers[0].HostKey == "" {
		t.Fatalf("servers %+v %v", servers, err)
	}
	if p, err := h.Pending(ctx); err != nil || len(p) != 0 {
		t.Fatalf("pending %v %v", p, err)
	}
	if err := h.Decide(ctx, "nope", Allowed, ""); err == nil || err.Error() != "no such pending request" {
		t.Fatalf("decide unknown: %v", err)
	}
	if err := h.Lock(ctx, false); err != nil {
		t.Fatal(err)
	}
	if s, _ := h.Status(ctx); !s.Locked {
		t.Fatal("not locked after Lock")
	}
}

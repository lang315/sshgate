package termconn

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/internal/hubtest"
)

// TestTrustThroughHub: an unpinned server answers hostKeyUnknown, and the
// retry with trust on the same id opens it and pins the key.
func TestTrustThroughHub(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	// The hub gets a temp home: the prompt's known_hosts hint comes from
	// its .ssh, never from the real one.
	env, _, home := hubtest.Runtime(t)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := hubtest.Start(t, hubtest.Options{KnownHosts: filepath.Join(home, ".ssh", "known_hosts")}).Store
	bin, _ := hubtest.Binaries(t)
	mux := NewMux()
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: []string{"hub", "--store=" + store}, Env: env}, func(m string, raw json.RawMessage) { mux.Note(m, raw) })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx := context.Background()
	h := hubapi.New(p.Client())
	if _, err := h.Unlock(ctx, hubtest.Password); err != nil {
		t.Fatal(err)
	}
	first, err := Open(ctx, p.Client(), mux, "t1", "box", 24, 80, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ForgetHostKey(ctx, "box"); err != nil {
		t.Fatal(err)
	}
	stop := time.AfterFunc(10*time.Second, func() { first.Close() })
	defer stop.Stop()
	if got := readAll(t, first); !strings.Contains(got, "[exited: ") || first.ExitReason() == "" {
		t.Fatalf("forgetting the key must end box's terminal with a reason; read %q", got)
	}

	_, err = Open(ctx, p.Client(), mux, "t2", "box", 24, 80, nil)
	var oe *OpenError
	if !errors.As(err, &oe) || oe.Status != "hostKeyUnknown" || !strings.HasPrefix(oe.Key.Fingerprint, "SHA256:") ||
		oe.Key.KeyType == "" || oe.Key.KnownHosts != "match" || oe.Key.Server != "box" || oe.Key.User != "test" || oe.Key.Host != "127.0.0.1" || oe.Key.Port == 0 {
		t.Fatalf("open unpinned: %#v", err)
	}
	c, err := Open(ctx, p.Client(), mux, "t2", "box", 24, 80, &hubapi.Trust{Fingerprint: oe.Key.Fingerprint, KeyType: oe.Key.KeyType})
	if err != nil {
		t.Fatalf("trusted retry: %v", err)
	}
	defer c.Close()
	servers, err := h.Servers(ctx)
	if err != nil || servers[0].HostKey != oe.Key.Fingerprint {
		t.Fatalf("pin after trust: %+v %v", servers, err)
	}
}

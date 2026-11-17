package sekey_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/lang315/sshgate/internal/sekey"
)

// Opt-in: SSHGATE_LIVE_SE=1 creates a real Secure Enclave key on this Mac.
func TestLiveSecureEnclave(t *testing.T) {
	if os.Getenv("SSHGATE_LIVE_SE") != "1" {
		t.Skip("set SSHGATE_LIVE_SE=1 to use the real Secure Enclave")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	helper := os.Getenv("SSHGATE_TOUCHID_HELPER")
	if helper == "" {
		helper = filepath.Join(root, "sshgate-touchid")
	}
	ctx := context.Background()
	blob, pub, err := sekey.Create(ctx, helper)
	if err != nil {
		t.Fatal(err)
	}
	spub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	sign := func() time.Duration {
		data := []byte("live check")
		start := time.Now()
		sig, err := sekey.NewSigner(helper, blob, spub).Sign(nil, data)
		d := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if err := spub.Verify(data, sig); err != nil {
			t.Fatal(err)
		}
		return d
	}
	ds := make([]time.Duration, 20)
	for i := range ds {
		ds[i] = sign()
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	median := ds[len(ds)/2]
	t.Logf("sign x20: median=%v max=%v", median, ds[len(ds)-1])
	if median > 500*time.Millisecond {
		t.Fatalf("median %v over 500ms", median)
	}

	// A rebuilt helper must still use the old blob.
	if err := os.WriteFile(filepath.Join(t.TempDir(), "blob"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", filepath.Join(root, "app/scripts/build-touchid.sh")).CombinedOutput(); err != nil {
		t.Fatalf("rebuild: %v\n%s", err, out)
	}
	t.Logf("after rebuild: sign took %v, verified", sign())
}

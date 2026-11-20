package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func inboundKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func TestCanonInboundIP(t *testing.T) {
	for in, want := range map[string]string{
		"100.64.0.5": "100.64.0.5", " 100.64.0.5 ": "100.64.0.5", "10.1.2.3/8": "10.0.0.0/8",
		"FD7A:115C::1": "fd7a:115c::1", "::ffff:1.2.3.4": "1.2.3.4",
	} {
		if got, err := CanonInboundIP(in); err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0.0.0.0/0", "::/0", "1.2.3", "host.example", "1.2.3.4,5.6.7.8", `1.2.3.4"`, "fe80::1%en0", "1.2.3.4/33", "!1.2.3.4", "*"} {
		if got, err := CanonInboundIP(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

func TestCanonInboundKey(t *testing.T) {
	k := inboundKey(t)
	if got, err := CanonInboundKey(k + " iphone@herdr\n"); err != nil || got != k {
		t.Fatalf("comment not dropped: %q %v", got, err)
	}
	for _, in := range []string{"", "garbage", `command="x" ` + k, "from=\"1.2.3.4\" " + k, k + "\n" + k, "restrict " + k, k + `" x`} {
		if got, err := CanonInboundKey(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
	if CanonFP := InboundFingerprint(k); !strings.HasPrefix(CanonFP, "SHA256:") {
		t.Fatalf("fingerprint %q", CanonFP)
	}
}

func TestInboundAddDeleteSetIP(t *testing.T) {
	f := &File{Version: 1}
	now := time.Now()
	k1, k2 := inboundKey(t), inboundKey(t)
	if err := AddInbound(f, "bad name!", k1, "1.2.3.4", now); err == nil || !strings.Contains(err.Error(), "device name") {
		t.Fatalf("bad name: %v", err)
	}
	if err := AddInbound(f, "ios", k1, "100.64.0.5", now); err != nil {
		t.Fatal(err)
	}
	if err := AddInbound(f, "ios", k2, "1.2.3.4", now); !errors.Is(err, ErrInboundExists) {
		t.Fatalf("dup name: %v", err)
	}
	if err := AddInbound(f, "other", k1+" c", "1.2.3.4", now); !errors.Is(err, ErrInboundKeyDup) {
		t.Fatalf("dup key: %v", err)
	}
	if err := AddInbound(f, "other", k2, "0.0.0.0/0", now); err == nil || len(f.Inbound) != 1 {
		t.Fatalf("any-address accepted: %v", err)
	}
	d := func() InboundDevice { return f.Inbound[0] }
	if _, err := SetInboundIP(f, "ios", "100.64.0.5", "blocked"); err != nil || len(d().Allowed) != 0 || len(d().Blocked) != 1 {
		t.Fatalf("block: %v %+v", err, d())
	}
	if _, err := SetInboundIP(f, "ios", "100.64.0.6", "allowed"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetInboundIP(f, "ios", "100.64.0.5", "allowed"); err != nil || len(d().Blocked) != 0 || len(d().Allowed) != 2 {
		t.Fatalf("unblock: %v %+v", err, d())
	}
	if _, err := SetInboundIP(f, "ios", "100.64.0.5", "allowed"); err != nil || len(d().Allowed) != 2 {
		t.Fatalf("repeat allow duplicated: %+v", d())
	}
	if _, err := SetInboundIP(f, "ios", "100.64.0.5", "removed"); err != nil || len(d().Allowed) != 1 {
		t.Fatalf("remove: %v %+v", err, d())
	}
	if _, err := SetInboundIP(f, "ios", "100.64.0.5", "removed"); !errors.Is(err, ErrNoInboundIP) {
		t.Fatalf("remove unknown: %v", err)
	}
	if _, err := SetInboundIP(f, "ios", "100.64.0.6", "maybe"); err == nil {
		t.Fatal("bad state accepted")
	}
	if _, err := SetInboundIP(f, "nope", "100.64.0.6", "allowed"); !errors.Is(err, ErrNoInbound) {
		t.Fatalf("no device: %v", err)
	}
	for i := 1; len(d().Allowed)+len(d().Blocked) < MaxInboundIPs; i++ {
		if _, err := SetInboundIP(f, "ios", fmt.Sprintf("10.0.0.%d", i), "blocked"); err != nil {
			t.Fatalf("under cap: %v", err)
		}
	}
	if _, err := SetInboundIP(f, "ios", "9.9.9.9", "allowed"); !errors.Is(err, ErrInboundIPCap) {
		t.Fatalf("cap: %v", err)
	}
	if err := DeleteInbound(f, "ios"); err != nil || len(f.Inbound) != 0 {
		t.Fatalf("delete: %v", err)
	}
	if err := DeleteInbound(f, "ios"); !errors.Is(err, ErrNoInbound) {
		t.Fatalf("delete again: %v", err)
	}
}

// The list is under the vault MAC: an edited device list fails to load.
func TestInboundUnderMAC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	k, mk, err := NewKDF("pw")
	if err != nil {
		t.Fatal(err)
	}
	f := &File{Version: 1, KDF: &k}
	if err := AddInbound(f, "ios", inboundKey(t), "100.64.0.5", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, f, mk); err != nil {
		t.Fatal(err)
	}
	g, err := Load(path)
	if err != nil || g.VerifyMAC(mk) != nil || len(g.Inbound) != 1 {
		t.Fatalf("round trip: %v", err)
	}
	g.Inbound[0].Allowed = append(g.Inbound[0].Allowed, "6.6.6.6")
	if g.VerifyMAC(mk) == nil {
		t.Fatal("an edited device list passed the MAC")
	}
}

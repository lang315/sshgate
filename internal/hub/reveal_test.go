package hub

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
)

// revealHub is newEncHub unlocked with its own key: server "enc" holds
// password "s3cr3t-pw" under master password "pw".
func revealHub(t *testing.T) (*Hub, string, []byte) {
	t.Helper()
	h, path, mk := newEncHub(t, &fakeExec{})
	h.mu.Lock()
	h.deps.MasterKey = bytes.Clone(mk)
	h.unlocked.Store(true)
	h.mu.Unlock()
	return h, path, mk
}

func reveal(c *rpc.Client, server, field, pw string) (string, error) {
	var r struct {
		Value string `json:"value"`
	}
	err := c.Call(context.Background(), "servers.reveal", map[string]string{"server": server, "field": field, "password": pw}, &r)
	return r.Value, err
}

func revealRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	_, recs := readAudit(t, path)
	var out []map[string]any
	for _, r := range recs {
		if a, _ := r["action"].(string); strings.HasPrefix(a, "secretReveal") {
			out = append(out, r)
		}
	}
	return out
}

func TestRevealShows(t *testing.T) {
	h, path, _ := revealHub(t)
	c, _ := startUI(t, h)
	v, err := reveal(c, "enc", "password", "pw")
	if err != nil || v != "s3cr3t-pw" {
		t.Fatalf("reveal = %q, %v", v, err)
	}
	recs := revealRecords(t, path)
	if len(recs) != 1 || recs[0]["kind"] != "config" || recs[0]["action"] != "secretReveal" ||
		recs[0]["server"] != "enc" || recs[0]["field"] != "password" {
		t.Fatalf("records = %v", recs)
	}
	raw, _ := readAudit(t, path)
	if strings.Contains(raw, "s3cr3t-pw") || strings.Contains(raw, `"pw"`) {
		t.Fatalf("audit holds the secret or the master password: %s", raw)
	}
}

func TestRevealEachField(t *testing.T) {
	h, path, mk := revealHub(t)
	want := map[string]string{"password": "p1", "suPassword": "p2", "sudoPassword": "p3", "keyPassphrase": "p4"}
	h.mu.Lock()
	f := h.deps.File
	s := config.Server{Name: "all", Host: "h", Port: 22, User: "u", Auth: "password"}
	for wire, pt := range want {
		stored := revealFields[wire]
		blob, err := config.Encrypt(mk, s.Name+"/"+stored, config.AADFor(f, s, stored), pt)
		if err != nil {
			t.Fatal(err)
		}
		*encField(&s, stored) = blob
	}
	f.Servers = append(slices.Clone(f.Servers), s)
	h.mu.Unlock()
	_ = path
	c, _ := startUI(t, h)
	for wire, pt := range want {
		if v, err := reveal(c, "all", wire, "pw"); err != nil || v != pt {
			t.Errorf("%s = %q, %v; want %q", wire, v, err, pt)
		}
	}
}

func TestRevealWrongPassword(t *testing.T) {
	h, path, _ := revealHub(t)
	c, _ := startUI(t, h)
	if _, err := reveal(c, "enc", "password", "nope"); err == nil || !strings.Contains(err.Error(), "wrong master password") {
		t.Fatalf("err = %v", err)
	}
	recs := revealRecords(t, path)
	if len(recs) != 1 || recs[0]["action"] != "secretRevealDenied" || recs[0]["field"] != "password" {
		t.Fatalf("records = %v", recs)
	}
}

func TestRevealRefusals(t *testing.T) {
	h, path, _ := revealHub(t)
	c, _ := startUI(t, h)
	for _, tc := range []struct{ server, field, want string }{
		{"ghost", "password", "no such server"},
		{"agent", "password", "no saved password"},
		{"enc", "sudoPassword", "no saved sudoPassword"},
	} {
		if _, err := reveal(c, tc.server, tc.field, "pw"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s/%s: %v, want %q", tc.server, tc.field, err, tc.want)
		}
	}
	if err := c.Call(context.Background(), "lock", map[string]any{"stopAuto": true}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := reveal(c, "enc", "password", "pw"); err == nil || !strings.Contains(err.Error(), ErrLocked.Error()) {
		t.Errorf("locked: %v", err)
	}
	if recs := revealRecords(t, path); len(recs) != 0 {
		t.Errorf("refusals wrote records: %v", recs)
	}

	nh, _ := newHubAt(t, nil, nil)
	nc, _ := startUI(t, nh)
	if _, err := reveal(nc, "enc", "password", "pw"); err == nil || !strings.Contains(err.Error(), errNoVault.Error()) {
		t.Errorf("no vault: %v", err)
	}
}

func TestRevealStrictParams(t *testing.T) {
	h, _, _ := revealHub(t)
	c, _ := startUI(t, h)
	for _, p := range []map[string]any{
		{"server": "enc", "field": "password", "password": "pw", "x": 1},
		{"server": "enc", "password": "pw"},
		{"server": "enc", "field": "encPassword", "password": "pw"},
	} {
		var re *rpc.Error
		if err := c.Call(context.Background(), "servers.reveal", p, nil); !errors.As(err, &re) || re.Code != -32602 {
			t.Errorf("%v: %v", p, err)
		}
	}
}

// startReveal runs a reveal in the background with the derive blocked.
func startReveal(t *testing.T, h *Hub) (started, release chan struct{}, done chan error) {
	t.Helper()
	started, release = blockDerive(t)
	done = make(chan error, 1)
	go func() {
		_, err := h.RevealSecret("enc", "password", "pw")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("derive did not start")
	}
	return started, release, done
}

func TestRevealLockDuringDerive(t *testing.T) {
	h, _, _ := revealHub(t)
	_, release, done := startReveal(t, h)
	h.Lock()
	close(release)
	if err := <-done; !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
}

func TestRevealServerChangesDuringDerive(t *testing.T) {
	for name, change := range map[string]func(f []config.Server) []config.Server{
		"deleted": func(f []config.Server) []config.Server {
			return slices.DeleteFunc(f, func(s config.Server) bool { return s.Name == "enc" })
		},
		// The host moved without re-keying: the stored blob no longer
		// opens, which proves the fresh copy is the one decrypted.
		"moved": func(f []config.Server) []config.Server {
			f[slices.IndexFunc(f, func(s config.Server) bool { return s.Name == "enc" })].Host = "other"
			return f
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, _, _ := revealHub(t)
			_, release, done := startReveal(t, h)
			h.mu.Lock()
			h.deps.File.Servers = change(slices.Clone(h.deps.File.Servers))
			h.mu.Unlock()
			close(release)
			if err := <-done; err == nil {
				t.Fatal("reveal succeeded after the server changed")
			}
		})
	}
}

func TestRevealOneAtATime(t *testing.T) {
	h, _, _ := revealHub(t)
	_, release, done := startReveal(t, h)
	if _, err := h.RevealSecret("enc", "password", "pw"); !errors.Is(err, ErrRevealBusy) {
		t.Errorf("second reveal: %v, want ErrRevealBusy", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRevealNotOnMCPDoor(t *testing.T) {
	h, _, _ := revealHub(t)
	c := startDoor(t, h)
	var re *rpc.Error
	err := c.Call(context.Background(), "servers.reveal", map[string]string{"server": "enc", "field": "password", "password": "pw"}, nil)
	if !errors.As(err, &re) || re.Code != -32601 {
		t.Fatalf("MCP door servers.reveal: %v", err)
	}
}

// A KDF change during the derive (a vault.create elsewhere) refuses with
// reveal wording, not unlock's.
func TestRevealKDFChangesDuringDerive(t *testing.T) {
	h, _, _ := revealHub(t)
	_, release, done := startReveal(t, h)
	h.mu.Lock()
	k := *h.deps.File.KDF
	k.Time++
	h.deps.File.KDF = &k
	h.mu.Unlock()
	close(release)
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "the vault changed while the password was checked") {
		t.Fatalf("err = %v", err)
	}
}

// A key held with no KDF in the file is refused, never a panic.
func TestRevealNoKDFWithAKey(t *testing.T) {
	h, _, _ := revealHub(t)
	h.mu.Lock()
	h.deps.File.KDF = nil
	h.mu.Unlock()
	if _, err := h.RevealSecret("enc", "password", "pw"); !errors.Is(err, errNoVault) {
		t.Fatalf("err = %v, want errNoVault", err)
	}
}

package hub

import (
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshconfig"
)

type importFixture struct {
	h              *Hub
	c              *rpc.Client
	store, key, fp string
}

// importHub is an unlocked vault whose SSH config has web (key auth, its key
// in known_hosts) and jump (behind a ProxyJump).
func importHub(t *testing.T) importFixture {
	t.Helper()
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no OpenSSH client on PATH")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "id_web") // import parses it but never dials with it
	pubKey, priv, _ := ed25519.GenerateKey(nil)
	hk, _ := ssh.NewPublicKey(pubKey)
	blk, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	kh := filepath.Join(dir, "known_hosts")
	cfg := filepath.Join(dir, "config")
	for p, body := range map[string]string{
		key: string(pem.EncodeToMemory(blk)),
		kh:  knownhosts.Line([]string{knownhosts.Normalize("10.0.0.5:2200")}, hk) + "\n",
		cfg: strings.Join([]string{
			"Host web", "  HostName 10.0.0.5", "  Port 2200", "  User deploy", "  IdentityFile " + key,
			"Host jump", "  ProxyJump web",
			"Host *", "  IdentityFile " + filepath.Join(dir, "missing"), "  UserKnownHostsFile " + kh, "",
		}, "\n"),
	} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h, store := newHubAt(t, nil, nil)
	h.o.SSHConfigPath = cfg
	c, _ := startUI(t, h)
	if err := c.Call(context.Background(), "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	return importFixture{h, c, store, key, ssh.FingerprintSHA256(hk)}
}

type scanResult struct {
	Candidates []sshconfig.Candidate `json:"candidates"`
	Note       string                `json:"note"`
}

type applyResult struct {
	Imported []string `json:"imported"`
	Skipped  []struct {
		Alias  string `json:"alias"`
		Reason string `json:"reason"`
	} `json:"skipped"`
}

func TestImportScanAndApply(t *testing.T) {
	fx := importHub(t)
	ctx := context.Background()
	var scan scanResult
	if err := fx.c.Call(ctx, "import.scan", nil, &scan); err != nil {
		t.Fatal(err)
	}
	web := sshconfig.Candidate{Alias: "web", Host: "10.0.0.5", Port: 2200, User: "deploy", Auth: "key", KeyPath: fx.key,
		HostKey: fx.fp, HostKeyAlgo: "ssh-ed25519", Status: "ready"}
	if len(scan.Candidates) != 2 || scan.Candidates[0] != web ||
		scan.Candidates[1].Status != "skipped" || scan.Candidates[1].Reason != "needs ProxyJump" {
		t.Fatalf("scan %+v", scan)
	}

	before, _ := config.Load(fx.store)
	// Only aliases count: a planted host or pin in the params is ignored.
	params := map[string]any{"aliases": []string{"web", "jump", "nothere", "web"}, "host": "evil.example", "hostKey": "SHA256:planted"}
	var res applyResult
	if err := fx.c.Call(ctx, "import.apply", params, &res); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Imported, []string{"web"}) || len(res.Skipped) != 2 ||
		res.Skipped[0].Alias != "nothere" || res.Skipped[0].Reason != "not in the SSH config" ||
		res.Skipped[1].Alias != "jump" || res.Skipped[1].Reason != "needs ProxyJump" {
		t.Fatalf("apply %+v", res)
	}
	after, _ := config.Load(fx.store)
	if after.Revision != before.Revision+1 {
		t.Fatalf("revision %d → %d, want exactly one write", before.Revision, after.Revision)
	}
	want := config.Server{Name: "web", Host: "10.0.0.5", Port: 2200, User: "deploy", Auth: "key", KeyPath: fx.key,
		HostKey: fx.fp, HostKeyAlgo: "ssh-ed25519"}
	if s, ok := after.FindServer("web"); !ok || s != want {
		t.Fatalf("stored %+v, want %+v", s, want)
	}
	if _, ok := after.FindServer("jump"); ok {
		t.Fatal("jump was imported")
	}

	var imports []map[string]any
	_, recs := readAudit(t, fx.store)
	for _, r := range recs {
		if r["action"] == "import" {
			imports = append(imports, r)
		}
	}
	if len(imports) != 1 || imports[0]["kind"] != "config" || imports[0]["server"] != "web" || imports[0]["host"] != "10.0.0.5" ||
		imports[0]["port"] != float64(2200) || imports[0]["fingerprint"] != fx.fp || imports[0]["algo"] != "ssh-ed25519" {
		t.Fatalf("audit %v", imports)
	}

	// Again: web is in the vault now, so nothing is written.
	if err := fx.c.Call(ctx, "import.apply", map[string]any{"aliases": []string{"web"}}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Reason != "Already in vault" {
		t.Fatalf("second apply %+v", res)
	}
	if again, _ := config.Load(fx.store); again.Revision != after.Revision {
		t.Fatal("an import with nothing ready wrote the vault")
	}
	if err := fx.c.Call(ctx, "import.scan", nil, &scan); err != nil || scan.Candidates[0].Status != "exists" {
		t.Fatalf("rescan %v %+v", err, scan)
	}
}

func TestImportApplyStopsWhenLockedMidway(t *testing.T) {
	fx := importHub(t)
	dir := t.TempDir()
	started, proceed := filepath.Join(dir, "started"), filepath.Join(dir, "go")
	cfg := fx.h.o.SSHConfigPath
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// ssh -G runs this while resolving web, so the test can lock mid-apply.
	gate := fmt.Sprintf("Match exec \"touch %s; while [ ! -e %s ]; do sleep 0.05; done\"\n", started, proceed)
	if err := os.WriteFile(cfg, append([]byte(gate), b...), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := config.Load(fx.store)
	errc := make(chan error, 1)
	go func() {
		errc <- fx.c.Call(context.Background(), "import.apply", map[string]any{"aliases": []string{"web"}}, nil)
	}()
	for deadline := time.Now().Add(4 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ssh -G never ran the Match exec")
		}
	}
	fx.h.Lock()
	if err := os.WriteFile(proceed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err == nil || !strings.Contains(err.Error(), ErrLocked.Error()) {
		t.Fatalf("want %v, got %v", ErrLocked, err)
	}
	if after, _ := config.Load(fx.store); after.Revision != before.Revision {
		t.Fatal("an apply locked midway wrote the vault")
	}
}

func TestImportNeedsUnlockedVault(t *testing.T) {
	h, _ := newHubAt(t, nil, nil)
	h.o.SSHConfigPath = filepath.Join(t.TempDir(), "config")
	c, _ := startUI(t, h)
	ctx := context.Background()
	call := func(want string) {
		t.Helper()
		for _, m := range []string{"import.scan", "import.apply"} {
			if err := c.Call(ctx, m, map[string]any{"aliases": []string{"web"}}, nil); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: want %q, got %v", m, want, err)
			}
		}
	}
	call("create a vault first")
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "lock", nil, nil); err != nil {
		t.Fatal(err)
	}
	call(ErrLocked.Error())
}

func TestImportScanWithoutConfig(t *testing.T) {
	h, _ := newHubAt(t, nil, nil)
	h.o.SSHConfigPath = filepath.Join(t.TempDir(), "none")
	c, _ := startUI(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	var scan scanResult
	if err := c.Call(ctx, "import.scan", nil, &scan); err != nil || scan.Note != "No ~/.ssh/config" || len(scan.Candidates) != 0 {
		t.Fatalf("%v %+v", err, scan)
	}
}

func TestImportWithoutSSHClient(t *testing.T) {
	h, _ := newHubAt(t, nil, nil)
	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(cfg, []byte("Host web\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.o.SSHConfigPath = cfg
	c, _ := startUI(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	if err := c.Call(ctx, "import.scan", nil, nil); err == nil || !strings.Contains(err.Error(), "OpenSSH client (ssh) not found") {
		t.Fatalf("want no-ssh error, got %v", err)
	}
}

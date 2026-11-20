package hub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"golang.org/x/crypto/ssh"
)

// TestMain points HOME at an empty folder: the default authorized_keys is
// ~/.ssh/authorized_keys, and no test may read or rewrite the real one.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "hub-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

func inboundPub(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := ssh.NewPublicKey(pub)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

const testBin = "/opt/sshgate/sshgate"

// inboundHub is an unlocked hub with an audit file and its own authorized_keys path.
func inboundHub(t *testing.T) (h *Hub, ak, dir string) {
	t.Helper()
	dir = t.TempDir()
	path := filepath.Join(dir, "servers.json")
	kdf, mk := testVault(t)
	if err := config.Save(path, &config.File{Version: 1, KDF: kdf}, mk); err != nil {
		t.Fatal(err)
	}
	audit, err := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { audit.Close() })
	ak = filepath.Join(dir, "ssh", "authorized_keys")
	h, err = New(Options{StorePath: path, Audit: audit, AuthorizedKeysPath: ak, InboundBinary: testBin})
	if err != nil {
		t.Fatal(err)
	}
	unlockForTest(h)
	return h, ak, dir
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInboundDoorFlow(t *testing.T) {
	h, ak, dir := inboundHub(t)
	c, _ := startUI(t, h)
	ctx := context.Background()
	key := inboundPub(t)
	call := func(m string, p any) error { return c.Call(ctx, m, p, nil) }

	if err := call("inbound.add", map[string]any{"name": "iphone", "publicKey": key + " me@phone", "ip": "100.64.0.5"}); err != nil {
		t.Fatal(err)
	}
	wantLine := `restrict,pty,from="100.64.0.5",command="'` + testBin + `' inbound iphone" ` + key
	want := "# sshgate inbound begin\n" + wantLine + "\n# sshgate inbound end\n"
	if got := readFile(t, ak); got != want {
		t.Fatalf("file:\n%s\nwant:\n%s", got, want)
	}
	if fi, _ := os.Stat(ak); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Dir(ak)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", fi.Mode())
	}

	if err := call("inbound.setIP", map[string]any{"name": "iphone", "ip": "100.64.0.6", "state": "allowed"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, ak), `from="100.64.0.5,100.64.0.6"`) {
		t.Fatalf("second ip missing:\n%s", readFile(t, ak))
	}
	for _, ip := range []string{"100.64.0.5", "100.64.0.6"} {
		if err := call("inbound.setIP", map[string]any{"name": "iphone", "ip": ip, "state": "blocked"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := readFile(t, ak); got != "# sshgate inbound begin\n# sshgate inbound end\n" {
		t.Fatalf("a device with no allowed IP still has a line:\n%s", got)
	}

	var list struct {
		Devices []struct {
			Name, PublicKey, Fingerprint string
			Allowed, Blocked             []string
		}
		AuthorizedKeys string
		FileError      string
	}
	if err := c.Call(ctx, "inbound.list", map[string]any{}, &list); err != nil {
		t.Fatal(err)
	}
	d := list.Devices[0]
	if len(list.Devices) != 1 || d.Name != "iphone" || d.PublicKey != key || !strings.HasPrefix(d.Fingerprint, "SHA256:") ||
		len(d.Allowed) != 0 || len(d.Blocked) != 2 || list.AuthorizedKeys != ak || list.FileError != "" {
		t.Fatalf("list %+v", list)
	}

	if err := call("inbound.setIP", map[string]any{"name": "iphone", "ip": "100.64.0.6", "state": "removed"}); err != nil {
		t.Fatal(err)
	}
	if err := call("inbound.delete", map[string]any{"name": "iphone"}); err != nil {
		t.Fatal(err)
	}
	if err := call("inbound.delete", map[string]any{"name": "iphone"}); err == nil || !strings.Contains(err.Error(), config.ErrNoInbound.Error()) {
		t.Fatalf("delete twice: %v", err)
	}
	f, _ := config.Load(filepath.Join(dir, "servers.json"))
	if len(f.Inbound) != 0 {
		t.Fatalf("vault still lists %+v", f.Inbound)
	}

	log := auditText(t, dir)
	for _, w := range []string{`"action":"inboundAdd"`, `"fingerprint":"SHA256:`, `"action":"inboundAllow","device":"iphone","ip":"100.64.0.6"`,
		`"action":"inboundBlock","device":"iphone","ip":"100.64.0.5"`, `"action":"inboundForget","device":"iphone","ip":"100.64.0.6"`, `"action":"inboundDelete","device":"iphone"`} {
		if !strings.Contains(log, w) {
			t.Errorf("audit lacks %s", w)
		}
	}
	if strings.Contains(log, strings.Fields(key)[1]) {
		t.Fatal("the audit log holds key material")
	}
}

func TestInboundValidationOverDoor(t *testing.T) {
	h, ak, _ := inboundHub(t)
	c, _ := startUI(t, h)
	ctx := context.Background()
	key := inboundPub(t)
	for name, p := range map[string]map[string]any{
		"bad name": {"name": "a b", "publicKey": key, "ip": "1.2.3.4"},
		"bad key":  {"name": "a", "publicKey": `command="x" ` + key, "ip": "1.2.3.4"},
		"bad ip":   {"name": "a", "publicKey": key, "ip": "0.0.0.0/0"},
	} {
		if err := c.Call(ctx, "inbound.add", p, nil); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := os.Stat(ak); err == nil {
		t.Fatal("a refused add wrote the file")
	}
	if err := c.Call(ctx, "inbound.add", map[string]any{"name": "a", "publicKey": key, "ip": "1.2.3.4"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "inbound.add", map[string]any{"name": "b", "publicKey": key, "ip": "1.2.3.4"}, nil); err == nil || !strings.Contains(err.Error(), config.ErrInboundKeyDup.Error()) {
		t.Fatalf("dup key: %v", err)
	}
	var re *rpc.Error
	if err := c.Call(ctx, "inbound.setIP", map[string]any{"name": "a", "ip": "1.2.3.4", "state": "maybe"}, nil); !errors.As(err, &re) || re.Code != -32602 {
		t.Fatalf("state: %v", err)
	}
}

func TestInboundStrictParams(t *testing.T) {
	h, _, _ := inboundHub(t)
	c, _ := startUI(t, h)
	ctx := context.Background()
	for method, p := range map[string]any{
		"inbound.list":   map[string]any{"x": 1},
		"inbound.add":    map[string]any{"name": "n", "publicKey": "k", "ip": "1.1.1.1", "x": 1},
		"inbound.delete": map[string]any{"name": "n", "x": 1},
		"inbound.setIP":  map[string]any{"name": "n", "ip": "1.1.1.1", "state": "allowed", "x": 1},
	} {
		var re *rpc.Error
		if err := c.Call(ctx, method, p, nil); !errors.As(err, &re) || re.Code != -32602 {
			t.Errorf("%s: %v", method, err)
		}
	}
}

func TestInboundRefusedWhileLockedAndWithoutVault(t *testing.T) {
	h, ak, _ := inboundHub(t)
	c, _ := startUI(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "lock", map[string]any{"stopAuto": true}, nil); err != nil {
		t.Fatal(err)
	}
	for method, p := range map[string]any{
		"inbound.list": map[string]any{}, "inbound.add": map[string]any{"name": "n", "publicKey": inboundPub(t), "ip": "1.1.1.1"},
		"inbound.delete": map[string]any{"name": "n"}, "inbound.setIP": map[string]any{"name": "n", "ip": "1.1.1.1", "state": "allowed"},
	} {
		if err := c.Call(ctx, method, p, nil); err == nil || !strings.Contains(err.Error(), ErrLocked.Error()) {
			t.Errorf("%s while locked: %v", method, err)
		}
	}
	if _, err := os.Stat(ak); err == nil {
		t.Fatal("a locked hub wrote the file")
	}

	nv, _ := newHubAt(t, &config.File{Version: 1}, nil)
	c2, _ := startUI(t, nv)
	if err := c2.Call(ctx, "inbound.list", map[string]any{}, nil); err == nil || !strings.Contains(err.Error(), "create a vault first") {
		t.Fatalf("no vault: %v", err)
	}
}

func TestInboundFileKeepsForeignLines(t *testing.T) {
	h, ak, _ := inboundHub(t)
	if err := os.MkdirAll(filepath.Dir(ak), 0o700); err != nil {
		t.Fatal(err)
	}
	before := "ssh-ed25519 AAAAhand me@laptop\r\n# note\n\nfrom=\"1.1.1.1\" ssh-rsa AAAAold x\n"
	after := "ssh-ed25519 AAAAafter y\nno trailing newline"
	if err := os.WriteFile(ak, []byte(before+after), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.AddInbound("a", inboundPub(t), "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, ak)
	if !strings.HasPrefix(got, before+after+"\n# sshgate inbound begin\n") {
		t.Fatalf("a foreign line changed or the block is not appended:\n%q", got)
	}
	// Text after the block must survive a rewrite, and only the block changes.
	if err := os.WriteFile(ak, []byte(before+strings.TrimPrefix(got, before+after+"\n")+after+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.AddInbound("b", inboundPub(t), "1.2.3.5"); err != nil {
		t.Fatal(err)
	}
	got = readFile(t, ak)
	if strings.Count(got, "inbound begin") != 1 || strings.Count(got, `command="'`+testBin+`' inbound `) != 2 ||
		!strings.HasPrefix(got, before+"# sshgate inbound begin\n") || !strings.HasSuffix(got, "# sshgate inbound end\n"+after+"\n") {
		t.Fatalf("block not replaced in place:\n%q", got)
	}
}

func TestInboundRefusesSymlinkAndKeepsList(t *testing.T) {
	h, ak, dir := inboundHub(t)
	if err := os.MkdirAll(filepath.Dir(ak), 0o700); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, ak); err != nil {
		t.Skip("no symlinks:", err)
	}
	err := h.AddInbound("a", inboundPub(t), "1.2.3.4")
	if err == nil || !strings.HasPrefix(err.Error(), "saved, but authorized_keys was not updated: ") {
		t.Fatalf("want the saved-but error, got %v", err)
	}
	if readFile(t, real) != "keep\n" {
		t.Fatal("wrote through the link")
	}
	l, err := h.ListInbound()
	if err != nil || len(l.Devices) != 1 || l.FileError == "" {
		t.Fatalf("list %+v %v", l, err)
	}
	os.Remove(ak)
	if err := h.SetInboundIP("a", "1.2.3.5", "allowed"); err != nil {
		t.Fatal(err)
	}
	if l, _ := h.ListInbound(); l.FileError != "" {
		t.Fatalf("fileError stuck: %q", l.FileError)
	}
}

func TestSpliceInboundRefusesBrokenMarkers(t *testing.T) {
	for name, in := range map[string]string{
		"begin only": "x\n# sshgate inbound begin\ny\n",
		"end only":   "x\n# sshgate inbound end\n",
		"two begins": "# sshgate inbound begin\n# sshgate inbound end\n# sshgate inbound begin\n# sshgate inbound end\n",
	} {
		if _, err := spliceInbound([]byte(in), nil); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if out, err := spliceInbound([]byte("a\n"), nil); err != nil || string(out) != "a\n" {
		t.Fatalf("no block, no lines: %q %v", out, err)
	}
}

func TestInboundBinaryPathRefused(t *testing.T) {
	for _, p := range []string{`/a"b`, `/a\b`, "/a\nb", "relative/sshgate", "/a'b", "/a$b"} {
		h := &Hub{o: Options{InboundBinary: p}}
		if _, err := h.inboundCommand(); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
	h := &Hub{o: Options{InboundBinary: "/Applications/my app/sshgate"}}
	if got, err := h.inboundCommand(); err != nil || got != "'/Applications/my app/sshgate'" {
		t.Fatalf("%q %v", got, err)
	}
	h = &Hub{o: Options{InboundBinary: "/opt/a;b/sshgate"}}
	if got, err := h.inboundCommand(); err != nil || got != "'/opt/a;b/sshgate'" {
		t.Fatalf("shell character unquoted: %q %v", got, err)
	}
}

func TestCreateVaultClearsInboundAndBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	ak := filepath.Join(dir, "authorized_keys")
	foreign := "ssh-ed25519 AAAAhand me\n"
	if err := os.WriteFile(ak, []byte(foreign+"# sshgate inbound begin\nrestrict,pty,from=\"1.2.3.4\",command=\"/old/sshgate inbound x\" ssh-ed25519 AAAAold\n# sshgate inbound end\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &config.File{Version: 1, Inbound: []config.InboundDevice{{Name: "x", PublicKey: "ssh-ed25519 AAAAplant", Allowed: []string{"1.2.3.4"}}}}
	if err := config.Save(path, f, nil); err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{StorePath: path, AuthorizedKeysPath: ak, InboundBinary: testBin})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.CreateVault("longenough"); err != nil {
		t.Fatal(err)
	}
	if g, _ := config.Load(path); len(g.Inbound) != 0 {
		t.Fatalf("inbound survived vault.create: %+v", g.Inbound)
	}
	if got := readFile(t, ak); got != foreign+"# sshgate inbound begin\n# sshgate inbound end\n" {
		t.Fatalf("block not emptied:\n%q", got)
	}
}

func TestInboundLinesNegateBlocked(t *testing.T) {
	d := config.InboundDevice{Name: "a", PublicKey: "ssh-ed25519 AAAA", Allowed: []string{"100.64.0.0/10"}, Blocked: []string{"100.100.1.2", "fd00::/8"}}
	got := inboundLines([]config.InboundDevice{d, {Name: "b", PublicKey: "ssh-ed25519 BBBB", Blocked: []string{"1.2.3.4"}}}, "'/x'")
	if len(got) != 1 || !strings.Contains(got[0], `from="100.64.0.0/10,!100.100.1.2,!fd00::/8",`) {
		t.Fatalf("%q", got)
	}
}

func TestInboundReconcileOnUnlockAndList(t *testing.T) {
	h, ak, _ := inboundHub(t)
	if err := h.AddInbound("a", inboundPub(t), "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	good := readFile(t, ak)
	if err := os.WriteFile(ak, []byte("# gone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ListInbound(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, ak); got != "# gone\n"+good {
		t.Fatalf("list did not reconcile:\n%q", got)
	}
	os.WriteFile(ak, []byte(strings.ReplaceAll(good, testBin, "/stale/sshgate")), 0o600)
	h.Lock()
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, ak); got != good {
		t.Fatalf("unlock did not repair the stale path:\n%q", got)
	}
}

func TestInboundReconcileWritesNothingWithNoDevices(t *testing.T) {
	h, ak, _ := inboundHub(t)
	if _, err := h.ListInbound(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ak); !os.IsNotExist(err) {
		t.Fatalf("authorized_keys created: %v", err)
	}
}

func TestInboundRefusesForeignLineInBlock(t *testing.T) {
	h, ak, _ := inboundHub(t)
	os.MkdirAll(filepath.Dir(ak), 0o700)
	in := "# sshgate inbound begin\nssh-ed25519 AAAAlaptop me@laptop\n# sshgate inbound end\n"
	os.WriteFile(ak, []byte(in), 0o600)
	err := h.AddInbound("a", inboundPub(t), "1.2.3.4")
	if err == nil || !strings.Contains(err.Error(), "inside sshgate's block") {
		t.Fatalf("got %v", err)
	}
	if readFile(t, ak) != in {
		t.Fatal("the foreign line was deleted")
	}
}

func TestInboundReportsKeyOutsideBlock(t *testing.T) {
	h, ak, _ := inboundHub(t)
	os.MkdirAll(filepath.Dir(ak), 0o700)
	pub := inboundPub(t)
	os.WriteFile(ak, []byte(pub+" me@laptop\n"), 0o600)
	err := h.AddInbound("a", pub, "1.2.3.4")
	if err == nil || !strings.Contains(err.Error(), "the key of a is also on a line outside sshgate's block; remove that line") {
		t.Fatalf("got %v", err)
	}
	got := readFile(t, ak)
	if !strings.HasPrefix(got, pub+" me@laptop\n") || !strings.Contains(got, `from="1.2.3.4"`) {
		t.Fatalf("foreign line edited or block missing:\n%q", got)
	}
	if l, _ := h.ListInbound(); !strings.Contains(l.FileError, "the key of a") {
		t.Fatalf("fileError %q", l.FileError)
	}
}

package hubapi

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if err := h.ForgetHostKey(ctx, "nope"); err == nil || err.Error() != `server "nope" not found` {
		t.Fatalf("forget unknown: %v", err)
	}
	if err := h.ForgetHostKey(ctx, "box"); err != nil {
		t.Fatal(err)
	}
	if servers, err := h.Servers(ctx); err != nil || servers[0].HostKey != "" {
		t.Fatalf("still pinned after ForgetHostKey: %+v %v", servers, err)
	}
	if err := h.Lock(ctx, false); err != nil {
		t.Fatal(err)
	}
	s, err = h.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Locked {
		t.Fatal("not locked after Lock")
	}
}

// TestHostWritesAgainstRealHub: servers.save, servers.delete and import
// answer with the hub's texts; secrets come back as flags only.
func TestHostWritesAgainstRealHub(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("import needs the OpenSSH client")
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "ssh_config")
	hubtest.SSHConfig(t, cfg, "Host jump\n  HostName 127.0.0.1\n  ProxyJump other\n", filepath.Join(dir, "known_hosts"))
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: []string{"hub", "--store=" + store, "--sshConfig=" + cfg}, Env: hubtest.Env(t)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx := context.Background()
	h := New(p.Client())
	if _, err := h.Unlock(ctx, hubtest.Password); err != nil {
		t.Fatal(err)
	}
	pw := "secret"
	in := ServerInput{Name: "web", Host: "10.0.0.5", Port: 22, User: "u", Auth: "password", Password: &pw}
	if err := h.SaveServer(ctx, "", in, "off"); err != nil {
		t.Fatal(err)
	}
	servers, err := h.Servers(ctx)
	if err != nil || len(servers) != 2 || servers[1].Name != "web" || !servers[1].HasPassword || servers[1].HasSuPassword || servers[1].AIVisible {
		t.Fatalf("servers %+v %v", servers, err)
	}
	in.Name = "box"
	if err := h.SaveServer(ctx, "", in, "off"); err == nil || err.Error() != `a server named "box" already exists` {
		t.Fatalf("duplicate: %v", err)
	}
	in.Name, in.Port = "web2", 0
	if err := h.SaveServer(ctx, "", in, "off"); err == nil || err.Error() != "port: must be 1-65535" {
		t.Fatalf("port 0: %v", err)
	}
	if err := h.DeleteServer(ctx, "web"); err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteServer(ctx, "web"); err == nil || err.Error() != `server "web" not found` {
		t.Fatalf("delete twice: %v", err)
	}
	scan, err := h.ImportScan(ctx)
	if err != nil || len(scan.Candidates) != 1 || scan.Candidates[0].Alias != "jump" || scan.Candidates[0].Status != "skipped" || scan.Candidates[0].Reason != "needs ProxyJump" {
		t.Fatalf("scan %+v %v", scan, err)
	}
	res, err := h.ImportApply(ctx, []string{"jump", "nope"})
	if err != nil || len(res.Imported) != 0 || len(res.Skipped) != 2 ||
		res.Skipped[0] != (ImportSkip{"nope", "not in the SSH config"}) || res.Skipped[1] != (ImportSkip{"jump", "needs ProxyJump"}) {
		t.Fatalf("apply %+v %v", res, err)
	}
}

// TestAutoAllowSettingsAgainstRealHub: the soft-lock limit round-trips
// through status, and the root check answers with the hub's texts (sshtestd
// echoes the probe instead of running it, so it cannot be checked).
func TestAutoAllowSettingsAgainstRealHub(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	if s.SoftLockLimit != "" {
		t.Fatalf("a locked hub reports the limit %q", s.SoftLockLimit)
	}
	if _, err := h.Unlock(ctx, hubtest.Password); err != nil {
		t.Fatal(err)
	}
	s, err = h.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.SoftLockLimit != "24h" {
		t.Fatalf("default limit %q", s.SoftLockLimit)
	}
	if err := h.SetSoftLockLimit(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	s, err = h.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.SoftLockLimit != "none" {
		t.Fatalf("limit %q after setting none", s.SoftLockLimit)
	}
	if err := h.SetSoftLockLimit(ctx, "1d"); err == nil || err.Error() != "limit must be 24h, 3d, 7d, or none" {
		t.Fatalf("1d: %v", err)
	}
	if _, err := h.AutoAllowCheck(ctx, "box"); err == nil || err.Error() != "could not check this host" {
		t.Fatalf("check box: %v", err)
	}
	if _, err := h.AutoAllowCheck(ctx, "nope"); err == nil || err.Error() != `server "nope" not found` {
		t.Fatalf("check nope: %v", err)
	}
	if err := h.Lock(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := h.SetSoftLockLimit(ctx, "7d"); err == nil || err.Error() != "Vault is locked; unlock it in the app" {
		t.Fatalf("locked: %v", err)
	}
}

// TestFilesAgainstRealHub: list, mkdir, rename, and an upload job through
// sshtestd's SFTP; files.run takes only overwrite or skip (never Electron's
// "ask"), and a locked hub refuses files.list.
func TestFilesAgainstRealHub(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	srv := hubtest.Start(t, hubtest.Options{SFTP: true})
	bin, _ := hubtest.Binaries(t)
	notes := make(chan string, 64)
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: []string{"hub", "--store=" + srv.Store}, Env: hubtest.Env(t)},
		func(m string, params json.RawMessage) {
			if strings.HasPrefix(m, "files.") {
				notes <- m + " " + string(params)
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	next := func(method string) string {
		t.Helper()
		for {
			select {
			case n := <-notes:
				if strings.HasPrefix(n, method+" ") {
					return n
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("no %s", method)
			}
		}
	}
	ctx := context.Background()
	h := New(p.Client())
	if _, err := h.Unlock(ctx, hubtest.Password); err != nil {
		t.Fatal(err)
	}
	l, err := h.FilesList(ctx, "box", "", nil)
	if err != nil || l.Status != "listed" || l.Path != "/home" || len(l.Entries) != 0 {
		t.Fatalf("home: %+v %v", l, err)
	}
	if err := h.FilesMkdir(ctx, "box", "/home/made"); err != nil {
		t.Fatal(err)
	}
	if err := h.FilesRename(ctx, "box", "/home/made", "/home/made2"); err != nil {
		t.Fatal(err)
	}
	if l, _ := h.FilesList(ctx, "box", "/home", nil); len(l.Entries) != 1 || l.Entries[0].Name != "made2" || l.Entries[0].Kind != "dir" {
		t.Fatalf("after rename: %+v", l.Entries)
	}
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.FilesPlan(ctx, "f1", "box", "upload", []string{local}, "/home"); err != nil {
		t.Fatal(err)
	}
	if n := next("files.planned"); !strings.Contains(n, `"files":1`) || !strings.Contains(n, `"count":0`) {
		t.Fatal(n)
	}
	if err := h.FilesRun(ctx, "f1", "skip"); err != nil {
		t.Fatal(err)
	}
	if n := next("files.done"); !strings.Contains(n, `"copied":1`) || !strings.Contains(n, `"cancelled":false`) {
		t.Fatal(n)
	}
	if b, err := os.ReadFile(filepath.Join(srv.SFTP, "home", "a.txt")); err != nil || string(b) != "alpha" {
		t.Fatalf("uploaded %q %v", b, err)
	}
	if err := h.FilesPlan(ctx, "f2", "box", "upload", []string{local}, "/home"); err != nil {
		t.Fatal(err)
	}
	if n := next("files.planned"); !strings.Contains(n, `"count":1`) || !strings.Contains(n, `"sample":["a.txt"]`) {
		t.Fatal(n)
	}
	if err := h.FilesRun(ctx, "f2", "ask"); err == nil || err.Error() != "conflict must be overwrite or skip" {
		t.Fatalf("ask: %v", err)
	}
	if err := p.Client().Notify("files.cancel", map[string]string{"id": "f2"}); err != nil {
		t.Fatal(err)
	}
	if n := next("files.done"); !strings.Contains(n, `"cancelled":true`) || !strings.Contains(n, `"reason":"cancelled"`) {
		t.Fatal(n)
	}
	if err := h.Lock(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.FilesList(ctx, "box", "", nil); err == nil || err.Error() != "Vault is locked; unlock it in the app" {
		t.Fatalf("locked: %v", err)
	}
}

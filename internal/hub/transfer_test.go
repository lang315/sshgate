package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/files"
)

// aiFiles: filesHub with "fs" visible to AI, and a project folder.
func aiFiles(t *testing.T) (filesFixture, string) {
	t.Helper()
	fx := filesHub(t)
	if err := config.Update(fx.store, fx.h.deps.MasterKey, func(f *config.File) error {
		f.Servers[0].AIVisible = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.Reload(); err != nil {
		t.Fatal(err)
	}
	return fx, t.TempDir()
}

// decideTransfer runs h.Transfer, decides its pending request with d, and
// returns what Transfer returned.
func decideTransfer(t *testing.T, h *Hub, r TransferRequest, d broker.Decision) (TransferResponse, error) {
	t.Helper()
	type res struct {
		r   TransferResponse
		err error
	}
	c := make(chan res, 1)
	go func() { r, err := h.Transfer(context.Background(), r); c <- res{r, err} }()
	waitPending(t, h.Broker(), 1)
	h.Broker().Decide(h.Broker().Pending()[0].ID, d)
	got := <-c
	return got.r, got.err
}

func TestTransferUploadApproved(t *testing.T) {
	fx, proj := aiFiles(t)
	os.MkdirAll(filepath.Join(proj, "build"), 0o755)
	os.WriteFile(filepath.Join(proj, "build", "app.bin"), []byte{0, 1, 2, 0xff}, 0o755)
	type res struct {
		r   TransferResponse
		err error
	}
	c := make(chan res, 1)
	go func() {
		r, err := fx.h.Transfer(context.Background(), TransferRequest{Client: "c", Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"build"}, Remote: []string{"/home"}})
		c <- res{r, err}
	}()
	waitPending(t, fx.h.Broker(), 1)
	p := fx.h.Broker().Pending()[0]
	if p.Transfer == nil || p.Transfer.Op != "upload" || p.Transfer.Root != proj || p.Transfer.Remote[0] != "/home" {
		t.Fatalf("pending %+v", p)
	}
	fx.h.Broker().Decide(p.ID, broker.Decision{Outcome: broker.Allowed})
	got := <-c
	resp, err := got.r, got.err
	if err != nil || resp.Files != 1 || resp.Bytes != 4 {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if b, _ := os.ReadFile(filepath.Join(fx.root, "home", "build", "app.bin")); !bytes.Equal(b, []byte{0, 1, 2, 0xff}) {
		t.Fatalf("remote file %v", b)
	}
	rec := fileRecords(t, fx.store)
	last := rec[len(rec)-1]
	if last["client"] != "c" || last["outcome"] != "allowed" || last["action"] != "upload" || last["approval"] != nil {
		t.Fatalf("audit %v", last)
	}
}

func TestTransferDownloadRefusesSecrets(t *testing.T) {
	fx, proj := aiFiles(t)
	os.MkdirAll(filepath.Join(fx.root, "home", "app"), 0o755)
	os.WriteFile(filepath.Join(fx.root, "home", "app", ".env"), []byte("password=hunter2x\n"), 0o600)
	os.WriteFile(filepath.Join(fx.root, "home", "app", "main.go"), []byte("package main\n"), 0o644)
	os.Mkdir(filepath.Join(proj, "out"), 0o755)
	resp, err := decideTransfer(t, fx.h, TransferRequest{Client: "c", Server: "fs", Op: files.OpDownload, Root: proj, Local: []string{"out"}, Remote: []string{"/home/app"}}, broker.Decision{Outcome: broker.Allowed})
	if err != nil || resp.Files != 1 || resp.RefusedCount != 1 || resp.Refused[0].Path != "app/.env" || resp.Refused[0].Kinds["password"] != 1 {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if _, err := os.Stat(filepath.Join(proj, "out", "app", ".env")); !os.IsNotExist(err) {
		t.Fatal(".env written locally")
	}
	rec := fileRecords(t, fx.store)
	if last := rec[len(rec)-1]; last["refused"] != json.Number("1") && last["refused"] != float64(1) {
		t.Fatalf("audit %v", last)
	}
}

func TestTransferDeniedAndExpiredAudited(t *testing.T) {
	fx, proj := aiFiles(t)
	os.WriteFile(filepath.Join(proj, "f"), []byte("x"), 0o644)
	req := TransferRequest{Client: "c", Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}}
	if _, err := decideTransfer(t, fx.h, req, broker.Decision{Outcome: broker.Denied, Reason: "no"}); err == nil {
		t.Fatal("denied transfer succeeded")
	}
	rec := fileRecords(t, fx.store)
	if last := rec[len(rec)-1]; last["outcome"] != "denied" || last["reason"] != "no" {
		t.Fatalf("audit %v", last)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "home", "f")); !os.IsNotExist(err) {
		t.Fatal("denied upload ran")
	}
	// Send to tab is refused by the broker and the request stays pending.
	// (The hub's own SentToTab branch is unreachable through the broker; it
	// is there to fail closed if that refusal ever regresses.)
	c := make(chan error, 1)
	go func() { _, err := fx.h.Transfer(context.Background(), req); c <- err }()
	waitPending(t, fx.h.Broker(), 1)
	id := fx.h.Broker().Pending()[0].ID
	if err := fx.h.Broker().Decide(id, broker.Decision{Outcome: broker.SentToTab}); !errors.Is(err, broker.ErrSendToTabTransfer) {
		t.Fatalf("sent_to_tab: %v", err)
	}
	fx.h.Broker().Decide(id, broker.Decision{Outcome: broker.Denied})
	if err := <-c; err == nil {
		t.Fatal("transfer succeeded")
	}
	if _, err := os.Stat(filepath.Join(fx.root, "home", "f")); !os.IsNotExist(err) {
		t.Fatal("upload ran")
	}
}

// Refused before the broker: nothing pending, nothing audited.
func TestTransferRefusals(t *testing.T) {
	fx, proj := aiFiles(t)
	home, _ := os.UserHomeDir()
	up := func(mod func(*TransferRequest)) error {
		r := TransferRequest{Client: "c", Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}}
		mod(&r)
		_, err := fx.h.Transfer(context.Background(), r)
		return err
	}
	for name, mod := range map[string]func(*TransferRequest){
		"root /":        func(r *TransferRequest) { r.Root = "/" },
		"root home":     func(r *TransferRequest) { r.Root = home },
		"root relative": func(r *TransferRequest) { r.Root = "proj" },
		"root control":  func(r *TransferRequest) { r.Root = proj + "\x1b[31m" },
		"local ..":      func(r *TransferRequest) { r.Local = []string{"../x"} },
		"local abs":     func(r *TransferRequest) { r.Local = []string{"/etc/passwd"} },
		"remote rel":    func(r *TransferRequest) { r.Remote = []string{"home"} },
		"two dests":     func(r *TransferRequest) { r.Remote = []string{"/a", "/b"} },
		"no sources":    func(r *TransferRequest) { r.Local = nil },
		"control char":  func(r *TransferRequest) { r.Local = []string{"a\x1bb"} },
		"download ..": func(r *TransferRequest) {
			r.Op, r.Local, r.Remote = files.OpDownload, []string{".."}, []string{"/home/f"}
		},
	} {
		if err := up(mod); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if n := len(fx.h.Broker().Pending()); n != 0 {
		t.Fatalf("%d pending", n)
	}
	if n := len(fileRecords(t, fx.store)); n != 0 {
		t.Fatalf("%d audit records", n)
	}
}

func TestTransferRefusesSuHost(t *testing.T) {
	fx, proj := aiFiles(t)
	if err := config.Update(fx.store, fx.h.deps.MasterKey, func(f *config.File) error {
		s := &f.Servers[0]
		enc, err := config.Encrypt(fx.h.deps.MasterKey, s.Name+"/encSuPassword", config.AADFor(f, *s, "encSuPassword"), "rootpw")
		s.EncSuPassword = enc
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.Reload(); err != nil {
		t.Fatal(err)
	}
	_, err := fx.h.Transfer(context.Background(), TransferRequest{Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}})
	if !errors.Is(err, ErrTransferSu) {
		t.Fatalf("err = %v", err)
	}
}

func TestTransferServerChangedDuringWait(t *testing.T) {
	fx, proj := aiFiles(t)
	os.WriteFile(filepath.Join(proj, "f"), []byte("x"), 0o644)
	c := make(chan error, 1)
	go func() {
		_, err := fx.h.Transfer(context.Background(), TransferRequest{Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}})
		c <- err
	}()
	waitPending(t, fx.h.Broker(), 1)
	id := fx.h.Broker().Pending()[0].ID
	// change the port behind the broker's back, as a hand edit would; the
	// pin stays, so the target compare is what refuses it
	if err := config.Update(fx.store, fx.h.deps.MasterKey, func(f *config.File) error {
		f.Servers[0].Port++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.Reload(); err != nil {
		t.Fatal(err)
	}
	fx.h.Broker().Decide(id, broker.Decision{Outcome: broker.Allowed})
	if err := <-c; !errors.Is(err, ErrServerChanged) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "home", "f")); !os.IsNotExist(err) {
		t.Fatal("upload ran")
	}
	rec := fileRecords(t, fx.store)
	if last := rec[len(rec)-1]; last["outcome"] != "error" {
		t.Fatalf("audit %v", last)
	}
}

// No vault: refused like Exec, with one capped audit record.
func TestTransferNoVaultAudited(t *testing.T) {
	h, store := newHubAt(t, nil, nil)
	huge := strings.Repeat("x", 1<<20)
	_, err := h.Transfer(context.Background(), TransferRequest{Client: huge, Server: huge, Description: huge, Op: files.OpUpload,
		Root: "/p", Local: []string{huge, huge}, Remote: []string{huge}})
	if !errors.Is(err, ErrNoVault) {
		t.Fatalf("err = %v", err)
	}
	if n := len(h.Broker().Pending()); n != 0 {
		t.Fatalf("%d pending", n)
	}
	raw, _ := readAudit(t, store)
	rec := fileRecords(t, store)
	if len(rec) != 1 || rec[0]["outcome"] != "error" || rec[0]["reason"] != ErrNoVault.Error() || len(raw) > 32<<10 {
		t.Fatalf("audit is %d bytes: %v", len(raw), len(rec))
	}
}

func TestTransferUnderGrantRunsAuto(t *testing.T) {
	fx, proj := aiFiles(t)
	os.WriteFile(filepath.Join(proj, "f"), []byte("x"), 0o644)
	if err := fx.h.SetAutoAllow("fs", "15m"); err != nil {
		t.Fatal(err)
	}
	ran := make(chan map[string]any, 1)
	release := fx.h.setAutoSink(func(m string, p any) {
		if m == "autoAllow.ran" {
			ran <- p.(map[string]any)
		}
	})
	defer release()
	resp, err := fx.h.Transfer(context.Background(), TransferRequest{Client: "c", Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}})
	if err != nil || resp.Files != 1 || len(fx.h.Broker().Pending()) != 0 {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	rec := fileRecords(t, fx.store)
	if last := rec[len(rec)-1]; last["approval"] != "auto" || last["outcome"] != "allowed" {
		t.Fatalf("audit %v", last)
	}
	select {
	case p := <-ran:
		if p["op"] != "upload" || p["command"] != "upload 1 path(s) to /home" {
			t.Fatalf("ran %v", p)
		}
	case <-time.After(testWait):
		t.Fatal("no autoAllow.ran")
	}
}

func TestTransferUnderSoftLock(t *testing.T) {
	fx, proj := aiFiles(t)
	os.WriteFile(filepath.Join(proj, "f"), []byte("x"), 0o644)
	if err := fx.h.SetAutoAllow("fs", "15m"); err != nil {
		t.Fatal(err)
	}
	fx.h.LockKeepAuto()
	if _, err := fx.h.Transfer(context.Background(), TransferRequest{Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}}); err != nil {
		t.Fatalf("soft lock: %v", err)
	}
	fx.h.Lock() // hard: no grant
	if _, err := fx.h.Transfer(context.Background(), TransferRequest{Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}}); !errors.Is(err, ErrLocked) {
		t.Fatalf("hard lock: %v", err)
	}
}

func TestTransferEndedByServerSave(t *testing.T) {
	fx, proj := aiFiles(t)
	os.WriteFile(filepath.Join(proj, "big"), bytes.Repeat([]byte("x"), 1<<20), 0o644)
	if err := fx.h.SetAutoAllow("fs", "15m"); err != nil {
		t.Fatal(err)
	}
	c := make(chan error, 1)
	go func() {
		_, err := fx.h.Transfer(context.Background(), TransferRequest{Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"big"}, Remote: []string{"/home"}})
		c <- err
	}()
	waitInflight(t, fx.h, "fs", 1)
	// Gate only once the transfer is in the job set: gated earlier, the stall
	// would land in NewSFTP or the plan's Stat, before registration, and
	// endServer would find nothing to end.
	if !waitFor(t, "the transfer in the job set", func() bool {
		fx.h.files.mu.Lock()
		defer fx.h.files.mu.Unlock()
		for j := range fx.h.files.all {
			if _, ok := j.(*aiTransfer); ok {
				return true
			}
		}
		return false
	}) {
		return
	}
	gate := make(chan struct{})
	fx.srv.GateSFTP(gate) // each SFTP request now waits for a gate send
	t.Cleanup(func() { close(gate) })
	fx.h.files.endServer("fs", "server changed") // what servers.save does
	select {
	case err := <-c:
		if !errors.Is(err, ErrServerChanged) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(cancelGrace + 5*time.Second):
		t.Fatal("transfer not stopped")
	}
}

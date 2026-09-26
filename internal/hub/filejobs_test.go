package hub

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func plan(t *testing.T, fx filesFixture, params map[string]any) map[string]any {
	t.Helper()
	if err := fx.c.Call(context.Background(), "files.plan", params, nil); err != nil {
		t.Fatal(err)
	}
	return waitNote(t, fx.notes, "files.planned", params["id"].(string))
}

func run(t *testing.T, fx filesFixture, id, conflict string) map[string]any {
	t.Helper()
	if err := fx.c.Call(context.Background(), "files.run", map[string]any{"id": id, "conflict": conflict}, nil); err != nil {
		t.Fatal(err)
	}
	return waitNote(t, fx.notes, "files.done", id)
}

func noPartFiles(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".sshgate-part") {
			t.Errorf("part file left: %s", p)
		}
		return nil
	})
}

func TestJobUploadDownloadDelete(t *testing.T) {
	fx := filesHub(t)
	src := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "a"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b"), []byte("b"), 0o644)

	p := plan(t, fx, map[string]any{"id": "j1", "server": "fs", "op": "upload", "sources": []string{src}, "dest": "/home"})
	if p["files"] != float64(2) || p["dirs"] != float64(2) || p["bytes"] != float64(4) || p["conflicts"].(map[string]any)["count"] != float64(0) {
		t.Fatalf("planned %v", p)
	}
	d := run(t, fx, "j1", "skip")
	if d["copied"] != float64(2) || d["op"] != "upload" || d["cancelled"] != false {
		t.Fatalf("done %v", d)
	}
	if b, _ := os.ReadFile(filepath.Join(fx.root, "home", "proj", "sub", "b")); string(b) != "b" {
		t.Fatal("upload content")
	}

	// Again: a conflict, skipped.
	p = plan(t, fx, map[string]any{"id": "j2", "server": "fs", "op": "upload", "sources": []string{src}, "dest": "/home"})
	if p["conflicts"].(map[string]any)["count"] != float64(2) {
		t.Fatalf("planned %v", p)
	}
	if d := run(t, fx, "j2", "skip"); d["skipped"] != float64(2) {
		t.Fatalf("done %v", d)
	}

	dest := t.TempDir()
	plan(t, fx, map[string]any{"id": "j3", "server": "fs", "op": "download", "sources": []string{"/home/proj"}, "dest": dest})
	if d := run(t, fx, "j3", "skip"); d["copied"] != float64(2) {
		t.Fatalf("done %v", d)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "proj", "a")); string(b) != "aaa" {
		t.Fatal("download content")
	}

	p = plan(t, fx, map[string]any{"id": "j4", "server": "fs", "op": "delete", "sources": []string{"/home/proj"}})
	if p["files"] != float64(2) || p["dirs"] != float64(2) {
		t.Fatalf("planned %v", p)
	}
	if d := run(t, fx, "j4", "skip"); d["deleted"] != float64(4) {
		t.Fatalf("done %v", d)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "home", "proj")); err == nil {
		t.Fatal("not deleted")
	}

	var ups []map[string]any
	for _, r := range fileRecords(t, fx.store) {
		if r["action"] == "upload" {
			ups = append(ups, r)
		}
	}
	if len(ups) != 4 || ups[0]["phase"] != "start" || ups[1]["phase"] != "end" ||
		ups[0]["local"].([]any)[0] != src || ups[0]["remote"].([]any)[0] != "/home" || ups[1]["files"] != float64(2) {
		t.Fatalf("upload audit %v", ups)
	}
}

func TestJobCancelWhileRunningAndWhileLocked(t *testing.T) {
	fx := filesHub(t)
	os.WriteFile(filepath.Join(fx.root, "home", "big"), []byte(strings.Repeat("z", 1<<20)), 0o644)
	dest := t.TempDir()
	plan(t, fx, map[string]any{"id": "c1", "server": "fs", "op": "download", "sources": []string{"/home/big"}, "dest": dest})
	gate := make(chan struct{})
	fx.srv.GateSFTP(gate)
	if err := fx.c.Call(context.Background(), "files.run", map[string]any{"id": "c1", "conflict": "skip"}, nil); err != nil {
		t.Fatal(err)
	}
	gate <- struct{}{}
	fx.h.Lock() // a running transfer survives a lock, and can still be cancelled
	sendNote(t, fx.w, "files.cancel", map[string]any{"id": "c1"})
	close(gate)
	d := waitNote(t, fx.notes, "files.done", "c1")
	if d["cancelled"] != true {
		t.Fatalf("done %v", d)
	}
	noPartFiles(t, dest)
}

func TestJobRunNeedsUnlockAndAPlan(t *testing.T) {
	fx := filesHub(t)
	ctx := context.Background()
	if err := fx.c.Call(ctx, "files.run", map[string]any{"id": "nope", "conflict": "skip"}, nil); err == nil {
		t.Fatal("ran an unknown job")
	}
	os.WriteFile(filepath.Join(fx.root, "home", "f"), []byte("f"), 0o644)
	plan(t, fx, map[string]any{"id": "r1", "server": "fs", "op": "download", "sources": []string{"/home/f"}, "dest": t.TempDir()})
	if err := fx.c.Call(ctx, "files.plan", map[string]any{"id": "r1", "server": "fs", "op": "delete", "sources": []string{"/home/f"}}, nil); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if err := fx.c.Call(ctx, "files.run", map[string]any{"id": "r1", "conflict": "ask"}, nil); err == nil {
		t.Fatal("conflict must be overwrite or skip")
	}
	fx.h.Lock()
	if err := fx.c.Call(ctx, "files.run", map[string]any{"id": "r1", "conflict": "skip"}, nil); err == nil {
		t.Fatal("ran while locked")
	}
	if err := fx.c.Call(ctx, "files.plan", map[string]any{"id": "r2", "server": "fs", "op": "delete", "sources": []string{"/home/f"}}, nil); err == nil {
		t.Fatal("planned while locked")
	}
}

func TestJobStrictPlanParams(t *testing.T) {
	fx := filesHub(t)
	raw := `{"id":"s1","server":"fs","op":"upload","sources":["/tmp/x"],"dest":"/home","Sources":["/etc/passwd"]}`
	if err := fx.c.Call(context.Background(), "files.plan", json.RawMessage(raw), nil); err == nil {
		t.Fatal("a case-variant key was accepted")
	}
}

func TestJobEndedByServerChange(t *testing.T) {
	fx := filesHub(t)
	os.WriteFile(filepath.Join(fx.root, "home", "big"), []byte(strings.Repeat("z", 1<<20)), 0o644)
	plan(t, fx, map[string]any{"id": "e1", "server": "fs", "op": "download", "sources": []string{"/home/big"}, "dest": t.TempDir()})
	gate := make(chan struct{})
	fx.srv.GateSFTP(gate)
	if err := fx.c.Call(context.Background(), "files.run", map[string]any{"id": "e1", "conflict": "skip"}, nil); err != nil {
		t.Fatal(err)
	}
	gate <- struct{}{}
	go func() { time.Sleep(50 * time.Millisecond); close(gate) }()
	if err := fx.h.DeleteServer("fs"); err != nil {
		t.Fatal(err)
	}
	d := waitNote(t, fx.notes, "files.done", "e1")
	if d["cancelled"] != true || d["reason"] != "server changed" {
		t.Fatalf("done %v", d)
	}
}

func TestJobCancelAllAndPlanExpiry(t *testing.T) {
	fx := filesHub(t)
	old := planTTL
	planTTL = 50 * time.Millisecond
	t.Cleanup(func() { planTTL = old })
	os.WriteFile(filepath.Join(fx.root, "home", "f"), []byte("f"), 0o644)
	plan(t, fx, map[string]any{"id": "x1", "server": "fs", "op": "delete", "sources": []string{"/home/f"}})
	if d := waitNote(t, fx.notes, "files.done", "x1"); d["reason"] != "plan expired" {
		t.Fatalf("done %v", d)
	}
	planTTL = old
	plan(t, fx, map[string]any{"id": "x2", "server": "fs", "op": "delete", "sources": []string{"/home/f"}})
	var r map[string]any
	if err := fx.c.Call(context.Background(), "files.cancelAll", map[string]any{}, &r); err != nil || r["cancelled"] != float64(1) {
		t.Fatalf("cancelAll %v %v", r, err)
	}
	if d := waitNote(t, fx.notes, "files.done", "x2"); d["cancelled"] != true {
		t.Fatalf("done %v", d)
	}
	if b, _ := os.ReadFile(filepath.Join(fx.root, "home", "f")); string(b) != "f" {
		t.Fatal("a cancelled delete deleted")
	}
}

func TestJobProgressIsRateLimited(t *testing.T) {
	fx := filesHub(t)
	old := progressEvery
	progressEvery = time.Hour
	t.Cleanup(func() { progressEvery = old })
	os.WriteFile(filepath.Join(fx.root, "home", "big"), []byte(strings.Repeat("z", 4<<20)), 0o644)
	plan(t, fx, map[string]any{"id": "p1", "server": "fs", "op": "download", "sources": []string{"/home/big"}, "dest": t.TempDir()})
	if err := fx.c.Call(context.Background(), "files.run", map[string]any{"id": "p1", "conflict": "skip"}, nil); err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		m := <-fx.notes
		if m.method == "files.progress" {
			n++
		}
		if m.method == "files.done" {
			break
		}
	}
	if n > 1 {
		t.Fatalf("%d progress notes with a 1 h interval", n)
	}
}

//go:build unix

package broker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Another process (`sshgate inbound`) holds the file lock while it appends:
// the hub's append waits for it, so its size check and write cannot straddle it.
func TestAuditAppendWaitsForFileLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	other, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	unlock, err := lockFile(other)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.WriteConfig(ConfigRecord{Action: "x"}) }()
	select {
	case <-done:
		t.Fatal("append did not wait for the lock")
	case <-time.After(200 * time.Millisecond):
	}
	other.WriteString(`{"kind":"login"}` + "\n")
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if a.n != 2 {
		t.Fatalf("seq %d, want 2: the hub missed the other writer's line", a.n)
	}
}

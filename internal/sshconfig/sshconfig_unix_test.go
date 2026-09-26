//go:build unix

package sshconfig

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCheckSkipsFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no FIFO:", err)
	}
	done := make(chan Candidate, 1)
	go func() {
		done <- Check(Resolved{Alias: "h", HostName: "10.0.0.1", Port: 22, User: "u", IdentityFiles: []string{fifo}}, func(string) bool { return false })
	}()
	select {
	case c := <-done:
		if c.Auth != "agent" {
			t.Fatalf("got %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Check blocked reading a FIFO")
	}
}

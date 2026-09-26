package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
)

func TestSSHTestdWritesReadyStore(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "sshtestd")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	store := filepath.Join(t.TempDir(), "servers.json")
	kh := filepath.Join(t.TempDir(), "known_hosts")
	cmd := exec.Command(bin, "-write-store="+store, "-password=pw", "-write-known-hosts="+kh)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "PORT=") {
		t.Fatalf("first line %q err %v", line, err)
	}
	f, err := config.Load(store)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := f.FindServer("box")
	if !ok || !s.AIVisible || s.HostKey == "" || s.EncPassword == "" || f.KDF == nil {
		t.Fatalf("store not ready: %+v", f)
	}
	port, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PORT=")))
	if algo, fp, ok := sshx.KnownHostKey([]string{kh}, "127.0.0.1", port); !ok || fp != s.HostKey || algo != "ssh-ed25519" {
		t.Fatalf("known_hosts: %q %q %v, want %q", algo, fp, ok, s.HostKey)
	}
	mk, err := f.KDF.DeriveKey("pw")
	if err != nil || !f.KDF.Verify(mk) || f.VerifyMAC(mk) != nil {
		t.Fatalf("store does not open with pw: %v", err)
	}
	info, _ := os.Stat(store)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v", info.Mode().Perm())
	}
}

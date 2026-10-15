package hubtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/sshgate/app/internal/hubtest"
)

func TestStartWritesWhatItIsAskedTo(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	kh := filepath.Join(t.TempDir(), "known_hosts")
	s := hubtest.Start(t, hubtest.Options{EmptyStore: true, KnownHosts: kh})
	if _, err := os.Stat(s.Store); !os.IsNotExist(err) {
		t.Fatalf("an empty store was written: %v", err)
	}
	b, err := os.ReadFile(kh)
	if err != nil || !strings.HasPrefix(string(b), fmt.Sprintf("[127.0.0.1]:%d ssh-ed25519 ", s.Port)) {
		t.Fatalf("known_hosts %q %v", b, err)
	}
	if v := hubtest.Start(t, hubtest.Options{}); v.Port == 0 {
		t.Fatal("no port")
	} else if _, err := os.Stat(v.Store); err != nil {
		t.Fatal("no vault:", err)
	} else if v.SFTP != "" {
		t.Fatalf("SFTP root %q without Options.SFTP", v.SFTP)
	}
	if v := hubtest.Start(t, hubtest.Options{SFTP: true}); v.SFTP == "" {
		t.Fatal("no SFTP root")
	} else if fi, err := os.Stat(filepath.Join(v.SFTP, "home")); err != nil || !fi.IsDir() {
		t.Fatalf("no home folder in the SFTP root: %v", err)
	}
}

// A test hub never gets the developer's ssh-agent: exec keeps the last
// value of a duplicated variable, so SSH_AUTH_SOCK must end up empty.
func TestEnvDropsTheAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/real/agent.sock")
	sock := "unset"
	for _, kv := range hubtest.Env(t) {
		if v, ok := strings.CutPrefix(kv, "SSH_AUTH_SOCK="); ok {
			sock = v
		}
	}
	if sock != "" {
		t.Fatalf("SSH_AUTH_SOCK=%q", sock)
	}
}

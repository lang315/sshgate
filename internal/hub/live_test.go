package hub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"

	"github.com/lang315/sshgate/internal/rpc"
)

// TestLiveImportConnect imports your real ~/.ssh/config into a throwaway vault
// and opens a terminal on every imported host, the way the app does. It dials
// real hosts, so it runs only when SSHGATE_LIVE_SSH is set: "1" for every
// host, or a comma-separated list of aliases.
//
//	SSHGATE_LIVE_SSH=1 go test ./internal/hub -run TestLiveImportConnect -v
func TestLiveImportConnect(t *testing.T) {
	only := os.Getenv("SSHGATE_LIVE_SSH")
	if only == "" {
		t.Skip("set SSHGATE_LIVE_SSH=1 (or a list of aliases) to dial the hosts in ~/.ssh/config")
	}
	h, _ := newHubAt(t, nil, nil)
	c, _, _ := startTermDoor(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	var scan scanResult
	if err := c.Call(ctx, "import.scan", nil, &scan); err != nil {
		t.Fatal(err)
	}
	var aliases []string
	for _, cd := range scan.Candidates {
		if only != "1" && !slices.Contains(strings.Split(only, ","), cd.Alias) {
			continue
		}
		if cd.Status != "ready" {
			t.Logf("%s: not imported (%s: %s)", cd.Alias, cd.Status, cd.Reason)
			continue
		}
		aliases = append(aliases, cd.Alias)
	}
	if len(aliases) == 0 {
		t.Fatalf("no ready host to dial (note %q)", scan.Note)
	}
	var res applyResult
	if err := c.Call(ctx, "import.apply", map[string]any{"aliases": aliases}, &res); err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Skipped {
		t.Logf("%s: skipped by apply (%s)", s.Alias, s.Reason)
	}
	for _, alias := range res.Imported {
		t.Run(alias, func(t *testing.T) { liveOpen(t, c, alias) })
	}
}

// TestLiveVaultConnect opens a terminal on every server in a copy of your real
// vault (SSHGATE_STORE, else ~/.config/sshgate/servers.json), so it uses what
// you saved in the app: passwords, passphrases, pins. The master password is
// read from the terminal without echo; the real vault is never written. It
// runs only when SSHGATE_LIVE_VAULT is set: "1" for every server, or a
// comma-separated list of names.
//
//	SSHGATE_LIVE_VAULT=1 go test ./internal/hub -run TestLiveVaultConnect -v -count=1
func TestLiveVaultConnect(t *testing.T) {
	only := os.Getenv("SSHGATE_LIVE_VAULT")
	if only == "" {
		t.Skip("set SSHGATE_LIVE_VAULT=1 (or a list of server names) to dial the servers in your vault")
	}
	src := os.Getenv("SSHGATE_STORE")
	if src == "" {
		home, _ := os.UserHomeDir()
		src = filepath.Join(home, ".config", "sshgate", "servers.json")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no terminal to read the master password from")
	}
	defer tty.Close()
	fmt.Fprintf(tty, "Master password for %s: ", src)
	pw, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	if err != nil {
		t.Fatal(err)
	}

	h, err := New(Options{StorePath: path, IdleLock: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	t.Cleanup(h.Registry().CloseAll)
	c, _, _ := startTermDoor(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "unlock", map[string]string{"password": string(pw)}, nil); err != nil {
		t.Fatal(err)
	}
	var servers []struct {
		Name string `json:"name"`
	}
	if err := c.Call(ctx, "servers", nil, &servers); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range servers {
		if only != "1" && !slices.Contains(strings.Split(only, ","), s.Name) {
			continue
		}
		n++
		t.Run(s.Name, func(t *testing.T) { liveOpen(t, c, s.Name) })
	}
	if n == 0 {
		t.Fatal("no matching server in the vault")
	}
}

// liveOpen opens a terminal on server the way the app does, never trusting a
// host key, and closes it.
func liveOpen(t *testing.T, c *rpc.Client, server string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var open map[string]any
	if err := c.Call(ctx, "term.open", map[string]any{"id": server, "server": server, "rows": 24, "cols": 80}, &open); err != nil {
		t.Fatalf("term.open: %v", err)
	}
	if open["status"] != "open" {
		t.Fatalf("term.open: %v", open)
	}
	if err := c.Call(ctx, "term.close", map[string]string{"id": server}, nil); err != nil {
		t.Errorf("term.close: %v", err)
	}
	var l map[string]any
	if err := c.Call(ctx, "files.list", map[string]any{"server": server, "path": ""}, &l); err != nil {
		t.Errorf("files.list: %v", err)
	} else if l["status"] != "listed" {
		t.Errorf("files.list: %v", l)
	}
}

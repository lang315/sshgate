package hub

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
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
		t.Run(alias, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			var open map[string]any
			if err := c.Call(ctx, "term.open", map[string]any{"id": alias, "server": alias, "rows": 24, "cols": 80}, &open); err != nil {
				t.Fatalf("term.open: %v", err)
			}
			if open["status"] != "open" {
				t.Fatalf("term.open: %v", open)
			}
			if err := c.Call(ctx, "term.close", map[string]string{"id": alias}, nil); err != nil {
				t.Errorf("term.close: %v", err)
			}
		})
	}
}

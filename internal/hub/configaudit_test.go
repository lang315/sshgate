package hub

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
)

func TestConfigAuditOneRecordPerActionAndNoSecrets(t *testing.T) {
	srv := sshtest.Start(t)
	h, path := newHubAt(t, &config.File{Version: 1, Servers: []config.Server{
		{Name: "box", Host: srv.Host, Port: srv.Port, User: "u", Auth: "password"}}}, nil)
	h.o.KnownHostsPath = filepath.Join(t.TempDir(), "known_hosts")
	c, _, _ := startTermDoor(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	secrets := []string{"sekret-pw-1", "sekret-su-2", "sekret-sudo-3", "sekret-kp-4"}
	in := config.ServerInput{Name: "box", Host: srv.Host, Port: srv.Port, User: "u", Auth: "password", AIVisible: true,
		Password: &secrets[0], SuPassword: &secrets[1], SudoPassword: &secrets[2], KeyPassphrase: &secrets[3]}
	if err := save(c, "box", in); err != nil {
		t.Fatal(err)
	}
	if r := openTerm(t, c, "t1", trusting(openTerm(t, c, "t1", nil))); r.Status != "open" {
		t.Fatalf("trust: %+v", r)
	}
	for _, m := range []string{"servers.forgetHostKey", "servers.delete"} {
		if err := c.Call(ctx, m, map[string]string{"name": "box"}, nil); err != nil {
			t.Fatal(err)
		}
	}

	raw, recs := readAudit(t, path)
	for _, s := range secrets {
		if strings.Contains(raw, s) {
			t.Fatalf("audit leaked %q", s)
		}
	}
	var actions []string
	by := map[string]map[string]any{}
	for _, r := range recs {
		if r["kind"] == "config" {
			a := r["action"].(string)
			actions = append(actions, a)
			by[a] = r
		}
	}
	if !slices.Equal(actions, []string{"vaultCreate", "save", "trust", "forgetHostKey", "delete"}) {
		t.Fatalf("actions %v", actions)
	}
	if kept := by["vaultCreate"]["keptServers"].([]any); len(kept) != 1 || kept[0] != "box" {
		t.Fatalf("keptServers %v", kept)
	}
	changed := fmt.Sprint(by["save"]["changed"])
	for _, want := range []string{"aiVisible: false → true", "password", "suPassword", "sudoPassword", "keyPassphrase"} {
		if !strings.Contains(changed, want) {
			t.Fatalf("changed %s lacks %q", changed, want)
		}
	}
	if tr := by["trust"]; tr["server"] != "box" || tr["fingerprint"] != srv.Fingerprint() || tr["algo"] != "ssh-ed25519" || tr["host"] != srv.Host {
		t.Fatalf("trust %v", tr)
	}
	if by["forgetHostKey"]["oldFingerprint"] != srv.Fingerprint() || by["delete"]["server"] != "box" {
		t.Fatalf("forget %v delete %v", by["forgetHostKey"], by["delete"])
	}
}

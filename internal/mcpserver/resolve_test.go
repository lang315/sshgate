package mcpserver

import (
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
)

func TestResolveCLIDefault(t *testing.T) {
	d := &Deps{CLI: &config.CLIConfig{Host: "h", Port: 22, User: "u", Password: "p", HasHost: true, TimeoutMs: 60000}}
	dc, err := d.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if dc.Host != "h" || dc.Password != "p" {
		t.Fatalf("got %+v", dc)
	}
}

func TestResolveLockedVault(t *testing.T) {
	f := &config.File{Version: 1, Servers: []config.Server{{Name: "p", Host: "h", Port: 22, User: "u", Auth: "password", EncPassword: "xxx"}}}
	d := &Deps{File: f, MasterKey: nil}
	if _, err := d.Resolve("p"); err == nil {
		t.Fatal("encrypted server without master key must error 'vault locked'")
	}
	if !d.IsLocked("p") {
		t.Fatal("IsLocked should be true")
	}
}

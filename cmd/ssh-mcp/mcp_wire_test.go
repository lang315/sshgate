package main

import (
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
)

func TestBuildDepsHostOnlyIgnoresStore(t *testing.T) {
	m := config.ParseArgv([]string{"--host=h", "--user=u", "--disableSudo", "--maxChars=50"})
	d, disableSudo, maxChars, err := buildDeps(m)
	if err != nil {
		t.Fatal(err)
	}
	if !disableSudo || maxChars != 50 || d.CLI == nil || !d.CLI.HasHost {
		t.Fatalf("got %+v %v %d", d, disableSudo, maxChars)
	}
	if d.File != nil {
		t.Fatal("--host mode must not load the vault")
	}
}

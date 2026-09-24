package main

import "testing"

func TestBuildDepsHostOnlyIgnoresStore(t *testing.T) {
	d, disableSudo, maxChars, err := buildDeps([]string{"--host=h", "--user=u", "--disableSudo", "--maxChars=50"})
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

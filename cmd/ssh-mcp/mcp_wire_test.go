package main

import "testing"

func TestBuildDepsFromArgs(t *testing.T) {
	d, disableSudo, maxChars, err := buildDeps([]string{"--host=h", "--user=u", "--disableSudo", "--maxChars=50"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !disableSudo {
		t.Fatal("disableSudo not parsed")
	}
	if maxChars != 50 {
		t.Fatalf("maxChars = %d", maxChars)
	}
	if d.CLI == nil || !d.CLI.HasHost {
		t.Fatal("CLI host not set")
	}
}

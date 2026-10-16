package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// mygo.json is where the version of the app and of the sshgate beside it
// comes from (scripts/package-mac.sh reads it with sed): it must stay JSON
// with these keys and a plain x.y.z version.
func TestManifest(t *testing.T) {
	b, err := os.ReadFile("../../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct{ Name, Identifier, Version, Main string }
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "sshgate" || m.Identifier != "io.github.lang315.sshgate" || m.Main != "./cmd/sshgate-app" || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(m.Version) {
		t.Fatalf("%+v", m)
	}
	for _, key := range []string{`"version":`, `"identifier":`} { // the script's sed needs "key": "value" on one line
		if !regexp.MustCompile(key + ` *"[^"]+"`).Match(b) {
			t.Errorf("no one-line %s in mygo.json", key)
		}
	}
}

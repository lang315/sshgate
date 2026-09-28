package broker

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditAppendsJSONLWithMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	code := 0
	if err := a.Write(AuditRecord{Time: time.Now(), Client: "c", Server: "s", Command: "ls", Outcome: "allowed", ExitCode: &code}); err != nil {
		t.Fatal(err)
	}
	if err := a.Write(AuditRecord{Time: time.Now(), Client: "c", Server: "s", Command: "rm", Outcome: "denied", Reason: "no"}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", info.Mode().Perm())
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		var r AuditRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("line %d not JSON: %v", n, err)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("want 2 lines, got %d", n)
	}
}

// The new auto-allow fields must stay out of every ordinary record's JSON.
func TestAuditRecordOmitsEmptyAutoFields(t *testing.T) {
	ab, err := json.Marshal(AuditRecord{})
	if err != nil {
		t.Fatal(err)
	}
	var am map[string]any
	if err := json.Unmarshal(ab, &am); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"approval", "waitMs"} {
		if _, ok := am[name]; ok {
			t.Fatalf("AuditRecord{} JSON has %q: %s", name, ab)
		}
	}

	cb, err := json.Marshal(ConfigRecord{})
	if err != nil {
		t.Fatal(err)
	}
	var cm map[string]any
	if err := json.Unmarshal(cb, &cm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"until", "forever", "reason"} {
		if _, ok := cm[name]; ok {
			t.Fatalf("ConfigRecord{} JSON has %q: %s", name, cb)
		}
	}
}

func TestOpenAuditTightensExistingFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", info.Mode().Perm())
	}
}

func TestAuditConfigRecordShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.WriteConfig(ConfigRecord{Time: time.Now(), Action: "trust", Server: "s", Host: "h", Port: 22, Fingerprint: "SHA256:x", Algo: "ssh-ed25519"}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	raw, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["kind"] != "config" || m["action"] != "trust" || m["fingerprint"] != "SHA256:x" || m["algo"] != "ssh-ed25519" {
		t.Fatalf("got %v", m)
	}
	if _, has := m["command"]; has {
		t.Fatal("a config record carries exec fields")
	}
}

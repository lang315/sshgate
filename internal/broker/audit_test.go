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
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", info.Mode().Perm())
	}
	f, _ := os.Open(path)
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

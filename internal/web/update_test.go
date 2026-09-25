package web

import (
	"encoding/json"
	"os"
	"testing"
)

// Web writes go through config.Update: a store whose MAC no longer verifies
// is refused, never re-signed.
func TestWebWriteRefusesTamperedStore(t *testing.T) {
	app, csrf := initApp(t)
	raw, err := os.ReadFile(app.Path)
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	f["servers"] = []any{map[string]any{"name": "evil", "host": "x", "port": 22, "user": "u", "auth": "agent", "aiVisible": true}}
	tampered, _ := json.Marshal(f)
	if err := os.WriteFile(app.Path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	w := doWrite(t, app, csrf, "POST", "/api/servers", `{"name":"ok","host":"h","port":22,"user":"u","auth":"agent"}`)
	if w.Code == 201 {
		t.Fatal("write to a tampered store succeeded")
	}
	if after, _ := os.ReadFile(app.Path); string(after) != string(tampered) {
		t.Fatal("tampered store was rewritten")
	}
}

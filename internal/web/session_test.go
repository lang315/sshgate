package web

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstRunThenUnlock(t *testing.T) {
	dir := t.TempDir()
	app, err := NewApp(8422, filepath.Join(dir, "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	// first run requires bootstrap token
	body := `{"bootstrapToken":"` + app.bootstrap + `","masterPassword":"masterpw1"}`
	r := httptest.NewRequest("POST", "http://127.0.0.1:8422/api/first-run", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.handleFirstRun(w, r)
	if w.Code != 200 {
		t.Fatalf("first-run code %d body %s", w.Code, w.Body.String())
	}
	// wrong bootstrap rejected
	r2 := httptest.NewRequest("POST", "http://127.0.0.1:8422/api/first-run", strings.NewReader(`{"bootstrapToken":"nope","masterPassword":"x"}`))
	r2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	app.handleFirstRun(w2, r2)
	if w2.Code == 200 {
		t.Fatal("already-initialized / wrong token must not succeed")
	}
}

func TestUnlockWrongPassword(t *testing.T) {
	dir := t.TempDir()
	app, _ := NewApp(8422, filepath.Join(dir, "servers.json"))
	body := `{"bootstrapToken":"` + app.bootstrap + `","masterPassword":"masterpw1"}`
	r := httptest.NewRequest("POST", "/api/first-run", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	app.handleFirstRun(httptest.NewRecorder(), r)

	r2 := httptest.NewRequest("POST", "/api/unlock", strings.NewReader(`{"masterPassword":"WRONG"}`))
	r2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	app.handleUnlock(w2, r2)
	if w2.Code == 200 {
		t.Fatal("wrong master password must fail")
	}
}

func TestNewAppRefusesCorruptStore(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "servers.json")
	if err := os.WriteFile(p, []byte("{ this is not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewApp(8422, p); err == nil {
		t.Fatal("NewApp must refuse a corrupt existing store, not treat it as first-run")
	}
}

func TestFirstRunRejectsShortPassword(t *testing.T) {
	dir := t.TempDir()
	app, _ := NewApp(8422, filepath.Join(dir, "servers.json"))
	body := `{"bootstrapToken":"` + app.bootstrap + `","masterPassword":"short"}`
	r := httptest.NewRequest("POST", "/api/first-run", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.handleFirstRun(w, r)
	if w.Code == 200 {
		t.Fatal("master password under 8 chars must be rejected")
	}
}

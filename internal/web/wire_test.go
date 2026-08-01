package web

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutesServeUIAndAPI(t *testing.T) {
	dir := t.TempDir()
	app, _ := NewApp(8422, filepath.Join(dir, "servers.json"))
	h := app.Routes()

	r := httptest.NewRequest("GET", "/api/servers", nil) // no session
	r.Host = "127.0.0.1:8422"                            // pass securityMiddleware's host allowlist so the session guard is what's under test
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("unauth servers should be 401, got %d", w.Code)
	}
	_ = strings.TrimSpace
}

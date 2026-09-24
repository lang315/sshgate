package web

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
)

func TestTestConnectionGenericFailure(t *testing.T) {
	app, csrf := initApp(t)
	// create a server pointing nowhere
	body := `{"name":"dead","host":"127.0.0.1","port":1,"user":"x","auth":"password","password":"y"}`
	cr := httptest.NewRequest("POST", "/api/servers", strings.NewReader(body))
	cr.Header.Set("Content-Type", "application/json")
	cr.Header.Set("Origin", "http://127.0.0.1:8422")
	cr.Header.Set("X-CSRF-Token", csrf)
	c := cookieFor(app)
	cr.AddCookie(&c)
	app.handleServers(httptest.NewRecorder(), cr)

	r := httptest.NewRequest("POST", "/api/test-connection", strings.NewReader(`{"name":"dead"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:8422")
	r.Header.Set("X-CSRF-Token", csrf)
	c2 := cookieFor(app)
	r.AddCookie(&c2)
	w := httptest.NewRecorder()
	app.handleTestConnection(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "refused") || strings.Contains(w.Body.String(), "127.0.0.1") {
		t.Fatal("must not leak dial detail to browser")
	}
	if !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("body %s", w.Body.String())
	}
}

func TestTestConnectionKeyAuthGenericFailure(t *testing.T) {
	dir := t.TempDir()
	kp := filepath.Join(dir, "id")
	if err := os.WriteFile(kp, []byte("not-a-real-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, csrf := initApp(t)
	create := `{"name":"k","host":"127.0.0.1","port":1,"user":"u","auth":"key","keyPath":"` + kp + `"}`
	doWrite(t, app, csrf, "POST", "/api/servers", create)

	r := httptest.NewRequest("POST", "/api/test-connection", strings.NewReader(`{"name":"k"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:8422")
	r.Header.Set("X-CSRF-Token", csrf)
	c := cookieFor(app)
	r.AddCookie(&c)
	w := httptest.NewRecorder()
	app.handleTestConnection(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"ok":false`) {
		t.Fatalf("expected ok:false, got %s", body)
	}
	if strings.Contains(body, kp) || strings.Contains(body, "not-a-real-key") {
		t.Fatal("must not leak key path or key contents")
	}
}

// I8: after a successful Test connection the list must show the pin that
// TOFU just recorded, so "blank = pinned on first Test connection" holds.
func TestTestConnectionPinShowsInList(t *testing.T) {
	srv := sshtest.Start(t)
	close(srv.Release) // let the "true" exec finish
	app, csrf := initApp(t)
	doWrite(t, app, csrf, "POST", "/api/servers",
		fmt.Sprintf(`{"name":"box","host":%q,"port":%d,"user":"u","auth":"password","password":"p"}`, srv.Host, srv.Port))

	w := postJSON(t, app, csrf, "/api/test-connection", map[string]string{"name": "box"})
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("test-connection: %s", w.Body.String())
	}
	r := httptest.NewRequest("GET", "/api/servers", nil)
	c := cookieFor(app)
	r.AddCookie(&c)
	lw := httptest.NewRecorder()
	app.handleServers(lw, r)
	if !strings.Contains(lw.Body.String(), `"hostKey":"SHA256:`) {
		t.Fatalf("pin not in list after test: %s", lw.Body.String())
	}
}

func TestUIHasTestConnectionButton(t *testing.T) {
	app, _ := initApp(t)
	r := httptest.NewRequest("GET", "/app.js", nil)
	r.Host = "127.0.0.1:8422"
	w := httptest.NewRecorder()
	app.Routes().ServeHTTP(w, r)
	js := w.Body.String()
	for _, want := range []string{"'/api/test-connection'", "Test connection", "blank = pinned on first Test connection"} {
		if !strings.Contains(js, want) {
			t.Fatalf("app.js lacks %q", want)
		}
	}
}

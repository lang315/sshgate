package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func initApp(t *testing.T) (*App, string) {
	dir := t.TempDir()
	app, _ := NewApp(8422, filepath.Join(dir, "servers.json"))
	body := `{"bootstrapToken":"` + app.bootstrap + `","masterPassword":"masterpw"}`
	r := httptest.NewRequest("POST", "/api/first-run", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.handleFirstRun(w, r)
	var resp struct{ CSRF string }
	json.Unmarshal(w.Body.Bytes(), &resp)
	return app, resp.CSRF
}

func cookieFor(a *App) http.Cookie {
	return http.Cookie{Name: "ssh_mcp_sess", Value: a.sess.Token}
}

func TestCreateAndListServer(t *testing.T) {
	app, csrf := initApp(t)
	body := `{"name":"prod","host":"1.2.3.4","port":22,"user":"root","auth":"password","password":"s3cret"}`
	r := httptest.NewRequest("POST", "/api/servers", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:8422")
	r.Header.Set("X-CSRF-Token", csrf)
	c := cookieFor(app)
	r.AddCookie(&c)
	w := httptest.NewRecorder()
	app.handleServers(w, r)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create code %d body %s", w.Code, w.Body.String())
	}
	// list must not leak the password
	r2 := httptest.NewRequest("GET", "/api/servers", nil)
	c2 := cookieFor(app)
	r2.AddCookie(&c2)
	w2 := httptest.NewRecorder()
	app.handleServers(w2, r2)
	if strings.Contains(w2.Body.String(), "s3cret") {
		t.Fatal("list leaked the password")
	}
	if !strings.Contains(w2.Body.String(), "prod") {
		t.Fatalf("server not listed: %s", w2.Body.String())
	}
}

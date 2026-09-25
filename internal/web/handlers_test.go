package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
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

func doWrite(t *testing.T, app *App, csrf, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:8422")
	r.Header.Set("X-CSRF-Token", csrf)
	c := cookieFor(app)
	r.AddCookie(&c)
	w := httptest.NewRecorder()
	if path == "/api/servers" {
		app.handleServers(w, r)
	} else {
		app.handleServerByName(w, r)
	}
	return w
}

func TestUpdateHostPreservesDecryptablePassword(t *testing.T) {
	app, csrf := initApp(t)
	doWrite(t, app, csrf, "POST", "/api/servers", `{"name":"prod","host":"1.2.3.4","port":22,"user":"root","auth":"password","password":"s3cret"}`)
	// change host, omit password → must remain decryptable under the NEW AAD
	w := doWrite(t, app, csrf, "PUT", "/api/servers/prod", `{"name":"prod","host":"5.6.7.8","port":22,"user":"root","auth":"password"}`)
	if w.Code != 200 {
		t.Fatalf("PUT code %d body %s", w.Code, w.Body.String())
	}
	f, err := config.Load(app.Path)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := f.FindServer("prod")
	if s.Host != "5.6.7.8" {
		t.Fatalf("host not updated: %s", s.Host)
	}
	got, err := config.Decrypt(app.sess.MasterKey, "prod/encPassword", config.AADFor(f, s, "encPassword"), s.EncPassword)
	if err != nil || got != "s3cret" {
		t.Fatalf("password lost after host change: got=%q err=%v", got, err)
	}
}

func TestDeleteServer(t *testing.T) {
	app, csrf := initApp(t)
	doWrite(t, app, csrf, "POST", "/api/servers", `{"name":"d","host":"h","port":22,"user":"u","auth":"password","password":"x"}`)
	w := doWrite(t, app, csrf, "DELETE", "/api/servers/d", "")
	if w.Code != 204 {
		t.Fatalf("delete code %d body %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/api/servers", nil)
	c := cookieFor(app)
	r.AddCookie(&c)
	lw := httptest.NewRecorder()
	app.handleServers(lw, r)
	if strings.Contains(lw.Body.String(), `"name":"d"`) {
		t.Fatal("server should be gone from the list after delete")
	}
}

func TestServerDTOCarriesAIVisible(t *testing.T) {
	s := config.Server{Name: "a", Host: "h", Port: 22, User: "u", Auth: "key", AIVisible: true}
	d := serverToDTO(s)
	if !d.AIVisible {
		t.Fatal("DTO lost aiVisible")
	}
	back := dtoToServer(d)
	if !back.AIVisible {
		t.Fatal("dtoToServer lost aiVisible")
	}
}

func TestPutStaleIfMatchConflicts(t *testing.T) {
	app, csrf := initApp(t)
	doWrite(t, app, csrf, "POST", "/api/servers", `{"name":"p","host":"h","port":22,"user":"u","auth":"password","password":"x"}`)
	r := httptest.NewRequest("PUT", "/api/servers/p", strings.NewReader(`{"name":"p","host":"h2","port":22,"user":"u","auth":"password"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:8422")
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("If-Match", "999")
	c := cookieFor(app)
	r.AddCookie(&c)
	w := httptest.NewRecorder()
	app.handleServerByName(w, r)
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match must be 412, got %d body %s", w.Code, w.Body.String())
	}
}

// The web form has no key type: an edit that keeps the pin keeps the app's
// HostKeyAlgo (it is part of the dial config); a new pin drops it.
func TestPutKeepsHostKeyAlgoWithThePin(t *testing.T) {
	app, csrf := initApp(t)
	doWrite(t, app, csrf, "POST", "/api/servers", `{"name":"p","host":"h","port":22,"user":"u","auth":"agent","hostKey":"SHA256:abc"}`)
	if err := config.Update(app.Path, app.sess.MasterKey, func(f *config.File) error {
		f.Servers[0].HostKeyAlgo = "ssh-ed25519"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ pin, algo string }{{"SHA256:abc", "ssh-ed25519"}, {"SHA256:new", ""}} {
		w := doWrite(t, app, csrf, "PUT", "/api/servers/p", `{"name":"p","host":"h","port":22,"user":"u","auth":"agent","aiVisible":true,"hostKey":"`+c.pin+`"}`)
		if w.Code != 200 {
			t.Fatalf("PUT code %d body %s", w.Code, w.Body.String())
		}
		f, _ := config.Load(app.Path)
		if s, _ := f.FindServer("p"); s.HostKey != c.pin || s.HostKeyAlgo != c.algo || !s.AIVisible {
			t.Fatalf("pin %s: %+v", c.pin, s)
		}
	}
}

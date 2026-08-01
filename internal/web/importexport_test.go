package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
)

func TestParseSSHConfig(t *testing.T) {
	text := `
Host prod
    HostName 1.2.3.4
    Port 2222
    User root
    IdentityFile ~/.ssh/id_ed25519

Host *
    ForwardAgent yes
`
	entries, notes := ParseSSHConfig(text)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Name != "prod" || e.Host != "1.2.3.4" || e.Port != 2222 || e.User != "root" || e.Auth != "key" {
		t.Fatalf("parsed wrong: %+v", e)
	}
	if len(notes) == 0 {
		t.Fatal("wildcard Host * should produce a skip note")
	}
}

func postJSON(t *testing.T, app *App, csrf, path string, v any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:8422")
	r.Header.Set("X-CSRF-Token", csrf)
	c := cookieFor(app)
	r.AddCookie(&c)
	w := httptest.NewRecorder()
	switch path {
	case "/api/import/preview":
		app.handleImportPreview(w, r)
	case "/api/import/apply":
		app.handleImportApply(w, r)
	case "/api/export":
		app.handleExport(w, r)
	}
	return w
}

func TestImportPreviewAndApplyJSON(t *testing.T) {
	app, csrf := initApp(t)
	doWrite(t, app, csrf, "POST", "/api/servers", `{"name":"prod","host":"1.2.3.4","port":22,"user":"root","auth":"password","password":"s3cret"}`)

	payload, _ := json.Marshal([]ServerDTO{
		{Name: "prod", Host: "9.9.9.9", Port: 22, User: "root", Auth: "password"}, // conflicts with existing "prod"
		{Name: "staging", Host: "5.6.7.8", Port: 22, User: "deploy", Auth: "password"},
	})

	w := postJSON(t, app, csrf, "/api/import/preview", map[string]string{"source": "json", "payload": string(payload)})
	if w.Code != 200 {
		t.Fatalf("preview code %d body %s", w.Code, w.Body.String())
	}
	var preview struct {
		Entries   []ServerDTO
		Conflicts []string
		Notes     []string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(preview.Entries))
	}
	if len(preview.Conflicts) != 1 || preview.Conflicts[0] != "prod" {
		t.Fatalf("want conflict on prod, got %v", preview.Conflicts)
	}

	w2 := postJSON(t, app, csrf, "/api/import/apply", map[string]any{"entries": preview.Entries})
	if w2.Code != 200 {
		t.Fatalf("apply code %d body %s", w2.Code, w2.Body.String())
	}
	var applied struct{ Applied int }
	json.Unmarshal(w2.Body.Bytes(), &applied)
	if applied.Applied != 1 {
		t.Fatalf("want applied=1 (staging only; prod is a conflict), got %d", applied.Applied)
	}
	f, err := config.Load(app.Path)
	if err != nil {
		t.Fatal(err)
	}
	if prod, _ := f.FindServer("prod"); prod.Host != "1.2.3.4" {
		t.Fatalf("existing prod server must be untouched by import, got host=%s", prod.Host)
	}
	if _, ok := f.FindServer("staging"); !ok {
		t.Fatal("staging not created by import apply")
	}
}

func TestExportWithoutSecretsStripsFieldsAndDoesNotCorruptLiveState(t *testing.T) {
	app, csrf := initApp(t)
	doWrite(t, app, csrf, "POST", "/api/servers", `{"name":"prod","host":"1.2.3.4","port":22,"user":"root","auth":"password","password":"s3cret"}`)

	w := postJSON(t, app, csrf, "/api/export", map[string]bool{"secrets": false})
	if w.Code != 200 {
		t.Fatalf("export code %d body %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "s3cret") || strings.Contains(body, "encPassword") || strings.Contains(body, `"kdf"`) {
		t.Fatalf("no-secrets export leaked sensitive fields: %s", body)
	}

	// the exported copy must be independent of the live in-memory file: stripping
	// secrets for export must not blank out a.file's own ciphertext (shared backing
	// array via a shallow slice copy would do exactly that).
	app.mu.Lock()
	live, exists := app.file.FindServer("prod")
	app.mu.Unlock()
	if !exists || live.EncPassword == "" {
		t.Fatalf("export corrupted live in-memory server state: %+v", live)
	}
	got, err := config.Decrypt(app.sess.MasterKey, "prod/encPassword", config.AADFor(app.file, live, "encPassword"), live.EncPassword)
	if err != nil || got != "s3cret" {
		t.Fatalf("live password no longer decryptable after export: got=%q err=%v", got, err)
	}
}

func TestExportWithSecretsReencryptsUnderExportKey(t *testing.T) {
	app, csrf := initApp(t)
	doWrite(t, app, csrf, "POST", "/api/servers", `{"name":"prod","host":"1.2.3.4","port":22,"user":"root","auth":"password","password":"s3cret"}`)

	// simulate an existing encKeyPassphrase blob under the master key (no DTO field
	// creates one yet, but old vault files/future features can populate it).
	app.mu.Lock()
	idx := -1
	for i, s := range app.file.Servers {
		if s.Name == "prod" {
			idx = i
		}
	}
	if idx < 0 {
		app.mu.Unlock()
		t.Fatal("prod not found")
	}
	blob, err := config.Encrypt(app.sess.MasterKey, "prod/encKeyPassphrase", config.AADFor(app.file, app.file.Servers[idx], "encKeyPassphrase"), "keypass123")
	if err != nil {
		app.mu.Unlock()
		t.Fatal(err)
	}
	app.file.Servers[idx].EncKeyPassphrase = blob
	app.mu.Unlock()

	w := postJSON(t, app, csrf, "/api/export", map[string]any{"secrets": true, "exportPassphrase": "exportpw123"})
	if w.Code != 200 {
		t.Fatalf("export code %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("missing attachment header: %s", w.Header().Get("Content-Disposition"))
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing no-store cache-control: %s", w.Header().Get("Cache-Control"))
	}

	var out config.File
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.KDF == nil || out.KDF.Salt == app.file.KDF.Salt {
		t.Fatalf("export must carry a fresh KDF, not the master KDF: %+v", out.KDF)
	}
	ek, err := out.KDF.DeriveKey("exportpw123")
	if err != nil {
		t.Fatal(err)
	}
	s, ok := out.FindServer("prod")
	if !ok {
		t.Fatal("prod missing from export")
	}
	pw, err := config.Decrypt(ek, "prod/encPassword", config.AADFor(&out, s, "encPassword"), s.EncPassword)
	if err != nil || pw != "s3cret" {
		t.Fatalf("password not decryptable under export key: got=%q err=%v", pw, err)
	}
	kp, err := config.Decrypt(ek, "prod/encKeyPassphrase", config.AADFor(&out, s, "encKeyPassphrase"), s.EncKeyPassphrase)
	if err != nil || kp != "keypass123" {
		t.Fatalf("key passphrase not decryptable under export key: got=%q err=%v", kp, err)
	}
	// genuinely re-encrypted (not copied verbatim): the master key must NOT open it.
	if _, err := config.Decrypt(app.sess.MasterKey, "prod/encPassword", config.AADFor(&out, s, "encPassword"), s.EncPassword); err == nil {
		t.Fatal("export ciphertext should not decrypt under the master key")
	}
}

func TestExportRequiresPassphraseForSecrets(t *testing.T) {
	app, csrf := initApp(t)
	w := postJSON(t, app, csrf, "/api/export", map[string]bool{"secrets": true})
	if w.Code != 400 {
		t.Fatalf("want 400 without exportPassphrase, got %d body %s", w.Code, w.Body.String())
	}
}

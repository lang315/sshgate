package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostAllowlist(t *testing.T) {
	h := securityMiddleware(8422, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "http://evil.com/", nil)
	req.Host = "evil.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("evil Host should be 403, got %d", rec.Code)
	}

	req2 := httptest.NewRequest("GET", "http://127.0.0.1:8422/", nil)
	req2.Host = "127.0.0.1:8422"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("localhost Host should pass, got %d", rec2.Code)
	}
	if rec2.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("CSP header missing")
	}
}

func TestSecurityHeadersOn403(t *testing.T) {
	h := securityMiddleware(8422, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest("GET", "http://evil/", nil)
	req.Host = "evil"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("code %d", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("403 response must still carry security headers")
	}
}

func TestCheckOriginCSRFRejectsEmptyOrigin(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/x", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "tok")
	if err := checkOriginCSRF(req, "8422", "tok"); err == nil {
		t.Fatal("missing Origin on a write must be rejected")
	}
}

func TestCheckOriginCSRFAccepts(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/x", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8422")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "tok")
	if err := checkOriginCSRF(req, "8422", "tok"); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

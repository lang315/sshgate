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

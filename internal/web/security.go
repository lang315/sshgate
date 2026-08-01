package web

import (
	"fmt"
	"net/http"
	"strconv"
)

func allowedHosts(port int) map[string]bool {
	p := strconv.Itoa(port)
	return map[string]bool{"127.0.0.1:" + p: true, "localhost:" + p: true}
}

func securityMiddleware(port int, next http.Handler) http.Handler {
	hosts := allowedHosts(port)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func checkOriginCSRF(r *http.Request, port, sessionToken string) error {
	origin := r.Header.Get("Origin")
	if origin != "" && origin != "http://127.0.0.1:"+port && origin != "http://localhost:"+port {
		return fmt.Errorf("bad origin")
	}
	if r.Header.Get("Content-Type") != "application/json" {
		return fmt.Errorf("content-type must be application/json")
	}
	if r.Header.Get("X-CSRF-Token") == "" || r.Header.Get("X-CSRF-Token") != sessionToken {
		return fmt.Errorf("bad csrf token")
	}
	return nil
}

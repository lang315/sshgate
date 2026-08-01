package web

import (
	"embed"
	"io/fs"
	"net/http"
	"time"
)

//go:embed static
var staticFS embed.FS

func New(port int, handler http.Handler) *http.Server {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	if handler != nil {
		mux.Handle("/api/", handler)
	}
	return &http.Server{
		Addr:              "127.0.0.1:" + itoa(port),
		Handler:           securityMiddleware(port, mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
}

func itoa(n int) string {
	return strconvItoa(n)
}

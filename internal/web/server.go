package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/first-run", a.handleFirstRun)
	mux.HandleFunc("/api/unlock", a.handleUnlock)
	mux.HandleFunc("/api/lock", a.handleLock)
	mux.HandleFunc("/api/servers", a.handleServers)
	mux.HandleFunc("/api/servers/", a.handleServerByName)
	mux.HandleFunc("/api/import/preview", a.handleImportPreview)
	mux.HandleFunc("/api/import/apply", a.handleImportApply)
	mux.HandleFunc("/api/export", a.handleExport)

	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return securityMiddleware(a.Port, mux)
}

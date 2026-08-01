package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/web"
)

func runWeb(args []string) error {
	m := config.ParseArgv(args)
	port := 8422
	if p := m["port"]; p != nil {
		fmt.Sscanf(*p, "%d", &port)
	}
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "ssh-mcp", "servers.json")
	app, err := web.NewApp(port, path)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           app.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "ssh-mcp web UI on http://127.0.0.1:%d\n", port)
	return srv.ListenAndServe()
}

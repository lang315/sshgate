package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

func (a *App) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.writeGuard(w, r)
	if !ok {
		return
	}
	var in struct{ Name string }
	if err := readJSON(r, &in); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	a.mu.Lock()
	s, found := a.file.FindServer(in.Name)
	f := a.file
	a.mu.Unlock()
	if !found {
		http.Error(w, "not found", 404)
		return
	}
	dec := func(field, blob string) string {
		if blob == "" {
			return ""
		}
		pt, _ := config.Decrypt(sess.MasterKey, s.Name+"/"+field, config.AADFor(f, s, field), blob)
		return pt
	}
	mgr := sshx.NewManager(sshx.DialConfig{
		Host: s.Host, Port: s.Port, User: s.User, Auth: s.Auth,
		Password: dec("encPassword", s.EncPassword), HostKey: s.HostKey,
		Insecure: s.HostKey == "", TimeoutMs: 8000,
	})
	defer mgr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err := mgr.Exec(ctx, "true")
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ssh-mcp] test-connection %s failed: %v\n", in.Name, err)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "connection test failed"})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

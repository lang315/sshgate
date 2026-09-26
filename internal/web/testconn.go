package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
)

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

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
	dc := sshx.DialConfig{
		Host: s.Host, Port: s.Port, User: s.User, Auth: s.Auth,
		Password: dec("encPassword", s.EncPassword), HostKey: s.HostKey,
		Insecure: false, TimeoutMs: 8000,
	}
	dc.OnLearnHostKey = func(fp string) {
		_ = config.RecordHostKey(a.Path, s.Name, s.Host, s.Port, fp, "", sess.MasterKey)
		// The list is served from a.file; reload so the new pin shows.
		if f, err := config.Load(a.Path); err == nil {
			a.mu.Lock()
			a.file = f
			a.mu.Unlock()
		}
	}
	if s.Auth == "key" {
		if s.EncKeyPassphrase != "" {
			dc.Passphrase = dec("encKeyPassphrase", s.EncKeyPassphrase)
		}
		if s.KeyPath != "" {
			data, err := os.ReadFile(expandHome(s.KeyPath))
			if err != nil {
				fmt.Fprintf(os.Stderr, "[sshgate] test-connection %s: reading key file: %v\n", in.Name, err)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "connection test failed"})
				return
			}
			dc.PrivateKey = string(data)
		}
	}
	mgr := sshx.NewManager(dc)
	defer mgr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err := mgr.Exec(ctx, "true")
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[sshgate] test-connection %s failed: %v\n", in.Name, err)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "connection test failed"})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

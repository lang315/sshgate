package web

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/lang315/ssh-mcp/internal/config"
)

func ParseSSHConfig(text string) ([]ServerDTO, []string) {
	var entries []ServerDTO
	var notes []string
	var cur *ServerDTO
	flush := func() {
		if cur != nil && cur.Host != "" {
			if cur.Port == 0 {
				cur.Port = 22
			}
			entries = append(entries, *cur)
		}
		cur = nil
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		key := strings.ToLower(fields[0])
		val := strings.Join(fields[1:], " ")
		switch key {
		case "host":
			flush()
			if strings.ContainsAny(val, "*?") || strings.Contains(val, " ") {
				notes = append(notes, "skipped wildcard/multi Host: "+val)
				cur = nil
				continue
			}
			cur = &ServerDTO{Name: val, Auth: "password"}
		case "include", "match":
			notes = append(notes, "skipped "+key+" directive")
		case "hostname":
			if cur != nil {
				cur.Host = val
			}
		case "port":
			if cur != nil {
				cur.Port, _ = strconv.Atoi(val)
			}
		case "user":
			if cur != nil {
				cur.User = val
			}
		case "identityfile":
			if cur != nil {
				cur.KeyPath = val
				cur.Auth = "key"
			}
		}
	}
	flush()
	return entries, notes
}

func (a *App) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.writeGuard(w, r); !ok {
		return
	}
	var in struct{ Source, Payload string }
	if err := readJSON(r, &in); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	var entries []ServerDTO
	var notes []string
	switch in.Source {
	case "ssh_config":
		entries, notes = ParseSSHConfig(in.Payload)
	case "json":
		json.Unmarshal([]byte(in.Payload), &entries)
	default:
		http.Error(w, "unknown source", 400)
		return
	}
	if len(entries) > 500 {
		entries = entries[:500]
		notes = append(notes, "truncated to 500 entries")
	}
	a.mu.Lock()
	var conflicts []string
	for _, e := range entries {
		if _, exists := a.file.FindServer(e.Name); exists {
			conflicts = append(conflicts, e.Name)
		}
	}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"entries": entries, "conflicts": conflicts, "notes": notes})
}

func (a *App) handleImportApply(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.writeGuard(w, r)
	if !ok {
		return
	}
	var in struct{ Entries []ServerDTO }
	if err := readJSON(r, &in); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	applied := 0
	err := a.saveLocked(func(f *config.File) error {
		for _, e := range in.Entries {
			if !nameRe.MatchString(e.Name) {
				continue
			}
			if _, exists := f.FindServer(e.Name); exists {
				continue // skip conflicts; UI renames before apply
			}
			s := dtoToServer(e)
			if err := encryptSecrets(&s, f, e, sess.MasterKey); err != nil {
				return err
			}
			f.Servers = append(f.Servers, s)
			applied++
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"applied": applied})
}

func (a *App) handleExport(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.writeGuard(w, r)
	if !ok {
		return
	}
	var in struct {
		Secrets          bool
		ExportPassphrase string
	}
	if err := readJSON(r, &in); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !in.Secrets {
		clean := *a.file
		clean.KDF = nil
		clean.MAC = ""
		clean.Servers = make([]config.Server, len(a.file.Servers))
		copy(clean.Servers, a.file.Servers)
		for i := range clean.Servers {
			clean.Servers[i].EncPassword = ""
			clean.Servers[i].EncSuPassword = ""
			clean.Servers[i].EncSudoPassword = ""
			clean.Servers[i].EncKeyPassphrase = ""
		}
		writeExport(w, clean)
		return
	}
	if in.ExportPassphrase == "" {
		http.Error(w, "export passphrase required for secrets", 400)
		return
	}
	k, ek, err := config.NewKDF(in.ExportPassphrase)
	if err != nil {
		http.Error(w, "error", 500)
		return
	}
	out := config.File{Version: a.file.Version, KDF: &k, Servers: make([]config.Server, len(a.file.Servers))}
	copy(out.Servers, a.file.Servers)
	// re-encrypt each secret from master key to export key
	reenc := func(field string, s *config.Server, blob *string) error {
		if *blob == "" {
			return nil
		}
		pt, err := config.Decrypt(sess.MasterKey, s.Name+"/"+field, config.AADFor(a.file, *s, field), *blob)
		if err != nil {
			return err
		}
		nb, err := config.Encrypt(ek, s.Name+"/"+field, config.AADFor(&out, *s, field), pt)
		if err != nil {
			return err
		}
		*blob = nb
		return nil
	}
	for i := range out.Servers {
		s := &out.Servers[i]
		if err := reenc("encPassword", s, &s.EncPassword); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := reenc("encSuPassword", s, &s.EncSuPassword); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := reenc("encSudoPassword", s, &s.EncSudoPassword); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := reenc("encKeyPassphrase", s, &s.EncKeyPassphrase); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	writeExport(w, out)
}

func writeExport(w http.ResponseWriter, f config.File) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=ssh-mcp-export.json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(f)
}

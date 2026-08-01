package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/lang315/ssh-mcp/internal/config"
)

type ServerDTO struct {
	Name            string  `json:"name"`
	Host            string  `json:"host"`
	Port            int     `json:"port"`
	User            string  `json:"user"`
	Auth            string  `json:"auth"`
	KeyPath         string  `json:"keyPath,omitempty"`
	HostKey         string  `json:"hostKey,omitempty"`
	HasPassword     bool    `json:"hasPassword"`
	HasSuPassword   bool    `json:"hasSuPassword"`
	HasSudoPassword bool    `json:"hasSudoPassword"`
	Password        *string `json:"password,omitempty"`
	SuPassword      *string `json:"suPassword,omitempty"`
	SudoPassword    *string `json:"sudoPassword,omitempty"`
}

var nameRe = regexpMustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func (a *App) writeGuard(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	s, err := a.requireSession(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	if err := checkOriginCSRF(r, strconv.Itoa(a.Port), s.CSRF); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return nil, false
	}
	return s, true
}

func (a *App) handleServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if _, err := a.requireSession(r); err != nil {
			http.Error(w, "unauthorized", 401)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		var out []ServerDTO
		for _, s := range a.file.Servers {
			out = append(out, ServerDTO{
				Name: s.Name, Host: s.Host, Port: s.Port, User: s.User, Auth: s.Auth,
				KeyPath: s.KeyPath, HostKey: s.HostKey,
				HasPassword: s.EncPassword != "", HasSuPassword: s.EncSuPassword != "", HasSudoPassword: s.EncSudoPassword != "",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	case http.MethodPost:
		sess, ok := a.writeGuard(w, r)
		if !ok {
			return
		}
		var dto ServerDTO
		if err := readJSON(r, &dto); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if !nameRe.MatchString(dto.Name) {
			http.Error(w, "invalid name", 400)
			return
		}
		err := a.saveLocked(func(f *config.File) error {
			if _, exists := f.FindServer(dto.Name); exists {
				return fmt.Errorf("name exists")
			}
			s := dtoToServer(dto)
			if err := encryptSecrets(&s, f, dto, sess.MasterKey); err != nil {
				return err
			}
			f.Servers = append(f.Servers, s)
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(201)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (a *App) handleServerByName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/servers/")
	sess, ok := a.writeGuard(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodDelete:
		err := a.saveLocked(func(f *config.File) error {
			out := f.Servers[:0]
			found := false
			for _, s := range f.Servers {
				if s.Name == name {
					found = true
					continue
				}
				out = append(out, s)
			}
			if !found {
				return fmt.Errorf("not found")
			}
			f.Servers = out
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.WriteHeader(204)
	case http.MethodPut:
		var dto ServerDTO
		if err := readJSON(r, &dto); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if ifm := r.Header.Get("If-Match"); ifm != "" {
			a.mu.Lock()
			curRev := a.file.Revision
			a.mu.Unlock()
			if rev, _ := strconv.Atoi(ifm); rev != curRev {
				http.Error(w, "revision conflict", http.StatusPreconditionFailed)
				return
			}
		}
		err := a.saveLocked(func(f *config.File) error {
			for i := range f.Servers {
				if f.Servers[i].Name == name {
					updated := dtoToServer(dto)
					updated.Name = name
					// preserve existing ciphertext when the DTO omits a secret
					updated.EncPassword = f.Servers[i].EncPassword
					updated.EncSuPassword = f.Servers[i].EncSuPassword
					updated.EncSudoPassword = f.Servers[i].EncSudoPassword
					if err := encryptSecrets(&updated, f, dto, sess.MasterKey); err != nil {
						return err
					}
					f.Servers[i] = updated
					return nil
				}
			}
			return fmt.Errorf("not found")
		})
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(200)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func dtoToServer(d ServerDTO) config.Server {
	return config.Server{Name: d.Name, Host: d.Host, Port: d.Port, User: d.User, Auth: d.Auth, KeyPath: d.KeyPath, HostKey: d.HostKey}
}

func encryptSecrets(s *config.Server, f *config.File, d ServerDTO, mk []byte) error {
	enc := func(field string, val *string, dst *string) error {
		if val == nil {
			return nil // keep existing
		}
		if *val == "" {
			*dst = ""
			return nil
		}
		blob, err := config.Encrypt(mk, s.Name+"/"+field, config.AADFor(f, *s, field), *val)
		if err != nil {
			return err
		}
		*dst = blob
		return nil
	}
	if err := enc("encPassword", d.Password, &s.EncPassword); err != nil {
		return err
	}
	if err := enc("encSuPassword", d.SuPassword, &s.EncSuPassword); err != nil {
		return err
	}
	return enc("encSudoPassword", d.SudoPassword, &s.EncSudoPassword)
}

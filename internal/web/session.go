package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/lang315/sshgate/internal/config"
)

type Session struct {
	Token     string
	CSRF      string
	MasterKey []byte
	Created   time.Time
	LastSeen  time.Time
}

type App struct {
	Port      int
	Path      string
	mu        sync.Mutex
	file      *config.File
	sess      *Session
	bootstrap string
	attempts  int
	lockUntil time.Time
}

func token() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func NewApp(port int, path string) (*App, error) {
	a := &App{Port: port, Path: path}
	if f, err := config.Load(path); err == nil {
		a.file = f
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot read config store %s (refusing to overwrite): %w", path, err)
	}
	if a.file == nil || a.file.KDF == nil {
		a.bootstrap = token()
		fmt.Fprintf(os.Stderr, "\n[sshgate] First-run setup token: %s\n(enter this in the browser to set your master password)\n\n", a.bootstrap)
	}
	return a, nil
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

func (a *App) handleFirstRun(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil && a.file.KDF != nil {
		http.Error(w, "already initialized", http.StatusConflict)
		return
	}
	var in struct{ BootstrapToken, MasterPassword string }
	if err := readJSON(r, &in); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if a.bootstrap == "" || subtle.ConstantTimeCompare([]byte(in.BootstrapToken), []byte(a.bootstrap)) != 1 {
		http.Error(w, "invalid bootstrap token", http.StatusForbidden)
		return
	}
	if len(in.MasterPassword) < 8 {
		http.Error(w, "master password too short (min 8)", 400)
		return
	}
	k, mk, err := config.NewKDF(in.MasterPassword)
	if err != nil {
		http.Error(w, "error", 500)
		return
	}
	var f *config.File
	err = config.Update(a.Path, mk, func(cur *config.File) error {
		if cur.KDF != nil {
			return fmt.Errorf("already initialized")
		}
		cur.KDF, cur.Servers = &k, []config.Server{}
		f = cur
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.file = f
	a.newSession(w, mk)
}

func (a *App) handleUnlock(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file == nil || a.file.KDF == nil {
		http.Error(w, "not initialized", 400)
		return
	}
	if time.Now().Before(a.lockUntil) {
		http.Error(w, "too many attempts, try later", http.StatusTooManyRequests)
		return
	}
	var in struct{ MasterPassword string }
	if err := readJSON(r, &in); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	mk, err := a.file.KDF.DeriveKey(in.MasterPassword)
	if err != nil || !a.file.KDF.Verify(mk) {
		a.attempts++
		if a.attempts >= 5 {
			a.lockUntil = time.Now().Add(30 * time.Second)
			a.attempts = 0
		}
		http.Error(w, "unlock failed", http.StatusUnauthorized)
		return
	}
	if err := a.file.VerifyMAC(mk); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.attempts = 0
	a.newSession(w, mk)
}

func (a *App) newSession(w http.ResponseWriter, mk []byte) {
	s := &Session{Token: token(), CSRF: token(), MasterKey: mk, Created: time.Now(), LastSeen: time.Now()}
	a.sess = s
	http.SetCookie(w, &http.Cookie{
		Name: "sshgate_sess", Value: s.Token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"csrf": s.CSRF})
}

func (a *App) handleLock(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sess = nil
	w.WriteHeader(204)
}

func (a *App) requireSession(r *http.Request) (*Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := r.Cookie("sshgate_sess")
	if err != nil || a.sess == nil {
		return nil, fmt.Errorf("no session")
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.sess.Token)) != 1 {
		return nil, fmt.Errorf("bad session")
	}
	if time.Since(a.sess.LastSeen) > 15*time.Minute {
		a.sess = nil
		return nil, fmt.Errorf("session expired")
	}
	a.sess.LastSeen = time.Now()
	return a.sess, nil
}

// saveLocked runs mutate through config.Update, which reloads the file,
// checks its MAC, and serializes with every other writer, the hub included.
// It needs an unlocked session.
func (a *App) saveLocked(mutate func(*config.File) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess == nil {
		return fmt.Errorf("locked")
	}
	var saved *config.File
	err := config.Update(a.Path, a.sess.MasterKey, func(f *config.File) error {
		if err := mutate(f); err != nil {
			return err
		}
		saved = f
		return nil
	})
	if err == nil {
		a.file = saved
	}
	return err
}

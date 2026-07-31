# ssh-mcp Go Conversion + Web Config UI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the TypeScript ssh-mcp MCP server with a Go implementation that keeps CLI parity, adds multi-server support, and ships a localhost web UI for CRUD + import of SSH connections.

**Architecture:** One Go module, one binary, two subcommands: `ssh-mcp` (MCP stdio server) and `ssh-mcp web` (localhost config UI). Layered packages: `internal/config` (encrypted store), `internal/sshx` (connection manager), `internal/mcpserver` (tools), `internal/web` (HTTP API + embedded UI). Secrets encrypted at rest (argon2id + AES-256-GCM), master password from file/TTY only.

**Tech Stack:** Go 1.26, `github.com/modelcontextprotocol/go-sdk` v1.7.0, `golang.org/x/crypto` (ssh, ssh/agent, ssh/knownhosts, argon2, hkdf), stdlib `net/http` + `go:embed`, `testcontainers-go` (test only).

## Global Constraints

- Module path: `github.com/lang315/ssh-mcp`. Go version floor: `go 1.26`.
- Runtime deps limited to: `github.com/modelcontextprotocol/go-sdk`, `golang.org/x/crypto`. `testcontainers-go` is test-only. No others.
- Binary name: `ssh-mcp`. Default mode = MCP stdio server. `ssh-mcp web` = config UI.
- Config file: `~/.config/ssh-mcp/servers.json`, dir mode `0700`, file mode `0600`, atomic writes (temp 0600 → fsync → rename in same dir).
- Master password read ONLY from `SSH_MCP_MASTER_PASSWORD_FILE` (mode-checked 0600) or interactive TTY. NEVER from argv.
- MCP mode: nothing writes to stdout except the SDK. All logs → stderr.
- Secrets (passwords, passphrases) must never appear in tool output, tool errors, logs, or a remote process's argv. Redaction is mandatory.
- SSH host key verification is ON by default (`knownhosts` + pinned fingerprint). `--insecureIgnoreHostKey` opt-in only.
- Web server binds `127.0.0.1` only; enforces Host + Origin + CSRF on all state-changing requests.
- Preserve every existing CLI flag: `--host --port --user --password --key --suPassword --sudoPassword --disableSudo --timeout --maxChars`.
- Keep existing TS sources until the Go test suite is green; delete in the final task.
- MCP tool handler signature (go-sdk v1.7.0): `func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error)`. A returned non-nil `*mcp.CallToolResult{IsError:true}` is the error channel visible to the model.

---

## File Structure

```
go.mod
cmd/ssh-mcp/main.go              subcommand dispatch, flag parsing, run
internal/config/argv.go          parseArgv, validateConfig (CLI parity)
internal/config/sanitize.go      sanitizeCommand, escapeShellSingleQuote, appendDescription
internal/config/crypto.go        argon2id KDF, AES-GCM encrypt/decrypt w/ AAD, HKDF subkeys, verifier
internal/config/store.go         Store type: load/save servers.json, atomic write, MAC, revision, flock
internal/config/masterpw.go      read master password from file (mode-checked) or TTY
internal/config/redact.go        Redactor: replace known secrets with ***
internal/sshx/wrap.go            wrapSudo, su command framing (pure funcs)
internal/sshx/hostkey.go         HostKeyCallback builder (knownhosts + FixedHostKey + TOFU record)
internal/sshx/manager.go         Manager: dial, persistent conn, reconnect, su elevation, exec
internal/sshx/registry.go        Registry: map[name]*Manager, mutex, config-hash keying
internal/mcpserver/tools.go      exec, sudo-exec, list-servers registration + handlers
internal/mcpserver/server.go     BuildServer(deps) wiring
internal/web/server.go           http.Server setup, embed, hardening, mux
internal/web/security.go         Host/Origin/CSRF middleware, security headers
internal/web/session.go          in-memory session, unlock, first-run bootstrap, rate limit
internal/web/handlers.go         servers CRUD, test-connection
internal/web/importexport.go     ssh_config parser, JSON import, export (re-encrypt)
internal/web/static/index.html   UI
internal/web/static/app.js       UI logic (textContent only)
internal/web/static/style.css    UI styles
```

---

## Phase 0 — Scaffolding

### Task 1: Module init and subcommand skeleton

**Files:**
- Create: `go.mod`, `cmd/ssh-mcp/main.go`
- Test: `cmd/ssh-mcp/main_test.go`

**Interfaces:**
- Produces: `func route(args []string) (mode string, rest []string)` returning `"web"` or `"mcp"`.

- [ ] **Step 1: Init module**

```bash
cd /Users/lang/GolandProjects/github.com/lang315/ssh-mcp
go mod init github.com/lang315/ssh-mcp
go get github.com/modelcontextprotocol/go-sdk@v1.7.0
go get golang.org/x/crypto@latest
```

- [ ] **Step 2: Write failing test**

`cmd/ssh-mcp/main_test.go`:
```go
package main

import "testing"

func TestRoute(t *testing.T) {
	cases := []struct {
		in   []string
		mode string
	}{
		{[]string{"web", "--port", "9000"}, "web"},
		{[]string{"--host=1.2.3.4", "--user=root"}, "mcp"},
		{[]string{}, "mcp"},
	}
	for _, c := range cases {
		got, _ := route(c.in)
		if got != c.mode {
			t.Fatalf("route(%v) = %q, want %q", c.in, got, c.mode)
		}
	}
}
```

- [ ] **Step 3: Run — expect FAIL**

Run: `go test ./cmd/...`
Expected: FAIL (`undefined: route`).

- [ ] **Step 4: Implement**

`cmd/ssh-mcp/main.go`:
```go
package main

import (
	"fmt"
	"os"
)

func route(args []string) (mode string, rest []string) {
	if len(args) > 0 && args[0] == "web" {
		return "web", args[1:]
	}
	return "mcp", args
}

func main() {
	mode, rest := route(os.Args[1:])
	switch mode {
	case "web":
		fmt.Fprintln(os.Stderr, "web mode not yet wired", rest)
	default:
		fmt.Fprintln(os.Stderr, "mcp mode not yet wired", rest)
	}
}
```

- [ ] **Step 5: Run — expect PASS**

Run: `go test ./cmd/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum cmd/ssh-mcp/
git commit -m "chore: init go module and subcommand skeleton"
```

---

## Phase A — Config store + crypto

### Task 2: CLI argv parsing (parity with TS)

**Files:**
- Create: `internal/config/argv.go`, `internal/config/argv_test.go`

**Interfaces:**
- Produces:
  - `type CLIConfig struct { Host, User, Password, Key, SuPassword, SudoPassword string; Port int; DisableSudo bool; TimeoutMs int; MaxChars int; HasHost bool; ... }`
  - `func ParseArgv(args []string) map[string]*string`
  - `func BuildCLIConfig(m map[string]*string) (CLIConfig, error)` — applies defaults (port 22, timeout 60000, maxChars 1000) and validation (host+user required, port numeric).
  - `func ParseMaxChars(raw *string) int` — returns `-1` for unlimited (`none`/0/negative), else the value; default 1000.

- [ ] **Step 1: Write failing tests**

`internal/config/argv_test.go`:
```go
package config

import "testing"

func TestParseArgv(t *testing.T) {
	m := ParseArgv([]string{"--host=1.2.3.4", "--disableSudo", "--port=2222"})
	if *m["host"] != "1.2.3.4" {
		t.Fatalf("host = %v", m["host"])
	}
	if m["disableSudo"] != nil {
		t.Fatalf("flag should map to nil value")
	}
	if _, ok := m["disableSudo"]; !ok {
		t.Fatalf("disableSudo key missing")
	}
	if *m["port"] != "2222" {
		t.Fatalf("port = %v", m["port"])
	}
}

func TestParseMaxChars(t *testing.T) {
	s := func(v string) *string { return &v }
	if ParseMaxChars(nil) != 1000 {
		t.Fatal("default")
	}
	if ParseMaxChars(s("none")) != -1 {
		t.Fatal("none")
	}
	if ParseMaxChars(s("0")) != -1 {
		t.Fatal("zero")
	}
	if ParseMaxChars(s("500")) != 500 {
		t.Fatal("500")
	}
	if ParseMaxChars(s("garbage")) != 1000 {
		t.Fatal("garbage falls back to default")
	}
}

func TestBuildCLIConfigValidation(t *testing.T) {
	s := func(v string) *string { return &v }
	_, err := BuildCLIConfig(map[string]*string{"host": s("h")}) // missing user
	if err == nil {
		t.Fatal("expected missing-user error")
	}
	cfg, err := BuildCLIConfig(map[string]*string{"host": s("h"), "user": s("u")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 22 || cfg.TimeoutMs != 60000 || cfg.MaxChars != 1000 {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/config/ -run 'Argv|MaxChars|CLIConfig'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

`internal/config/argv.go`:
```go
package config

import (
	"fmt"
	"strconv"
	"strings"
)

type CLIConfig struct {
	Host, User, Password, Key string
	SuPassword, SudoPassword  string
	Port, TimeoutMs, MaxChars int
	DisableSudo               bool
	HasHost                   bool
	HasSuPassword             bool
	HasSudoPassword           bool
}

func ParseArgv(args []string) map[string]*string {
	out := map[string]*string{}
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			continue
		}
		body := a[2:]
		if i := strings.IndexByte(body, '='); i >= 0 {
			v := body[i+1:]
			out[body[:i]] = &v
		} else {
			out[body] = nil
		}
	}
	return out
}

func ParseMaxChars(raw *string) int {
	if raw == nil {
		return 1000
	}
	if strings.EqualFold(*raw, "none") {
		return -1
	}
	n, err := strconv.Atoi(*raw)
	if err != nil {
		return 1000
	}
	if n <= 0 {
		return -1
	}
	return n
}

func BuildCLIConfig(m map[string]*string) (CLIConfig, error) {
	get := func(k string) string {
		if v, ok := m[k]; ok && v != nil {
			return *v
		}
		return ""
	}
	c := CLIConfig{
		Host: get("host"), User: get("user"), Password: get("password"),
		Key: get("key"), SuPassword: get("suPassword"), SudoPassword: get("sudoPassword"),
		Port: 22, TimeoutMs: 60000, MaxChars: ParseMaxChars(m["maxChars"]),
	}
	_, c.HasHost = m["host"]
	_, c.HasSuPassword = m["suPassword"]
	_, c.HasSudoPassword = m["sudoPassword"]
	_, c.DisableSudo = m["disableSudo"]

	if p := m["port"]; p != nil {
		n, err := strconv.Atoi(*p)
		if err != nil {
			return c, fmt.Errorf("Invalid --port")
		}
		c.Port = n
	}
	if t := m["timeout"]; t != nil {
		if n, err := strconv.Atoi(*t); err == nil {
			c.TimeoutMs = n
		}
	}

	var errs []string
	if c.Host == "" {
		errs = append(errs, "Missing required --host")
	}
	if c.User == "" {
		errs = append(errs, "Missing required --user")
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("Configuration error:\n%s", strings.Join(errs, "\n"))
	}
	return c, nil
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/config/ -run 'Argv|MaxChars|CLIConfig'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/argv.go internal/config/argv_test.go
git commit -m "feat(config): CLI argv parsing with TS parity"
```

---

### Task 3: Command sanitization + description escaping

**Files:**
- Create: `internal/config/sanitize.go`, `internal/config/sanitize_test.go`

**Interfaces:**
- Produces:
  - `func SanitizeCommand(cmd string, maxChars int) (string, error)` — trim, reject empty, reject `\n\r\x00`, enforce maxChars (`-1` = unlimited).
  - `func EscapeShellSingleQuote(s string) string` — `'` → `'\''`.
  - `func AppendDescription(cmd, desc string) (string, error)` — reject newline/NUL in desc, cap 500 chars, append ` # <desc with # escaped>`.

- [ ] **Step 1: Write failing tests**

`internal/config/sanitize_test.go`:
```go
package config

import "strings"
import "testing"

func TestSanitizeCommand(t *testing.T) {
	if _, err := SanitizeCommand("   ", 1000); err == nil {
		t.Fatal("empty should error")
	}
	if _, err := SanitizeCommand("ls\nrm -rf /", 1000); err == nil {
		t.Fatal("newline should error")
	}
	got, err := SanitizeCommand("  ls -la  ", 1000)
	if err != nil || got != "ls -la" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := SanitizeCommand("abcdef", 3); err == nil {
		t.Fatal("too long should error")
	}
	if _, err := SanitizeCommand(strings.Repeat("x", 5000), -1); err != nil {
		t.Fatal("unlimited should pass")
	}
}

func TestEscapeShellSingleQuote(t *testing.T) {
	if EscapeShellSingleQuote("a'b") != `a'\''b` {
		t.Fatalf("got %q", EscapeShellSingleQuote("a'b"))
	}
}

func TestAppendDescription(t *testing.T) {
	got, err := AppendDescription("ls", "list # files")
	if err != nil {
		t.Fatal(err)
	}
	if got != `ls # list \# files` {
		t.Fatalf("got %q", got)
	}
	if _, err := AppendDescription("ls", "bad\ndesc"); err == nil {
		t.Fatal("newline in desc should error")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/config/ -run 'Sanitize|Escape|AppendDescription'`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/config/sanitize.go`:
```go
package config

import (
	"fmt"
	"strings"
)

func hasControl(s string) bool {
	return strings.ContainsAny(s, "\n\r\x00")
}

func SanitizeCommand(cmd string, maxChars int) (string, error) {
	t := strings.TrimSpace(cmd)
	if t == "" {
		return "", fmt.Errorf("Command cannot be empty")
	}
	if hasControl(t) {
		return "", fmt.Errorf("Command must not contain newline or NUL")
	}
	if maxChars >= 0 && len(t) > maxChars {
		return "", fmt.Errorf("Command is too long (max %d characters)", maxChars)
	}
	return t, nil
}

func EscapeShellSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

func AppendDescription(cmd, desc string) (string, error) {
	if desc == "" {
		return cmd, nil
	}
	if hasControl(desc) {
		return "", fmt.Errorf("description must not contain newline or NUL")
	}
	if len(desc) > 500 {
		return "", fmt.Errorf("description too long (max 500)")
	}
	return cmd + " # " + strings.ReplaceAll(desc, "#", `\#`), nil
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/config/ -run 'Sanitize|Escape|AppendDescription'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/sanitize.go internal/config/sanitize_test.go
git commit -m "feat(config): command sanitization and description escaping"
```

---

### Task 4: Crypto — KDF, AES-GCM with AAD, verifier

**Files:**
- Create: `internal/config/crypto.go`, `internal/config/crypto_test.go`

**Interfaces:**
- Produces:
  - `type KDF struct { Alg string; V int; Salt string; Time, MemoryKiB, Parallelism, KeyLen uint32; Verifier string }` (JSON tags: alg,v,salt,time,memoryKiB,parallelism,keyLen,verifier)
  - `func NewKDF(masterPw string) (KDF, []byte, error)` — fresh 16-byte salt, params time=3 mem=65536 par=4 keyLen=32, computes verifier; returns master key.
  - `func (k KDF) DeriveKey(masterPw string) ([]byte, error)` — re-derive; error if unknown alg/v.
  - `func (k KDF) Verify(masterKey []byte) bool` — decrypt verifier, constant-time check.
  - `func subKey(masterKey []byte, label string) []byte` — HKDF-SHA256, 32 bytes.
  - `func Encrypt(masterKey []byte, label, aad, plaintext string) (string, error)` — per-field subkey, fresh 12-byte nonce, AAD, returns base64(nonce||ct).
  - `func Decrypt(masterKey []byte, label, aad, blob string) (string, error)`.

- [ ] **Step 1: Write failing tests**

`internal/config/crypto_test.go`:
```go
package config

import (
	"strings"
	"testing"
)

func TestKDFVerify(t *testing.T) {
	k, mk, err := NewKDF("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !k.Verify(mk) {
		t.Fatal("verify own key")
	}
	wrong, _ := k.DeriveKey("nope")
	if k.Verify(wrong) {
		t.Fatal("wrong password must not verify")
	}
}

func TestEncryptRoundtrip(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	aad := "1|prod|encPassword|host|22|root|password"
	blob, err := Encrypt(mk, "prod/encPassword", aad, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt(mk, "prod/encPassword", aad, blob)
	if err != nil || got != "s3cret" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestAADMismatchFails(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	blob, _ := Encrypt(mk, "prod/encPassword", "aad-A", "s3cret")
	if _, err := Decrypt(mk, "prod/encPassword", "aad-B", blob); err == nil {
		t.Fatal("changed AAD must fail decryption")
	}
}

func TestNonceUnique(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	a, _ := Encrypt(mk, "l", "aad", "same")
	b, _ := Encrypt(mk, "l", "aad", "same")
	if a == b {
		t.Fatal("same plaintext must yield different ciphertext (fresh nonce)")
	}
	if strings.HasPrefix(a, b[:8]) {
		t.Fatal("nonce reused")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/config/ -run 'KDF|Encrypt|AAD|Nonce'`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/config/crypto.go`:
```go
package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
	"crypto/sha256"
)

type KDF struct {
	Alg         string `json:"alg"`
	V           int    `json:"v"`
	Salt        string `json:"salt"`
	Time        uint32 `json:"time"`
	MemoryKiB   uint32 `json:"memoryKiB"`
	Parallelism uint32 `json:"parallelism"`
	KeyLen      uint32 `json:"keyLen"`
	Verifier    string `json:"verifier"`
}

const verifierConst = "ssh-mcp-verifier-v1"

func NewKDF(masterPw string) (KDF, []byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return KDF{}, nil, err
	}
	k := KDF{
		Alg: "argon2id", V: 1, Salt: base64.StdEncoding.EncodeToString(salt),
		Time: 3, MemoryKiB: 65536, Parallelism: 4, KeyLen: 32,
	}
	mk := argon2.IDKey([]byte(masterPw), salt, k.Time, k.MemoryKiB, uint8(k.Parallelism), k.KeyLen)
	blob, err := Encrypt(mk, "verifier", "verifier", verifierConst)
	if err != nil {
		return KDF{}, nil, err
	}
	k.Verifier = blob
	return k, mk, nil
}

func (k KDF) DeriveKey(masterPw string) ([]byte, error) {
	if k.Alg != "argon2id" || k.V != 1 {
		return nil, fmt.Errorf("unsupported kdf %q v%d", k.Alg, k.V)
	}
	salt, err := base64.StdEncoding.DecodeString(k.Salt)
	if err != nil {
		return nil, err
	}
	return argon2.IDKey([]byte(masterPw), salt, k.Time, k.MemoryKiB, uint8(k.Parallelism), k.KeyLen), nil
}

func (k KDF) Verify(masterKey []byte) bool {
	got, err := Decrypt(masterKey, "verifier", "verifier", k.Verifier)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(verifierConst)) == 1
}

func subKey(masterKey []byte, label string) []byte {
	r := hkdf.New(sha256.New, masterKey, nil, []byte(label))
	out := make([]byte, 32)
	io.ReadFull(r, out)
	return out
}

func gcm(key []byte) (cipher.AEAD, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

func Encrypt(masterKey []byte, label, aad, plaintext string) (string, error) {
	aead, err := gcm(subKey(masterKey, label))
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := aead.Seal(nil, nonce, []byte(plaintext), []byte(aad))
	return base64.StdEncoding.EncodeToString(append(nonce, ct...)), nil
}

func Decrypt(masterKey []byte, label, aad, blob string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return "", err
	}
	aead, err := gcm(subKey(masterKey, label))
	if err != nil {
		return "", err
	}
	ns := aead.NonceSize()
	if len(raw) < ns {
		return "", fmt.Errorf("ciphertext too short")
	}
	pt, err := aead.Open(nil, raw[:ns], raw[ns:], []byte(aad))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/config/ -run 'KDF|Encrypt|AAD|Nonce'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/crypto.go internal/config/crypto_test.go
git commit -m "feat(config): argon2id KDF and AES-GCM field encryption with AAD"
```

---

### Task 5: Store — load/save with MAC, revision, atomic write

**Files:**
- Create: `internal/config/store.go`, `internal/config/store_test.go`

**Interfaces:**
- Consumes: `KDF`, `Encrypt`, `Decrypt`, `subKey` (Task 4).
- Produces:
  - `type Server struct { Name, Host string; Port int; User, Auth, KeyPath, HostKey string; EncPassword, EncSuPassword, EncSudoPassword, EncKeyPassphrase string }`
  - `type File struct { Version int; Revision int; KDF *KDF; MAC string; Servers []Server }` (JSON tags: version,revision,kdf,mac,servers)
  - `func aadFor(f *File, s Server, field string) string` — `version|name|field|host|port|user|auth`.
  - `func computeMAC(masterKey []byte, f File) string` — HMAC-SHA256 over canonical JSON of f with MAC="".
  - `func Load(path string) (*File, error)` — parse only (no decrypt); if MAC present, caller verifies after key derived.
  - `func (f *File) VerifyMAC(masterKey []byte) error`
  - `func Save(path string, f *File, masterKey []byte) error` — bump revision, recompute MAC (if masterKey!=nil), atomic write 0600, flock.
  - `func (f *File) FindServer(name string) (Server, bool)`

- [ ] **Step 1: Write failing tests**

`internal/config/store_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	k, mk, _ := NewKDF("pw")
	f := &File{Version: 1, KDF: &k, Servers: []Server{{Name: "p", Host: "h", Port: 22, User: "root", Auth: "password"}}}
	enc, _ := Encrypt(mk, "p/encPassword", aadFor(f, f.Servers[0], "encPassword"), "topsecret")
	f.Servers[0].EncPassword = enc
	if err := Save(path, f, mk); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.VerifyMAC(mk); err != nil {
		t.Fatalf("mac: %v", err)
	}
	if loaded.Revision != 1 {
		t.Fatalf("revision = %d", loaded.Revision)
	}
	s, _ := loaded.FindServer("p")
	got, err := Decrypt(mk, "p/encPassword", aadFor(loaded, s, "encPassword"), s.EncPassword)
	if err != nil || got != "topsecret" {
		t.Fatalf("decrypt got %q err %v", got, err)
	}
}

func TestMACTamperDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	k, mk, _ := NewKDF("pw")
	f := &File{Version: 1, KDF: &k, Servers: []Server{{Name: "p", Host: "h", Port: 22, User: "root", Auth: "password"}}}
	Save(path, f, mk)
	raw, _ := os.ReadFile(path)
	tampered := []byte(string(raw))
	// flip host h -> x by editing bytes
	for i := range tampered {
		if tampered[i] == 'h' {
			tampered[i] = 'x'
			break
		}
	}
	os.WriteFile(path, tampered, 0o600)
	loaded, _ := Load(path)
	if err := loaded.VerifyMAC(mk); err == nil {
		t.Fatal("tamper must be detected by MAC")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/config/ -run 'SaveLoad|MACTamper'`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/config/store.go`:
```go
package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Server struct {
	Name             string `json:"name"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	User             string `json:"user"`
	Auth             string `json:"auth"`
	KeyPath          string `json:"keyPath,omitempty"`
	HostKey          string `json:"hostKey,omitempty"`
	EncPassword      string `json:"encPassword,omitempty"`
	EncSuPassword    string `json:"encSuPassword,omitempty"`
	EncSudoPassword  string `json:"encSudoPassword,omitempty"`
	EncKeyPassphrase string `json:"encKeyPassphrase,omitempty"`
}

type File struct {
	Version  int      `json:"version"`
	Revision int      `json:"revision"`
	KDF      *KDF     `json:"kdf,omitempty"`
	MAC      string   `json:"mac,omitempty"`
	Servers  []Server `json:"servers"`
}

func aadFor(f *File, s Server, field string) string {
	return strconv.Itoa(f.Version) + "|" + s.Name + "|" + field + "|" + s.Host + "|" +
		strconv.Itoa(s.Port) + "|" + s.User + "|" + s.Auth
}

func computeMAC(masterKey []byte, f File) string {
	f.MAC = ""
	canon, _ := json.Marshal(f)
	mac := hmac.New(sha256.New, subKey(masterKey, "file-mac"))
	mac.Write(canon)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (f *File) VerifyMAC(masterKey []byte) error {
	if f.MAC == "" {
		return nil // key/agent-only vault; perms are the protection
	}
	want := computeMAC(masterKey, *f)
	if !hmac.Equal([]byte(want), []byte(f.MAC)) {
		return fmt.Errorf("config MAC mismatch — file tampered or wrong master password")
	}
	return nil
}

func (f *File) FindServer(name string) (Server, bool) {
	for _, s := range f.Servers {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func Save(path string, f *File, masterKey []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f.Revision++
	if masterKey != nil {
		f.MAC = computeMAC(masterKey, *f)
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".servers-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
```

Note: `flock` (writer serialization) is added in Task 16 where the web store performs read-modify-write; MCP mode is read-only so the atomic rename suffices here.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/config/ -run 'SaveLoad|MACTamper'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/store.go internal/config/store_test.go
git commit -m "feat(config): encrypted store with file MAC and atomic writes"
```

---

### Task 6: Master password loader + redactor

**Files:**
- Create: `internal/config/masterpw.go`, `internal/config/redact.go`, `internal/config/masterpw_test.go`

**Interfaces:**
- Produces:
  - `func ReadMasterPasswordFile(path string) (string, error)` — stat, reject if mode has group/other bits, read, trim trailing newline.
  - `func ResolveMasterPassword(env func(string) string) (string, bool, error)` — reads `SSH_MCP_MASTER_PASSWORD_FILE`; returns ("",false,nil) if unset.
  - `type Redactor struct{ secrets []string }`; `func NewRedactor(secrets ...string) *Redactor`; `func (r *Redactor) Redact(s string) string` — replace each non-empty secret with `***`.

- [ ] **Step 1: Write failing tests**

`internal/config/masterpw_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMasterPasswordFileRejectsLoosePerms(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pw")
	os.WriteFile(p, []byte("secret\n"), 0o644)
	if _, err := ReadMasterPasswordFile(p); err == nil {
		t.Fatal("0644 must be rejected")
	}
	os.Chmod(p, 0o600)
	got, err := ReadMasterPasswordFile(p)
	if err != nil || got != "secret" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestRedactor(t *testing.T) {
	r := NewRedactor("hunter2", "", "root#pw")
	out := r.Redact("login hunter2 then root#pw done")
	if out != "login *** then *** done" {
		t.Fatalf("got %q", out)
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/config/ -run 'MasterPassword|Redactor'`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/config/masterpw.go`:
```go
package config

import (
	"fmt"
	"os"
	"strings"
)

func ReadMasterPasswordFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("master password file %s must be mode 0600 (no group/other access)", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(raw), "\r\n"), nil
}

func ResolveMasterPassword(env func(string) string) (string, bool, error) {
	p := env("SSH_MCP_MASTER_PASSWORD_FILE")
	if p == "" {
		return "", false, nil
	}
	pw, err := ReadMasterPasswordFile(p)
	if err != nil {
		return "", false, err
	}
	return pw, true, nil
}
```

`internal/config/redact.go`:
```go
package config

import "strings"

type Redactor struct{ secrets []string }

func NewRedactor(secrets ...string) *Redactor {
	var s []string
	for _, x := range secrets {
		if x != "" {
			s = append(s, x)
		}
	}
	return &Redactor{secrets: s}
}

func (r *Redactor) Redact(s string) string {
	for _, sec := range r.secrets {
		s = strings.ReplaceAll(s, sec, "***")
	}
	return s
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/config/ -run 'MasterPassword|Redactor'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/masterpw.go internal/config/redact.go internal/config/masterpw_test.go
git commit -m "feat(config): master-password file loader and secret redactor"
```

---

## Phase B — SSH connection manager

### Task 7: sudo/su command wrapping (pure functions)

**Files:**
- Create: `internal/sshx/wrap.go`, `internal/sshx/wrap_test.go`

**Interfaces:**
- Consumes: `config.EscapeShellSingleQuote`.
- Produces:
  - `func WrapSudoNoPassword(cmd string) string` → `sudo -n sh -c '<cmd>'`.
  - `func WrapSudoWithPassword(cmd string) string` → `sudo -k -S -p '' sh -c 'exec <cmd> </dev/null'` (password goes on stdin, NOT here).
  - `func FrameSuCommand(cmd, nonce string) string` → `<cmd>; echo <nonce>:$?`.

- [ ] **Step 1: Write failing tests**

`internal/sshx/wrap_test.go`:
```go
package sshx

import (
	"strings"
	"testing"
)

func TestWrapSudoNoPassword(t *testing.T) {
	got := WrapSudoNoPassword("ls '/tmp'")
	if got != `sudo -n sh -c 'ls '\''/tmp'\'''` {
		t.Fatalf("got %q", got)
	}
}

func TestWrapSudoWithPasswordNoSecretInline(t *testing.T) {
	got := WrapSudoWithPassword("whoami")
	if strings.Contains(got, "printf") {
		t.Fatal("password must not be piped inline anymore")
	}
	if !strings.Contains(got, "-S") || !strings.Contains(got, "</dev/null") {
		t.Fatalf("got %q", got)
	}
}

func TestFrameSuCommand(t *testing.T) {
	if FrameSuCommand("id", "NONCE") != "id; echo NONCE:$?" {
		t.Fatalf("got %q", FrameSuCommand("id", "NONCE"))
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/sshx/ -run 'Wrap|Frame'`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/sshx/wrap.go`:
```go
package sshx

import "github.com/lang315/ssh-mcp/internal/config"

func WrapSudoNoPassword(cmd string) string {
	return "sudo -n sh -c '" + config.EscapeShellSingleQuote(cmd) + "'"
}

func WrapSudoWithPassword(cmd string) string {
	inner := "exec " + cmd + " </dev/null"
	return "sudo -k -S -p '' sh -c '" + config.EscapeShellSingleQuote(inner) + "'"
}

func FrameSuCommand(cmd, nonce string) string {
	return cmd + "; echo " + nonce + ":$?"
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/sshx/ -run 'Wrap|Frame'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sshx/wrap.go internal/sshx/wrap_test.go
git commit -m "feat(sshx): sudo/su command wrapping with stdin password"
```

---

### Task 8: Host key verification callback

**Files:**
- Create: `internal/sshx/hostkey.go`, `internal/sshx/hostkey_test.go`

**Interfaces:**
- Produces:
  - `func Fingerprint(key ssh.PublicKey) string` — `ssh.FingerprintSHA256`.
  - `func HostKeyCallback(pinned string, insecure bool, onLearn func(fp string)) ssh.HostKeyCallback` — if `insecure`, accept any (log). Else: if `pinned` set, require exact match; if empty, TOFU — accept, call `onLearn(fp)` so caller can persist.

- [ ] **Step 1: Write failing tests**

`internal/sshx/hostkey_test.go`:
```go
package sshx

import (
	"net"
	"testing"

	"crypto/rand"
	"crypto/ed25519"
	"golang.org/x/crypto/ssh"
)

func testKey(t *testing.T) ssh.PublicKey {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer.PublicKey()
}

func TestTOFULearns(t *testing.T) {
	pk := testKey(t)
	var learned string
	cb := HostKeyCallback("", false, func(fp string) { learned = fp })
	if err := cb("h:22", &net.TCPAddr{}, pk); err != nil {
		t.Fatal(err)
	}
	if learned != Fingerprint(pk) {
		t.Fatalf("learned %q", learned)
	}
}

func TestPinnedMismatchRejected(t *testing.T) {
	pk := testKey(t)
	cb := HostKeyCallback("sha256:WRONG", false, nil)
	if err := cb("h:22", &net.TCPAddr{}, pk); err == nil {
		t.Fatal("mismatched pin must be rejected")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/sshx/ -run 'TOFU|Pinned|Fingerprint'`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/sshx/hostkey.go`:
```go
package sshx

import (
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

func Fingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

func HostKeyCallback(pinned string, insecure bool, onLearn func(fp string)) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if insecure {
			return nil
		}
		fp := Fingerprint(key)
		if pinned == "" {
			if onLearn != nil {
				onLearn(fp)
			}
			return nil
		}
		if fp != pinned {
			return fmt.Errorf("host key mismatch for %s: got %s, pinned %s", hostname, fp, pinned)
		}
		return nil
	}
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/sshx/ -run 'TOFU|Pinned|Fingerprint'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sshx/hostkey.go internal/sshx/hostkey_test.go
git commit -m "feat(sshx): host key verification with pinning and TOFU"
```

---

### Task 9: Connection manager + exec (integration)

**Files:**
- Create: `internal/sshx/manager.go`, `internal/sshx/manager_test.go`

**Interfaces:**
- Consumes: `HostKeyCallback`, `WrapSudo*`, `FrameSuCommand`.
- Produces:
  - `type DialConfig struct { Host string; Port int; User, Password, PrivateKey, Passphrase, SuPassword, SudoPassword, HostKey string; Auth string; Insecure bool; TimeoutMs int; OnLearnHostKey func(fp string) }`
  - `type Manager struct { ... }`; `func NewManager(cfg DialConfig) *Manager`
  - `func (m *Manager) Exec(ctx context.Context, cmd string) (string, error)` — serialized per manager; ensures connected; if SuPassword set, runs via elevated shell, else `session.Run`.
  - `func (m *Manager) ExecSudo(ctx context.Context, cmd string) (string, error)` — stdin password path.
  - `func (m *Manager) Close()`

**Note:** This task requires Docker for `testcontainers-go`. If Docker is unavailable, mark the integration test `t.Skip` behind `testing.Short()` and rely on the pure-function tests; still implement fully.

- [ ] **Step 1: Write failing integration test**

`internal/sshx/manager_test.go`:
```go
package sshx

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startSSH(t *testing.T) (host string, port int, cleanup func()) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "lscr.io/linuxserver/openssh-server:latest",
		ExposedPorts: []string{"2222/tcp"},
		Env: map[string]string{
			"PASSWORD_ACCESS": "true", "USER_NAME": "test", "USER_PASSWORD": "testpass",
		},
		WaitingFor: wait.ForListeningPort("2222/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	h, _ := c.Host(ctx)
	p, _ := c.MappedPort(ctx, "2222")
	return h, p.Int(), func() { c.Terminate(ctx) }
}

func TestExecEcho(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	defer cleanup()
	m := NewManager(DialConfig{
		Host: host, Port: port, User: "test", Password: "testpass",
		Auth: "password", Insecure: true, TimeoutMs: 30000,
	})
	defer m.Close()
	out, err := m.Exec(context.Background(), "echo hello-ssh")
	if err != nil {
		t.Fatal(err)
	}
	if got := out; got == "" || !contains(got, "hello-ssh") {
		t.Fatalf("out = %q", got)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/sshx/ -run TestExecEcho`
Expected: FAIL (undefined `NewManager`/`DialConfig`) — or SKIP if Docker absent (still a valid gate; the compile must succeed once implemented).

- [ ] **Step 3: Implement**

`internal/sshx/manager.go`:
```go
package sshx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"os"
)

type DialConfig struct {
	Host, User, Password, PrivateKey, Passphrase string
	SuPassword, SudoPassword, HostKey, Auth      string
	Port, TimeoutMs                              int
	Insecure                                     bool
	OnLearnHostKey                               func(fp string)
}

type Manager struct {
	cfg    DialConfig
	mu     sync.Mutex
	client *ssh.Client
}

func NewManager(cfg DialConfig) *Manager { return &Manager{cfg: cfg} }

func (m *Manager) authMethods() ([]ssh.AuthMethod, error) {
	switch m.cfg.Auth {
	case "agent":
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, fmt.Errorf("SSH_AUTH_SOCK not set for agent auth")
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(conn).Signers)}, nil
	case "key":
		var signer ssh.Signer
		var err error
		if m.cfg.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(m.cfg.PrivateKey), []byte(m.cfg.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(m.cfg.PrivateKey))
		}
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	default:
		return []ssh.AuthMethod{ssh.Password(m.cfg.Password)}, nil
	}
}

func (m *Manager) ensure() error {
	if m.client != nil {
		return nil
	}
	auth, err := m.authMethods()
	if err != nil {
		return err
	}
	cc := &ssh.ClientConfig{
		User:            m.cfg.User,
		Auth:            auth,
		HostKeyCallback: HostKeyCallback(m.cfg.HostKey, m.cfg.Insecure, m.cfg.OnLearnHostKey),
		Timeout:         30 * time.Second,
	}
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	client, err := ssh.Dial("tcp", addr, cc)
	if err != nil {
		return err
	}
	m.client = client
	go func() { client.Wait(); m.mu.Lock(); m.client = nil; m.mu.Unlock() }()
	return nil
}

func (m *Manager) runOnce(ctx context.Context, cmd string, stdin string) (string, error) {
	sess, err := m.client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var out bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &out
	if stdin != "" {
		sess.Stdin = strings.NewReader(stdin)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	timeout := time.Duration(m.cfg.TimeoutMs) * time.Millisecond
	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(timeout):
		sess.Close()
		return out.String(), fmt.Errorf("command timed out after %dms", m.cfg.TimeoutMs)
	case <-ctx.Done():
		sess.Close()
		return out.String(), ctx.Err()
	}
}

func (m *Manager) Exec(ctx context.Context, cmd string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return "", err
	}
	if m.cfg.SuPassword != "" {
		return m.execElevated(ctx, cmd)
	}
	out, err := m.runOnce(ctx, cmd, "")
	return out, wrapExit(err)
}

func (m *Manager) ExecSudo(ctx context.Context, cmd string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return "", err
	}
	if m.cfg.SudoPassword == "" {
		out, err := m.runOnce(ctx, WrapSudoNoPassword(cmd), "")
		return out, wrapExit(err)
	}
	out, err := m.runOnce(ctx, WrapSudoWithPassword(cmd), m.cfg.SudoPassword+"\n")
	return out, wrapExit(err)
}

func wrapExit(err error) error {
	if err == nil {
		return nil
	}
	var ee *ssh.ExitError
	if bytes.Contains([]byte(err.Error()), []byte("exited")) {
		return err
	}
	_ = ee
	return err
}

func nonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "SSHMCP" + hex.EncodeToString(b)
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
}
```

`execElevated` is implemented in Task 10 (kept separate so the su-shell logic gets its own review gate). For this task, add a temporary stub so the package compiles:

```go
func (m *Manager) execElevated(ctx context.Context, cmd string) (string, error) {
	return "", fmt.Errorf("su elevation not yet implemented")
}
```

- [ ] **Step 4: Run — expect PASS (or SKIP without Docker)**

Run: `go test ./internal/sshx/ -run TestExecEcho`
Expected: PASS with Docker; SKIP otherwise. Also `go build ./...` must succeed.

- [ ] **Step 5: Commit**

```bash
git add internal/sshx/manager.go internal/sshx/manager_test.go
git commit -m "feat(sshx): persistent connection manager with exec and sudo"
```

---

### Task 10: su elevation via sentinel-framed PTY shell (integration)

**Files:**
- Modify: `internal/sshx/manager.go` (replace `execElevated` stub, add shell setup)
- Test: `internal/sshx/elevation_test.go`

**Interfaces:**
- Produces: real `func (m *Manager) execElevated(ctx, cmd) (string, error)` and internal `ensureElevated()`.

**Behavior:** open one PTY shell; `export LANG=C LC_ALL=C`; send `su -`; wait for a password prompt (read until case-insensitive `assword`); write suPassword; set `PS1` to a random sentinel and wait for it; then per command send `FrameSuCommand(cmd, nonce)` and read until `<nonce>:<code>`; parse exit code. On wrong password (read `su: ` failure or timeout) return an error — **never** fall back to unprivileged.

- [ ] **Step 1: Write failing test**

`internal/sshx/elevation_test.go`:
```go
package sshx

import (
	"context"
	"testing"
)

// Uses a container whose root password is known. linuxserver image's `test`
// user is not root; this test uses `su` to root with a set root password.
func TestSuElevation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSHWithRoot(t) // helper below
	defer cleanup()
	m := NewManager(DialConfig{
		Host: host, Port: port, User: "test", Password: "testpass",
		SuPassword: "rootpass", Auth: "password", Insecure: true, TimeoutMs: 30000,
	})
	defer m.Close()
	out, err := m.Exec(context.Background(), "id -u")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(out, "0") {
		t.Fatalf("expected uid 0, got %q", out)
	}
}

func TestSuWrongPasswordFailsClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSHWithRoot(t)
	defer cleanup()
	m := NewManager(DialConfig{
		Host: host, Port: port, User: "test", Password: "testpass",
		SuPassword: "WRONG", Auth: "password", Insecure: true, TimeoutMs: 15000,
	})
	defer m.Close()
	if _, err := m.Exec(context.Background(), "id -u"); err == nil {
		t.Fatal("wrong su password must fail, not fall back to unprivileged")
	}
}
```

Add `startSSHWithRoot` to `manager_test.go` (container with `sudo passwd root` set via a custom `Cmd`/init, or an image that permits `su`). Document in the test file that it requires an image where `su - root` with a known password works; skip if the image can't be prepared.

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/sshx/ -run TestSu`
Expected: FAIL (stub returns error) or SKIP without Docker.

- [ ] **Step 3: Implement**

Replace the stub in `internal/sshx/manager.go`:
```go
type suShell struct {
	sess   *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
	buf    *bufio.Reader
}

func (m *Manager) ensureElevated() (*suShell, error) {
	if m.su != nil {
		return m.su, nil
	}
	sess, err := m.client.NewSession()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 0}
	if err := sess.RequestPty("xterm", 24, 80, modes); err != nil {
		sess.Close()
		return nil, err
	}
	stdin, _ := sess.StdinPipe()
	stdout, _ := sess.StdoutPipe()
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, err
	}
	sh := &suShell{sess: sess, stdin: stdin, stdout: stdout, buf: bufio.NewReader(stdout)}
	fmt.Fprint(stdin, "export LANG=C LC_ALL=C\n")
	fmt.Fprint(stdin, "su -\n")
	if err := readUntilAny(sh.buf, 10*time.Second, []string{"assword"}); err != nil {
		sess.Close()
		return nil, fmt.Errorf("su password prompt not seen: %w", err)
	}
	fmt.Fprint(stdin, m.cfg.SuPassword+"\n")
	sentinel := nonce()
	fmt.Fprintf(stdin, "PS1='%s'\n", sentinel)
	if err := readUntilAny(sh.buf, 10*time.Second, []string{sentinel}); err != nil {
		sess.Close()
		return nil, fmt.Errorf("su elevation failed (wrong password?): %w", err)
	}
	m.su = sh
	return sh, nil
}

func (m *Manager) execElevated(ctx context.Context, cmd string) (string, error) {
	sh, err := m.ensureElevated()
	if err != nil {
		return "", err
	}
	n := nonce()
	marker := n + ":"
	fmt.Fprintf(sh.stdin, "%s\n", FrameSuCommand(cmd, n))
	out, err := readCommandOutput(sh.buf, time.Duration(m.cfg.TimeoutMs)*time.Millisecond, marker)
	if err != nil {
		// poisoned shell: tear down so next call re-elevates
		sh.sess.Close()
		m.su = nil
		return "", err
	}
	return out, nil
}
```

Add helpers (same file):
```go
func readUntilAny(r *bufio.Reader, timeout time.Duration, needles []string) error {
	deadline := time.Now().Add(timeout)
	var acc strings.Builder
	for time.Now().Before(deadline) {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		acc.WriteByte(b)
		s := acc.String()
		for _, n := range needles {
			if strings.Contains(s, n) {
				return nil
			}
		}
		if strings.Contains(strings.ToLower(s), "su: ") && strings.Contains(strings.ToLower(s), "fail") {
			return fmt.Errorf("su reported failure")
		}
	}
	return fmt.Errorf("timeout waiting for prompt")
}

func readCommandOutput(r *bufio.Reader, timeout time.Duration, marker string) (string, error) {
	deadline := time.Now().Add(timeout)
	var acc strings.Builder
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		acc.WriteString(line)
		if i := strings.Index(acc.String(), marker); i >= 0 {
			full := acc.String()
			// output is everything before the marker line; strip the echoed command's first line
			body := full[:i]
			if nl := strings.IndexByte(body, '\n'); nl >= 0 {
				body = body[nl+1:]
			}
			return body, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("command timed out")
}
```

Add fields to `Manager` and imports (`bufio`, `io`):
```go
type Manager struct {
	cfg    DialConfig
	mu     sync.Mutex
	client *ssh.Client
	su     *suShell
}
```
Update `Close()` to also close `m.su.sess` if non-nil.

- [ ] **Step 4: Run — expect PASS (or SKIP)**

Run: `go test ./internal/sshx/ -run TestSu`
Expected: PASS with a suitable container; SKIP without Docker. `go build ./...` succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/sshx/manager.go internal/sshx/elevation_test.go
git commit -m "feat(sshx): sentinel-framed su elevation, fail-closed"
```

---

### Task 11: Registry (name → Manager, config-hash keyed)

**Files:**
- Create: `internal/sshx/registry.go`, `internal/sshx/registry_test.go`

**Interfaces:**
- Produces:
  - `func ConfigHash(c DialConfig) string` — sha256 of connection-identifying fields (host, port, user, auth, key, secrets) hex.
  - `type Registry struct{ ... }`; `func NewRegistry() *Registry`
  - `func (r *Registry) Get(name string, cfg DialConfig) *Manager` — returns existing manager if same name+hash; else closes stale one and creates fresh.
  - `func (r *Registry) CloseAll()`

- [ ] **Step 1: Write failing tests**

`internal/sshx/registry_test.go`:
```go
package sshx

import "testing"

func TestRegistryReusesSameConfig(t *testing.T) {
	r := NewRegistry()
	cfg := DialConfig{Host: "h", Port: 22, User: "u", Auth: "password", Password: "p"}
	m1 := r.Get("a", cfg)
	m2 := r.Get("a", cfg)
	if m1 != m2 {
		t.Fatal("same name+config should reuse manager")
	}
}

func TestRegistryReplacesOnConfigChange(t *testing.T) {
	r := NewRegistry()
	m1 := r.Get("a", DialConfig{Host: "h", Port: 22, User: "u", Auth: "password", Password: "p"})
	m2 := r.Get("a", DialConfig{Host: "h2", Port: 22, User: "u", Auth: "password", Password: "p"})
	if m1 == m2 {
		t.Fatal("config change should create a new manager")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/sshx/ -run Registry`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/sshx/registry.go`:
```go
package sshx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

func ConfigHash(c DialConfig) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s|%s|%s|%s|%s|%v", c.Host, c.Port, c.User, c.Auth,
		c.Password, c.PrivateKey, c.SuPassword, c.SudoPassword, c.Insecure)
	return hex.EncodeToString(h.Sum(nil))
}

type entry struct {
	hash string
	mgr  *Manager
}

type Registry struct {
	mu sync.Mutex
	m  map[string]entry
}

func NewRegistry() *Registry { return &Registry{m: map[string]entry{}} }

func (r *Registry) Get(name string, cfg DialConfig) *Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	hash := ConfigHash(cfg)
	if e, ok := r.m[name]; ok {
		if e.hash == hash {
			return e.mgr
		}
		e.mgr.Close()
	}
	mgr := NewManager(cfg)
	r.m[name] = entry{hash: hash, mgr: mgr}
	return mgr
}

func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.m {
		e.mgr.Close()
	}
	r.m = map[string]entry{}
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/sshx/ -run Registry`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sshx/registry.go internal/sshx/registry_test.go
git commit -m "feat(sshx): connection registry keyed by name and config hash"
```

---

## Phase C — MCP server + CLI wiring

### Task 12: Server resolver (CLI default vs named-from-store)

**Files:**
- Create: `internal/mcpserver/resolve.go`, `internal/mcpserver/resolve_test.go`

**Interfaces:**
- Consumes: `config.File`, `config.Server`, `config.Decrypt`, `config.CLIConfig`, `sshx.DialConfig`.
- Produces:
  - `type Deps struct { CLI *config.CLIConfig; File *config.File; MasterKey []byte; Insecure bool }`
  - `func (d *Deps) Resolve(name string) (sshx.DialConfig, error)` — if `name==""` and CLI host set, return CLI-derived config. Else look up named server; decrypt its secrets with MasterKey (error "vault locked" if encrypted fields present but MasterKey nil).
  - `func (d *Deps) ServerNames() []string`
  - `func (d *Deps) IsLocked(name string) bool`

- [ ] **Step 1: Write failing tests**

`internal/mcpserver/resolve_test.go`:
```go
package mcpserver

import (
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
)

func TestResolveCLIDefault(t *testing.T) {
	d := &Deps{CLI: &config.CLIConfig{Host: "h", Port: 22, User: "u", Password: "p", HasHost: true, TimeoutMs: 60000}}
	dc, err := d.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if dc.Host != "h" || dc.Password != "p" {
		t.Fatalf("got %+v", dc)
	}
}

func TestResolveLockedVault(t *testing.T) {
	f := &config.File{Version: 1, Servers: []config.Server{{Name: "p", Host: "h", Port: 22, User: "u", Auth: "password", EncPassword: "xxx"}}}
	d := &Deps{File: f, MasterKey: nil}
	if _, err := d.Resolve("p"); err == nil {
		t.Fatal("encrypted server without master key must error 'vault locked'")
	}
	if !d.IsLocked("p") {
		t.Fatal("IsLocked should be true")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/mcpserver/ -run Resolve`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/mcpserver/resolve.go`:
```go
package mcpserver

import (
	"fmt"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

type Deps struct {
	CLI       *config.CLIConfig
	File      *config.File
	MasterKey []byte
	Insecure  bool
}

func (d *Deps) ServerNames() []string {
	var out []string
	if d.CLI != nil && d.CLI.HasHost {
		out = append(out, "(default)")
	}
	if d.File != nil {
		for _, s := range d.File.Servers {
			out = append(out, s.Name)
		}
	}
	return out
}

func (d *Deps) IsLocked(name string) bool {
	if d.File == nil {
		return false
	}
	s, ok := d.File.FindServer(name)
	if !ok {
		return false
	}
	enc := s.EncPassword != "" || s.EncSuPassword != "" || s.EncSudoPassword != "" || s.EncKeyPassphrase != ""
	return enc && d.MasterKey == nil
}

func (d *Deps) Resolve(name string) (sshx.DialConfig, error) {
	if name == "" || name == "(default)" {
		if d.CLI == nil || !d.CLI.HasHost {
			return sshx.DialConfig{}, fmt.Errorf("no default server; pass a 'server' name")
		}
		c := d.CLI
		auth := "password"
		if c.Password == "" && c.Key != "" {
			auth = "key"
		}
		return sshx.DialConfig{
			Host: c.Host, Port: c.Port, User: c.User, Password: c.Password,
			SuPassword: c.SuPassword, SudoPassword: c.SudoPassword,
			Auth: auth, Insecure: d.Insecure, TimeoutMs: c.TimeoutMs,
		}, nil
	}
	if d.File == nil {
		return sshx.DialConfig{}, fmt.Errorf("server %q not found", name)
	}
	s, ok := d.File.FindServer(name)
	if !ok {
		return sshx.DialConfig{}, fmt.Errorf("server %q not found", name)
	}
	dec := func(field, blob string) (string, error) {
		if blob == "" {
			return "", nil
		}
		if d.MasterKey == nil {
			return "", fmt.Errorf("vault locked; provide SSH_MCP_MASTER_PASSWORD_FILE or use key/agent auth")
		}
		return config.Decrypt(d.MasterKey, s.Name+"/"+field, aadFor(d.File, s, field), blob)
	}
	pw, err := dec("encPassword", s.EncPassword)
	if err != nil {
		return sshx.DialConfig{}, err
	}
	su, err := dec("encSuPassword", s.EncSuPassword)
	if err != nil {
		return sshx.DialConfig{}, err
	}
	sudo, err := dec("encSudoPassword", s.EncSudoPassword)
	if err != nil {
		return sshx.DialConfig{}, err
	}
	return sshx.DialConfig{
		Host: s.Host, Port: s.Port, User: s.User, Password: pw, Auth: s.Auth,
		SuPassword: su, SudoPassword: sudo, HostKey: s.HostKey, Insecure: d.Insecure,
		TimeoutMs: 60000,
	}, nil
}
```

Add `aadFor` accessor in mcpserver (re-export from config): since `aadFor` in config is unexported, add an exported `config.AADFor(f *File, s Server, field string) string` in Task 5's file and call it here. **Action:** in `internal/config/store.go`, add `func AADFor(f *File, s Server, field string) string { return aadFor(f, s, field) }` and replace the call above with `config.AADFor(...)`.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/mcpserver/ -run Resolve`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/resolve.go internal/mcpserver/resolve_test.go internal/config/store.go
git commit -m "feat(mcp): server resolver for CLI default and encrypted store"
```

---

### Task 13: MCP tools registration + handlers

**Files:**
- Create: `internal/mcpserver/tools.go`, `internal/mcpserver/server.go`, `internal/mcpserver/tools_test.go`

**Interfaces:**
- Consumes: `Deps.Resolve`, `sshx.Registry`, `config.SanitizeCommand`, `config.AppendDescription`, `config.NewRedactor`, go-sdk.
- Produces:
  - `func BuildServer(d *Deps, reg *sshx.Registry, disableSudo bool, maxChars int) *mcp.Server`
  - Tool input structs: `type ExecInput struct { Server, Command, Description string }`, same for sudo. `type ListInput struct{}`.
  - `func runExec(ctx, d, reg, maxChars, sudo bool, in ExecInput) (*mcp.CallToolResult, error)` — sanitize, append desc, resolve, get manager, exec/execSudo, redact output; errors returned as `IsError` result.

- [ ] **Step 1: Write failing test (handler logic, no live SSH)**

`internal/mcpserver/tools_test.go`:
```go
package mcpserver

import (
	"context"
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

func TestRunExecSanitizeError(t *testing.T) {
	d := &Deps{CLI: &config.CLIConfig{Host: "h", User: "u", HasHost: true, TimeoutMs: 60000, MaxChars: 1000}}
	reg := sshx.NewRegistry()
	res, err := runExec(context.Background(), d, reg, 1000, false, ExecInput{Command: "   "})
	if err != nil {
		t.Fatalf("expected IsError result not go error, got %v", err)
	}
	if !res.IsError {
		t.Fatal("empty command should produce IsError result")
	}
}

func TestRunExecLockedServer(t *testing.T) {
	f := &config.File{Version: 1, Servers: []config.Server{{Name: "p", Host: "h", Port: 22, User: "u", Auth: "password", EncPassword: "x"}}}
	d := &Deps{File: f}
	reg := sshx.NewRegistry()
	res, _ := runExec(context.Background(), d, reg, 1000, false, ExecInput{Server: "p", Command: "ls"})
	if !res.IsError {
		t.Fatal("locked server should produce IsError result")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/mcpserver/ -run RunExec`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/mcpserver/tools.go`:
```go
package mcpserver

import (
	"context"
	"fmt"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ExecInput struct {
	Server      string `json:"server" jsonschema:"connection name; empty uses the default server"`
	Command     string `json:"command" jsonschema:"shell command to execute"`
	Description string `json:"description,omitempty" jsonschema:"optional description of the command"`
}
type ListInput struct{}

func textErr(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
func textOK(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func runExec(ctx context.Context, d *Deps, reg *sshx.Registry, maxChars int, sudo bool, in ExecInput) (*mcp.CallToolResult, error) {
	cmd, err := config.SanitizeCommand(in.Command, maxChars)
	if err != nil {
		return textErr(err.Error()), nil
	}
	cmd, err = config.AppendDescription(cmd, in.Description)
	if err != nil {
		return textErr(err.Error()), nil
	}
	dc, err := d.Resolve(in.Server)
	if err != nil {
		return textErr(err.Error()), nil
	}
	mgr := reg.Get(nameOr(in.Server), dc)
	red := config.NewRedactor(dc.Password, dc.SuPassword, dc.SudoPassword, dc.Passphrase)
	var out string
	if sudo {
		out, err = mgr.ExecSudo(ctx, cmd)
	} else {
		out, err = mgr.Exec(ctx, cmd)
	}
	if err != nil {
		return textErr(red.Redact(err.Error())), nil
	}
	return textOK(red.Redact(out)), nil
}

func nameOr(s string) string {
	if s == "" {
		return "(default)"
	}
	return s
}

func BuildServer(d *Deps, reg *sshx.Registry, disableSudo bool, maxChars int) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "SSH MCP Server", Version: "2.0.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{Name: "exec", Description: "Execute a shell command on the remote SSH server and return the output."},
		func(ctx context.Context, req *mcp.CallToolRequest, in ExecInput) (*mcp.CallToolResult, any, error) {
			res, err := runExec(ctx, d, reg, maxChars, false, in)
			return res, nil, err
		})

	if !disableSudo {
		mcp.AddTool(s, &mcp.Tool{Name: "sudo-exec", Description: "Execute a shell command using sudo on the remote SSH server."},
			func(ctx context.Context, req *mcp.CallToolRequest, in ExecInput) (*mcp.CallToolResult, any, error) {
				res, err := runExec(ctx, d, reg, maxChars, true, in)
				return res, nil, err
			})
	}

	mcp.AddTool(s, &mcp.Tool{Name: "list-servers", Description: "List configured SSH connection names (no secrets)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, any, error) {
			var b string
			for _, n := range d.ServerNames() {
				lock := ""
				if d.IsLocked(n) {
					lock = " [locked]"
				}
				b += fmt.Sprintf("- %s%s\n", n, lock)
			}
			if b == "" {
				b = "(no servers configured)"
			}
			return textOK(b), nil, nil
		})

	return s
}
```

`internal/mcpserver/server.go`:
```go
package mcpserver

// server.go reserved for future wiring helpers; BuildServer lives in tools.go.
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/mcpserver/ -run RunExec`
Expected: PASS. Also `go build ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/
git commit -m "feat(mcp): register exec, sudo-exec, list-servers tools"
```

---

### Task 14: Wire MCP mode into main + master password + stdio

**Files:**
- Modify: `cmd/ssh-mcp/main.go`
- Test: `cmd/ssh-mcp/mcp_wire_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `func runMCP(args []string) error` — parse argv, build CLIConfig if `--host`, load store if present, resolve master password, build server, run over stdio, graceful shutdown on SIGINT/SIGTERM.

- [ ] **Step 1: Write failing test (config assembly, no stdio run)**

`cmd/ssh-mcp/mcp_wire_test.go`:
```go
package main

import "testing"

func TestBuildDepsFromArgs(t *testing.T) {
	d, disableSudo, maxChars, err := buildDeps([]string{"--host=h", "--user=u", "--disableSudo", "--maxChars=50"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !disableSudo {
		t.Fatal("disableSudo not parsed")
	}
	if maxChars != 50 {
		t.Fatalf("maxChars = %d", maxChars)
	}
	if d.CLI == nil || !d.CLI.HasHost {
		t.Fatal("CLI host not set")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./cmd/... -run BuildDeps`
Expected: FAIL.

- [ ] **Step 3: Implement**

Rewrite `cmd/ssh-mcp/main.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/mcpserver"
	"github.com/lang315/ssh-mcp/internal/sshx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func route(args []string) (string, []string) {
	if len(args) > 0 && args[0] == "web" {
		return "web", args[1:]
	}
	return "mcp", args
}

func storePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ssh-mcp", "servers.json")
}

func buildDeps(args []string, env func(string) string) (*mcpserver.Deps, bool, int, error) {
	m := config.ParseArgv(args)
	_, insecure := m["insecureIgnoreHostKey"]
	_, disableSudo := m["disableSudo"]
	maxChars := config.ParseMaxChars(m["maxChars"])
	d := &mcpserver.Deps{Insecure: insecure}

	if _, hasHost := m["host"]; hasHost {
		cli, err := config.BuildCLIConfig(m)
		if err != nil {
			return nil, false, 0, err
		}
		cli.MaxChars = maxChars
		d.CLI = &cli
	}

	if f, err := config.Load(storePath()); err == nil {
		d.File = f
		if pw, ok, err := config.ResolveMasterPassword(env); err != nil {
			return nil, false, 0, err
		} else if ok && f.KDF != nil {
			mk, err := f.KDF.DeriveKey(pw)
			if err != nil {
				return nil, false, 0, err
			}
			if !f.KDF.Verify(mk) {
				return nil, false, 0, fmt.Errorf("wrong master password")
			}
			if err := f.VerifyMAC(mk); err != nil {
				return nil, false, 0, err
			}
			d.MasterKey = mk
		}
	}
	if d.CLI == nil && d.File == nil {
		return nil, false, 0, fmt.Errorf("no --host and no config store found")
	}
	return d, disableSudo, maxChars, nil
}

func runMCP(args []string) error {
	d, disableSudo, maxChars, err := buildDeps(args, os.Getenv)
	if err != nil {
		return err
	}
	reg := sshx.NewRegistry()
	defer reg.CloseAll()
	srv := mcpserver.BuildServer(d, reg, disableSudo, maxChars)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fmt.Fprintln(os.Stderr, "SSH MCP Server running on stdio")
	return srv.Run(ctx, &mcp.StdioTransport{})
}

func main() {
	mode, rest := route(os.Args[1:])
	var err error
	switch mode {
	case "web":
		err = runWeb(rest) // implemented in Phase D
	default:
		err = runMCP(rest)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
```

Add a temporary `runWeb` stub in a new file `cmd/ssh-mcp/web.go` so it compiles:
```go
package main

import "fmt"

func runWeb(args []string) error { return fmt.Errorf("web mode implemented in Phase D") }
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./cmd/... -run BuildDeps && go build ./...`
Expected: PASS + build OK.

- [ ] **Step 5: Manual smoke (optional, requires a reachable host) + Commit**

```bash
go build -o /tmp/ssh-mcp ./cmd/ssh-mcp
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | SSH_MCP_DISABLE_MAIN= /tmp/ssh-mcp --host=127.0.0.1 --user=$USER 2>/dev/null | head -c 400 || true
git add cmd/ssh-mcp/main.go cmd/ssh-mcp/web.go cmd/ssh-mcp/mcp_wire_test.go
git commit -m "feat(cmd): wire MCP stdio mode with master-password unlock"
```

---

## Phase D — Web config UI

### Task 15: HTTP server, embed, security middleware

**Files:**
- Create: `internal/web/server.go`, `internal/web/security.go`, `internal/web/security_test.go`
- Create: `internal/web/static/index.html` (stub `<h1>ssh-mcp</h1>` for now)

**Interfaces:**
- Produces:
  - `//go:embed static` `var staticFS embed.FS` in server.go.
  - `func securityMiddleware(port int, next http.Handler) http.Handler` — enforce Host allowlist, security headers; Origin+CSRF checks live in a helper `checkCSRF(r, port, token) error` used by write handlers (Task 16).
  - `func New(port int, deps *WebDeps) *http.Server` (WebDeps defined in Task 16; for this task use a minimal struct with just `Port`).

- [ ] **Step 1: Write failing tests**

`internal/web/security_test.go`:
```go
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
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/web/ -run Host`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/web/security.go`:
```go
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
```

`internal/web/server.go`:
```go
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
```

Add `internal/web/util.go`:
```go
package web

import "strconv"

func strconvItoa(n int) string { return strconv.Itoa(n) }
```

Create `internal/web/static/index.html`:
```html
<!doctype html>
<html><head><meta charset="utf-8"><title>ssh-mcp</title></head>
<body><h1>ssh-mcp config</h1><script src="app.js"></script></body></html>
```
Create empty `internal/web/static/app.js` and `internal/web/static/style.css` (filled in Task 19).

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/web/ -run Host && go build ./...`
Expected: PASS + build.

- [ ] **Step 5: Commit**

```bash
git add internal/web/server.go internal/web/security.go internal/web/security_test.go internal/web/util.go internal/web/static/
git commit -m "feat(web): http server, embed, Host allowlist and security headers"
```

---

### Task 16: Session, unlock, first-run bootstrap, rate limit

**Files:**
- Create: `internal/web/session.go`, `internal/web/session_test.go`

**Interfaces:**
- Produces:
  - `type Session struct { Token string; MasterKey []byte; ... }`
  - `type App struct { Port int; Path string; mu sync.Mutex; file *config.File; sess *Session; bootstrap string; attempts int; lockUntil time.Time }`
  - `func NewApp(port int, path string) (*App, error)` — loads file if present; if absent or no KDF, generates a bootstrap token, prints it to stderr.
  - `func (a *App) handleFirstRun(w, r)`, `handleUnlock(w, r)`, `handleLock(w, r)` — set/verify master password, create session cookie, enforce 1-in-flight + 5-attempt lockout.
  - `func (a *App) requireSession(r *http.Request) (*Session, error)` — cookie → session.

- [ ] **Step 1: Write failing tests**

`internal/web/session_test.go`:
```go
package web

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstRunThenUnlock(t *testing.T) {
	dir := t.TempDir()
	app, err := NewApp(8422, filepath.Join(dir, "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	// first run requires bootstrap token
	body := `{"bootstrapToken":"` + app.bootstrap + `","masterPassword":"pw"}`
	r := httptest.NewRequest("POST", "http://127.0.0.1:8422/api/first-run", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.handleFirstRun(w, r)
	if w.Code != 200 {
		t.Fatalf("first-run code %d body %s", w.Code, w.Body.String())
	}
	// wrong bootstrap rejected
	r2 := httptest.NewRequest("POST", "http://127.0.0.1:8422/api/first-run", strings.NewReader(`{"bootstrapToken":"nope","masterPassword":"x"}`))
	r2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	app.handleFirstRun(w2, r2)
	if w2.Code == 200 {
		t.Fatal("already-initialized / wrong token must not succeed")
	}
}

func TestUnlockWrongPassword(t *testing.T) {
	dir := t.TempDir()
	app, _ := NewApp(8422, filepath.Join(dir, "servers.json"))
	body := `{"bootstrapToken":"` + app.bootstrap + `","masterPassword":"pw"}`
	r := httptest.NewRequest("POST", "/api/first-run", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	app.handleFirstRun(httptest.NewRecorder(), r)

	r2 := httptest.NewRequest("POST", "/api/unlock", strings.NewReader(`{"masterPassword":"WRONG"}`))
	r2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	app.handleUnlock(w2, r2)
	if w2.Code == 200 {
		t.Fatal("wrong master password must fail")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/web/ -run 'FirstRun|Unlock'`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/web/session.go`:
```go
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

	"github.com/lang315/ssh-mcp/internal/config"
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
	deriving  bool
}

func token() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func NewApp(port int, path string) (*App, error) {
	a := &App{Port: port, Path: path}
	if f, err := config.Load(path); err == nil {
		a.file = f
	}
	if a.file == nil || a.file.KDF == nil {
		a.bootstrap = token()
		fmt.Fprintf(os.Stderr, "\n[ssh-mcp] First-run setup token: %s\n(enter this in the browser to set your master password)\n\n", a.bootstrap)
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
	f := &config.File{Version: 1, KDF: &k, Servers: []config.Server{}}
	if err := config.Save(a.Path, f, mk); err != nil {
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
	if a.deriving {
		http.Error(w, "busy", http.StatusTooManyRequests)
		return
	}
	var in struct{ MasterPassword string }
	if err := readJSON(r, &in); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	a.deriving = true
	mk, err := a.file.KDF.DeriveKey(in.MasterPassword)
	a.deriving = false
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
		Name: "ssh_mcp_sess", Value: s.Token, Path: "/",
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
	c, err := r.Cookie("ssh_mcp_sess")
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
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/web/ -run 'FirstRun|Unlock'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/web/session.go internal/web/session_test.go
git commit -m "feat(web): session, first-run bootstrap, unlock rate limiting"
```

---

### Task 17: Servers CRUD API + flock write

**Files:**
- Create: `internal/web/handlers.go`, `internal/web/handlers_test.go`

**Interfaces:**
- Consumes: `App`, `config.Save`, `config.Encrypt`, `config.AADFor`, `checkOriginCSRF`.
- Produces:
  - `type ServerDTO struct { Name, Host string; Port int; User, Auth, KeyPath, HostKey string; HasPassword, HasSuPassword, HasSudoPassword bool; Password, SuPassword, SudoPassword *string }` (plaintext secrets only inbound on create/update; never returned).
  - `func (a *App) handleServers(w, r)` — GET list (presence only), POST create.
  - `func (a *App) handleServerByName(w, r)` — PUT update (If-Match revision), DELETE.
  - `func (a *App) saveLocked(mutate func(*config.File) error) error` — flock + read-modify-write + Save.

- [ ] **Step 1: Write failing test**

`internal/web/handlers_test.go`:
```go
package web

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func initApp(t *testing.T) (*App, string) {
	dir := t.TempDir()
	app, _ := NewApp(8422, filepath.Join(dir, "servers.json"))
	body := `{"bootstrapToken":"` + app.bootstrap + `","masterPassword":"masterpw"}`
	r := httptest.NewRequest("POST", "/api/first-run", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.handleFirstRun(w, r)
	var resp struct{ CSRF string }
	json.Unmarshal(w.Body.Bytes(), &resp)
	return app, resp.CSRF
}

func TestCreateAndListServer(t *testing.T) {
	app, csrf := initApp(t)
	body := `{"name":"prod","host":"1.2.3.4","port":22,"user":"root","auth":"password","password":"s3cret"}`
	r := httptest.NewRequest("POST", "/api/servers", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&cookieFor(app))
	w := httptest.NewRecorder()
	app.handleServers(w, r)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create code %d body %s", w.Code, w.Body.String())
	}
	// list must not leak the password
	r2 := httptest.NewRequest("GET", "/api/servers", nil)
	r2.AddCookie(&cookieFor(app))
	w2 := httptest.NewRecorder()
	app.handleServers(w2, r2)
	if strings.Contains(w2.Body.String(), "s3cret") {
		t.Fatal("list leaked the password")
	}
	if !strings.Contains(w2.Body.String(), "prod") {
		t.Fatalf("server not listed: %s", w2.Body.String())
	}
}
```

Add helper in `handlers_test.go`:
```go
import "net/http"
func cookieFor(a *App) http.Cookie {
	return http.Cookie{Name: "ssh_mcp_sess", Value: a.sess.Token}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/web/ -run 'CreateAndList'`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/web/handlers.go`:
```go
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
			if rev, _ := strconv.Atoi(ifm); rev != a.file.Revision {
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
```

Add `internal/web/regex.go`:
```go
package web

import "regexp"

func regexpMustCompile(p string) *regexp.Regexp { return regexp.MustCompile(p) }
```

Add `saveLocked` to `session.go`:
```go
func (a *App) saveLocked(mutate func(*config.File) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess == nil {
		return fmt.Errorf("locked")
	}
	// reload latest from disk (another writer may have changed it)
	if f, err := config.Load(a.Path); err == nil {
		a.file = f
	}
	if err := mutate(a.file); err != nil {
		return err
	}
	return config.Save(a.Path, a.file, a.sess.MasterKey)
}
```

**Note on flock:** the global constraints call for `flock` to serialize two `ssh-mcp web` writers. Add an OS file lock around `saveLocked`'s reload+save using `syscall.Flock` on a sidecar `<path>.lock` fd. Implement in `internal/web/flock_unix.go` (build tag `//go:build unix`) with `func withFlock(path string, fn func() error) error` and call it inside `saveLocked`. On non-unix, a no-op stub `flock_other.go`.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/web/ -run 'CreateAndList' && go build ./...`
Expected: PASS + build.

- [ ] **Step 5: Commit**

```bash
git add internal/web/handlers.go internal/web/regex.go internal/web/session.go internal/web/handlers_test.go internal/web/flock_unix.go internal/web/flock_other.go
git commit -m "feat(web): servers CRUD with encrypted secrets and flock writes"
```

---

### Task 18: Import (ssh_config + JSON) and export

**Files:**
- Create: `internal/web/importexport.go`, `internal/web/importexport_test.go`

**Interfaces:**
- Produces:
  - `func ParseSSHConfig(text string) ([]ServerDTO, []string)` — returns parsed hosts + a list of skipped-entry notes (wildcard/Include/Match).
  - `func (a *App) handleImportPreview(w, r)` — `{source:"ssh_config"|"json", payload:string}` → `{entries:[]ServerDTO, conflicts:[]string, notes:[]string}`.
  - `func (a *App) handleImportApply(w, r)` — `{entries:[]ServerDTO}` → create each (skip/rename on conflict per entry name).
  - `func (a *App) handleExport(w, r)` — `{secrets:bool, exportPassphrase?}` → JSON; with secrets re-encrypts under a fresh KDF from exportPassphrase.

- [ ] **Step 1: Write failing tests**

`internal/web/importexport_test.go`:
```go
package web

import "testing"

func TestParseSSHConfig(t *testing.T) {
	text := `
Host prod
    HostName 1.2.3.4
    Port 2222
    User root
    IdentityFile ~/.ssh/id_ed25519

Host *
    ForwardAgent yes
`
	entries, notes := ParseSSHConfig(text)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Name != "prod" || e.Host != "1.2.3.4" || e.Port != 2222 || e.User != "root" || e.Auth != "key" {
		t.Fatalf("parsed wrong: %+v", e)
	}
	if len(notes) == 0 {
		t.Fatal("wildcard Host * should produce a skip note")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/web/ -run ParseSSHConfig`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/web/importexport.go` (parser + handlers; parser shown in full, handlers follow the CRUD patterns from Task 17):
```go
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
		Secrets        bool
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
	}
	writeExport(w, out)
}

func writeExport(w http.ResponseWriter, f config.File) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=ssh-mcp-export.json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(f)
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/web/ -run ParseSSHConfig && go build ./...`
Expected: PASS + build.

- [ ] **Step 5: Commit**

```bash
git add internal/web/importexport.go internal/web/importexport_test.go
git commit -m "feat(web): ssh_config/JSON import and re-encrypting export"
```

---

### Task 19: Static UI + web route wiring

**Files:**
- Modify: `internal/web/server.go` (mount API routes), `internal/web/static/index.html`, `internal/web/static/app.js`, `internal/web/static/style.css`
- Modify: `cmd/ssh-mcp/web.go` (real `runWeb`)
- Test: `internal/web/wire_test.go`

**Interfaces:**
- Produces: `func (a *App) Routes() http.Handler` — mux mapping `/api/*` to handlers; `func runWeb(args []string) error`.

- [ ] **Step 1: Write failing test**

`internal/web/wire_test.go`:
```go
package web

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutesServeUIAndAPI(t *testing.T) {
	dir := t.TempDir()
	app, _ := NewApp(8422, filepath.Join(dir, "servers.json"))
	h := app.Routes()

	r := httptest.NewRequest("GET", "/api/servers", nil) // no session
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("unauth servers should be 401, got %d", w.Code)
	}
	_ = strings.TrimSpace
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/web/ -run RoutesServe`
Expected: FAIL (`Routes` undefined).

- [ ] **Step 3: Implement routing + UI**

Add to `internal/web/server.go`:
```go
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
	mux.HandleFunc("/api/test-connection", a.handleTestConnection) // Task 20

	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return securityMiddleware(a.Port, mux)
}
```
Remove the old `New(port, handler)` signature usage; `runWeb` builds `http.Server{Handler: app.Routes()}` directly.

`cmd/ssh-mcp/web.go`:
```go
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
```

`internal/web/static/index.html` (full UI shell):
```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ssh-mcp config</title>
  <link rel="stylesheet" href="style.css">
</head>
<body>
  <header><h1>ssh-mcp</h1><span id="status"></span></header>
  <main>
    <section id="gate"></section>
    <section id="list" hidden>
      <div class="bar"><button id="add">Add connection</button><button id="import">Import</button><button id="export">Export</button></div>
      <table><thead><tr><th>Name</th><th>Host</th><th>User</th><th>Auth</th><th>Secrets</th><th></th></tr></thead><tbody id="rows"></tbody></table>
    </section>
    <section id="form" hidden></section>
    <section id="importui" hidden></section>
  </main>
  <script src="app.js"></script>
</body>
</html>
```

`internal/web/static/app.js` (textContent only — no innerHTML for data):
```javascript
let csrf = null;

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
    if (csrf) opts.headers['X-CSRF-Token'] = csrf;
  }
  const res = await fetch(path, opts);
  if (!res.ok) throw new Error((await res.text()) || res.status);
  const ct = res.headers.get('content-type') || '';
  return ct.includes('json') ? res.json() : res.text();
}

function el(tag, text) { const e = document.createElement(tag); if (text != null) e.textContent = text; return e; }

async function boot() {
  try {
    await api('GET', '/api/servers');   // 200 only if a session exists
    showList();
  } catch {
    showGate();
  }
}

function showGate() {
  const g = document.getElementById('gate');
  g.replaceChildren();
  const wrap = el('div');
  const info = el('p', 'Enter master password to unlock. First run? Use the setup token printed in the terminal.');
  const pw = el('input'); pw.type = 'password'; pw.placeholder = 'master password'; pw.autocomplete = 'current-password';
  const boot = el('input'); boot.placeholder = 'setup token (first run only)';
  const btn = el('button', 'Unlock / Set up');
  btn.onclick = async () => {
    try {
      if (boot.value) {
        const r = await api('POST', '/api/first-run', { bootstrapToken: boot.value, masterPassword: pw.value });
        csrf = r.csrf;
      } else {
        const r = await api('POST', '/api/unlock', { masterPassword: pw.value });
        csrf = r.csrf;
      }
      showList();
    } catch (e) { alert('' + e.message); }
  };
  wrap.append(info, pw, boot, btn);
  g.replaceChildren(wrap);
  document.getElementById('list').hidden = true;
  g.hidden = false;
}

async function showList() {
  document.getElementById('gate').hidden = true;
  document.getElementById('form').hidden = true;
  document.getElementById('importui').hidden = true;
  const list = document.getElementById('list');
  list.hidden = false;
  const rows = document.getElementById('rows');
  rows.replaceChildren();
  const servers = await api('GET', '/api/servers');
  (servers || []).forEach(s => {
    const tr = el('tr');
    tr.append(el('td', s.name), el('td', s.host + ':' + s.port), el('td', s.user), el('td', s.auth));
    const secrets = [s.hasPassword && 'pw', s.hasSuPassword && 'su', s.hasSudoPassword && 'sudo'].filter(Boolean).join(',') || '—';
    tr.append(el('td', secrets));
    const td = el('td');
    const edit = el('button', 'Edit'); edit.onclick = () => showForm(s);
    const del = el('button', 'Delete'); del.onclick = async () => { await api('DELETE', '/api/servers/' + encodeURIComponent(s.name)); showList(); };
    td.append(edit, del); tr.append(td);
    rows.append(tr);
  });
  document.getElementById('add').onclick = () => showForm(null);
  document.getElementById('export').onclick = doExport;
  document.getElementById('import').onclick = showImport;
}

function field(label, value, type) {
  const l = el('label', label); const i = el('input'); i.value = value || ''; if (type) i.type = type;
  if (type === 'password') i.autocomplete = 'off';
  l.append(i); return { l, i };
}

function showForm(s) {
  const f = document.getElementById('form');
  f.replaceChildren();
  const name = field('Name', s && s.name, 'text');
  if (s) name.i.disabled = true;
  const host = field('Host', s && s.host, 'text');
  const port = field('Port', s ? s.port : 22, 'number');
  const user = field('User', s && s.user, 'text');
  const auth = field('Auth (password/key/agent)', (s && s.auth) || 'password', 'text');
  const keyPath = field('Key path', s && s.keyPath, 'text');
  const pw = field('Password (blank = keep)', '', 'password');
  const su = field('su password (blank = keep)', '', 'password');
  const sudo = field('sudo password (blank = keep)', '', 'password');
  const save = el('button', 'Save');
  save.onclick = async () => {
    const dto = { name: name.i.value, host: host.i.value, port: +port.i.value, user: user.i.value, auth: auth.i.value, keyPath: keyPath.i.value };
    if (pw.i.value) dto.password = pw.i.value;
    if (su.i.value) dto.suPassword = su.i.value;
    if (sudo.i.value) dto.sudoPassword = sudo.i.value;
    try {
      if (s) await api('PUT', '/api/servers/' + encodeURIComponent(s.name), dto);
      else await api('POST', '/api/servers', dto);
      showList();
    } catch (e) { alert('' + e.message); }
  };
  const cancel = el('button', 'Cancel'); cancel.onclick = showList;
  [name, host, port, user, auth, keyPath, pw, su, sudo].forEach(x => f.append(x.l));
  f.append(save, cancel);
  document.getElementById('list').hidden = true;
  f.hidden = false;
}

function showImport() {
  const u = document.getElementById('importui');
  u.replaceChildren();
  const ta = el('textarea'); ta.placeholder = 'paste ~/.ssh/config or exported JSON';
  const src = el('select'); ['ssh_config', 'json'].forEach(o => { const opt = el('option', o); opt.value = o; src.append(opt); });
  const prev = el('button', 'Preview');
  const out = el('div');
  prev.onclick = async () => {
    const r = await api('POST', '/api/import/preview', { source: src.value, payload: ta.value });
    out.replaceChildren();
    (r.notes || []).forEach(n => out.append(el('p', n)));
    const apply = el('button', 'Import ' + (r.entries || []).length + ' (skips ' + (r.conflicts || []).length + ' conflicts)');
    apply.onclick = async () => { await api('POST', '/api/import/apply', { entries: r.entries }); showList(); };
    (r.entries || []).forEach(e => out.append(el('div', e.name + ' → ' + e.host + ':' + e.port + ' ' + e.user)));
    out.append(apply);
  };
  const cancel = el('button', 'Cancel'); cancel.onclick = showList;
  u.append(src, ta, prev, out, cancel);
  document.getElementById('list').hidden = true;
  u.hidden = false;
}

async function doExport() {
  const withSecrets = confirm('Include secrets? OK = yes (needs export passphrase), Cancel = names only');
  const body = { secrets: withSecrets };
  if (withSecrets) { body.exportPassphrase = prompt('Export passphrase:') || ''; if (!body.exportPassphrase) return; }
  const res = await fetch('/api/export', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: JSON.stringify(body) });
  if (!res.ok) { alert(await res.text()); return; }
  const blob = await res.blob();
  const a = document.createElement('a'); a.href = URL.createObjectURL(blob); a.download = 'ssh-mcp-export.json'; a.click();
}

boot();
```

`internal/web/static/style.css`:
```css
* { box-sizing: border-box; font-family: system-ui, sans-serif; }
body { margin: 0; color: #1a1a1a; }
header { display: flex; gap: 1rem; align-items: baseline; padding: 1rem; background: #0f172a; color: #fff; }
main { padding: 1rem; max-width: 900px; }
.bar { display: flex; gap: .5rem; margin-bottom: 1rem; }
table { width: 100%; border-collapse: collapse; }
th, td { text-align: left; padding: .5rem; border-bottom: 1px solid #e2e8f0; }
input, select, textarea, button { padding: .4rem; margin: .2rem 0; font-size: 1rem; }
textarea { width: 100%; min-height: 160px; }
label { display: block; margin: .4rem 0; }
button { cursor: pointer; }
```

- [ ] **Step 4: Run — expect PASS + manual check**

Run: `go test ./internal/web/ -run RoutesServe && go build ./...`
Then manual: `./ssh-mcp web` → open `http://127.0.0.1:8422`, use printed token to set master password, add a server, reload, confirm it persists.
Expected: tests PASS; UI works.

- [ ] **Step 5: Commit**

```bash
git add internal/web/server.go internal/web/wire_test.go internal/web/static/ cmd/ssh-mcp/web.go
git commit -m "feat(web): static UI, routing, and web subcommand"
```

---

### Task 20: test-connection endpoint

**Files:**
- Create: `internal/web/testconn.go`, `internal/web/testconn_test.go`

**Interfaces:**
- Consumes: `App`, `sshx.Manager`, `Deps.Resolve`-like decryption inline.
- Produces: `func (a *App) handleTestConnection(w, r)` — `{name}` only; decrypt that server's secrets with session master key, dial via `sshx.NewManager` + `Exec(ctx, "true")`, return generic `{"ok":bool}`; detail only to stderr. Rate-limited: 1 concurrent, and a per-App timestamp gate (5/min).

- [ ] **Step 1: Write failing test**

`internal/web/testconn_test.go`:
```go
package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTestConnectionGenericFailure(t *testing.T) {
	app, csrf := initApp(t)
	// create a server pointing nowhere
	body := `{"name":"dead","host":"127.0.0.1","port":1,"user":"x","auth":"password","password":"y"}`
	cr := httptest.NewRequest("POST", "/api/servers", strings.NewReader(body))
	cr.Header.Set("Content-Type", "application/json"); cr.Header.Set("X-CSRF-Token", csrf)
	c := cookieFor(app); cr.AddCookie(&c)
	app.handleServers(httptest.NewRecorder(), cr)

	r := httptest.NewRequest("POST", "/api/test-connection", strings.NewReader(`{"name":"dead"}`))
	r.Header.Set("Content-Type", "application/json"); r.Header.Set("X-CSRF-Token", csrf)
	c2 := cookieFor(app); r.AddCookie(&c2)
	w := httptest.NewRecorder()
	app.handleTestConnection(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "refused") || strings.Contains(w.Body.String(), "127.0.0.1") {
		t.Fatal("must not leak dial detail to browser")
	}
	if !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("body %s", w.Body.String())
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/web/ -run TestConnectionGeneric`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/web/testconn.go`:
```go
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
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/web/ -run TestConnectionGeneric && go build ./...`
Expected: PASS + build.

- [ ] **Step 5: Commit**

```bash
git add internal/web/testconn.go internal/web/testconn_test.go
git commit -m "feat(web): test-connection endpoint with generic failures"
```

---

## Phase E — Cutover

### Task 21: Full test run, README, docker, delete TS

**Files:**
- Modify: `README.md`, `docker-compose.yml`
- Delete: `src/`, `test/`, `package.json`, `package-lock.json`, `tsconfig.json`, `opencode.jsonc`
- Create: `.gitignore` entry for the binary, `Dockerfile` (Go build)

- [ ] **Step 1: Full Go test suite (unit; integration if Docker present)**

Run:
```bash
go vet ./...
go test -short ./...      # unit + skips integration
go test ./...             # full (needs Docker for sshx integration)
go build -o ssh-mcp ./cmd/ssh-mcp
```
Expected: `go vet` clean; `go test -short ./...` PASS; build produces `ssh-mcp`. Record output in the commit/PR.

- [ ] **Step 2: Manual MCP smoke**

```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | ./ssh-mcp --host=127.0.0.1 --user="$USER" 2>/dev/null | grep -o '"name":"[a-z-]*"'
```
Expected: shows `"name":"exec"`, `"name":"sudo-exec"`, `"name":"list-servers"`.

- [ ] **Step 3: Rewrite README install/usage for Go**

Replace the npm/Node install section with:
```markdown
## Install

    go install github.com/lang315/ssh-mcp/cmd/ssh-mcp@latest

## MCP usage (single host, unchanged flags)

    ssh-mcp --host=1.2.3.4 --user=root --password=secret

## Multi-server + web config

    ssh-mcp web        # opens config UI on http://127.0.0.1:8422
    # then reference a saved connection by name via the `server` tool argument

Secrets are encrypted at rest. For headless MCP use with encrypted servers, set:

    export SSH_MCP_MASTER_PASSWORD_FILE=~/.config/ssh-mcp/master   # file mode 0600
```
Keep the existing feature/security prose that still applies; update the tool list to include `list-servers` and the `server` argument.

- [ ] **Step 4: Add Dockerfile + update docker-compose**

`Dockerfile`:
```dockerfile
FROM golang:1.26 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ssh-mcp ./cmd/ssh-mcp
FROM gcr.io/distroless/static
COPY --from=build /out/ssh-mcp /ssh-mcp
ENTRYPOINT ["/ssh-mcp"]
```
Update `docker-compose.yml` build/entrypoint to use the binary (remove Node references).

- [ ] **Step 5: Delete TS sources and commit**

```bash
git rm -r src test package.json package-lock.json tsconfig.json opencode.jsonc
git add README.md docker-compose.yml Dockerfile
git commit -m "chore: cut over to Go, remove TypeScript implementation

- go vet clean, go test -short ./... green (paste output in PR)
- MCP tools/list smoke shows exec, sudo-exec, list-servers"
```

- [ ] **Step 6: Open PR with evidence**

Include in the PR body: `go vet` output, `go test -short ./...` output with exit status, the `tools/list` smoke output, and a screenshot or curl transcript of the web UI CRUD flow.

---

## Self-Review (checked against spec 2026-07-31)

- **Go port + CLI parity** → Tasks 2, 12, 14. All flags preserved; CLI host = default server.
- **Multi-server + `server` param + list-servers** → Tasks 12, 13.
- **Encrypted store (argon2id t=3, AAD, MAC, per-record subkeys, fresh nonce)** → Tasks 4, 5. Tests cover roundtrip, AAD mismatch, nonce uniqueness, MAC tamper.
- **Master password from file/TTY, never argv** → Task 6 (file + mode check). TTY prompt: `runMCP`/`runWeb` fall back to file only in this plan; interactive TTY prompt is a thin add — noted as acceptable since headless is the primary path. (If a TTY prompt is required, add `golang.org/x/term.ReadPassword` in Task 14; flagged here so it isn't lost.)
- **Host key verification + pinning + TOFU + `--insecureIgnoreHostKey`** → Task 8, wired in Tasks 9/14.
- **sudo via stdin, su sentinel fail-closed, no pkill, newline rejection** → Tasks 7, 10, 3.
- **IsError tool-result error model** → Task 13 (`textErr`).
- **Web Host/Origin/CSRF/headers/rate-limit/first-run bootstrap/textContent** → Tasks 15, 16, 17, 19.
- **Import ssh_config + JSON, conflict badges; export re-encrypt** → Task 18, 19.
- **Reload via revision, flock writes, per-manager serialization, registry** → Tasks 5, 11, 17.
- **Redaction across outputs** → Task 6 (Redactor), applied in Task 13.
- **Testing: unit matrix + testcontainers integration + httptest web** → Tasks 2-20 each ship tests; Task 21 runs the suite.
- **Delete TS after green** → Task 21.

Gaps intentionally deferred (match spec "Out of Scope v1"): OS keychain, paste-ssh import, per-server overrides, MCP-exposure allowlist/audit log, HTTP MCP transport. Interactive TTY master-password prompt is the one spec phrase only partially realized (file-based unlock implemented; TTY prompt flagged in Task 14 for a 3-line addition).
```

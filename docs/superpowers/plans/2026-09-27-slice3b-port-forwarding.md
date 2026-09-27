# Slice 3b Port Forwarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Save local, remote, and dynamic (SOCKS5) tunnels per host in the vault, and start or stop them from a Tunnels tab in the desktop app. Tunnels run in the hub over the host's shared SSH connection.

**Architecture:**
- `config.Server` gains a `Tunnels` slice. It is written only through `config.Update`, by the new `tunnels.save` / `tunnels.delete` UI-door methods.
- A new `internal/tunnel` package holds the forwarding mechanics: loopback listeners, `ssh.Client.Dial` / `Listen`, and a CONNECT-only SOCKS5 server.
- `internal/hub/tunnels.go` holds the policy: checks, runtime state, audit, `tunnels.state` notifications, and ending tunnels when a server changes.
- The renderer gets a `tunnels` tab kind, a `TunnelsView`, a `TunnelDialog`, and a running-count badge on host cards.

**Tech Stack:** Go 1.26, `golang.org/x/crypto/ssh` (no new dependency), React/TypeScript renderer, vitest, Playwright/Electron.

**Spec:** `docs/superpowers/specs/2026-09-27-slice3b-port-forwarding-design.md`

## Global Constraints

- Branch `feat/port-forwarding`. Commit trailer for the controller: `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>` then `Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2`. Subagents use their own model name in `Co-Authored-By` and keep the same `Claude-Session` line. Git user "Lãng".
- No new Go or npm dependency.
- Listeners bind **only** `127.0.0.1`. The bind address is never a parameter: local and dynamic use `net.Listen("tcp", "127.0.0.1:<port>")`, and remote uses `client.Listen("tcp", "127.0.0.1:<port>")`.
- No `tunnels.*` method on the MCP door (`internal/hub/mcpdoor.go` is not touched).
- UI-door params are decoded with `strictParams` (exact keys, no duplicates).
- `ProtocolVersion` (Go, `internal/hub/idle.go`) and `PROTOCOL_VERSION` (TS, `desktop/src/shared/protocol.ts`) become `5`.
- Every `tunnels.*` request counts as UI activity (`h.touch()`). `tunnels.stop` is a notification (`s.Handle`) and does not count.
- Running tunnels survive `lock`. `tunnels.start`, `tunnels.save`, and `tunnels.delete` need an unlocked vault. `tunnels.stop` and `tunnels.list` work while locked.
- The renderer never polls. `tunnels.list` is called on becoming ready, after a hub restart, and after a save or delete.
- Server-sourced text (labels, errors) goes through `displayText` (`desktop/src/shared/display.ts`) in the renderer.
- Validation limits: ports 1..65535; `targetHost` must pass `plainToken` and be ≤253 bytes; label ≤64 bytes with no control or format runes; at most 32 tunnels per server; `kind`+`listenPort` unique within a server; `dynamic` has no target.
- Error texts, verbatim: `port %d is already in use`, `the server refused the remote forward`, `open a terminal to this host once to trust its host key`, `host key changed`, `connection failed`, `stop the tunnel first`, `connection lost`, `server changed`, `tunnel not found`, `a server holds at most 32 tunnels`, `another tunnel already listens on %s port %d`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/config/tunnel.go` (new) | `Tunnel` type and `Validate` |
| `internal/config/store.go` | `Server.Tunnels` field |
| `internal/config/server_input.go` | `ApplyServer` keeps `before.Tunnels` |
| `internal/hub/hosts.go` | `dialChanged` ignores `Tunnels`; server changes end tunnels |
| `internal/sshx/sshtest/forward.go` (new) | `direct-tcpip` channels and `tcpip-forward` global requests for the test server |
| `internal/sshx/manager.go` | `Manager.Client()` accessor |
| `internal/tunnel/tunnel.go` (new) | `Forward`, `Local`, `Remote`, `Dynamic`, pipe, and conn tracking |
| `internal/tunnel/socks.go` (new) | SOCKS5 handshake |
| `internal/broker/audit.go` | `TunnelRecord`, `WriteTunnel` |
| `internal/hub/tunnels.go` (new) | `tunnelSet`, Hub methods, UI-door registration |
| `internal/hub/uidoor.go`, `hub.go`, `idle.go` | wiring, `Close` ends tunnels, protocol 5 |
| `desktop/src/shared/protocol.ts` | types, method lists, protocol 5 |
| `desktop/src/renderer/transport.ts` | `hub.tunnels*` calls |
| `desktop/src/renderer/tunnels.ts` (new) | pure helpers: `summary`, `parseTunnelForm`, `applyState`, `runningCount` |
| `desktop/src/renderer/TunnelsView.tsx`, `TunnelDialog.tsx` (new) | the tab UI |
| `desktop/src/renderer/terminals.ts`, `TerminalTabs.tsx`, `HostList.tsx`, `App.tsx`, `icons.tsx`, `styles.css` | tab kind, button, badge, state |
| `desktop/e2e/tunnels.spec.ts` (new) | e2e against `sshtestd` |
| `CLAUDE.md`, `docs/superpowers/ROADMAP.md`, `PRODUCT.md` | docs |

---

### Task 1: Tunnel config type and keeping tunnels across server edits

**Files:**
- Create: `internal/config/tunnel.go`, `internal/config/tunnel_test.go`
- Modify: `internal/config/store.go:15-29`, `internal/config/server_input.go:81`, `internal/hub/hosts.go:138-143`
- Test: `internal/config/server_input_test.go` (append), `internal/hub/hosts_test.go` (append)

**Interfaces:**
- Produces:
  - `config.Tunnel{ID, Kind, ListenPort, TargetHost, TargetPort, Label}`
  - `func (t Tunnel) Validate() error`
  - `config.MaxTunnels = 32`
  - `config.Server.Tunnels []Tunnel` (json `tunnels,omitempty`)

- [ ] **Step 1: Write the failing tests**

`internal/config/tunnel_test.go`:

```go
package config

import (
	"strings"
	"testing"
)

func TestTunnelValidate(t *testing.T) {
	ok := []Tunnel{
		{Kind: "local", ListenPort: 5433, TargetHost: "db", TargetPort: 5432},
		{Kind: "remote", ListenPort: 8080, TargetHost: "localhost", TargetPort: 3000, Label: "web"},
		{Kind: "dynamic", ListenPort: 1080},
		{Kind: "local", ListenPort: 1, TargetHost: "::1", TargetPort: 65535},
	}
	for _, tn := range ok {
		if err := tn.Validate(); err != nil {
			t.Errorf("%+v: %v", tn, err)
		}
	}
	bad := map[string]Tunnel{
		"kind":       {Kind: "socks", ListenPort: 1080},
		"listenPort": {Kind: "dynamic", ListenPort: 0},
		"listenPort ": {Kind: "dynamic", ListenPort: 65536},
		"targetHost": {Kind: "local", ListenPort: 1, TargetHost: "a b", TargetPort: 1},
		"targetHost ": {Kind: "local", ListenPort: 1, TargetHost: strings.Repeat("a", 254), TargetPort: 1},
		"targetPort": {Kind: "remote", ListenPort: 1, TargetHost: "h", TargetPort: 0},
		"dynamic":    {Kind: "dynamic", ListenPort: 1, TargetHost: "h", TargetPort: 1},
		"label":      {Kind: "dynamic", ListenPort: 1, Label: strings.Repeat("x", 65)},
		"label ":     {Kind: "dynamic", ListenPort: 1, Label: "a\x1bb"},
	}
	for field, tn := range bad {
		err := tn.Validate()
		if err == nil || !strings.HasPrefix(err.Error(), strings.TrimSpace(field)) {
			t.Errorf("%s: %+v: got %v", field, tn, err)
		}
	}
}
```

Append to `internal/config/server_input_test.go`. Check which helpers that file already uses to build a `File`, and reuse them; the literal below is enough on its own:

```go
func TestApplyServerKeepsTunnels(t *testing.T) {
	tun := []Tunnel{{ID: "0123456789abcdef", Kind: "dynamic", ListenPort: 1080}}
	f := &File{Version: 1, Servers: []Server{{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent", Tunnels: tun}}}
	// A host change and a rename: the tunnels still come along.
	_, after, err := ApplyServer(f, "a", ServerInput{Name: "b", Host: "h2", Port: 22, User: "u", Auth: "agent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Tunnels) != 1 || after.Tunnels[0] != tun[0] || len(f.Servers[0].Tunnels) != 1 {
		t.Fatalf("tunnels lost: %+v", after)
	}
}
```

Append to `internal/hub/hosts_test.go`:

```go
func TestDialChangedIgnoresTunnelsAndAIVisible(t *testing.T) {
	a := config.Server{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent"}
	b := a
	b.AIVisible = true
	b.Tunnels = []config.Tunnel{{ID: "x", Kind: "dynamic", ListenPort: 1080}}
	if dialChanged(a, b) {
		t.Fatal("tunnels or aiVisible counted as a dial change")
	}
	b.Port = 23
	if !dialChanged(a, b) {
		t.Fatal("port change missed")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config ./internal/hub -run 'TestTunnelValidate|TestApplyServerKeepsTunnels|TestDialChangedIgnores' 2>&1 | head`
Expected: build failure, with `undefined: Tunnel` and `unknown field Tunnels`.

- [ ] **Step 3: Implement**

`internal/config/tunnel.go`:

```go
package config

import "errors"

// MaxTunnels caps the tunnels one server holds.
const MaxTunnels = 32

// Tunnel is a saved port forward. Not a secret: it lives in plain text in
// the MAC-covered store. The bind address is not a field: the hub binds
// only 127.0.0.1 (see slice 3b spec).
type Tunnel struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // local, remote, dynamic
	ListenPort int    `json:"listenPort"`
	TargetHost string `json:"targetHost,omitempty"`
	TargetPort int    `json:"targetPort,omitempty"`
	Label      string `json:"label,omitempty"`
}

// Validate names the first bad field. The ID is the hub's to check.
func (t Tunnel) Validate() error {
	_, _, badLabel := forbiddenRune(t.Label)
	switch {
	case t.Kind != "local" && t.Kind != "remote" && t.Kind != "dynamic":
		return errors.New("kind: must be local, remote, or dynamic")
	case t.ListenPort < 1 || t.ListenPort > 65535:
		return errors.New("listenPort: must be 1-65535")
	case t.Kind == "dynamic" && (t.TargetHost != "" || t.TargetPort != 0):
		return errors.New("dynamic: takes no target")
	case t.Kind != "dynamic" && (!plainToken(t.TargetHost) || len(t.TargetHost) > 253):
		return errors.New("targetHost: required, at most 253 bytes, with no spaces or control characters")
	case t.Kind != "dynamic" && (t.TargetPort < 1 || t.TargetPort > 65535):
		return errors.New("targetPort: must be 1-65535")
	case len(t.Label) > 64 || badLabel:
		return errors.New("label: at most 64 bytes, with no control characters")
	}
	return nil
}
```

In `internal/config/store.go`, add this after `EncKeyPassphrase` in `Server`:

```go
	Tunnels          []Tunnel `json:"tunnels,omitempty"`
```

In `internal/config/server_input.go`, `ApplyServer`, the line building `after` becomes:

```go
	after = Server{Name: in.Name, Host: in.Host, Port: in.Port, User: in.User, Auth: in.Auth, KeyPath: in.KeyPath, AIVisible: in.AIVisible, Tunnels: before.Tunnels}
```

Also extend the doc comment's first sentence with: "Tunnels are kept as they are; only tunnels.save and tunnels.delete change them."

In `internal/hub/hosts.go`, replace `dialChanged`:

```go
// dialChanged: every field but AIVisible and Tunnels feeds the dial config
// or the name the connection is registered under.
func dialChanged(a, b config.Server) bool {
	a.AIVisible, a.Tunnels = b.AIVisible, nil
	b.Tunnels = nil
	return !reflect.DeepEqual(a, b)
}
```

Add `"reflect"` to that file's imports. Then run `go vet ./...`. Any other `==` / `!=` comparison of `config.Server` values fails to compile now; fix each one the same way. `grep -rn "config.Server{}\s*[!=]=" internal cmd` should find nothing.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config ./internal/hub -short -count=1`
Expected: `ok` for both packages.

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/hub/hosts.go internal/hub/hosts_test.go
git commit -m "feat(config): saved tunnels per server, kept across server edits"
```

---

### Task 2: Test SSH server supports port forwarding

**Files:**
- Create: `internal/sshx/sshtest/forward.go`, `internal/sshx/sshtest/forward_test.go`
- Modify: `internal/sshx/sshtest/sshtest.go` (`Server` fields, `serveConn`, package doc)

**Interfaces:**
- Produces:
  - `sshtest.Server` accepts `direct-tcpip` channels (it dials the target from the test process);
  - it answers `tcpip-forward` / `cancel-tcpip-forward` global requests for `127.0.0.1` or `localhost` only, opening a `forwarded-tcpip` channel per accepted connection;
  - `func (s *Server) RefuseForward()` makes every later `tcpip-forward` fail;
  - `sshtestd` gets all of this for free.

- [ ] **Step 1: Write the failing test**

`internal/sshx/sshtest/forward_test.go`:

```go
package sshtest

import (
	"io"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

func dial(t *testing.T, s *Server) *ssh.Client {
	t.Helper()
	c, err := ssh.Dial("tcp", s.Addr(), &ssh.ClientConfig{User: "u", Auth: []ssh.AuthMethod{ssh.Password("x")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func roundTrip(t *testing.T, c net.Conn) {
	t.Helper()
	defer c.Close()
	c.Write([]byte("ping"))
	b := make([]byte, 4)
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "ping" {
		t.Fatalf("got %q %v", b, err)
	}
}

func TestDirectTCPIP(t *testing.T) {
	s := Start(t)
	c := dial(t, s)
	conn, err := c.Dial("tcp", echoServer(t))
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, conn)
}

func TestRemoteForward(t *testing.T) {
	s := Start(t)
	c := dial(t, s)
	ln, err := c.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		in, err := ln.Accept()
		if err != nil {
			return
		}
		io.Copy(in, in)
		in.Close()
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, conn)
}

func TestRemoteForwardRefusals(t *testing.T) {
	s := Start(t)
	c := dial(t, s)
	if _, err := c.Listen("tcp", "0.0.0.0:0"); err == nil {
		t.Fatal("non-loopback bind accepted")
	}
	s.RefuseForward()
	if _, err := c.Listen("tcp", "127.0.0.1:0"); err == nil {
		t.Fatal("RefuseForward ignored")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sshx/sshtest -run 'TestDirectTCPIP|TestRemoteForward' -count=1`
Expected: FAIL. It may not compile yet (`RefuseForward` is undefined), and `ssh: rejected: unknown channel type` / `tcpip-forward request denied by peer`.

- [ ] **Step 3: Implement**

In `sshtest.go`:
- Add the field `refuseFwd bool // tcpip-forward always fails` to `Server`.
- Add ", direct-tcpip and loopback tcpip-forward" to the package doc sentence about what it serves.
- Replace the body of `serveConn` after `go func() { <-s.done; conn.Close() }()` with:

```go
	go s.globalRequests(conn, reqs)
	for nch := range chans {
		switch nch.ChannelType() {
		case "session":
			ch, creqs, err := nch.Accept()
			if err != nil {
				continue
			}
			go s.session(ch, creqs)
		case "direct-tcpip":
			go directTCPIP(nch)
		default:
			nch.Reject(ssh.UnknownChannelType, "")
		}
	}
```

`internal/sshx/sshtest/forward.go`:

```go
package sshtest

import (
	"io"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

// RefuseForward makes every later tcpip-forward request fail.
func (s *Server) RefuseForward() { s.mu.Lock(); s.refuseFwd = true; s.mu.Unlock() }

// pipe copies both ways and closes both ends when either direction ends.
func pipe(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
}

func directTCPIP(nch ssh.NewChannel) {
	var p struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nch.ExtraData(), &p); err != nil {
		nch.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	out, err := net.Dial("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
	if err != nil {
		nch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		out.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	pipe(ch, out)
}

// globalRequests serves tcpip-forward for 127.0.0.1/localhost only, like an
// sshd with GatewayPorts no, and cancel-tcpip-forward.
func (s *Server) globalRequests(conn *ssh.ServerConn, reqs <-chan *ssh.Request) {
	var mu sync.Mutex
	lns := map[string]net.Listener{}
	defer func() {
		mu.Lock()
		for _, ln := range lns {
			ln.Close()
		}
		mu.Unlock()
	}()
	for req := range reqs {
		var p struct {
			Addr string
			Port uint32
		}
		switch req.Type {
		case "tcpip-forward":
			s.mu.Lock()
			refuse := s.refuseFwd
			s.mu.Unlock()
			if ssh.Unmarshal(req.Payload, &p) != nil || refuse || (p.Addr != "127.0.0.1" && p.Addr != "localhost") {
				req.Reply(false, nil)
				continue
			}
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(p.Port))))
			if err != nil {
				req.Reply(false, nil)
				continue
			}
			port := uint32(ln.Addr().(*net.TCPAddr).Port)
			mu.Lock()
			lns[net.JoinHostPort(p.Addr, strconv.Itoa(int(port)))] = ln
			mu.Unlock()
			if p.Port == 0 {
				req.Reply(true, ssh.Marshal(struct{ Port uint32 }{port}))
			} else {
				req.Reply(true, nil)
			}
			go func(addr string) {
				for {
					in, err := ln.Accept()
					if err != nil {
						return
					}
					ra := in.RemoteAddr().(*net.TCPAddr)
					ch, creqs, err := conn.OpenChannel("forwarded-tcpip", ssh.Marshal(struct {
						Addr     string
						Port     uint32
						OrigAddr string
						OrigPort uint32
					}{addr, port, ra.IP.String(), uint32(ra.Port)}))
					if err != nil {
						in.Close()
						continue
					}
					go ssh.DiscardRequests(creqs)
					go pipe(ch, in)
				}
			}(p.Addr)
		case "cancel-tcpip-forward":
			ssh.Unmarshal(req.Payload, &p)
			key := net.JoinHostPort(p.Addr, strconv.Itoa(int(p.Port)))
			mu.Lock()
			ln := lns[key]
			delete(lns, key)
			mu.Unlock()
			if ln != nil {
				ln.Close()
			}
			req.Reply(ln != nil, nil)
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sshx/sshtest -count=1 -race`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/sshx/sshtest
git commit -m "test(sshtest): serve direct-tcpip and loopback tcpip-forward"
```

---

### Task 3: `internal/tunnel` package (local, remote, SOCKS5)

**Files:**
- Create: `internal/tunnel/tunnel.go`, `internal/tunnel/socks.go`, `internal/tunnel/tunnel_test.go`
- Modify: `internal/sshx/manager.go` (add `Client`)

**Interfaces:**
- Consumes: `sshtest` forwarding (Task 2).
- Produces:

```go
package tunnel
type Forward struct{ /* unexported */ }
func Local(c *ssh.Client, listen, target string, onConns func()) (*Forward, error)   // listen: "127.0.0.1:<port>"
func Remote(c *ssh.Client, listen, target string, onConns func()) (*Forward, error)  // listen on the server
func Dynamic(c *ssh.Client, listen string, onConns func()) (*Forward, error)
func (f *Forward) Conns() int           // open now
func (f *Forward) Total() int           // accepted since start
func (f *Forward) Done() <-chan struct{} // closed when the accept loop ends
func (f *Forward) Err() error           // nil after Close; the accept error otherwise
func (f *Forward) Close()               // listener and every open connection; idempotent
// sshx:
func (m *Manager) Client() (*ssh.Client, error) // dials if needed
```

`onConns` (may be nil) is called, on a goroutine of the forward, every time `Conns()` changes.

- [ ] **Step 1: Write the failing tests**

`internal/tunnel/tunnel_test.go`:

```go
package tunnel

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

func client(t *testing.T) *ssh.Client {
	t.Helper()
	s := sshtest.Start(t)
	c, err := ssh.Dial("tcp", s.Addr(), &ssh.ClientConfig{User: "u", Auth: []ssh.AuthMethod{ssh.Password("x")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func echo(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	return ln.Addr().String()
}

func ping(t *testing.T, c net.Conn) {
	t.Helper()
	c.Write([]byte("ping"))
	b := make([]byte, 4)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "ping" {
		t.Fatalf("got %q %v", b, err)
	}
}

func waitConns(t *testing.T, f *Forward, n int) {
	t.Helper()
	for i := 0; i < 200 && f.Conns() != n; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if f.Conns() != n {
		t.Fatalf("conns %d, want %d", f.Conns(), n)
	}
}

func TestLocal(t *testing.T) {
	c := client(t)
	h, p := echo(t)
	f, err := Local(c, freePort(t), net.JoinHostPort(h, strconv.Itoa(p)), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	conn, err := net.Dial("tcp", f.Addr())
	if err != nil {
		t.Fatal(err)
	}
	ping(t, conn)
	waitConns(t, f, 1)
	f.Close() // ends the open connection too
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection survived Close")
	}
	<-f.Done()
	if f.Err() != nil || f.Total() != 1 {
		t.Fatalf("err %v total %d", f.Err(), f.Total())
	}
}

func TestLocalPortInUse(t *testing.T) {
	c := client(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	if _, err := Local(c, ln.Addr().String(), "127.0.0.1:1", nil); err == nil {
		t.Fatal("bound a port in use")
	}
}

func TestRemote(t *testing.T) {
	c := client(t)
	h, p := echo(t)
	f, err := Remote(c, freePort(t), net.JoinHostPort(h, strconv.Itoa(p)), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	conn, err := net.Dial("tcp", f.Addr()) // sshtest listens on this machine's loopback
	if err != nil {
		t.Fatal(err)
	}
	ping(t, conn)
	conn.Close()
}

func socksConnect(t *testing.T, proxy string, req []byte) (net.Conn, byte) {
	t.Helper()
	c, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte{5, 1, 0})
	r := make([]byte, 2)
	io.ReadFull(c, r)
	if r[0] != 5 || r[1] != 0 {
		t.Fatalf("method reply %v", r)
	}
	c.Write(req)
	rep := make([]byte, 10)
	if _, err := io.ReadFull(c, rep); err != nil {
		t.Fatal(err)
	}
	return c, rep[1]
}

func TestDynamic(t *testing.T) {
	c := client(t)
	_, p := echo(t)
	f, err := Dynamic(c, freePort(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	port := binary.BigEndian.AppendUint16(nil, uint16(p))
	// IPv4.
	conn, code := socksConnect(t, f.Addr(), append([]byte{5, 1, 0, 1, 127, 0, 0, 1}, port...))
	if code != 0 {
		t.Fatalf("ipv4 reply %d", code)
	}
	ping(t, conn)
	conn.Close()
	// Domain name, resolved by the server side.
	name := "localhost"
	conn, code = socksConnect(t, f.Addr(), append(append([]byte{5, 1, 0, 3, byte(len(name))}, name...), port...))
	if code != 0 {
		t.Fatalf("domain reply %d", code)
	}
	ping(t, conn)
	conn.Close()
	// BIND is not supported.
	conn, code = socksConnect(t, f.Addr(), append([]byte{5, 2, 0, 1, 127, 0, 0, 1}, port...))
	conn.Close()
	if code != 7 {
		t.Fatalf("bind reply %d, want 7", code)
	}
}

func TestDoneWhenClientDies(t *testing.T) {
	c := client(t)
	f, err := Remote(c, freePort(t), "127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	select {
	case <-f.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("remote forward outlived its client")
	}
	if f.Err() == nil {
		t.Fatal("no error after the client died")
	}
}
```

Note: `Addr()` returns the listener's address. Add it to the interface:

```go
func (f *Forward) Addr() string
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tunnel -count=1`
Expected: build failure (the package is empty).

- [ ] **Step 3: Implement**

`internal/tunnel/tunnel.go`:

```go
// Package tunnel runs port forwards over an SSH client: local (-L), remote
// (-R) and dynamic (-D, SOCKS5). It holds no policy; the hub decides who may
// start what and binds only loopback.
package tunnel

import (
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Forward is one running tunnel.
type Forward struct {
	ln       net.Listener
	onConns  func()
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	total    int
	closed   bool
	err      error
	done     chan struct{}
}

func serve(ln net.Listener, onConns func(), handle func(net.Conn)) *Forward {
	f := &Forward{ln: ln, onConns: onConns, conns: map[net.Conn]struct{}{}, done: make(chan struct{})}
	go func() {
		defer close(f.done)
		for {
			in, err := ln.Accept()
			if err != nil {
				f.mu.Lock()
				if !f.closed {
					f.err = err
				}
				f.mu.Unlock()
				f.Close()
				return
			}
			if !f.track(in) {
				in.Close()
				continue
			}
			go func() {
				defer f.untrack(in)
				handle(in)
			}()
		}
	}()
	return f
}

func (f *Forward) track(c net.Conn) bool {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return false
	}
	f.conns[c] = struct{}{}
	f.total++
	f.mu.Unlock()
	f.changed()
	return true
}

func (f *Forward) untrack(c net.Conn) {
	c.Close()
	f.mu.Lock()
	delete(f.conns, c)
	f.mu.Unlock()
	f.changed()
}

func (f *Forward) changed() {
	if f.onConns != nil {
		f.onConns()
	}
}

func (f *Forward) Addr() string            { return f.ln.Addr().String() }
func (f *Forward) Done() <-chan struct{}   { return f.done }
func (f *Forward) Conns() int              { f.mu.Lock(); defer f.mu.Unlock(); return len(f.conns) }
func (f *Forward) Total() int              { f.mu.Lock(); defer f.mu.Unlock(); return f.total }
func (f *Forward) Err() error              { f.mu.Lock(); defer f.mu.Unlock(); return f.err }

// Close stops accepting and ends every open connection. Idempotent.
func (f *Forward) Close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	open := make([]net.Conn, 0, len(f.conns))
	for c := range f.conns {
		open = append(open, c)
	}
	f.mu.Unlock()
	f.ln.Close()
	for _, c := range open {
		c.Close()
	}
}

// pipe copies both ways; when either direction ends, both ends close.
func pipe(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
	<-done
}

// Local listens on listen (this machine) and dials target through the server.
func Local(c *ssh.Client, listen, target string, onConns func()) (*Forward, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(in net.Conn) {
		out, err := c.Dial("tcp", target)
		if err != nil {
			return
		}
		pipe(in, out)
	}), nil
}

// Remote asks the server to listen on listen and dials target from here.
func Remote(c *ssh.Client, listen, target string, onConns func()) (*Forward, error) {
	ln, err := c.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(in net.Conn) {
		out, err := net.DialTimeout("tcp", target, 10*time.Second)
		if err != nil {
			return
		}
		pipe(in, out)
	}), nil
}

// Dynamic runs a SOCKS5 server (CONNECT only, no auth) on listen.
func Dynamic(c *ssh.Client, listen string, onConns func()) (*Forward, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	return serve(ln, onConns, func(in net.Conn) {
		out, err := socks(in, func(addr string) (net.Conn, error) { return c.Dial("tcp", addr) })
		if err != nil {
			return
		}
		pipe(in, out)
	}), nil
}
```

`internal/tunnel/socks.go`:

```go
package tunnel

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"time"
)

// SOCKS5 reply codes used here (RFC 1928 §6).
const (
	repOK          = 0x00
	repFailure     = 0x01
	repCmdNotSupp  = 0x07
	repAddrNotSupp = 0x08
)

var errSocks = errors.New("socks: bad request")

// socks runs the RFC 1928 handshake on in: version 5, method "no auth",
// CONNECT with an IPv4, IPv6 or domain address. A domain is passed to dial
// unresolved, so DNS happens on the server, as with ssh -D. It returns the
// dialled connection after writing the success reply.
func socks(in net.Conn, dial func(addr string) (net.Conn, error)) (net.Conn, error) {
	in.SetDeadline(time.Now().Add(10 * time.Second))
	defer in.SetDeadline(time.Time{})
	h := make([]byte, 2)
	if _, err := io.ReadFull(in, h); err != nil || h[0] != 5 {
		return nil, errSocks
	}
	methods := make([]byte, h[1])
	if _, err := io.ReadFull(in, methods); err != nil {
		return nil, errSocks
	}
	noAuth := false
	for _, m := range methods {
		noAuth = noAuth || m == 0
	}
	if !noAuth {
		in.Write([]byte{5, 0xff})
		return nil, errSocks
	}
	if _, err := in.Write([]byte{5, 0}); err != nil {
		return nil, err
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(in, req); err != nil || req[0] != 5 {
		return nil, errSocks
	}
	var host string
	switch req[3] {
	case 1, 4:
		ip := make([]byte, map[byte]int{1: 4, 4: 16}[req[3]])
		if _, err := io.ReadFull(in, ip); err != nil {
			return nil, errSocks
		}
		host = net.IP(ip).String()
	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(in, n); err != nil {
			return nil, errSocks
		}
		name := make([]byte, n[0])
		if _, err := io.ReadFull(in, name); err != nil {
			return nil, errSocks
		}
		host = string(name)
	default:
		reply(in, repAddrNotSupp)
		return nil, errSocks
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(in, pb); err != nil {
		return nil, errSocks
	}
	if req[1] != 1 {
		reply(in, repCmdNotSupp)
		return nil, errSocks
	}
	out, err := dial(net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb)))))
	if err != nil {
		reply(in, repFailure)
		return nil, err
	}
	if err := reply(in, repOK); err != nil {
		out.Close()
		return nil, err
	}
	return out, nil
}

// reply sends a reply with a zero IPv4 bind address; clients ignore it for CONNECT.
func reply(w io.Writer, code byte) error {
	_, err := w.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}
```

In `internal/sshx/manager.go`, add this after `OpenSession`:

```go
// Client returns the shared connection, dialling it if needed. Forwards
// (internal/tunnel) run on it; a caller watches client.Wait for its end.
func (m *Manager) Client() (*ssh.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return nil, err
	}
	return m.client, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tunnel ./internal/sshx/sshtest -race -count=3`
Expected: `ok`, three times with no flake. `gofmt -l internal/tunnel` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/tunnel internal/sshx/manager.go
git commit -m "feat(tunnel): local, remote and SOCKS5 forwards over an SSH client"
```

---

### Task 4: Hub tunnels (methods, runtime, audit, server changes, protocol 5)

**Files:**
- Create: `internal/hub/tunnels.go`, `internal/hub/tunnels_test.go`
- Modify:
  - `internal/broker/audit.go` (add `TunnelRecord`, `WriteTunnel`);
  - `internal/hub/hub.go` (the `tunnels *tunnelSet` field, created in `New`, and `Close` ends all);
  - `internal/hub/hosts.go` (end tunnels before `h.reg.Close` in `SaveServer`, `DeleteServer`, `ForgetHostKey`);
  - `internal/hub/uidoor.go` (`registerTunnelMethods`);
  - `internal/hub/idle.go` (`ProtocolVersion = 5`, and any test asserting `4`).

**Interfaces:**
- Consumes: `config.Tunnel`, `config.MaxTunnels` (Task 1); `tunnel.Local/Remote/Dynamic`, `(*sshx.Manager).Client` (Task 3); `sshtest.RefuseForward` (Task 2).
- Produces the UI-door methods exactly as follows (the renderer, Task 5, relies on these shapes):
  - `tunnels.list {}` returns `[{server, id, kind, listenPort, targetHost?, targetPort?, label?, status, error?, conns}]`, where `status` is one of `stopped`, `starting`, `running`, `error`.
  - `tunnels.save {server, tunnel:{id, kind, listenPort, targetHost, targetPort, label}}` returns the saved tunnel (with its id). An empty `id` means new.
  - `tunnels.delete {server, id}` returns `{}`.
  - `tunnels.start {server, id}` returns `{}` once the tunnel is listening.
  - `tunnels.stop {server, id}` is a notification.
  - The notification `tunnels.state {server, id, status, error?, conns}`.

- [ ] **Step 1: Write the failing tests**

`internal/hub/tunnels_test.go`. It reuses `filesHub` (`files_test.go`): an unlocked vault with `fs` (pinned) and `new` (unpinned) on one `sshtest` server, a door client `fx.c`, raw input `fx.w`, and notifications `fx.notes`. Also reuses `sendNote` (`term_test.go`) and `readAudit` (`hub_test.go`).

```go
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/rpc"
)

type tunnelRow struct {
	Server     string `json:"server"`
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	ListenPort int    `json:"listenPort"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	Conns      int    `json:"conns"`
}

func tunnelEcho(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func tunnelFreePort(t *testing.T) int {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func saveTunnel(t *testing.T, fx filesFixture, server string, tn map[string]any) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	if err := fx.c.Call(context.Background(), "tunnels.save", map[string]any{"server": server, "tunnel": tn}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.ID) != 16 {
		t.Fatalf("id %q", out.ID)
	}
	return out.ID
}

func listTunnels(t *testing.T, fx filesFixture) []tunnelRow {
	t.Helper()
	var rows []tunnelRow
	if err := fx.c.Call(context.Background(), "tunnels.list", map[string]any{}, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func echoThrough(t *testing.T, port int) {
	t.Helper()
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("ping"))
	b := make([]byte, 4)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "ping" {
		t.Fatalf("echo %q %v", b, err)
	}
}

func tunnelsHub(t *testing.T) filesFixture {
	fx := filesHub(t)
	t.Cleanup(fx.h.Close) // Close ends running tunnels
	return fx
}

func TestTunnelSaveListStartStopDelete(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	lp := tunnelFreePort(t)
	id := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "local", "listenPort": lp,
		"targetHost": "127.0.0.1", "targetPort": tunnelEcho(t), "label": "echo"})
	if rows := listTunnels(t, fx); len(rows) != 1 || rows[0].Status != "stopped" || rows[0].Server != "fs" {
		t.Fatalf("%+v", rows)
	}
	if err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id}, nil); err != nil {
		t.Fatal(err)
	}
	echoThrough(t, lp)
	// starting, then running.
	if st := waitNote(t, fx.notes, "tunnels.state", id); st["status"] != "starting" {
		t.Fatalf("%v", st)
	}
	if st := waitNote(t, fx.notes, "tunnels.state", id); st["status"] != "running" {
		t.Fatalf("%v", st)
	}
	// A running tunnel cannot be edited.
	err := fx.c.Call(ctx, "tunnels.save", map[string]any{"server": "fs", "tunnel": map[string]any{"id": id, "kind": "dynamic", "listenPort": lp}}, nil)
	if err == nil || !strings.Contains(err.Error(), "stop the tunnel first") {
		t.Fatalf("edit while running: %v", err)
	}
	// It keeps running across a lock; stop works while locked.
	fx.h.Lock()
	echoThrough(t, lp)
	sendNote(t, fx.w, "tunnels.stop", map[string]any{"server": "fs", "id": id})
	for st := waitNote(t, fx.notes, "tunnels.state", id); st["status"] != "stopped"; st = waitNote(t, fx.notes, "tunnels.state", id) {
	}
	if _, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(lp)); err == nil {
		t.Fatal("still listening after stop")
	}
	// Start, save, delete need the vault unlocked.
	for _, m := range []string{"tunnels.start", "tunnels.delete"} {
		if err := fx.c.Call(ctx, m, map[string]any{"server": "fs", "id": id}, nil); err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatalf("%s while locked: %v", m, err)
		}
	}
	unlockForTest(fx.h)
	if err := fx.c.Call(ctx, "tunnels.delete", map[string]any{"server": "fs", "id": id}, nil); err != nil {
		t.Fatal(err)
	}
	if rows := listTunnels(t, fx); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
	_, recs := readAudit(t, fx.store)
	var phases []string
	for _, r := range recs {
		if r["kind"] == "tunnel" {
			phases = append(phases, r["phase"].(string))
		}
	}
	if strings.Join(phases, ",") != "start,end" {
		t.Fatalf("tunnel audit %v", phases)
	}
}

func TestTunnelStartRefusals(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	id := saveTunnel(t, fx, "new", map[string]any{"id": "", "kind": "dynamic", "listenPort": tunnelFreePort(t)})
	err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "new", "id": id}, nil)
	if err == nil || err.Error() != "open a terminal to this host once to trust its host key" {
		t.Fatalf("unpinned: %v", err)
	}
	busy, _ := net.Listen("tcp", "127.0.0.1:0")
	defer busy.Close()
	bp := busy.Addr().(*net.TCPAddr).Port
	id2 := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": bp})
	err = fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id2}, nil)
	if err == nil || err.Error() != "port "+strconv.Itoa(bp)+" is already in use" {
		t.Fatalf("busy port: %v", err)
	}
	if rows := listTunnels(t, fx); rows[len(rows)-1].Status != "error" {
		t.Fatalf("%+v", rows)
	}
	fx.srv.RefuseForward()
	id3 := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "remote", "listenPort": tunnelFreePort(t), "targetHost": "127.0.0.1", "targetPort": 1})
	err = fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id3}, nil)
	if err == nil || err.Error() != "the server refused the remote forward" {
		t.Fatalf("refused remote: %v", err)
	}
	if err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": "0000000000000000"}, nil); err == nil || err.Error() != "tunnel not found" {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestTunnelEndedByServerChangeAndConnectionLoss(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	start := func() string {
		id := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": tunnelFreePort(t)})
		if err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id}, nil); err != nil {
			t.Fatal(err)
		}
		return id
	}
	waitErr := func(id, want string) {
		t.Helper()
		for {
			st := waitNote(t, fx.notes, "tunnels.state", id)
			if st["status"] == "error" {
				if st["error"] != want {
					t.Fatalf("error %v, want %s", st["error"], want)
				}
				return
			}
		}
	}
	id := start()
	fx.h.Registry().Close("fs") // the client dies
	waitErr(id, "connection lost")

	id = start()
	// Change the port in the vault: the hub ends the tunnel before closing the connection.
	if err := fx.c.Call(ctx, "servers.save", map[string]any{"original": "fs", "server": map[string]any{
		"name": "fs", "host": fx.srv.Host, "port": fx.srv.Port + 1, "user": "u", "auth": "agent", "keyPath": "", "aiVisible": false}}, nil); err != nil {
		t.Fatal(err)
	}
	waitErr(id, "server changed")
	// The tunnels are still saved on the server.
	if rows := listTunnels(t, fx); len(rows) != 2 {
		t.Fatalf("tunnels lost on save: %+v", rows)
	}
}

func TestTunnelStrictParamsAndValidation(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	for _, c := range []struct{ method, raw string }{
		{"tunnels.start", `{"server":"fs","ID":"x"}`},
		{"tunnels.start", `{"server":"fs","id":"x","id":"y"}`},
		{"tunnels.save", `{"server":"fs","tunnel":{"kind":"dynamic","listenPort":0}}`},
		{"tunnels.list", `{"server":"fs"}`},
	} {
		var re *rpc.Error
		if err := fx.c.Call(ctx, c.method, json.RawMessage(c.raw), nil); !errors.As(err, &re) || re.Code != -32602 {
			t.Errorf("%s %s: %v", c.method, c.raw, err)
		}
	}
	lp := tunnelFreePort(t)
	saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": lp})
	err := fx.c.Call(ctx, "tunnels.save", map[string]any{"server": "fs", "tunnel": map[string]any{"id": "", "kind": "dynamic", "listenPort": lp}}, nil)
	if err == nil || err.Error() != "another tunnel already listens on dynamic port "+strconv.Itoa(lp) {
		t.Fatalf("dup: %v", err)
	}
}
```

Notes for the implementer:
- `servers.save` with `auth: "agent"` and a new port is valid input. It changes the dial, so the hub ends tunnels. If `ServerInput.Validate` in this repo needs other keys, match the keys `hosts_test.go` sends.
- The `tunnels.save` validation case (`listenPort: 0`) must fail with code `-32602`. Validation errors are `&rpc.Error{Code: -32602, Message: err.Error()}`, as `servers.save` does.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/hub -run TestTunnel -count=1 2>&1 | tail -5`
Expected: FAIL with `method not found` (or a build error before the tests exist).

- [ ] **Step 3: Implement**

In `internal/broker/audit.go`, add this after `FileRecord`:

```go
// TunnelRecord is an audit line for a port forward: one at start, one at end.
type TunnelRecord struct {
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"`  // always "tunnel"
	Phase      string    `json:"phase"` // start, end
	Server     string    `json:"server"`
	Target     string    `json:"target"` // user@host:port
	ID         string    `json:"id"`
	TunnelKind string    `json:"tunnelKind"` // local, remote, dynamic
	Listen     string    `json:"listen"`
	To         string    `json:"to,omitempty"`
	Conns      int       `json:"conns,omitempty"` // end: connections served
	Reason     string    `json:"reason,omitempty"`
}
```

Add this beside `WriteFile`:

```go
func (a *Audit) WriteTunnel(r TunnelRecord) error {
	r.Kind = "tunnel"
	return a.append(r)
}
```

`internal/hub/tunnels.go`:

```go
package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshx"
	"github.com/lang315/sshgate/internal/tunnel"
)

// liveTunnel is a tunnel that is starting, running, or ended with an error
// (kept so tunnels.list can show why). A stopped tunnel has no entry.
type liveTunnel struct {
	server string
	def    config.Tunnel
	target string // user@host:port, for audit
	status string // starting, running, error
	err    string
	fwd    *tunnel.Forward
	notify *time.Timer // pending conns-only tunnels.state
}

type tunnelSet struct {
	mu      sync.Mutex
	live    map[string]*liveTunnel // server + "\x00" + id
	sink    func(tunnelState)
	sinkGen uint64
}

type tunnelState struct {
	Server string `json:"server"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Conns  int    `json:"conns"`
}

func newTunnelSet() *tunnelSet { return &tunnelSet{live: map[string]*liveTunnel{}} }

func tunnelKey(server, id string) string { return server + "\x00" + id }

func (lt *liveTunnel) state() tunnelState {
	st := tunnelState{Server: lt.server, ID: lt.def.ID, Status: lt.status, Error: lt.err}
	if lt.fwd != nil && lt.status == "running" {
		st.Conns = lt.fwd.Conns()
	}
	return st
}

// setSink works like the hub's setLockSink.
func (ts *tunnelSet) setSink(f func(tunnelState)) (release func()) {
	ts.mu.Lock()
	ts.sink = f
	ts.sinkGen++
	gen := ts.sinkGen
	ts.mu.Unlock()
	return func() {
		ts.mu.Lock()
		if ts.sinkGen == gen {
			ts.sink = nil
		}
		ts.mu.Unlock()
	}
}

func (ts *tunnelSet) emit(st tunnelState) {
	ts.mu.Lock()
	sink := ts.sink
	ts.mu.Unlock()
	if sink != nil {
		sink(st)
	}
}

// connsChanged sends at most one conns-only tunnels.state a second.
func (ts *tunnelSet) connsChanged(key string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	lt := ts.live[key]
	if lt == nil || lt.notify != nil {
		return
	}
	lt.notify = time.AfterFunc(time.Second, func() {
		ts.mu.Lock()
		cur := ts.live[key]
		if cur != lt {
			ts.mu.Unlock()
			return
		}
		lt.notify = nil
		st := lt.state()
		ts.mu.Unlock()
		ts.emit(st)
	})
}

// end stops lt if it is still the live entry for its key. reason "" means
// stopped by the author (entry removed); anything else leaves an error entry.
func (h *Hub) endTunnel(lt *liveTunnel, reason string) {
	ts := h.tunnels
	key := tunnelKey(lt.server, lt.def.ID)
	ts.mu.Lock()
	if ts.live[key] != lt || lt.status != "running" {
		ts.mu.Unlock()
		return
	}
	if lt.notify != nil {
		lt.notify.Stop()
		lt.notify = nil
	}
	if reason == "" {
		delete(ts.live, key)
		lt.status = "stopped"
	} else {
		lt.status, lt.err = "error", reason
	}
	st := lt.state()
	ts.mu.Unlock()
	lt.fwd.Close()
	auditReason := reason
	if auditReason == "" {
		auditReason = "stopped"
	}
	h.auditTunnel(broker.TunnelRecord{Phase: "end", Server: lt.server, Target: lt.target, ID: lt.def.ID,
		TunnelKind: lt.def.Kind, Listen: listenAddr(lt.def), To: toAddr(lt.def), Conns: lt.fwd.Total(), Reason: auditReason})
	ts.emit(st)
}

// endServer ends every running tunnel of server with reason. Callers run it
// before closing the server's connection, as with file jobs.
func (h *Hub) endServerTunnels(server, reason string) {
	h.tunnels.mu.Lock()
	var lts []*liveTunnel
	for _, lt := range h.tunnels.live {
		if lt.server == server {
			lts = append(lts, lt)
		}
	}
	h.tunnels.mu.Unlock()
	for _, lt := range lts {
		h.endTunnel(lt, reason)
	}
}

func (h *Hub) endAllTunnels(reason string) {
	h.tunnels.mu.Lock()
	lts := make([]*liveTunnel, 0, len(h.tunnels.live))
	for _, lt := range h.tunnels.live {
		lts = append(lts, lt)
	}
	h.tunnels.mu.Unlock()
	for _, lt := range lts {
		h.endTunnel(lt, reason)
	}
}

func (h *Hub) auditTunnel(r broker.TunnelRecord) {
	if h.audit != nil {
		r.Time = time.Now()
		_ = h.audit.WriteTunnel(r)
	}
}

func listenAddr(t config.Tunnel) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(t.ListenPort)) }

func toAddr(t config.Tunnel) string {
	if t.Kind == "dynamic" {
		return ""
	}
	return net.JoinHostPort(t.TargetHost, strconv.Itoa(t.TargetPort))
}

func newTunnelID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// savedTunnel finds server's tunnel id in the loaded store.
func (h *Hub) savedTunnel(server, id string) (config.Tunnel, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deps.File == nil {
		return config.Tunnel{}, false
	}
	for _, s := range h.deps.File.Servers {
		if s.Name == server {
			for _, t := range s.Tunnels {
				if t.ID == id {
					return t, true
				}
			}
		}
	}
	return config.Tunnel{}, false
}

type tunnelView struct {
	Server string `json:"server"`
	config.Tunnel
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Conns  int    `json:"conns"`
}

func (h *Hub) listTunnels() []tunnelView {
	out := []tunnelView{}
	h.mu.Lock()
	var defs []tunnelView
	if h.deps.File != nil && h.deps.File.KDF != nil {
		for _, s := range h.deps.File.Servers {
			for _, t := range s.Tunnels {
				defs = append(defs, tunnelView{Server: s.Name, Tunnel: t, Status: "stopped"})
			}
		}
	}
	h.mu.Unlock()
	h.tunnels.mu.Lock()
	defer h.tunnels.mu.Unlock()
	for _, v := range defs {
		if lt := h.tunnels.live[tunnelKey(v.Server, v.ID)]; lt != nil {
			st := lt.state()
			v.Status, v.Error, v.Conns = st.Status, st.Error, st.Conns
		}
		out = append(out, v)
	}
	return out
}

func (h *Hub) running(server, id string) bool {
	h.tunnels.mu.Lock()
	defer h.tunnels.mu.Unlock()
	lt := h.tunnels.live[tunnelKey(server, id)]
	return lt != nil && (lt.status == "running" || lt.status == "starting")
}

// SaveTunnel adds (empty ID) or replaces one of server's tunnels.
// ponytail: the running check and the write are not atomic with a
// concurrent tunnels.start of the same id; one author, one window.
func (h *Hub) SaveTunnel(server string, t config.Tunnel) (config.Tunnel, error) {
	key, err := h.writeKey()
	if err != nil {
		return t, err
	}
	defer clear(key)
	if t.ID != "" && h.running(server, t.ID) {
		return t, errors.New("stop the tunnel first")
	}
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		i := slices.IndexFunc(f.Servers, func(s config.Server) bool { return s.Name == server })
		if i < 0 {
			return serverNotFound(server)
		}
		s := &f.Servers[i]
		for _, o := range s.Tunnels {
			if o.ID != t.ID && o.Kind == t.Kind && o.ListenPort == t.ListenPort {
				return fmt.Errorf("another tunnel already listens on %s port %d", t.Kind, t.ListenPort)
			}
		}
		if t.ID == "" {
			if len(s.Tunnels) >= config.MaxTunnels {
				return errors.New("a server holds at most 32 tunnels")
			}
			t.ID = newTunnelID()
			s.Tunnels = append(s.Tunnels, t)
			return nil
		}
		j := slices.IndexFunc(s.Tunnels, func(o config.Tunnel) bool { return o.ID == t.ID })
		if j < 0 {
			return errors.New("tunnel not found")
		}
		s.Tunnels[j] = t
		return nil
	})
	if err != nil {
		return t, err
	}
	reloadErr := h.Reload()
	h.tunnels.mu.Lock()
	delete(h.tunnels.live, tunnelKey(server, t.ID)) // an old error entry
	h.tunnels.mu.Unlock()
	h.auditConfig(broker.ConfigRecord{Action: "tunnelSave", Server: server,
		Changed: []string{fmt.Sprintf("%s %s %s → %s", t.ID, t.Kind, listenAddr(t), toAddr(t))}})
	return t, reloadErr
}

func (h *Hub) DeleteTunnel(server, id string) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	h.tunnels.mu.Lock()
	lt := h.tunnels.live[tunnelKey(server, id)]
	h.tunnels.mu.Unlock()
	if lt != nil {
		h.endTunnel(lt, "")
	}
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		i := slices.IndexFunc(f.Servers, func(s config.Server) bool { return s.Name == server })
		if i < 0 {
			return serverNotFound(server)
		}
		s := &f.Servers[i]
		j := slices.IndexFunc(s.Tunnels, func(o config.Tunnel) bool { return o.ID == id })
		if j < 0 {
			return errors.New("tunnel not found")
		}
		s.Tunnels = slices.Delete(s.Tunnels, j, j+1)
		return nil
	})
	if err != nil {
		return err
	}
	reloadErr := h.Reload()
	h.tunnels.mu.Lock()
	delete(h.tunnels.live, tunnelKey(server, id))
	h.tunnels.mu.Unlock()
	h.auditConfig(broker.ConfigRecord{Action: "tunnelDelete", Server: server, Changed: []string{id}})
	return reloadErr
}

// StartTunnel checks, in order: vault, unlocked, server, tunnel, pin. It
// answers once the forward listens.
func (h *Hub) StartTunnel(server, id string) error {
	_ = h.Reload()
	dc, err := h.resolveForTerm(server)
	if err != nil {
		return err
	}
	def, ok := h.savedTunnel(server, id)
	if !ok {
		return errors.New("tunnel not found")
	}
	if dc.HostKey == "" {
		return errors.New("open a terminal to this host once to trust its host key")
	}
	key := tunnelKey(server, id)
	lt := &liveTunnel{server: server, def: def, target: fmt.Sprintf("%s@%s:%d", dc.User, dc.Host, dc.Port), status: "starting"}
	h.tunnels.mu.Lock()
	if cur := h.tunnels.live[key]; cur != nil && (cur.status == "running" || cur.status == "starting") {
		h.tunnels.mu.Unlock()
		return errors.New("the tunnel is already running")
	}
	h.tunnels.live[key] = lt
	h.tunnels.mu.Unlock()
	h.tunnels.emit(lt.state())

	fail := func(msg string, detail error) error {
		if detail != nil {
			fmt.Fprintf(os.Stderr, "tunnel %s/%s: %v\n", server, id, detail)
		}
		h.tunnels.mu.Lock()
		if h.tunnels.live[key] == lt {
			lt.status, lt.err = "error", msg
		}
		st := lt.state()
		h.tunnels.mu.Unlock()
		h.tunnels.emit(st)
		return errors.New(msg)
	}
	mgr := h.Registry().Get(server, dc)
	mgr.StartKeepalive(30*time.Second, nil)
	client, err := mgr.Client()
	var unknown *sshx.HostKeyUnknownError
	switch {
	case errors.As(err, &unknown):
		return fail("open a terminal to this host once to trust its host key", err)
	case errors.Is(err, sshx.ErrHostKeyMismatch):
		return fail("host key changed", err)
	case err != nil:
		return fail("connection failed", err)
	}
	onConns := func() { h.tunnels.connsChanged(key) }
	var fwd *tunnel.Forward
	switch def.Kind {
	case "local":
		fwd, err = tunnel.Local(client, listenAddr(def), toAddr(def), onConns)
	case "remote":
		fwd, err = tunnel.Remote(client, listenAddr(def), toAddr(def), onConns)
	default:
		fwd, err = tunnel.Dynamic(client, listenAddr(def), onConns)
	}
	switch {
	case err != nil && errors.Is(err, syscall.EADDRINUSE):
		return fail(fmt.Sprintf("port %d is already in use", def.ListenPort), err)
	case err != nil && def.Kind == "remote":
		return fail("the server refused the remote forward", err)
	case err != nil:
		return fail(err.Error(), err)
	}
	h.tunnels.mu.Lock()
	if h.tunnels.live[key] != lt { // deleted or replaced meanwhile
		h.tunnels.mu.Unlock()
		fwd.Close()
		return errors.New("tunnel not found")
	}
	lt.fwd, lt.status = fwd, "running"
	st := lt.state()
	h.tunnels.mu.Unlock()
	h.auditTunnel(broker.TunnelRecord{Phase: "start", Server: server, Target: lt.target, ID: id,
		TunnelKind: def.Kind, Listen: listenAddr(def), To: toAddr(def)})
	h.tunnels.emit(st)
	go func() { client.Wait(); h.endTunnel(lt, "connection lost") }()
	go func() {
		<-fwd.Done()
		if fwd.Err() != nil {
			h.endTunnel(lt, "connection lost")
		}
	}()
	return nil
}

func (h *Hub) StopTunnel(server, id string) {
	h.tunnels.mu.Lock()
	lt := h.tunnels.live[tunnelKey(server, id)]
	h.tunnels.mu.Unlock()
	if lt != nil {
		h.endTunnel(lt, "")
	}
}

// registerTunnelMethods adds tunnels.* to one UI door. Running tunnels are
// the hub's, not the door's: closing the door leaves them running.
func registerTunnelMethods(s *rpc.Server, h *Hub) (release func()) {
	release = h.tunnels.setSink(func(st tunnelState) { s.Notify("tunnels.state", st) })
	type ref struct {
		Server string `json:"server"`
		ID     string `json:"id"`
	}
	s.HandleRequest("tunnels.list", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct{}
		if err := strictParams(raw, &p); err != nil {
			return nil, err
		}
		_ = h.Reload()
		return h.listTunnels(), nil
	})
	s.HandleRequest("tunnels.save", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			Server string          `json:"server"`
			Tunnel json.RawMessage `json:"tunnel"`
		}
		if err := strictParams(raw, &p, "server", "tunnel"); err != nil {
			return nil, err
		}
		var t config.Tunnel
		if err := strictParams(p.Tunnel, &t, "id", "kind", "listenPort", "targetHost", "targetPort", "label"); err != nil {
			return nil, err
		}
		if err := t.Validate(); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: err.Error()}
		}
		return h.SaveTunnel(p.Server, t)
	})
	s.HandleRequest("tunnels.delete", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p ref
		if err := strictParams(raw, &p, "server", "id"); err != nil {
			return nil, err
		}
		return map[string]any{}, h.DeleteTunnel(p.Server, p.ID)
	})
	s.HandleRequest("tunnels.start", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p ref
		if err := strictParams(raw, &p, "server", "id"); err != nil {
			return nil, err
		}
		return map[string]any{}, h.StartTunnel(p.Server, p.ID)
	})
	s.Handle("tunnels.stop", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p ref
		if err := strictParams(raw, &p, "server", "id"); err != nil {
			return nil, err
		}
		h.StopTunnel(p.Server, p.ID)
		return map[string]any{}, nil
	})
	return release
}
```

Before relying on the code above, check these points in the codebase:
- **`strictParams` with no allowed keys** must accept `{}` and refuse `{"server":"fs"}`. It does, because `slices.Contains(nil, k)` is false.
- **Where the "locked" text comes from.** `resolveForTerm` returns `ErrLocked` while locked. `writeKey` returns `ErrLocked` for save and delete. The test checks that the error text contains `locked`; confirm `ErrLocked`'s message contains it.
- **Lock timing in `endTunnel`.** It must not hold `ts.mu` while calling `fwd.Close()` or `emit`. `emit` calls `s.Notify`, which writes to the door.
- **`EADDRINUSE` on Windows.** It is `WSAEADDRINUSE`, and `errors.Is(err, syscall.EADDRINUSE)` may not match there. That is acceptable: the fallback message is the OS text. Add a `ponytail:` comment saying so.

Wiring:
- `hub.go`:
  - add `tunnels *tunnelSet // every running tunnel; see tunnels.go` to `Hub`;
  - add `tunnels: newTunnelSet()` in `New`;
  - `Close` becomes `func (h *Hub) Close() { h.closeOnce.Do(func() { h.endAllTunnels("hub stopped"); close(h.done) }) }`.
- `hosts.go`: add `h.endServerTunnels(name, "server changed")` next to each `h.files.endServer(name, "server changed")` (3 places, always before `h.reg.Close`).
- `uidoor.go`: after `closeFiles := ...`, add:
  ```go
  releaseTunnels := registerTunnelMethods(s, h)
  defer releaseTunnels()
  ```
- `idle.go`: `ProtocolVersion = 5`. Run `grep -rn "ProtocolVersion\|\"protocol\"" internal cmd` and update any test expecting 4.
- Protocol doc comment: add the `tunnels.*` methods to the list on `ServeUIDoor` / `uidoor.go` if it enumerates them.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/hub -run TestTunnel -race -count=3 && go test -race ./... && go vet ./...`
Expected: all `ok`, and `go vet` clean.

- [ ] **Step 5: Commit**

```bash
git add internal/broker internal/hub
git commit -m "feat(hub): tunnels.* on the UI door, protocol 5"
```

---

### Task 5: Renderer plumbing and pure tunnel helpers

**Files:**
- Modify: `desktop/src/shared/protocol.ts`, `desktop/src/renderer/transport.ts`
- Create: `desktop/src/renderer/tunnels.ts`, `desktop/test/tunnels.test.ts`
- Check: `desktop/test/*` and `desktop/src/main/*` tests that snapshot `REQUEST_METHODS` / `NOTIFY_METHODS` / `PROTOCOL_VERSION`; update them.

**Interfaces:**
- Consumes: the hub shapes from Task 4.
- Produces (TS):

```ts
// protocol.ts
export type TunnelKind = 'local' | 'remote' | 'dynamic'
export interface Tunnel { id: string; kind: TunnelKind; listenPort: number; targetHost?: string; targetPort?: number; label?: string }
export type TunnelStatus = 'stopped' | 'starting' | 'running' | 'error'
export interface TunnelState { server: string; id: string; status: TunnelStatus; error?: string; conns: number }
export type TunnelView = Tunnel & TunnelState
// HubEvent gains: | { method: 'tunnels.state'; params: TunnelState }
// REQUEST_METHODS gains 'tunnels.list', 'tunnels.save', 'tunnels.delete', 'tunnels.start'
// NOTIFY_METHODS gains 'tunnels.stop'
// PROTOCOL_VERSION = 5
// transport.ts hub.*:
tunnelsList: () => Promise<TunnelView[]>
tunnelsSave: (server: string, t: Tunnel) => Promise<Tunnel>
tunnelsDelete: (server: string, id: string) => Promise<void>
tunnelsStart: (server: string, id: string) => Promise<void>
tunnelsStop: (server: string, id: string) => void
// tunnels.ts
export interface TunnelForm { kind: TunnelKind; listenPort: string; targetHost: string; targetPort: string; label: string }
export function summary(t: Tunnel): string
export function parseTunnelForm(f: TunnelForm, id: string): { ok: true; tunnel: Tunnel } | { ok: false; error: string }
export function formFor(t?: Tunnel): TunnelForm
export function applyState(list: TunnelView[], s: TunnelState): TunnelView[]
export function runningCount(list: TunnelView[], server: string): number
```

- [ ] **Step 1: Write the failing test**

`desktop/test/tunnels.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { applyState, formFor, parseTunnelForm, runningCount, summary } from '../src/renderer/tunnels'
import type { TunnelView } from '../src/shared/protocol'

describe('summary', () => {
  it('describes each kind', () => {
    expect(summary({ id: 'a', kind: 'local', listenPort: 5433, targetHost: 'db', targetPort: 5432 })).toBe('L 127.0.0.1:5433 → db:5432')
    expect(summary({ id: 'a', kind: 'remote', listenPort: 8080, targetHost: 'localhost', targetPort: 3000 })).toBe('R server 127.0.0.1:8080 → localhost:3000')
    expect(summary({ id: 'a', kind: 'dynamic', listenPort: 1080 })).toBe('D 127.0.0.1:1080 SOCKS5')
    expect(summary({ id: 'a', kind: 'local', listenPort: 1, targetHost: '::1', targetPort: 2 })).toBe('L 127.0.0.1:1 → [::1]:2')
  })
})

describe('parseTunnelForm', () => {
  const base = { kind: 'local' as const, listenPort: '5433', targetHost: 'db', targetPort: '5432', label: '' }
  it('accepts a local tunnel and trims', () => {
    expect(parseTunnelForm({ ...base, targetHost: ' db ', label: ' pg ' }, '')).toEqual(
      { ok: true, tunnel: { id: '', kind: 'local', listenPort: 5433, targetHost: 'db', targetPort: 5432, label: 'pg' } })
  })
  it('drops the target for dynamic', () => {
    expect(parseTunnelForm({ ...base, kind: 'dynamic' }, 'x')).toEqual(
      { ok: true, tunnel: { id: 'x', kind: 'dynamic', listenPort: 5433, targetHost: '', targetPort: 0, label: '' } })
  })
  it.each([
    [{ listenPort: '0' }, 'Listen port must be 1-65535'],
    [{ listenPort: '5x' }, 'Listen port must be 1-65535'],
    [{ targetHost: '' }, 'Target host is required, with no spaces'],
    [{ targetHost: 'a b' }, 'Target host is required, with no spaces'],
    [{ targetPort: '70000' }, 'Target port must be 1-65535'],
    [{ label: 'x'.repeat(65) }, 'Label is at most 64 characters'],
  ])('refuses %o', (patch, error) => {
    expect(parseTunnelForm({ ...base, ...patch }, '')).toEqual({ ok: false, error })
  })
  it('round-trips formFor', () => {
    expect(formFor({ id: 'i', kind: 'remote', listenPort: 1, targetHost: 'h', targetPort: 2, label: 'l' }))
      .toEqual({ kind: 'remote', listenPort: '1', targetHost: 'h', targetPort: '2', label: 'l' })
    expect(formFor()).toEqual({ kind: 'local', listenPort: '', targetHost: 'localhost', targetPort: '', label: '' })
  })
})

describe('state', () => {
  const list: TunnelView[] = [
    { server: 's', id: 'a', kind: 'dynamic', listenPort: 1, status: 'stopped', conns: 0 },
    { server: 's', id: 'b', kind: 'dynamic', listenPort: 2, status: 'running', conns: 1 },
    { server: 't', id: 'a', kind: 'dynamic', listenPort: 3, status: 'running', conns: 0 },
  ]
  it('applies a state to the matching row only', () => {
    const next = applyState(list, { server: 's', id: 'a', status: 'error', error: 'port 1 is already in use', conns: 0 })
    expect(next[0]).toMatchObject({ status: 'error', error: 'port 1 is already in use' })
    expect(next[2]).toBe(list[2])
    expect(applyState(list, { server: 'x', id: 'a', status: 'running', conns: 0 })).toBe(list)
  })
  it('clears a stale error on a new status', () => {
    const e = applyState(list, { server: 's', id: 'a', status: 'error', error: 'boom', conns: 0 })
    expect(applyState(e, { server: 's', id: 'a', status: 'running', conns: 0 })[0].error).toBeUndefined()
  })
  it('counts running per server', () => {
    expect(runningCount(list, 's')).toBe(1)
    expect(runningCount(list, 'u')).toBe(0)
  })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd desktop && npx vitest run test/tunnels.test.ts`
Expected: FAIL, `Failed to resolve import "../src/renderer/tunnels"`.

- [ ] **Step 3: Implement**

In `protocol.ts`, add the types listed in Interfaces after `FileGrant`, add the `tunnels.state` member to `HubEvent`, extend the two method arrays, and set `PROTOCOL_VERSION = 5`.

In `transport.ts`, add these to `hub` (import the new types):

```ts
  tunnelsList: () => call<TunnelView[]>('tunnels.list', {}),
  tunnelsSave: (server: string, tunnel: Tunnel) => call<Tunnel>('tunnels.save', {
    server, tunnel: { id: tunnel.id, kind: tunnel.kind, listenPort: tunnel.listenPort,
      targetHost: tunnel.targetHost ?? '', targetPort: tunnel.targetPort ?? 0, label: tunnel.label ?? '' },
  }),
  tunnelsDelete: async (server: string, id: string) => { await call('tunnels.delete', { server, id }) },
  tunnelsStart: async (server: string, id: string) => { await call('tunnels.start', { server, id }) },
  tunnelsStop: (server: string, id: string) => bridge().notify('tunnels.stop', { server, id }),
```

`desktop/src/renderer/tunnels.ts`:

```ts
import type { Tunnel, TunnelKind, TunnelState, TunnelView } from '../shared/protocol'

export interface TunnelForm { kind: TunnelKind; listenPort: string; targetHost: string; targetPort: string; label: string }

const hostPort = (h: string, p: number) => (h.includes(':') ? `[${h}]:${p}` : `${h}:${p}`)

export function summary(t: Tunnel): string {
  const listen = `127.0.0.1:${t.listenPort}`
  switch (t.kind) {
    case 'local': return `L ${listen} → ${hostPort(t.targetHost ?? '', t.targetPort ?? 0)}`
    case 'remote': return `R server ${listen} → ${hostPort(t.targetHost ?? '', t.targetPort ?? 0)}`
    default: return `D ${listen} SOCKS5`
  }
}

const port = (s: string) => (/^\d{1,5}$/.test(s.trim()) && +s >= 1 && +s <= 65535 ? +s : 0)

// Mirrors config.Tunnel.Validate; the hub checks again.
export function parseTunnelForm(f: TunnelForm, id: string): { ok: true; tunnel: Tunnel } | { ok: false; error: string } {
  const listenPort = port(f.listenPort)
  if (!listenPort) return { ok: false, error: 'Listen port must be 1-65535' }
  const label = f.label.trim()
  if (label.length > 64) return { ok: false, error: 'Label is at most 64 characters' }
  if (f.kind === 'dynamic') return { ok: true, tunnel: { id, kind: 'dynamic', listenPort, targetHost: '', targetPort: 0, label } }
  const targetHost = f.targetHost.trim()
  if (!targetHost || /\s/.test(targetHost) || targetHost.length > 253) return { ok: false, error: 'Target host is required, with no spaces' }
  const targetPort = port(f.targetPort)
  if (!targetPort) return { ok: false, error: 'Target port must be 1-65535' }
  return { ok: true, tunnel: { id, kind: f.kind, listenPort, targetHost, targetPort, label } }
}

export function formFor(t?: Tunnel): TunnelForm {
  if (!t) return { kind: 'local', listenPort: '', targetHost: 'localhost', targetPort: '', label: '' }
  return { kind: t.kind, listenPort: String(t.listenPort), targetHost: t.targetHost ?? '',
    targetPort: t.targetPort ? String(t.targetPort) : '', label: t.label ?? '' }
}

export function applyState(list: TunnelView[], s: TunnelState): TunnelView[] {
  const i = list.findIndex((t) => t.server === s.server && t.id === s.id)
  if (i < 0) return list
  const next = list.slice()
  next[i] = { ...list[i], status: s.status, error: s.error, conns: s.conns }
  return next
}

export const runningCount = (list: TunnelView[], server: string) =>
  list.filter((t) => t.server === server && t.status === 'running').length
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd desktop && npm run typecheck && npm test`
Expected: typecheck exits 0, and every vitest passes (the previous 160 plus the new ones).

- [ ] **Step 5: Commit**

```bash
git add desktop/src/shared/protocol.ts desktop/src/renderer/transport.ts desktop/src/renderer/tunnels.ts desktop/test
git commit -m "feat(desktop): tunnels protocol types, transport, pure helpers"
```

---

### Task 6: Tunnels tab, dialog, host-card button and badge

**Files:**
- Create: `desktop/src/renderer/TunnelsView.tsx`, `desktop/src/renderer/TunnelDialog.tsx`
- Modify:
  - `terminals.ts` (`kind: 'term' | 'files' | 'tunnels'`, plus `findKind`);
  - `TerminalTabs.tsx` (`openTunnels`, rendering);
  - `HostList.tsx` (button and badge);
  - `App.tsx` (tunnel state);
  - `icons.tsx` (`TunnelIcon`);
  - `styles.css` (tokens only; no raw colours).

**Interfaces:**
- Consumes: `hub.tunnels*`, and `summary`, `parseTunnelForm`, `formFor`, `applyState`, `runningCount` (Task 5).
- Produces, relied on by the e2e in Task 7:
  - the host card button `aria-label="Tunnels <name>"`;
  - the badge `.hostcard .chip.tunnels` with text `<n> ⇄`, shown only when n > 0;
  - the tab `data-kind="tunnels"`;
  - `TunnelsView` root `role="region" aria-label="Tunnels on <name>"`, containing:
    - a `table` with one `tr[data-id]` per tunnel, each with a `td.status` (text `running`, `stopped`, `starting`, or the error) and a `td.conns`;
    - buttons named `Start`, `Stop`, `Edit`, `Delete` per row, and `Add tunnel` in the toolbar;
  - the dialog `role="dialog"` named `Add tunnel` or `Edit tunnel`, with:
    - radios `Local`, `Remote`, `Dynamic`;
    - inputs labelled `Listen port`, `Target host`, `Target port`, `Label`;
    - buttons `Save` and `Cancel`;
    - an error in `role="alert"`.

- [ ] **Step 1: Implement `terminals.ts`**

- Change the `kind` union to `'term' | 'files' | 'tunnels'`.
- In `open`, the initial `state` is `'open'` for both `'files'` and `'tunnels'`: `state: kind === 'term' ? 'opening' : 'open'`.
- Add:

```ts
  // One Tunnels tab per host: the existing one, if any.
  findKind(server: string, kind: Tab['kind']): Tab | undefined {
    return this.tabs.find((t) => t.server === server && t.kind === kind)
  }
```

Add a vitest case to `desktop/test/terminals.test.ts` (or whichever file tests `TabSet`; find it with `grep -rln TabSet desktop/test`):

```ts
it('finds a tab by server and kind', () => {
  const s = new TabSet()
  const a = s.open('x', 'tunnels')
  s.open('x')
  expect(s.findKind('x', 'tunnels')).toBe(a)
  expect(s.findKind('y', 'tunnels')).toBeUndefined()
  expect(a.state).toBe('open')
})
```

- [ ] **Step 2: Implement `TunnelDialog.tsx`**

```tsx
import { useState } from 'react'
import type { Tunnel, TunnelKind } from '../shared/protocol'
import { formFor, parseTunnelForm, type TunnelForm } from './tunnels'

export function TunnelDialog({ server, tunnel, onSave, onClose }: {
  server: string; tunnel?: Tunnel; onSave: (t: Tunnel) => Promise<void>; onClose: () => void
}) {
  const [f, setF] = useState<TunnelForm>(() => formFor(tunnel))
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const title = tunnel ? 'Edit tunnel' : 'Add tunnel'
  const set = (patch: Partial<TunnelForm>) => setF((cur) => ({ ...cur, ...patch }))
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const r = parseTunnelForm(f, tunnel?.id ?? '')
    if (!r.ok) { setError(r.error); return }
    setBusy(true)
    try { await onSave(r.tunnel) } catch (err) { setError((err as Error).message); setBusy(false) }
  }
  const kinds: [TunnelKind, string, string][] = [
    ['local', 'Local', 'A port here reaches a host:port as seen from the server'],
    ['remote', 'Remote', 'A port on the server reaches a host:port as seen from here'],
    ['dynamic', 'Dynamic', 'A SOCKS5 proxy here; connections leave from the server'],
  ]
  return (
    <div className="modal-backdrop">
      <form className="dialog" role="dialog" aria-modal="true" aria-label={title} onSubmit={submit}
        onKeyDown={(e) => { if (e.key === 'Escape') onClose() }}>
        <h2>{title}</h2>
        <p className="muted">{`On ${server}. Listens on 127.0.0.1 only.`}</p>
        <fieldset className="kinds">
          <legend>Kind</legend>
          {kinds.map(([k, name, hint]) => (
            <label key={k} title={hint}>
              <input type="radio" name="kind" checked={f.kind === k} onChange={() => set({ kind: k })} />{name}
            </label>
          ))}
        </fieldset>
        <label>Listen port<input inputMode="numeric" autoFocus value={f.listenPort} onChange={(e) => set({ listenPort: e.target.value })} /></label>
        {f.kind !== 'dynamic' && (
          <div className="row">
            <label>Target host<input value={f.targetHost} onChange={(e) => set({ targetHost: e.target.value })} /></label>
            <label>Target port<input inputMode="numeric" value={f.targetPort} onChange={(e) => set({ targetPort: e.target.value })} /></label>
          </div>
        )}
        <label>Label<input value={f.label} onChange={(e) => set({ label: e.target.value })} /></label>
        {error && <p className="error" role="alert">{error}</p>}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn primary" disabled={busy}>Save</button>
        </div>
      </form>
    </div>
  )
}
```

Before writing this, open `FileDialogs.tsx` and reuse its dialog markup and class names (backdrop, dialog, actions, error) wherever they differ from the ones above. The Files dialogs are the pattern to match; the class names above are placeholders for theirs.

- [ ] **Step 3: Implement `TunnelsView.tsx`**

```tsx
import { useState } from 'react'
import type { Tunnel, TunnelView } from '../shared/protocol'
import { displayText } from '../shared/display'
import { hub } from './transport'
import { summary } from './tunnels'
import { TunnelDialog } from './TunnelDialog'

export function TunnelsView({ server, visible, tunnels, locked, onChanged }: {
  server: string; visible: boolean; tunnels: TunnelView[]; locked: boolean; onChanged: () => void
}) {
  const [editing, setEditing] = useState<{ tunnel?: Tunnel }>()
  const [error, setError] = useState<string>()
  const mine = tunnels.filter((t) => t.server === server)
  const live = (t: TunnelView) => t.status === 'running' || t.status === 'starting'
  const act = async (fn: () => Promise<void>) => {
    setError(undefined)
    try { await fn() } catch (e) { setError((e as Error).message) }
  }
  return (
    <section className="tunnels" role="region" aria-label={`Tunnels on ${server}`} style={{ display: visible ? 'flex' : 'none' }}>
      <div className="toolbar">
        <button type="button" className="btn" disabled={locked} onClick={() => setEditing({})}>Add tunnel</button>
      </div>
      {error && <p className="error" role="alert">{displayText(error)}</p>}
      {mine.length === 0 ? (
        <p className="empty">No tunnels yet. Add one to reach a port on or through this host.</p>
      ) : (
        <table>
          <thead><tr><th>Label</th><th>Forward</th><th>Status</th><th>Connections</th><th /></tr></thead>
          <tbody>
            {mine.map((t) => (
              <tr key={t.id} data-id={t.id} data-status={t.status}>
                <td>{displayText(t.label ?? '')}</td>
                <td className="mono">{displayText(summary(t))}</td>
                <td className="status">{t.status === 'error' ? displayText(t.error ?? 'error') : t.status}</td>
                <td className="conns">{t.status === 'running' ? t.conns : ''}</td>
                <td className="rowactions">
                  {live(t) ? (
                    <button type="button" className="btn" onClick={() => hub.tunnelsStop(server, t.id)}>Stop</button>
                  ) : (
                    <button type="button" className="btn" disabled={locked} onClick={() => act(() => hub.tunnelsStart(server, t.id))}>Start</button>
                  )}
                  <button type="button" className="btn" disabled={locked || live(t)} title={live(t) ? 'Stop it first' : undefined}
                    onClick={() => setEditing({ tunnel: t })}>Edit</button>
                  <button type="button" className="btn" disabled={locked || live(t)} title={live(t) ? 'Stop it first' : undefined}
                    onClick={() => act(async () => { await hub.tunnelsDelete(server, t.id); onChanged() })}>Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <p className="muted">While a tunnel runs, any program on this computer can use its local port, and any program on the server can use a remote tunnel’s port.</p>
      {editing && (
        <TunnelDialog server={server} tunnel={editing.tunnel} onClose={() => setEditing(undefined)}
          onSave={async (t) => { await hub.tunnelsSave(server, t); setEditing(undefined); onChanged() }} />
      )}
    </section>
  )
}
```

- [ ] **Step 4: Wire `TerminalTabs.tsx`, `HostList.tsx`, `App.tsx`, and `icons.tsx`**

- `icons.tsx`: add `export const TunnelIcon = icon(<><path d="M4 8h13l-3-3" /><path d="M20 16H7l3 3" /></>)`.
- `TerminalTabs.tsx`:
  - Add the props `tunnels: TunnelView[]` and `locked: boolean`. Add `onTunnelsChanged: () => void` to the component's props and `openTunnels(server: string): void` to `TerminalsHandle`, implemented as:
    ```ts
    openTunnels(server) { const t = tabs.findKind(server, 'tunnels'); if (t) tabs.activate(t.id); else tabs.open(server, 'tunnels'); changed() },
    ```
  - Tab label icon: `t.kind === 'files' ? <FolderIcon /> : t.kind === 'tunnels' ? <TunnelIcon /> : <span className="dot" … />`.
  - The render branch: `t.kind === 'tunnels' ? <TunnelsView key={t.id} server={t.server} visible={t.id === tabs.active} tunnels={tunnels} locked={locked} onChanged={onTunnelsChanged} /> : t.kind === 'files' ? … : …`.
- `HostList.tsx`:
  - Add props `onTunnels: (name: string) => void` and `tunnels: TunnelView[]`.
  - In `hostcard-name`, after the `New key` chip: `{runningCount(tunnels, s.name) > 0 && <span className="chip tunnels" title="Running tunnels">{`${runningCount(tunnels, s.name)} ⇄`}</span>}`.
  - In `hostcard-actions`, after Files: `<button type="button" className="icon" aria-label={`Tunnels ${s.name}`} title="Tunnels" onClick={() => onTunnels(s.name)}><TunnelIcon /></button>`.
- `App.tsx`:
  - `const [tunnels, setTunnels] = useState<TunnelView[]>([])`.
  - `const reloadTunnels = useCallback(() => hub.tunnelsList().then(setTunnels).catch(() => {}), [])`.
  - Call `reloadTunnels()` in the same effect that calls `reloadServers()` when `screen.kind === 'ready'`, and keep the list while locked (do not clear it in the `else`; the locked screen hides it anyway). When `hubState.kind !== 'running'`, `setTunnels([])`.
  - Subscribe: `useEffect(() => hub.onEvent((e) => { if (e.method === 'tunnels.state') setTunnels((cur) => applyState(cur, e.params)) }), [])`.
  - Pass `tunnels` and `onTunnels={(name) => terms.current?.openTunnels(name)}` to `HostList`, and `tunnels={tunnels} locked={!!status?.locked} onTunnelsChanged={reloadTunnels}` to `Terminals`.
  - After a host save or delete (`reloadServers` call sites in `onSave` and `deleteHost`), also call `reloadTunnels()`.
- `styles.css`: `.tunnels` gets the same layout as the Files view root (flex column, gap, padding; copy the Files rule's tokens). Add `.hostcard .chip.tunnels { color: var(--ok) }`, or the existing token used for success/online; check `:root` for its name and use a token only. Style the table like the Files grid header (tokens only).

- [ ] **Step 5: Verify**

Run: `cd desktop && npm run typecheck && npm test && npm run build`
Expected: all exit 0. Then run `npm start` against `sshtestd` (see CLAUDE.md, `go run ./internal/sshx/sshtest/sshtestd -write-store=... -password=pw`). Open Tunnels on `box`, add a dynamic tunnel, start it, and see `running` and the card badge `1 ⇄`. Take one screenshot for the review.

- [ ] **Step 6: Commit**

```bash
git add desktop/src desktop/test
git commit -m "feat(desktop): Tunnels tab per host with add/edit/delete/start/stop and a running badge"
```

---

### Task 7: e2e: tunnels through the app against sshtestd

**Files:**
- Create: `desktop/e2e/tunnels.spec.ts`
- Modify: `CLAUDE.md` (the e2e list in the `npm run e2e` comment gains `tunnels`)

**Interfaces:**
- Consumes: the Task 6 locators; `launch`, `unlock` (`e2e/launch.ts`); sshtestd forwarding (Task 2).

- [ ] **Step 1: Write the test**

```ts
import { test, expect, type Page } from '@playwright/test'
import * as net from 'node:net'
import { launch, unlock, type Launched } from './launch'

// Spec 3b §Testing: local and dynamic tunnels through the app, surviving a lock.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')

let l: Launched
let echoPort = 0
let echo: net.Server
test.beforeAll(async () => {
  echo = net.createServer((c) => c.pipe(c))
  await new Promise<void>((r) => echo.listen(0, '127.0.0.1', r))
  echoPort = (echo.address() as net.AddressInfo).port
  l = await launch()
})
test.afterAll(async () => { await l?.close(); echo?.close() })

const freePort = () => new Promise<number>((resolve) => {
  const s = net.createServer().listen(0, '127.0.0.1', () => { const p = (s.address() as net.AddressInfo).port; s.close(() => resolve(p)) })
})

// Sends "ping" on c and waits for it back.
const ping = (c: net.Socket) => new Promise<void>((resolve, reject) => {
  c.once('data', (d) => (d.toString() === 'ping' ? resolve() : reject(new Error(`got ${d}`))))
  c.once('error', reject)
  c.write('ping')
})
const connect = (port: number) => new Promise<net.Socket>((resolve, reject) => {
  const c = net.connect(port, '127.0.0.1', () => resolve(c)); c.once('error', reject)
})
// A SOCKS5 CONNECT to 127.0.0.1:target through the proxy on port.
async function socks(port: number, target: number): Promise<net.Socket> {
  const c = await connect(port)
  const read = (n: number) => new Promise<Buffer>((resolve) => {
    let buf = Buffer.alloc(0)
    const on = (d: Buffer) => { buf = Buffer.concat([buf, d]); if (buf.length >= n) { c.off('data', on); resolve(buf) } }
    c.on('data', on)
  })
  c.write(Buffer.from([5, 1, 0]))
  expect([...(await read(2))]).toEqual([5, 0])
  c.write(Buffer.from([5, 1, 0, 1, 127, 0, 0, 1, target >> 8, target & 0xff]))
  expect((await read(10))[1]).toBe(0)
  return c
}

async function addTunnel(win: Page, kind: 'Local' | 'Dynamic', listen: number, target?: number) {
  await win.getByRole('button', { name: 'Add tunnel' }).click()
  const d = win.getByRole('dialog', { name: 'Add tunnel' })
  await d.getByRole('radio', { name: kind }).check()
  await d.getByLabel('Listen port').fill(String(listen))
  if (target) {
    await d.getByLabel('Target host').fill('127.0.0.1')
    await d.getByLabel('Target port').fill(String(target))
  }
  await d.getByLabel('Label').fill(kind.toLowerCase())
  await d.getByRole('button', { name: 'Save' }).click()
  await expect(d).toBeHidden()
}

test('local and dynamic tunnels run, survive a lock, and stop', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await win.locator('.tabbar .hometab').click()
  await win.getByRole('button', { name: 'Tunnels box' }).click()
  const view = win.getByRole('region', { name: 'Tunnels on box' })
  await expect(win.locator('.tabbar .tab.active')).toHaveAttribute('data-kind', 'tunnels')

  const lp = await freePort()
  const dp = await freePort()
  await addTunnel(win, 'Local', lp, echoPort)
  await addTunnel(win, 'Dynamic', dp)
  const rows = view.locator('tbody tr')
  await expect(rows).toHaveCount(2)

  for (const i of [0, 1]) {
    await rows.nth(i).getByRole('button', { name: 'Start' }).click()
    await expect(rows.nth(i).locator('td.status')).toHaveText('running')
  }
  const lc = await connect(lp)
  await ping(lc)
  await expect(rows.nth(0).locator('td.conns')).toHaveText('1')
  const sc = await socks(dp, echoPort)
  await ping(sc)

  // The host card shows the running count.
  await win.locator('.tabbar .hometab').click()
  await expect(win.locator('.hostcard .chip.tunnels')).toHaveText('2 ⇄')

  // Lock: tunnels keep working.
  await win.getByRole('button', { name: 'Lock' }).click()
  await expect(win.getByLabel('Master password')).toBeVisible()
  await ping(lc)
  await ping(sc)
  lc.destroy(); sc.destroy()
  await unlock(win)

  // Stop both; the ports close.
  await win.locator('.tabbar .tab[data-kind="tunnels"] .tabname').click()
  for (const i of [0, 1]) {
    await rows.nth(i).getByRole('button', { name: 'Stop' }).click()
    await expect(rows.nth(i).locator('td.status')).toHaveText('stopped')
  }
  await expect(connect(lp)).rejects.toThrow()
  await win.locator('.tabbar .hometab').click()
  await expect(win.locator('.hostcard .chip.tunnels')).toHaveCount(0)
})
```

- [ ] **Step 2: Run it**

Run: `cd desktop && npm run build && npx playwright test e2e/tunnels.spec.ts` (on Linux, prefix with `xvfb-run -a`).
Expected: 1 passed. If a locator differs from Task 6's contract, fix the component to match the contract; do not change the test to match the component.

- [ ] **Step 3: Run the full suite**

Run: `cd desktop && npm run e2e`
Expected: every previous spec still passes, and tunnels passes too. The known `ime.spec.ts` flake may need one rerun; say so if it happens.

- [ ] **Step 4: Commit**

```bash
git add desktop/e2e/tunnels.spec.ts CLAUDE.md
git commit -m "test(desktop): e2e for local and dynamic tunnels across a lock"
```

---

### Task 8: Docs

**Files:**
- Modify: `CLAUDE.md`, `docs/superpowers/ROADMAP.md`, `PRODUCT.md`, `docs/superpowers/specs/2026-09-27-slice3b-port-forwarding-design.md` (Status line)

- [ ] **Step 1: Edit**

- `CLAUDE.md`:
  - UI-door method list: add `tunnels.list`, `tunnels.save`, `tunnels.delete`, `tunnels.start`, `tunnels.stop` (notification), and change the protocol version to 5.
  - Add a bullet after the Files bullet, **Tunnels** (`tunnels.go`, `internal/tunnel`), in the file's style. It should cover:
    - tunnels are saved per server in the vault (`config.Tunnel`), written only by `tunnels.save/delete` through `config.Update`, and kept by `ApplyServer`;
    - `dialChanged` ignores them;
    - forwards run on the manager's shared client, loopback only (the bind address is not a field);
    - `tunnels.start` needs an unlocked vault and a pin (no trust prompt);
    - running tunnels survive lock; `tunnels.stop` is a notification that works while locked;
    - a tunnel ends on `client.Wait` ("connection lost") or on a server change ("server changed");
    - start and end `kind: "tunnel"` audit records;
    - no MCP-door access.
  - Desktop section: one line for the Tunnels tab (`TunnelsView.tsx`, `TunnelDialog.tsx`, `tunnels.ts`) and the host card badge.
  - `sshtestd` line: it also serves `direct-tcpip` and loopback `tcpip-forward`.
- `ROADMAP.md`: the 3b status stays "Specced" until the exit gate. Add Later-list bullets: auto-start tunnels, LAN sharing, reconnect, remote dynamic.
- `PRODUCT.md`: find the "Not built" mention of port forwarding. Leave it until the exit gate, but add "(in progress, slice 3b)" if the file lists in-progress work; otherwise do not touch it.
- Spec Status: `Approved 2026-09-27; implemented (plan plans/2026-09-27-slice3b-port-forwarding.md).`

- [ ] **Step 2: Verify**

Run: `go test -race ./... && (cd desktop && npm run typecheck && npm test)`
Expected: all pass.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md docs PRODUCT.md
git commit -m "docs: slice 3b tunnels in CLAUDE.md, ROADMAP, spec status"
```

---

## Exit gate (manual, after Task 8)

On `buildpc`, from the app (`npm start` with the real vault):
1. Save a local tunnel to a real service, such as `127.0.0.1:<port of something listening on buildpc>`, and a dynamic tunnel.
2. Start both, and use each: the service through the local port, and `curl --socks5-hostname 127.0.0.1:<dp> http://<a host only buildpc reaches>`.
3. Lock the vault, check both still work, and stop them.

Record the result in ROADMAP row 3b ("Done <date>").

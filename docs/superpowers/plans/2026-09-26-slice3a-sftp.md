# Slice 3a SFTP File Browser Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Files tab per saved host in the desktop app: list, upload/download (files and folders), mkdir, rename, recursive delete, Finder drop, with progress, cancel, one conflict question per transfer, and audit.

**Architecture:** The hub owns every byte: a new engine package `internal/files` walks, plans, and copies between an `*sftp.Client` and `os.Root`-confined local folders; `internal/hub` exposes it on the UI door as `files.*` (plan/run jobs with notifications, protocol 4). Electron main is the only place local paths exist: it hands the renderer opaque single-use tokens and rebuilds every `files.plan` from an exact key allowlist; it also asks the native conflict question for downloads. The renderer adds a Files tab kind with a virtualised listing, dialogs, and a transfer strip.

**Tech Stack:** Go 1.26 (`os.Root`), `github.com/pkg/sftp` v1.13.11 (new), `golang.org/x/crypto/ssh`; Electron 44 (`dialog`, `webUtils`), React 19, vitest (node env), Playwright.

**Spec:** `docs/superpowers/specs/2026-09-26-slice3a-sftp-design.md` (read it; this plan argues from it).

## Global Constraints

Copied from the spec. Every task's requirements include this section.

- **The renderer never names a local path.** Local paths exist only in Electron main, which creates them from the system dialogs and from Finder drops and hands the renderer an opaque token for each. Main rebuilds every `files.plan` it relays from an exact allowlist of keys, swapping tokens for paths; it never forwards a key it did not build. The hub decodes `files.*` params strictly: object keys must match its allowlist exactly (case-sensitive), with no unknown or duplicate keys.
- **Grants are narrow.** A token is bound to one server and one kind (`read` for upload sources, `writeDir` for a download destination), is used by at most one `files.plan`, expires after 5 minutes, and all tokens are dropped when the vault locks and when the renderer is reloaded. The dialogs name the server and folder.
- **Main decides download overwrites.** For a download plan with conflicts, the renderer can only ask main to resolve it; main shows the native Cancel / Skip existing / Overwrite all dialog and relays the author's choice.
- **Drops are real files.** Preload checks it was given an array of `File` objects (at most 1,000), turns each into a path with `webUtils.getPathForFile`, and sends the non-empty paths to main.
- **All local reads and writes go through `os.Root`.** A download writes only under an `os.Root` opened on the picked folder; an upload reads each source through an `os.Root` opened on its parent.
- **Names from the server are untrusted.** Every name must be valid UTF-8 and one path element: not empty, not `.` or `..`, no `/`, `\`, or NUL. A download also requires `filepath.IsLocal(name)` and `filepath.Localize(name) == name`, and treats two names in one folder that differ only in case as a conflict (the second is an error). A bad name is skipped and reported as an error.
- **No writing through symlinks, and no silent replace.** Each file is written to a new part file `.<name>.<random>.sshgate-part` beside its destination, created with `O_CREATE|O_EXCL` and mode 0600, then committed: without overwrite by a no-replace link (`Link` then remove the part file), falling back to `Lstat` then rename where links are unsupported; with overwrite by an atomic replacing rename (`posix-rename@openssh.com` on the server). A server with no replacing rename cannot overwrite (per-file error). A destination that is a symlink at commit time is an error and is skipped.
- **Recursive walks never follow symlinks.** Walks use `Lstat`. Symlinks inside folders are skipped (reported); a symlink the author selected directly is resolved once with `Stat` and copied as what it points to. Delete removes a symlink as the link itself; `sftp.Client.RemoveAll` is never used.
- **Mutations need real paths.** `files.mkdir`, `files.rename`, and delete and upload plans take absolute, cleaned remote paths only; delete and rename refuse `/` and the login user's home directory itself.
- **Permission bits.** Uploads keep `rwx` bits (setuid/setgid/sticky dropped). Downloads keep at most `rwxr-xr-x`. Folders are created `0700` while filled and get their final mode last. Modification times are kept. Ownership is never set.
- **Displayed text is sanitised:** C0/C1 controls, U+202A–202E, U+2066–2069, zero-width characters (U+200B–200F, U+2060–2064, U+FEFF), U+2028/2029 are replaced with a visible `\uXXXX` escape in every server-sourced string the app shows.
- **Bounded work.** A listing returns at most 10,000 entries; a plan walks at most 100,000 entries and 64 levels; a job keeps its first 20 errors and a count. Every walk and copy runs under a cancellable context; each job uses its own SFTP channel.
- **UI door only.** Every `files.*` method is on the UI door only, needs a pinned host key and an unlocked vault, except `files.cancel` (always allowed, a notification) and `files.cancelAll` (Electron main only).
- Protocol 4: `ProtocolVersion` in `internal/hub/idle.go`, `PROTOCOL_VERSION` in `desktop/src/shared/protocol.ts`, and `desktop/test/fixtures/fakeHub.mjs` move together.
- Repo rules (CLAUDE.md): fail closed; no secret in any error, log, or audit record; a test with every security-path change; the renderer never polls anything but `status`; commits end with the session trailer given to you.

## Plan decisions

1. **Engine in its own package** `internal/files` (no hub or rpc imports), tested directly against the `sshtest` SFTP server. The hub only wires it.
2. **mtime on the remote part file is set by path** (`Client.Chtimes(part, …)`): `pkg/sftp` v1.13.11 has `File.Chmod` but no `File.Chtimes`. The part file has a random name created with `O_EXCL`, so the path names the file we created. Locally both go through the handle's root (`os.Root.Chtimes` on the part name).
3. **Grants clear on the `locked` notification and on any non-`running` hub state** (lock reaches main as the `locked` notification, not as `hub:state`), and in `recoverRenderer`.
4. **`files.cancel` is a notification**: it runs on the rpc read loop, so it only cancels a context. `files.cancelAll` is a request registered like `term.closeAll`, not in the renderer whitelist.
5. **Jobs are hub-wide and door-owned.** A hub-wide set lets `SaveServer`/`DeleteServer`/`ForgetHostKey` end a server's jobs with `"server changed"`; they call it **before** `h.reg.Close(name)` so that reason wins over "connection lost". Each door owns its ids, `cancelAll`, and notification routing; the door's `ServeUIDoor` cancels its jobs and waits up to 5 s when it returns.
6. **Audit start record is written at `files.run`** (a plan changes nothing); mkdir and rename write one record each.
7. **Listing memory.** `pkg/sftp` v1.13.11 has no streaming readdir: `ReadDirContext` buffers the whole folder. The hub bounds it with a 30 s context and returns at most 10,000 entries. Ceiling: a hostile server can still make one listing buffer what it sends in 30 s. Upgrade path: a raw `SSH_FXP_READDIR` loop if that ever matters. (Marked with a `ponytail:` comment.)
8. **Docker test image.** The OpenSSH SFTP test keeps the existing `:latest` image and skips (does not fail) when the server answers `ErrNoSFTP`, since no tag can be verified offline here.
9. **Renderer tests stay pure.** vitest runs `environment: 'node'` on `test/**/*.test.ts`; every rule worth testing lives in `desktop/src/renderer/files.ts` or `desktop/src/main/files.ts` as plain functions or classes.
10. **Main's files logic is a class** `FilesRelay` (`desktop/src/main/files.ts`) holding grants and download-job conflict counts; `relayCall` stays stateless and `registerIpc` routes `files.plan`/`files.run` to the relay.

## File map

Go:
- Create `internal/sshx/sshtest/sftp.go` — rooted `pkg/sftp` request-server handlers (normal/hostile/gated) for the test server.
- Modify `internal/sshx/sshtest/sshtest.go` — `subsystem` request; methods `ServeSFTP`, `GateSFTP`, `HostileSFTP`, `Addr`.
- Modify `internal/sshx/sshtest/sshtestd/main.go` — `-sftp-root`.
- Create `internal/sshx/sftp.go` — `ErrNoSFTP`, `Manager.SFTP`, `Manager.NewSFTP`, `newSFTP`.
- Modify `internal/sshx/manager.go` — `sftp` field; `Close` closes it outside `m.mu`.
- Create `internal/files/names.go`, `perm.go`, `errors.go`, `list.go`, `plan.go`, `copy.go`, `delete.go`, `fatal_unix.go`, `fatal_other.go` + tests.
- Modify `internal/broker/audit.go` — `FileRecord`, `WriteFile`.
- Create `internal/hub/files.go` — strict params, `files.list/mkdir/rename`, handler registration.
- Create `internal/hub/filejobs.go` — job set, plan/run/cancel/cancelAll, notifications, audit.
- Modify `internal/hub/uidoor.go`, `hub.go`, `hosts.go`, `idle.go`.
- Modify `internal/hub/live_test.go` — `files.list` per server.

Desktop:
- Create `desktop/src/main/files.ts` — `Grants`, `FilesRelay`, dialogs.
- Modify `desktop/src/main/ipc.ts`, `main.ts`, `window.ts`, `desktop/src/preload/preload.ts`.
- Modify `desktop/src/shared/protocol.ts`, `desktop/test/fixtures/fakeHub.mjs`.
- Modify `desktop/src/renderer/transport.ts`, `terminals.ts`, `TerminalTabs.tsx`, `HostList.tsx`, `HostEditor.tsx`, `App.tsx`, `icons.tsx`, `styles.css`.
- Create `desktop/src/renderer/files.ts`, `FilesView.tsx`, `FileDialogs.tsx`.
- Tests: `desktop/test/files-main.test.ts`, `desktop/test/files.test.ts`, updates to `ipc.test.ts`, `window.test.ts`, `terminals.test.ts`; e2e `desktop/e2e/files.spec.ts`, `launch.ts` option.
- Docs: `CLAUDE.md`, `README.md`, `docs/superpowers/ROADMAP.md`, `PRODUCT.md`, spec status, `docs/superpowers/checklists/slice3a-manual.md`.

## Interfaces at a glance (names every task must use)

```go
// internal/sshx
var ErrNoSFTP = errors.New("server has no SFTP subsystem")
func (m *Manager) SFTP() (*sftp.Client, error)    // shared, tied to the current *ssh.Client
func (m *Manager) NewSFTP() (*sftp.Client, error) // one job's own channel; caller closes

// internal/files
const MaxList, MaxPlanEntries, MaxDepth, MaxErrors = 10000, 100000, 64, 20
type Entry struct { Name string; Size int64; Mode uint32; Mtime int64; Kind string; Target string }
type Listing struct { Path string; Entries []Entry; Truncated bool; Bad int }
type Errors struct { List []string; Count int }
type Op string // "upload" | "download" | "delete"
type Plan struct { Op Op; Files, Dirs, Links int; Bytes int64; Conflicts int; Sample []string; Errors Errors /* + unexported */ }
type Result struct { Copied, Skipped, Deleted int; Bytes int64; Errors Errors; Cancelled bool; Fatal error }
type Progress func(file string, done, total int64)
func Home(c *sftp.Client) (string, error)
func List(ctx context.Context, c *sftp.Client, p string) (Listing, error)
func Mkdir(c *sftp.Client, p string) error
func Rename(c *sftp.Client, from, to string) error
func PlanUpload(ctx context.Context, c *sftp.Client, sources []string, dest string) (*Plan, error)
func PlanDownload(ctx context.Context, c *sftp.Client, sources []string, dest string) (*Plan, error)
func PlanDelete(ctx context.Context, c *sftp.Client, paths []string) (*Plan, error)
func (p *Plan) Run(ctx context.Context, c *sftp.Client, overwrite bool, progress Progress) Result
func (p *Plan) Close()
```

UI door (protocol 4):

| Method | Kind | Params | Result / effect |
|---|---|---|---|
| `files.list` | request | `{server, path, trustHostKey?}` | `{status:"listed", path, entries, truncated, bad}` or `hostKeyUnknown`/`hostKeyMismatch` |
| `files.mkdir` | request | `{server, path}` | `{}` |
| `files.rename` | request | `{server, from, to}` | `{}` |
| `files.plan` | request | `{id, server, op, sources, dest?}` | `{}`; then `files.planned` |
| `files.run` | request | `{id, conflict:"overwrite"\|"skip"}` | `{}`; then `files.progress`…, `files.done` |
| `files.cancel` | notification | `{id}` | cancels |
| `files.cancelAll` | request (main only) | `{}` | `{cancelled:n}` |

Notifications: `files.planned {id, files, dirs, links, bytes, conflicts:{count, sample}, errorCount, errors}`, `files.progress {id, file, done, total}`, `files.done {id, op, copied, skipped, deleted, bytes, errorCount, errors, cancelled, reason}`.

---

### Task 1: SFTP subsystem in the test server

**Files:**
- Modify: `go.mod`, `go.sum` (`go get github.com/pkg/sftp@v1.13.11`)
- Create: `internal/sshx/sshtest/sftp.go`
- Modify: `internal/sshx/sshtest/sshtest.go` (`Server` fields and methods, `session` switch)
- Modify: `internal/sshx/sshtest/sshtestd/main.go` (`-sftp-root`)
- Test: `internal/sshx/sshtest/sftp_test.go`

**Interfaces:**
- Produces: `sshtest.Server` methods (all take `s.mu`, so tests may call them while connections are live): `ServeSFTP(root string)` (until called, the subsystem is refused), `GateSFTP(gate chan struct{})` (nil = ungated; else each SFTP ReadAt/WriteAt receives one value first), `HostileSFTP(names ...string)` (names listed in `/hostile`), and `Addr() string`. The handlers read the gate and hostile names on every call, so they apply to connections already open. The server's SFTP home is `/home` (created under the root). Paths are rooted with `os.Root`, so nothing outside the root is reachable. Plain `Rename` refuses to replace (as OpenSSH's `sftp-server` does); `PosixRename` replaces; `Link` hard-links; `O_EXCL` is honoured.
- Consumed by: Tasks 2, 4–8, 12.

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/pkg/sftp@v1.13.11 && go mod tidy`
Expected: `go.mod` lists `github.com/pkg/sftp v1.13.11`.

- [ ] **Step 2: Write the failing test** — `internal/sshx/sshtest/sftp_test.go`

```go
package sshtest

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func sftpClient(t *testing.T, s *Server) *sftp.Client {
	t.Helper()
	cc := &ssh.ClientConfig{User: "u", Auth: []ssh.AuthMethod{ssh.Password("x")}, HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	conn, err := ssh.Dial("tcp", s.Addr(), cc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func rootedServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := Start(t)
	root := t.TempDir()
	s.ServeSFTP(root)
	return s, root
}

func TestSFTPRootedHomeAndFiles(t *testing.T) {
	s, root := rootedServer(t)
	c := sftpClient(t, s)
	home, err := c.RealPath(".")
	if err != nil || home != "/home" {
		t.Fatalf("home %q %v", home, err)
	}
	f, err := c.OpenFile("/home/a.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("hi"))
	f.Close()
	if b, _ := os.ReadFile(filepath.Join(root, "home", "a.txt")); string(b) != "hi" {
		t.Fatalf("on disk %q", b)
	}
	if _, err := c.OpenFile("/home/a.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL); err == nil {
		t.Fatal("O_EXCL create of an existing file succeeded")
	}
	// Absolute paths and .. stay inside the root.
	if _, err := c.Stat("/../../etc/passwd"); err == nil {
		t.Fatal("escaped the root")
	}
	os.Symlink("/etc", filepath.Join(root, "home", "esc"))
	if _, err := c.Stat("/home/esc/passwd"); err == nil {
		t.Fatal("followed a symlink out of the root")
	}
	if fi, err := c.Lstat("/home/esc"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("lstat link %v %v", fi, err)
	}
}

func TestSFTPRenameSemantics(t *testing.T) {
	s, root := rootedServer(t)
	c := sftpClient(t, s)
	os.WriteFile(filepath.Join(root, "home", "a"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(root, "home", "b"), []byte("b"), 0o644)
	if err := c.Rename("/home/a", "/home/b"); err == nil {
		t.Fatal("plain rename replaced an existing file")
	}
	if err := c.PosixRename("/home/a", "/home/b"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "home", "b")); string(b) != "a" {
		t.Fatalf("b = %q", b)
	}
	if err := c.Link("/home/b", "/home/c"); err != nil {
		t.Fatal(err)
	}
	if err := c.Link("/home/b", "/home/c"); err == nil {
		t.Fatal("link onto an existing name succeeded")
	}
}

func TestSFTPGateBlocksTransfers(t *testing.T) {
	s, root := rootedServer(t)
	gate := make(chan struct{})
	s.GateSFTP(gate)
	os.WriteFile(filepath.Join(root, "home", "g"), []byte("gated"), 0o644)
	c := sftpClient(t, s)
	f, err := c.Open("/home/g")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(f); got <- b }()
	select {
	case <-got:
		t.Fatal("read finished without a gate token")
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	if b := <-got; !bytes.Equal(b, []byte("gated")) {
		t.Fatalf("read %q", b)
	}
}

func TestSFTPHostileListing(t *testing.T) {
	s, _ := rootedServer(t)
	s.HostileSFTP("x/..", `a\b`, "nul\x00", "\xff", "A", "a")
	c := sftpClient(t, s)
	fis, err := c.ReadDir("/hostile")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, fi := range fis {
		names = append(names, fi.Name())
	}
	// The client applies path.Base: "x/.." arrives as "..".
	want := []string{"..", `a\b`, "nul\x00", "\xff", "A", "a"}
	if len(names) != len(want) {
		t.Fatalf("names %q", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names %q, want %q", names, want)
		}
	}
	if fi, err := c.Stat("/hostile/A"); err != nil || fi.Size() != 1<<40 {
		t.Fatalf("hostile size %v %v", fi, err)
	}
}

func TestSFTPRefusedWithoutRoot(t *testing.T) {
	s := Start(t)
	cc := &ssh.ClientConfig{User: "u", Auth: []ssh.AuthMethod{ssh.Password("x")}, HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	conn, err := ssh.Dial("tcp", s.Addr(), cc)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sftp.NewClient(conn); err == nil {
		t.Fatal("sftp subsystem accepted with no root")
	}
}
```

`Server.Addr()` does not exist yet; add it in Step 4 as `net.JoinHostPort(s.Host, strconv.Itoa(s.Port))`.

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/sshx/sshtest -run TestSFTP -v`
Expected: FAIL (compile errors: `ServeSFTP`, `Addr`, … undefined).

- [ ] **Step 4: Implement** — `internal/sshx/sshtest/sftp.go`

```go
package sshtest

import (
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sftpHome is the test server's login directory, inside the root.
const sftpHome = "/home"

// rootFS serves SFTP from an os.Root: every path is cleaned to an absolute
// path and resolved inside the root, so nothing outside it is reachable,
// even through a symlink. The gate and the hostile names are read from the
// server on every call (under s.mu), so tests may change them while a
// connection is open. /hostile lists hostile names whose files report 1 TiB
// and read as "x".
type rootFS struct {
	root *os.Root
	srv  *Server
}

func (s *Server) serveSFTP(ch ssh.Channel, root string) {
	r, err := os.OpenRoot(root)
	if err != nil {
		ch.Close()
		return
	}
	defer r.Close()
	r.MkdirAll(strings.TrimPrefix(sftpHome, "/"), 0o755)
	h := &rootFS{root: r, srv: s}
	rs := sftp.NewRequestServer(ch, sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h}, sftp.WithStartDirectory(sftpHome))
	rs.Serve()
	rs.Close()
}

func (h *rootFS) gate() chan struct{} {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	return h.srv.sftpGate
}

func (h *rootFS) hostile() []string {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	return h.srv.sftpHostile
}

func rel(p string) string {
	p = path.Clean("/" + p)
	if p == "/" {
		return "."
	}
	return p[1:]
}

func (h *rootFS) hostileDir(p string) bool {
	return h.hostile() != nil && (path.Clean(p) == "/hostile" || strings.HasPrefix(path.Clean(p), "/hostile/"))
}

type gated struct {
	f    *os.File
	gate chan struct{}
}

func (g gated) ReadAt(b []byte, off int64) (int, error) {
	if g.gate != nil {
		<-g.gate
	}
	return g.f.ReadAt(b, off)
}

func (g gated) WriteAt(b []byte, off int64) (int, error) {
	if g.gate != nil {
		<-g.gate
	}
	return g.f.WriteAt(b, off)
}

func (g gated) Close() error { return g.f.Close() }

func (h *rootFS) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if h.hostileDir(r.Filepath) {
		return strings.NewReader("x"), nil
	}
	f, err := h.root.Open(rel(r.Filepath))
	if err != nil {
		return nil, err
	}
	return gated{f, h.gate()}, nil
}

func (h *rootFS) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	fl := os.O_WRONLY | os.O_CREATE
	if r.Pflags().Excl {
		fl |= os.O_EXCL
	}
	if r.Pflags().Trunc {
		fl |= os.O_TRUNC
	}
	f, err := h.root.OpenFile(rel(r.Filepath), fl, 0o644)
	if err != nil {
		return nil, err
	}
	return gated{f, h.gate()}, nil
}

func (h *rootFS) Filecmd(r *sftp.Request) error {
	p := rel(r.Filepath)
	switch r.Method {
	case "Setstat":
		a := r.Attributes()
		if r.AttrFlags().Permissions {
			if err := h.root.Chmod(p, a.FileMode()&fs.ModePerm); err != nil {
				return err
			}
		}
		if r.AttrFlags().Acmodtime {
			return h.root.Chtimes(p, time.Unix(int64(a.Atime), 0), time.Unix(int64(a.Mtime), 0))
		}
		return nil
	case "Rename": // like OpenSSH's sftp-server: never replaces
		if _, err := h.root.Lstat(rel(r.Target)); err == nil {
			return os.ErrExist
		}
		return h.root.Rename(p, rel(r.Target))
	case "Rmdir", "Remove":
		return h.root.Remove(p)
	case "Mkdir":
		return h.root.Mkdir(p, 0o755)
	case "Link":
		return h.root.Link(p, rel(r.Target))
	case "Symlink": // Filepath is the link's target, Target the link itself
		return h.root.Symlink(r.Filepath, rel(r.Target))
	}
	return sftp.ErrSSHFxOpUnsupported
}

// PosixRename replaces, like posix-rename@openssh.com.
func (h *rootFS) PosixRename(r *sftp.Request) error {
	return h.root.Rename(rel(r.Filepath), rel(r.Target))
}

type listerAt []os.FileInfo

func (l listerAt) ListAt(out []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(out, l[off:])
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}

type fakeInfo struct {
	name string
	dir  bool
}

func (f fakeInfo) Name() string { return f.name }
func (f fakeInfo) Size() int64  { return 1 << 40 }
func (f fakeInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (f fakeInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() any           { return nil }

func (h *rootFS) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	if h.hostileDir(r.Filepath) {
		if path.Clean(r.Filepath) != "/hostile" {
			return listerAt{fakeInfo{name: path.Base(r.Filepath)}}, nil
		}
		if r.Method != "List" {
			return listerAt{fakeInfo{name: "hostile", dir: true}}, nil
		}
		var l listerAt
		for _, n := range h.hostile() {
			l = append(l, fakeInfo{name: n})
		}
		return l, nil
	}
	p := rel(r.Filepath)
	switch r.Method {
	case "List":
		f, err := h.root.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		des, err := f.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		var l listerAt
		for _, de := range des {
			if fi, err := de.Info(); err == nil {
				l = append(l, fi)
			}
		}
		return l, nil
	case "Stat":
		fi, err := h.root.Stat(p)
		if err != nil {
			return nil, err
		}
		return listerAt{fi}, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}

func (h *rootFS) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	if h.hostileDir(r.Filepath) {
		return h.Filelist(&sftp.Request{Method: "Stat", Filepath: r.Filepath})
	}
	fi, err := h.root.Lstat(rel(r.Filepath))
	if err != nil {
		return nil, err
	}
	return listerAt{fi}, nil
}

func (h *rootFS) Readlink(p string) (string, error) {
	return h.root.Readlink(rel(p))
}

// RealPath resolves relative paths against the home directory, lexically.
func (h *rootFS) RealPath(p string) (string, error) {
	if !path.IsAbs(p) {
		p = path.Join(sftpHome, p)
	}
	return path.Clean(p), nil
}
```

In `internal/sshx/sshtest/sshtest.go`:
- Add unexported fields to `Server` (guarded by the existing `s.mu`):
```go
	sftpRoot    string        // "" refuses the sftp subsystem
	sftpGate    chan struct{} // nil: ungated; else each SFTP ReadAt/WriteAt takes one value
	sftpHostile []string      // names listed in /hostile (see sftp.go)
```
- Add methods:
```go
// Addr is host:port for ssh.Dial.
func (s *Server) Addr() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// ServeSFTP turns on the sftp subsystem, rooted in root (home is <root>/home).
func (s *Server) ServeSFTP(root string) { s.mu.Lock(); s.sftpRoot = root; s.mu.Unlock() }

// GateSFTP makes each SFTP ReadAt/WriteAt wait for one value from gate (nil: no gate).
func (s *Server) GateSFTP(gate chan struct{}) { s.mu.Lock(); s.sftpGate = gate; s.mu.Unlock() }

// HostileSFTP sets the names /hostile lists.
func (s *Server) HostileSFTP(names ...string) { s.mu.Lock(); s.sftpHostile = names; s.mu.Unlock() }
```
- In `session`, add a case before `default`:
```go
		case "subsystem":
			var p struct{ Name string }
			ssh.Unmarshal(req.Payload, &p)
			s.mu.Lock()
			root := s.sftpRoot
			s.mu.Unlock()
			if p.Name != "sftp" || root == "" {
				req.Reply(false, nil)
				continue
			}
			req.Reply(true, nil)
			go s.serveSFTP(ch, root)
```
`session`'s `defer ch.Close()` runs when `reqs` closes (the client closed the channel), which is after `serveSFTP` finished, so there is no double-use.

In `sshtestd/main.go`: add `sftpRoot := flag.String("sftp-root", "", "serve SFTP rooted in this directory (created if missing); home is <dir>/home")`, and after `Listen`:
```go
	if *sftpRoot != "" {
		if err := os.MkdirAll(filepath.Join(*sftpRoot, "home"), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		srv.ServeSFTP(*sftpRoot)
	}
```
and mention `-sftp-root` in the package comment.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/sshx/sshtest/... -race -v -run 'TestSFTP'`
Expected: PASS (5 tests). Then `go vet ./... && GOOS=windows go vet ./...`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/sshx/sshtest
git commit -m "test(sshtest): rooted SFTP subsystem with hostile and gated modes"
```

---

### Task 2: `Manager.SFTP` and `Manager.NewSFTP`

**Files:**
- Create: `internal/sshx/sftp.go`
- Modify: `internal/sshx/manager.go` (`Manager` gains `sftp *sftpConn`; `Close`)
- Test: `internal/sshx/sftp_test.go` (in-process), `internal/sshx/manager_test.go` (Docker)

**Interfaces:**
- Consumes: Task 1's `sshtest.Server.ServeSFTP`.
- Produces: `sshx.ErrNoSFTP`; `(*Manager).SFTP() (*sftp.Client, error)`; `(*Manager).NewSFTP() (*sftp.Client, error)`.

- [ ] **Step 1: Write the failing test** — `internal/sshx/sftp_test.go`

```go
package sshx

import (
	"errors"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

func sftpManager(t *testing.T, root string) (*Manager, *sshtest.Server) {
	t.Helper()
	s := sshtest.Start(t)
	if root != "" {
		s.ServeSFTP(root)
	}
	m := NewManager(DialConfig{Host: s.Host, Port: s.Port, User: "u", Password: "p", Auth: "password",
		HostKey: s.Fingerprint(), StrictHostKey: true})
	t.Cleanup(m.Close)
	return m, s
}

func TestManagerSFTPSharedAndFresh(t *testing.T) {
	m, _ := sftpManager(t, t.TempDir())
	a, err := m.SFTP()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.SFTP()
	if a != b {
		t.Fatal("SFTP() did not reuse the shared client")
	}
	if home, err := a.RealPath("."); err != nil || home != "/home" {
		t.Fatalf("home %q %v", home, err)
	}
	j, err := m.NewSFTP()
	if err != nil {
		t.Fatal(err)
	}
	if j == a {
		t.Fatal("NewSFTP returned the shared client")
	}
	j.Close()
	if _, err := a.Getwd(); err != nil {
		t.Fatalf("closing a job client broke the shared one: %v", err)
	}
}

func TestManagerSFTPNoSubsystem(t *testing.T) {
	m, _ := sftpManager(t, "")
	if _, err := m.SFTP(); !errors.Is(err, ErrNoSFTP) {
		t.Fatalf("err %v, want ErrNoSFTP", err)
	}
	if _, err := m.NewSFTP(); !errors.Is(err, ErrNoSFTP) {
		t.Fatalf("err %v, want ErrNoSFTP", err)
	}
}

func TestManagerSFTPDroppedWithConnection(t *testing.T) {
	m, _ := sftpManager(t, t.TempDir())
	a, err := m.SFTP()
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := a.Getwd(); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shared client still works after Close")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, err := m.SFTP() // redials
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("a closed client was reused after redial")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/sshx -short -run TestManagerSFTP -v`
Expected: FAIL (`m.SFTP undefined`).

- [ ] **Step 3: Implement** — `internal/sshx/sftp.go`

```go
package sshx

import (
	"errors"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// ErrNoSFTP: the server refused the sftp subsystem.
var ErrNoSFTP = errors.New("server has no SFTP subsystem")

// sftpConn is the shared SFTP client and the SSH client it runs on.
type sftpConn struct {
	on *ssh.Client
	c  *sftp.Client
}

// newSFTP opens the sftp subsystem on its own channel. Unlike
// sftp.NewClient, it closes the session when the subsystem is refused.
func newSFTP(client *ssh.Client) (*sftp.Client, error) {
	s, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	w, err := s.StdinPipe()
	if err != nil {
		s.Close()
		return nil, err
	}
	r, err := s.StdoutPipe()
	if err != nil {
		s.Close()
		return nil, err
	}
	if err := s.RequestSubsystem("sftp"); err != nil {
		s.Close()
		if err.Error() == "ssh: subsystem request failed" {
			return nil, ErrNoSFTP
		}
		return nil, err
	}
	c, err := sftp.NewClientPipe(r, w)
	if err != nil {
		s.Close()
		return nil, err
	}
	go func() { c.Wait(); s.Close() }()
	return c, nil
}

// SFTP returns the shared client for listings and single operations. It
// belongs to the current *ssh.Client: a redial replaces it, and it is
// dropped when its own channel dies. Closes happen outside m.mu.
func (m *Manager) SFTP() (*sftp.Client, error) {
	m.mu.Lock()
	if err := m.ensure(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	var stale *sftp.Client
	if m.sftp != nil && m.sftp.on != m.client {
		stale, m.sftp = m.sftp.c, nil
	}
	if s := m.sftp; s != nil {
		m.mu.Unlock()
		return s.c, nil
	}
	client := m.client
	m.mu.Unlock()
	if stale != nil {
		go stale.Close()
	}
	c, err := newSFTP(client)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	switch {
	case m.client != client:
		m.mu.Unlock()
		go c.Close()
		return nil, errors.New("connection closed while opening SFTP")
	case m.sftp != nil: // another caller won the race
		keep := m.sftp.c
		m.mu.Unlock()
		go c.Close()
		return keep, nil
	}
	m.sftp = &sftpConn{on: client, c: c}
	m.mu.Unlock()
	go func() {
		c.Wait()
		m.mu.Lock()
		if m.sftp != nil && m.sftp.c == c {
			m.sftp = nil
		}
		m.mu.Unlock()
	}()
	return c, nil
}

// NewSFTP opens a client on its own channel for one job; the caller closes it.
func (m *Manager) NewSFTP() (*sftp.Client, error) {
	m.mu.Lock()
	err := m.ensure()
	client := m.client
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return newSFTP(client)
}
```

In `manager.go`: add `sftp *sftpConn // shared SFTP client; see sftp.go` to `Manager` (under the `mu` comment's list: "mu guards client, su, sftp and kaStop"). Change `Close` to close the shared client after unlocking:

```go
func (m *Manager) Close() {
	m.mu.Lock()
	if m.kaStop != nil {
		close(m.kaStop)
		m.kaStop = nil
	}
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
	if m.su != nil {
		m.su.sess.Close()
		m.su = nil
	}
	var s *sftpConn
	s, m.sftp = m.sftp, nil
	m.mu.Unlock()
	if s != nil {
		s.c.Close() // outside m.mu: a hung close must not block the manager
	}
}
```

- [ ] **Step 4: Docker test** — append to `internal/sshx/manager_test.go`

```go
func TestSFTPAgainstOpenSSH(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	defer cleanup()
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 30000})
	defer m.Close()
	c, err := m.SFTP()
	if errors.Is(err, ErrNoSFTP) {
		t.Skip("image offers no sftp subsystem")
	}
	if err != nil {
		t.Fatal(err)
	}
	home, err := c.RealPath(".")
	if err != nil {
		t.Fatal(err)
	}
	a, b := home+"/sftp-a", home+"/sftp-b"
	for _, p := range []string{a, b} {
		f, err := c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(p))
		f.Close()
	}
	if err := c.Rename(a, b); err == nil {
		t.Fatal("plain SFTP rename replaced an existing file on OpenSSH")
	}
	if _, ok := c.HasExtension("posix-rename@openssh.com"); !ok {
		t.Fatal("OpenSSH offers no posix-rename")
	}
	fis, err := c.ReadDir(home)
	if err != nil || len(fis) == 0 {
		t.Fatalf("readdir %v %v", fis, err)
	}
}
```
Add `"errors"` and `"os"` to that file's imports if missing.

- [ ] **Step 5: Run**

Run: `go test ./internal/sshx -race -run 'TestManagerSFTP|TestSFTPAgainstOpenSSH' -v`
Expected: the three in-process tests PASS; the Docker test PASSes or SKIPs ("docker unavailable" / "image offers no sftp subsystem"). Then `go vet ./... && GOOS=windows go vet ./...`.

- [ ] **Step 6: Commit**

```bash
git add internal/sshx
git commit -m "feat(sshx): shared and per-job SFTP clients on the manager's connection"
```

---

### Task 3: Engine rules — names, permissions, errors, fatal errors

**Files:**
- Create: `internal/files/names.go`, `internal/files/perm.go`, `internal/files/errors.go`, `internal/files/fatal_unix.go`, `internal/files/fatal_other.go`
- Test: `internal/files/names_test.go`

**Interfaces:**
- Produces (package `files`): `checkRemoteName(name string) error`, `checkLocalName(name string) error`, `CheckAbs(p string) error`, `checkMutable(p, home string) error`, `uploadMode(fs.FileMode) fs.FileMode`, `downloadMode(fs.FileMode) fs.FileMode`, `type Errors struct { List []string; Count int }` with `(*Errors).Add(path string, err error)`, `isFatal(err error) bool`, `partName(name string) string`, constants `MaxList = 10000`, `MaxPlanEntries = 100000`, `MaxDepth = 64`, `MaxErrors = 20`.

- [ ] **Step 1: Write the failing test** — `internal/files/names_test.go`

```go
package files

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestRemoteNameRules(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "nul\x00", "\xff\xfe"} {
		if checkRemoteName(bad) == nil {
			t.Errorf("remote name %q accepted", bad)
		}
	}
	for _, ok := range []string{"a", ".env", "a b", "ünï", "x..y", "-rf"} {
		if err := checkRemoteName(ok); err != nil {
			t.Errorf("remote name %q refused: %v", ok, err)
		}
	}
}

func TestLocalNameRules(t *testing.T) {
	if checkLocalName("..") == nil || checkLocalName("a/b") == nil {
		t.Fatal("local rules must include the remote rules")
	}
	if runtime.GOOS == "windows" {
		for _, bad := range []string{"CON", "a:b", "x.", "x "} {
			if checkLocalName(bad) == nil {
				t.Errorf("windows name %q accepted", bad)
			}
		}
	}
	if err := checkLocalName("report.pdf"); err != nil {
		t.Fatal(err)
	}
}

func TestPathRules(t *testing.T) {
	for _, bad := range []string{"", "rel", "/a/../b", "/a/", "//a"} {
		if CheckAbs(bad) == nil {
			t.Errorf("path %q accepted", bad)
		}
	}
	if err := CheckAbs("/srv/app"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"/", "/home/u"} {
		if checkMutable(bad, "/home/u") == nil {
			t.Errorf("mutation of %q allowed", bad)
		}
	}
	if err := checkMutable("/home/u/x", "/home/u"); err != nil {
		t.Fatal(err)
	}
}

func TestModes(t *testing.T) {
	if got := uploadMode(fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky | 0o777); got != 0o777 {
		t.Fatalf("upload %o", got)
	}
	if got := downloadMode(0o777); got != 0o755 {
		t.Fatalf("download %o", got)
	}
	if got := downloadMode(0o600); got != 0o600 {
		t.Fatalf("download %o", got)
	}
}

func TestErrorsKeepFirstTwenty(t *testing.T) {
	var e Errors
	for i := range 25 {
		e.Add(fmt.Sprintf("/f%d", i), errors.New("boom"))
	}
	if e.Count != 25 || len(e.List) != MaxErrors || e.List[0] != "/f0: boom" {
		t.Fatalf("%+v", e)
	}
}

func TestFatal(t *testing.T) {
	if !isFatal(syscall.ENOSPC) || isFatal(fs.ErrPermission) || isFatal(nil) {
		t.Fatal("fatal classification")
	}
}

func TestPartName(t *testing.T) {
	a, b := partName("x.txt"), partName("x.txt")
	if a == b || !strings.HasPrefix(a, ".x.txt.") || !strings.HasSuffix(a, ".sshgate-part") || checkRemoteName(a) != nil {
		t.Fatalf("%q %q", a, b)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/files -run 'Test(RemoteName|LocalName|PathRules|Modes|Errors|Fatal|PartName)' -v`
Expected: FAIL (package has no Go files / undefined).

- [ ] **Step 3: Implement**

`internal/files/names.go`:

```go
// Package files plans and runs SFTP file operations for the hub: listings,
// mkdir, rename, and recursive upload, download, and delete. Remote names are
// untrusted; local I/O goes through os.Root.
package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	MaxList        = 10000  // entries one listing returns
	MaxPlanEntries = 100000 // entries one plan walks
	MaxDepth       = 64     // folder levels one plan walks
	MaxErrors      = 20     // errors a job keeps (it counts the rest)
)

var errBadName = errors.New("bad file name from the server")

// checkRemoteName: valid UTF-8 and exactly one path element.
func checkRemoteName(name string) error {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\\x00") {
		return errBadName
	}
	return nil
}

// checkLocalName adds the local OS's rules (reserved names, ':' and
// trailing dots or spaces on Windows).
func checkLocalName(name string) error {
	if err := checkRemoteName(name); err != nil {
		return err
	}
	if l, err := filepath.Localize(name); err != nil || l != name || !filepath.IsLocal(name) {
		return errBadName
	}
	return nil
}

// CheckAbs: an absolute, already-clean remote path.
func CheckAbs(p string) error {
	if p == "" || !path.IsAbs(p) || path.Clean(p) != p || strings.HasPrefix(p, "//") {
		return errors.New("path must be absolute and clean")
	}
	return nil
}

// checkMutable: CheckAbs, and never "/" or the login user's home itself.
func checkMutable(p, home string) error {
	if err := CheckAbs(p); err != nil {
		return err
	}
	if p == "/" || p == home {
		return errors.New("refusing to change / or the home directory itself")
	}
	return nil
}

// partName is a fresh hidden part-file name beside name.
func partName(name string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return "." + name + "." + hex.EncodeToString(b) + ".sshgate-part"
}
```

Windows note: `filepath.Localize("x.")` does not reject a trailing dot. Add, inside `checkLocalName`, a Windows-only check via a small build-tagged helper if the test fails there: `if runtime.GOOS == "windows" && (strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || strings.Contains(name, ":")) { return errBadName }`. Put it inline with `runtime.GOOS`; no build tags needed.

`internal/files/perm.go`:

```go
package files

import "io/fs"

// uploadMode keeps rwx for user, group, and other; never setuid, setgid, sticky.
func uploadMode(m fs.FileMode) fs.FileMode { return m & fs.ModePerm }

// downloadMode: at most rwxr-xr-x; the server does not choose group/other write.
func downloadMode(m fs.FileMode) fs.FileMode { return m & 0o755 }
```

`internal/files/errors.go`:

```go
package files

// Errors keeps the first MaxErrors messages and counts all of them.
type Errors struct {
	List  []string `json:"errors"`
	Count int      `json:"errorCount"`
}

func (e *Errors) Add(p string, err error) {
	e.Count++
	if len(e.List) < MaxErrors {
		e.List = append(e.List, p+": "+err.Error())
	}
}
```

`internal/files/fatal_unix.go`:

```go
//go:build unix

package files

import (
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/pkg/sftp"
)

// isFatal: errors that end a job instead of skipping one file.
func isFatal(err error) bool {
	return err != nil && (errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, sftp.ErrSSHFxNoConnection) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT))
}
```

`internal/files/fatal_other.go`:

```go
//go:build !unix

package files

import (
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/pkg/sftp"
)

func isFatal(err error) bool {
	return err != nil && (errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, sftp.ErrSSHFxNoConnection) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ENOSPC))
}
```

(`syscall.ENOSPC` exists on Windows as an `Errno` constant; `EDQUOT` does not.)

- [ ] **Step 4: Run**

Run: `go test ./internal/files -race -v && GOOS=windows go vet ./internal/files`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/files
git commit -m "feat(files): name, path, permission, and error rules for the SFTP engine"
```

### Task 4: Engine — list, mkdir, rename

**Files:**
- Create: `internal/files/list.go`
- Test: `internal/files/list_test.go`, `internal/files/helpers_test.go`

**Interfaces:**
- Consumes: Task 1 (`ServeSFTP`, `HostileSFTP`, `GateSFTP`), Task 3 rules.
- Produces: `Entry`, `Listing`, `Home`, `List`, `Mkdir`, `Rename` (signatures in "Interfaces at a glance").

- [ ] **Step 1: Test helpers** — `internal/files/helpers_test.go`

```go
package files

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

// remote starts a test SFTP server rooted in a temp dir and returns a client,
// the server, and the root on disk (the server's /home is <root>/home).
func remote(t *testing.T) (*sftp.Client, *sshtest.Server, string) {
	t.Helper()
	s := sshtest.Start(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "home"), 0o755)
	s.ServeSFTP(root)
	cc := &ssh.ClientConfig{User: "u", Auth: []ssh.AuthMethod{ssh.Password("x")}, HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	conn, err := ssh.Dial("tcp", s.Addr(), cc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, s, root
}

func write(t *testing.T, p, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode)
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
```

- [ ] **Step 2: Write the failing test** — `internal/files/list_test.go`

```go
package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestListHomeKindsAndLinks(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "a.txt"), "abc", 0o640)
	os.Mkdir(filepath.Join(root, "home", "dir"), 0o755)
	os.Symlink("a.txt", filepath.Join(root, "home", "ln"))
	l, err := List(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != "/home" || l.Truncated || l.Bad != 0 || len(l.Entries) != 3 {
		t.Fatalf("%+v", l)
	}
	got := map[string]Entry{}
	for _, e := range l.Entries {
		got[e.Name] = e
	}
	if e := got["a.txt"]; e.Kind != "file" || e.Size != 3 || e.Mode != 0o640 {
		t.Fatalf("file %+v", e)
	}
	if got["dir"].Kind != "dir" || got["ln"].Kind != "link" || got["ln"].Target != "a.txt" {
		t.Fatalf("%+v", got)
	}
}

func TestListRejectsRelativeAndCountsBadNames(t *testing.T) {
	c, s, _ := remote(t)
	if _, err := List(context.Background(), c, "home"); err == nil {
		t.Fatal("relative path listed")
	}
	s.HostileSFTP("x/..", `a\b`, "ok")
	l, err := List(context.Background(), c, "/hostile")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Name != "ok" || l.Bad != 2 {
		t.Fatalf("%+v", l)
	}
}

func TestListTruncates(t *testing.T) {
	c, _, root := remote(t)
	dir := filepath.Join(root, "home", "many")
	os.Mkdir(dir, 0o755)
	for i := range MaxList + 1 {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d", i)), nil, 0o644)
	}
	l, err := List(context.Background(), c, "/home/many")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Truncated || len(l.Entries) != MaxList {
		t.Fatalf("truncated %v entries %d", l.Truncated, len(l.Entries))
	}
}

func TestListPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	c, _, root := remote(t)
	d := filepath.Join(root, "home", "shut")
	os.Mkdir(d, 0o000)
	t.Cleanup(func() { os.Chmod(d, 0o755) })
	if _, err := List(context.Background(), c, "/home/shut"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err %v, want permission denied", err)
	}
}

func TestMkdirAndRename(t *testing.T) {
	c, _, root := remote(t)
	if err := Mkdir(c, "rel"); err == nil {
		t.Fatal("relative mkdir")
	}
	if err := Mkdir(c, "/home/new"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "home", "new")); err != nil || !fi.IsDir() {
		t.Fatal("mkdir did not create")
	}
	write(t, filepath.Join(root, "home", "a"), "a", 0o644)
	write(t, filepath.Join(root, "home", "b"), "b", 0o644)
	if err := Rename(c, "/home/a", "/home/b"); err == nil {
		t.Fatal("rename replaced a file")
	}
	if err := Rename(c, "/home/new", "/home/b"); err == nil {
		t.Fatal("rename replaced with a folder")
	}
	if err := Rename(c, "/home", "/home2"); err == nil {
		t.Fatal("renamed the home directory")
	}
	if err := Rename(c, "/home/a", "/home/c"); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "home", "c")) != "a" {
		t.Fatal("rename lost content")
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/files -run 'TestList|TestMkdir' -v`
Expected: FAIL (`List` undefined).

- [ ] **Step 4: Implement** — `internal/files/list.go`

```go
package files

import (
	"context"
	"errors"
	"io/fs"
	"path"

	"github.com/pkg/sftp"
)

// Entry is one listed name. Mode holds permission bits only.
type Entry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	Mtime  int64  `json:"mtime"` // unix seconds
	Kind   string `json:"kind"`  // dir, file, link, other
	Target string `json:"target,omitempty"`
}

// Listing is one folder. Bad counts names that failed the name rules and
// were left out.
type Listing struct {
	Path      string  `json:"path"`
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
	Bad       int     `json:"bad"`
}

func kind(m fs.FileMode) string {
	switch {
	case m.IsDir():
		return "dir"
	case m&fs.ModeSymlink != 0:
		return "link"
	case m.IsRegular():
		return "file"
	}
	return "other"
}

// Home is the login user's home directory.
func Home(c *sftp.Client) (string, error) { return c.RealPath(".") }

// List reads one folder ("" is home).
// ponytail: pkg/sftp v1.13.11 has no streaming readdir, so ReadDirContext
// buffers the whole folder; the caller bounds it with ctx. Upgrade path: a
// raw SSH_FXP_READDIR loop that stops at MaxList.
func List(ctx context.Context, c *sftp.Client, p string) (Listing, error) {
	if p == "" {
		h, err := Home(c)
		if err != nil {
			return Listing{}, err
		}
		p = h
	} else if err := CheckAbs(p); err != nil {
		return Listing{}, err
	}
	fis, err := c.ReadDirContext(ctx, p)
	if err != nil {
		return Listing{}, err
	}
	l := Listing{Path: p, Entries: []Entry{}}
	for _, fi := range fis {
		if checkRemoteName(fi.Name()) != nil {
			l.Bad++
			continue
		}
		if len(l.Entries) == MaxList {
			l.Truncated = true
			break
		}
		e := Entry{Name: fi.Name(), Size: fi.Size(), Mode: uint32(fi.Mode().Perm()), Mtime: fi.ModTime().Unix(), Kind: kind(fi.Mode())}
		if e.Kind == "link" {
			e.Target, _ = c.ReadLink(path.Join(p, e.Name))
		}
		l.Entries = append(l.Entries, e)
	}
	return l, nil
}

// Mkdir creates one folder.
func Mkdir(c *sftp.Client, p string) error {
	if err := CheckAbs(p); err != nil {
		return err
	}
	return c.Mkdir(p)
}

var errExists = errors.New("already exists; not replaced")

// Rename never replaces: it checks the target, then uses the plain SFTP
// rename, which OpenSSH's sftp-server itself refuses to run onto an existing
// name (verified by TestSFTPAgainstOpenSSH).
func Rename(c *sftp.Client, from, to string) error {
	home, err := Home(c)
	if err != nil {
		return err
	}
	if err := checkMutable(from, home); err != nil {
		return err
	}
	if err := checkMutable(to, home); err != nil {
		return err
	}
	if _, err := c.Lstat(to); err == nil {
		return errExists
	}
	return c.Rename(from, to)
}
```

- [ ] **Step 5: Run**

Run: `go test ./internal/files -race -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/files
git commit -m "feat(files): bounded listings, mkdir, and a rename that never replaces"
```

---

### Task 5: Engine — plans (walk, conflicts, limits)

**Files:**
- Create: `internal/files/plan.go`
- Test: `internal/files/plan_test.go`

**Interfaces:**
- Consumes: Tasks 3–4.
- Produces: `type Op string` with `OpUpload`, `OpDownload`, `OpDelete`; `type Plan struct { Op Op; Files, Dirs, Links int; Bytes int64; Conflicts int; Sample []string; Errors Errors; Remote, Local []string; … }`; `PlanUpload`, `PlanDownload`, `PlanDelete`; `(*Plan).Close()`. `Remote`/`Local` list the paths the audit records (first 20 each are kept by the hub). `Links` counts symlinks skipped inside folders (copy) or removed as links (delete).

- [ ] **Step 1: Write the failing test** — `internal/files/plan_test.go`

```go
package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanDownloadConflictsMergeAndErrors(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "src", "a.txt"), "aa", 0o644)
	write(t, filepath.Join(root, "home", "src", "sub", "b.txt"), "b", 0o644)
	write(t, filepath.Join(root, "home", "src", "clash"), "file on the server", 0o644)
	os.Symlink("a.txt", filepath.Join(root, "home", "src", "inner-link"))
	dest := t.TempDir()
	write(t, filepath.Join(dest, "src", "a.txt"), "old", 0o644) // conflict
	os.MkdirAll(filepath.Join(dest, "src", "sub"), 0o755)      // merge
	os.MkdirAll(filepath.Join(dest, "src", "clash"), 0o755)    // folder where a file goes
	p, err := PlanDownload(context.Background(), c, []string{"/home/src"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 2 || p.Dirs != 2 || p.Links != 1 || p.Bytes != 3 || p.Conflicts != 1 || p.Sample[0] != "src/a.txt" {
		t.Fatalf("%+v", p)
	}
	if p.Errors.Count != 1 || !strings.Contains(p.Errors.List[0], "clash") {
		t.Fatalf("errors %+v", p.Errors)
	}
}

func TestPlanDownloadHostileNames(t *testing.T) {
	c, s, _ := remote(t)
	s.HostileSFTP("x/..", "x/.", `a\b`, "nul\x00", "\xff", "A", "a", "ok")
	p, err := PlanDownload(context.Background(), c, []string{"/hostile"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// ok and A plan; ".." "." a\b nul \xff are bad names; "a" differs from "A" only in case.
	if p.Files != 2 || p.Errors.Count != 6 {
		t.Fatalf("files %d errors %+v", p.Files, p.Errors)
	}
}

func TestPlanDownloadSelectedLinkIsFollowedOnce(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "real", "f"), "x", 0o644)
	os.Symlink("real", filepath.Join(root, "home", "ln"))
	p, err := PlanDownload(context.Background(), c, []string{"/home/ln"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Dirs != 1 || p.Files != 1 {
		t.Fatalf("%+v", p)
	}
}

func TestPlanDepthLimit(t *testing.T) {
	c, _, root := remote(t)
	d := filepath.Join(root, "home", "deep")
	for range MaxDepth + 2 {
		d = filepath.Join(d, "d")
	}
	os.MkdirAll(d, 0o755)
	if _, err := PlanDownload(context.Background(), c, []string{"/home/deep"}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "levels") {
		t.Fatalf("err %v", err)
	}
}

func TestPlanDownloadNeedsAFolder(t *testing.T) {
	c, _, _ := remote(t)
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o644)
	if _, err := PlanDownload(context.Background(), c, []string{"/home"}, f); err == nil {
		t.Fatal("planned into a file")
	}
	if _, err := PlanDownload(context.Background(), c, []string{"home"}, t.TempDir()); err == nil {
		t.Fatal("planned a relative source")
	}
}

func TestPlanUpload(t *testing.T) {
	c, _, root := remote(t)
	src := filepath.Join(t.TempDir(), "proj")
	write(t, filepath.Join(src, "a"), "aaa", 0o644)
	write(t, filepath.Join(src, "sub", "b"), "b", 0o644)
	os.Symlink("/etc/passwd", filepath.Join(src, "leak"))
	write(t, filepath.Join(root, "home", "proj", "a"), "old", 0o644)
	p, err := PlanUpload(context.Background(), c, []string{src}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 2 || p.Dirs != 2 || p.Links != 1 || p.Conflicts != 1 || p.Sample[0] != "proj/a" {
		t.Fatalf("%+v", p)
	}
	if _, err := PlanUpload(context.Background(), c, []string{src}, "home"); err == nil {
		t.Fatal("relative destination")
	}
}

func TestPlanUploadSelectedLinkIsFollowedOnce(t *testing.T) {
	c, _, _ := remote(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "real.txt"), "r", 0o644)
	os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "ln.txt"))
	p, err := PlanUpload(context.Background(), c, []string{filepath.Join(dir, "ln.txt")}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 1 || p.Links != 0 || p.Sample != nil {
		t.Fatalf("%+v", p)
	}
}

func TestPlanDelete(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "tree", "a"), "aa", 0o644)
	write(t, filepath.Join(root, "home", "tree", "sub", "b"), "b", 0o644)
	os.MkdirAll(filepath.Join(root, "home", "keep"), 0o755)
	os.Symlink("../keep", filepath.Join(root, "home", "tree", "to-keep"))
	p, err := PlanDelete(context.Background(), c, []string{"/home/tree"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 2 || p.Dirs != 2 || p.Links != 1 || p.Bytes != 3 {
		t.Fatalf("%+v", p)
	}
	for _, bad := range []string{"/", "/home", "home/tree"} {
		if _, err := PlanDelete(context.Background(), c, []string{bad}); err == nil {
			t.Fatalf("planned deleting %q", bad)
		}
	}
}

func TestPlanCancelled(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "x", "f"), "f", 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PlanDownload(ctx, c, []string{"/home/x"}, t.TempDir()); err == nil {
		t.Fatal("a cancelled plan succeeded")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/files -run TestPlan -v`
Expected: FAIL (`PlanDownload` undefined).

- [ ] **Step 3: Implement** — `internal/files/plan.go`

```go
package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

type Op string

const (
	OpUpload   Op = "upload"
	OpDownload Op = "download"
	OpDelete   Op = "delete"
)

// item is one planned entry.
type item struct {
	rel    string   // slash path under the destination; for delete, the absolute remote path
	src    string   // upload: OS path inside root; download: absolute remote path
	root   *os.Root // upload: the source's parent folder
	dir    bool
	size   int64
	mode   fs.FileMode
	mtime  time.Time
	exists bool // upload/download: something of the same kind is already there
}

// Plan is a walked, checked operation, ready to Run once.
type Plan struct {
	Op        Op
	Files     int
	Dirs      int
	Links     int // symlinks skipped inside folders (copy) or removed as links (delete)
	Bytes     int64
	Conflicts int      // existing files at file destinations
	Sample    []string // up to 5 conflicting paths, relative to the destination
	Errors    Errors
	Remote    []string // remote paths for the audit
	Local     []string // local paths for the audit

	items []item
	dest  string     // upload: the remote folder
	local *os.Root   // download: the destination folder
	roots []*os.Root // upload: one per source
}

// Close releases the plan's local folder handles.
func (p *Plan) Close() {
	if p.local != nil {
		p.local.Close()
	}
	for _, r := range p.roots {
		r.Close()
	}
}

var (
	errNotFolder  = errors.New("destination exists and is not a folder")
	errNotFile    = errors.New("destination exists and is not a file")
	errCaseClash  = errors.New("another name here differs only in case")
	errNotRegular = errors.New("not a regular file or folder")
)

// walker enforces the entry and depth limits and the cancel.
type walker struct {
	ctx  context.Context
	n    int
	p    *Plan
	seen map[string]map[string]bool // download: parent → lower-cased names
}

func (w *walker) step(depth int) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.n++; w.n > MaxPlanEntries {
		return fmt.Errorf("more than %d entries; pick fewer", MaxPlanEntries)
	}
	if depth > MaxDepth {
		return fmt.Errorf("folders nested more than %d levels", MaxDepth)
	}
	return nil
}

func (p *Plan) conflict(rel string) {
	p.Conflicts++
	if len(p.Sample) < 5 {
		p.Sample = append(p.Sample, rel)
	}
}

// PlanDownload walks remote sources (absolute paths) into the local folder dest.
func PlanDownload(ctx context.Context, c *sftp.Client, sources []string, dest string) (*Plan, error) {
	for _, s := range sources {
		if err := CheckAbs(s); err != nil {
			return nil, err
		}
	}
	if fi, err := os.Stat(dest); err != nil || !fi.IsDir() {
		return nil, errors.New("the download folder is missing or not a folder")
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return nil, err
	}
	p := &Plan{Op: OpDownload, local: root, Remote: sources, Local: []string{dest}}
	w := &walker{ctx: ctx, p: p, seen: map[string]map[string]bool{}}
	for _, s := range sources {
		fi, err := c.Stat(s) // a selected symlink is followed once
		if err != nil {
			p.Errors.Add(s, err)
			continue
		}
		if err := w.down(c, path.Dir(s), "", path.Base(s), fi, 0); err != nil {
			p.Close()
			return nil, err
		}
	}
	return p, nil
}

func (w *walker) down(c *sftp.Client, parentSrc, parentRel, name string, fi fs.FileInfo, depth int) error {
	if err := w.step(depth); err != nil {
		return err
	}
	p := w.p
	src := path.Join(parentSrc, name)
	if err := checkLocalName(name); err != nil {
		p.Errors.Add(parentSrc+"/"+name, err)
		return nil
	}
	low := strings.ToLower(name)
	if w.seen[parentRel] == nil {
		w.seen[parentRel] = map[string]bool{}
	}
	if w.seen[parentRel][low] {
		p.Errors.Add(src, errCaseClash)
		return nil
	}
	w.seen[parentRel][low] = true
	rel := path.Join(parentRel, name)
	it := item{rel: rel, src: src, size: fi.Size(), mode: fi.Mode(), mtime: fi.ModTime()}
	existing, lerr := p.local.Lstat(filepath.FromSlash(rel))
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		p.Links++
	case fi.IsDir():
		if lerr == nil {
			if !existing.IsDir() {
				p.Errors.Add(src, errNotFolder)
				return nil
			}
			it.exists = true
		}
		it.dir = true
		p.items = append(p.items, it)
		p.Dirs++
		fis, err := c.ReadDirContext(w.ctx, src)
		if err != nil {
			if w.ctx.Err() != nil {
				return w.ctx.Err()
			}
			p.Errors.Add(src, err)
			return nil
		}
		for _, cfi := range fis {
			if err := w.down(c, src, rel, cfi.Name(), cfi, depth+1); err != nil {
				return err
			}
		}
	case fi.Mode().IsRegular():
		if lerr == nil {
			if !existing.Mode().IsRegular() {
				p.Errors.Add(src, errNotFile)
				return nil
			}
			it.exists = true
			p.conflict(rel)
		}
		p.items = append(p.items, it)
		p.Files++
		p.Bytes += fi.Size()
	default:
		p.Errors.Add(src, errNotRegular)
	}
	return nil
}

// PlanUpload walks local sources (absolute paths) into the remote folder dest.
func PlanUpload(ctx context.Context, c *sftp.Client, sources []string, dest string) (*Plan, error) {
	if err := CheckAbs(dest); err != nil {
		return nil, err
	}
	if fi, err := c.Stat(dest); err != nil || !fi.IsDir() {
		return nil, errors.New("the upload folder is missing or not a folder")
	}
	p := &Plan{Op: OpUpload, dest: dest, Remote: []string{dest}, Local: sources}
	w := &walker{ctx: ctx, p: p}
	for _, s := range sources {
		if !filepath.IsAbs(s) {
			p.Close()
			return nil, errors.New("local sources must be absolute")
		}
		resolved := s
		if lfi, err := os.Lstat(s); err == nil && lfi.Mode()&fs.ModeSymlink != 0 {
			if r, err := filepath.EvalSymlinks(s); err == nil { // a selected symlink is followed once
				resolved = r
			}
		}
		root, err := os.OpenRoot(filepath.Dir(resolved))
		if err != nil {
			p.Errors.Add(s, err)
			continue
		}
		p.roots = append(p.roots, root)
		inRoot := filepath.Base(resolved)
		fi, err := root.Lstat(inRoot)
		if err != nil {
			p.Errors.Add(s, err)
			continue
		}
		if err := w.up(c, root, inRoot, filepath.Base(s), fi, 0); err != nil {
			p.Close()
			return nil, err
		}
	}
	return p, nil
}

func (w *walker) up(c *sftp.Client, root *os.Root, inRoot, rel string, fi fs.FileInfo, depth int) error {
	if err := w.step(depth); err != nil {
		return err
	}
	p := w.p
	if err := checkRemoteName(path.Base(rel)); err != nil {
		p.Errors.Add(inRoot, err)
		return nil
	}
	it := item{rel: rel, src: inRoot, root: root, size: fi.Size(), mode: fi.Mode(), mtime: fi.ModTime()}
	existing, rerr := c.Lstat(path.Join(p.dest, rel))
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		p.Links++
	case fi.IsDir():
		if rerr == nil {
			if !existing.IsDir() {
				p.Errors.Add(rel, errNotFolder)
				return nil
			}
			it.exists = true
		}
		it.dir = true
		p.items = append(p.items, it)
		p.Dirs++
		f, err := root.Open(inRoot)
		if err != nil {
			p.Errors.Add(rel, err)
			return nil
		}
		des, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			p.Errors.Add(rel, err)
			return nil
		}
		for _, de := range des {
			cfi, err := de.Info() // Lstat semantics
			if err != nil {
				p.Errors.Add(path.Join(rel, de.Name()), err)
				continue
			}
			if err := w.up(c, root, filepath.Join(inRoot, de.Name()), path.Join(rel, de.Name()), cfi, depth+1); err != nil {
				return err
			}
		}
	case fi.Mode().IsRegular():
		if rerr == nil {
			if !existing.Mode().IsRegular() {
				p.Errors.Add(rel, errNotFile)
				return nil
			}
			it.exists = true
			p.conflict(rel)
		}
		p.items = append(p.items, it)
		p.Files++
		p.Bytes += fi.Size()
	default:
		p.Errors.Add(rel, errNotRegular)
	}
	return nil
}

// PlanDelete walks remote paths depth first, never following a symlink, not
// even a selected one. Items come out children first.
func PlanDelete(ctx context.Context, c *sftp.Client, paths []string) (*Plan, error) {
	home, err := Home(c)
	if err != nil {
		return nil, err
	}
	for _, s := range paths {
		if err := checkMutable(s, home); err != nil {
			return nil, err
		}
	}
	p := &Plan{Op: OpDelete, Remote: paths}
	w := &walker{ctx: ctx, p: p}
	for _, s := range paths {
		fi, err := c.Lstat(s)
		if err != nil {
			p.Errors.Add(s, err)
			continue
		}
		if err := w.del(c, s, fi, 0); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (w *walker) del(c *sftp.Client, p string, fi fs.FileInfo, depth int) error {
	if err := w.step(depth); err != nil {
		return err
	}
	pl := w.p
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		pl.items = append(pl.items, item{rel: p})
		pl.Links++
	case fi.IsDir():
		fis, err := c.ReadDirContext(w.ctx, p)
		if err != nil {
			if w.ctx.Err() != nil {
				return w.ctx.Err()
			}
			pl.Errors.Add(p, err)
			return nil
		}
		for _, cfi := range fis {
			if err := checkRemoteName(cfi.Name()); err != nil {
				pl.Errors.Add(p, err) // the folder will not empty; its removal fails too
				continue
			}
			if err := w.del(c, path.Join(p, cfi.Name()), cfi, depth+1); err != nil {
				return err
			}
		}
		pl.items = append(pl.items, item{rel: p, dir: true})
		pl.Dirs++
	default:
		pl.items = append(pl.items, item{rel: p})
		pl.Files++
		pl.Bytes += fi.Size()
	}
	return nil
}
```

`TestPlanDownloadHostileNames` expects 6 errors: `..` (from `x/..`), `.` (from `x/.`), `a\b`, `nul\x00`, `\xff`, and `a` (case clash with `A`). If `ReadDirContext` drops `.` itself on this version, adjust the expectation to 5 and say so in the commit message; do not loosen the rule.

- [ ] **Step 4: Run**

Run: `go test ./internal/files -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/files
git commit -m "feat(files): bounded plans for upload, download, and delete"
```

---

### Task 6: Engine — run (copy, commit, delete, cancel, progress)

**Files:**
- Create: `internal/files/copy.go`
- Test: `internal/files/copy_test.go`

**Interfaces:**
- Consumes: Task 5's `Plan` (unexported `items`, `dest`, `local`, `roots`).
- Produces: `type Result struct { Copied, Skipped, Deleted int; Bytes int64; Errors Errors; Cancelled bool; Fatal error }`; `type Progress func(file string, done, total int64)`; `(*Plan).Run(ctx, c, overwrite bool, progress Progress) Result`. `Result.Errors` starts from the plan's errors. `progress` may be nil. Run may be called once.

- [ ] **Step 1: Write the failing test** — `internal/files/copy_test.go`

```go
package files

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func noParts(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".sshgate-part") {
			t.Errorf("part file left: %s", p)
		}
		return nil
	})
}

func TestDownloadRoundTrip(t *testing.T) {
	c, _, root := remote(t)
	src := filepath.Join(root, "home", "src")
	write(t, filepath.Join(src, "a.txt"), "alpha", 0o777|fs.ModeSetuid)
	write(t, filepath.Join(src, "ro", "b.txt"), "beta", 0o600)
	os.Chmod(filepath.Join(src, "ro"), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Join(src, "ro"), 0o755) })
	mt := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(filepath.Join(src, "a.txt"), mt, mt)
	dest := t.TempDir()
	p, err := PlanDownload(context.Background(), c, []string{"/home/src"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	var last int64
	r := p.Run(context.Background(), c, false, func(_ string, done, total int64) { last = done })
	p.Close()
	if r.Copied != 2 || r.Errors.Count != 0 || r.Bytes != 9 || last != 9 {
		t.Fatalf("%+v last %d", r, last)
	}
	a := filepath.Join(dest, "src", "a.txt")
	if read(t, a) != "alpha" {
		t.Fatal("content")
	}
	if fi, _ := os.Stat(a); fi.Mode().Perm() != 0o755 || fi.Mode()&fs.ModeSetuid != 0 || !fi.ModTime().Equal(mt) {
		t.Fatalf("a.txt mode %v mtime %v", fi.Mode(), fi.ModTime())
	}
	if fi, _ := os.Stat(filepath.Join(dest, "src", "ro")); fi.Mode().Perm() != 0o555 {
		t.Fatalf("ro folder mode %v", fi.Mode())
	}
	os.Chmod(filepath.Join(dest, "src", "ro"), 0o755)
	noParts(t, dest)
}

func TestUploadRoundTrip(t *testing.T) {
	c, _, root := remote(t)
	src := filepath.Join(t.TempDir(), "proj")
	write(t, filepath.Join(src, "x.sh"), "#!/bin/sh", 0o775)
	write(t, filepath.Join(src, "sub", "y"), "yy", 0o640)
	p, err := PlanUpload(context.Background(), c, []string{src}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	r := p.Run(context.Background(), c, false, nil)
	p.Close()
	if r.Copied != 2 || r.Errors.Count != 0 {
		t.Fatalf("%+v", r)
	}
	x := filepath.Join(root, "home", "proj", "x.sh")
	if read(t, x) != "#!/bin/sh" {
		t.Fatal("content")
	}
	if fi, _ := os.Stat(x); fi.Mode().Perm() != 0o775 {
		t.Fatalf("mode %v", fi.Mode())
	}
	noParts(t, filepath.Join(root, "home"))
}

func TestConflictsSkipOverwriteAndLateArrival(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "d", "same"), "new", 0o644)
	write(t, filepath.Join(root, "home", "d", "late"), "new", 0o644)
	dest := t.TempDir()
	write(t, filepath.Join(dest, "d", "same"), "old", 0o644)

	p, _ := PlanDownload(context.Background(), c, []string{"/home/d"}, dest)
	write(t, filepath.Join(dest, "d", "late"), "arrived after the plan", 0o644)
	r := p.Run(context.Background(), c, false, nil) // skip
	p.Close()
	if r.Skipped != 1 || r.Errors.Count != 1 || !strings.Contains(r.Errors.List[0], "late") {
		t.Fatalf("%+v", r)
	}
	if read(t, filepath.Join(dest, "d", "same")) != "old" || read(t, filepath.Join(dest, "d", "late")) != "arrived after the plan" {
		t.Fatal("skip replaced a file")
	}

	p, _ = PlanDownload(context.Background(), c, []string{"/home/d"}, dest)
	r = p.Run(context.Background(), c, true, nil) // overwrite
	p.Close()
	if r.Copied != 2 || read(t, filepath.Join(dest, "d", "same")) != "new" {
		t.Fatalf("%+v", r)
	}
	noParts(t, dest)
}

func TestDownloadNeverWritesThroughASymlink(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "d", "f"), "evil", 0o644)
	dest := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	write(t, outside, "safe", 0o644)
	write(t, filepath.Join(dest, "d", "f"), "old", 0o644)
	p, _ := PlanDownload(context.Background(), c, []string{"/home/d"}, dest)
	os.Remove(filepath.Join(dest, "d", "f"))
	os.Symlink(outside, filepath.Join(dest, "d", "f")) // swapped after the plan
	r := p.Run(context.Background(), c, true, nil)
	p.Close()
	if r.Copied != 0 || r.Errors.Count != 1 || read(t, outside) != "safe" {
		t.Fatalf("%+v, victim %q", r, read(t, outside))
	}
	noParts(t, dest)
}

func TestDownloadHostileStaysInside(t *testing.T) {
	c, s, _ := remote(t)
	s.HostileSFTP("x/..", `a\b`, "A", "a", "ok")
	dest := filepath.Join(t.TempDir(), "in")
	os.Mkdir(dest, 0o755)
	p, err := PlanDownload(context.Background(), c, []string{"/hostile"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	r := p.Run(context.Background(), c, false, nil)
	p.Close()
	if r.Copied != 2 {
		t.Fatalf("%+v", r)
	}
	if read(t, filepath.Join(dest, "hostile", "ok")) != "x" { // the 1 TiB size was a lie
		t.Fatal("content")
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 {
		t.Fatalf("something was written beside the picked folder: %v", entries)
	}
}

func TestCancelLeavesNoPartFile(t *testing.T) {
	c, s, root := remote(t)
	write(t, filepath.Join(root, "home", "big"), strings.Repeat("z", 1<<20), 0o644)
	dest := t.TempDir()
	p, _ := PlanDownload(context.Background(), c, []string{"/home/big"}, dest)
	gate := make(chan struct{})
	s.GateSFTP(gate)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result)
	go func() { done <- p.Run(ctx, c, false, nil) }()
	gate <- struct{}{} // one chunk through
	cancel()
	close(gate)
	r := <-done
	p.Close()
	if !r.Cancelled || r.Copied != 0 {
		t.Fatalf("%+v", r)
	}
	noParts(t, dest)
	if _, err := os.Stat(filepath.Join(dest, "big")); err == nil {
		t.Fatal("a cancelled download left the file")
	}
}

func TestDeleteNeverFollowsLinks(t *testing.T) {
	c, _, root := remote(t)
	home := filepath.Join(root, "home")
	write(t, filepath.Join(home, "keep", "precious"), "p", 0o644)
	write(t, filepath.Join(home, "tree", "a"), "a", 0o644)
	os.Symlink("../keep", filepath.Join(home, "tree", "to-keep"))
	os.Symlink("keep", filepath.Join(home, "direct"))
	p, err := PlanDelete(context.Background(), c, []string{"/home/tree", "/home/direct"})
	if err != nil {
		t.Fatal(err)
	}
	r := p.Run(context.Background(), c, false, nil)
	p.Close()
	if r.Deleted != 4 || r.Errors.Count != 0 {
		t.Fatalf("%+v", r)
	}
	if read(t, filepath.Join(home, "keep", "precious")) != "p" {
		t.Fatal("deleted through a symlink")
	}
	for _, gone := range []string{"tree", "direct"} {
		if _, err := os.Lstat(filepath.Join(home, gone)); err == nil {
			t.Fatalf("%s still there", gone)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/files -run 'Test(Download|Upload|Conflicts|Cancel|Delete)' -v`
Expected: FAIL (`p.Run` undefined).

- [ ] **Step 3: Implement** — `internal/files/copy.go`

```go
package files

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/pkg/sftp"
)

// Result is what a Run did. Errors starts with the plan's own errors.
type Result struct {
	Copied, Skipped, Deleted int
	Bytes                    int64
	Errors                   Errors
	Cancelled                bool
	Fatal                    error // ended the job early (connection lost, disk full)
}

// Progress reports bytes done of the plan's total, with the current file.
type Progress func(file string, done, total int64)

var (
	errSymlinkDest = errors.New("destination is a symlink; not written through")
	errAppeared    = errors.New("destination appeared after the plan; not replaced")
	errNoReplace   = errors.New("the server cannot replace files (no posix-rename)")
)

// Run carries out the plan once. overwrite applies to files that existed at
// plan time; nothing else is ever replaced.
func (p *Plan) Run(ctx context.Context, c *sftp.Client, overwrite bool, progress Progress) Result {
	r := Result{Errors: p.Errors}
	if progress == nil {
		progress = func(string, int64, int64) {}
	}
	if p.Op == OpDelete {
		p.runDelete(ctx, c, &r)
	} else {
		p.runCopy(ctx, c, overwrite, progress, &r)
	}
	r.Cancelled = ctx.Err() != nil
	return r
}

func (p *Plan) runCopy(ctx context.Context, c *sftp.Client, overwrite bool, progress Progress, r *Result) {
	var done int64
	var made []item // folders this run created; their final mode is set last
	defer func() {
		for i := len(made) - 1; i >= 0; i-- {
			p.setDirMode(c, made[i])
		}
	}()
	for _, it := range p.items {
		if ctx.Err() != nil {
			return
		}
		if it.dir {
			if it.exists {
				continue
			}
			if err := p.mkdir(c, it.rel); err != nil {
				if isFatal(err) {
					r.Fatal = err
					return
				}
				r.Errors.Add(it.rel, err)
				continue
			}
			made = append(made, it)
			continue
		}
		if it.exists && !overwrite {
			r.Skipped++
			continue
		}
		base := done
		prog := func(n int64) { progress(it.rel, base+n, p.Bytes) }
		var n int64
		var err error
		if p.Op == OpDownload {
			n, err = p.download(ctx, c, it, overwrite && it.exists, prog)
		} else {
			n, err = p.upload(ctx, c, it, overwrite && it.exists, prog)
		}
		done += n
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if isFatal(err) {
				r.Fatal = err
				return
			}
			r.Errors.Add(it.rel, err)
			continue
		}
		r.Copied++
		r.Bytes += n
	}
}

func (p *Plan) mkdir(c *sftp.Client, rel string) error {
	if p.Op == OpDownload {
		return p.local.Mkdir(filepath.FromSlash(rel), 0o700)
	}
	d := path.Join(p.dest, rel)
	if err := c.Mkdir(d); err != nil {
		return err
	}
	return c.Chmod(d, 0o700)
}

func (p *Plan) setDirMode(c *sftp.Client, it item) {
	if p.Op == OpDownload {
		p.local.Chmod(filepath.FromSlash(it.rel), downloadMode(it.mode))
		p.local.Chtimes(filepath.FromSlash(it.rel), it.mtime, it.mtime)
		return
	}
	d := path.Join(p.dest, it.rel)
	c.Chmod(d, uploadMode(it.mode))
	c.Chtimes(d, it.mtime, it.mtime)
}

// ctxWriter and ctxReader stop a copy at the next chunk once ctx is done,
// and report bytes so far.
type ctxWriter struct {
	ctx  context.Context
	w    io.Writer
	n    int64
	prog func(int64)
}

func (w *ctxWriter) Write(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(b)
	w.n += int64(n)
	w.prog(w.n)
	return n, err
}

type ctxReader struct {
	ctx  context.Context
	r    io.Reader
	n    int64
	prog func(int64)
}

func (r *ctxReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(b)
	r.n += int64(n)
	r.prog(r.n)
	return n, err
}

func (p *Plan) download(ctx context.Context, c *sftp.Client, it item, replace bool, prog func(int64)) (int64, error) {
	dir, name := path.Split(it.rel)
	part := filepath.FromSlash(path.Join(dir, partName(name)))
	final := filepath.FromSlash(it.rel)
	src, err := c.Open(it.src)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	dst, err := p.local.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	w := &ctxWriter{ctx: ctx, w: dst, prog: prog}
	_, err = src.WriteTo(w)
	if err == nil {
		err = dst.Chmod(downloadMode(it.mode))
	}
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = p.local.Chtimes(part, it.mtime, it.mtime)
	}
	if err == nil {
		err = commitLocal(p.local, part, final, replace)
	}
	if err != nil {
		p.local.Remove(part)
		return w.n, err
	}
	return w.n, nil
}

func commitLocal(root *os.Root, part, final string, replace bool) error {
	if fi, err := root.Lstat(final); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return errSymlinkDest
	}
	if replace {
		return root.Rename(part, final)
	}
	if err := root.Link(part, final); err == nil {
		return root.Remove(part)
	}
	// Link failed: either the name exists, or this filesystem has no hard
	// links (exFAT, some network shares). Check, then rename.
	if _, err := root.Lstat(final); err == nil {
		return errAppeared
	}
	return root.Rename(part, final)
}

func (p *Plan) upload(ctx context.Context, c *sftp.Client, it item, replace bool, prog func(int64)) (int64, error) {
	final := path.Join(p.dest, it.rel)
	part := path.Join(path.Dir(final), partName(path.Base(final)))
	src, err := it.root.Open(it.src)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	if st, err := src.Stat(); err != nil || !st.Mode().IsRegular() {
		return 0, errNotRegular
	}
	dst, err := c.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return 0, err
	}
	r := &ctxReader{ctx: ctx, r: src, prog: prog}
	_, err = dst.ReadFromWithConcurrency(r, 64)
	if err == nil {
		err = dst.Chmod(uploadMode(it.mode))
	}
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = c.Chtimes(part, it.mtime, it.mtime) // by path: pkg/sftp has no File.Chtimes; part is ours (O_EXCL)
	}
	if err == nil {
		err = commitRemote(c, part, final, replace)
	}
	if err != nil {
		c.Remove(part)
		return r.n, err
	}
	return r.n, nil
}

func commitRemote(c *sftp.Client, part, final string, replace bool) error {
	if fi, err := c.Lstat(final); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return errSymlinkDest
	}
	if replace {
		if _, ok := c.HasExtension("posix-rename@openssh.com"); !ok {
			return errNoReplace
		}
		return c.PosixRename(part, final)
	}
	if err := c.Link(part, final); err == nil {
		return c.Remove(part)
	}
	if _, err := c.Lstat(final); err == nil {
		return errAppeared
	}
	return c.Rename(part, final) // plain SFTP rename never replaces on OpenSSH
}

func (p *Plan) runDelete(ctx context.Context, c *sftp.Client, r *Result) {
	for _, it := range p.items {
		if ctx.Err() != nil {
			return
		}
		var err error
		if it.dir {
			err = c.RemoveDirectory(it.rel)
		} else {
			err = c.Remove(it.rel) // a symlink goes as the link itself
		}
		if err != nil {
			if isFatal(err) {
				r.Fatal = err
				return
			}
			r.Errors.Add(it.rel, err)
			continue
		}
		r.Deleted++
	}
}
```

Notes for the implementer:
- `TestCancelLeavesNoPartFile` relies on the gate: `sftp.File.WriteTo` issues several concurrent reads; each waits for one gate value. After the first value the test cancels and closes the gate so the remaining reads finish; `ctxWriter.Write` then returns `ctx.Err()` and `download` removes the part file. If `WriteTo` buffers the first chunk without calling `Write` before the others complete, the test still ends cancelled (the next `Write` fails); it must not hang. If it flakes, send the one token only after a short `time.Sleep(20 * time.Millisecond)` — never loosen the assertions.
- `c.Remove` on a symlink removes the link (OpenSSH `remove` is `unlink`), as the test checks.

- [ ] **Step 4: Run**

Run: `go test ./internal/files -race -count=3 -v`
Expected: PASS three times (no flakes). Then `go vet ./... && GOOS=windows go vet ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/files
git commit -m "feat(files): run plans through part files with no-replace commits, cancel, and progress"
```

---

### Task 7: Hub — `files.list`, `files.mkdir`, `files.rename`, strict params, `FileRecord`, protocol 4

**Files:**
- Modify: `internal/broker/audit.go` (`FileRecord`, `WriteFile`)
- Create: `internal/hub/files.go`
- Modify: `internal/hub/uidoor.go` (register the file methods), `internal/hub/idle.go` (`ProtocolVersion = 4`), `desktop/test/fixtures/fakeHub.mjs` (protocol 4), `desktop/src/shared/protocol.ts` (`PROTOCOL_VERSION = 4` only; the rest of protocol.ts is Task 9)
- Test: `internal/hub/files_test.go`, `internal/hub/mcpdoor_test.go` (method list)

**Interfaces:**
- Consumes: Task 2 (`Manager.SFTP`, `ErrNoSFTP`), Task 4 (`files.List`, `Mkdir`, `Rename`, `CheckAbs`).
- Produces: `broker.FileRecord` + `(*Audit).WriteFile`; `(*Hub).auditFile(broker.FileRecord)`; `strictParams(raw json.RawMessage, v any, allowed ...string) error`; `(*Hub).sftpFor(server string) (*sshx.Manager, sshx.DialConfig, error)`; `fileErr(error) error`; `registerFileMethods(s *rpc.Server, h *Hub) (closeAll func())` — Task 8 extends the same function with jobs. Test helpers `filesHub`, `waitNote` in `files_test.go` are reused by Task 8.

- [ ] **Step 1: Audit record** — in `internal/broker/audit.go`, after `ConfigRecord`:

```go
// FileRecord is an audit line for a file operation from the app. Transfers
// and deletes write two, at start and at end; mkdir and rename write one.
type FileRecord struct {
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"`            // always "file"
	Phase      string    `json:"phase,omitempty"` // start, end; empty for mkdir and rename
	Action     string    `json:"action"`          // upload, download, delete, mkdir, rename
	Server     string    `json:"server"`
	Host       string    `json:"host,omitempty"`
	Port       int       `json:"port,omitempty"`
	Remote     []string  `json:"remote,omitempty"` // first 20
	Local      []string  `json:"local,omitempty"`  // first 20; transfers only
	From       string    `json:"from,omitempty"`
	To         string    `json:"to,omitempty"`
	Conflict   string    `json:"conflict,omitempty"`
	Files      int       `json:"files,omitempty"`
	Bytes      int64     `json:"bytes,omitempty"`
	Skipped    int       `json:"skipped,omitempty"`
	ErrorCount int       `json:"errorCount,omitempty"`
	Cancelled  bool      `json:"cancelled,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}
```
and next to `WriteConfig`:
```go
func (a *Audit) WriteFile(r FileRecord) error {
	r.Kind = "file"
	return a.append(r)
}
```

- [ ] **Step 2: Write the failing test** — `internal/hub/files_test.go`

```go
package hub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

type filesFixture struct {
	h     *Hub
	c     *rpc.Client
	w     io.Writer // raw door input, for notifications
	notes chan note
	srv   *sshtest.Server
	root  string // the SFTP root on disk; the server's /home is <root>/home
	store string
}

// filesHub: an unlocked vault with "fs" (pinned) and "new" (no pin), both
// key auth against one in-process SSH server that serves SFTP.
func filesHub(t *testing.T) filesFixture {
	t.Helper()
	srv := sshtest.Start(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "home"), 0o755)
	srv.ServeSFTP(root)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "id")
	os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600)
	f := &config.File{Version: 1, Servers: []config.Server{
		{Name: "fs", Host: srv.Host, Port: srv.Port, User: "u", Auth: "key", KeyPath: keyPath, HostKey: srv.Fingerprint()},
		{Name: "new", Host: srv.Host, Port: srv.Port, User: "u", Auth: "key", KeyPath: keyPath},
	}}
	var mk []byte
	f.KDF, mk = testVault(t)
	h, store := newHubAt(t, f, mk)
	unlockForTest(h)
	c, w, notes := startTermDoor(t, h)
	return filesFixture{h: h, c: c, w: w, notes: notes, srv: srv, root: root, store: store}
}

// waitNote returns the params of the next notification method for id,
// skipping everything else.
func waitNote(t *testing.T, notes chan note, method, id string) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case n := <-notes:
			if n.method != method {
				continue
			}
			var p map[string]any
			json.Unmarshal(n.params, &p)
			if p["id"] == id {
				return p
			}
		case <-deadline:
			t.Fatalf("no %s for %s", method, id)
		}
	}
}

func fileRecords(t *testing.T, store string) []map[string]any {
	t.Helper()
	_, recs := readAudit(t, store)
	var out []map[string]any
	for _, r := range recs {
		if r["kind"] == "file" {
			out = append(out, r)
		}
	}
	return out
}

func TestFilesListMkdirRename(t *testing.T) {
	fx := filesHub(t)
	ctx := context.Background()
	os.WriteFile(filepath.Join(fx.root, "home", "a.txt"), []byte("abc"), 0o644)
	var l struct {
		Status  string `json:"status"`
		Path    string `json:"path"`
		Entries []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"entries"`
	}
	if err := fx.c.Call(ctx, "files.list", map[string]any{"server": "fs", "path": ""}, &l); err != nil {
		t.Fatal(err)
	}
	if l.Status != "listed" || l.Path != "/home" || len(l.Entries) != 1 || l.Entries[0].Name != "a.txt" {
		t.Fatalf("%+v", l)
	}
	if err := fx.c.Call(ctx, "files.mkdir", map[string]any{"server": "fs", "path": "/home/d"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := fx.c.Call(ctx, "files.rename", map[string]any{"server": "fs", "from": "/home/a.txt", "to": "/home/d/b.txt"}, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(fx.root, "home", "d", "b.txt")); string(b) != "abc" {
		t.Fatal("rename")
	}
	recs := fileRecords(t, fx.store)
	if len(recs) != 2 || recs[0]["action"] != "mkdir" || recs[1]["action"] != "rename" ||
		recs[1]["from"] != "/home/a.txt" || recs[1]["to"] != "/home/d/b.txt" || recs[0]["server"] != "fs" {
		t.Fatalf("audit %v", recs)
	}
}

func TestFilesStrictParams(t *testing.T) {
	fx := filesHub(t)
	ctx := context.Background()
	for _, raw := range []string{
		`{"server":"fs","Path":"/etc"}`,              // case variant
		`{"server":"fs","path":"","extra":1}`,        // unknown key
		`{"server":"fs","server":"new","path":""}`,   // duplicate key
		`["fs"]`,                                     // not an object
	} {
		var re *rpc.Error
		err := fx.c.Call(ctx, "files.list", json.RawMessage(raw), nil)
		if !errors.As(err, &re) || re.Code != -32602 {
			t.Errorf("%s: want invalid params, got %v", raw, err)
		}
	}
}

func TestFilesNeedUnlockAndPaths(t *testing.T) {
	fx := filesHub(t)
	ctx := context.Background()
	if err := fx.c.Call(ctx, "files.mkdir", map[string]any{"server": "fs", "path": "rel"}, nil); err == nil {
		t.Fatal("relative mkdir accepted")
	}
	if err := fx.c.Call(ctx, "files.rename", map[string]any{"server": "fs", "from": "/home", "to": "/x"}, nil); err == nil {
		t.Fatal("renamed home")
	}
	fx.h.Lock()
	err := fx.c.Call(ctx, "files.list", map[string]any{"server": "fs", "path": ""}, nil)
	if err == nil || !strings.Contains(err.Error(), ErrLocked.Error()) {
		t.Fatalf("locked list: %v", err)
	}
}

func TestFilesListTrustsAnUnpinnedHost(t *testing.T) {
	fx := filesHub(t)
	ctx := context.Background()
	var r map[string]any
	if err := fx.c.Call(ctx, "files.list", map[string]any{"server": "new", "path": ""}, &r); err != nil {
		t.Fatal(err)
	}
	if r["status"] != "hostKeyUnknown" {
		t.Fatalf("%v", r)
	}
	trust := map[string]any{"fingerprint": r["fingerprint"], "keyType": r["keyType"]}
	if err := fx.c.Call(ctx, "files.list", map[string]any{"server": "new", "path": "", "trustHostKey": trust}, &r); err != nil {
		t.Fatal(err)
	}
	if r["status"] != "listed" {
		t.Fatalf("%v", r)
	}
	f, _ := config.Load(fx.store)
	if s, _ := f.FindServer("new"); s.HostKey != fx.srv.Fingerprint() {
		t.Fatalf("pin %q", s.HostKey)
	}
}

func TestFilesPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	fx := filesHub(t)
	d := filepath.Join(fx.root, "home", "shut")
	os.Mkdir(d, 0o000)
	t.Cleanup(func() { os.Chmod(d, 0o755) })
	err := fx.c.Call(context.Background(), "files.list", map[string]any{"server": "fs", "path": "/home/shut"}, nil)
	if err == nil || err.Error() != "permission denied" {
		t.Fatalf("err %v", err)
	}
}
```

In `mcpdoor_test.go`'s `TestMCPDoorRejectsUIOnlyMethods`, add `"files.list", "files.mkdir", "files.rename", "files.plan", "files.run", "files.cancelAll"` to the method list.

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/hub -run 'TestFiles|TestMCPDoorRejects' -v`
Expected: FAIL (`method not found: files.list`).

- [ ] **Step 4: Implement** — `internal/hub/files.go`

```go
package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"strconv"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/files"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshx"
)

var errBadParams = &rpc.Error{Code: -32602, Message: "invalid params"}

// strictParams decodes a JSON object into v after checking its top-level
// keys: each must be one of allowed, spelled exactly, and appear once.
// encoding/json alone would match "Sources" to "sources" and let the last
// duplicate win, which would let a renderer slip a key past Electron main.
func strictParams(raw json.RawMessage, v any, allowed ...string) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errBadParams
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		k, ok := tok.(string)
		if err != nil || !ok {
			return errBadParams
		}
		if !slices.Contains(allowed, k) || seen[k] {
			return &rpc.Error{Code: -32602, Message: "invalid params: unexpected key " + strconv.Quote(k)}
		}
		seen[k] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return errBadParams
		}
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return errBadParams
	}
	return nil
}

// fileErr keeps the app's message short for the common cases.
func fileErr(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return errors.New("permission denied")
	}
	return err
}

// sftpFor resolves server as term.open does (unlocked vault, known server)
// and returns its manager, with keepalive on.
func (h *Hub) sftpFor(server string) (*sshx.Manager, sshx.DialConfig, error) {
	_ = h.Reload()
	dc, err := h.resolveForTerm(server)
	if err != nil {
		return nil, dc, err
	}
	mgr := h.Registry().Get(server, dc)
	mgr.StartKeepalive(30*time.Second, nil)
	return mgr, dc, nil
}

// pinnedClient is sftpFor plus the shared client, for methods that need a
// pin already (everything but files.list).
func (h *Hub) pinnedClient(server string) (*sshx.Manager, sshx.DialConfig, error) {
	mgr, dc, err := h.sftpFor(server)
	if err != nil {
		return nil, dc, err
	}
	if dc.HostKey == "" {
		return nil, dc, errors.New("trust the host key first: open the Files tab")
	}
	return mgr, dc, nil
}

func first20(s []string) []string { return s[:min(len(s), 20)] }

// auditFile never takes h.mu.
func (h *Hub) auditFile(r broker.FileRecord) {
	if h.audit != nil {
		r.Time = time.Now()
		r.Remote, r.Local = first20(r.Remote), first20(r.Local)
		_ = h.audit.WriteFile(r)
	}
}

// listTimeout bounds one listing (see files.List's ponytail note).
var listTimeout = 30 * time.Second

// registerFileMethods adds the files.* methods for one UI door and returns
// a func that cancels its jobs and waits for them (Task 8 adds the jobs).
func registerFileMethods(s *rpc.Server, h *Hub) (closeAll func()) {
	empty := map[string]any{}
	s.HandleRequest("files.list", func(ctx context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			Server       string `json:"server"`
			Path         string `json:"path"`
			TrustHostKey *struct {
				Fingerprint string `json:"fingerprint"`
				KeyType     string `json:"keyType"`
			} `json:"trustHostKey"`
		}
		if err := strictParams(raw, &p, "server", "path", "trustHostKey"); err != nil {
			return nil, err
		}
		if tk := p.TrustHostKey; tk != nil && (tk.Fingerprint == "" || tk.KeyType == "") {
			return nil, &rpc.Error{Code: -32602, Message: "trustHostKey needs fingerprint and keyType"}
		}
		_ = h.Reload()
		dc, err := h.resolveForTerm(p.Server)
		if err != nil {
			return nil, err
		}
		trust := p.TrustHostKey != nil && dc.HostKey == ""
		if trust {
			dc.HostKey, dc.HostKeyAlgo = p.TrustHostKey.Fingerprint, p.TrustHostKey.KeyType
		}
		mgr := h.Registry().Get(p.Server, dc)
		mgr.StartKeepalive(30*time.Second, nil)
		c, err := mgr.SFTP()
		if err != nil {
			if res := h.hostKeyResult(p.Server, dc, trust, err); res != nil {
				return res, nil
			}
			return nil, err
		}
		if trust {
			if err := h.recordTrust(p.Server, dc); err != nil {
				h.Registry().Close(p.Server)
				return nil, err
			}
		}
		ctx, cancel := context.WithTimeout(ctx, listTimeout)
		defer cancel()
		l, err := files.List(ctx, c, p.Path)
		if err != nil {
			return nil, fileErr(err)
		}
		return map[string]any{"status": "listed", "path": l.Path, "entries": l.Entries, "truncated": l.Truncated, "bad": l.Bad}, nil
	})
	s.HandleRequest("files.mkdir", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			Server string `json:"server"`
			Path   string `json:"path"`
		}
		if err := strictParams(raw, &p, "server", "path"); err != nil {
			return nil, err
		}
		if err := files.CheckAbs(p.Path); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: err.Error()}
		}
		mgr, dc, err := h.pinnedClient(p.Server)
		if err != nil {
			return nil, err
		}
		c, err := mgr.SFTP()
		if err == nil {
			err = files.Mkdir(c, p.Path)
		}
		rec := broker.FileRecord{Action: "mkdir", Server: p.Server, Host: dc.Host, Port: dc.Port, Remote: []string{p.Path}}
		if err != nil {
			rec.Reason = err.Error()
		}
		h.auditFile(rec)
		if err != nil {
			return nil, fileErr(err)
		}
		return empty, nil
	})
	s.HandleRequest("files.rename", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			Server string `json:"server"`
			From   string `json:"from"`
			To     string `json:"to"`
		}
		if err := strictParams(raw, &p, "server", "from", "to"); err != nil {
			return nil, err
		}
		mgr, dc, err := h.pinnedClient(p.Server)
		if err != nil {
			return nil, err
		}
		c, err := mgr.SFTP()
		if err == nil {
			err = files.Rename(c, p.From, p.To)
		}
		rec := broker.FileRecord{Action: "rename", Server: p.Server, Host: dc.Host, Port: dc.Port, From: p.From, To: p.To}
		if err != nil {
			rec.Reason = err.Error()
		}
		h.auditFile(rec)
		if err != nil {
			return nil, fileErr(err)
		}
		return empty, nil
	})
	return func() {}
}
```

In `uidoor.go`, next to `closeTerms := registerTermMethods(s, h)`:
```go
	closeFiles := registerFileMethods(s, h)
	defer closeFiles() // after Serve: cancels this door's jobs and waits up to 5 s
```
and add `files.*` to the comment above ServeUIDoor's method list (see CLAUDE.md update in Task 12).

In `idle.go`: `ProtocolVersion = 4`. In `desktop/src/shared/protocol.ts`: `export const PROTOCOL_VERSION = 4`. In `desktop/test/fixtures/fakeHub.mjs`: `mode === 'badproto' ? 99 : 4`.

- [ ] **Step 5: Run**

Run: `go test ./internal/hub -race -run 'TestFiles|TestMCPDoor|TestUIDoor' -v && (cd desktop && npm test)`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/broker internal/hub desktop/src/shared/protocol.ts desktop/test/fixtures/fakeHub.mjs
git commit -m "feat(hub): files.list, files.mkdir, files.rename with strict params and audit (protocol 4)"
```

---

### Task 8: Hub — jobs (`files.plan`, `files.run`, `files.cancel`, `files.cancelAll`)

**Files:**
- Create: `internal/hub/filejobs.go`
- Modify: `internal/hub/files.go` (`registerFileMethods` registers the job methods and returns the door's close func), `internal/hub/hub.go` (`files *jobSet` in `Hub`, built in `New`), `internal/hub/hosts.go` (end a server's jobs before `h.reg.Close`), `internal/hub/live_test.go` (`files.list` per server)
- Test: `internal/hub/filejobs_test.go`

**Interfaces:**
- Consumes: Task 5–6 (`files.PlanUpload/PlanDownload/PlanDelete`, `Plan.Run`, `Result`), Task 7 (`strictParams`, `pinnedClient`, `auditFile`, `filesHub`, `waitNote`).
- Produces: notifications `files.planned`, `files.progress`, `files.done` exactly as in "Interfaces at a glance"; `(*jobSet).endServer(name, reason string)`; test knobs `planTTL` (10 min) and `progressEvery` (250 ms).

- [ ] **Step 1: Write the failing test** — `internal/hub/filejobs_test.go`

```go
package hub

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func plan(t *testing.T, fx filesFixture, params map[string]any) map[string]any {
	t.Helper()
	if err := fx.c.Call(context.Background(), "files.plan", params, nil); err != nil {
		t.Fatal(err)
	}
	return waitNote(t, fx.notes, "files.planned", params["id"].(string))
}

func run(t *testing.T, fx filesFixture, id, conflict string) map[string]any {
	t.Helper()
	if err := fx.c.Call(context.Background(), "files.run", map[string]any{"id": id, "conflict": conflict}, nil); err != nil {
		t.Fatal(err)
	}
	return waitNote(t, fx.notes, "files.done", id)
}

func noPartFiles(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".sshgate-part") {
			t.Errorf("part file left: %s", p)
		}
		return nil
	})
}

func TestJobUploadDownloadDelete(t *testing.T) {
	fx := filesHub(t)
	src := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "a"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b"), []byte("b"), 0o644)

	p := plan(t, fx, map[string]any{"id": "j1", "server": "fs", "op": "upload", "sources": []string{src}, "dest": "/home"})
	if p["files"] != float64(2) || p["dirs"] != float64(2) || p["bytes"] != float64(4) || p["conflicts"].(map[string]any)["count"] != float64(0) {
		t.Fatalf("planned %v", p)
	}
	d := run(t, fx, "j1", "skip")
	if d["copied"] != float64(2) || d["op"] != "upload" || d["cancelled"] != false {
		t.Fatalf("done %v", d)
	}
	if b, _ := os.ReadFile(filepath.Join(fx.root, "home", "proj", "sub", "b")); string(b) != "b" {
		t.Fatal("upload content")
	}

	// Again: a conflict, skipped.
	p = plan(t, fx, map[string]any{"id": "j2", "server": "fs", "op": "upload", "sources": []string{src}, "dest": "/home"})
	if p["conflicts"].(map[string]any)["count"] != float64(2) {
		t.Fatalf("planned %v", p)
	}
	if d := run(t, fx, "j2", "skip"); d["skipped"] != float64(2) {
		t.Fatalf("done %v", d)
	}

	dest := t.TempDir()
	plan(t, fx, map[string]any{"id": "j3", "server": "fs", "op": "download", "sources": []string{"/home/proj"}, "dest": dest})
	if d := run(t, fx, "j3", "skip"); d["copied"] != float64(2) {
		t.Fatalf("done %v", d)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "proj", "a")); string(b) != "aaa" {
		t.Fatal("download content")
	}

	p = plan(t, fx, map[string]any{"id": "j4", "server": "fs", "op": "delete", "sources": []string{"/home/proj"}})
	if p["files"] != float64(2) || p["dirs"] != float64(2) {
		t.Fatalf("planned %v", p)
	}
	if d := run(t, fx, "j4", "skip"); d["deleted"] != float64(4) {
		t.Fatalf("done %v", d)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "home", "proj")); err == nil {
		t.Fatal("not deleted")
	}

	var ups []map[string]any
	for _, r := range fileRecords(t, fx.store) {
		if r["action"] == "upload" {
			ups = append(ups, r)
		}
	}
	if len(ups) != 4 || ups[0]["phase"] != "start" || ups[1]["phase"] != "end" ||
		ups[0]["local"].([]any)[0] != src || ups[0]["remote"].([]any)[0] != "/home" || ups[1]["files"] != float64(2) {
		t.Fatalf("upload audit %v", ups)
	}
}

func TestJobCancelWhileRunningAndWhileLocked(t *testing.T) {
	fx := filesHub(t)
	os.WriteFile(filepath.Join(fx.root, "home", "big"), []byte(strings.Repeat("z", 1<<20)), 0o644)
	dest := t.TempDir()
	plan(t, fx, map[string]any{"id": "c1", "server": "fs", "op": "download", "sources": []string{"/home/big"}, "dest": dest})
	gate := make(chan struct{})
	fx.srv.GateSFTP(gate)
	if err := fx.c.Call(context.Background(), "files.run", map[string]any{"id": "c1", "conflict": "skip"}, nil); err != nil {
		t.Fatal(err)
	}
	gate <- struct{}{}
	fx.h.Lock() // a running transfer survives a lock, and can still be cancelled
	sendNote(t, fx.w, "files.cancel", map[string]any{"id": "c1"})
	close(gate)
	d := waitNote(t, fx.notes, "files.done", "c1")
	if d["cancelled"] != true {
		t.Fatalf("done %v", d)
	}
	noPartFiles(t, dest)
}

func TestJobRunNeedsUnlockAndAPlan(t *testing.T) {
	fx := filesHub(t)
	ctx := context.Background()
	if err := fx.c.Call(ctx, "files.run", map[string]any{"id": "nope", "conflict": "skip"}, nil); err == nil {
		t.Fatal("ran an unknown job")
	}
	os.WriteFile(filepath.Join(fx.root, "home", "f"), []byte("f"), 0o644)
	plan(t, fx, map[string]any{"id": "r1", "server": "fs", "op": "download", "sources": []string{"/home/f"}, "dest": t.TempDir()})
	if err := fx.c.Call(ctx, "files.plan", map[string]any{"id": "r1", "server": "fs", "op": "delete", "sources": []string{"/home/f"}}, nil); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if err := fx.c.Call(ctx, "files.run", map[string]any{"id": "r1", "conflict": "ask"}, nil); err == nil {
		t.Fatal("conflict must be overwrite or skip")
	}
	fx.h.Lock()
	if err := fx.c.Call(ctx, "files.run", map[string]any{"id": "r1", "conflict": "skip"}, nil); err == nil {
		t.Fatal("ran while locked")
	}
	if err := fx.c.Call(ctx, "files.plan", map[string]any{"id": "r2", "server": "fs", "op": "delete", "sources": []string{"/home/f"}}, nil); err == nil {
		t.Fatal("planned while locked")
	}
}

func TestJobStrictPlanParams(t *testing.T) {
	fx := filesHub(t)
	raw := `{"id":"s1","server":"fs","op":"upload","sources":["/tmp/x"],"dest":"/home","Sources":["/etc/passwd"]}`
	if err := fx.c.Call(context.Background(), "files.plan", json.RawMessage(raw), nil); err == nil {
		t.Fatal("a case-variant key was accepted")
	}
}

func TestJobEndedByServerChange(t *testing.T) {
	fx := filesHub(t)
	os.WriteFile(filepath.Join(fx.root, "home", "big"), []byte(strings.Repeat("z", 1<<20)), 0o644)
	plan(t, fx, map[string]any{"id": "e1", "server": "fs", "op": "download", "sources": []string{"/home/big"}, "dest": t.TempDir()})
	gate := make(chan struct{})
	fx.srv.GateSFTP(gate)
	if err := fx.c.Call(context.Background(), "files.run", map[string]any{"id": "e1", "conflict": "skip"}, nil); err != nil {
		t.Fatal(err)
	}
	gate <- struct{}{}
	go func() { time.Sleep(50 * time.Millisecond); close(gate) }()
	if err := fx.h.DeleteServer("fs"); err != nil {
		t.Fatal(err)
	}
	d := waitNote(t, fx.notes, "files.done", "e1")
	if d["cancelled"] != true || d["reason"] != "server changed" {
		t.Fatalf("done %v", d)
	}
}

func TestJobCancelAllAndPlanExpiry(t *testing.T) {
	fx := filesHub(t)
	old := planTTL
	planTTL = 50 * time.Millisecond
	t.Cleanup(func() { planTTL = old })
	os.WriteFile(filepath.Join(fx.root, "home", "f"), []byte("f"), 0o644)
	plan(t, fx, map[string]any{"id": "x1", "server": "fs", "op": "delete", "sources": []string{"/home/f"}})
	if d := waitNote(t, fx.notes, "files.done", "x1"); d["reason"] != "plan expired" {
		t.Fatalf("done %v", d)
	}
	planTTL = old
	plan(t, fx, map[string]any{"id": "x2", "server": "fs", "op": "delete", "sources": []string{"/home/f"}})
	var r map[string]any
	if err := fx.c.Call(context.Background(), "files.cancelAll", map[string]any{}, &r); err != nil || r["cancelled"] != float64(1) {
		t.Fatalf("cancelAll %v %v", r, err)
	}
	if d := waitNote(t, fx.notes, "files.done", "x2"); d["cancelled"] != true {
		t.Fatalf("done %v", d)
	}
	if b, _ := os.ReadFile(filepath.Join(fx.root, "home", "f")); string(b) != "f" {
		t.Fatal("a cancelled delete deleted")
	}
}

func TestJobProgressIsRateLimited(t *testing.T) {
	fx := filesHub(t)
	old := progressEvery
	progressEvery = time.Hour
	t.Cleanup(func() { progressEvery = old })
	os.WriteFile(filepath.Join(fx.root, "home", "big"), []byte(strings.Repeat("z", 4<<20)), 0o644)
	plan(t, fx, map[string]any{"id": "p1", "server": "fs", "op": "download", "sources": []string{"/home/big"}, "dest": t.TempDir()})
	if err := fx.c.Call(context.Background(), "files.run", map[string]any{"id": "p1", "conflict": "skip"}, nil); err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		m := <-fx.notes
		if m.method == "files.progress" {
			n++
		}
		if m.method == "files.done" {
			break
		}
	}
	if n > 1 {
		t.Fatalf("%d progress notes with a 1 h interval", n)
	}
}
```

`sendNote` (in `term_test.go`) writes `files.cancel` as a notification through the fixture's raw writer.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/hub -run TestJob -v`
Expected: FAIL (`method not found: files.plan`).

- [ ] **Step 3: Implement** — `internal/hub/filejobs.go`

```go
package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/files"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshx"
)

// Test knobs.
var (
	planTTL       = 10 * time.Minute       // a planned job not run by then is dropped
	progressEvery = 250 * time.Millisecond // files.progress per job at most this often
	cancelGrace   = 5 * time.Second        // then a stuck job's SFTP channel is closed
)

// jobSet is every job in the hub, so a server change can end its jobs.
type jobSet struct {
	mu  sync.Mutex
	all map[*fileJob]struct{}
}

func newJobSet() *jobSet { return &jobSet{all: map[*fileJob]struct{}{}} }

func (s *jobSet) add(j *fileJob)    { s.mu.Lock(); s.all[j] = struct{}{}; s.mu.Unlock() }
func (s *jobSet) remove(j *fileJob) { s.mu.Lock(); delete(s.all, j); s.mu.Unlock() }

// endServer ends name's jobs. Callers run it before closing the server's
// connection, so "server changed" is the reason reported, not a lost connection.
func (s *jobSet) endServer(name, reason string) {
	s.mu.Lock()
	var js []*fileJob
	for j := range s.all {
		if j.server == name {
			js = append(js, j)
		}
	}
	s.mu.Unlock()
	for _, j := range js {
		j.end(reason)
	}
}

// fileJob is one plan → run → done. state moves planning → planned →
// running → done under mu; reason, once set, is why it ended early.
type fileJob struct {
	id, server string
	op         files.Op
	dc         sshx.DialConfig
	h          *Hub
	door       *fileDoor
	c          *sftp.Client
	ctx        context.Context
	cancel     context.CancelFunc

	mu       sync.Mutex
	state    string
	reason   string
	plan     *files.Plan
	conflict string
	expire   *time.Timer
	once     sync.Once
	done     chan struct{}
}

// fileDoor is one UI door's jobs, by the client's id.
type fileDoor struct {
	h    *Hub
	s    *rpc.Server
	mu   sync.Mutex
	jobs map[string]*fileJob
}

func (d *fileDoor) get(id string) *fileJob {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.jobs[id]
}

// end stops the job for reason. It never blocks on the network: a planned
// job finishes on another goroutine; a running one is cancelled and, if its
// server does not answer within cancelGrace, its channel is closed.
func (j *fileJob) end(reason string) {
	j.mu.Lock()
	if j.reason == "" {
		j.reason = reason
	}
	st := j.state
	j.mu.Unlock()
	j.cancel()
	switch st {
	case "planned":
		go j.finish(files.Result{Cancelled: true}, nil)
	case "running":
		time.AfterFunc(cancelGrace, func() {
			select {
			case <-j.done:
			default:
				j.c.Close()
			}
		})
	}
}

func (j *fileJob) planWalk(sources []string, dest string) {
	var p *files.Plan
	var err error
	switch j.op {
	case files.OpUpload:
		p, err = files.PlanUpload(j.ctx, j.c, sources, dest)
	case files.OpDownload:
		p, err = files.PlanDownload(j.ctx, j.c, sources, dest)
	default:
		p, err = files.PlanDelete(j.ctx, j.c, sources)
	}
	if err != nil {
		j.finish(files.Result{Cancelled: j.ctx.Err() != nil}, err)
		return
	}
	j.mu.Lock()
	if j.reason != "" { // ended while planning
		j.mu.Unlock()
		p.Close()
		j.finish(files.Result{Cancelled: true}, nil)
		return
	}
	j.plan, j.state = p, "planned"
	j.expire = time.AfterFunc(planTTL, func() { j.end("plan expired") })
	j.mu.Unlock()
	j.door.s.Notify("files.planned", map[string]any{
		"id": j.id, "files": p.Files, "dirs": p.Dirs, "links": p.Links, "bytes": p.Bytes,
		"conflicts":  map[string]any{"count": p.Conflicts, "sample": nonNil(p.Sample)},
		"errorCount": p.Errors.Count, "errors": nonNil(p.Errors.List),
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// run starts a planned job; conflict is "overwrite" or "skip".
func (j *fileJob) run(conflict string) error {
	j.mu.Lock()
	if j.state != "planned" || j.reason != "" {
		j.mu.Unlock()
		return fmt.Errorf("job %q is not ready to run", j.id)
	}
	j.state, j.conflict = "running", conflict
	j.expire.Stop()
	p := j.plan
	j.mu.Unlock()
	j.h.auditFile(broker.FileRecord{Phase: "start", Action: string(j.op), Server: j.server, Host: j.dc.Host, Port: j.dc.Port,
		Remote: p.Remote, Local: p.Local, Conflict: conflict, Files: p.Files, Bytes: p.Bytes})
	go func() {
		var last time.Time
		r := p.Run(j.ctx, j.c, conflict == "overwrite", func(file string, done, total int64) {
			if time.Since(last) < progressEvery {
				return
			}
			last = time.Now()
			j.door.s.Notify("files.progress", map[string]any{"id": j.id, "file": file, "done": done, "total": total})
		})
		j.finish(r, nil)
	}()
	return nil
}

// finish runs once: it releases the job, writes the end audit record for a
// job that ran, and sends files.done.
func (j *fileJob) finish(r files.Result, planErr error) {
	j.once.Do(func() {
		j.mu.Lock()
		ran, p, reason := j.state == "running", j.plan, j.reason
		if j.expire != nil {
			j.expire.Stop()
		}
		j.state = "done"
		j.mu.Unlock()
		j.cancel()
		if p != nil {
			p.Close()
		}
		j.c.Close()
		j.h.files.remove(j)
		j.door.mu.Lock()
		if j.door.jobs[j.id] == j {
			delete(j.door.jobs, j.id)
		}
		j.door.mu.Unlock()
		r.Cancelled = r.Cancelled || reason != "" // ended early by end()
		switch {
		case reason != "":
		case planErr != nil:
			reason = planErr.Error()
		case r.Fatal != nil:
			reason = r.Fatal.Error()
		}
		if ran {
			j.h.auditFile(broker.FileRecord{Phase: "end", Action: string(j.op), Server: j.server, Host: j.dc.Host, Port: j.dc.Port,
				Remote: p.Remote, Local: p.Local, Conflict: j.conflict, Files: r.Copied + r.Deleted, Bytes: r.Bytes,
				Skipped: r.Skipped, ErrorCount: r.Errors.Count, Cancelled: r.Cancelled, Reason: reason})
		}
		j.door.s.Notify("files.done", map[string]any{
			"id": j.id, "op": j.op, "copied": r.Copied, "skipped": r.Skipped, "deleted": r.Deleted, "bytes": r.Bytes,
			"errorCount": r.Errors.Count, "errors": nonNil(r.Errors.List), "cancelled": r.Cancelled, "reason": reason,
		})
		close(j.done)
	})
}

// registerJobMethods adds files.plan/run/cancel/cancelAll for one door and
// returns its close func: cancel every job, then wait up to 5 s.
func registerJobMethods(s *rpc.Server, h *Hub) (closeAll func()) {
	d := &fileDoor{h: h, s: s, jobs: map[string]*fileJob{}}
	s.HandleRequest("files.plan", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			ID      string   `json:"id"`
			Server  string   `json:"server"`
			Op      files.Op `json:"op"`
			Sources []string `json:"sources"`
			Dest    string   `json:"dest"`
		}
		if err := strictParams(raw, &p, "id", "server", "op", "sources", "dest"); err != nil {
			return nil, err
		}
		switch {
		case !termIDRe.MatchString(p.ID):
			return nil, &rpc.Error{Code: -32602, Message: "id must be 1-64 characters of A-Z a-z 0-9 _ -"}
		case p.Op != files.OpUpload && p.Op != files.OpDownload && p.Op != files.OpDelete:
			return nil, &rpc.Error{Code: -32602, Message: "op must be upload, download, or delete"}
		case len(p.Sources) == 0 || len(p.Sources) > 1000:
			return nil, &rpc.Error{Code: -32602, Message: "sources: 1 to 1000 paths"}
		case (p.Op == files.OpDelete) != (p.Dest == ""):
			return nil, &rpc.Error{Code: -32602, Message: "dest is required for upload and download, and absent for delete"}
		}
		mgr, dc, err := h.pinnedClient(p.Server)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		j := &fileJob{id: p.ID, server: p.Server, op: p.Op, dc: dc, h: h, door: d, ctx: ctx, cancel: cancel, state: "planning", done: make(chan struct{})}
		d.mu.Lock()
		_, dup := d.jobs[p.ID]
		if !dup {
			d.jobs[p.ID] = j
		}
		d.mu.Unlock()
		if dup {
			cancel()
			return nil, fmt.Errorf("job %q already exists", p.ID)
		}
		c, err := mgr.NewSFTP()
		if err != nil {
			cancel()
			d.mu.Lock()
			delete(d.jobs, p.ID)
			d.mu.Unlock()
			return nil, err
		}
		j.c = c
		h.files.add(j)
		go j.planWalk(p.Sources, p.Dest)
		return map[string]any{}, nil
	})
	s.HandleRequest("files.run", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			ID       string `json:"id"`
			Conflict string `json:"conflict"`
		}
		if err := strictParams(raw, &p, "id", "conflict"); err != nil {
			return nil, err
		}
		if p.Conflict != "overwrite" && p.Conflict != "skip" {
			return nil, &rpc.Error{Code: -32602, Message: "conflict must be overwrite or skip"}
		}
		if h.Locked() {
			return nil, ErrLocked
		}
		j := d.get(p.ID)
		if j == nil {
			return nil, fmt.Errorf("no job %q", p.ID)
		}
		return map[string]any{}, j.run(p.Conflict)
	})
	// A notification, on the read loop: it only cancels. Allowed while locked.
	s.Handle("files.cancel", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID string `json:"id"`
		}
		if err := strictParams(raw, &p, "id"); err != nil {
			fmt.Fprintf(os.Stderr, "files.cancel: malformed notification: %v\n", err)
			return nil, err
		}
		if j := d.get(p.ID); j != nil {
			j.end("cancelled")
		}
		return map[string]any{}, nil
	})
	cancelAll := func(reason string) []*fileJob {
		d.mu.Lock()
		js := make([]*fileJob, 0, len(d.jobs))
		for _, j := range d.jobs {
			js = append(js, j)
		}
		d.mu.Unlock()
		for _, j := range js {
			j.end(reason)
		}
		return js
	}
	// For Electron main after a renderer crash; not in the renderer whitelist.
	s.HandleRequest("files.cancelAll", func(context.Context, json.RawMessage) (any, error) {
		return map[string]int{"cancelled": len(cancelAll("renderer reloaded"))}, nil
	})
	return func() {
		js := cancelAll("app closed")
		deadline := time.After(5 * time.Second)
		for _, j := range js {
			select {
			case <-j.done:
			case <-deadline:
				return
			}
		}
	}
}
```

Wire it:
- `files.go`: at the end of `registerFileMethods`, replace `return func() {}` with `return registerJobMethods(s, h)`.
- `hub.go`: add `files *jobSet // every file job; see filejobs.go` to `Hub`, and `files: newJobSet(),` in `New`'s struct literal.
- `hosts.go`: in `SaveServer`, inside `if dialChanged(before, after) {`, call `h.files.endServer(name, "server changed")` before `h.reg.Close(name)`; in `DeleteServer` and `ForgetHostKey`, call `h.files.endServer(name, "server changed")` right before `h.reg.Close(name)`.

A planned job whose `end` runs `finish` in a goroutine makes `files.done` arrive shortly after; tests wait for it with `waitNote`.

- [ ] **Step 4: Live test** — in `internal/hub/live_test.go`'s `liveOpen`, after `term.close`:

```go
	var l map[string]any
	if err := c.Call(ctx, "files.list", map[string]any{"server": server, "path": ""}, &l); err != nil {
		t.Errorf("files.list: %v", err)
	} else if l["status"] != "listed" {
		t.Errorf("files.list: %v", l)
	}
```

- [ ] **Step 5: Run**

Run: `go test ./internal/hub -race -count=3 -run 'TestJob|TestFiles' -v && go test -race ./... && go vet ./... && GOOS=windows go vet ./...`
Expected: PASS (three runs, no flakes).

- [ ] **Step 6: Commit**

```bash
git add internal/hub
git commit -m "feat(hub): files.plan/run jobs with cancel, expiry, server-change end, and start/end audit"
```

---

### Task 9: Electron main and preload — grants, the `files.plan`/`files.run` relay, native download conflicts

**Files:**
- Create: `desktop/src/shared/display.ts`, `desktop/src/main/files.ts`
- Modify: `desktop/src/shared/protocol.ts`, `desktop/src/main/ipc.ts`, `desktop/src/main/main.ts`, `desktop/src/main/window.ts`, `desktop/src/preload/preload.ts`, `desktop/src/renderer/transport.ts`
- Test: `desktop/test/files-main.test.ts`, `desktop/test/preload.test.ts`, `desktop/test/ipc.test.ts`, `desktop/test/window.test.ts`

**Interfaces:**
- Consumes: the UI door methods from Tasks 7–8.
- Produces:
  - `shared/display.ts`: `displayText(s: string): string`.
  - `shared/protocol.ts`: `FileEntry`, `FileListing`, `FilesListResult`, `FileOp`, `FilesPlanned`, `FilesProgress`, `FilesDone`, `FileGrant`; `HubEvent` gains `files.planned|progress|done`; `REQUEST_METHODS` gains `files.list`, `files.mkdir`, `files.rename`; `FILES_RELAYED = ['files.plan', 'files.run']`; `NOTIFY_METHODS` gains `files.cancel`.
  - `main/files.ts`: `GRANT_TTL_MS`, `MAX_DROP`, `class Grants { add(path, kind, server): string; take(token, kind, server): string; clear() }`, `interface DialogLike`, `class FilesRelay { grants; pickUpload(p); pickDownloadDir(p); grantDropped(p); call(method, params); onNotification(method, params); onState(s); reset() }`.
  - Preload bridge (`window.sshmcp`) gains `pickUpload(server, folder, mode)`, `pickDownloadDir(server)`, `grantDropped(files, server)`.
  - `transport.ts` `hub` gains `filesList`, `filesMkdir`, `filesRename`, `filesPlan`, `filesRun`, `filesCancel`, `pickUpload`, `pickDownloadDir`, `grantDropped`.
  - `recoverRenderer(hub, win, reason, policy, onReset?)` also calls `files.cancelAll` and `onReset`.

- [ ] **Step 1: Shared types** — append to `desktop/src/shared/protocol.ts`:

```ts
// Files (slice 3a). Local paths never appear here: the renderer only holds
// tokens from Electron main (FileGrant); main swaps them for paths.
export interface FileEntry { name: string; size: number; mode: number; mtime: number; kind: 'dir' | 'file' | 'link' | 'other'; target?: string }
export interface FileListing { status: 'listed'; path: string; entries: FileEntry[]; truncated: boolean; bad: number }
export type FilesListResult = FileListing | HostKeyUnknown | HostKeyMismatch
export type FileOp = 'upload' | 'download' | 'delete'
export interface FilesPlanned {
  id: string; files: number; dirs: number; links: number; bytes: number
  conflicts: { count: number; sample: string[] }; errorCount: number; errors: string[]
}
export interface FilesProgress { id: string; file: string; done: number; total: number }
export interface FilesDone {
  id: string; op: FileOp; copied: number; skipped: number; deleted: number; bytes: number
  errorCount: number; errors: string[]; cancelled: boolean; reason: string
}
export interface FileGrant { token: string; name: string }
```
Add to `HubEvent`:
```ts
  | { method: 'files.planned'; params: FilesPlanned }
  | { method: 'files.progress'; params: FilesProgress }
  | { method: 'files.done'; params: FilesDone }
```
`REQUEST_METHODS`: add `'files.list', 'files.mkdir', 'files.rename'`. Add:
```ts
// Relayed through Electron main's FilesRelay (tokens → paths, native download
// conflicts), never straight through relayCall.
export const FILES_RELAYED = ['files.plan', 'files.run'] as const
```
`NOTIFY_METHODS`: add `'files.cancel'`. (`files.cancelAll` stays main-only, like `term.closeAll`.)

`desktop/src/shared/display.ts`:
```ts
// Server-sourced text shown in the app: controls, bidi overrides and
// isolates, zero-width characters, and line separators become a visible
// \uXXXX escape, so one name cannot pose as another.
const HIDDEN = /[\u0000-\u001f\u007f-\u009f​-‏‪-‮  ⁠-⁤⁦-⁩﻿]/g

export const displayText = (s: string): string =>
  s.replace(HIDDEN, (c) => '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0'))
```

- [ ] **Step 2: Write the failing tests**

`desktop/test/files-main.test.ts`:
```ts
import { describe, expect, it, vi } from 'vitest'
import { FilesRelay, Grants, GRANT_TTL_MS, MAX_DROP } from '../src/main/files'
import { displayText } from '../src/shared/display'

const fakeHub = () => ({ call: vi.fn(async (_m: string, _p?: unknown) => ({})), notify: vi.fn() })
const fakeDialog = (open: string[] = [], response = 0) => ({
  showOpenDialog: vi.fn(async () => ({ canceled: open.length === 0, filePaths: open })),
  showMessageBox: vi.fn(async () => ({ response, checkboxChecked: false })),
})
const win = {} as never

describe('Grants', () => {
  it('are single-use, bound to kind and server, and expire', () => {
    let now = 0
    const g = new Grants(() => now)
    const t = g.add('/a', 'read', 'box')
    expect(() => g.take(t, 'writeDir', 'box')).toThrow('not granted')
    const u = g.add('/a', 'read', 'box')
    expect(() => g.take(u, 'read', 'other')).toThrow('not granted')
    const v = g.add('/a', 'read', 'box')
    expect(g.take(v, 'read', 'box')).toBe('/a')
    expect(() => g.take(v, 'read', 'box')).toThrow('not granted')
    const w = g.add('/a', 'read', 'box')
    now = GRANT_TTL_MS + 1
    expect(() => g.take(w, 'read', 'box')).toThrow('not granted')
    expect(() => g.take('/etc/passwd', 'read', 'box')).toThrow('not granted')
    const x = g.add('/a', 'read', 'box')
    g.clear()
    expect(() => g.take(x, 'read', 'box')).toThrow('not granted')
  })
})

describe('FilesRelay', () => {
  it('names the server and folder in the upload dialog and hands out tokens', async () => {
    const d = fakeDialog(['/Users/me/proj'])
    const r = new FilesRelay(fakeHub(), d, () => win)
    const picks = await r.pickUpload({ server: 'box', folder: '/home/u', mode: 'folder' })
    expect(d.showOpenDialog.mock.calls[0][1]).toMatchObject({ title: 'Upload to box:/home/u', properties: ['openDirectory', 'multiSelections'] })
    expect(picks).toEqual([{ token: expect.stringMatching(/^g-[0-9a-f]{32}$/), name: 'proj' }])
    expect(picks[0].token).not.toContain('/Users')
  })

  it('rebuilds files.plan from its allowlist and swaps tokens', async () => {
    const h = fakeHub()
    const r = new FilesRelay(h, fakeDialog(['/Users/me/a']), () => win)
    const [{ token }] = await r.pickUpload({ server: 'box', folder: '/home/u', mode: 'files' })
    await r.call('files.plan', { id: 'j1', server: 'box', op: 'upload', sources: [token], dest: '/home/u', Sources: ['/etc/passwd'], extra: 1 })
    expect(h.call).toHaveBeenCalledWith('files.plan', { id: 'j1', server: 'box', op: 'upload', sources: ['/Users/me/a'], dest: '/home/u' })
  })

  it('refuses raw paths, reused tokens, and tokens for another server', async () => {
    const h = fakeHub()
    const r = new FilesRelay(h, fakeDialog(['/Users/me/a']), () => win)
    await expect(r.call('files.plan', { id: 'j', server: 'box', op: 'upload', sources: ['/Users/me/.ssh/id_ed25519'], dest: '/tmp' })).rejects.toThrow('not granted')
    const [{ token }] = await r.pickUpload({ server: 'box', folder: '/', mode: 'files' })
    await expect(r.call('files.plan', { id: 'j', server: 'other', op: 'upload', sources: [token], dest: '/tmp' })).rejects.toThrow('not granted')
    await expect(r.call('files.plan', { id: 'j', server: 'box', op: 'upload', sources: [token], dest: '/tmp' })).rejects.toThrow('not granted')
    await expect(r.call('files.plan', { id: 'j', server: 'box', op: 'download', sources: ['/etc'], dest: '/Users/me' })).rejects.toThrow('not granted')
    expect(h.call).not.toHaveBeenCalled()
  })

  it('decides download conflicts in the native dialog', async () => {
    const h = fakeHub()
    const d = fakeDialog(['/Users/me/dl'], 2)
    const r = new FilesRelay(h, d, () => win)
    const dir = await r.pickDownloadDir({ server: 'box' })
    await r.call('files.plan', { id: 'd1', server: 'box', op: 'download', sources: ['/home/u/x'], dest: dir!.token })
    r.onNotification('files.planned', { id: 'd1', conflicts: { count: 3, sample: ['x/a', 'x/‮b'] } })
    await expect(r.call('files.run', { id: 'd1', conflict: 'overwrite' })).rejects.toThrow('native dialog')
    await r.call('files.run', { id: 'd1', conflict: 'ask' })
    expect(d.showMessageBox.mock.calls[0][1]).toMatchObject({ buttons: ['Cancel', 'Skip existing', 'Overwrite all'], defaultId: 0, cancelId: 0 })
    expect(String(d.showMessageBox.mock.calls[0][1].detail)).toContain(displayText('x/‮b'))
    expect(h.call).toHaveBeenLastCalledWith('files.run', { id: 'd1', conflict: 'overwrite' })
  })

  it('cancels a download when the native dialog is cancelled, and skips when nothing conflicts', async () => {
    const h = fakeHub()
    const r = new FilesRelay(h, fakeDialog(['/Users/me/dl'], 0), () => win)
    const a = await r.pickDownloadDir({ server: 'box' })
    await r.call('files.plan', { id: 'd2', server: 'box', op: 'download', sources: ['/x'], dest: a!.token })
    r.onNotification('files.planned', { id: 'd2', conflicts: { count: 1, sample: ['x'] } })
    expect(await r.call('files.run', { id: 'd2', conflict: 'ask' })).toEqual({ cancelled: true })
    expect(h.notify).toHaveBeenCalledWith('files.cancel', { id: 'd2' })
    const b = await r.pickDownloadDir({ server: 'box' })
    await r.call('files.plan', { id: 'd3', server: 'box', op: 'download', sources: ['/x'], dest: b!.token })
    r.onNotification('files.planned', { id: 'd3', conflicts: { count: 0, sample: [] } })
    await r.call('files.run', { id: 'd3', conflict: 'ask' })
    expect(h.call).toHaveBeenLastCalledWith('files.run', { id: 'd3', conflict: 'skip' })
  })

  it('relays upload and delete runs as asked, and only overwrite or skip', async () => {
    const h = fakeHub()
    const r = new FilesRelay(h, fakeDialog(), () => win)
    await r.call('files.run', { id: 'u1', conflict: 'overwrite', extra: 1 })
    expect(h.call).toHaveBeenLastCalledWith('files.run', { id: 'u1', conflict: 'overwrite' })
    await expect(r.call('files.run', { id: 'u1', conflict: 'ask' })).rejects.toThrow()
  })

  it('drops grants on lock, when the hub stops, and on reset', async () => {
    const r = new FilesRelay(fakeHub(), fakeDialog(['/a', '/b', '/c']), () => win)
    const [a] = await r.pickUpload({ server: 'box', folder: '/', mode: 'files' })
    r.onNotification('locked', { reason: 'idle' })
    expect(() => r.grants.take(a.token, 'read', 'box')).toThrow()
    const [b] = await r.pickUpload({ server: 'box', folder: '/', mode: 'files' })
    r.onState({ kind: 'restarting', attempt: 1, inMs: 1000 })
    expect(() => r.grants.take(b.token, 'read', 'box')).toThrow()
    const [c] = await r.pickUpload({ server: 'box', folder: '/', mode: 'files' })
    r.reset()
    expect(() => r.grants.take(c.token, 'read', 'box')).toThrow()
  })

  it('grants dropped paths only when absolute and not too many', () => {
    const r = new FilesRelay(fakeHub(), fakeDialog(), () => win)
    expect(r.grantDropped({ server: 'box', paths: ['/Users/me/a'] })).toEqual([{ token: expect.any(String), name: 'a' }])
    expect(() => r.grantDropped({ server: 'box', paths: ['rel'] })).toThrow()
    expect(() => r.grantDropped({ server: 'box', paths: Array(MAX_DROP + 1).fill('/a') })).toThrow()
    expect(() => r.grantDropped({ server: 'box', paths: '/a' })).toThrow()
  })
})
```

`desktop/test/preload.test.ts`:
```ts
import { describe, expect, it, vi } from 'vitest'

const exposed: Record<string, Record<string, (...a: unknown[]) => unknown>> = {}
const invoke = vi.fn(async (..._a: unknown[]) => [])
vi.mock('electron', () => ({
  contextBridge: { exposeInMainWorld: (k: string, v: Record<string, (...a: unknown[]) => unknown>) => { exposed[k] = v } },
  ipcRenderer: { invoke, send: vi.fn(), on: vi.fn(), removeListener: vi.fn() },
  webUtils: { getPathForFile: (f: File) => (f.name === 'real' ? '/Users/me/real' : '') },
}))

await import('../src/preload/preload')

describe('preload grantDropped', () => {
  const grant = (...a: unknown[]) => exposed.sshmcp.grantDropped(...a) as Promise<unknown>
  it('sends only real file paths', async () => {
    await grant([new File([], 'real'), new File([], 'made-by-script')], 'box')
    expect(invoke).toHaveBeenCalledWith('files:grantDropped', { server: 'box', paths: ['/Users/me/real'] })
  })
  it('refuses anything but an array of Files, and too many', async () => {
    await expect(grant('x', 'box')).rejects.toThrow()
    await expect(grant([{ name: 'real' }], 'box')).rejects.toThrow()
    await expect(grant(Array(1001).fill(new File([], 'real')), 'box')).rejects.toThrow()
  })
})
```
If vitest rejects top-level `await import` in this config, use `beforeAll(async () => { await import(...) })`.

In `desktop/test/window.test.ts`, every expected order that has `'term.closeAll'` gets `'files.cancelAll'` right after it, and add:
```ts
  it('drops file grants after a crash', async () => {
    const order: string[] = []
    const reset = vi.fn()
    await recoverRenderer(okHub(order), fakeWin(order), 'crashed', new CrashPolicy(), reset)
    expect(reset).toHaveBeenCalledTimes(1)
  })
```

In `desktop/test/ipc.test.ts`: `relayCall(h, 'files.plan', {})` and `relayCall(h, 'files.cancelAll', {})` must reject with "not allowed"; the existing `registerIpc(...)` call gets a fourth argument `{ call: vi.fn(), onNotification: vi.fn(), onState: vi.fn() } as never`; add a test that a trusted `hub:call` for `files.plan` goes to the relay's `call`, not to `hub.call`.

- [ ] **Step 3: Run to verify they fail**

Run: `cd desktop && npm test`
Expected: FAIL (`../src/main/files` not found, etc.).

- [ ] **Step 4: Implement** — `desktop/src/main/files.ts`

```ts
import { randomBytes } from 'node:crypto'
import * as path from 'node:path'
import type { BrowserWindow, MessageBoxOptions, MessageBoxReturnValue, OpenDialogOptions, OpenDialogReturnValue } from 'electron'
import type { HubState } from '../shared/protocol'
import { displayText } from '../shared/display'

export type GrantKind = 'read' | 'writeDir'
interface Grant { path: string; kind: GrantKind; server: string; expires: number }

export const GRANT_TTL_MS = 5 * 60_000
export const MAX_DROP = 1000

// Grants are the only way a local path reaches the hub: the renderer holds
// opaque tokens, each good once, for one kind and one server, for 5 minutes.
export class Grants {
  private m = new Map<string, Grant>()
  constructor(private now: () => number = Date.now) {}
  add(p: string, kind: GrantKind, server: string): string {
    const t = 'g-' + randomBytes(16).toString('hex')
    this.m.set(t, { path: p, kind, server, expires: this.now() + GRANT_TTL_MS })
    return t
  }
  take(token: unknown, kind: GrantKind, server: string): string {
    const g = typeof token === 'string' ? this.m.get(token) : undefined
    if (typeof token === 'string') this.m.delete(token)
    if (!g || g.kind !== kind || g.server !== server || this.now() > g.expires) throw new Error('file access not granted')
    return g.path
  }
  clear(): void { this.m.clear() }
}

// Called as property accesses at call time, so e2e can replace them.
export interface DialogLike {
  showOpenDialog(win: BrowserWindow, o: OpenDialogOptions): Promise<OpenDialogReturnValue>
  showMessageBox(win: BrowserWindow, o: MessageBoxOptions): Promise<MessageBoxReturnValue>
}

interface HubLike {
  call(method: string, params?: unknown): Promise<unknown>
  notify(method: string, params?: unknown): void
}

const str = (v: unknown): v is string => typeof v === 'string'
const strs = (v: unknown): v is string[] => Array.isArray(v) && v.every(str)
const bad = () => new Error('invalid files request')

export class FilesRelay {
  readonly grants: Grants
  private downloads = new Map<string, { folder: string; conflicts: number; sample: string[] }>()

  constructor(private hub: HubLike, private dialog: DialogLike, private getWindow: () => BrowserWindow | undefined, now?: () => number) {
    this.grants = new Grants(now)
  }

  private win(): BrowserWindow {
    const w = this.getWindow()
    if (!w) throw new Error('no window')
    return w
  }

  async pickUpload(p: unknown): Promise<{ token: string; name: string }[]> {
    const { server, folder, mode } = (p ?? {}) as Record<string, unknown>
    if (!str(server) || !str(folder) || !['files', 'folder', 'both'].includes(mode as string)) throw bad()
    const properties: OpenDialogOptions['properties'] = mode === 'files' ? ['openFile', 'multiSelections']
      : mode === 'folder' ? ['openDirectory', 'multiSelections'] : ['openFile', 'openDirectory', 'multiSelections']
    const r = await this.dialog.showOpenDialog(this.win(), { title: `Upload to ${displayText(server)}:${displayText(folder)}`, buttonLabel: 'Upload', properties })
    if (r.canceled) return []
    return r.filePaths.map((fp) => ({ token: this.grants.add(fp, 'read', server), name: path.basename(fp) }))
  }

  async pickDownloadDir(p: unknown): Promise<{ token: string; name: string } | null> {
    const { server } = (p ?? {}) as Record<string, unknown>
    if (!str(server)) throw bad()
    const r = await this.dialog.showOpenDialog(this.win(), { title: `Download from ${displayText(server)} to…`, buttonLabel: 'Download here', properties: ['openDirectory', 'createDirectory'] })
    if (r.canceled || r.filePaths.length !== 1) return null
    return { token: this.grants.add(r.filePaths[0], 'writeDir', server), name: path.basename(r.filePaths[0]) }
  }

  grantDropped(p: unknown): { token: string; name: string }[] {
    const { server, paths } = (p ?? {}) as Record<string, unknown>
    if (!str(server) || !strs(paths) || paths.length > MAX_DROP || !paths.every((x) => path.isAbsolute(x))) throw bad()
    return paths.map((fp) => ({ token: this.grants.add(fp, 'read', server), name: path.basename(fp) }))
  }

  // files.plan rebuilt from its allowlist, tokens swapped for paths.
  private plan(params: unknown): Record<string, unknown> {
    const { id, server, op, sources, dest } = (params ?? {}) as Record<string, unknown>
    if (!str(id) || !str(server) || !strs(sources)) throw bad()
    if (op === 'upload' && str(dest)) {
      return { id, server, op, sources: sources.map((t) => this.grants.take(t, 'read', server)), dest }
    }
    if (op === 'download') {
      const folder = this.grants.take(dest, 'writeDir', server)
      this.downloads.set(id, { folder, conflicts: 0, sample: [] })
      return { id, server, op, sources, dest: folder }
    }
    if (op === 'delete') return { id, server, op, sources }
    throw bad()
  }

  async call(method: string, params: unknown): Promise<unknown> {
    if (method === 'files.plan') return this.hub.call('files.plan', this.plan(params))
    if (method !== 'files.run') throw bad()
    const { id, conflict } = (params ?? {}) as Record<string, unknown>
    if (!str(id)) throw bad()
    const d = this.downloads.get(id)
    if (!d) {
      if (conflict !== 'skip' && conflict !== 'overwrite') throw bad()
      return this.hub.call('files.run', { id, conflict })
    }
    if (d.conflicts === 0) return this.hub.call('files.run', { id, conflict: 'skip' })
    if (conflict !== 'ask') throw new Error('download conflicts are decided in the native dialog')
    const n = d.conflicts
    const r = await this.dialog.showMessageBox(this.win(), {
      type: 'question', buttons: ['Cancel', 'Skip existing', 'Overwrite all'], defaultId: 0, cancelId: 0, noLink: true,
      message: `${n} ${n === 1 ? 'file already exists' : 'files already exist'} in ${d.folder}`,
      detail: d.sample.map(displayText).join('\n'),
    })
    if (r.response === 1) return this.hub.call('files.run', { id, conflict: 'skip' })
    if (r.response === 2) return this.hub.call('files.run', { id, conflict: 'overwrite' })
    this.hub.notify('files.cancel', { id })
    return { cancelled: true }
  }

  onNotification(method: string, params: unknown): void {
    const p = (params ?? {}) as { id?: string; conflicts?: { count?: number; sample?: string[] } }
    if (method === 'files.planned' && p.id) {
      const d = this.downloads.get(p.id)
      if (d) { d.conflicts = p.conflicts?.count ?? 0; d.sample = p.conflicts?.sample ?? [] }
    }
    if (method === 'files.done' && p.id) this.downloads.delete(p.id)
    if (method === 'locked') this.grants.clear()
  }

  onState(s: HubState): void {
    if (s.kind !== 'running') { this.grants.clear(); this.downloads.clear() }
  }

  // The renderer was reloaded: its tokens die with it.
  reset(): void { this.grants.clear() }
}
```

`desktop/src/main/ipc.ts`:
- import `FILES_RELAYED` and `FilesRelay` (type).
- `const relayed = new Set<string>(FILES_RELAYED)`.
- `registerIpc(hub, getWindow, isTrusted, files: Pick<FilesRelay, 'call' | 'onNotification' | 'onState' | 'pickUpload' | 'pickDownloadDir' | 'grantDropped'>)`:
```ts
  ipcMain.handle('hub:call', (e, method: unknown, params: unknown) =>
    guard(isTrusted, e, () => (typeof method === 'string' && relayed.has(method) ? files.call(method, params) : relayCall(hub, method, params))))
  ipcMain.handle('files:pickUpload', (e, p: unknown) => guard(isTrusted, e, () => files.pickUpload(p)))
  ipcMain.handle('files:pickDownloadDir', (e, p: unknown) => guard(isTrusted, e, () => files.pickDownloadDir(p)))
  ipcMain.handle('files:grantDropped', (e, p: unknown) => guard(isTrusted, e, async () => files.grantDropped(p)))
  hub.on('notification', (method: string, params: unknown) => {
    files.onNotification(method, params)
    getWindow()?.webContents.send('hub:event', { method, params })
  })
  hub.on('state', (s: HubState) => { files.onState(s); getWindow()?.webContents.send('hub:state', s) })
```
(replacing the existing two `hub.on` lines).

`desktop/src/main/main.ts`: `import { app, BrowserWindow, dialog, session, … } from 'electron'`; after `const hub = …`: `const files = new FilesRelay(hub, dialog, getWindow)` (move `getWindow` above it if needed — it is a function declaration, so hoisting covers it); `registerIpc(hub, getWindow, isTrusted, files)`; in `createWindow`: `recoverRenderer(hub, w, d.reason, crashPolicy, () => files.reset())`.

`desktop/src/main/window.ts` `recoverRenderer`: add `onReset?: () => void` after `policy`; after the `term.closeAll` call:
```ts
  await hub.call('files.cancelAll', {}, 5000).catch((e) => console.error('sshgate: files.cancelAll after renderer crash failed:', (e as Error).message))
  onReset?.()
```
and extend the comment: "…close the terminals and cancel the file transfers the dead renderer started, and drop its file grants."

`desktop/src/preload/preload.ts` (sandboxed: it may only require `electron`, so the checks live here):
```ts
import { contextBridge, ipcRenderer, webUtils, type IpcRendererEvent } from 'electron'

contextBridge.exposeInMainWorld('sshmcp', {
  // …existing call/notify/getState/onEvent/onState unchanged…
  pickUpload: (server: string, folder: string, mode: string) => ipcRenderer.invoke('files:pickUpload', { server, folder, mode }),
  pickDownloadDir: (server: string) => ipcRenderer.invoke('files:pickDownloadDir', { server }),
  // Only real dropped files have a path; a File made by page script has none.
  grantDropped: (files: unknown, server: string) => {
    if (!Array.isArray(files) || files.length > 1000 || !files.every((f) => f instanceof File)) {
      return Promise.reject(new Error('invalid drop'))
    }
    const paths = files.map((f) => webUtils.getPathForFile(f)).filter((p) => p !== '')
    return ipcRenderer.invoke('files:grantDropped', { server, paths })
  },
})
```

`desktop/src/renderer/transport.ts`: extend `Bridge` with
```ts
  pickUpload(server: string, folder: string, mode: 'files' | 'folder' | 'both'): Promise<FileGrant[]>
  pickDownloadDir(server: string): Promise<FileGrant | null>
  grantDropped(files: File[], server: string): Promise<FileGrant[]>
```
and add to `hub` (importing the new types):
```ts
  filesList: (server: string, path: string, trustHostKey?: { fingerprint: string; keyType: string }) =>
    call<FilesListResult>('files.list', trustHostKey ? { server, path, trustHostKey } : { server, path }),
  filesMkdir: async (server: string, path: string) => { await call('files.mkdir', { server, path }) },
  filesRename: async (server: string, from: string, to: string) => { await call('files.rename', { server, from, to }) },
  // sources/dest carry grant tokens where they name local paths; main swaps them.
  filesPlan: async (id: string, server: string, op: FileOp, sources: string[], dest?: string) => {
    await call('files.plan', dest === undefined ? { id, server, op, sources } : { id, server, op, sources, dest })
  },
  filesRun: (id: string, conflict: 'skip' | 'overwrite' | 'ask') => call<{ cancelled?: boolean }>('files.run', { id, conflict }),
  filesCancel: (id: string) => bridge().notify('files.cancel', { id }),
  pickUpload: async (server: string, folder: string, mode: 'files' | 'folder' | 'both') => {
    try { return await bridge().pickUpload(server, folder, mode) } catch (e) { throw cleanError(e) }
  },
  pickDownloadDir: async (server: string) => {
    try { return await bridge().pickDownloadDir(server) } catch (e) { throw cleanError(e) }
  },
  grantDropped: async (files: File[], server: string) => {
    try { return await bridge().grantDropped(files, server) } catch (e) { throw cleanError(e) }
  },
```

- [ ] **Step 5: Run**

Run: `cd desktop && npm run typecheck && npm test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add desktop/src desktop/test
git commit -m "feat(desktop): file grants and the files relay in Electron main; native download conflicts"
```

---

### Task 10: Renderer pure helpers — `files.ts`

**Files:**
- Create: `desktop/src/renderer/files.ts`
- Modify: `desktop/src/renderer/hostForm.ts` (`closeWarning`)
- Test: `desktop/test/files.test.ts`, `desktop/test/hostForm.test.ts`

**Interfaces:**
- Produces (all pure): `newJobId()`, `type SortKey = 'name' | 'size' | 'mtime' | 'mode'`, `sortEntries(es, key, desc)`, `visibleEntries(es, showHidden)`, `formatSize(n)`, `formatMode(kind, mode)`, `formatTime(sec)`, `joinPath(dir, name)`, `parentPath(p): string | undefined`, `nameError(name): string | undefined`, `deleteNeedsTyping(hasFolder, selectedCount, planned)`, `DELETE_WORD`, `windowRange(scrollTop, viewport, rowHeight, count, overscan?)`, `nextSelection(names, order, clicked, mods, anchor?)`, `doneSummary(done?, error?)`, `lastFolder.get/set(store, server[, path])`, `hiddenPref.get/set(store[, on])`. `hostForm.ts`: `closeWarning(openTabs, transfers): string`.

- [ ] **Step 1: Write the failing test** — `desktop/test/files.test.ts`

```ts
import { describe, expect, it } from 'vitest'
import type { FileEntry } from '../src/shared/protocol'
import {
  deleteNeedsTyping, doneSummary, formatMode, formatSize, hiddenPref, joinPath, lastFolder, nameError, newJobId,
  nextSelection, parentPath, sortEntries, visibleEntries, windowRange,
} from '../src/renderer/files'
import { displayText } from '../src/shared/display'

const e = (name: string, kind: FileEntry['kind'] = 'file', size = 0, mtime = 0, mode = 0o644): FileEntry => ({ name, kind, size, mtime, mode })

describe('listing helpers', () => {
  it('sorts folders first, then by the key', () => {
    const es = [e('b', 'file', 5), e('a', 'file', 9), e('z', 'dir'), e('c', 'dir')]
    expect(sortEntries(es, 'name', false).map((x) => x.name)).toEqual(['c', 'z', 'a', 'b'])
    expect(sortEntries(es, 'size', true).map((x) => x.name)).toEqual(['c', 'z', 'a', 'b'])
    expect(sortEntries(es, 'name', true).map((x) => x.name)).toEqual(['z', 'c', 'b', 'a'])
  })
  it('hides dotfiles unless asked', () => {
    expect(visibleEntries([e('.env'), e('a')], false).map((x) => x.name)).toEqual(['a'])
    expect(visibleEntries([e('.env'), e('a')], true)).toHaveLength(2)
  })
  it('formats sizes and modes', () => {
    expect(formatSize(0)).toBe('0 B')
    expect(formatSize(1023)).toBe('1023 B')
    expect(formatSize(1536)).toBe('1.5 KB')
    expect(formatSize(5 * 1024 ** 3)).toBe('5.0 GB')
    expect(formatMode('dir', 0o755)).toBe('drwxr-xr-x')
    expect(formatMode('link', 0o777)).toBe('lrwxrwxrwx')
    expect(formatMode('file', 0o640)).toBe('-rw-r-----')
  })
  it('joins and walks up remote paths', () => {
    expect(joinPath('/', 'a')).toBe('/a')
    expect(joinPath('/home/u', 'a')).toBe('/home/u/a')
    expect(parentPath('/home/u')).toBe('/home')
    expect(parentPath('/home')).toBe('/')
    expect(parentPath('/')).toBeUndefined()
    expect(parentPath('')).toBeUndefined()
  })
  it('checks new names', () => {
    for (const bad of ['', '.', '..', 'a/b', 'a\u0000b']) expect(nameError(bad)).toBeTruthy()
    expect(nameError('ok name')).toBeUndefined()
  })
  it('windows the rows it renders', () => {
    expect(windowRange(0, 280, 28, 1000, 5)).toEqual([0, 15])
    expect(windowRange(28 * 500, 280, 28, 1000, 5)).toEqual([495, 515])
    expect(windowRange(28 * 999, 280, 28, 1000, 5)).toEqual([994, 1000])
  })
})

describe('selection', () => {
  const order = ['a', 'b', 'c', 'd']
  it('click selects one, toggle adds, shift selects a range from the anchor', () => {
    let s = nextSelection(new Set(), order, 'b', {})
    expect([...s.names]).toEqual(['b'])
    s = nextSelection(s.names, order, 'd', { toggle: true }, s.anchor)
    expect([...s.names].sort()).toEqual(['b', 'd'])
    s = nextSelection(s.names, order, 'c', { range: true }, 'a')
    expect([...s.names]).toEqual(['a', 'b', 'c'])
  })
})

describe('delete confirmation', () => {
  it('asks for the typed word only when a selected folder has contents', () => {
    expect(deleteNeedsTyping(false, 2, { files: 2, dirs: 0, links: 0 })).toBe(false)
    expect(deleteNeedsTyping(true, 1, { files: 0, dirs: 1, links: 0 })).toBe(false)
    expect(deleteNeedsTyping(true, 1, { files: 3, dirs: 1, links: 0 })).toBe(true)
    expect(deleteNeedsTyping(true, 2, { files: 1, dirs: 1, links: 1 })).toBe(true)
  })
})

describe('summaries and memory', () => {
  it('summarises a finished job', () => {
    expect(doneSummary({ id: 'j', op: 'upload', copied: 3, skipped: 1, deleted: 0, bytes: 10, errorCount: 2, errors: [], cancelled: false, reason: '' }))
      .toBe('Uploaded 3 · skipped 1 · 2 errors')
    expect(doneSummary({ id: 'j', op: 'delete', copied: 0, skipped: 0, deleted: 4, bytes: 0, errorCount: 0, errors: [], cancelled: false, reason: '' }))
      .toBe('Deleted 4')
    expect(doneSummary({ id: 'j', op: 'download', copied: 1, skipped: 0, deleted: 0, bytes: 1, errorCount: 0, errors: [], cancelled: true, reason: 'server changed' }))
      .toBe('Cancelled: server changed · downloaded 1')
    expect(doneSummary(undefined, 'hub restarted')).toBe('hub restarted')
  })
  it('remembers the last folder per host and the hidden-files toggle', () => {
    const m = new Map<string, string>()
    const store = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => { m.set(k, v) } } as unknown as Storage
    expect(lastFolder.get(store, 'box')).toBeUndefined()
    lastFolder.set(store, 'box', '/srv')
    expect(lastFolder.get(store, 'box')).toBe('/srv')
    expect(lastFolder.get(undefined, 'box')).toBeUndefined()
    expect(hiddenPref.get(store)).toBe(false)
    hiddenPref.set(store, true)
    expect(hiddenPref.get(store)).toBe(true)
  })
  it('ids match the hub rules', () => {
    expect(newJobId()).toMatch(/^[A-Za-z0-9_-]{1,64}$/)
  })
  it('makes hidden characters visible', () => {
    expect(displayText('invoice‮fdp.exe')).toBe('invoice\\u202efdp.exe')
    expect(displayText('a​b\nc')).toBe('a\\u200bb\\u000ac')
  })
})
```

In `desktop/test/hostForm.test.ts` add:
```ts
import { closeWarning } from '../src/renderer/hostForm'
describe('closeWarning', () => {
  it('counts tabs and transfers', () => {
    expect(closeWarning(2, 0)).toBe('Saving will close 2 open tabs.')
    expect(closeWarning(1, 1)).toBe('Saving will close 1 open tab and cancel 1 transfer.')
    expect(closeWarning(0, 3)).toBe('Saving will cancel 3 transfers.')
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd desktop && npm test`
Expected: FAIL (module not found).

- [ ] **Step 3: Implement** — `desktop/src/renderer/files.ts`

```ts
import type { FileEntry, FilesDone } from '../shared/protocol'

export function newJobId(): string {
  const b = new Uint8Array(8)
  crypto.getRandomValues(b)
  return 'j-' + Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

export type SortKey = 'name' | 'size' | 'mtime' | 'mode'

// Folders always first; then by key, descending if asked; ties by name, ascending.
export function sortEntries(es: FileEntry[], key: SortKey, desc: boolean): FileEntry[] {
  const byName = (a: FileEntry, b: FileEntry) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0)
  const cmp = (a: FileEntry, b: FileEntry) => {
    const d = key === 'name' ? byName(a, b) : (a[key] as number) - (b[key] as number)
    return (desc ? -d : d) || byName(a, b)
  }
  const dirs = es.filter((x) => x.kind === 'dir').sort(cmp)
  const rest = es.filter((x) => x.kind !== 'dir').sort(cmp)
  return [...dirs, ...rest]
}

export const visibleEntries = (es: FileEntry[], showHidden: boolean) => (showHidden ? es : es.filter((x) => !x.name.startsWith('.')))

export function formatSize(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(1)} ${units[i]}`
}

export function formatMode(kind: FileEntry['kind'], mode: number): string {
  const t = kind === 'dir' ? 'd' : kind === 'link' ? 'l' : '-'
  const bits = 'rwxrwxrwx'
  return t + Array.from(bits, (c, i) => (mode & (1 << (8 - i)) ? c : '-')).join('')
}

export function formatTime(sec: number): string {
  const d = new Date(sec * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export const joinPath = (dir: string, name: string) => (dir.endsWith('/') ? dir + name : `${dir}/${name}`)

export function parentPath(p: string): string | undefined {
  if (p === '' || p === '/') return undefined
  const i = p.lastIndexOf('/')
  return i <= 0 ? '/' : p.slice(0, i)
}

export function nameError(name: string): string | undefined {
  if (name === '' || name === '.' || name === '..') return 'Enter a name.'
  if (/[/\u0000]/.test(name)) return 'A name cannot contain / or NUL.'
  return undefined
}

export const DELETE_WORD = 'delete'

// A selected folder has contents when the plan found more than was selected.
export const deleteNeedsTyping = (hasFolder: boolean, selectedCount: number, p: { files: number; dirs: number; links: number }) =>
  hasFolder && p.files + p.dirs + p.links > selectedCount

export function windowRange(scrollTop: number, viewport: number, rowHeight: number, count: number, overscan = 10): [number, number] {
  const first = Math.floor(scrollTop / rowHeight)
  const start = Math.max(0, first - overscan)
  const end = Math.min(count, first + Math.ceil(viewport / rowHeight) + overscan)
  return [start, end]
}

export function nextSelection(names: Set<string>, order: string[], clicked: string,
  mods: { toggle?: boolean; range?: boolean }, anchor?: string): { names: Set<string>; anchor: string } {
  if (mods.range && anchor !== undefined && order.includes(anchor)) {
    const [a, b] = [order.indexOf(anchor), order.indexOf(clicked)].sort((x, y) => x - y)
    return { names: new Set(order.slice(a, b + 1)), anchor }
  }
  if (mods.toggle) {
    const n = new Set(names)
    if (n.has(clicked)) n.delete(clicked); else n.add(clicked)
    return { names: n, anchor: clicked }
  }
  return { names: new Set([clicked]), anchor: clicked }
}

const VERB = { upload: 'Uploaded', download: 'Downloaded', delete: 'Deleted' } as const

export function doneSummary(d?: FilesDone, error?: string): string {
  if (!d) return error ?? ''
  const n = d.op === 'delete' ? d.deleted : d.copied
  const parts = [`${VERB[d.op]} ${n}`]
  if (d.skipped) parts.push(`skipped ${d.skipped}`)
  if (d.errorCount) parts.push(`${d.errorCount} ${d.errorCount === 1 ? 'error' : 'errors'}`)
  if (!d.cancelled) return parts.join(' · ')
  parts[0] = parts[0].toLowerCase()
  return [`Cancelled${d.reason ? `: ${d.reason}` : ''}`, ...parts].join(' · ')
}

const get = (s: Storage | undefined, k: string) => { try { return s?.getItem(k) ?? undefined } catch { return undefined } }
const set = (s: Storage | undefined, k: string, v: string) => { try { s?.setItem(k, v) } catch { /* private mode: not remembered */ } }

export const lastFolder = {
  get: (s: Storage | undefined, server: string) => get(s, `sshgate.files.last.${server}`),
  set: (s: Storage | undefined, server: string, p: string) => set(s, `sshgate.files.last.${server}`, p),
}

export const hiddenPref = {
  get: (s: Storage | undefined) => get(s, 'sshgate.files.hidden') === '1',
  set: (s: Storage | undefined, on: boolean) => set(s, 'sshgate.files.hidden', on ? '1' : '0'),
}

export function localStore(): Storage | undefined {
  try { return window.localStorage } catch { return undefined }
}
```

Note: `doneSummary` for `Cancelled: server changed · downloaded 1` lower-cases the verb; the test pins that text.

In `hostForm.ts`:
```ts
export function closeWarning(openTabs: number, transfers: number): string {
  const tabs = `close ${openTabs} open ${openTabs === 1 ? 'tab' : 'tabs'}`
  const jobs = `cancel ${transfers} ${transfers === 1 ? 'transfer' : 'transfers'}`
  if (openTabs && transfers) return `Saving will ${tabs} and ${jobs}.`
  return `Saving will ${openTabs ? tabs : jobs}.`
}
```

- [ ] **Step 4: Run**

Run: `cd desktop && npm run typecheck && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add desktop/src/renderer/files.ts desktop/src/renderer/hostForm.ts desktop/test
git commit -m "feat(desktop): pure helpers for the Files tab"
```

---

### Task 11: Renderer — Files tab, dialogs, transfer strip

**Files:**
- Create: `desktop/src/renderer/FilesView.tsx`, `desktop/src/renderer/FileDialogs.tsx`
- Modify: `desktop/src/renderer/terminals.ts` (`Tab.kind`), `TerminalTabs.tsx`, `HostList.tsx`, `HostEditor.tsx`, `App.tsx`, `icons.tsx`, `styles.css`
- Test: `desktop/test/terminals.test.ts`

**Interfaces:**
- Consumes: Task 9 (`hub.files*`, `hub.pick*`, `hub.grantDropped`, protocol types), Task 10 helpers.
- Produces: `TabSet.open(server, kind = 'term')`, `Tab.kind: 'term' | 'files'`; `TerminalsHandle.openFiles(server)`, `TerminalsHandle.transferCount(server)`; `HostList` prop `onFiles(name)`; `EditorWarnings`/`HostEditor` prop `transfers`. Accessible names used by e2e: host card button `Files <name>`; toolbar buttons `Up`, `Refresh`, `New folder`, `Upload` (macOS) or `Upload files`/`Upload folder`, `Download`, `Rename`, `Delete`; checkbox `Show hidden files`; path field `Path`; list `role="grid"` named `Files on <server>` with rows `role="row"` and `data-name`; dialogs named `Files already exist`, `Delete files`, `New folder`, `Rename`; transfer strip `role="list"` named `Transfers`, each item `role="listitem"`.

- [ ] **Step 1: Write the failing test** — add to `desktop/test/terminals.test.ts`:

```ts
describe('TabSet files tabs', () => {
  it('opens files tabs that never count as terminals', () => {
    const t = new TabSet()
    const f = t.open('box', 'files')
    expect(f.kind).toBe('files')
    expect(f.state).toBe('open')
    expect(t.openCount('box')).toBe(0)
    expect(t.mostRecentFor('box')).toBeUndefined()
    const term = t.open('box')
    expect(term.kind).toBe('term')
    expect(t.mostRecentFor('box')?.id).toBe(term.id)
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd desktop && npm test -- terminals`
Expected: FAIL (`kind` undefined).

- [ ] **Step 3: Implement**

`terminals.ts`: `Tab` gains `kind: 'term' | 'files'`. `open(server: string, kind: Tab['kind'] = 'term')` creates `{ id: newTermId(), server, kind, state: kind === 'files' ? 'open' : 'opening', sawOutput: false }`. `mostRecentFor` and `openCount` consider only `t.kind === 'term'`.

`icons.tsx` add:
```tsx
export const FolderIcon = icon(<path d="M3 6h6l2 2h10v11H3z" />)
export const FileIcon = icon(<><path d="M6 3h8l4 4v14H6z" /><path d="M14 3v4h4" /></>)
export const LinkIcon = icon(<><path d="M10 14a4 4 0 0 0 6 0l3-3a4 4 0 0 0-6-6l-1 1" /><path d="M14 10a4 4 0 0 0-6 0l-3 3a4 4 0 0 0 6 6l1-1" /></>)
export const UpIcon = icon(<path d="M12 19V5M6 11l6-6 6 6" />)
export const RefreshIcon = icon(<><path d="M20 11a8 8 0 1 0-2.3 5.7" /><path d="M20 4v7h-7" /></>)
export const UploadIcon = icon(<><path d="M12 16V4M7 9l5-5 5 5" /><path d="M4 20h16" /></>)
export const DownloadIcon = icon(<><path d="M12 4v12M7 11l5 5 5-5" /><path d="M4 20h16" /></>)
```

`FileDialogs.tsx`:
```tsx
import { useState, type FormEvent } from 'react'
import type { FilesPlanned } from '../shared/protocol'
import { displayText } from '../shared/display'
import { DELETE_WORD, formatSize, nameError } from './files'
import { TrashIcon, WarningIcon } from './icons'

// Cancel is the default (Enter) in both risky dialogs.
export function ConflictDialog({ planned, onChoice }: { planned: FilesPlanned; onChoice: (c: 'cancel' | 'skip' | 'overwrite') => void }) {
  const n = planned.conflicts.count
  return (
    <div className="modal" role="dialog" aria-label="Files already exist">
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onChoice('cancel') }}>
        <div className="dialog-title"><span className="dialog-icon"><WarningIcon /></span>
          <h3>{`${n} ${n === 1 ? 'file already exists' : 'files already exist'}`}</h3></div>
        <ul className="files-sample mono">{planned.conflicts.sample.map((s) => <li key={s}>{displayText(s)}</li>)}</ul>
        {n > planned.conflicts.sample.length && <p className="muted">{`and ${n - planned.conflicts.sample.length} more`}</p>}
        <div className="dialog-actions">
          <button type="button" className="btn danger-outline" onClick={() => onChoice('overwrite')}>Overwrite all</button>
          <button type="button" className="btn" onClick={() => onChoice('skip')}>Skip existing</button>
          <button type="submit" className="btn primary" autoFocus>Cancel<kbd aria-hidden="true">↵</kbd></button>
        </div>
      </form>
    </div>
  )
}

export function DeleteDialog({ names, planned, needsTyping, onDelete, onCancel }: {
  names: string[]; planned: FilesPlanned; needsTyping: boolean; onDelete: () => void; onCancel: () => void
}) {
  const [typed, setTyped] = useState('')
  const ok = !needsTyping || typed === DELETE_WORD
  return (
    <div className="modal" role="dialog" aria-label="Delete files">
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onCancel() }}>
        <div className="dialog-title"><span className="dialog-icon danger"><TrashIcon /></span><h3>Delete for good?</h3></div>
        <ul className="files-sample mono">{names.slice(0, 10).map((n) => <li key={n}>{displayText(n)}</li>)}</ul>
        {names.length > 10 && <p className="muted">{`and ${names.length - 10} more`}</p>}
        <p>{`${planned.files} files, ${planned.dirs} folders, ${planned.links} links · ${formatSize(planned.bytes)}. There is no undo.`}</p>
        {needsTyping && (
          <label className="field">{`Type ${DELETE_WORD} to confirm`}
            <input value={typed} onChange={(e) => setTyped(e.target.value)} aria-label={`Type ${DELETE_WORD} to confirm`} />
          </label>
        )}
        <div className="dialog-actions">
          <button type="button" className="btn danger-outline" disabled={!ok} onClick={onDelete}>Delete</button>
          <button type="submit" className="btn primary" autoFocus={!needsTyping}>Cancel<kbd aria-hidden="true">↵</kbd></button>
        </div>
      </form>
    </div>
  )
}

export function NameDialog({ title, initial, onSubmit, onCancel }: {
  title: 'New folder' | 'Rename'; initial: string; onSubmit: (name: string) => Promise<string | undefined>; onCancel: () => void
}) {
  const [name, setName] = useState(initial)
  const [err, setErr] = useState<string>()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const bad = nameError(name)
    if (bad) return setErr(bad)
    setErr(await onSubmit(name))
  }
  return (
    <div className="modal" role="dialog" aria-label={title}>
      <form className="dialog" onSubmit={submit} onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }}>
        <div className="dialog-title"><h3>{title}</h3></div>
        <label className="field">Name<input value={name} onChange={(e) => setName(e.target.value)} autoFocus aria-label="Name" /></label>
        {err && <p className="error">{displayText(err)}</p>}
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={onCancel}>Cancel</button>
          <button type="submit" className="btn primary">{title === 'Rename' ? 'Rename' : 'Create'}</button>
        </div>
      </form>
    </div>
  )
}
```

`FilesView.tsx`:
```tsx
import { useCallback, useEffect, useMemo, useReducer, useRef, useState, type DragEvent, type KeyboardEvent, type MouseEvent } from 'react'
import type { FileEntry, FileListing, FileOp, FilesDone, FilesPlanned, FilesProgress, HostKeyMismatch } from '../shared/protocol'
import { displayText } from '../shared/display'
import { hub } from './transport'
import type { HostKeyPrompts } from './hostkeys'
import type { Dispatcher, Tab } from './terminals'
import type { TermEvent } from './TermView'
import { ConflictDialog, DeleteDialog, NameDialog } from './FileDialogs'
import {
  deleteNeedsTyping, doneSummary, formatMode, formatSize, formatTime, hiddenPref, joinPath, lastFolder, localStore,
  newJobId, nextSelection, parentPath, sortEntries, visibleEntries, windowRange, type SortKey,
} from './files'
import { CloseIcon, DownloadIcon, FileIcon, FolderIcon, LinkIcon, PlusIcon, RefreshIcon, TrashIcon, UpIcon, UploadIcon, EditIcon } from './icons'

const ROW = 28

interface Job {
  id: string; op: FileOp; label: string; folder: string
  state: 'planning' | 'confirm' | 'running' | 'done'
  names?: string[]; hasFolder?: boolean
  planned?: FilesPlanned; progress?: FilesProgress; done?: FilesDone; error?: string
}

type Dialog =
  | { kind: 'conflict'; id: string; planned: FilesPlanned }
  | { kind: 'delete'; id: string; planned: FilesPlanned; names: string[]; hasFolder: boolean }
  | { kind: 'mkdir' }
  | { kind: 'rename'; from: string }

export function FilesView({ tab, visible, events, hostKeys, onMismatch, onTrusted, onJobs }: {
  tab: Tab; visible: boolean; events: Dispatcher<TermEvent>; hostKeys: HostKeyPrompts
  onMismatch: (m: HostKeyMismatch) => void; onTrusted: () => void; onJobs: (running: number) => void
}) {
  const server = tab.server
  const [listing, setListing] = useState<FileListing>()
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(false)
  const [pathInput, setPathInput] = useState('')
  const [showHidden, setShowHidden] = useState(() => hiddenPref.get(localStore()))
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({ key: 'name', desc: false })
  const [sel, setSel] = useState<{ names: Set<string>; anchor?: string }>({ names: new Set() })
  const [dialog, setDialog] = useState<Dialog>()
  const [scrollTop, setScrollTop] = useState(0)
  const [viewport, setViewport] = useState(400)
  const [dropping, setDropping] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)
  const shown = useRef('') // the folder listed now
  const jobs = useRef(new Map<string, Job>()).current
  const offs = useRef(new Map<string, () => void>()).current
  const [, bump] = useReducer((n: number) => n + 1, 0)

  const rows = useMemo(() => (listing ? sortEntries(visibleEntries(listing.entries, showHidden), sort.key, sort.desc) : []), [listing, showHidden, sort])
  const selected = rows.filter((r) => sel.names.has(r.name))
  const running = [...jobs.values()].filter((j) => j.state !== 'done').length
  useEffect(() => { onJobs(running) }, [running, onJobs])

  useEffect(() => {
    const el = listRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setViewport(el.clientHeight))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const load = useCallback(async (p: string): Promise<boolean> => {
    setLoading(true); setError(undefined); setPathInput(p)
    try {
      let r = await hub.filesList(server, p)
      while (r.status === 'hostKeyUnknown') {
        if (!(await hostKeys.ask(tab.id, r))) throw new Error('host key not trusted')
        r = await hub.filesList(server, p, { fingerprint: r.fingerprint, keyType: r.keyType })
        onTrusted()
      }
      if (r.status === 'hostKeyMismatch') { onMismatch(r); throw new Error('host key mismatch') }
      setListing(r); setPathInput(r.path); shown.current = r.path
      setSel({ names: new Set() }); setScrollTop(0); listRef.current?.scrollTo(0, 0)
      lastFolder.set(localStore(), server, r.path)
      return true
    } catch (e) {
      setListing(undefined); setError((e as Error).message)
      return false
    } finally { setLoading(false) }
  }, [server, tab.id, hostKeys, onMismatch, onTrusted])

  // Lists once when the tab opens (the last folder, else home), then only on
  // the author's own actions: never on a timer (see CLAUDE.md, idle lock).
  useEffect(() => {
    const last = lastFolder.get(localStore(), server)
    void load(last ?? '').then((ok) => { if (!ok && last) void load('') })
    return () => {
      hostKeys.drop(tab.id)
      for (const [id, j] of jobs) if (j.state !== 'done') hub.filesCancel(id) // closing the tab cancels its jobs
      for (const off of offs.values()) off()
    }
  }, []) // once per tab

  const patch = (id: string, p: Partial<Job>) => { const j = jobs.get(id); if (j) { jobs.set(id, { ...j, ...p }); bump() } }

  const finishJob = (id: string, done?: FilesDone, error?: string) => {
    const j = jobs.get(id)
    if (!j || j.state === 'done') return
    offs.get(id)?.(); offs.delete(id)
    patch(id, { state: 'done', done, error })
    setDialog((d) => (d && 'id' in d && d.id === id ? undefined : d))
    if (done && j.op !== 'download' && j.folder === shown.current) void load(shown.current)
  }

  const runJob = async (id: string, conflict: 'skip' | 'overwrite' | 'ask') => {
    patch(id, { state: 'running' })
    setDialog(undefined)
    try { await hub.filesRun(id, conflict) } catch (e) { hub.filesCancel(id); finishJob(id, undefined, (e as Error).message) }
  }

  const onJobEvent = (id: string, e: TermEvent) => {
    const j = jobs.get(id)
    if (!j) return
    if (e.method === 'files.planned') {
      const planned = e.params
      patch(id, { planned })
      if (j.op === 'delete') {
        patch(id, { state: 'confirm' })
        setDialog({ kind: 'delete', id, planned, names: j.names ?? [], hasFolder: !!j.hasFolder })
      } else if (j.op === 'upload' && planned.conflicts.count > 0) {
        patch(id, { state: 'confirm' })
        setDialog({ kind: 'conflict', id, planned })
      } else void runJob(id, j.op === 'download' ? 'ask' : 'skip') // main asks about download conflicts
    } else if (e.method === 'files.progress') patch(id, { progress: e.params })
    else if (e.method === 'files.done') finishJob(id, e.params)
    else if (e.method === 'hub.stopped') finishJob(id, undefined, 'hub restarted')
  }

  const startJob = async (op: FileOp, sources: string[], dest: string | undefined, label: string, extra: Partial<Job> = {}) => {
    const id = newJobId()
    jobs.set(id, { id, op, label, folder: shown.current, state: 'planning', ...extra }); bump()
    offs.set(id, events.on(id, (e) => onJobEvent(id, e)))
    try { await hub.filesPlan(id, server, op, sources, dest) } catch (e) { finishJob(id, undefined, (e as Error).message) }
  }

  const label = (n: string[]) => (n.length === 1 ? n[0] : `${n.length} items`)
  const upload = async (mode: 'files' | 'folder' | 'both') => {
    try {
      const picks = await hub.pickUpload(server, shown.current, mode)
      if (picks.length) await startJob('upload', picks.map((p) => p.token), shown.current, label(picks.map((p) => p.name)))
    } catch (e) { setError((e as Error).message) }
  }
  const download = async () => {
    if (!selected.length) return
    try {
      const d = await hub.pickDownloadDir(server)
      if (d) await startJob('download', selected.map((x) => joinPath(shown.current, x.name)), d.token, label(selected.map((x) => x.name)))
    } catch (e) { setError((e as Error).message) }
  }
  const remove = () => {
    if (!selected.length) return
    const names = selected.map((x) => x.name)
    void startJob('delete', names.map((n) => joinPath(shown.current, n)), undefined, label(names),
      { names, hasFolder: selected.some((x) => x.kind === 'dir') })
  }
  const drop = async (e: DragEvent) => {
    e.preventDefault(); setDropping(false)
    const fl = Array.from(e.dataTransfer.files)
    if (!fl.length || !listing) return
    try {
      const picks = await hub.grantDropped(fl, server)
      if (picks.length) await startJob('upload', picks.map((p) => p.token), shown.current, label(picks.map((p) => p.name)))
    } catch (err) { setError((err as Error).message) }
  }
  const open = (x: FileEntry) => { if (x.kind === 'dir' || x.kind === 'link') void load(joinPath(shown.current, x.name)) }
  const up = () => { const p = parentPath(listing ? shown.current : pathInput); if (p !== undefined) void load(p) }
  const click = (x: FileEntry, e: MouseEvent) => {
    setSel(nextSelection(sel.names, rows.map((r) => r.name), x.name, { toggle: e.metaKey || e.ctrlKey, range: e.shiftKey }, sel.anchor))
  }
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && selected.length === 1) { e.preventDefault(); open(selected[0]) }
    else if (e.key === 'Backspace') { e.preventDefault(); up() }
    else if (e.key === 'F2' && selected.length === 1) { e.preventDefault(); setDialog({ kind: 'rename', from: selected[0].name }) }
    else if (e.key === 'Delete') { e.preventDefault(); remove() }
  }
  const submitName = async (name: string): Promise<string | undefined> => {
    try {
      if (dialog?.kind === 'mkdir') await hub.filesMkdir(server, joinPath(shown.current, name))
      if (dialog?.kind === 'rename') await hub.filesRename(server, joinPath(shown.current, dialog.from), joinPath(shown.current, name))
      setDialog(undefined)
      await load(shown.current)
      return undefined
    } catch (e) { return (e as Error).message }
  }
  const sortBy = (key: SortKey) => setSort((s) => ({ key, desc: s.key === key ? !s.desc : false }))
  const mac = navigator.platform.startsWith('Mac')
  const [start, end] = windowRange(scrollTop, viewport, ROW, rows.length)
  const jobList = [...jobs.values()]

  return (
    <div className="filesview" style={{ display: visible ? 'flex' : 'none' }}>
      <div className="files-toolbar">
        <button type="button" className="icon" aria-label="Up" title="Up" onClick={up}><UpIcon /></button>
        <form className="files-path" onSubmit={(e) => { e.preventDefault(); void load(pathInput.trim()) }}>
          <input className="mono" aria-label="Path" value={pathInput} onChange={(e) => setPathInput(e.target.value)} spellCheck={false} />
        </form>
        <button type="button" className="icon" aria-label="Refresh" title="Refresh" onClick={() => void load(shown.current)}><RefreshIcon /></button>
        <label className="files-hidden"><input type="checkbox" checked={showHidden}
          onChange={(e) => { setShowHidden(e.target.checked); hiddenPref.set(localStore(), e.target.checked) }} />Show hidden files</label>
        <span className="spacer" />
        <button type="button" className="btn" disabled={!listing} onClick={() => setDialog({ kind: 'mkdir' })}><PlusIcon />New folder</button>
        {mac ? (
          <button type="button" className="btn" disabled={!listing} onClick={() => void upload('both')}><UploadIcon />Upload</button>
        ) : (<>
          <button type="button" className="btn" disabled={!listing} onClick={() => void upload('files')}><UploadIcon />Upload files</button>
          <button type="button" className="btn" disabled={!listing} onClick={() => void upload('folder')}><UploadIcon />Upload folder</button>
        </>)}
        <button type="button" className="btn" disabled={!selected.length} onClick={() => void download()}><DownloadIcon />Download</button>
        <button type="button" className="btn" disabled={selected.length !== 1} onClick={() => setDialog({ kind: 'rename', from: selected[0].name })}><EditIcon />Rename</button>
        <button type="button" className="btn danger-outline" disabled={!selected.length} onClick={remove}><TrashIcon />Delete</button>
      </div>
      <div className={'files-body' + (dropping ? ' dropping' : '')}
        onDragOver={(e) => { e.preventDefault(); setDropping(true) }} onDragLeave={() => setDropping(false)} onDrop={(e) => void drop(e)}>
        <div className="files-head" role="presentation">
          {(['name', 'size', 'mtime', 'mode'] as SortKey[]).map((k) => (
            <button key={k} type="button" className={'files-col ' + k} onClick={() => sortBy(k)}>
              {{ name: 'Name', size: 'Size', mtime: 'Modified', mode: 'Permissions' }[k]}{sort.key === k ? (sort.desc ? ' ↓' : ' ↑') : ''}
            </button>
          ))}
        </div>
        {error ? <p className="files-note error">{displayText(error)}</p> : null}
        {listing?.truncated && <p className="files-note muted">Showing the first 10,000 entries.</p>}
        {listing && listing.bad > 0 && <p className="files-note muted">{`${listing.bad} ${listing.bad === 1 ? 'name is' : 'names are'} not shown: not safe to display or copy.`}</p>}
        <div className="fileslist" ref={listRef} role="grid" aria-label={`Files on ${server}`} aria-busy={loading} tabIndex={0}
          onKeyDown={onKey} onScroll={(e) => setScrollTop(e.currentTarget.scrollTop)}>
          <div style={{ height: start * ROW }} />
          {rows.slice(start, end).map((x) => (
            <div key={x.name} role="row" data-name={x.name} aria-selected={sel.names.has(x.name)}
              className={'file-row' + (sel.names.has(x.name) ? ' selected' : '')}
              onClick={(e) => click(x, e)} onDoubleClick={() => open(x)}>
              <span className="files-col name">
                {x.kind === 'dir' ? <FolderIcon /> : x.kind === 'link' ? <LinkIcon /> : <FileIcon />}
                <span className="mono">{displayText(x.name)}</span>
                {x.target !== undefined && <span className="muted mono">{` → ${displayText(x.target)}`}</span>}
              </span>
              <span className="files-col size">{x.kind === 'dir' ? '—' : formatSize(x.size)}</span>
              <span className="files-col mtime">{formatTime(x.mtime)}</span>
              <span className="files-col mode mono">{formatMode(x.kind, x.mode)}</span>
            </div>
          ))}
          <div style={{ height: (rows.length - end) * ROW }} />
          {listing && rows.length === 0 && <p className="files-note muted">Empty folder.</p>}
        </div>
      </div>
      {jobList.length > 0 && (
        <ul className="transfers" aria-label="Transfers">
          {jobList.map((j) => (
            <li key={j.id} className="transfer">
              <span className="transfer-label">{`${{ upload: 'Upload', download: 'Download', delete: 'Delete' }[j.op]} ${displayText(j.label)}`}</span>
              {j.state === 'planning' && <span className="muted">Checking…</span>}
              {j.state === 'confirm' && <span className="muted">Waiting for you</span>}
              {j.state === 'running' && (
                <>
                  <progress max={j.progress?.total || j.planned?.bytes || 1} value={j.progress?.done ?? 0} />
                  <span className="muted mono">{j.progress ? `${formatSize(j.progress.done)} of ${formatSize(j.progress.total)} · ${displayText(j.progress.file)}` : ''}</span>
                </>
              )}
              {j.state === 'done' && <span>{displayText(doneSummary(j.done, j.error))}</span>}
              {j.state !== 'done'
                ? <button type="button" className="btn" onClick={() => hub.filesCancel(j.id)}>Cancel</button>
                : <button type="button" className="icon" aria-label="Dismiss" onClick={() => { jobs.delete(j.id); bump() }}><CloseIcon /></button>}
              {j.done && j.done.errors.length > 0 && (
                <ul className="transfer-errors mono">
                  {j.done.errors.map((m, i) => <li key={i}>{displayText(m)}</li>)}
                  {j.done.errorCount > j.done.errors.length && <li className="muted">{`and ${j.done.errorCount - j.done.errors.length} more`}</li>}
                </ul>
              )}
            </li>
          ))}
        </ul>
      )}
      {dialog?.kind === 'conflict' && (
        <ConflictDialog planned={dialog.planned} onChoice={(c) => {
          if (c === 'cancel') { hub.filesCancel(dialog.id); setDialog(undefined) } else void runJob(dialog.id, c)
        }} />
      )}
      {dialog?.kind === 'delete' && (
        <DeleteDialog names={dialog.names} planned={dialog.planned}
          needsTyping={deleteNeedsTyping(dialog.hasFolder, dialog.names.length, dialog.planned)}
          onDelete={() => void runJob(dialog.id, 'skip')} onCancel={() => { hub.filesCancel(dialog.id); setDialog(undefined) }} />
      )}
      {(dialog?.kind === 'mkdir' || dialog?.kind === 'rename') && (
        <NameDialog title={dialog.kind === 'mkdir' ? 'New folder' : 'Rename'} initial={dialog.kind === 'rename' ? dialog.from : ''}
          onSubmit={submitName} onCancel={() => setDialog(undefined)} />
      )}
    </div>
  )
}
```

The transfer list is a `ul` (implicit `list` role) labelled `Transfers`; its `li`s are `listitem`s.

`TerminalTabs.tsx`:
- `TerminalsHandle` gains `openFiles(server: string): void` and `transferCount(server: string): number`.
- `const transfers = useRef(new Map<string, number>()).current` (tab id → running jobs).
- The event routing becomes `if ((e.method.startsWith('term.') || e.method.startsWith('files.')) && typeof id === 'string') events.emit(id, e)`.
- `openFiles(server) { tabs.open(server, 'files'); changed() }`; `transferCount: (server) => tabs.tabs.filter((t) => t.server === server && t.kind === 'files').reduce((n, t) => n + (transfers.get(t.id) ?? 0), 0)`.
- Tab label: `{t.kind === 'files' ? <FolderIcon /> : <span className="dot" aria-hidden="true" />}{t.server}`, and `data-kind={t.kind}` on the tab `div`. Reconnect only when `t.kind === 'term' && t.state === 'exited'`. Closing a tab also does `transfers.delete(t.id)`.
- In `.termarea`, render `t.kind === 'files' ? <FilesView key={t.id} tab={t} visible={t.id === tabs.active} events={events} hostKeys={hostKeys} onMismatch={onMismatch} onTrusted={onTrusted} onJobs={(n) => transfers.set(t.id, n)} /> : <TermView … />`.

`HostList.tsx`: prop `onFiles: (name: string) => void`; in `.hostcard-actions`, before Edit:
```tsx
<button type="button" className="icon" aria-label={`Files ${s.name}`} title="Files" onClick={() => onFiles(s.name)}><FolderIcon /></button>
```

`HostEditor.tsx`: `EditorWarnings` and `HostEditor` take `transfers: number`; `const closes = (openTabs > 0 || transfers > 0) && closesTabs(server, draft)`; the message is `closeWarning(openTabs, transfers)` (from `hostForm.ts`).

`App.tsx`: `HostList … onFiles={(name) => terms.current?.openFiles(name)}`; `HostEditor … transfers={editing.name ? terms.current?.transferCount(editing.name) ?? 0 : 0}`.

`styles.css` (theme tokens only, no raw colours):
```css
.filesview { position: absolute; inset: 0; flex-direction: column; background: var(--surface); }
.files-toolbar { display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-bottom: 1px solid var(--divider); flex-wrap: wrap; }
.files-toolbar .spacer { flex: 1; }
.files-path { flex: 1 1 240px; min-width: 160px; }
.files-path input { width: 100%; }
.files-hidden { display: flex; align-items: center; gap: 6px; color: var(--label); }
.files-body { position: relative; flex: 1; min-height: 0; display: flex; flex-direction: column; }
.files-body.dropping { outline: 2px dashed var(--accent); outline-offset: -4px; background: var(--accent-tint); }
.files-head, .file-row { display: grid; grid-template-columns: minmax(200px, 1fr) 90px 150px 110px; align-items: center; padding: 0 12px; }
.files-head { border-bottom: 1px solid var(--divider); }
.files-head .files-col { background: none; border: 0; text-align: left; color: var(--label); padding: 6px 0; cursor: pointer; }
.fileslist { flex: 1; min-height: 0; overflow: auto; outline: none; }
.file-row { height: 28px; cursor: default; user-select: none; }
.file-row:hover { background: var(--raised); }
.file-row.selected { background: var(--accent-tint); }
.files-col.name { display: flex; align-items: center; gap: 6px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.files-col.size, .files-col.mtime, .files-col.mode { color: var(--muted); }
.files-note { padding: 8px 12px; }
.files-sample { max-height: 160px; overflow: auto; }
.transfers { list-style: none; margin: 0; padding: 8px 12px; border-top: 1px solid var(--divider); display: flex; flex-direction: column; gap: 6px; max-height: 30%; overflow: auto; }
.transfer { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.transfer progress { flex: 0 0 160px; accent-color: var(--accent); }
.transfer-errors { flex-basis: 100%; margin: 0; padding-left: 16px; color: var(--danger); }
```
`.termarea` is `position: relative` (styles.css), so `.filesview` fills it like `.term` does.

- [ ] **Step 4: Run**

Run: `cd desktop && npm run typecheck && npm test && npm run build`
Expected: PASS. Then run the app against sshtestd by hand to eyeball the tab (optional here; Task 12's e2e drives it):
```bash
go build -o sshgate ./cmd/sshgate && go run ./internal/sshx/sshtest/sshtestd -write-store=/tmp/v.json -password=pw -sftp-root=/tmp/sftproot
# in another terminal: SSHGATE_STORE=/tmp/v.json npm start (from desktop/), unlock with pw, open Files on box
```

- [ ] **Step 5: Commit**

```bash
git add desktop/src desktop/test
git commit -m "feat(desktop): Files tab with virtualised listing, dialogs, drop upload, and transfer strip"
```

---

### Task 12: End-to-end test, docs, ROADMAP

**Files:**
- Modify: `desktop/e2e/launch.ts` (option `sftp`), create `desktop/e2e/files.spec.ts`
- Modify: `CLAUDE.md`, `README.md`, `PRODUCT.md`, `docs/superpowers/ROADMAP.md`, `docs/superpowers/specs/2026-09-26-slice3a-sftp-design.md` (status)
- Create: `docs/superpowers/checklists/slice3a-manual.md`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: `launch.ts`** — add `sftp?: boolean` to `opts`; when set, push `-sftp-root=${path.join(tmp, 'sftp')}` to sshtestd's args; add `sftp: string` to `Launched` (the SFTP root on disk: `path.join(tmp, 'sftp')`; the server's home is `<sftp>/home`). Update the doc comment.

- [ ] **Step 2: Write the e2e test** — `desktop/e2e/files.spec.ts`

```ts
import { test, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launch, unlock, type Launched } from './launch'

// Spec 3a §Testing: list, mkdir, upload a folder, conflict (skip), download it back
// with the native conflict dialog, rename, recursive delete with the typed word.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')

let l: Launched
test.beforeAll(async () => { l = await launch({}, { sftp: true }) })
test.afterAll(async () => { await l?.close() })

const stubOpen = (paths: string[]) => l.app.evaluate(({ dialog }, p) => {
  dialog.showOpenDialog = (async () => ({ canceled: false, filePaths: p })) as typeof dialog.showOpenDialog
}, paths)
const stubMessage = (response: number) => l.app.evaluate(({ dialog }, r) => {
  dialog.showMessageBox = (async () => ({ response: r, checkboxChecked: false })) as typeof dialog.showMessageBox
}, response)

test('browse, upload, download, rename, and delete over SFTP', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await win.locator('.tabbar .hometab').click()
  await win.getByRole('button', { name: 'Files box' }).click()
  const grid = win.getByRole('grid', { name: 'Files on box' })
  await expect(win.getByLabel('Path')).toHaveValue('/home')

  // New folder.
  await win.getByRole('button', { name: 'New folder' }).click()
  await win.getByRole('dialog', { name: 'New folder' }).getByLabel('Name').fill('made')
  await win.getByRole('dialog', { name: 'New folder' }).getByRole('button', { name: 'Create' }).click()
  await expect(grid.locator('[data-name="made"]')).toBeVisible()

  // Upload a folder.
  const local = path.join(l.tmp, 'proj')
  fs.mkdirSync(path.join(local, 'sub'), { recursive: true })
  fs.writeFileSync(path.join(local, 'a.txt'), 'alpha')
  fs.writeFileSync(path.join(local, 'sub', 'b.txt'), 'beta')
  await stubOpen([local])
  const uploadName = process.platform === 'darwin' ? 'Upload' : 'Upload folder'
  await win.getByRole('button', { name: uploadName, exact: true }).click()
  const transfers = win.getByRole('list', { name: 'Transfers' })
  await expect(transfers).toContainText('Uploaded 2')
  await expect(grid.locator('[data-name="proj"]')).toBeVisible()
  expect(fs.readFileSync(path.join(l.sftp, 'home', 'proj', 'sub', 'b.txt'), 'utf8')).toBe('beta')

  // Again: the conflict dialog; Skip existing.
  await stubOpen([local])
  await win.getByRole('button', { name: uploadName, exact: true }).click()
  const conflict = win.getByRole('dialog', { name: 'Files already exist' })
  await expect(conflict).toContainText('2 files already exist')
  await conflict.getByRole('button', { name: 'Skip existing' }).click()
  await expect(transfers).toContainText('skipped 2')

  // Download it back, twice: the second time main's native dialog answers Skip existing.
  const dl = path.join(l.tmp, 'dl')
  fs.mkdirSync(dl)
  await grid.locator('[data-name="proj"]').click()
  await stubOpen([dl])
  await win.getByRole('button', { name: 'Download' }).click()
  await expect(transfers).toContainText('Downloaded 2')
  expect(fs.readFileSync(path.join(dl, 'proj', 'a.txt'), 'utf8')).toBe('alpha')
  await stubMessage(1)
  await stubOpen([dl])
  await win.getByRole('button', { name: 'Download' }).click()
  await expect(transfers).toContainText('Downloaded 0 · skipped 2')

  // Rename.
  await grid.locator('[data-name="proj"]').click()
  await win.getByRole('button', { name: 'Rename' }).click()
  const rename = win.getByRole('dialog', { name: 'Rename' })
  await rename.getByLabel('Name').fill('proj2')
  await rename.getByRole('button', { name: 'Rename' }).click()
  await expect(grid.locator('[data-name="proj2"]')).toBeVisible()

  // Recursive delete needs the typed word.
  await grid.locator('[data-name="proj2"]').click()
  await win.getByRole('button', { name: 'Delete', exact: true }).click()
  const del = win.getByRole('dialog', { name: 'Delete files' })
  await expect(del.getByRole('button', { name: 'Delete' })).toBeDisabled()
  await del.getByLabel('Type delete to confirm').fill('delete')
  await del.getByRole('button', { name: 'Delete' }).click()
  await expect(grid.locator('[data-name="proj2"]')).toHaveCount(0)
  expect(fs.existsSync(path.join(l.sftp, 'home', 'proj2'))).toBe(false)
})
```

- [ ] **Step 3: Run it**

Run: `cd desktop && npm run e2e -- files`
Expected: PASS. Then the whole suite: `npm run e2e`.

- [ ] **Step 4: Docs**

- `CLAUDE.md`:
  - Commands: the `sshtestd` line gains `[-sftp-root=<dir>]` ("serves SFTP rooted in `<dir>`, home `<dir>/home`"); the e2e list gains `files`.
  - Architecture, the UI-door method list: add `files.list`, `files.mkdir`, `files.rename`, `files.plan`, `files.run`, `files.cancel`, `files.cancelAll`; protocol 4.
  - New bullet under **Hub and bridge**, after Import: "Files (`files.go`, `filejobs.go`, `internal/files`): `files.list/mkdir/rename` run on the manager's shared SFTP client (`Manager.SFTP`); transfers and deletes are jobs (`files.plan` answers at once and walks in the background, `files.planned` → `files.run {conflict}` → `files.progress`… → `files.done`; `files.cancel` is a notification and works while locked; `files.cancelAll` is main-only), each on its own SFTP channel (`Manager.NewSFTP`). Params are decoded strictly (`strictParams`: exact keys, no duplicates). Remote names are untrusted (one element, UTF-8, `filepath.Localize` for downloads, case clashes are errors); local I/O goes through `os.Root`; files are written to random `O_EXCL` part files and committed without replacing unless the author chose overwrite; walks never follow symlinks (a selected one is resolved once); delete never uses `RemoveAll`. `servers.save/delete/forgetHostKey` end a server's jobs (`"server changed"`) before closing its connection. Each job writes start and end `kind: "file"` audit records (local paths included); mkdir and rename write one."
  - **desktop/** section: a bullet for the Files tab (`FilesView.tsx`, `FileDialogs.tsx`, `files.ts`): "Files tab per host (`TabSet` kind `files`): lists only on open and on the author's actions; local paths exist only in main (`main/files.ts`: `Grants`, single-use 5-minute tokens bound to server and kind, cleared on lock, hub stop, and renderer reload; `FilesRelay` rebuilds `files.plan` from an allowlist and shows the native conflict dialog for downloads); preload's `grantDropped` turns real dropped `File`s into paths with `webUtils.getPathForFile`; `recoverRenderer` also calls `files.cancelAll`. Server-sourced text goes through `displayText` (`shared/display.ts`)."
- `README.md`: a "Files" paragraph under the app features: browse, upload/download folders, drop from Finder, rename, delete; everything as the login user; audited.
- `PRODUCT.md`: move SFTP out of "Not built".
- `docs/superpowers/ROADMAP.md`: row 3a → "Implemented 2026-09-26; exit gate pending (author on a real host)"; Updated line.
- Spec status line: "Approved 2026-09-26; implemented (plan `plans/2026-09-26-slice3a-sftp.md`)."
- `docs/superpowers/checklists/slice3a-manual.md`:

```markdown
# Slice 3a manual checklist

1. Exit gate, on a real host (e.g. buildpc): open Files, browse to a folder, upload a folder, upload it again and choose Skip existing, download it back, rename it, delete it (type `delete`).
2. Drag a folder from Finder onto a Files tab: it uploads into the folder shown. (Playwright cannot produce a real dropped file.)
3. Windows or Linux: Upload files and Upload folder both work.
4. Lock the vault during a large download: it keeps running; Cancel still works; a new transfer asks for unlock.
5. Save a host's port while a transfer runs: the transfer ends with "server changed"; the editor warned about it first.
6. A folder you cannot read shows "Permission denied".
```

- [ ] **Step 5: Full verification**

Run:
```bash
go vet ./... && GOOS=windows go vet ./... && go test -race ./...
cd desktop && npm run typecheck && npm test && npm run e2e
```
Expected: all PASS (Docker tests PASS or SKIP).

- [ ] **Step 6: Commit**

```bash
git add CLAUDE.md README.md PRODUCT.md docs desktop/e2e
git commit -m "test(e2e): files tab end to end; docs for slice 3a"
```

## Self-review notes (for the executor)

- Spec coverage: every Security rule has a task and a test (tokens: 9; strict params: 7–8; grants/lock: 9; native download conflicts: 9, 12; drops: 9; `os.Root`: 5–6; names: 3, 5, 6; part files and no-replace: 6; symlinks: 5–6; real paths: 3, 4, 5, 7; permissions: 3, 6; sanitising: 9–11; bounds: 4–5; UI door only: 7). Jobs/cancel/lock/server change/cancelAll/expiry/progress: 8. Audit: 7–8. Idle: `files.*` requests call `h.touch()`, notifications do not (7–8). Live test: 8. e2e and docs: 12.
- Deviation recorded in Plan decisions: mtime set by path on the remote part file (2); grants clear on the `locked` notification (3); listing memory bounded by time, not entries (7); Docker test skips without SFTP (8). The spec's e2e item "a transfer running through a lock" is covered by the Go test `TestJobCancelWhileRunningAndWhileLocked` instead (sshtestd has no gate flag); the manual checklist item 4 covers it in the app.

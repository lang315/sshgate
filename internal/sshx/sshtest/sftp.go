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

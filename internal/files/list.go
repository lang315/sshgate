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
// raw SSH_FXP_READDIR loop that stops at MaxList. A single hung ReadLink on
// the shared client cannot itself be cancelled (pkg/sftp has no
// ReadLinkContext, and closing the shared client would kill other listings
// in flight); the connection's keepalive is the backstop for that case.
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
		if err := ctx.Err(); err != nil {
			return Listing{}, err
		}
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

// Mkdir creates one folder. Its name must pass the listing's name rules, or
// the next listing would hide it.
func Mkdir(c *sftp.Client, p string) error {
	if err := CheckAbs(p); err != nil {
		return err
	}
	if err := checkRemoteName(path.Base(p)); err != nil {
		return err
	}
	return c.Mkdir(p)
}

var errExists = errors.New("already exists; not replaced")

// Rename never replaces: the Lstat check on to is the enforced guard. The
// remaining check-then-rename window is covered on OpenSSH servers, because
// plain SSH_FXP_RENAME there also refuses to replace an existing name
// (verified by TestSFTPAgainstOpenSSH in CI).
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
	if err := checkRemoteName(path.Base(to)); err != nil { // as Mkdir
		return err
	}
	if _, err := c.Lstat(to); err == nil {
		return errExists
	}
	return c.Rename(from, to)
}

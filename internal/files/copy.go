package files

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

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
// For an upload, pkg/sftp's concurrent writer calls it from a goroutine
// other than the one that called Run; a caller must be goroutine-safe.
type Progress func(file string, done, total int64)

var (
	errSymlinkDest   = errors.New("destination is a symlink; not written through")
	errAppeared      = errors.New("destination appeared after the plan; not replaced")
	errNoReplace     = errors.New("the server cannot replace files (no posix-rename)")
	errParentMissing = errors.New("the containing folder could not be created")
	errDirReplaced   = errors.New("a folder was replaced before it could be made private")
)

func hasPosixRename(c *sftp.Client) bool {
	_, ok := c.HasExtension("posix-rename@openssh.com")
	return ok
}

// under reports whether rel names something inside one of dirs, folders
// whose creation failed this run, so nothing under them is written.
func under(rel string, dirs []string) bool {
	for _, d := range dirs {
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

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
	var made []item              // folders this run created; their final mode is set last
	private := map[string]bool{} // their paths: 0700 until then, so nothing staged in them
	var failedDirs []string      // folders whose mkdir failed; nothing under them is written
	// A no-replace server would fail every conflicting upload at commit time
	// anyway; check once so those files are never sent just to fail.
	noReplace := p.Op == OpUpload && overwrite && !hasPosixRename(c)
	defer func() {
		for i := len(made) - 1; i >= 0; i-- {
			p.setDirMode(c, made[i])
		}
	}()
	for _, it := range p.items {
		if ctx.Err() != nil {
			return
		}
		if under(it.rel, failedDirs) {
			r.Errors.Add(it.rel, errParentMissing)
			continue
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
				failedDirs = append(failedDirs, it.rel)
				continue
			}
			made = append(made, it)
			private[it.rel] = true
			continue
		}
		if it.exists {
			if !overwrite {
				r.Skipped++
				continue
			}
			if noReplace {
				r.Errors.Add(it.rel, errNoReplace)
				continue
			}
		}
		base := done
		prog := func(n int64) { progress(it.rel, base+n, p.Bytes) }
		var n int64
		var err error
		if p.Op == OpDownload {
			n, err = p.download(ctx, c, it, overwrite && it.exists, prog)
		} else {
			n, err = p.upload(ctx, c, it, overwrite && it.exists, private[path.Dir(it.rel)], prog)
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
		return bare(p.local.Mkdir(filepath.FromSlash(rel), 0o700))
	}
	d := path.Join(p.dest, rel)
	if err := c.Mkdir(d); err != nil {
		return err
	}
	return makePrivate(c, d)
}

// makePrivate chmods a folder the run just made to 0700, before anything is
// put in it. A setgid bit it inherited is kept: chmod clears it on Linux,
// and files in a setgid folder must get the folder's group. A folder that
// is no longer a folder (swapped for a link since the mkdir) is refused.
func makePrivate(c *sftp.Client, d string) error {
	fi, err := c.Lstat(d)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errDirReplaced
	}
	return c.Chmod(d, 0o700|fi.Mode()&fs.ModeSetgid)
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
		return 0, bare(err)
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
		return w.n, bare(err)
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

func (p *Plan) upload(ctx context.Context, c *sftp.Client, it item, replace, inPrivate bool, prog func(int64)) (int64, error) {
	final := path.Join(p.dest, it.rel)
	part := path.Join(path.Dir(final), partName(path.Base(final)))
	// it.root.Open follows an in-root symlink; a source swapped for one after
	// the plan must not be read as if it were still the original file.
	lfi, err := it.root.Lstat(it.src)
	if err != nil {
		return 0, bare(err)
	}
	if !lfi.Mode().IsRegular() {
		return 0, errNotRegular
	}
	src, err := it.root.Open(it.src)
	if err != nil {
		return 0, bare(err)
	}
	defer src.Close()
	if st, err := src.Stat(); err != nil || !st.Mode().IsRegular() || !os.SameFile(lfi, st) {
		return 0, errNotRegular
	}
	// pkg/sftp v1.13.11 has no create-with-attributes open: the server creates
	// the part file at its own default mode, and a chmod cannot revoke an fd
	// another user opens before it. So the part file is created only inside
	// a 0700 folder: one this run made (inPrivate), else a staging folder
	// of the part file's name beside final, so the commit's link or rename
	// never crosses a filesystem.
	// ponytail: one staging folder per top-level file (3 round trips); share
	// one per parent if many top-level files measurably hurt.
	exposed := false
	if !inPrivate {
		stage := part
		if err := c.Mkdir(stage); err != nil {
			// A server that refuses folders (sftp-server -P mkdir, an
			// upload-only account) gets the part file beside final as before,
			// at the server's default mode until the chmod below.
			exposed = true
		} else {
			defer c.RemoveDirectory(stage)
			if err := makePrivate(c, stage); err != nil {
				return 0, err
			}
			part = path.Join(stage, "data")
		}
	}
	dst, err := c.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return 0, err
	}
	if exposed {
		if err := dst.Chmod(0o600); err != nil {
			dst.Close()
			c.Remove(part)
			return 0, err
		}
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
		return r.n, bare(err)
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

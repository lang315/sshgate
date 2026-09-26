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
		return bare(p.local.Mkdir(filepath.FromSlash(rel), 0o700))
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

func (p *Plan) upload(ctx context.Context, c *sftp.Client, it item, replace bool, prog func(int64)) (int64, error) {
	final := path.Join(p.dest, it.rel)
	part := path.Join(path.Dir(final), partName(path.Base(final)))
	src, err := it.root.Open(it.src)
	if err != nil {
		return 0, bare(err)
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

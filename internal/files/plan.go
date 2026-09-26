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
	errNotFolder     = errors.New("destination exists and is not a folder")
	errNotFile       = errors.New("destination exists and is not a file")
	errCaseClash     = errors.New("another name here differs only in case")
	errNotRegular    = errors.New("not a regular file or folder")
	errDuplicateName = errors.New("another selected source has this name already")
)

// bare strips a *fs.PathError down to its underlying error, so a local
// filesystem error never repeats the (possibly sensitive) local path.
func bare(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

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
	if depth >= MaxDepth {
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
		return nil, errors.New("the download folder is missing or not a folder")
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
	w := &walker{ctx: ctx, p: p, seen: map[string]map[string]bool{}}
	for _, s := range sources {
		if !filepath.IsAbs(s) {
			p.Close()
			return nil, errors.New("local sources must be absolute")
		}
		base := filepath.Base(s)
		if w.seen[""] == nil {
			w.seen[""] = map[string]bool{}
		}
		low := strings.ToLower(base)
		if w.seen[""][low] { // two sources collapsing onto one remote name
			p.Errors.Add(base, errDuplicateName)
			continue
		}
		w.seen[""][low] = true
		resolved := s
		if lfi, err := os.Lstat(s); err == nil && lfi.Mode()&fs.ModeSymlink != 0 {
			r, err := filepath.EvalSymlinks(s) // a selected symlink is followed once
			if err != nil {
				p.Errors.Add(base, bare(err))
				continue
			}
			resolved = r
		}
		root, err := os.OpenRoot(filepath.Dir(resolved))
		if err != nil {
			p.Errors.Add(base, bare(err))
			continue
		}
		p.roots = append(p.roots, root)
		inRoot := filepath.Base(resolved)
		fi, err := root.Lstat(inRoot)
		if err != nil {
			p.Errors.Add(base, bare(err))
			continue
		}
		if err := w.up(c, root, inRoot, base, fi, 0); err != nil {
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
		p.Errors.Add(rel, err)
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
			p.Errors.Add(rel, bare(err))
			return nil
		}
		des, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			p.Errors.Add(rel, bare(err))
			return nil
		}
		for _, de := range des {
			cfi, err := de.Info() // Lstat semantics
			if err != nil {
				p.Errors.Add(path.Join(rel, de.Name()), bare(err))
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

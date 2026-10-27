// Package davfs is an environment's files as one tree: its home at
// /workspace and its volumes at their paths in the environment, under
// synthesized parent folders, the way the desktop sees them. It implements
// webdav.FileSystem, without any notion of who is asking: authentication and
// HTTP are the caller's.
//
// Each mount is an os.Root, so neither ".." nor a symlink leads out of it.
// Writes go to a hidden ".part" file renamed over the target when complete;
// macOS metadata files (.DS_Store, ._*) are accepted and dropped.
package davfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/webdav"
)

// desktopUID owns the desktops' files; a supervisor running as root gives
// what it creates to it.
const desktopUID = 1000

// partPrefix and partSuffix name the hidden files writes go to first.
const (
	partPrefix = ".dav-"
	partSuffix = ".part"
)

// A Mount is one folder of the tree.
type Mount struct {
	At       string // where it appears, such as /workspace or /share/photos: clean and absolute
	Folder   string // the folder holding its files
	ReadOnly bool
}

type mount struct {
	at       string
	root     *os.Root
	readOnly bool
}

// FS is a tree of mounts. Close it when done.
type FS struct {
	mounts []mount
	chown  bool
}

var (
	errReadOnly = &fs.PathError{Op: "write", Err: fs.ErrPermission}
	// ErrIncomplete: an upload's body ended early; the file was not replaced.
	ErrIncomplete = errors.New("the upload was incomplete")
)

// New opens the mounts. One whose folder doesn't exist is left out.
func New(mounts []Mount) (*FS, error) {
	f := &FS{chown: os.Geteuid() == 0}
	for _, m := range mounts {
		if !path.IsAbs(m.At) || path.Clean(m.At) != m.At || m.At == "/" {
			f.Close()
			return nil, errors.New("davfs: mount path " + m.At + " must be clean, absolute and not /")
		}
		root, err := os.OpenRoot(m.Folder)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			f.Close()
			return nil, err
		}
		f.mounts = append(f.mounts, mount{at: m.At, root: root, readOnly: m.ReadOnly})
	}
	return f, nil
}

func (f *FS) Close() {
	for _, m := range f.mounts {
		m.root.Close()
	}
}

// junk is a macOS metadata file: accepted, never stored.
func junk(name string) bool {
	b := path.Base(name)
	return b == ".DS_Store" || strings.HasPrefix(b, "._")
}

// Dropped reports whether name is a macOS metadata file, which the tree
// accepts and never stores.
func Dropped(name string) bool { return junk(name) }

// hidden is left out of listings: junk, and unfinished writes.
func hidden(base string) bool {
	return junk(base) || strings.HasPrefix(base, partPrefix) && strings.HasSuffix(base, partSuffix)
}

// resolve finds the mount holding name and the path within it ("." for the
// mount itself), or reports that name is a synthesized folder above mounts.
func (f *FS) resolve(name string) (m *mount, rel string, synthesized bool, err error) {
	name = path.Clean("/" + name)
	for i := range f.mounts {
		m := &f.mounts[i]
		if name == m.at {
			return m, ".", false, nil
		}
		if r, ok := strings.CutPrefix(name, m.at+"/"); ok {
			if hidden(path.Base(r)) && !junk(r) {
				return nil, "", false, fs.ErrNotExist // nobody reaches a .part file
			}
			return m, r, false, nil
		}
	}
	if slices.ContainsFunc(f.mounts, func(m mount) bool { return name == "/" || strings.HasPrefix(m.at, name+"/") }) {
		return nil, "", true, nil
	}
	return nil, "", false, fs.ErrNotExist
}

// Writable reports whether name may be created, changed or removed: inside
// a writable mount, and not a mount itself. macOS metadata files always
// are, since they are dropped.
func (f *FS) Writable(name string) bool {
	if junk(name) {
		return true
	}
	m, rel, synthesized, err := f.resolve(name)
	return err == nil && !synthesized && !m.readOnly && rel != "."
}

func (f *FS) owned(root *os.Root, rel string) {
	if f.chown {
		_ = root.Lchown(rel, desktopUID, desktopUID)
	}
}

func (f *FS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	if junk(name) || !f.Writable(name) {
		return errReadOnly
	}
	m, rel, _, err := f.resolve(name)
	if err != nil {
		return err
	}
	if err := m.root.Mkdir(rel, 0o755); err != nil {
		return err
	}
	f.owned(m.root, rel)
	return nil
}

func (f *FS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	writing := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	if junk(name) {
		if writing {
			return &sink{name: path.Base(name)}, nil
		}
		return nil, fs.ErrNotExist
	}
	m, rel, synthesized, err := f.resolve(name)
	if err != nil {
		if _, _, parentSynthesized, _ := f.resolve(path.Dir(path.Clean("/" + name))); writing && parentSynthesized {
			return nil, errReadOnly // a new name beside the mounts
		}
		return nil, err
	}
	if synthesized {
		if writing {
			return nil, errReadOnly
		}
		return &synthesizedDir{f: f, name: path.Clean("/" + name)}, nil
	}
	if !writing {
		file, err := m.root.Open(rel)
		if err != nil {
			return nil, err
		}
		return &listed{File: file}, nil
	}
	if m.readOnly || rel == "." {
		return nil, errReadOnly
	}
	if flag&os.O_TRUNC == 0 || flag&os.O_APPEND != 0 {
		return nil, errReadOnly // only whole-file writes, which is all WebDAV makes
	}
	return f.createPart(ctx, m, rel)
}

func (f *FS) createPart(ctx context.Context, m *mount, rel string) (*part, error) {
	mode := fs.FileMode(0o644)
	if fi, err := m.root.Stat(rel); err == nil {
		if fi.IsDir() {
			return nil, &fs.PathError{Op: "open", Path: rel, Err: errors.New("is a folder")}
		}
		mode = fi.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if _, err := m.root.Stat(path.Dir(rel)); err != nil {
		return nil, err // a missing parent is the caller's 409
	}
	partRel := path.Join(path.Dir(rel), partPrefix+randomHex()+partSuffix)
	file, err := m.root.OpenFile(partRel, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nil, err
	}
	_ = file.Chmod(mode) // past the umask
	f.owned(m.root, partRel)
	return &part{File: file, root: m.root, rel: rel, partRel: partRel, upload: uploadOf(ctx), ctx: ctx}, nil
}

func (f *FS) RemoveAll(ctx context.Context, name string) error {
	if junk(name) {
		return nil
	}
	if !f.Writable(name) {
		return errReadOnly
	}
	m, rel, _, err := f.resolve(name)
	if err != nil {
		return err
	}
	if _, err := m.root.Lstat(rel); err != nil {
		return err
	}
	if r := replacementOf(ctx); r != nil && r.target == path.Clean("/"+name) && r.root == nil {
		// Set aside rather than removed, until the copy or move replacing it succeeds.
		stash := path.Join(path.Dir(rel), partPrefix+randomHex()+partSuffix)
		if err := m.root.Rename(rel, stash); err != nil {
			return err
		}
		r.root, r.rel, r.stash = m.root, rel, stash
		return nil
	}
	return m.root.RemoveAll(rel)
}

// Rename moves within a mount, or copies to another mount and then removes
// the source. A failed copy removes what it made and leaves the source.
// Moving a macOS metadata file onto a real one, or the reverse, is refused.
func (f *FS) Rename(ctx context.Context, oldName, newName string) error {
	if junk(oldName) && junk(newName) {
		return nil
	}
	if junk(oldName) || junk(newName) || !f.Writable(oldName) || !f.Writable(newName) {
		return errReadOnly
	}
	src, srcRel, _, err := f.resolve(oldName)
	if err != nil {
		return err
	}
	dst, dstRel, _, err := f.resolve(newName)
	if err != nil {
		return err
	}
	if src == dst {
		return src.root.Rename(srcRel, dstRel)
	}
	if _, err := dst.root.Lstat(dstRel); err == nil {
		return &fs.PathError{Op: "rename", Path: newName, Err: fs.ErrExist}
	}
	if err := f.copyTree(src.root, srcRel, dst.root, dstRel); err != nil {
		_ = dst.root.RemoveAll(dstRel)
		return err
	}
	// The move has arrived whole. What can't be removed of the source (in a
	// read-only folder, say) stays behind rather than failing the move: a
	// failed move would have its copy undone, and with it the files already
	// removed from the source.
	_ = src.root.RemoveAll(srcRel)
	return nil
}

func (f *FS) copyTree(from *os.Root, fromRel string, to *os.Root, toRel string) error {
	fi, err := from.Lstat(fromRel)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := from.Readlink(fromRel)
		if err != nil {
			return err
		}
		if err := to.Symlink(target, toRel); err != nil {
			return err
		}
	case fi.IsDir():
		// Writable while its children are copied, then the source's mode.
		if err := to.Mkdir(toRel, 0o700); err != nil {
			return err
		}
		defer to.Chmod(toRel, fi.Mode().Perm())
		dir, err := from.Open(fromRel)
		if err != nil {
			return err
		}
		entries, err := dir.ReadDir(-1)
		dir.Close()
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := f.copyTree(from, path.Join(fromRel, e.Name()), to, path.Join(toRel, e.Name())); err != nil {
				return err
			}
		}
	case fi.Mode().IsRegular():
		if err := copyFile(from, fromRel, to, toRel, fi.Mode().Perm()); err != nil {
			return err
		}
	default:
		return nil // sockets, devices and pipes aren't files to carry over
	}
	f.owned(to, toRel)
	return nil
}

func copyFile(from *os.Root, fromRel string, to *os.Root, toRel string, mode fs.FileMode) error {
	in, err := from.Open(fromRel)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := to.OpenFile(toRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	_ = out.Chmod(mode)
	return out.Close()
}

func (f *FS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	if junk(name) {
		return nil, fs.ErrNotExist
	}
	m, rel, synthesized, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	if synthesized {
		return folderInfo(path.Base(path.Clean("/" + name))), nil
	}
	fi, err := m.root.Stat(rel)
	if err == nil && rel == "." {
		fi = renamed{fi, path.Base(m.at)}
	}
	return fi, err
}

// children lists a synthesized folder: the next path element of each mount
// below it.
func (f *FS) children(dir string) []fs.FileInfo {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	var names []string
	for _, m := range f.mounts {
		if rest, ok := strings.CutPrefix(m.at, prefix); ok {
			if n, _, _ := strings.Cut(rest, "/"); !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	slices.Sort(names)
	var out []fs.FileInfo
	for _, n := range names {
		if fi, err := f.Stat(context.Background(), prefix+n); err == nil {
			out = append(out, fi)
		}
	}
	return out
}

// --- uploads ------------------------------------------------------------------

type upload struct{ complete atomic.Bool }

type uploadKey struct{}

func uploadOf(ctx context.Context) *upload {
	u, _ := ctx.Value(uploadKey{}).(*upload)
	return u
}

// WithUpload marks ctx as carrying an upload whose body is the returned
// reader: a write made under ctx replaces its file only when that body was
// read to its end. Without it (a COPY, a LOCK of a new name) writes are
// taken as complete.
func WithUpload(ctx context.Context, body io.ReadCloser) (context.Context, io.ReadCloser) {
	u := &upload{}
	return context.WithValue(ctx, uploadKey{}, u), &uploadBody{ReadCloser: body, u: u, ctx: ctx}
}

type uploadBody struct {
	io.ReadCloser
	u   *upload
	ctx context.Context
}

func (b *uploadBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.u.complete.Store(true)
	}
	return n, err
}

// part is a file being written: renamed over its target on Close.
type part struct {
	*os.File
	root         *os.Root
	rel, partRel string
	upload       *upload
	ctx          context.Context
}

func (p *part) Close() error {
	err := p.File.Close()
	if err == nil && (p.upload != nil && !p.upload.complete.Load() || p.ctx.Err() != nil) {
		err = ErrIncomplete // cut short, or the request was cancelled (access revoked)
	}
	if err == nil {
		err = p.root.Rename(p.partRel, p.rel)
	}
	if err != nil {
		_ = p.root.Remove(p.partRel)
	}
	return err
}

// --- replacing ------------------------------------------------------------------

type replacement struct {
	target     string // the destination, as a name in the tree
	root       *os.Root
	rel, stash string
}

type replacementKey struct{}

func replacementOf(ctx context.Context) *replacement {
	r, _ := ctx.Value(replacementKey{}).(*replacement)
	return r
}

// WithReplacement makes a COPY or MOVE onto target, under the returned
// context, set the existing target aside instead of removing it first.
// done(true) then removes what was set aside; done(false) puts it back in
// place of whatever the failed copy or move left.
func WithReplacement(ctx context.Context, target string) (context.Context, func(ok bool) error) {
	r := &replacement{target: path.Clean("/" + target)}
	return context.WithValue(ctx, replacementKey{}, r), func(ok bool) error {
		if r.root == nil {
			return nil
		}
		if ok {
			return r.root.RemoveAll(r.stash)
		}
		if err := r.root.RemoveAll(r.rel); err != nil {
			return err
		}
		return r.root.Rename(r.stash, r.rel)
	}
}

func randomHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- listings -----------------------------------------------------------------

// listed is an opened file or folder whose listing leaves out hidden names.
type listed struct{ *os.File }

func (l *listed) Readdir(count int) ([]fs.FileInfo, error) {
	for {
		fis, err := l.File.Readdir(count)
		fis = slices.DeleteFunc(fis, func(fi fs.FileInfo) bool { return hidden(fi.Name()) })
		if len(fis) > 0 || err != nil || count <= 0 {
			return fis, err
		}
	}
}

func (l *listed) Write([]byte) (int, error) { return 0, errReadOnly }

type synthesizedDir struct {
	f    *FS
	name string
	done bool
}

func (d *synthesizedDir) Close() error                   { return nil }
func (d *synthesizedDir) Read([]byte) (int, error)       { return 0, io.EOF }
func (d *synthesizedDir) Seek(int64, int) (int64, error) { return 0, nil }
func (d *synthesizedDir) Write([]byte) (int, error)      { return 0, errReadOnly }
func (d *synthesizedDir) Stat() (fs.FileInfo, error)     { return folderInfo(path.Base(d.name)), nil }
func (d *synthesizedDir) Readdir(count int) ([]fs.FileInfo, error) {
	if d.done && count > 0 {
		return nil, io.EOF
	}
	d.done = true
	return d.f.children(d.name), nil
}

// sink takes a macOS metadata file's content and keeps none of it.
type sink struct{ name string }

func (s *sink) Close() error                       { return nil }
func (s *sink) Read([]byte) (int, error)           { return 0, io.EOF }
func (s *sink) Seek(int64, int) (int64, error)     { return 0, nil }
func (s *sink) Write(p []byte) (int, error)        { return len(p), nil }
func (s *sink) Readdir(int) ([]fs.FileInfo, error) { return nil, nil }
func (s *sink) Stat() (fs.FileInfo, error)         { return fileInfo(s.name), nil }

type folderInfo string

func (n folderInfo) Name() string       { return string(n) }
func (n folderInfo) Size() int64        { return 0 }
func (n folderInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (n folderInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (n folderInfo) IsDir() bool        { return true }
func (n folderInfo) Sys() any           { return nil }

type fileInfo string

func (n fileInfo) Name() string       { return string(n) }
func (n fileInfo) Size() int64        { return 0 }
func (n fileInfo) Mode() fs.FileMode  { return 0o644 }
func (n fileInfo) ModTime() time.Time { return time.Now() }
func (n fileInfo) IsDir() bool        { return false }
func (n fileInfo) Sys() any           { return nil }

// renamed gives a mount's folder the name it has in the tree.
type renamed struct {
	fs.FileInfo
	name string
}

func (r renamed) Name() string { return r.name }

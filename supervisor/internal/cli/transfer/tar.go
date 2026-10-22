// Package transfer implements `deskyard cp`: files and directories streamed
// through the exec WebSocket with cat and tar.
package transfer

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// WriteTar writes the contents of dir (not dir itself) as a tar stream, with
// names like ./a/b, the way `tar -C dir -cf - .` does.
func WriteTar(w io.Writer, dir string) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		name := "./" + filepath.ToSlash(rel)
		if rel == "." {
			name = "./"
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&fs.ModeSymlink == 0 {
			return nil // sockets, devices, fifos: skip
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = name
		if info.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
			hdr.Name += "/"
		}
		// Owner and group are the remote side's business (files land as user).
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if runtime.GOOS == "windows" {
			if info.IsDir() {
				hdr.Mode = 0o755
			} else {
				hdr.Mode = 0o644
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

// ExtractTar unpacks a tar stream into dir (created if missing). Entries
// that would land outside dir are refused.
func ExtractTar(r io.Reader, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the archive: %v", err)
		}
		clean := path.Clean("/" + hdr.Name)
		if clean == "/" {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(clean[1:]))
		if !within(root, target) {
			return fmt.Errorf("refusing archive entry outside the destination: %s", hdr.Name)
		}
		if err := checkNoSymlinkParents(root, filepath.Dir(target)); err != nil {
			return err
		}
		mode := fs.FileMode(hdr.Mode) & fs.ModePerm
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			_ = os.Chmod(target, mode|0o700)
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target) // replace symlinks rather than write through them
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			if runtime.GOOS == "windows" {
				fmt.Fprintf(os.Stderr, "deskyard cp: skipping symlink %s -> %s (not supported on Windows)\n", hdr.Name, hdr.Linkname)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		default:
			// hard links, devices, fifos: skip
		}
	}
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkNoSymlinkParents refuses to write through a symlinked directory that
// an earlier archive entry created.
func checkNoSymlinkParents(root, dir string) error {
	for d := dir; within(root, d) && d != root; d = filepath.Dir(d) {
		fi, err := os.Lstat(d)
		if err != nil {
			continue
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink %s", d)
		}
	}
	return nil
}

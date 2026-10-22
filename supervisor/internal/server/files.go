package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// Browsing and downloading a desktop's files. Everything is read inside the
// desktop as `user` through exec, so what the desktop's own user can't read
// can't be downloaded, and a path is only trusted after the desktop itself
// has resolved it (symlinks and ".." included) to /workspace or below.

const (
	filesRoot       = "/workspace"
	maxListEntries  = 2000
	maxListScan     = 200000 // entries read from one folder before giving up on the rest
	fileListTimeout = 20 * time.Second
)

var (
	errOutside  = errors.New("only files under /workspace can be downloaded")
	errNoSuchFS = errors.New("no such file or folder")
)

type fileEntry struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// insideRoot reports whether a resolved absolute path is root or below it.
func insideRoot(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+"/")
}

// resolveFile has the desktop resolve p (relative to /workspace) and refuses
// anything that ends up outside /workspace. The result is the real path.
func (s *Server) resolveFile(ctx context.Context, c desktops.Desktop, p string) (string, error) {
	if p == "" {
		p = filesRoot
	}
	if strings.ContainsRune(p, 0) {
		return "", errNoSuchFS
	}
	out, err := s.execOutput(ctx, c, "realpath", "-e", "--", filesRoot, p)
	if err != nil {
		var ee *execError
		if errors.As(err, &ee) {
			return "", errNoSuchFS
		}
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 2 {
		return "", errNoSuchFS
	}
	root, real := lines[0], lines[1]
	if !insideRoot(real, root) {
		return "", errOutside
	}
	return real, nil
}

// fileKind is "dir", "file" or "other" for a resolved path, with its size.
func (s *Server) fileKind(ctx context.Context, c desktops.Desktop, real string) (kind string, size int64, err error) {
	// find's %y is a letter, so the answer doesn't depend on the desktop's locale.
	out, err := s.execOutput(ctx, c, "find", real, "-maxdepth", "0", "-printf", `%y\t%s`)
	if err != nil {
		return "", 0, errNoSuchFS
	}
	f, sz, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	size, _ = strconv.ParseInt(sz, 10, 64)
	switch f {
	case "d":
		return "dir", size, nil
	case "f":
		return "file", size, nil
	}
	return "other", size, nil
}

func fileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errOutside):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errNoSuchFS):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// filesTarget is the common start of both routes: access, running, a resolved path.
func (s *Server) filesTarget(w http.ResponseWriter, r *http.Request, ctx context.Context) (desktops.Desktop, string, string, int64, bool) {
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return c, "", "", 0, false
	}
	if !c.Running() {
		writeError(w, http.StatusConflict, desktops.ErrStopped.Error())
		return c, "", "", 0, false
	}
	real, err := s.resolveFile(ctx, c, r.URL.Query().Get("path"))
	if err != nil {
		fileError(w, err)
		return c, "", "", 0, false
	}
	kind, size, err := s.fileKind(ctx, c, real)
	if err != nil {
		fileError(w, err)
		return c, "", "", 0, false
	}
	return c, real, kind, size, true
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := timeout(r, fileListTimeout)
	defer cancel()
	c, real, kind, _, ok := s.filesTarget(w, r, ctx)
	if !ok {
		return
	}
	if kind != "dir" {
		writeError(w, http.StatusBadRequest, real+" is not a folder")
		return
	}
	var entries []fileEntry
	err := s.execStream(ctx, c, 0, []string{"find", real, "-mindepth", "1", "-maxdepth", "1", "-printf", `%Y\t%s\t%T@\t%f\0`},
		func(out io.Reader) error {
			var err error
			entries, err = parseListing(out)
			return err
		})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Modified != entries[j].Modified {
			return entries[i].Modified > entries[j].Modified
		}
		return entries[i].Name < entries[j].Name
	})
	truncated := len(entries) > maxListEntries
	if truncated {
		entries = entries[:maxListEntries]
	}
	if entries == nil {
		entries = []fileEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": real, "entries": entries, "truncated": truncated})
}

// parseListing reads find's NUL-terminated "type\tsize\tmtime\tname" records.
func parseListing(r io.Reader) ([]fileEntry, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxListScan*512))
	if err != nil {
		return nil, err
	}
	var entries []fileEntry
	for _, rec := range bytes.Split(data, []byte{0}) {
		f := strings.SplitN(string(rec), "\t", 4)
		if len(f) != 4 || f[3] == "" {
			continue
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)
		secs, _ := strconv.ParseFloat(f[2], 64)
		mod := time.Unix(int64(secs), 0).UTC().Format(time.RFC3339)
		entries = append(entries, fileEntry{Name: f[3], Dir: f[0] == "d", Size: size, Modified: mod})
		if len(entries) >= maxListScan {
			break
		}
	}
	return entries, nil
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := timeout(r, fileListTimeout)
	c, real, kind, size, ok := s.filesTarget(w, r, ctx)
	cancel() // the transfer itself has no deadline; it ends when the client goes
	if !ok {
		return
	}
	ctx = r.Context()
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	base := path.Base(real)
	if real == filesRoot {
		base = "workspace"
	}
	switch kind {
	case "file":
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": base}))
		h.Set("Content-Length", strconv.FormatInt(size, 10))
		err := s.execStream(ctx, c, 0, []string{"cat", "--", real}, func(out io.Reader) error {
			_, err := io.Copy(w, out)
			return err
		})
		if err != nil {
			s.abortDownload(w, c, real, err)
		}
	case "dir":
		h.Set("Content-Type", "application/zip")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": base + ".zip"}))
		// tar exits 1 when a file changed or vanished while it was read; the archive is still
		// complete. Unreadable files are skipped rather than failing the whole folder.
		err := s.execStream(ctx, c, 1, []string{"tar", "--ignore-failed-read", "--warning=no-file-changed", "--hard-dereference", "-C", real, "-cf", "-", "."}, func(out io.Reader) error {
			return tarToZip(w, out)
		})
		if err != nil {
			s.abortDownload(w, c, real, err)
		}
	default:
		writeError(w, http.StatusBadRequest, base+" is not a regular file or a folder")
	}
}

// abortDownload cuts the connection: a failure after the headers went out
// must not look like a complete download.
func (s *Server) abortDownload(w http.ResponseWriter, c desktops.Desktop, real string, err error) {
	s.Log.Warn("download failed", "desktop", c.Name, "path", real, "err", err)
	panic(http.ErrAbortHandler)
}

// execStream runs cmd as the desktop's user and hands its stdout to fn. An exit
// code above okUpTo is an error, as is fn's.
func (s *Server) execStream(ctx context.Context, c desktops.Desktop, okUpTo int, cmd []string, fn func(stdout io.Reader) error) error {
	p, err := s.Eng.Exec(ctx, c.Container.ID, engine.ExecSpec{Cmd: cmd, User: "user", WorkDir: filesRoot, Env: append([]string{"LC_ALL=C"}, execEnv...)})
	if err != nil {
		return err
	}
	defer p.Close()
	var stderr bytes.Buffer
	errDone := make(chan struct{})
	go func() {
		defer close(errDone)
		if p.Stderr != nil {
			_, _ = io.Copy(&limitedWriter{w: &stderr, n: 4096}, p.Stderr)
		}
	}()
	ferr := fn(p.Stdout)
	if ferr != nil {
		p.Close() // unblock the command
	} else {
		_, _ = io.Copy(io.Discard, p.Stdout)
	}
	<-errDone
	code, werr := p.Wait()
	switch {
	case ferr != nil:
		return ferr
	case werr != nil:
		return werr
	case code > okUpTo:
		return &execError{code, strings.TrimSpace(stderr.String())}
	}
	return nil
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

// tarToZip streams a tar archive out as a zip. Only folders and regular
// files go in: links aren't followed, so nothing outside the folder can leak.
func tarToZip(w io.Writer, tarStream io.Reader) error {
	zw := zip.NewWriter(w)
	tr := tar.NewReader(tarStream)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(path.Clean("/"+hdr.Name), "/")
		if name == "" {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: name + "/", Modified: hdr.ModTime, Method: zip.Store}); err != nil {
				return err
			}
		case tar.TypeReg:
			fh := &zip.FileHeader{Name: name, Modified: hdr.ModTime, Method: zip.Deflate}
			fh.SetMode(hdr.FileInfo().Mode())
			fw, err := zw.CreateHeader(fh)
			if err != nil {
				return err
			}
			if _, err := io.Copy(fw, tr); err != nil {
				return err
			}
		}
	}
	return zw.Close()
}

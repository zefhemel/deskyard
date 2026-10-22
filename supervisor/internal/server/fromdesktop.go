package server

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/recipes"
)

// Images made from a desktop: its home folder, sorted into groups, becomes
// the starting home (/opt/deskyard/home-template) of a recipe built on the
// desktop's own image, and its Flatpak apps are installed again system-wide.

// scanTimeout limits reading a home folder for the dialog.
const scanTimeout = 10 * time.Minute

type imageScanJSON struct {
	Groups     recipes.Groups             `json:"groups"`
	Flatpaks   []recipes.InstalledFlatpak `json:"flatpaks"`
	Remotes    []recipes.UserRemote       `json:"remotes"`
	TotalBytes int64                      `json:"totalBytes"`
	// Notes say what the image would leave out, and why (Flatpak remotes
	// without a key, and their apps).
	Notes []string `json:"notes"`
}

func (s *Server) handleImageScan(w http.ResponseWriter, r *http.Request) {
	if a, _ := s.account(r); !requireAdmin(w, a) {
		return
	}
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	ctx, cancel := timeout(r, scanTimeout)
	defer cancel()
	sc := recipes.NewScanner()
	err := s.walkHome(ctx, c.Name, func(rel string, h *tar.Header, _ func() (io.Reader, error)) error {
		if h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeSymlink {
			sc.Add(rel, h.Size)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading "+c.Name+"'s files: "+err.Error())
		return
	}
	fl := s.desktopFlatpaks(ctx, c)
	_, _, notes := recipes.FlatpakParts(fl.apps, fl.remotes, fl.key)
	out := imageScanJSON{
		Groups: sc.Groups(), Flatpaks: fl.apps, Remotes: fl.remotes, TotalBytes: sc.Total(),
		Notes: append(fl.notes, notes...),
	}
	if out.Flatpaks == nil {
		out.Flatpaks = []recipes.InstalledFlatpak{}
	}
	if out.Remotes == nil {
		out.Remotes = []recipes.UserRemote{}
	}
	if out.Notes == nil {
		out.Notes = []string{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleImageFromDesktop(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	var req struct {
		Name    string   `json:"name"`
		Slug    string   `json:"slug"`
		Groups  []string `json:"groups"`
		Include []string `json:"include"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	for _, g := range req.Groups {
		if !recipes.ValidGroup(g) {
			writeError(w, http.StatusBadRequest, "no group "+g)
			return
		}
	}
	for _, p := range req.Include {
		if !recipes.IsSecret(p) || p != path.Clean(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "..") {
			writeError(w, http.StatusBadRequest, p+" isn't a secret: only secrets are ticked one by one")
			return
		}
	}
	rec := recipes.Recipe{
		Slug: req.Slug, Name: strings.TrimSpace(req.Name), Base: c.Image, HomeFrom: recipes.HomeFromImage,
		Secrets: len(req.Include) > 0 || c.ImageLabels[labelSecrets] == "true",
	}
	if slices.Contains(req.Groups, recipes.GroupFlatpak) {
		ctx, cancel := timeout(r, time.Minute)
		fl := s.desktopFlatpaks(ctx, c)
		cancel()
		rec.Flatpaks, rec.Remotes, _ = recipes.FlatpakParts(fl.apps, fl.remotes, fl.key)
	}
	if !s.checkRecipe(w, rec) {
		return
	}
	if _, err := s.recipes.Get(rec.Slug); err == nil {
		writeError(w, http.StatusConflict, "a recipe named "+rec.Slug+" already exists")
		return
	}
	sel := recipes.Selection{Groups: req.Groups, Include: req.Include}
	s.startBuild(w, a, rec, true, buildOptions{home: func(ctx context.Context, tw *tar.Writer) error {
		return s.copyHome(ctx, c.Name, sel, tw)
	}})
}

// copyHome writes the selected files of a desktop's folder into a build
// context as home-template/ (always there, even if empty). Folders are
// written as their files need them, with their own modes; files keep their
// permission bits only (no setuid, setgid or sticky); the image's build
// gives everything to UID 1000.
func (s *Server) copyHome(ctx context.Context, name string, sel recipes.Selection, tw *tar.Writer) error {
	const prefix = "home-template/"
	now := time.Now()
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: prefix, Mode: 0o755, ModTime: now}); err != nil {
		return err
	}
	dirs := map[string]*tar.Header{}
	written := map[string]bool{}
	ensureDir := func(dir string) error {
		if dir == "." || written[dir] {
			return nil
		}
		parts := strings.Split(dir, "/")
		for i := range parts {
			d := strings.Join(parts[:i+1], "/")
			if written[d] {
				continue
			}
			h := &tar.Header{Typeflag: tar.TypeDir, Mode: 0o755, ModTime: now}
			if seen := dirs[d]; seen != nil {
				h.Mode, h.ModTime = seen.Mode&0o777, seen.ModTime
			}
			h.Name = prefix + d + "/"
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			written[d] = true
		}
		return nil
	}
	return s.walkHome(ctx, name, func(rel string, h *tar.Header, content func() (io.Reader, error)) error {
		if h.Typeflag == tar.TypeDir {
			dirs[rel] = h
			return nil
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeSymlink {
			return nil // hard links, sockets, devices
		}
		if keep, _ := sel.Keep(rel); !keep {
			return nil
		}
		if err := ensureDir(path.Dir(rel)); err != nil {
			return err
		}
		out := &tar.Header{Typeflag: h.Typeflag, Name: prefix + rel, Mode: h.Mode & 0o777, ModTime: h.ModTime, Linkname: h.Linkname}
		if h.Typeflag == tar.TypeReg {
			out.Size = h.Size
		}
		if err := tw.WriteHeader(out); err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			return nil
		}
		rd, err := content()
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}
		_, err = io.Copy(tw, rd)
		return err
	})
}

// walkHome calls fn for each folder, file and link in a desktop's folder,
// folders before what's in them. rel is the path relative to the folder.
// h carries the type, permission bits, size, time and link target; content
// gives a regular file's data, exactly h.Size bytes.
//
// The supervisor reads the folder itself where it can, skipping what is
// always left out; otherwise (files of UID 1000 it may not read) a helper
// container reads it, as removing a folder does.
func (s *Server) walkHome(ctx context.Context, name string, fn func(rel string, h *tar.Header, content func() (io.Reader, error)) error) error {
	folder := filepath.Join(s.Desktops.Cfg.DataDir, name)
	if err := readableHere(ctx, folder); err == nil {
		return walkHere(ctx, folder, fn)
	} else if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	rc, err := s.Desktops.FolderTar(ctx, name)
	if err != nil {
		return err
	}
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			rc.Close()
			return err
		}
		rel := strings.Trim(path.Clean(strings.TrimPrefix(h.Name, "./")), "/")
		if rel == "." || rel == "" || strings.HasPrefix(rel, "../") {
			continue
		}
		if err := fn(rel, h, func() (io.Reader, error) { return tr, nil }); err != nil {
			rc.Close()
			return err
		}
	}
	return rc.Close()
}

// skipHere tells walks of the folder which folders not to go into: those
// always left out.
func skipHere(rel string) bool {
	_, ok := recipes.Classify(rel)
	return !ok
}

// readableHere reports whether the supervisor can read everything walkHere
// would read: an error wrapping fs.ErrPermission if not.
func readableHere(ctx context.Context, folder string) error {
	return filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(folder, p)
		rel = filepath.ToSlash(rel)
		if rel != "." && skipHere(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if err := syscall.Access(p, 4 /* R_OK */); err != nil {
				return &fs.PathError{Op: "read", Path: p, Err: fs.ErrPermission}
			}
		}
		return nil
	})
}

func walkHere(ctx context.Context, folder string, fn func(rel string, h *tar.Header, content func() (io.Reader, error)) error) error {
	return filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(folder, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if skipHere(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil // gone since the folder was listed (a running desktop)
		}
		if err != nil {
			return err
		}
		h := &tar.Header{Mode: int64(info.Mode().Perm()), ModTime: info.ModTime()}
		switch {
		case info.IsDir():
			h.Typeflag = tar.TypeDir
		case info.Mode().IsRegular():
			h.Typeflag, h.Size = tar.TypeReg, info.Size()
		case info.Mode()&fs.ModeSymlink != 0:
			h.Typeflag = tar.TypeSymlink
			if h.Linkname, err = os.Readlink(p); err != nil {
				return nil
			}
		default:
			return nil
		}
		var f *os.File
		defer func() {
			if f != nil {
				f.Close()
			}
		}()
		content := func() (io.Reader, error) {
			var err error
			if f, err = os.Open(p); err != nil {
				return nil, err
			}
			// Exactly the size in the header: a file that changed since (a
			// running desktop) is cut or padded.
			return io.LimitReader(io.MultiReader(io.LimitReader(f, h.Size), zeros{}), h.Size), nil
		}
		return fn(rel, h, content)
	})
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// desktopFlatpaks are a desktop's per-user Flatpak apps and remotes, and how
// to read a remote's GPG key.
type desktopFlatpaks struct {
	apps    []recipes.InstalledFlatpak
	remotes []recipes.UserRemote
	key     func(remote string) []byte
	notes   []string
}

// desktopFlatpaks asks Flatpak in a running desktop; a stopped desktop's
// installation is read from its folder.
func (s *Server) desktopFlatpaks(ctx context.Context, c desktops.Desktop) desktopFlatpaks {
	var out desktopFlatpaks
	plain := func(name string) bool {
		return name != "" && !strings.ContainsAny(name, "/\\") && name != "." && name != ".."
	}
	if c.Running() {
		list, err := s.execOutput(ctx, c, "flatpak", "list", "--user", "--app", "--columns=application,origin")
		if err != nil {
			out.notes = append(out.notes, "Couldn't list the Flatpak apps: "+err.Error())
			return out
		}
		rms, err := s.execOutput(ctx, c, "flatpak", "remotes", "--user", "--columns=name,url")
		if err != nil {
			out.notes = append(out.notes, "Couldn't list the Flatpak remotes: "+err.Error())
			return out
		}
		out.apps, out.remotes = recipes.ParseFlatpakList(string(list)), recipes.ParseFlatpakRemotes(string(rms))
		out.key = func(remote string) []byte {
			if !plain(remote) {
				return nil
			}
			b, _ := s.execOutput(ctx, c, "cat", "/workspace/"+recipes.FlatpakKeyPath(remote))
			return b
		}
		return out
	}
	home := os.DirFS(filepath.Join(s.Desktops.Cfg.DataDir, c.Name))
	if _, err := fs.ReadDir(home, ".local/share/flatpak/app"); errors.Is(err, fs.ErrPermission) {
		out.notes = append(out.notes, "Couldn't read the Flatpak apps of a stopped desktop here: start it to include them.")
	}
	out.apps, out.remotes = recipes.FlatpaksInHome(home)
	out.key = func(remote string) []byte {
		if !plain(remote) {
			return nil
		}
		b, _ := fs.ReadFile(home, recipes.FlatpakKeyPath(remote))
		return b
	}
	return out
}

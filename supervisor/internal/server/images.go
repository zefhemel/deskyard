package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// catalog is the list of images desktops can be made from (images.json),
// with the newest pulled image ID per reference, for "update available".
type catalogEntry struct {
	Ref     string     `json:"ref"`
	Newest  string     `json:"newest"` // image ID of the latest pull
	Checked *time.Time `json:"checked"`
}

type catalog struct {
	mu   sync.Mutex
	path string
	list []catalogEntry
}

const checkInterval = 24 * time.Hour

func loadCatalog(stateDir, defaultRef string) (*catalog, error) {
	c := &catalog{path: filepath.Join(stateDir, "images.json")}
	var f struct {
		Images []catalogEntry `json:"images"`
	}
	err := store.ReadJSON(c.path, &f)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.list = []catalogEntry{{Ref: defaultRef}}
		return c, c.save()
	case err != nil:
		return nil, err
	}
	c.list = f.Images
	return c, nil
}

func (c *catalog) save() error {
	return store.WriteJSON(c.path, map[string]any{"images": c.list})
}

func (c *catalog) entries() []catalogEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.list)
}

func (c *catalog) defaultRef() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.list) == 0 {
		return ""
	}
	return c.list[0].Ref
}

func (c *catalog) setNewest(ref, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	for i := range c.list {
		if c.list[i].Ref == ref {
			c.list[i].Newest, c.list[i].Checked = id, &now
		}
	}
	_ = c.save()
}

func (c *catalog) newest(ref string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.list {
		if e.Ref == ref {
			return e.Newest
		}
	}
	return ""
}

// updateFor returns the version a desktop could upgrade to, if any.
func (c *catalog) updateFor(comp desktops.Desktop) *string {
	newest := c.newest(comp.Image)
	if newest == "" || comp.ImageID == "" || newest == comp.ImageID {
		return nil
	}
	v := "newer version"
	if l, ok := c.labelsOf(newest); ok && l["org.opencontainers.image.version"] != "" {
		v = l["org.opencontainers.image.version"]
	}
	return &v
}

// labelsOf is filled by the server with the engine's image labels.
var imageLabels func(id string) (map[string]string, bool)

func (c *catalog) labelsOf(id string) (map[string]string, bool) {
	if imageLabels == nil {
		return nil, false
	}
	return imageLabels(id)
}

// check pulls every catalog image and records the newest IDs.
func (s *Server) checkImages(ctx context.Context, progress func(string, float64)) error {
	var errs []error
	entries := s.images.entries()
	for i, e := range entries {
		progress("Checking "+e.Ref, float64(i)/float64(len(entries)))
		if err := s.Eng.Pull(ctx, e.Ref, nil); err != nil {
			errs = append(errs, err)
			continue
		}
		s.Desktops.ForgetImage(e.Ref)
		img, err := s.Eng.ImageInspect(ctx, e.Ref)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.images.setNewest(e.Ref, img.ID)
	}
	s.events.refresh()
	return errors.Join(errs...)
}

func (c *catalog) checkLoop(ctx context.Context, s *Server) {
	imageLabels = func(id string) (map[string]string, bool) {
		img, err := s.Eng.ImageInspect(context.Background(), id)
		return img.Labels, err == nil
	}
	// Record what's already pulled, without pulling, so updates show at once.
	for _, e := range c.entries() {
		if img, err := s.Eng.ImageInspect(ctx, e.Ref); err == nil && c.newest(e.Ref) == "" {
			c.setNewest(e.Ref, img.ID)
		}
	}
	for {
		var last time.Time
		for _, e := range c.entries() {
			if e.Checked != nil && e.Checked.After(last) {
				last = *e.Checked
			}
		}
		wait := time.Until(last.Add(checkInterval))
		if wait < time.Minute {
			wait = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := s.checkImages(ctx, func(string, float64) {}); err != nil {
			s.Log.Warn("checking images", "err", err)
		}
	}
}

// --- API ----------------------------------------------------------------------------------

type catalogImageJSON struct {
	Ref         string     `json:"ref"`
	Title       string     `json:"title"`
	Version     string     `json:"version"`
	Description string     `json:"description"`
	Desktops    []string   `json:"desktops"`
	Behind      []string   `json:"behind"`
	Checked     *time.Time `json:"checked"`
}

func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	list, _ := s.Desktops.List(r.Context())
	out := []catalogImageJSON{}
	for _, e := range s.images.entries() {
		j := catalogImageJSON{Ref: e.Ref, Checked: e.Checked, Desktops: []string{}, Behind: []string{}}
		if img, err := s.Eng.ImageInspect(r.Context(), e.Ref); err == nil {
			j.Title = img.Labels["org.opencontainers.image.title"]
			j.Version = img.Labels["org.opencontainers.image.version"]
			j.Description = img.Labels["org.opencontainers.image.description"]
		}
		for _, c := range list {
			if c.Image == e.Ref {
				j.Desktops = append(j.Desktops, c.Name)
				if s.images.updateFor(c) != nil {
					j.Behind = append(j.Behind, c.Name)
				}
			}
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddImage(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !auth.Can(a, auth.ManageAccounts, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if err := readJSON(r, &req); err != nil || req.Ref == "" {
		writeError(w, http.StatusBadRequest, "an image reference is required")
		return
	}
	for _, e := range s.images.entries() {
		if e.Ref == req.Ref {
			writeError(w, http.StatusConflict, "already in the catalog")
			return
		}
	}
	id := s.jobs.start(a.Username, "", func(ctx context.Context, progress func(string, float64)) error {
		if err := s.Eng.Pull(ctx, req.Ref, nil); err != nil {
			return err
		}
		s.Desktops.ForgetImage(req.Ref)
		img, err := s.Desktops.EnsureImage(ctx, req.Ref, progress)
		if err != nil {
			return err
		}
		s.images.mu.Lock()
		now := time.Now().UTC()
		s.images.list = append(s.images.list, catalogEntry{Ref: req.Ref, Newest: img.ID, Checked: &now})
		err = s.images.save()
		s.images.mu.Unlock()
		return err
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

func (s *Server) handleRemoveImage(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !auth.Can(a, auth.ManageAccounts, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	ref := r.URL.Query().Get("ref")
	list, _ := s.Desktops.List(r.Context())
	for _, c := range list {
		if c.Image == ref {
			writeError(w, http.StatusConflict, c.Name+" uses this image")
			return
		}
	}
	s.images.mu.Lock()
	defer s.images.mu.Unlock()
	if len(s.images.list) == 1 && s.images.list[0].Ref == ref {
		writeError(w, http.StatusConflict, "the catalog needs at least one image")
		return
	}
	s.images.list = slices.DeleteFunc(s.images.list, func(e catalogEntry) bool { return e.Ref == ref })
	_ = s.images.save()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCheckImages(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !auth.Can(a, auth.ManageAccounts, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	id := s.jobs.start(a.Username, "", s.checkImages)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

func (s *Server) handleUpgradeImage(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !auth.Can(a, auth.Manage, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if err := readJSON(r, &req); err != nil || req.Ref == "" {
		writeError(w, http.StatusBadRequest, "an image reference is required")
		return
	}
	id := s.jobs.start(a.Username, "", func(ctx context.Context, progress func(string, float64)) error {
		list, err := s.Desktops.List(ctx)
		if err != nil {
			return err
		}
		var behind []desktops.Desktop
		for _, c := range list {
			if c.Image == req.Ref && s.images.updateFor(c) != nil {
				behind = append(behind, c)
			}
		}
		var errs []error
		for i, c := range behind {
			if err := s.Desktops.Begin(c.Name, "upgrading"); err != nil {
				errs = append(errs, err)
				continue
			}
			s.publishDesktop(ctx, c.Name)
			s.viewers.closeAll(c.Name)
			_, err := s.Desktops.Recreate(ctx, c.Name, c.Settings, func(msg string, _ float64) {
				progress(c.Name+": "+msg, float64(i)/float64(len(behind)))
			})
			s.Desktops.Done(c.Name)
			s.publishDesktop(ctx, c.Name)
			if err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
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
	Newest  string     `json:"newest"` // image ID of the latest pull or build
	Checked *time.Time `json:"checked"`
	// Custom images are built here from a recipe (deskyard-custom/<slug>:latest);
	// the update check never pulls them.
	Custom bool `json:"custom,omitempty"`
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
		if sameRef(c.list[i].Ref, ref) {
			c.list[i].Newest, c.list[i].Checked = id, &now
		}
	}
	_ = c.save()
}

// noteLocal records the image a reference resolves to here, without
// counting as a check. Locally built images (`:local`) never pull, so this is
// how a rebuild becomes their newest version.
func (c *catalog) noteLocal(ref, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.list {
		if sameRef(c.list[i].Ref, ref) && c.list[i].Newest != id {
			c.list[i].Newest = id
			_ = c.save()
		}
	}
}

func (c *catalog) newest(ref string) string {
	e, _ := c.entry(ref)
	return e.Newest
}

func (c *catalog) entry(ref string) (catalogEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.list {
		if sameRef(e.Ref, ref) {
			return e, true
		}
	}
	return catalogEntry{}, false
}

// put adds e, or replaces the entry with the same ref.
func (c *catalog) put(e catalogEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.list {
		if sameRef(c.list[i].Ref, e.Ref) {
			c.list[i] = e
			return c.save()
		}
	}
	c.list = append(c.list, e)
	return c.save()
}

// remove drops ref; the catalog keeps at least one image.
func (c *catalog) remove(ref string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.list) == 1 && sameRef(c.list[0].Ref, ref) {
		return errLastImage
	}
	c.list = slices.DeleteFunc(c.list, func(e catalogEntry) bool { return sameRef(e.Ref, ref) })
	return c.save()
}

var errLastImage = errors.New("the catalog needs at least one image")

// sameRef compares image references, reading a missing tag as ":latest"
// (desktops store the ref they were made from, with or without it).
func sameRef(a, b string) bool { return normRef(a) == normRef(b) }

func normRef(ref string) string {
	if strings.HasPrefix(ref, "sha256:") || strings.Contains(ref, "@") {
		return ref
	}
	if i := strings.LastIndex(ref, "/"); !strings.Contains(ref[i+1:], ":") {
		return ref + ":latest"
	}
	return ref
}

// updateFor returns the version a desktop could upgrade to, if any.
// Custom images' refs end in :latest, which every rebuild moves.
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

// check pulls every catalog image and records the newest IDs. Custom images
// are built here, so there is nothing to pull.
func (s *Server) checkImages(ctx context.Context, progress func(string, float64)) error {
	var errs []error
	entries := slices.DeleteFunc(s.images.entries(), func(e catalogEntry) bool { return e.Custom })
	for i, e := range entries {
		progress("Checking "+e.Ref, float64(i)/float64(len(entries)))
		if err := s.Eng.Pull(ctx, e.Ref, nil); err != nil {
			errs = append(errs, err)
			if img, ierr := s.Eng.ImageInspect(ctx, e.Ref); ierr == nil {
				s.images.noteLocal(e.Ref, img.ID) // a local build
			}
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
	// Record what each reference is here, without pulling, so updates show
	// at once and a rebuilt local image counts as the newest.
	for _, e := range c.entries() {
		if img, err := s.Eng.ImageInspect(ctx, e.Ref); err == nil {
			c.noteLocal(e.Ref, img.ID)
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
	Custom      bool       `json:"custom"`
	Secrets     bool       `json:"secrets"`
	BaseUpdated bool       `json:"baseUpdated"`
	Recipe      *string    `json:"recipe"`
}

func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	list, _ := s.Desktops.List(r.Context())
	out := []catalogImageJSON{}
	for _, e := range s.images.entries() {
		j := catalogImageJSON{Ref: e.Ref, Checked: e.Checked, Desktops: []string{}, Behind: []string{}, Custom: e.Custom}
		if img, err := s.Eng.ImageInspect(r.Context(), e.Ref); err == nil {
			j.Title = img.Labels["org.opencontainers.image.title"]
			j.Version = img.Labels["org.opencontainers.image.version"]
			j.Description = img.Labels["org.opencontainers.image.description"]
			j.Secrets = img.Labels[labelSecrets] == "true"
			if e.Custom {
				j.BaseUpdated = s.baseUpdated(r.Context(), img)
			}
		}
		if slug, ok := customSlug(e.Ref); ok && e.Custom {
			j.Recipe = &slug
		}
		for _, c := range list {
			if sameRef(c.Image, e.Ref) {
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
	if _, ok := customSlug(req.Ref); ok {
		writeError(w, http.StatusBadRequest, "custom images are made from a recipe")
		return
	}
	if _, ok := s.images.entry(req.Ref); ok {
		writeError(w, http.StatusConflict, "already in the catalog")
		return
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
		now := time.Now().UTC()
		return s.images.put(catalogEntry{Ref: req.Ref, Newest: img.ID, Checked: &now})
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
	if e, ok := s.images.entry(ref); ok && e.Custom {
		slug, _ := customSlug(e.Ref)
		s.removeCustom(w, r, slug)
		return
	}
	if user := s.imageUser(r.Context(), ref); user != "" {
		writeError(w, http.StatusConflict, user+" uses this image")
		return
	}
	if err := s.images.remove(ref); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.events.refresh()
	w.WriteHeader(http.StatusNoContent)
}

// imageUser names a desktop made from ref, or "" if there is none.
func (s *Server) imageUser(ctx context.Context, ref string) string {
	list, _ := s.Desktops.List(ctx)
	for _, c := range list {
		if sameRef(c.Image, ref) {
			return c.Name
		}
	}
	return ""
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
		Ref            string `json:"ref"`
		ConfirmSecrets bool   `json:"confirmSecrets"`
	}
	if err := readJSON(r, &req); err != nil || req.Ref == "" {
		writeError(w, http.StatusBadRequest, "an image reference is required")
		return
	}
	behindOn := func(ctx context.Context) ([]desktops.Desktop, error) {
		list, err := s.Desktops.List(ctx)
		if err != nil {
			return nil, err
		}
		var behind []desktops.Desktop
		for _, c := range list {
			if sameRef(c.Image, req.Ref) && s.images.updateFor(c) != nil {
				behind = append(behind, c)
			}
		}
		return behind, nil
	}
	if !req.ConfirmSecrets {
		behind, err := behindOn(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !s.secretsConfirmed(w, r, req.Ref, behind, false) {
			return
		}
	}
	id := s.jobs.start(a.Username, "", func(ctx context.Context, progress func(string, float64)) error {
		behind, err := behindOn(ctx)
		if err != nil {
			return err
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

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
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// catalog is the list of images desktops can be made from (images.json),
// with the newest pulled image ID per reference, for "update available".
type catalogEntry struct {
	Ref     string     `json:"ref"`
	Newest  string     `json:"newest"` // image ID of the latest pull or build
	Checked *time.Time `json:"checked"`
	// Legacy locally built images retain their update policy; the update
	// check never pulls them.
	Custom bool `json:"custom,omitempty"`
}

type catalog struct {
	mu   sync.Mutex
	path string
	list []catalogEntry
}

const checkInterval = 24 * time.Hour

func loadCatalog(dataDir, defaultRef string) (*catalog, error) {
	c := &catalog{path: filepath.Join(dataDir, "images.json")}
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
	return availableEntries(c.list)
}

func (c *catalog) defaultRef() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.list) == 0 {
		return ""
	}
	return c.list[0].Ref
}

func (c *catalog) publish(next []catalogEntry) error {
	if err := store.WriteJSON(c.path, map[string]any{"images": next}); err != nil {
		return err
	}
	c.list = next
	return nil
}

func (c *catalog) setNewest(ref, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := slices.Clone(c.list)
	now := time.Now().UTC()
	for i := range next {
		if sameRef(next[i].Ref, ref) {
			next[i].Newest, next[i].Checked = id, &now
			return c.publish(next)
		}
	}
	if image, ok := officialImageFor(ref); ok {
		return c.publish(append(next, catalogEntry{Ref: image.Ref, Newest: id, Checked: &now}))
	}
	return nil
}

// noteLocal records a local resolution without counting it as a registry check.
func (c *catalog) noteLocal(ref, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := slices.Clone(c.list)
	for i := range next {
		if sameRef(next[i].Ref, ref) {
			if next[i].Newest == id {
				return nil
			}
			next[i].Newest = id
			return c.publish(next)
		}
	}
	if image, ok := officialImageFor(ref); ok {
		return c.publish(append(next, catalogEntry{Ref: image.Ref, Newest: id}))
	}
	return nil
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
	if image, ok := officialImageFor(ref); ok {
		return catalogEntry{Ref: image.Ref}, true
	}
	return catalogEntry{}, false
}

// put adds e, or replaces the entry with the same ref.
func (c *catalog) put(e catalogEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := slices.Clone(c.list)
	for i := range next {
		if sameRef(next[i].Ref, e.Ref) {
			next[i] = e
			return c.publish(next)
		}
	}
	return c.publish(append(next, e))
}

// remove drops ref; the catalog keeps at least one image.
func (c *catalog) remove(ref string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.list) == 1 && sameRef(c.list[0].Ref, ref) {
		return errLastImage
	}
	return c.publish(slices.DeleteFunc(slices.Clone(c.list), func(e catalogEntry) bool { return sameRef(e.Ref, ref) }))
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
func (c *catalog) updateFor(comp environments.Environment) *string {
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

// check refreshes downloaded images. Available choices stay undownloaded;
// legacy locally built images are excluded from registry checks.
func (s *Server) checkImages(ctx context.Context, progress func(string, float64)) error {
	var errs []error
	entries := slices.DeleteFunc(s.images.entries(), func(e catalogEntry) bool { return e.Custom })
	for i, e := range entries {
		if _, err := s.Eng.ImageInspect(ctx, e.Ref); errors.Is(err, engine.ErrNotFound) {
			continue
		} else if err != nil {
			errs = append(errs, err)
			continue
		}
		progress("Checking "+e.Ref, float64(i)/float64(len(entries)))
		if err := s.Eng.Pull(ctx, e.Ref, nil); err != nil {
			errs = append(errs, err)
			if img, ierr := s.Eng.ImageInspect(ctx, e.Ref); ierr == nil {
				if err := s.images.noteLocal(e.Ref, img.ID); err != nil {
					errs = append(errs, err)
				} // a local build
			}
			continue
		}
		s.Environments.ForgetImage(e.Ref)
		img, err := s.Eng.ImageInspect(ctx, e.Ref)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.images.setNewest(e.Ref, img.ID); err != nil {
			errs = append(errs, err)
		}
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
			if err := c.noteLocal(e.Ref, img.ID); err != nil {
				s.Log.Warn("recording local image", "ref", e.Ref, "err", err)
			}
		}
	}
	var lastRun time.Time
	for {
		last := lastRun
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
		lastRun = time.Now().UTC()
	}
}

// --- API ----------------------------------------------------------------------------------

type catalogImageJSON struct {
	Installed    bool       `json:"installed"`
	Official     bool       `json:"official"`
	Ref          string     `json:"ref"`
	Title        string     `json:"title"`
	Version      string     `json:"version"`
	Description  string     `json:"description"`
	Environments []string   `json:"environments"`
	Behind       []string   `json:"behind"`
	Checked      *time.Time `json:"checked"`
}

func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	list, _ := s.Environments.List(r.Context())
	out := []catalogImageJSON{}
	for _, e := range s.images.entries() {
		j := catalogImageJSON{Ref: e.Ref, Checked: e.Checked, Environments: []string{}, Behind: []string{}}
		if image, ok := officialImageFor(e.Ref); ok {
			j.Official, j.Title, j.Description = true, image.Title, image.Description
		}
		if img, err := s.Eng.ImageInspect(r.Context(), e.Ref); err == nil {
			j.Installed = true
			if title := img.Labels["org.opencontainers.image.title"]; title != "" {
				j.Title = title
			}
			j.Version = img.Labels["org.opencontainers.image.version"]
			if desc := img.Labels["org.opencontainers.image.description"]; desc != "" {
				j.Description = desc
			}
		}
		for _, c := range list {
			if sameRef(c.Image, e.Ref) {
				j.Environments = append(j.Environments, c.Name)
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
	if _, ok := s.images.entry(req.Ref); ok {
		writeError(w, http.StatusConflict, "already in the catalog")
		return
	}
	id := s.pullCatalogImage(a.Username, req.Ref)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

func (s *Server) handleInstallImage(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if err := readJSON(r, &req); err != nil || req.Ref == "" {
		writeError(w, http.StatusBadRequest, "an image reference is required")
		return
	}
	entry, ok := s.images.entry(req.Ref)
	if !ok {
		writeError(w, http.StatusBadRequest, "choose a catalog image to install")
		return
	}
	id := s.pullCatalogImage(a.Username, entry.Ref)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

func (s *Server) pullCatalogImage(username, ref string) string {
	return s.jobs.start(username, "", func(ctx context.Context, progress func(string, float64)) error {
		s.Environments.ForgetImage(ref)
		img, err := s.Environments.EnsureImage(ctx, ref, progress)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		old, _ := s.images.entry(ref)
		local := old.Custom || img.Labels["deskyard.kind"] == "custom"
		return s.images.put(catalogEntry{Ref: ref, Newest: img.ID, Checked: &now, Custom: local})
	})
}

func (s *Server) handleRemoveImage(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !auth.Can(a, auth.ManageAccounts, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	ref := r.URL.Query().Get("ref")
	if _, official := officialImageFor(ref); official {
		writeError(w, http.StatusBadRequest, "official images remain available in the catalog")
		return
	}
	if user := s.imageUser(ref); user != "" {
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

// imageUser names an environment whose definition references ref or pins
// its newest image, with or without a container, or "" if there is none.
func (s *Server) imageUser(ref string) string {
	newest := s.images.newest(ref)
	for _, def := range s.Environments.Defs.List() {
		if sameRef(def.Image.Ref, ref) || (newest != "" && def.Image.ID == newest) {
			return def.Name
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
		Ref string `json:"ref"`
	}
	if err := readJSON(r, &req); err != nil || req.Ref == "" {
		writeError(w, http.StatusBadRequest, "an image reference is required")
		return
	}
	behindOn := func(ctx context.Context) ([]environments.Environment, error) {
		list, err := s.Environments.List(ctx)
		if err != nil {
			return nil, err
		}
		var behind []environments.Environment
		for _, c := range list {
			if sameRef(c.Image, req.Ref) && s.images.updateFor(c) != nil {
				behind = append(behind, c)
			}
		}
		return behind, nil
	}
	id := s.jobs.start(a.Username, "", func(ctx context.Context, progress func(string, float64)) error {
		behind, err := behindOn(ctx)
		if err != nil {
			return err
		}
		var errs []error
		for i, c := range behind {
			release, err := s.Environments.Reserve(c.Name, "upgrading")
			if err != nil {
				errs = append(errs, err)
				continue
			}
			s.publishEnvironment(ctx, c.Name)
			s.viewers.closeAll(c.Name)
			_, err = s.Environments.Recreate(ctx, c.Name, c.Settings, func(msg string, _ float64) {
				progress(c.Name+": "+msg, float64(i)/float64(len(behind)))
			})
			release()
			s.publishEnvironment(ctx, c.Name)
			if err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

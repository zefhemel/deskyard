package server

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/recipes"
)

// Custom images are built here from recipes, as deskyard-custom/<slug> tagged
// with the build time and :latest; the catalog refers to :latest.
const (
	customRepo   = "deskyard-custom/"
	labelKind    = "deskyard.kind"
	labelBase    = "deskyard.base"
	labelRecipe  = "deskyard.recipe"
	labelSecrets = "deskyard.secrets"
	labelTitle   = "org.opencontainers.image.title"
	labelVersion = "org.opencontainers.image.version"
	homeTemplate = "/opt/deskyard/home-template"
)

// buildsDir (in StateDir) holds the build contexts of running builds; it is
// emptied at startup.
const buildsDir = "builds"

// buildTimeout limits a whole build, checks included (tests shorten it).
var buildTimeout = 30 * time.Minute

// buildClock gives the build time (tests move it on).
var buildClock = time.Now

func customRef(slug string) string { return customRepo + slug + ":latest" }

// customSlug returns the recipe slug of a deskyard-custom/<slug>[:tag] ref.
func customSlug(ref string) (string, bool) {
	rest, ok := strings.CutPrefix(ref, customRepo)
	if !ok {
		return "", false
	}
	slug, _, _ := strings.Cut(rest, ":")
	return slug, slug != "" && !strings.Contains(slug, "/")
}

// builds allows one build (or removal) per recipe at a time.
type builds struct {
	mu   sync.Mutex
	busy map[string]bool
}

func (b *builds) claim(slug string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.busy[slug] {
		return false
	}
	if b.busy == nil {
		b.busy = map[string]bool{}
	}
	b.busy[slug] = true
	return true
}

func (b *builds) release(slug string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.busy, slug)
}

// --- API ----------------------------------------------------------------------------------

func (s *Server) handleListRecipes(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	list, err := s.recipes.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []recipes.Recipe{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateRecipe(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	var rec recipes.Recipe
	if err := readJSON(r, &rec); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	// Only images made from a desktop have a home template; whether an
	// image has secrets is never the client's to say.
	rec.HomeFrom = ""
	rec.Secrets = s.hasSecrets(r.Context(), rec.Base)
	if !s.checkRecipe(w, rec) {
		return
	}
	if _, err := s.recipes.Get(rec.Slug); err == nil {
		writeError(w, http.StatusConflict, "a recipe named "+rec.Slug+" already exists")
		return
	}
	s.startBuild(w, a, rec, true, buildOptions{})
}

func (s *Server) handleUpdateRecipe(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	slug := r.PathValue("slug")
	var rec recipes.Recipe
	if err := readJSON(r, &rec); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if rec.Slug == "" {
		rec.Slug = slug
	}
	if rec.Slug != slug {
		writeError(w, http.StatusBadRequest, "the slug can't change")
		return
	}
	old, err := s.recipes.Get(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such recipe")
		return
	}
	// The home template and its secrets stay as they were (the template is
	// copied out of the current image); a base with secrets adds them.
	rec.HomeFrom = old.HomeFrom
	rec.Secrets = old.Secrets || s.hasSecrets(r.Context(), rec.Base)
	if !s.checkRecipe(w, rec) {
		return
	}
	s.startBuild(w, a, rec, true, buildOptions{rebuild: true})
}

func (s *Server) handleRebuildRecipe(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	rec, err := s.recipes.Get(r.PathValue("slug"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such recipe")
		return
	}
	s.startBuild(w, a, rec, false, buildOptions{rebuild: true})
}

func (s *Server) handleDeleteRecipe(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	s.removeCustom(w, r, r.PathValue("slug"))
}

// checkRecipe validates rec and its base: a catalog image, and not one built
// on this recipe's own image.
func (s *Server) checkRecipe(w http.ResponseWriter, rec recipes.Recipe) bool {
	if err := rec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if _, ok := s.images.entry(rec.Base); !ok {
		writeError(w, http.StatusBadRequest, "base: "+rec.Base+" isn't in the image catalog")
		return false
	}
	for ref, seen := rec.Base, 0; seen < 64; seen++ {
		slug, ok := customSlug(ref)
		if !ok {
			break
		}
		if slug == rec.Slug {
			writeError(w, http.StatusBadRequest, "base: "+rec.Base+" is built on this image")
			return false
		}
		parent, err := s.recipes.Get(slug)
		if err != nil {
			break
		}
		ref = parent.Base
	}
	return true
}

// hasSecrets reports whether the image ref is marked as containing secrets.
func (s *Server) hasSecrets(ctx context.Context, ref string) bool {
	img, err := s.Eng.ImageInspect(ctx, ref)
	return err == nil && img.Labels[labelSecrets] == "true"
}

// startBuild runs a build job for rec (saving rec first when save is set)
// and answers 202 with the job, or 409 when the recipe is already building.
func (s *Server) startBuild(w http.ResponseWriter, a accounts.Account, rec recipes.Recipe, save bool, opts buildOptions) {
	if !s.builds.claim(rec.Slug) {
		writeError(w, http.StatusConflict, "already building")
		return
	}
	if save {
		if err := s.recipes.Put(rec); err != nil {
			s.builds.release(rec.Slug)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	id := s.jobs.startCancellable(a.Username, func(ctx context.Context, progress func(string, float64)) error {
		defer s.builds.release(rec.Slug)
		ctx, cancel := context.WithTimeout(ctx, buildTimeout)
		defer cancel()
		err := s.build(ctx, rec, opts, func(line string) { progress(line, -1) })
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("the build took longer than %s", buildTimeout)
		}
		if err != nil {
			s.Log.Warn("image build failed", "recipe", rec.Slug, "err", err)
			if save && opts.keepOnlyIfBuilt {
				if err := s.recipes.Delete(rec.Slug); err != nil && !errors.Is(err, recipes.ErrNotFound) {
					s.Log.Warn("removing the recipe of a failed first build", "recipe", rec.Slug, "err", err)
				}
				s.events.refresh()
			}
		} else {
			s.Log.Info("image built", "recipe", rec.Slug, "by", a.Username)
		}
		return err
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

// removeCustom deletes a custom image: its catalog entry, recipe and tags.
// It is refused while a desktop uses any of its tags, or while it builds.
func (s *Server) removeCustom(w http.ResponseWriter, r *http.Request, slug string) {
	ctx := r.Context()
	_, recErr := s.recipes.Get(slug)
	_, inCatalog := s.images.entry(customRef(slug))
	if errors.Is(recErr, recipes.ErrNotFound) && !inCatalog {
		writeError(w, http.StatusNotFound, "no such recipe")
		return
	}
	if !s.builds.claim(slug) {
		writeError(w, http.StatusConflict, "already building")
		return
	}
	defer s.builds.release(slug)
	repo := customRepo + slug
	tags, err := s.Eng.ImageTags(ctx, repo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ids := map[string]bool{}
	for _, id := range tags {
		ids[id] = true
	}
	list, _ := s.Desktops.List(ctx)
	for _, c := range list {
		if sl, ok := customSlug(c.Image); (ok && sl == slug) || ids[c.ImageID] {
			writeError(w, http.StatusConflict, c.Name+" uses this image")
			return
		}
	}
	if inCatalog {
		if err := s.images.remove(customRef(slug)); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	if err := s.recipes.Delete(slug); err != nil && !errors.Is(err, recipes.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for tag := range tags {
		if err := s.Eng.RemoveImage(ctx, tag); err != nil && !errors.Is(err, engine.ErrNotFound) {
			s.Log.Warn("removing image tag", "tag", tag, "err", err)
		}
	}
	s.Desktops.ForgetImage(customRef(slug))
	s.events.refresh()
	w.WriteHeader(http.StatusNoContent)
}

// --- the build ------------------------------------------------------------------------------

type buildOptions struct {
	// rebuild pulls a non-custom base first and builds without cache.
	rebuild bool
	// home, when set, writes the home template (entries under
	// "home-template/", the folder itself included) into the build context.
	// Without it, a recipe with HomeFrom "image" takes the template from its
	// current image.
	home func(ctx context.Context, tw *tar.Writer) error
	// after, when set, runs once the image is built, tagged and in the catalog.
	after func(log func(string))
	// keepOnlyIfBuilt removes the recipe startBuild saved when this build
	// fails: it can't be built again without its first image (an image from a
	// desktop takes its home template from it), and it would block a retry.
	keepOnlyIfBuilt bool
}

var imageUserRE = regexp.MustCompile(`^[A-Za-z0-9._-]+(:[A-Za-z0-9._-]+)?$`)

// build makes the image of rec, checks it, tags it and puts it in the
// catalog. On failure it leaves no tag, no catalog entry and no image.
func (s *Server) build(ctx context.Context, rec recipes.Recipe, opts buildOptions, log func(string)) error {
	if err := rec.Validate(); err != nil {
		return err
	}
	repo := customRepo + rec.Slug

	// 1-2. The base: pulled again on a rebuild (unless it's built here), with
	// the right contract.
	baseEntry, ok := s.images.entry(rec.Base)
	if !ok {
		return fmt.Errorf("the base %s isn't in the image catalog", rec.Base)
	}
	if opts.rebuild && !baseEntry.Custom {
		log("Pulling " + rec.Base)
		if err := s.Eng.Pull(ctx, rec.Base, nil); err != nil {
			// An image built on this machine can't be pulled; one that is
			// here still serves.
			if _, ierr := s.Eng.ImageInspect(ctx, rec.Base); ierr != nil || ctx.Err() != nil {
				return fmt.Errorf("pulling %s: %w", rec.Base, err)
			}
			log("Couldn't pull " + rec.Base + " (" + err.Error() + "); using the image that is here")
		} else {
			s.Desktops.ForgetImage(rec.Base)
			if img, err := s.Eng.ImageInspect(ctx, rec.Base); err == nil {
				if err := s.images.setNewest(rec.Base, img.ID); err != nil {
					return fmt.Errorf("recording base image: %w", err)
				}
			}
		}
	}
	base, err := s.Desktops.EnsureImage(ctx, rec.Base, func(msg string, _ float64) { log(msg) })
	if err != nil {
		return err
	}
	log("Base " + rec.Base + " is " + base.ID)

	// 3. The build context: the Dockerfile, and the home template if any.
	dockerfile := recipes.Dockerfile(rec, base.ID)
	if dockerfile == "" {
		return errors.New("the recipe can't be turned into a Dockerfile")
	}
	// The generated steps run as root; give the image its base's user back.
	if base.User != "" {
		if !imageUserRE.MatchString(base.User) {
			return fmt.Errorf("the base's user %q isn't a plain user name", base.User)
		}
		dockerfile += "USER " + base.User + "\n"
	}
	buildCtx, err := s.buildContext(ctx, rec, dockerfile, opts)
	if err != nil {
		return err
	}
	defer func() {
		buildCtx.Close()
		_ = os.Remove(buildCtx.Name())
	}()
	now := buildClock()
	recJSON, _ := json.Marshal(rec)
	labels := map[string]string{
		labelKind:    "custom",
		labelTitle:   rec.Name,
		labelVersion: now.Format("2006-01-02 15:04"),
		labelBase:    base.ID,
		labelRecipe:  string(recJSON),
	}
	if rec.Secrets || base.Labels[labelSecrets] == "true" {
		labels[labelSecrets] = "true"
	}

	// 4. Build.
	log("Building")
	id, err := s.Eng.Build(ctx, engine.BuildSpec{
		Context: buildCtx, Dockerfile: "Dockerfile", Labels: labels, NoCache: opts.rebuild,
	}, log)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("the build failed: %w", err)
	}
	ok = false
	defer func() {
		if !ok {
			if err := s.Eng.RemoveImage(context.WithoutCancel(ctx), id); err != nil && !errors.Is(err, engine.ErrNotFound) {
				s.Log.Warn("removing a failed build", "image", id, "err", err)
			}
		}
	}()
	img, err := s.Eng.ImageInspect(ctx, id)
	if err != nil {
		return err
	}
	if img.Labels[desktops.LabelContract] != desktops.Contract {
		return fmt.Errorf("the image lost the Deskyard contract (label %s=%s)", desktops.LabelContract, desktops.Contract)
	}

	// 5-6. Checks.
	log("Checking that the image adds no way to root")
	if err := s.rootScan(ctx, base.ID, id); err != nil {
		return err
	}
	log("Starting a test desktop")
	if err := s.smokeTest(ctx, id); err != nil {
		return fmt.Errorf("the test desktop didn't start: %w", err)
	}

	// 7. Tag.
	stamp, latest := repo+":"+now.Format("20060102-150405"), repo+":latest"
	// The current :latest, to put back if this build can't finish. If it
	// can't be read, stop before touching any tag.
	prev, prevErr := s.Eng.ImageInspect(context.WithoutCancel(ctx), latest)
	hadPrev := prevErr == nil
	if !hadPrev && !errors.Is(prevErr, engine.ErrNotFound) {
		return fmt.Errorf("reading %s: %w", latest, prevErr)
	}
	// untag puts the tags back as they were (the image itself goes in the
	// deferred clean-up).
	untag := func() {
		bg := context.WithoutCancel(ctx)
		if hadPrev {
			_ = s.Eng.Tag(bg, prev.ID, latest)
		} else {
			_ = s.Eng.RemoveImage(bg, latest)
		}
		_ = s.Eng.RemoveImage(bg, stamp)
	}
	for _, ref := range []string{stamp, latest} {
		if err := s.Eng.Tag(ctx, id, ref); err != nil {
			untag()
			return err
		}
	}
	log("Tagged " + stamp)

	// 8. Catalog. Custom images built on this one now show "base updated"
	// (computed from their deskyard.base label).
	s.Desktops.ForgetImage(customRef(rec.Slug))
	checked := time.Now().UTC()
	if err := s.images.put(catalogEntry{Ref: customRef(rec.Slug), Newest: id, Checked: &checked, Custom: true}); err != nil {
		untag()
		return err
	}
	ok = true
	s.events.refresh()

	// 9. Older builds that no container uses.
	s.pruneBuilds(context.WithoutCancel(ctx), repo, id)
	if opts.after != nil {
		opts.after(log)
	}
	return nil
}

// buildContext writes the tar archive the build runs from to a temporary
// file (a home template can be large), positioned at its start. The caller
// closes and removes it.
func (s *Server) buildContext(ctx context.Context, rec recipes.Recipe, dockerfile string, opts buildOptions) (f *os.File, err error) {
	dir := filepath.Join(s.StateDir, buildsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err = os.CreateTemp(dir, "context-*.tar")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	bw := bufio.NewWriterSize(f, 1<<20)
	tw := tar.NewWriter(bw)
	if err := tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(dockerfile)), ModTime: time.Unix(0, 0)}); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(tw, dockerfile); err != nil {
		return nil, err
	}
	for name, data := range recipes.ContextFiles(rec) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: time.Unix(0, 0)}); err != nil {
			return nil, err
		}
		if _, err := io.WriteString(tw, data); err != nil {
			return nil, err
		}
	}
	if rec.HomeFrom == recipes.HomeFromImage {
		home := opts.home
		if home == nil {
			home = func(ctx context.Context, tw *tar.Writer) error { return s.homeFromImage(ctx, customRef(rec.Slug), tw) }
		}
		if err := home(ctx, tw); err != nil {
			return nil, fmt.Errorf("the home template: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return f, nil
}

// homeFromImage copies ref's home template into the build context, as
// home-template/.
func (s *Server) homeFromImage(ctx context.Context, ref string, tw *tar.Writer) error {
	rc, err := s.Eng.CopyFromImage(ctx, ref, homeTemplate)
	if err != nil {
		return fmt.Errorf("reading it from %s: %w", ref, err)
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Name != "home-template" && !strings.HasPrefix(h.Name, "home-template/") {
			continue
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
}

// pruneBuilds removes the timestamp tags of repo other than current's that
// no container (desktop or not) uses.
func (s *Server) pruneBuilds(ctx context.Context, repo, current string) {
	tags, err := s.Eng.ImageTags(ctx, repo)
	if err != nil {
		return
	}
	cs, err := s.Eng.List(ctx, nil)
	if err != nil {
		return
	}
	used := map[string]bool{current: true}
	for _, c := range cs {
		used[c.ImageID] = true
	}
	for tag, id := range tags {
		if used[id] || strings.HasSuffix(tag, ":latest") {
			continue
		}
		if err := s.Eng.RemoveImage(ctx, tag); err != nil && !errors.Is(err, engine.ErrNotFound) {
			s.Log.Warn("removing an old build", "tag", tag, "err", err)
		}
	}
}

// baseUpdated reports whether a custom image's base has a newer image in the
// catalog than the one it was built on. Rebuilding an image so marks the
// custom images built on it, down a chain.
func (s *Server) baseUpdated(ctx context.Context, img engine.Image) bool {
	var rec recipes.Recipe
	if json.Unmarshal([]byte(img.Labels[labelRecipe]), &rec) != nil || rec.Base == "" || img.Labels[labelBase] == "" {
		return false
	}
	newest := s.images.newest(rec.Base)
	if newest == "" {
		b, err := s.Eng.ImageInspect(ctx, rec.Base)
		if err != nil {
			return false
		}
		newest = b.ID
	}
	return newest != img.Labels[labelBase]
}

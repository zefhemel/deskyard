package environments

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// replacement is the transition state of a container replacement, saved
// before the old container is touched: the revision being applied, and the
// name the old container is parked under until its replacement is up. The
// definition, not the journal, holds the settings.
type replacement struct {
	Version    int    `json:"version"`
	Name       string `json:"name"`
	Revision   uint64 `json:"revision"`
	Parked     string `json:"parked"`
	WasRunning bool   `json:"wasRunning"`
}

// parkedName is where name's old container waits while its replacement comes
// up. Environment names have no '.', so it is never another's container name.
func parkedName(name string) string { return containerName(name) + ".parked" }

func (s *Service) recoveryDir() string { return filepath.Join(s.Layout.State, "recovery") }
func (s *Service) recoveryPath(name string) string {
	return filepath.Join(s.recoveryDir(), name+".json")
}

// Sync directory entries as well as file contents: an intent must outlive the
// container it protects, and a committed removal must not resurrect on reboot.
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Service) saveReplacement(rec replacement) error {
	if err := store.WriteJSON(s.recoveryPath(rec.Name), rec); err != nil {
		return err
	}
	for _, dir := range []string{s.recoveryDir(), s.Layout.State, s.Layout.Data} {
		if err := syncDirectory(dir); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) clearReplacement(name string) error {
	if err := os.Remove(s.recoveryPath(name)); err != nil {
		return err
	}
	return syncDirectory(s.recoveryDir())
}

// Reserve owns the matching Done call, including early returns. Callers may
// retain Begin/Done while migrating, but new orchestration should use a release.
func (s *Service) Reserve(name, state string) (func(), error) {
	if err := s.Begin(name, state); err != nil {
		return nil, err
	}
	release := sync.OnceFunc(func() { s.Done(name) })
	if _, err := os.Stat(s.recoveryPath(name)); err == nil {
		release()
		return nil, fmt.Errorf("%w: %s has pending replacement recovery; restart the supervisor to retry", ErrBusy, name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		release()
		return nil, err
	}
	return release, nil
}

// Recreate is Apply of name's definition changed to set.
func (s *Service) Recreate(ctx context.Context, name string, set Settings, progress func(string, float64)) (Environment, error) {
	def, ok := s.Defs.Get(name)
	if !ok {
		return Environment{}, ErrNotFound
	}
	if err := s.apply(ctx, name, set, withSettings(def, set, ""), progress); err != nil {
		return Environment{}, err
	}
	return s.Get(ctx, name)
}

// Apply validates and preflights next's container-affecting settings (image,
// limits, network, mounts, volume grants, Docker access, camera), saves them
// as name's definition, a new revision when they differ, and replaces its
// container (if it has one) to match. The old container is parked until the new one is
// up; if that fails, the old one comes back and the definition stays ahead of
// it, with the error, until a start or another change applies it. The caller
// reserves its operation slot.
func (s *Service) Apply(ctx context.Context, name string, next Definition, progress func(string, float64)) error {
	return s.apply(ctx, name, next.Settings(), next, progress)
}

// apply is Apply; set is next as settings. They come only from the
// definition: an isolated network carries no LAN exceptions.
func (s *Service) apply(ctx context.Context, name string, set Settings, next Definition, progress func(string, float64)) error {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return err
		}
		defer unlock()
	}
	if err := s.blocked(name); err != nil {
		return err
	}
	if s.Networking != nil && (set.VPNConnectionID != "" || s.Networking.selected(name)) {
		_, err := s.Networking.recreate(ctx, name, set, progress)
		return err
	}
	if set.VPNConnectionID != "" {
		return errVPNUnavailable
	}
	return s.applyDirect(ctx, name, next, progress)
}

func (s *Service) applyDirect(ctx context.Context, name string, next Definition, progress func(string, float64)) error {
	c, err := s.Get(ctx, name)
	if err != nil {
		return err
	}
	if c.Conflict != "" {
		return fmt.Errorf("%w: %s", ErrConflict, c.Conflict)
	}
	if len(next.Mounts) > 0 {
		progress("Checking host folders", -1)
	}
	if err := s.checkHostSources(ctx, next.Mounts, next.DockerAccess); err != nil {
		return err
	}
	// A container can't be replaced without its folders, so that is refused
	// before anything is saved; without one the change is only saved, and
	// the start that builds it is refused instead.
	if c.Container.ID != "" {
		if err := s.missingFolder(name, next.Volumes); err != nil {
			return err
		}
	}
	if next.Camera != "" && next.Camera != c.Settings.Camera {
		if err := s.CheckCamera(ctx, name, next.Camera); err != nil {
			return err
		}
	}
	img, err := s.EnsureImage(ctx, next.Image.Ref, progress)
	if err != nil {
		return err
	}
	if next.Camera != "" && next.Camera == c.Settings.Camera && s.cameraGone(next.Camera) {
		s.Log.Warn("camera gone from the host; recreating the environment without it", "environment", name, "camera", next.Camera)
		next.Camera = ""
	}
	// A replacement still being recovered must finish first.
	if err := s.noPendingReplacement(name); err != nil {
		return err
	}
	def, err := s.Defs.Update(name, func(d *Definition) error {
		prev := *d
		d.Image = ImageRef{Ref: next.Image.Ref, ID: img.ID}
		d.Limits, d.Network = next.Limits, next.Network
		d.Mounts, d.Volumes = next.Mounts, next.Volumes
		d.DockerAccess, d.Camera = next.DockerAccess, next.Camera
		*d = d.clone()
		if ContainerAffecting(prev, *d) {
			d.Revision = prev.Revision + 1
		}
		return nil
	})
	if err != nil {
		return err
	}
	// A folder error of the old grants goes with them; one of the new
	// grants (only without a container) shows at once.
	_ = s.checkFolders(name)
	if c.Container.ID == "" {
		// Built from the definition when started, or by reconcile once
		// the error of an earlier build is cleared.
		return s.applied(name, nil)
	}
	if c.AppliedRevision == def.Revision {
		return s.applied(name, nil) // nothing left to apply
	}
	return s.applied(name, s.replace(ctx, c, def, progress))
}

// applied records the outcome of applying name's definition: the error is
// shown on the environment until a change succeeds.
func (s *Service) applied(name string, err error) error {
	if err != nil {
		s.setError(name, err.Error())
	} else {
		s.setError(name, "")
	}
	return err
}

// revertSettings takes back a change to name's settings, to those of prev.
// Fields the change didn't touch (memory reclaim, power) keep their current
// values: they may have been saved meanwhile.
func (s *Service) revertSettings(name string, prev Definition) error {
	_, err := s.Defs.Update(name, func(d *Definition) error {
		d.Image, d.Limits, d.Network = prev.Image, prev.Limits, prev.Network
		d.Mounts, d.Volumes = prev.Mounts, prev.Volumes
		d.DockerAccess, d.Camera = prev.DockerAccess, prev.Camera
		d.Revision = prev.Revision
		*d = d.clone()
		return nil
	})
	return err
}

// applyDefinition replaces c's container, built from an older revision, with
// one built from its current definition. A camera gone from the host is
// dropped from the definition first, and a pinned image gone from the engine
// pulled again by its reference, each as a new revision.
func (s *Service) applyDefinition(ctx context.Context, c Environment, progress func(string, float64)) error {
	if err := s.checkFolders(c.Name); err != nil {
		return err
	}
	if err := s.checkHostSources(ctx, c.Settings.Mounts, c.Settings.DockerAccess); err != nil {
		return s.applied(c.Name, err)
	}
	if c.Settings.Camera != "" && s.cameraGone(c.Settings.Camera) {
		s.Log.Warn("camera gone from the host; recreating the environment without it", "environment", c.Name, "camera", c.Settings.Camera)
		s.dropCamera(c.Name, c.Settings.Camera)
	}
	def, err := s.repin(ctx, c.Name)
	if err != nil {
		return s.applied(c.Name, err)
	}
	return s.applied(c.Name, s.replace(ctx, c, def, progress))
}

func (s *Service) noPendingReplacement(name string) error {
	if _, err := os.Stat(s.recoveryPath(name)); err == nil {
		return fmt.Errorf("%s has pending replacement recovery; restart the supervisor to retry it", name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// replace replaces c's container with one built from def. The journal is
// saved before the old container is touched; the old one is stopped and
// parked, not removed, until the new one is up (started and ready if the old
// one was running). On failure the new one is removed and the old one comes
// back. Recovery retained after a failed restore finishes at the next startup.
func (s *Service) replace(ctx context.Context, c Environment, def Definition, progress func(string, float64)) error {
	name := c.Name
	if err := s.noPendingReplacement(name); err != nil {
		return err
	}
	rec := replacement{Version: 1, Name: name, Revision: def.Revision, Parked: parkedName(name), WasRunning: c.Container.Running()}
	if _, err := s.Eng.Inspect(ctx, rec.Parked); err == nil {
		return fmt.Errorf("%w: a container named %s is in the way", ErrConflict, rec.Parked)
	} else if !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	if err := s.saveReplacement(rec); err != nil {
		return fmt.Errorf("saving replacement recovery: %w", err)
	}
	fail := func(cause error) error {
		recovery, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer done()
		if err := s.restoreReplacement(recovery, rec); err != nil {
			if s.noPendingReplacement(name) != nil {
				return errors.Join(cause, fmt.Errorf("restoring %s (intent retained at %s): %w", name, s.recoveryPath(name), err))
			}
			return errors.Join(cause, fmt.Errorf("restoring %s: %w", name, err))
		}
		return cause
	}
	progress("Stopping", -1)
	if rec.WasRunning {
		if err := s.Eng.Stop(ctx, c.Container.ID, stopTimeout); err != nil {
			return fail(err)
		}
	}
	if err := s.Eng.Rename(ctx, c.Container.ID, rec.Parked); err != nil {
		return fail(err)
	}
	progress("Recreating", -1)
	if err := s.realise(ctx, def, ""); err != nil {
		return fail(err)
	}
	if rec.WasRunning {
		progress("Starting", -1)
		if err := s.Eng.Start(ctx, containerName(name)); err != nil {
			return fail(err)
		}
		if err := s.WaitReady(ctx, name, 2*time.Minute); err != nil {
			return fail(err)
		}
	}
	if err := s.commitReplacement(ctx, rec); err != nil {
		return fail(fmt.Errorf("committing replacement: %w", err))
	}
	return nil
}

// commitReplacement removes the parked container of a replacement that is up,
// and its journal.
func (s *Service) commitReplacement(ctx context.Context, rec replacement) error {
	parked, err := s.Eng.Inspect(ctx, rec.Parked)
	if err == nil {
		if !s.owns(parked.Labels, rec.Name) {
			return fmt.Errorf("%w: the parked container %s isn't this environment's", ErrConflict, rec.Parked)
		}
		if err := s.Eng.Remove(ctx, parked.ID); err != nil && !errors.Is(err, engine.ErrNotFound) {
			return err
		}
	} else if !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	return s.clearReplacement(rec.Name)
}

// restoreReplacement is idempotent: it removes a replacement container, if
// any, and renames the parked one back. Once the old container is back under
// its name the journal is cleared; it is then started if it was running. With
// nothing parked (interrupted before parking) the container under the name is
// the old one, and is left where it is.
func (s *Service) restoreReplacement(ctx context.Context, rec replacement) error {
	if err := s.saveReplacement(rec); err != nil {
		return fmt.Errorf("retaining recovery intent: %w", err)
	}
	parked, err := s.Eng.Inspect(ctx, rec.Parked)
	if err != nil && !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	isParked := err == nil
	if isParked && !s.owns(parked.Labels, rec.Name) {
		return fmt.Errorf("%w: the parked container %s isn't this environment's", ErrConflict, rec.Parked)
	}
	current, err := s.Eng.Inspect(ctx, containerName(rec.Name))
	if err != nil && !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	if err == nil && !s.owns(current.Labels, rec.Name) {
		return errors.New("recovery name belongs to another environment")
	}
	hasCurrent := err == nil
	if isParked {
		if hasCurrent {
			if err := s.Eng.Remove(ctx, current.ID); err != nil && !errors.Is(err, engine.ErrNotFound) {
				return err
			}
		}
		if err := s.Eng.Rename(ctx, parked.ID, containerName(rec.Name)); err != nil {
			return err
		}
	} else if !hasCurrent {
		s.Log.Warn("an interrupted replacement left no container; it is built from the definition when started", "environment", rec.Name)
	}
	if err := s.clearReplacement(rec.Name); err != nil {
		return err
	}
	if !rec.WasRunning || (!isParked && !hasCurrent) {
		return nil
	}
	if !isParked && current.Running() {
		return nil
	}
	if err := s.Eng.Start(ctx, containerName(rec.Name)); err != nil {
		return fmt.Errorf("starting the restored environment: %w", err)
	}
	if err := s.WaitReady(ctx, rec.Name, 2*time.Minute); err != nil {
		return fmt.Errorf("restored environment not ready: %w", err)
	}
	return nil
}

// recoverReplacement finishes a replacement interrupted after its new
// container was up, and otherwise restores the parked one. Under a definition
// that now selects a VPN the parked container is discarded instead.
func (s *Service) recoverReplacement(ctx context.Context, rec replacement) error {
	if def, ok := s.Defs.Get(rec.Name); ok && def.Network.Kind == NetworkVPN {
		return s.discardReplacement(ctx, rec)
	}
	current, err := s.Eng.Inspect(ctx, containerName(rec.Name))
	if err == nil && s.owns(current.Labels, rec.Name) && current.Labels[LabelRevision] == strconv.FormatUint(rec.Revision, 10) {
		if _, perr := s.Eng.Inspect(ctx, rec.Parked); perr == nil {
			up := current.Running() && s.WaitReady(ctx, rec.Name, time.Minute) == nil
			if up || (!rec.WasRunning && !current.Running()) {
				return s.commitReplacement(ctx, rec)
			}
		}
	}
	if err := s.restoreReplacement(ctx, rec); err != nil {
		return err
	}
	if def, ok := s.Defs.Get(rec.Name); ok && def.Revision >= rec.Revision {
		s.setError(rec.Name, errRolledBack)
	}
	return nil
}

// errRolledBack is the error of an environment whose change was interrupted
// and rolled back at startup: its definition stays ahead of its container.
const errRolledBack = "An interrupted change was rolled back at startup; start, restart or edit the environment to apply it"

// discardReplacement removes the parked container and the journal of a
// replacement whose definition has since moved to a VPN: the old container
// never comes back on direct networking. A direct container under the name is
// stopped; the networking coordinator applies the definition.
func (s *Service) discardReplacement(ctx context.Context, rec replacement) error {
	parked, err := s.Eng.Inspect(ctx, rec.Parked)
	if err == nil {
		if !s.owns(parked.Labels, rec.Name) {
			return fmt.Errorf("%w: the parked container %s isn't this environment's", ErrConflict, rec.Parked)
		}
		if err := s.Eng.Remove(ctx, parked.ID); err != nil && !errors.Is(err, engine.ErrNotFound) {
			return err
		}
	} else if !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	current, err := s.Eng.Inspect(ctx, containerName(rec.Name))
	if err == nil && s.owns(current.Labels, rec.Name) && current.NetworkContainer == "" {
		if err := s.Eng.UpdateRestartPolicy(ctx, current.ID, "no"); err != nil {
			return err
		}
		if current.Running() {
			if err := s.Eng.Stop(ctx, current.ID, stopTimeout); err != nil {
				return err
			}
		}
	} else if err != nil && !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	s.Log.Warn("discarded an interrupted replacement: the definition now selects a VPN", "environment", rec.Name)
	return s.clearReplacement(rec.Name)
}

func (s *Service) recoverReplacements(ctx context.Context) error {
	entries, err := os.ReadDir(s.recoveryDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		var rec replacement
		if err := store.ReadJSON(filepath.Join(s.recoveryDir(), entry.Name()), &rec); err != nil {
			errs = append(errs, fmt.Errorf("reading replacement recovery %s: %w", entry.Name(), err))
			continue
		}
		if rec.Version != 1 || rec.Name != name || ValidName(name) != nil || rec.Revision == 0 || rec.Parked != parkedName(name) {
			errs = append(errs, fmt.Errorf("invalid replacement recovery %s", entry.Name()))
			continue
		}
		err := s.Begin(name, "upgrading")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		recovery, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err = s.recoverReplacement(recovery, rec)
		cancel()
		s.Done(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("recovering interrupted replacement %s: %w", name, err))
		} else {
			s.Log.Info("recovered interrupted environment replacement", "environment", name)
		}
	}
	return errors.Join(errs...)
}

// dropCamera clears camera, gone from the host, from name's definition, as a
// new revision.
func (s *Service) dropCamera(name, camera string) {
	_, err := s.Defs.Update(name, func(d *Definition) error {
		if d.Camera == camera {
			d.Camera = ""
			d.Revision++
		}
		return nil
	})
	if err != nil {
		s.Log.Warn("dropping a gone camera from the definition", "environment", name, "err", err)
	}
}

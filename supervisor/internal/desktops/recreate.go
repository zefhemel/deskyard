package desktops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// replacement is recovery intent, persisted before the old container is touched.
// The previous immutable image is separate from its logical update reference.
type replacement struct {
	Version     int       `json:"version"`
	Name        string    `json:"name"`
	ID          string    `json:"id"`
	ContainerID string    `json:"containerID"`
	ImageID     string    `json:"imageID"`
	Token       string    `json:"token"`
	Created     time.Time `json:"created"`
	WasRunning  bool      `json:"wasRunning"`
	Settings    Settings  `json:"settings"`
}

func (s *Service) recoveryDir() string { return filepath.Join(s.Cfg.DataDir, ".deskyard", "recovery") }
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
	for _, dir := range []string{s.recoveryDir(), filepath.Dir(s.recoveryDir()), s.Cfg.DataDir} {
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

// Recreate replaces a desktop while retaining a private, durable rollback record.
// The caller reserves its operation slot. Startup restores any unfinished change.
func (s *Service) Recreate(ctx context.Context, name string, set Settings, progress func(string, float64)) (Desktop, error) {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return Desktop{}, err
		}
		defer unlock()
	}
	if s.Networking != nil && (set.VPNConnectionID != "" || s.Networking.selected(name)) {
		return s.Networking.recreate(ctx, name, set, progress)
	}
	if set.VPNConnectionID != "" {
		return Desktop{}, errors.New("VPN networking unavailable")
	}
	return s.recreateDirect(ctx, name, set, progress)
}

func (s *Service) recreateDirect(ctx context.Context, name string, set Settings, progress func(string, float64)) (Desktop, error) {
	c, err := s.Get(ctx, name)
	if err != nil {
		return Desktop{}, err
	}
	if _, err := hostMountsFromLabels(c.Container.Labels); err != nil {
		return Desktop{}, err
	}
	if len(set.Mounts) > 0 {
		progress("Checking host folders", -1)
	}
	if err := s.checkHostSources(ctx, set.Mounts, set.DockerAccess); err != nil {
		return Desktop{}, err
	}
	img, err := s.EnsureImage(ctx, set.Image, progress)
	if err != nil {
		return Desktop{}, err
	}
	if set.Camera != "" && set.Camera == c.Settings.Camera && s.cameraGone(set.Camera) {
		s.Log.Warn("camera gone from the host; recreating the desktop without it", "desktop", name, "camera", set.Camera)
		set.Camera = ""
	}
	// A previous failed recovery must not be overwritten with incomplete state.
	if _, err := os.Stat(s.recoveryPath(name)); err == nil {
		return Desktop{}, fmt.Errorf("%s has pending replacement recovery; restart the supervisor to retry it", name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Desktop{}, err
	}
	rec := replacement{Version: 1, Name: name, ID: c.ID, ContainerID: c.Container.ID, ImageID: c.ImageID, Token: c.Container.Env[TokenEnv], Created: c.Created, WasRunning: c.Container.Running(), Settings: c.Settings}
	if err := s.saveReplacement(rec); err != nil {
		return Desktop{}, fmt.Errorf("saving replacement recovery: %w", err)
	}
	fail := func(cause error) (Desktop, error) {
		recovery, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer done()
		if err := s.restoreReplacement(recovery, rec); err != nil {
			return Desktop{}, errors.Join(cause, fmt.Errorf("recovering %s (intent retained at %s): %w", name, s.recoveryPath(name), err))
		}
		return Desktop{}, cause
	}
	progress("Stopping", -1)
	if rec.WasRunning {
		if err := s.Eng.Stop(ctx, c.Container.ID, stopTimeout); err != nil {
			return fail(err)
		}
	}
	progress("Recreating", -1)
	if err := s.Eng.Remove(ctx, c.Container.ID); err != nil {
		return fail(err)
	}
	if err := s.createPinnedContainer(ctx, name, c.ID, rec.Token, c.Created, set, img.ID); err != nil {
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
	result, err := s.Get(ctx, name)
	if err != nil {
		return fail(err)
	}
	if err := s.clearReplacement(name); err != nil {
		return fail(fmt.Errorf("committing replacement: %w", err))
	}
	return result, nil
}

// restoreReplacement is idempotent. A retained original is restarted in place;
// a partially created replacement is removed before recreating the old image.
func (s *Service) restoreReplacement(ctx context.Context, rec replacement) error {
	if err := s.saveReplacement(rec); err != nil {
		return fmt.Errorf("retaining recovery intent: %w", err)
	}
	current, err := s.Eng.Inspect(ctx, containerName(rec.Name))
	if err != nil && !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	original := err == nil && current.ID == rec.ContainerID && current.ImageID == rec.ImageID
	if err == nil {
		if current.Labels[LabelManaged] != "true" || current.Labels[LabelID] != rec.ID {
			return errors.New("recovery name belongs to another desktop")
		}
		if !original {
			if err := s.Eng.Remove(ctx, current.ID); err != nil {
				return err
			}
		}
	}
	if !original {
		set := rec.Settings
		err := s.createPinnedContainer(ctx, rec.Name, rec.ID, rec.Token, rec.Created, set, rec.ImageID)
		if err != nil && set.Camera != "" {
			s.Log.Warn("recovering the desktop without its unavailable camera", "desktop", rec.Name, "camera", set.Camera, "err", err)
			set.Camera = ""
			err = s.createPinnedContainer(ctx, rec.Name, rec.ID, rec.Token, rec.Created, set, rec.ImageID)
		}
		if err != nil {
			return err
		}
	}
	if rec.WasRunning && (!original || !current.Running()) {
		if err := s.Eng.Start(ctx, containerName(rec.Name)); err != nil {
			return err
		}
	}
	if rec.WasRunning {
		if err := s.WaitReady(ctx, rec.Name, 2*time.Minute); err != nil {
			return fmt.Errorf("restored desktop not ready: %w", err)
		}
	}
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
		if rec.Version != 1 || rec.Name != name || ValidName(name) != nil || rec.ID == "" || rec.ImageID == "" || rec.Token == "" || rec.Settings.Image == "" {
			errs = append(errs, fmt.Errorf("invalid replacement recovery %s", entry.Name()))
			continue
		}
		if err := ValidateHostMounts(rec.Settings.Mounts); err != nil {
			errs = append(errs, fmt.Errorf("invalid replacement recovery %s: %w", name, err))
			continue
		}
		err := s.Begin(name, "upgrading")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		recovery, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err = s.restoreReplacement(recovery, rec)
		cancel()
		s.Done(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("recovering interrupted replacement %s: %w", name, err))
		} else {
			s.Log.Info("restored interrupted desktop replacement", "desktop", name)
		}
	}
	return errors.Join(errs...)
}

package environments

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// VolumeUser is an environment whose definition grants a volume.
type VolumeUser struct {
	Environment string
	ReadOnly    bool
}

// VolumeInUseError refuses to delete a volume that environments' definitions
// still grant, running or stopped. It is an ErrConflict.
type VolumeInUseError struct {
	Environments []string
}

func (e *VolumeInUseError) Error() string {
	return fmt.Sprintf("the volume is granted to %s; remove those grants first", strings.Join(e.Environments, ", "))
}

func (e *VolumeInUseError) Unwrap() error { return ErrConflict }

// CreateVolume adds the volume name. With a hostPath it is a host volume of
// that existing engine-host folder, which is checked through the engine and
// never created. Otherwise a folder volume/<name>/ that already exists, such
// as one kept when a volume of that name was deleted, is adopted untouched,
// or a new one is made for the desktop user.
func (s *Service) CreateVolume(ctx context.Context, name, hostPath string) (v Volume, adopted bool, err error) {
	if err := ValidName(name); err != nil {
		return Volume{}, false, fmt.Errorf("invalid volume name: %w", err)
	}
	if hostPath != "" {
		if err := ValidHostPath(hostPath); err != nil {
			return Volume{}, false, err
		}
	}
	s.volMu.Lock()
	defer s.volMu.Unlock()
	if _, ok := s.Vols.Get(name); ok {
		return Volume{}, false, fmt.Errorf("volume %q: %w", name, ErrExists)
	}
	if hostPath != "" {
		v = Volume{Name: name, Created: time.Now().UTC(), Kind: VolumeHost, HostPath: hostPath}
		if err := s.checkHostSources(ctx, []hostSource{{volume: name, path: hostPath}}, false); err != nil {
			return Volume{}, false, err
		}
		if err := s.Vols.Add(v); err != nil {
			return Volume{}, false, err
		}
		return v, false, nil
	}
	folder := s.Layout.VolumeOf(name)
	st, err := os.Stat(folder)
	switch {
	case err == nil && !st.IsDir():
		return Volume{}, false, fmt.Errorf("%w: %s is in the way and isn't a folder", ErrConflict, folder)
	case err == nil:
		adopted = true
	case errors.Is(err, fs.ErrNotExist):
		if err := s.makeVolumeFolder(ctx, name); err != nil {
			return Volume{}, false, err
		}
	default:
		return Volume{}, false, err
	}
	v = Volume{Name: name, Created: time.Now().UTC(), Kind: VolumeLocal}
	if err := s.Vols.Add(v); err != nil {
		// A failed directory sync still saves the volume; keep its folder then.
		if _, saved := s.Vols.Get(name); !adopted && !saved {
			_ = os.Remove(folder) // still empty: nothing could use it yet
		}
		return Volume{}, false, err
	}
	return v, adopted, nil
}

// DeleteVolume deletes the volume name, refusing with a VolumeInUseError while
// any definition grants it, and with an ErrConflict while a container of this
// supervisor still mounts it: a definition is saved before its container is
// replaced, and a failed replacement keeps the old container. Its folder is
// kept unless deleteFiles; an error deleting the files leaves the volume
// deleted. A host volume's folder is never deleted: deleteFiles is refused.
// While ItemBusy reports the volume busy (a backup or restore), it is refused
// with that error.
func (s *Service) DeleteVolume(ctx context.Context, name string, deleteFiles bool) error {
	s.volMu.Lock()
	defer s.volMu.Unlock()
	v, ok := s.Vols.Get(name)
	if !ok {
		return fmt.Errorf("volume %q: %w", name, ErrNotFound)
	}
	if deleteFiles && v.Kind == VolumeHost {
		return fmt.Errorf("%w: %s is a host volume; Deskyard never deletes its folder %s", ErrConflict, name, v.HostPath)
	}
	if s.ItemBusy != nil {
		if err := s.ItemBusy("volume", name); err != nil {
			return err
		}
	}
	// Under Defs' lock, so no grant of it can be saved in between.
	err := s.Defs.Hold(func(defs []Definition) error {
		if users := volumeUsers(defs, name); len(users) > 0 {
			envs := make([]string, len(users))
			for i, u := range users {
				envs[i] = u.Environment
			}
			return &VolumeInUseError{Environments: envs}
		}
		// A container with the mount is made only from a definition granting
		// it, so none can appear while Defs' lock is held.
		mounters, err := s.volumeMounters(ctx, v)
		if err != nil {
			return err
		}
		if len(mounters) > 0 {
			return fmt.Errorf("%w: %s is still mounted by the container of %s, whose settings change isn't applied yet; wait for it, or start or restart the environment to apply it",
				ErrConflict, name, strings.Join(mounters, ", "))
		}
		return s.Vols.Delete(name)
	})
	if err != nil || !deleteFiles {
		return err
	}
	return s.removeTree(ctx, s.Layout.Volume, name)
}

// volumeMounters returns the environments, sorted by name, with a container
// of this supervisor that mounts the volume v, parked ones included.
func (s *Service) volumeMounters(ctx context.Context, v Volume) ([]string, error) {
	cs, err := s.Eng.List(ctx, s.ownFilter())
	if err != nil {
		return nil, err
	}
	folders := []string{filepath.Clean(s.VolumeFolder(v))}
	if real, err := filepath.EvalSymlinks(folders[0]); err == nil && real != folders[0] {
		folders = append(folders, real)
	}
	var out []string
	for _, c := range cs {
		if slices.ContainsFunc(c.Mounts, func(m engine.Mount) bool {
			return slices.ContainsFunc(folders, func(f string) bool { return within(m.Source, f) })
		}) && !slices.Contains(out, c.Labels[LabelName]) {
			out = append(out, c.Labels[LabelName])
		}
	}
	sort.Strings(out)
	return out, nil
}

// VolumeFolder is the folder holding v's files: its host path, or
// volume/<name>/ in the data folder.
func (s *Service) VolumeFolder(v Volume) string {
	if v.Kind == VolumeHost {
		return v.HostPath
	}
	return s.Layout.VolumeOf(v.Name)
}

// VolumeUsers returns the environments whose definitions grant the volume
// name, sorted by name.
func (s *Service) VolumeUsers(name string) []VolumeUser {
	return volumeUsers(s.Defs.List(), name)
}

func volumeUsers(defs []Definition, name string) []VolumeUser {
	var out []VolumeUser
	for _, def := range defs {
		for _, g := range def.Volumes {
			if g.Name == name {
				out = append(out, VolumeUser{Environment: def.Name, ReadOnly: g.ReadOnly})
				break
			}
		}
	}
	return out
}

// checkGrants refuses grants of volumes that aren't in volumes.json. It runs
// with Defs' lock held, as Definitions.checkGrants.
func (s *Service) checkGrants(grants []VolumeGrant) error {
	for _, g := range grants {
		if _, ok := s.Vols.Get(g.Name); !ok {
			return fmt.Errorf("volume %q doesn't exist", g.Name)
		}
	}
	return nil
}

// makeVolumeFolder makes volume/<name>/, owned by the desktop user (UID and
// GID 1000) with mode 0755, through the root helper container: the
// supervisor may not be able to give a folder away itself.
func (s *Service) makeVolumeFolder(ctx context.Context, name string) error {
	if err := s.runHelper(ctx, "deskyard-volume-", s.Layout.Volume, "/volume-root",
		"install", "-d", "-o", "1000", "-g", "1000", "-m", "0755", "/volume-root/"+name); err != nil {
		return err
	}
	folder := s.Layout.VolumeOf(name)
	if st, err := os.Stat(folder); err != nil || !st.IsDir() {
		return fmt.Errorf("could not create %s", folder)
	}
	return nil
}

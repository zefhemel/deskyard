package environments

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// volumesVersion is the format version of volumes.json.
const volumesVersion = 1

// Volume kinds. A local volume is a plain folder in volume/<name>/; an
// entry without a kind is local. A host volume is an existing folder,
// HostPath, on the machine running the container engine, which the supervisor
// never creates, changes or deletes.
const (
	VolumeLocal = "local"
	VolumeHost  = "host"
)

// Volume is a named piece of shared storage, kept in volumes.json. Which
// environments may use it is recorded only in their definitions.
type Volume struct {
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	Kind     string    `json:"kind"`
	HostPath string    `json:"hostPath,omitempty"`
}

func (v Volume) validate() error {
	if err := ValidName(v.Name); err != nil {
		return err
	}
	if v.Created.IsZero() {
		return errors.New("no creation time")
	}
	switch v.Kind {
	case VolumeLocal:
		if v.HostPath != "" {
			return errors.New("a local volume has no host path")
		}
	case VolumeHost:
		return ValidHostPath(v.HostPath)
	default:
		return fmt.Errorf("unknown kind %q", v.Kind)
	}
	return nil
}

// ValidHostPath checks a host volume's folder: a clean absolute path.
func ValidHostPath(p string) error {
	if len(p) > 4096 || !filepath.IsAbs(p) || strings.ContainsRune(p, 0) || filepath.Clean(p) != p {
		return fmt.Errorf("a host volume needs a clean absolute path on the engine host, not %q", p)
	}
	return nil
}

type volumesFile struct {
	Version int      `json:"version"`
	Volumes []Volume `json:"volumes"`
}

// Volumes is the volumes.json store, the authority for which volumes exist.
// Like Definitions, every change is written atomically before it replaces the
// in-memory copy.
type Volumes struct {
	path string

	mu   sync.Mutex
	vols map[string]Volume // never mutated; replaced on every change
}

func volumesPath(l config.Layout) string { return filepath.Join(l.Data, "volumes.json") }

// LoadVolumes reads path; a missing file is an empty store. Any invalid entry
// fails the whole load rather than dropping a volume.
func LoadVolumes(path string) (*Volumes, error) {
	v := &Volumes{path: path, vols: map[string]Volume{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	var f volumesFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version != volumesVersion {
		return nil, fmt.Errorf("%s: unsupported version %d (want %d)", path, f.Version, volumesVersion)
	}
	for _, vol := range f.Volumes {
		if vol.Kind == "" {
			vol.Kind = VolumeLocal
		}
		if err := vol.validate(); err != nil {
			return nil, fmt.Errorf("%s: volume %q: %w", path, vol.Name, err)
		}
		if _, dup := v.vols[vol.Name]; dup {
			return nil, fmt.Errorf("%s: volume %q is defined twice", path, vol.Name)
		}
		v.vols[vol.Name] = vol
	}
	return v, nil
}

func (v *Volumes) Get(name string) (Volume, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	vol, ok := v.vols[name]
	return vol, ok
}

// List returns every volume, sorted by name.
func (v *Volumes) List() []Volume {
	v.mu.Lock()
	defer v.mu.Unlock()
	return sortedVolumes(v.vols)
}

// Add validates and saves vol, or returns ErrExists for a name in use.
func (v *Volumes) Add(vol Volume) error {
	if err := vol.validate(); err != nil {
		return fmt.Errorf("invalid volume: %w", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.vols[vol.Name]; ok {
		return fmt.Errorf("volume %q: %w", vol.Name, ErrExists)
	}
	next := make(map[string]Volume, len(v.vols)+1)
	for n, x := range v.vols {
		next[n] = x
	}
	next[vol.Name] = vol
	return v.save(next)
}

// Delete removes the volume of name, or returns ErrNotFound. It leaves the
// volume's folder alone.
func (v *Volumes) Delete(name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.vols[name]; !ok {
		return fmt.Errorf("volume %q: %w", name, ErrNotFound)
	}
	next := make(map[string]Volume, len(v.vols))
	for n, x := range v.vols {
		if n != name {
			next[n] = x
		}
	}
	return v.save(next)
}

// save writes next and adopts it, then syncs the directory; v.mu is held. As
// in Definitions.save, next is adopted once the file is in place, even when
// the sync fails.
func (v *Volumes) save(next map[string]Volume) error {
	if err := store.WriteJSON(v.path, volumesFile{Version: volumesVersion, Volumes: sortedVolumes(next)}); err != nil {
		return err
	}
	v.vols = next
	return syncVolumesDir(filepath.Dir(v.path))
}

// syncVolumesDir is syncDirectory, replaceable by tests.
var syncVolumesDir = syncDirectory

func sortedVolumes(vols map[string]Volume) []Volume {
	out := make([]Volume, 0, len(vols))
	for _, vol := range vols {
		out = append(out, vol)
	}
	slices.SortFunc(out, func(a, b Volume) int { return strings.Compare(a.Name, b.Name) })
	return out
}

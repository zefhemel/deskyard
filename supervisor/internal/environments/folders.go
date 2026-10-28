package environments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// Folder is a folder holding an environment's or a volume's files. Path is
// a mount source for containers and helpers. A folder OnEngineHost (a host
// volume's) is on the machine running the engine, which the supervisor
// can't see: only helpers reach it. Any other is in the supervisor's data
// folder, where it works with Path itself.
type Folder struct {
	Path         string
	OnEngineHost bool
}

// HomeFolder is the home folder of the environment name.
func (s *Service) HomeFolder(name string) Folder { return Folder{Path: s.Layout.HomeOf(name)} }

// HomePath is where an environment's home folder is mounted.
const HomePath = "/workspace"

// MountKind is what a FolderMount holds: the home or a granted volume.
type MountKind string

const (
	MountHome   MountKind = "home"
	MountVolume MountKind = "volume"
)

// FolderMount is one folder of an environment's mount table: Folder, at
// Target in the environment.
type FolderMount struct {
	Target   string
	Folder   Folder
	ReadOnly bool
	Kind     MountKind
	Volume   string // the granted volume's name, for MountVolume
}

// Mounts is the mount table of the environment name with grants: its home
// at HomePath, then each granted volume's folder at its GrantPath. Grants
// of volumes that don't exist are left out and named in missing. The
// container and WebDAV both serve this table.
func (s *Service) Mounts(name string, grants []VolumeGrant) (mounts []FolderMount, missing []string) {
	mounts = []FolderMount{{Target: HomePath, Folder: s.HomeFolder(name), Kind: MountHome}}
	for _, g := range grants {
		v, ok := s.Vols.Get(g.Name)
		if !ok {
			missing = append(missing, g.Name)
			continue
		}
		mounts = append(mounts, FolderMount{Target: GrantPath(g), Folder: s.VolumeFolder(v), ReadOnly: g.ReadOnly, Kind: MountVolume, Volume: g.Name})
	}
	return mounts, missing
}

// engineMounts are mounts as the engine takes them: strict, so the engine
// never makes a missing home or volume folder on start.
func engineMounts(mounts []FolderMount) []engine.Mount {
	out := make([]engine.Mount, len(mounts))
	for i, m := range mounts {
		out[i] = engine.Mount{Source: m.Folder.Path, Target: m.Target, ReadOnly: m.ReadOnly, MustExist: true}
	}
	return out
}

// validChild refuses anything but one folder name.
func validChild(child string) error {
	if child == "" || child == "." || child == ".." || strings.ContainsRune(child, '/') {
		return fmt.Errorf("not a folder name: %q", child)
	}
	return nil
}

// RemoveTree removes the folder child of parent, such as a workspace folder
// in home/, with everything in it. Its files belong to UID 1000 (and Flatpak
// leaves read-only directories) or to root, so what the supervisor can't
// remove itself is removed by a root helper. Nothing is ever removed from a
// folder on the engine host.
func (s *Service) RemoveTree(ctx context.Context, parent Folder, child string) error {
	if err := validChild(child); err != nil {
		return err
	}
	if parent.OnEngineHost {
		return fmt.Errorf("%w: Deskyard never deletes files on the engine host (%s)", ErrConflict, filepath.Join(parent.Path, child))
	}
	folder := filepath.Join(parent.Path, child)
	if err := os.RemoveAll(folder); err == nil {
		return nil
	}
	res, err := s.Helpers().Run(ctx, helper.Spec{
		Purpose: helper.Folders, Network: "none",
		Mounts: []engine.Mount{{Source: parent.Path, Target: "/parent", MustExist: true}},
		Cmd:    []string{"rm", "-rf", "--", "/parent/" + child},
	})
	if err != nil {
		return fmt.Errorf("deleting %s: %w", s.Layout.ToHost(folder), err)
	}
	if err := res.Err("deleting " + s.Layout.ToHost(folder)); err != nil {
		return err
	}
	if _, err := os.Stat(folder); err == nil {
		return fmt.Errorf("could not delete %s", s.Layout.ToHost(folder))
	}
	return nil
}

// removeTree is RemoveTree.
func (s *Service) removeTree(ctx context.Context, parent Folder, child string) error {
	return s.RemoveTree(ctx, parent, child)
}

// maxFreeNames bounds the numbered names MakeFreeFolder tries.
const maxFreeNames = 1000

// freeFolderScript makes, in the current folder, a folder named $1 or else
// "$1 (2)", "$1 (3)" … as mkdir, which fails on any name already there, has
// it; owns it to the desktop user; and prints its name.
const freeFolderScript = `base=$1
name=$1
n=2
while ! mkdir -m 0755 -- "$name" 2>/dev/null; do
	if [ ! -e "$name" ] && [ ! -L "$name" ]; then mkdir -m 0755 -- "$name"; exit 1; fi
	if [ "$n" -gt "$2" ]; then echo "no free name for $base" >&2; exit 1; fi
	name="$base ($n)"
	n=$((n + 1))
done
chown 1000:1000 -- "$name" && printf %s "$name"`

// MakeFreeFolder makes a new folder in parent named base, or, when that is
// taken, "base (2)", "base (3)" …, and returns its name. Each name is taken
// by a mkdir that fails if anything has it, so two calls never get the same
// folder. The folder has mode 0755 and belongs to the desktop user: one in
// the data folder is made by the supervisor (UID 1000), one on the engine
// host by a root helper, as the host's user may own its parent.
func (s *Service) MakeFreeFolder(ctx context.Context, parent Folder, base string) (string, error) {
	if err := validChild(base); err != nil {
		return "", err
	}
	if parent.OnEngineHost {
		var out bytes.Buffer
		res, err := s.Helpers().Run(ctx, helper.Spec{
			Purpose: helper.Folders, Network: "none",
			Mounts: []engine.Mount{{Source: parent.Path, Target: "/parent", MustExist: true}},
			Cmd:    []string{"sh", "-c", "cd /parent || exit 1\n" + freeFolderScript, "sh", base, fmt.Sprint(maxFreeNames)},
			Stdout: &out,
		})
		if err != nil {
			return "", fmt.Errorf("making %s: %w", base, err)
		}
		if err := res.Err("making " + base); err != nil {
			return "", err
		}
		name := out.String()
		if name != base && !strings.HasPrefix(name, base+" (") || validChild(name) != nil {
			return "", fmt.Errorf("making %s: the helper made %q", base, name)
		}
		return name, nil
	}
	name := base
	for n := 2; ; n++ {
		err := os.Mkdir(filepath.Join(parent.Path, name), 0o755)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
		if n > maxFreeNames {
			return "", fmt.Errorf("no free name for %s", base)
		}
		name = fmt.Sprintf("%s (%d)", base, n)
	}
}

// FolderExists reports whether a workspace folder of that name is already there.
func (s *Service) FolderExists(name string) bool {
	st, err := os.Stat(s.Layout.HomeOf(name))
	return err == nil && st.IsDir()
}

// missingHome is the error of a defined environment without its home folder.
func (s *Service) missingHome(name string) error {
	return fmt.Errorf("%s's home folder %s is missing", name, s.Layout.ToHost(s.Layout.HomeOf(name)))
}

// missingVolume is the error of the first of grants, name's, whose volume
// isn't in volumes.json or whose local folder is missing; nil when there is
// none. A host volume's folder is on the engine host, which the supervisor
// may not see: checkHostSources and the strict mount check that.
func (s *Service) missingVolume(name string, grants []VolumeGrant) error {
	for _, g := range grants {
		v, ok := s.Vols.Get(g.Name)
		if !ok {
			return fmt.Errorf("%s uses volume %s, which doesn't exist", name, g.Name)
		}
		if v.Kind == VolumeHost {
			continue
		}
		folder := s.Layout.VolumeOf(g.Name)
		if st, err := os.Stat(folder); err != nil || !st.IsDir() {
			return fmt.Errorf("%s uses volume %s, whose folder %s is missing", name, g.Name, s.Layout.ToHost(folder))
		}
	}
	return nil
}

// missingFolder is name's missing home folder, or else the first volume
// of grants missing as missingVolume has it; nil when there is none.
func (s *Service) missingFolder(name string, grants []VolumeGrant) error {
	if !s.FolderExists(name) {
		return s.missingHome(name)
	}
	return s.missingVolume(name, grants)
}

// checkFolders records a missing home or granted volume folder as name's
// error, and clears that error once the folder is back. It is kept apart from
// the error of a failed change, which shows again once the folder is back.
// Only creation makes a home, and only CreateVolume a volume folder.
func (s *Service) checkFolders(name string) error {
	def, defined := s.Defs.Get(name)
	err := s.missingFolder(name, def.Volumes)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil || !defined {
		delete(s.folderErrs, name)
	} else {
		s.folderErrs[name] = err.Error()
	}
	return err
}

// FolderSize adds up the sizes of the files in an environment's folder. complete
// is false when some directories couldn't be read (the supervisor is UID 1000,
// so only root-owned files, as an environment with Docker access can leave,
// are out of reach).
func (s *Service) FolderSize(name string) (bytes int64, complete bool) {
	return treeSize(s.Layout.HomeOf(name))
}

// VolumeSize is FolderSize for the folder of the volume name. A host
// volume's folder is on the engine host, which the supervisor can't see:
// its size is 0 and incomplete.
func (s *Service) VolumeSize(name string) (bytes int64, complete bool) {
	v, ok := s.Vols.Get(name)
	if !ok {
		return 0, false
	}
	folder := s.VolumeFolder(v)
	if folder.OnEngineHost {
		return 0, false
	}
	return treeSize(folder.Path)
}

func treeSize(dir string) (bytes int64, complete bool) {
	complete = true
	// Through an os.Root: an environment's links never lead the walk outside.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, !errors.Is(err, fs.ErrPermission)
	}
	defer root.Close()
	_ = fs.WalkDir(root.FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			complete = false
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				bytes += info.Size()
			}
		}
		return nil
	})
	return bytes, complete
}

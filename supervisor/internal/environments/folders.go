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

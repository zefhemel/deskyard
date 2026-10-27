package environments

import (
	"fmt"
	"path"
	"strings"
)

// ShareRoot is where a granted volume appears in an environment, at
// ShareRoot/<volume>, unless its grant names another path.
const ShareRoot = "/share"

// systemPaths can't take a volume, nor can anything below them: a mount there
// would replace part of the desktop's own runtime.
var systemPaths = []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32", "/opt", "/proc", "/run", "/sbin", "/sys", "/usr", "/var"}

// GrantPath is where g's volume is mounted in the environment.
func GrantPath(g VolumeGrant) string {
	if g.Path == "" {
		return path.Join(ShareRoot, g.Name)
	}
	return g.Path
}

// ValidateGrantPath checks a path in the environment for a volume: clean and
// absolute, and neither the whole filesystem, the home (workspace-init chowns
// /workspace recursively, which would re-own a mounted host folder or fail on
// a read-only one) nor a system folder.
func ValidateGrantPath(p string) error {
	switch {
	case len(p) > 4096 || !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0):
		return fmt.Errorf("the path %q must be absolute", p)
	case path.Clean(p) != p:
		return fmt.Errorf("the path %q must be clean (no trailing slash, . or ..)", p)
	case p == "/":
		return fmt.Errorf("a volume can't replace the whole filesystem")
	case within(p, "/workspace"):
		return fmt.Errorf("the path %q is in the home folder /workspace, which the desktop takes over", p)
	}
	for _, sys := range systemPaths {
		if within(p, sys) {
			return fmt.Errorf("the path %q is in %s, which the desktop runs from", p, sys)
		}
	}
	return nil
}

// ValidateVolumeGrants checks an environment's grants: valid volume names,
// each volume once, valid paths, none equal to or inside another. Whether the
// volumes exist is checked apart.
func ValidateVolumeGrants(grants []VolumeGrant) error {
	for i, g := range grants {
		if err := ValidName(g.Name); err != nil {
			return fmt.Errorf("volume %d: %w", i+1, err)
		}
		p := GrantPath(g)
		if err := ValidateGrantPath(p); err != nil {
			return fmt.Errorf("volume %s: %w", g.Name, err)
		}
		for _, other := range grants[:i] {
			if g.Name == other.Name {
				return fmt.Errorf("volume %s is granted twice", g.Name)
			}
			if q := GrantPath(other); within(p, q) || within(q, p) {
				return fmt.Errorf("the paths of volumes %s and %s overlap: %s and %s", other.Name, g.Name, q, p)
			}
		}
	}
	return nil
}

// withGrantPaths returns grants with every default path spelled out.
func withGrantPaths(grants []VolumeGrant) []VolumeGrant {
	out := make([]VolumeGrant, len(grants))
	for i, g := range grants {
		g.Path = GrantPath(g)
		out[i] = g
	}
	return out
}

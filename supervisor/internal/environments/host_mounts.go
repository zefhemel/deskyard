package environments

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// HostMount exposes an existing engine-host folder inside one environment. Targets
// stay outside the home and image runtime so initialization cannot chown or seed
// host files, and a mount cannot replace a service or credential directory.
type HostMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}

func ValidateHostMounts(mounts []HostMount) error {
	if len(mounts) > 16 {
		return errors.New("an environment can have at most 16 host folders")
	}
	for i, m := range mounts {
		if len(m.Source) > 4096 || strings.ContainsRune(m.Source, 0) || !filepath.IsAbs(m.Source) || filepath.Clean(m.Source) != m.Source {
			return fmt.Errorf("host folder %d needs a clean absolute source path", i+1)
		}
		if len(m.Target) > 4096 || strings.ContainsRune(m.Target, 0) || !strings.HasPrefix(m.Target, "/mnt/") || path.Clean(m.Target) != m.Target {
			return fmt.Errorf("host folder %d needs an environment path below /mnt/", i+1)
		}
		for _, other := range mounts[:i] {
			if m.Target == other.Target || strings.HasPrefix(m.Target, other.Target+"/") || strings.HasPrefix(other.Target, m.Target+"/") {
				return fmt.Errorf("host folder targets overlap: %s and %s", m.Target, other.Target)
			}
		}
	}
	return nil
}

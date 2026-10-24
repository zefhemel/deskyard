package desktops

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

const LabelHostMounts = "deskyard.setting.mounts"

// HostMount exposes an existing engine-host folder inside one desktop. Targets
// stay outside the home and image runtime so initialization cannot chown or seed
// host files, and a mount cannot replace a service or credential directory.
type HostMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}

func ValidateHostMounts(mounts []HostMount) error {
	if len(mounts) > 16 {
		return errors.New("a desktop can have at most 16 host folders")
	}
	for i, m := range mounts {
		if len(m.Source) > 4096 || strings.ContainsRune(m.Source, 0) || !filepath.IsAbs(m.Source) || filepath.Clean(m.Source) != m.Source {
			return fmt.Errorf("host folder %d needs a clean absolute source path", i+1)
		}
		if len(m.Target) > 4096 || strings.ContainsRune(m.Target, 0) || !strings.HasPrefix(m.Target, "/mnt/") || path.Clean(m.Target) != m.Target {
			return fmt.Errorf("host folder %d needs a desktop path below /mnt/", i+1)
		}
		for _, other := range mounts[:i] {
			if m.Target == other.Target || strings.HasPrefix(m.Target, other.Target+"/") || strings.HasPrefix(other.Target, m.Target+"/") {
				return fmt.Errorf("host folder targets overlap: %s and %s", m.Target, other.Target)
			}
		}
	}
	return nil
}

func hostMountsFromLabels(labels map[string]string) ([]HostMount, error) {
	mounts := []HostMount{}
	if raw := labels[LabelHostMounts]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &mounts); err != nil {
			return nil, fmt.Errorf("invalid saved host folders: %w", err)
		}
	}
	if mounts == nil {
		mounts = []HostMount{}
	}
	if err := ValidateHostMounts(mounts); err != nil {
		return nil, fmt.Errorf("invalid saved host folders: %w", err)
	}
	return mounts, nil
}

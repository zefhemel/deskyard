package desktops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
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

// checkHostMountSources runs against the engine host, which may be a different
// machine or the host of a containerized supervisor. Strict binds refuse missing
// paths instead of creating empty directories. Read-only probes never modify
// user data. Complete this before stopping or removing the current desktop.
func (s *Service) checkHostMountSources(ctx context.Context, mounts []HostMount) (retErr error) {
	if err := ValidateHostMounts(mounts); err != nil {
		return err
	}
	if len(mounts) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := s.ensureHelper(ctx); err != nil {
		return fmt.Errorf("preparing host-folder check: %w", err)
	}
	spec := engine.ContainerSpec{Name: "deskyard-mount-check-" + randomHex(4), Image: helperImage, Network: "none",
		Cmd: []string{"sleep", "60"}, MemoryBytes: 32 << 20, NanoCPUs: 1e9, NoHealthcheck: true}
	for i, m := range mounts {
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: m.Source, Target: fmt.Sprintf("/check/%d", i), ReadOnly: true, MustExist: true})
	}
	id, err := s.Eng.Create(ctx, spec)
	if err != nil {
		return fmt.Errorf("checking host folders: %w", err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer done()
		if err := s.Eng.Remove(cleanup, id); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("removing host-folder check: %w", err))
		}
	}()
	if err := s.Eng.Start(ctx, id); err != nil {
		return fmt.Errorf("checking host folders: %w", err)
	}
	_, _, code, err := engine.Output(ctx, s.Eng, id, engine.ExecSpec{Cmd: []string{"sh", "-c", `for folder in /check/*; do [ -d "$folder" ] || exit 1; done`}, User: "0"})
	if err != nil {
		return fmt.Errorf("checking host folders: %w", err)
	}
	if code != 0 {
		return errors.New("host mount sources must be existing folders, not files")
	}
	return nil
}

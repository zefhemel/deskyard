package main

import (
	"errors"
	"path/filepath"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// dataHostPath returns the host folder bind-mounted at /data in the
// supervisor's own container. The engine resolves the desktops' mounts on the
// host, so the supervisor must know where /data lives there. A named volume
// is refused: its folder is inside the engine's own storage.
func dataHostPath(self engine.Container) (string, error) {
	for _, m := range self.Mounts {
		if m.Target != config.DataDir {
			continue
		}
		if m.Volume != "" {
			return "", errors.New("a named volume at /data isn't supported; mount a host folder (-v <host folder>:/data)")
		}
		// A tmpfs or similar has no host folder to translate to.
		if !filepath.IsAbs(m.Source) {
			break
		}
		return m.Source, nil
	}
	return "", errors.New("mount a host folder at /data (-v <host folder>:/data)")
}

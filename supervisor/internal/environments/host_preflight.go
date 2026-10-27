package environments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// hostSource is a host volume's folder on the engine host.
type hostSource struct{ volume, path string }

func describeSources(sources []hostSource) string {
	parts := make([]string, len(sources))
	for i, h := range sources {
		parts[i] = fmt.Sprintf("%s (%s)", h.volume, h.path)
	}
	return strings.Join(parts, ", ")
}

// hostSources returns the engine-host folders of the host volumes grants
// name; grants of unknown or local volumes have none.
func (s *Service) hostSources(grants []VolumeGrant) []hostSource {
	var out []hostSource
	for _, g := range grants {
		if v, ok := s.Vols.Get(g.Name); ok && v.Kind == VolumeHost {
			out = append(out, hostSource{volume: v.Name, path: v.HostPath})
		}
	}
	return out
}

// checkHostSources validates host volume folders and the separately granted
// Docker permission on the engine host, before creating or replacing an
// environment: the supervisor may not see engine-host paths itself. One
// networkless helper owns all strict read-only probes and their cleanup.
func (s *Service) checkHostSources(ctx context.Context, mounts []hostSource, dockerAccess bool) (retErr error) {
	if len(mounts) == 0 && !dockerAccess {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := s.ensureHelper(ctx); err != nil {
		return fmt.Errorf("preparing engine-host mount check: %w", err)
	}
	spec := engine.ContainerSpec{Name: "deskyard-mount-check-" + randomHex(4), Image: helperImage, Network: "none", Cmd: []string{"sleep", "60"}, MemoryBytes: 32 << 20, NanoCPUs: 1e9, NoHealthcheck: true}
	var checks []string
	for i, m := range mounts {
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: m.path, Target: fmt.Sprintf("/check/%d", i), ReadOnly: true, MustExist: true})
	}
	if len(mounts) > 0 {
		checks = append(checks, `for folder in /check/*; do [ -d "$folder" ] || exit 1; done`)
	}
	if dockerAccess {
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: dockerSocket, Target: "/docker.sock", ReadOnly: true, MustExist: true})
		checks = append(checks, `test -S /docker.sock || exit 2`)
	}
	id, err := s.Eng.Create(ctx, spec)
	if err != nil {
		if dockerAccess {
			return fmt.Errorf("checking host volumes %s and Docker daemon access (requires an existing Unix socket at %s on the engine host): %w", describeSources(mounts), dockerSocket, err)
		}
		return fmt.Errorf("checking host volumes %s (their folders must exist on the engine host): %w", describeSources(mounts), err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer done()
		if err := s.Eng.Remove(cleanup, id); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("removing engine-host mount check: %w", err))
		}
	}()
	if err := s.Eng.Start(ctx, id); err != nil {
		return fmt.Errorf("starting engine-host mount check: %w", err)
	}
	_, _, code, err := engine.Output(ctx, s.Eng, id, engine.ExecSpec{User: "0", Cmd: []string{"sh", "-c", strings.Join(checks, "\n")}})
	if err != nil {
		return fmt.Errorf("checking engine-host mount sources: %w", err)
	}
	switch code {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("host volumes %s must be existing folders on the engine host, not files", describeSources(mounts))
	case 2:
		return fmt.Errorf("Docker daemon access requires a Unix socket at %s on the engine host", dockerSocket)
	default:
		return fmt.Errorf("engine-host mount check exited with status %d", code)
	}
}

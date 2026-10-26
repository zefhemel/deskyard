package environments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// checkHostSources validates both explicit folder mounts and the separately
// granted Docker permission on the engine host, before replacing an environment.
// One networkless helper owns all strict read-only probes and their cleanup.
func (s *Service) checkHostSources(ctx context.Context, mounts []HostMount, dockerAccess bool) (retErr error) {
	if err := ValidateHostMounts(mounts); err != nil {
		return err
	}
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
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: m.Source, Target: fmt.Sprintf("/check/%d", i), ReadOnly: true, MustExist: true})
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
			return fmt.Errorf("checking host folders and Docker daemon access (requires an existing Unix socket at %s on the engine host): %w", dockerSocket, err)
		}
		return fmt.Errorf("checking host folders: %w", err)
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
		return errors.New("host mount sources must be existing folders, not files")
	case 2:
		return fmt.Errorf("Docker daemon access requires a Unix socket at %s on the engine host", dockerSocket)
	default:
		return fmt.Errorf("engine-host mount check exited with status %d", code)
	}
}

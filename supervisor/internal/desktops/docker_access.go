package desktops

import (
	"context"
	"errors"
	"fmt"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"time"
)

const LabelDockerAccess = "deskyard.setting.docker-access"
const dockerSocket = "/var/run/docker.sock"

// checkDockerAccess checks the daemon's filesystem, including when the client
// runs on macOS. Strict binds never create a missing socket path as a directory.
// Do this before stopping an existing desktop, and do nothing when revoking.
func (s *Service) checkDockerAccess(ctx context.Context, enabled bool) (retErr error) {
	if !enabled {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := s.ensureHelper(ctx); err != nil {
		return fmt.Errorf("preparing Docker daemon access check: %w", err)
	}
	id, err := s.Eng.Create(ctx, engine.ContainerSpec{Name: "deskyard-docker-check-" + randomHex(4), Image: helperImage, Network: "none", Cmd: []string{"sleep", "60"}, MemoryBytes: 32 << 20, NanoCPUs: 1e9, NoHealthcheck: true, Mounts: []engine.Mount{{Source: dockerSocket, Target: "/docker.sock", ReadOnly: true, MustExist: true}}})
	if err != nil {
		return fmt.Errorf("Docker daemon access requires an existing Unix socket at %s on the engine host: %w", dockerSocket, err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer done()
		if err := s.Eng.Remove(cleanup, id); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("removing Docker socket check: %w", err))
		}
	}()
	if err := s.Eng.Start(ctx, id); err != nil {
		return fmt.Errorf("checking Docker daemon socket: %w", err)
	}
	_, _, code, err := engine.Output(ctx, s.Eng, id, engine.ExecSpec{User: "0", Cmd: []string{"sh", "-c", "test -S /docker.sock"}})
	if err != nil {
		return fmt.Errorf("checking Docker daemon socket: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("Docker daemon access requires a Unix socket at %s on the engine host", dockerSocket)
	}
	return nil
}

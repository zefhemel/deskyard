package environments

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// folderCheck is which of an environment's local folders preflight checks.
type folderCheck int

const (
	// savedFolders: the home and volume folders of the saved definition,
	// recorded on the environment while one is missing (checkFolders).
	savedFolders folderCheck = iota
	// allFolders: the home and the folders of the grants checked.
	allFolders
	// grantFolders: only the grants' folders, for a home still to be made.
	grantFolders
	// noFolders: none, for a change only saved until a start builds it.
	noFolders
)

// preflight is what creating, starting or changing the container of the
// environment name checks first, so that a missing folder or socket is
// named rather than left to the engine's bind error, and nothing is changed
// when one is: its local folders as folders says, and, with one helper on
// the engine host, the folders of the host volumes among grants and, with
// dockerAccess, the Docker socket. Files being replaced, its own or a
// granted volume's, refuse it with ErrItemBusy. progress, when set, hears of
// the helper.
func (s *Service) preflight(ctx context.Context, name string, grants []VolumeGrant, dockerAccess bool, folders folderCheck, progress func(string, float64)) error {
	if err := s.replacing(itemsOf(name, grants)); err != nil {
		return err
	}
	var err error
	switch folders {
	case savedFolders:
		err = s.checkFolders(name)
	case allFolders:
		err = s.missingFolder(name, grants)
	case grantFolders:
		err = s.missingVolume(name, grants)
	}
	if err != nil {
		return err
	}
	hosts := s.hostSources(grants)
	if len(hosts) > 0 && progress != nil {
		progress("Checking host volumes", -1)
	}
	return s.checkHostSources(ctx, hosts, dockerAccess)
}

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
func (s *Service) checkHostSources(ctx context.Context, mounts []hostSource, dockerAccess bool) error {
	if len(mounts) == 0 && !dockerAccess {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	spec := helper.Spec{Purpose: helper.MountCheck, Network: "none", MemoryBytes: 32 << 20, NanoCPUs: 1e9}
	var checks []string
	for i, m := range mounts {
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: m.path, OnEngineHost: true, Target: fmt.Sprintf("/check/%d", i), ReadOnly: true, MustExist: true})
	}
	if len(mounts) > 0 {
		checks = append(checks, `for folder in /check/*; do [ -d "$folder" ] || exit 1; done`)
	}
	if dockerAccess {
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: dockerSocket, OnEngineHost: true, Target: "/docker.sock", ReadOnly: true, MustExist: true})
		checks = append(checks, `test -S /docker.sock || exit 2`)
	}
	spec.Cmd = []string{"sh", "-c", strings.Join(checks, "\n")}
	res, err := s.Helpers().Run(ctx, spec)
	if err != nil {
		if dockerAccess {
			return fmt.Errorf("checking host volumes %s and Docker daemon access (requires an existing Unix socket at %s on the engine host): %w", describeSources(mounts), dockerSocket, err)
		}
		return fmt.Errorf("checking host volumes %s (their folders must exist on the engine host): %w", describeSources(mounts), err)
	}
	switch res.Code {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("host volumes %s must be existing folders on the engine host, not files", describeSources(mounts))
	case 2:
		return fmt.Errorf("Docker daemon access requires a Unix socket at %s on the engine host", dockerSocket)
	default:
		return fmt.Errorf("engine-host mount check exited with status %d", res.Code)
	}
}

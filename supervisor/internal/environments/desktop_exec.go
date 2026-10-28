package environments

import "github.com/zefhemel/deskyard/supervisor/internal/engine"

// Desktop images that run their session in its own user and mount namespace
// (for FUSE document portals) carry this label; workspace-exec joins it.
const (
	labelFUSE        = "deskyard.fuse"
	fuseUserNS       = "userns-v1"
	desktopUser      = "user"
	workspaceExecBin = "/usr/local/bin/workspace-exec"
)

// SessionNamespace reports whether the desktop session runs in its own
// namespace, which execs must join to see what the session sees. A container
// carries its image's labels from when it was created, so this is decided once
// per container, and needs no image lookup for containers made before the
// supervisor recorded anything itself.
func (e Environment) SessionNamespace() bool {
	return e.Container.Labels[labelFUSE] == fuseUserNS
}

// DesktopExec makes spec an exec in the environment's desktop session: run as
// the desktop's user and, when the session has its own namespace, inside it.
// spec's User is replaced.
func (e Environment) DesktopExec(spec engine.ExecSpec) engine.ExecSpec {
	spec.User = desktopUser
	if !e.SessionNamespace() {
		return spec
	}
	prefix := []string{workspaceExecBin}
	if spec.WorkDir != "" {
		// Portal paths only exist after entering the session mount namespace.
		prefix = append(prefix, "/usr/bin/env", "--chdir="+spec.WorkDir, "--")
		spec.WorkDir = "/"
	}
	spec.Cmd = append(prefix, spec.Cmd...)
	return spec
}

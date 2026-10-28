package environments

import (
	"reflect"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestDesktopExec(t *testing.T) {
	cmd := []string{"printf", "a b", "$HOME"}
	for _, tc := range []struct {
		name    string
		labels  map[string]string
		workDir string
		want    engine.ExecSpec
	}{
		{"legacy", nil, "/run/workspace/doc/probe", engine.ExecSpec{User: "user", WorkDir: "/run/workspace/doc/probe", Cmd: cmd}},
		{"userns", map[string]string{"deskyard.fuse": "userns-v1"}, "/run/workspace/doc/probe", engine.ExecSpec{
			User: "user", WorkDir: "/",
			Cmd: append([]string{"/usr/local/bin/workspace-exec", "/usr/bin/env", "--chdir=/run/workspace/doc/probe", "--"}, cmd...),
		}},
		{"userns without a folder", map[string]string{"deskyard.fuse": "userns-v1"}, "", engine.ExecSpec{
			User: "user", Cmd: append([]string{"/usr/local/bin/workspace-exec"}, cmd...),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := Environment{Container: engine.Container{ID: "c", Labels: tc.labels}}
			// Callers can't run a desktop exec as anyone else.
			got := env.DesktopExec(engine.ExecSpec{User: "root", WorkDir: tc.workDir, Cmd: cmd})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("exec: %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDesktopExecLeavesCallerArgv(t *testing.T) {
	cmd := make([]string, 1, 4)
	cmd[0] = "true"
	env := Environment{Container: engine.Container{Labels: map[string]string{"deskyard.fuse": "userns-v1"}}}
	env.DesktopExec(engine.ExecSpec{Cmd: cmd, WorkDir: "/workspace"})
	if cmd[0] != "true" {
		t.Fatalf("argv changed: %v", cmd)
	}
}

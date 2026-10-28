package environments

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// Faults are injected at the remote engine boundary, preserving real service
// state, settings validation, preflight ownership, and error handling.
type preflightFaultEngine struct {
	*dockerAccessEngine
	createErr, startErr, execErr, removeErr error
	cancel                                  context.CancelFunc
	operationDeadline                       time.Time
	cleanupErr                              error
	cleanupDeadline                         time.Time
	removals                                int
}

func (e *preflightFaultEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	e.operationDeadline, _ = ctx.Deadline()
	if e.createErr != nil {
		return "", e.createErr
	}
	return e.dockerAccessEngine.Create(ctx, s)
}
func (e *preflightFaultEngine) Start(ctx context.Context, id string) error {
	if e.startErr != nil {
		return e.startErr
	}
	return e.dockerAccessEngine.Start(ctx, id)
}
func (e *preflightFaultEngine) Exec(ctx context.Context, id string, s engine.ExecSpec) (*engine.Process, error) {
	if e.cancel != nil {
		e.cancel()
		return nil, ctx.Err()
	}
	if e.execErr != nil {
		return nil, e.execErr
	}
	return e.dockerAccessEngine.Exec(ctx, id, s)
}
func (e *preflightFaultEngine) Remove(ctx context.Context, id string) error {
	e.removals++
	e.cleanupErr = ctx.Err()
	e.cleanupDeadline, _ = ctx.Deadline()
	if e.removeErr != nil {
		return e.removeErr
	}
	return e.dockerAccessEngine.Remove(ctx, id)
}

func TestHostPreflightFailuresPreserveEnvironmentAndOwnCleanup(t *testing.T) {
	fault := errors.New("engine failure")
	cleanupFault := errors.New("cleanup failure")
	for _, stage := range []string{"success", "create", "start", "exec", "cancel", "cleanup", "start and cleanup"} {
		t.Run(stage, func(t *testing.T) {
			s, base := dockerAccessService(t)
			if err := s.seed(context.Background(), "alice", Settings{Image: "img"}); err != nil {
				t.Fatal(err)
			}
			if err := base.Start(context.Background(), "deskyard-alice"); err != nil {
				t.Fatal(err)
			}
			e := &preflightFaultEngine{dockerAccessEngine: base}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch stage {
			case "create":
				e.createErr = fault
			case "start":
				e.startErr = fault
			case "exec":
				e.execErr = fault
			case "cancel":
				e.cancel = cancel
			case "cleanup":
				e.removeErr = cleanupFault
			case "start and cleanup":
				e.startErr = fault
				e.removeErr = cleanupFault
			}
			s.Eng = e
			// Exercise Recreate for failure cases to prove no stop or replacement occurs.
			set := Settings{Image: "img", DockerAccess: true, Volumes: []VolumeGrant{hostVolume(t, s, "data", t.TempDir(), "/mnt/data", false)}}
			var err error
			if stage == "success" {
				err = s.checkHostSources(ctx, s.hostSources(set.Volumes), true)
			} else {
				_, err = s.Recreate(ctx, "alice", set, noProgress)
			}
			if stage == "success" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				want := fault
				if stage == "cancel" {
					want = context.Canceled
				}
				if stage == "cleanup" {
					want = cleanupFault
				}
				if !errors.Is(err, want) {
					t.Fatalf("lost failure: %v, want %v", err, want)
				}
				if e.removeErr != nil && !errors.Is(err, cleanupFault) {
					t.Fatalf("lost cleanup error: %v", err)
				}
			}
			if e.operationDeadline.IsZero() || time.Until(e.operationDeadline) > 45*time.Second {
				t.Fatal("unbounded operation")
			}
			wantRemovals := 1
			if stage == "create" {
				wantRemovals = 0
			}
			if e.removals != wantRemovals {
				t.Fatalf("cleanup calls %d, want %d", e.removals, wantRemovals)
			}
			if wantRemovals > 0 && (e.cleanupErr != nil || e.cleanupDeadline.IsZero() || time.Until(e.cleanupDeadline) > 10*time.Second) {
				t.Fatalf("cleanup inherited cancellation or lacks bound: %v %v", e.cleanupErr, e.cleanupDeadline)
			}
			d, getErr := s.Get(context.Background(), "alice")
			if getErr != nil || !d.Running() || d.ID != "alice" || e.stops != 0 {
				t.Fatalf("preflight disrupted environment: %+v %v", d, getErr)
			}
			if e.removeErr == nil && stage != "create" {
				for id, spec := range e.specs {
					if spec.Image == helper.Image {
						if _, exists := e.cs[id]; exists {
							t.Fatalf("helper leaked: %s", id)
						}
					}
				}
			}
		})
	}
}

// Execute the emitted type checks with real folders, files and a Unix socket.
// Only container mount translation is simulated; shell exit statuses are real.
type localPreflightEngine struct {
	*dockerAccessEngine
	folder, socket string
}

func (e *localPreflightEngine) Exec(ctx context.Context, id string, spec engine.ExecSpec) (*engine.Process, error) {
	cmd := exec.CommandContext(ctx, spec.Cmd[0], spec.Cmd[1:]...)
	cmd.Args[len(cmd.Args)-1] = strings.ReplaceAll(strings.ReplaceAll(cmd.Args[len(cmd.Args)-1], "/check/*", `"$FOLDER"`), "/docker.sock", `"$SOCKET"`)
	cmd.Env = append(os.Environ(), "FOLDER="+e.folder, "SOCKET="+e.socket)
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, err
		}
		code = exit.ExitCode()
	}
	return &engine.Process{Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(nil), Wait: func() (int, error) { return code, nil }, Close: func() {}}, nil
}
func TestHostPreflightChecksBothSourceTypes(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "preflight-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, tt := range []struct{ name, folder, socket, want string }{
		{"valid", root, socket, ""},
		{"folder is file", file, socket, "existing folders"},
		{"socket is file", root, file, "Unix socket"},
		{"folder is socket", socket, socket, "existing folders"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, base := dockerAccessService(t)
			s.Eng = &localPreflightEngine{dockerAccessEngine: base, folder: tt.folder, socket: tt.socket}
			err := s.checkHostSources(context.Background(), []hostSource{{volume: "data", path: tt.folder}}, true)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
		})
	}
}

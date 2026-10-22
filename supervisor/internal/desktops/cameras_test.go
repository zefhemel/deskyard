package desktops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestCameras(t *testing.T) {
	sys := t.TempDir()
	if got := Cameras(sys); len(got) != 0 {
		t.Fatalf("no video4linux folder: %v", got)
	}
	for _, n := range []string{"video10", "video2", "v4l-subdev0", "videox"} {
		addCamera(t, sys, n)
	}
	if got, want := Cameras(sys), []string{"/dev/video2", "/dev/video10"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLocalEngine(t *testing.T) {
	for host, want := range map[string]bool{"": true, "unix:///var/run/docker.sock": true, "tcp://10.0.0.5:2376": false, "ssh://me@box": false} {
		if LocalEngine(host) != want {
			t.Errorf("%q: want %v", host, want)
		}
	}
}

func TestDesktopWebcam(t *testing.T) {
	for _, tc := range []struct {
		state, camera string
		want          bool
	}{
		{"running", "/dev/video0", true},
		{"running", "", false},
		{"stopped", "/dev/video0", false},
	} {
		c := Desktop{State: tc.state, Settings: Settings{Camera: tc.camera}}
		if got := c.Webcam(); got != tc.want {
			t.Errorf("%s %q: got %v", tc.state, tc.camera, got)
		}
	}
}

func addCamera(t *testing.T, sys, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(sys, "devices", "virtual", "video4linux", name), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeEngine keeps containers in memory. A container starts only while the
// host (its fake sysfs) still has the devices passed into it, as Docker's
// does.
type fakeEngine struct {
	engine.Engine
	sys string
	mu  sync.Mutex
	cs  map[string]*engine.Container
}

func newFakeEngine(sys string) *fakeEngine {
	return &fakeEngine{sys: sys, cs: map[string]*engine.Container{}}
}

func (e *fakeEngine) Create(_ context.Context, s engine.ContainerSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.cs[s.Name]; ok {
		return "", errors.New("name in use")
	}
	e.cs[s.Name] = &engine.Container{ID: s.Name, Name: s.Name, Image: s.Image, ImageID: s.Image, Labels: s.Labels, Env: s.Env,
		State: "created", Devices: s.Devices}
	return s.Name, nil
}

func (e *fakeEngine) Start(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cs[id]
	if !ok {
		return engine.ErrNotFound
	}
	for _, d := range c.Devices {
		if !slices.Contains(Cameras(e.sys), d) {
			return fmt.Errorf("error gathering device information while adding custom device %q: no such file or directory", d)
		}
	}
	c.State = "running"
	return nil
}

func (e *fakeEngine) Stop(_ context.Context, id string, _ time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.cs[id]; ok {
		c.State = "exited"
	}
	return nil
}

func (e *fakeEngine) Remove(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.cs, id)
	return nil
}

func (e *fakeEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.cs[id]; ok {
		return *c, nil
	}
	return engine.Container{}, engine.ErrNotFound
}

func (e *fakeEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []engine.Container
	for _, c := range e.cs {
		out = append(out, *c)
	}
	return out, nil
}

func (e *fakeEngine) ImageInspect(_ context.Context, ref string) (engine.Image, error) {
	return engine.Image{ID: ref, Labels: map[string]string{LabelContract: Contract}}, nil
}

func newCameraService(t *testing.T, local bool) (*Service, *fakeEngine, string, *bytes.Buffer) {
	sys := t.TempDir()
	eng := newFakeEngine(sys)
	var log bytes.Buffer
	s := New(eng, Config{DataDir: t.TempDir(), SysDir: sys, LocalEngine: local}, slog.New(slog.NewTextHandler(&log, nil)))
	return s, eng, sys, &log
}

func TestCamerasAreAssigned(t *testing.T) {
	s, eng, sys, _ := newCameraService(t, true)
	ctx := context.Background()
	addCamera(t, sys, "video0")
	addCamera(t, sys, "video1")

	// No camera by default.
	if err := s.createContainer(ctx, "alice", "id-a", "tok", time.Time{}, Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Get(ctx, "alice"); c.Settings.Camera != "" || len(eng.cs["deskyard-alice"].Devices) != 0 {
		t.Fatalf("alice got a camera: %+v", c.Settings)
	}

	// Bob gets video0, and only bob has it.
	if err := s.createContainer(ctx, "bob", "id-b", "tok", time.Time{}, Settings{Image: "img", Camera: "/dev/video0"}); err != nil {
		t.Fatal(err)
	}
	bob, _ := s.Get(ctx, "bob")
	if bob.Settings.Camera != "/dev/video0" || !slices.Equal(eng.cs["deskyard-bob"].Devices, []string{"/dev/video0"}) {
		t.Fatalf("bob: %+v %v", bob.Settings, eng.cs["deskyard-bob"].Devices)
	}
	if err := s.CheckCamera(ctx, "alice", "/dev/video0"); !errors.Is(err, ErrCameraTaken) {
		t.Errorf("alice may take bob's camera: %v", err)
	}
	if err := s.createContainer(ctx, "carol", "id-c", "tok", time.Time{}, Settings{Image: "img", Camera: "/dev/video0"}); !errors.Is(err, ErrCameraTaken) {
		t.Errorf("carol was made with bob's camera: %v", err)
	}
	if err := s.CheckCamera(ctx, "bob", "/dev/video0"); err != nil {
		t.Errorf("bob's own camera: %v", err)
	}
	if err := s.CheckCamera(ctx, "alice", "/dev/video1"); err != nil {
		t.Errorf("a free camera: %v", err)
	}
	if err := s.CheckCamera(ctx, "alice", "/dev/video7"); !errors.Is(err, ErrCameraUnknown) {
		t.Errorf("a camera the host hasn't: %v", err)
	}
	// Given to alice by a recreate; bob keeps his.
	if _, err := s.Recreate(ctx, "alice", Settings{Image: "img", Camera: "/dev/video1"}, func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Get(ctx, "alice"); a.Settings.Camera != "/dev/video1" {
		t.Errorf("alice: %+v", a.Settings)
	}
	if b, _ := s.Get(ctx, "bob"); b.Settings.Camera != "/dev/video0" {
		t.Errorf("bob: %+v", b.Settings)
	}
}

func TestNoCamerasFromARemoteEngine(t *testing.T) {
	s, _, sys, _ := newCameraService(t, false)
	addCamera(t, sys, "video0")
	if got := s.HostCameras(); got != nil {
		t.Errorf("a remote engine's cameras: %v", got)
	}
	if err := s.CheckCamera(context.Background(), "alice", "/dev/video0"); !errors.Is(err, ErrCameraUnknown) {
		t.Errorf("%v", err)
	}
}

func TestStartWithoutAGoneCamera(t *testing.T) {
	s, eng, sys, log := newCameraService(t, true)
	ctx := context.Background()
	addCamera(t, sys, "video0")
	if err := s.createContainer(ctx, "bob", "id-b", "tok", time.Time{}, Settings{Image: "img", Camera: "/dev/video0"}); err != nil {
		t.Fatal(err)
	}
	// The host loses its camera (a reboot without the module).
	if err := os.RemoveAll(filepath.Join(sys, "devices")); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	bob, _ := s.Get(ctx, "bob")
	if !bob.Running() || bob.Settings.Camera != "" || len(eng.cs["deskyard-bob"].Devices) != 0 {
		t.Fatalf("bob: %s %+v", bob.State, bob.Settings)
	}
	if !strings.Contains(log.String(), "camera gone from the host") {
		t.Errorf("not logged: %s", log)
	}
}

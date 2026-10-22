package desktops

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestCameras(t *testing.T) {
	sys := t.TempDir()
	if got := Cameras(sys); len(got) != 0 {
		t.Fatalf("no video4linux folder: %v", got)
	}
	v4l := filepath.Join(sys, "devices", "virtual", "video4linux")
	for _, n := range []string{"video10", "video2", "v4l-subdev0", "videox"} {
		if err := os.MkdirAll(filepath.Join(v4l, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := Cameras(sys), []string{"/dev/video2", "/dev/video10"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDesktopWebcam(t *testing.T) {
	for _, tc := range []struct {
		state   string
		devices []string
		want    bool
	}{
		{"running", []string{"/dev/video0"}, true},
		{"running", nil, false},
		{"running", []string{"/dev/dri/renderD128"}, false},
		{"stopped", []string{"/dev/video0"}, false},
	} {
		c := Desktop{State: tc.state, Container: engine.Container{Devices: tc.devices}}
		if got := c.Webcam(); got != tc.want {
			t.Errorf("%s %v: got %v", tc.state, tc.devices, got)
		}
	}
}

// specEngine records the containers it is asked to create.
type specEngine struct {
	engine.Engine
	specs []engine.ContainerSpec
}

func (e *specEngine) Create(_ context.Context, s engine.ContainerSpec) (string, error) {
	e.specs = append(e.specs, s)
	return "id", nil
}

func TestContainerGetsTheHostsCameras(t *testing.T) {
	sys := t.TempDir()
	eng := &specEngine{}
	s := New(eng, Config{DataDir: t.TempDir(), InContainer: false, SysDir: sys}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.createContainer(context.Background(), "alice", "id", "tok", time.Time{}, Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sys, "devices", "virtual", "video4linux", "video0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.createContainer(context.Background(), "bob", "id", "tok", time.Time{}, Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if len(eng.specs) != 2 || len(eng.specs[0].Devices) != 0 || !slices.Equal(eng.specs[1].Devices, []string{"/dev/video0"}) {
		t.Fatalf("%+v", eng.specs)
	}
}

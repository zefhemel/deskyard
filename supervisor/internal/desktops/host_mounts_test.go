package desktops

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestValidateHostMounts(t *testing.T) {
	for _, mounts := range [][]HostMount{
		nil, {}, {{Source: "/srv/photos", Target: "/mnt/photos", ReadOnly: true}},
		{{Source: "/Users/alice/My files", Target: "/mnt/project files"}},
	} {
		if err := ValidateHostMounts(mounts); err != nil {
			t.Fatalf("%+v: %v", mounts, err)
		}
	}
	for _, mounts := range [][]HostMount{
		{{Source: "relative", Target: "/mnt/photos"}},
		{{Source: "/tmp/../etc", Target: "/mnt/photos"}},
		{{Source: "/tmp", Target: "relative"}},
		{{Source: "/tmp", Target: "/mnt"}},
		{{Source: "/tmp", Target: "/mnt/../etc"}},
		{{Source: "/tmp", Target: "/workspace/data"}},
		{{Source: "/tmp", Target: "/etc"}},
		{{Source: "/tmp", Target: "/shared/files"}},
		{{Source: "/tmp\x00", Target: "/mnt/files"}},
		{{Source: "/tmp", Target: "/mnt/files\x00"}},
		{{Source: "/tmp", Target: "/mnt/files"}, {Source: "/srv", Target: "/mnt/files"}},
		{{Source: "/tmp", Target: "/mnt/files"}, {Source: "/srv", Target: "/mnt/files/nested"}},
	} {
		if ValidateHostMounts(mounts) == nil {
			t.Fatalf("accepted %+v", mounts)
		}
	}
}

// Only the engine boundary is simulated: source validation reads real folders,
// and service container state/labels survive through the existing fake engine.
type mountEngine struct {
	*fakeEngine
	specs     map[string]engine.ContainerSpec
	stops     int
	failImage string
}

func (e *mountEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	if s.Image == e.failImage && e.failImage != "" {
		return "", errors.New("create failed")
	}
	for _, m := range s.Mounts {
		if m.MustExist {
			if _, err := os.Stat(m.Source); err != nil {
				return "", err
			}
		}
	}
	id, err := e.fakeEngine.Create(ctx, s)
	if err == nil {
		e.specs[id] = s
	}
	return id, err
}
func (e *mountEngine) Stop(ctx context.Context, id string, d time.Duration) error {
	e.stops++
	return e.fakeEngine.Stop(ctx, id, d)
}
func (e *mountEngine) Exec(_ context.Context, id string, _ engine.ExecSpec) (*engine.Process, error) {
	code := 0
	for _, m := range e.specs[id].Mounts {
		st, err := os.Stat(m.Source)
		if err != nil || !st.IsDir() {
			code = 1
		}
	}
	return &engine.Process{Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(nil), Wait: func() (int, error) { return code, nil }, Close: func() {}}, nil
}
func mountService(t *testing.T) (*Service, *mountEngine) {
	s, base, _, _ := newCameraService(t, true)
	eng := &mountEngine{fakeEngine: base, specs: map[string]engine.ContainerSpec{}}
	s.Eng = eng
	return s, eng
}
func TestHostMountsSurviveRecreation(t *testing.T) {
	s, eng := mountService(t)
	ctx := context.Background()
	source := t.TempDir()
	mounts := []HostMount{{Source: source, Target: "/mnt/photos", ReadOnly: true}}
	created := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	if err := s.createContainer(ctx, "alice", "stable-id", "token", created, Settings{Image: "img", Mounts: mounts}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Settings.Mounts, mounts) {
		t.Fatalf("lost mounts: %+v", c.Settings)
	}
	spec := eng.specs["deskyard-alice"]
	if len(spec.Mounts) != 2 || spec.Mounts[1].Source != source || spec.Mounts[1].Target != "/mnt/photos" || !spec.Mounts[1].ReadOnly || !spec.Mounts[1].MustExist {
		t.Fatalf("%+v", spec.Mounts)
	}
	set := c.Settings
	set.Image = "new-image"
	c, err = s.Recreate(ctx, "alice", set, func(string, float64) {})
	if err != nil {
		t.Fatal(err)
	}
	if c.Running() || c.ID != "stable-id" || c.Container.Env[TokenEnv] != "token" || !c.Created.Equal(created) || !reflect.DeepEqual(c.Settings.Mounts, mounts) {
		t.Fatalf("%+v", c)
	}
	set.Mounts = []HostMount{}
	if _, err = s.Recreate(ctx, "alice", set, func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	if len(eng.specs["deskyard-alice"].Mounts) != 1 {
		t.Fatal("detach retained mount")
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatal("detach removed source", err)
	}
}
func TestInvalidHostSourcesLeaveRunningDesktopUntouched(t *testing.T) {
	for _, file := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "file"}[file], func(t *testing.T) {
			s, eng := mountService(t)
			ctx := context.Background()
			if err := s.createContainer(ctx, "alice", "stable-id", "token", time.Time{}, Settings{Image: "img"}); err != nil {
				t.Fatal(err)
			}
			if err := eng.Start(ctx, "deskyard-alice"); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "source")
			if file {
				if err := os.WriteFile(source, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.Recreate(ctx, "alice", Settings{Image: "img", Mounts: []HostMount{{Source: source, Target: "/mnt/data"}}}, func(string, float64) {})
			if err == nil {
				t.Fatal("accepted invalid source")
			}
			c, _ := s.Get(ctx, "alice")
			if !c.Running() || eng.stops != 0 || c.ID != "stable-id" {
				t.Fatal("preflight disrupted desktop")
			}
			for id := range eng.cs {
				if strings.HasPrefix(id, "deskyard-mount-check-") {
					t.Fatal("helper leaked", id)
				}
			}
			if !file {
				if _, err = os.Stat(source); !os.IsNotExist(err) {
					t.Fatal("missing source was created")
				}
			}
		})
	}
}
func TestHostMountRollbackRetainsOldConfiguration(t *testing.T) {
	s, eng := mountService(t)
	ctx := context.Background()
	source := t.TempDir()
	old := []HostMount{{Source: source, Target: "/mnt/old", ReadOnly: true}}
	if err := s.createContainer(ctx, "alice", "id", "token", time.Time{}, Settings{Image: "img", Mounts: old}); err != nil {
		t.Fatal(err)
	}
	eng.failImage = "broken"
	if _, err := s.Recreate(ctx, "alice", Settings{Image: "broken", Mounts: []HostMount{{Source: source, Target: "/mnt/new"}}}, func(string, float64) {}); err == nil {
		t.Fatal("expected failure")
	}
	c, err := s.Get(ctx, "alice")
	if err != nil || !reflect.DeepEqual(c.Settings.Mounts, old) {
		t.Fatalf("rollback: %+v %v", c, err)
	}
}
func TestCorruptHostMountLabelCannotSilentlyDropMounts(t *testing.T) {
	s, eng := mountService(t)
	ctx := context.Background()
	if err := s.createContainer(ctx, "alice", "id", "token", time.Time{}, Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	eng.cs["deskyard-alice"].Labels[LabelHostMounts] = "not-json"
	_, err := s.Recreate(ctx, "alice", Settings{Image: "img"}, func(string, float64) {})
	if err == nil || eng.stops != 0 {
		t.Fatal("corrupt mount settings were ignored", err)
	}
}

func TestHostMountFailureRestartsPreviousRunningDesktop(t *testing.T) {
	s, eng := mountService(t)
	ctx := context.Background()
	source := t.TempDir()
	old := []HostMount{{Source: source, Target: "/mnt/old", ReadOnly: true}}
	if err := s.createContainer(ctx, "alice", "id", "token", time.Time{}, Settings{Image: "img", Mounts: old}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(ctx, "deskyard-alice"); err != nil {
		t.Fatal(err)
	}
	eng.failImage = "broken"
	if _, err := s.Recreate(ctx, "alice", Settings{Image: "broken", Mounts: old}, func(string, float64) {}); err == nil {
		t.Fatal("expected failure")
	}
	c, err := s.Get(ctx, "alice")
	if err != nil || !c.Running() || !reflect.DeepEqual(c.Settings.Mounts, old) {
		t.Fatalf("running rollback: %+v %v", c, err)
	}
}

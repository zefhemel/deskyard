package computers

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

// hostVolume saves a host volume of name at path and returns a grant of it.
func hostVolume(t *testing.T, s *Service, name, path, at string, readOnly bool) VolumeGrant {
	t.Helper()
	if _, ok := s.Vols.Get(name); !ok {
		if err := s.Vols.Add(Volume{Name: name, Created: time.Now().UTC(), Kind: VolumeHost, HostPath: path}); err != nil {
			t.Fatal(err)
		}
	}
	return VolumeGrant{Name: name, Path: at, ReadOnly: readOnly}
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
	s, base := newFakeService(t)
	eng := &mountEngine{fakeEngine: base, specs: map[string]engine.ContainerSpec{}}
	s.Eng = eng
	return s, eng
}
func TestHostVolumesSurviveRecreation(t *testing.T) {
	s, eng := mountService(t)
	ctx := context.Background()
	source := t.TempDir()
	mounts := []VolumeGrant{hostVolume(t, s, "photos", source, "/mnt/photos", true)}
	if err := s.seed(ctx, "alice", Settings{Image: "img", Volumes: mounts}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Settings.Volumes, mounts) {
		t.Fatalf("lost mounts: %+v", c.Settings)
	}
	created, token := c.Created, c.Container.Env[TokenEnv]
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
	if c.Running() || c.ID != "alice" || token == "" || c.Container.Env[TokenEnv] != token || !c.Created.Equal(created) || !reflect.DeepEqual(c.Settings.Volumes, mounts) {
		t.Fatalf("%+v", c)
	}
	set.Volumes = []VolumeGrant{}
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
func TestInvalidHostSourcesLeaveRunningComputerUntouched(t *testing.T) {
	for _, file := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "file"}[file], func(t *testing.T) {
			s, eng := mountService(t)
			ctx := context.Background()
			if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
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
			_, err := s.Recreate(ctx, "alice", Settings{Image: "img", Volumes: []VolumeGrant{hostVolume(t, s, "data", source, "/mnt/data", false)}}, func(string, float64) {})
			if err == nil {
				t.Fatal("accepted invalid source")
			}
			c, _ := s.Get(ctx, "alice")
			if !c.Running() || eng.stops != 0 || c.ID != "alice" {
				t.Fatal("preflight disrupted computer")
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
func TestHostVolumeRollbackRetainsOldConfiguration(t *testing.T) {
	s, eng := mountService(t)
	ctx := context.Background()
	source := t.TempDir()
	old := []VolumeGrant{hostVolume(t, s, "data", source, "/mnt/old", true)}
	if err := s.seed(ctx, "alice", Settings{Image: "img", Volumes: old}); err != nil {
		t.Fatal(err)
	}
	eng.failImage = "broken"
	if _, err := s.Recreate(ctx, "alice", Settings{Image: "broken", Volumes: []VolumeGrant{{Name: "data", Path: "/mnt/new"}}}, func(string, float64) {}); err == nil {
		t.Fatal("expected failure")
	}
	// The old container is back with its mounts; the definition stays ahead.
	c, err := s.Get(ctx, "alice")
	if err != nil || c.ImageID != "img" || c.AppliedRevision != 1 || c.Revision != 2 || c.Error == "" {
		t.Fatalf("rollback: %+v %v", c, err)
	}
	if m := eng.specs["deskyard-alice"].Mounts; len(m) != 2 || m[1].Target != "/mnt/old" || !m[1].ReadOnly {
		t.Fatalf("restored container's mounts: %+v", m)
	}
}
func TestHostVolumeFailureRestartsPreviousRunningComputer(t *testing.T) {
	s, eng := mountService(t)
	ctx := context.Background()
	source := t.TempDir()
	old := []VolumeGrant{hostVolume(t, s, "data", source, "/mnt/old", true)}
	if err := s.seed(ctx, "alice", Settings{Image: "img", Volumes: old}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(ctx, "deskyard-alice"); err != nil {
		t.Fatal(err)
	}
	eng.failImage = "broken"
	if _, err := s.Recreate(ctx, "alice", Settings{Image: "broken", Volumes: old}, func(string, float64) {}); err == nil {
		t.Fatal("expected failure")
	}
	c, err := s.Get(ctx, "alice")
	if err != nil || !c.Running() || !reflect.DeepEqual(c.Settings.Volumes, old) {
		t.Fatalf("running rollback: %+v %v", c, err)
	}
}

func TestCreateContainerMountsGrantPaths(t *testing.T) {
	s, eng := mountService(t)
	ctx := context.Background()
	if err := s.Vols.Add(Volume{Name: "local", Created: time.Now().UTC(), Kind: VolumeLocal}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.Layout.VolumeOf("local"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	grants := []VolumeGrant{{Name: "local"}, hostVolume(t, s, "photos", source, "/mnt/p", true)}
	if err := s.seed(ctx, "alice", Settings{Image: "img", Volumes: grants}); err != nil {
		t.Fatal(err)
	}
	spec := eng.specs["deskyard-alice"]
	want := []engine.Mount{
		{Source: s.Layout.HomeOf("alice"), Target: "/workspace", MustExist: true},
		{Source: s.Layout.VolumeOf("local"), Target: "/share/local", MustExist: true},
		{Source: source, Target: "/mnt/p", ReadOnly: true, MustExist: true},
	}
	if !reflect.DeepEqual(spec.Mounts, want) || !spec.NoNewPrivileges {
		t.Fatalf("mounts %+v nnp=%v", spec.Mounts, spec.NoNewPrivileges)
	}
}

func TestMissingHostVolumeIsComputerError(t *testing.T) {
	s, _ := mountService(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "gone")
	_, err := s.Recreate(ctx, "alice", Settings{Image: "img", Volumes: []VolumeGrant{hostVolume(t, s, "photos", gone, "", false)}}, noProgress)
	if err == nil || !strings.Contains(err.Error(), "photos") || !strings.Contains(err.Error(), gone) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatal("missing host folder was created")
	}
}

// Starting a computer whose host volume folder has gone says which
// volume and folder, rather than the engine's bind error.
func TestStartMissingHostVolumeNamesIt(t *testing.T) {
	s, eng := mountService(t)
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "usb")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.seed(ctx, "alice", Settings{Image: "img", Volumes: []VolumeGrant{hostVolume(t, s, "usb", source, "/mnt/usb", false)}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	err := s.Start(ctx, "alice")
	if err == nil || !strings.Contains(err.Error(), "usb") || !strings.Contains(err.Error(), source) {
		t.Fatalf("got %v", err)
	}
	if c, _ := eng.Inspect(ctx, "deskyard-alice"); c.Running() {
		t.Fatal("started anyway")
	}
}

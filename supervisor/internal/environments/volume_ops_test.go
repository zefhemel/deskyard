package environments

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// helperEngine plays the root helper container: an exec in it runs an
// install -d or rm -rf on the host folder its mount maps.
type helperEngine struct {
	*fakeEngine
	hmu     sync.Mutex
	helpers []engine.ContainerSpec
	// code and stderr are every helper command's outcome.
	code   int
	stderr string
}

func (e *helperEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	if s.Image == helper.Image {
		e.hmu.Lock()
		e.helpers = append(e.helpers, s)
		e.hmu.Unlock()
	}
	return e.fakeEngine.Create(ctx, s)
}

func (e *helperEngine) Exec(ctx context.Context, id string, x engine.ExecSpec) (*engine.Process, error) {
	e.hmu.Lock()
	var spec engine.ContainerSpec
	for _, s := range e.helpers {
		if s.Name == id {
			spec = s
		}
	}
	e.hmu.Unlock()
	host := func(p string) string {
		for _, m := range spec.Mounts {
			if rest, ok := strings.CutPrefix(p, m.Target+"/"); ok {
				return filepath.Join(m.Source, rest)
			}
		}
		return ""
	}
	switch target := x.Cmd[len(x.Cmd)-1]; x.Cmd[0] {
	case "install":
		_ = os.MkdirAll(host(target), 0o755)
	case "rm":
		_ = os.RemoveAll(host(target))
	}
	return &engine.Process{Stdout: strings.NewReader(""), Stderr: strings.NewReader(e.stderr), Wait: func() (int, error) { return e.code, nil }, Close: func() {}}, nil
}

func (e *helperEngine) ran() []engine.ContainerSpec {
	e.hmu.Lock()
	defer e.hmu.Unlock()
	return slices.Clone(e.helpers)
}

func volumeService(t *testing.T) (*Service, *helperEngine) {
	t.Helper()
	eng := &helperEngine{fakeEngine: newFakeEngine()}
	s, err := Open(eng, Config{DataDir: t.TempDir()}, "test", slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Layout.Prepare(); err != nil {
		t.Fatal(err)
	}
	return s, eng
}

// grantDefinition saves an environment definition granting volumes.
func grantDefinition(t *testing.T, s *Service, name string, grants ...VolumeGrant) {
	t.Helper()
	def := withSettings(Definition{Name: name, Created: time.Now().UTC(), Power: PowerStopped}, Settings{Image: "img"}, "img")
	def.Volumes = grants
	if err := s.Defs.Put(def); err != nil {
		t.Fatal(err)
	}
}

func TestCreateVolumeMakesFolder(t *testing.T) {
	s, eng := volumeService(t)
	v, adopted, err := s.CreateVolume(context.Background(), "photos", "")
	if err != nil {
		t.Fatal(err)
	}
	if adopted || v.Name != "photos" || v.Kind != VolumeLocal || v.Created.IsZero() {
		t.Fatalf("%+v adopted=%v", v, adopted)
	}
	if st, err := os.Stat(s.Layout.VolumeOf("photos")); err != nil || !st.IsDir() {
		t.Fatalf("no folder: %v", err)
	}
	if got, ok := s.Vols.Get("photos"); !ok || got != v {
		t.Fatalf("not saved: %+v", got)
	}
	reloaded, err := LoadVolumes(volumesPath(s.Layout), s.Layout)
	if err != nil || len(reloaded.List()) != 1 {
		t.Fatalf("not on disk: %v %v", reloaded, err)
	}
	if len(eng.ran()) != 0 {
		t.Fatalf("helpers: %+v", eng.ran())
	}
	if len(eng.cs) != 0 {
		t.Fatalf("helper left behind: %v", eng.cs)
	}
}

// A local volume's folder is the supervisor's to make: no helper container
// is created, and the folder has mode 0755.
func TestMakeVolumeFolderWithoutHelper(t *testing.T) {
	eng := &noCreateEngine{fakeEngine: newFakeEngine()}
	s, err := Open(eng, Config{DataDir: t.TempDir()}, "test", slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Layout.Prepare(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateVolume(context.Background(), "photos", ""); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(s.Layout.VolumeOf("photos"))
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
		t.Fatalf("folder %v %v", st, err)
	}
}

// noCreateEngine fails any Create.
type noCreateEngine struct{ *fakeEngine }

func (e *noCreateEngine) Create(context.Context, engine.ContainerSpec) (string, error) {
	return "", errors.New("no container expected")
}

func TestCreateVolumeAdoptsFolder(t *testing.T) {
	s, eng := volumeService(t)
	kept := filepath.Join(s.Layout.VolumeOf("photos"), "kept.txt")
	if err := os.MkdirAll(filepath.Dir(kept), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, adopted, err := s.CreateVolume(context.Background(), "photos", "")
	if err != nil {
		t.Fatal(err)
	}
	if !adopted {
		t.Fatal("an existing folder was not adopted")
	}
	if len(eng.ran()) != 0 {
		t.Fatalf("a helper ran over an adopted folder: %+v", eng.ran())
	}
	if b, err := os.ReadFile(kept); err != nil || string(b) != "old" {
		t.Fatalf("kept file changed: %q %v", b, err)
	}
	if st, _ := os.Stat(filepath.Dir(kept)); st.Mode().Perm() != 0o700 {
		t.Fatalf("adopted folder's mode changed: %v", st.Mode())
	}
}

func TestCreateVolumeInvalidOrDuplicate(t *testing.T) {
	s, eng := volumeService(t)
	ctx := context.Background()
	_, _, err := s.CreateVolume(ctx, "A", "")
	if err == nil || errors.Is(err, ErrExists) || errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid name: %v", err)
	}
	if _, err := os.Stat(s.Layout.VolumeOf("A")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid name made a folder: %v", err)
	}
	if _, _, err := s.CreateVolume(ctx, "photos", ""); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(s.Layout.VolumeOf("photos"), "data")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(eng.ran())
	if _, _, err := s.CreateVolume(ctx, "photos", ""); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if len(eng.ran()) != before {
		t.Fatal("a duplicate ran a helper")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a duplicate touched the folder: %v", err)
	}
}

// A failed directory sync still saves the volume, so its new folder stays.
func TestCreateVolumeKeepsFolderWhenSyncFails(t *testing.T) {
	s, _ := volumeService(t)
	syncVolumesDir = func(string) error { return errors.New("sync failed") }
	t.Cleanup(func() { syncVolumesDir = syncDirectory })
	if _, _, err := s.CreateVolume(context.Background(), "photos", ""); err == nil {
		t.Fatal("the sync error was lost")
	}
	if _, ok := s.Vols.Get("photos"); !ok {
		t.Fatal("the written volume was not kept")
	}
	if st, err := os.Stat(s.Layout.VolumeOf("photos")); err != nil || !st.IsDir() {
		t.Fatalf("the saved volume's folder was removed: %v", err)
	}
}

func TestDeleteVolumeInUse(t *testing.T) {
	s, _ := volumeService(t)
	ctx := context.Background()
	if _, _, err := s.CreateVolume(ctx, "photos", ""); err != nil {
		t.Fatal(err)
	}
	grantDefinition(t, s, "alice", VolumeGrant{Name: "photos", ReadOnly: true})
	grantDefinition(t, s, "bob")
	err := s.DeleteVolume(ctx, "photos", true)
	var inUse *VolumeInUseError
	if !errors.As(err, &inUse) || !reflect.DeepEqual(inUse.Environments, []string{"alice"}) || !errors.Is(err, ErrConflict) {
		t.Fatalf("%v", err)
	}
	if _, ok := s.Vols.Get("photos"); !ok {
		t.Fatal("entry deleted")
	}
	if _, err := os.Stat(s.Layout.VolumeOf("photos")); err != nil {
		t.Fatalf("folder deleted: %v", err)
	}
	if users := s.VolumeUsers("photos"); !reflect.DeepEqual(users, []VolumeUser{{Environment: "alice", ReadOnly: true}}) {
		t.Fatalf("users %+v", users)
	}
}

// A container that still mounts the volume, such as one kept by a failed
// replacement after its definition dropped the grant, blocks the delete: the
// files a running desktop has bind-mounted must not be removed.
func TestDeleteVolumeMountedByContainer(t *testing.T) {
	s, eng := volumeService(t)
	ctx := context.Background()
	if _, _, err := s.CreateVolume(ctx, "photos", ""); err != nil {
		t.Fatal(err)
	}
	grantDefinition(t, s, "alice") // the grant is already removed
	// alice's running container, and bob's parked one mid-replacement.
	for env, name := range map[string]string{"alice": containerName("alice"), "bob": parkedName("bob")} {
		if _, err := eng.Create(ctx, engine.ContainerSpec{Name: name, Image: "img", Labels: s.ownLabels(env),
			Mounts: []engine.Mount{{Source: s.Layout.VolumeOf("photos"), Target: "/share/photos", MustExist: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	eng.mu.Lock()
	eng.cs[containerName("alice")].State = "running"
	eng.mu.Unlock()
	err := s.DeleteVolume(ctx, "photos", true)
	var inUse *VolumeInUseError
	if !errors.Is(err, ErrConflict) || errors.As(err, &inUse) || !strings.Contains(err.Error(), "alice, bob") {
		t.Fatalf("%v", err)
	}
	if _, ok := s.Vols.Get("photos"); !ok {
		t.Fatal("entry deleted")
	}
	if _, err := os.Stat(s.Layout.VolumeOf("photos")); err != nil {
		t.Fatalf("folder deleted: %v", err)
	}
	// Another supervisor's container, or one mounting another folder, doesn't count.
	for _, id := range []string{containerName("alice"), parkedName("bob")} {
		if err := eng.Remove(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	other := map[string]string{LabelManaged: "true", LabelName: "carol", LabelInstance: "other"}
	if _, err := eng.Create(ctx, engine.ContainerSpec{Name: "deskyard-carol", Image: "img", Labels: other,
		Mounts: []engine.Mount{{Source: s.Layout.VolumeOf("photos"), Target: "/share/photos"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Create(ctx, engine.ContainerSpec{Name: containerName("dave"), Image: "img", Labels: s.ownLabels("dave"),
		Mounts: []engine.Mount{{Source: s.Layout.VolumeOf("photos2"), Target: "/share/photos2"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVolume(ctx, "photos", false); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteVolumeKeepsFilesByDefault(t *testing.T) {
	s, _ := volumeService(t)
	ctx := context.Background()
	if _, _, err := s.CreateVolume(ctx, "photos", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVolume(ctx, "photos", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Vols.Get("photos"); ok {
		t.Fatal("entry kept")
	}
	if _, err := os.Stat(s.Layout.VolumeOf("photos")); err != nil {
		t.Fatalf("files not kept: %v", err)
	}
	if err := s.DeleteVolume(ctx, "photos", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting it again: %v", err)
	}
}

func TestDeleteVolumeWithFiles(t *testing.T) {
	s, _ := volumeService(t)
	ctx := context.Background()
	if _, _, err := s.CreateVolume(ctx, "photos", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Layout.VolumeOf("photos"), "a"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVolume(ctx, "photos", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Vols.Get("photos"); ok {
		t.Fatal("entry kept")
	}
	if _, err := os.Stat(s.Layout.VolumeOf("photos")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder kept: %v", err)
	}
	if _, err := os.Stat(s.Layout.Volume); err != nil {
		t.Fatalf("volume/ itself went: %v", err)
	}
}

func TestDefinitionsRefuseGrantOfUnknownVolume(t *testing.T) {
	s, _ := volumeService(t)
	def := withSettings(Definition{Name: "alice", Created: time.Now().UTC(), Power: PowerStopped}, Settings{Image: "img"}, "img")
	def.Volumes = []VolumeGrant{{Name: "ghost"}}
	if err := s.Defs.Put(def); err == nil {
		t.Fatal("granted a volume that doesn't exist")
	}
	if _, ok := s.Defs.Get("alice"); ok {
		t.Fatal("saved anyway")
	}
}

func TestDeleteVolumeRacesGrant(t *testing.T) {
	s, _ := volumeService(t)
	ctx := context.Background()
	grantDefinition(t, s, "alice")
	for i := 0; i < 100; i++ {
		if _, _, err := s.CreateVolume(ctx, "photos", ""); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = s.DeleteVolume(ctx, "photos", false)
		}()
		go func() {
			defer wg.Done()
			_, _ = s.Defs.Update("alice", func(d *Definition) error {
				d.Volumes = []VolumeGrant{{Name: "photos"}}
				return nil
			})
		}()
		wg.Wait()
		_, exists := s.Vols.Get("photos")
		granted := len(s.VolumeUsers("photos")) > 0
		if granted && !exists {
			t.Fatalf("round %d: alice was granted a deleted volume", i)
		}
		// Reset: drop the grant, then the volume.
		if _, err := s.Defs.Update("alice", func(d *Definition) error { d.Volumes = nil; return nil }); err != nil {
			t.Fatal(err)
		}
		if exists {
			if err := s.DeleteVolume(ctx, "photos", false); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCreateHostVolumeChecksPath(t *testing.T) {
	s, eng := mountService(t)
	if err := s.Layout.Prepare(); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	v, adopted, err := s.CreateVolume(context.Background(), "photos", source)
	if err != nil {
		t.Fatal(err)
	}
	if adopted || v.Kind != VolumeHost || v.HostPath != source {
		t.Fatalf("%+v adopted=%v", v, adopted)
	}
	if _, err := os.Stat(s.Layout.VolumeOf("photos")); !os.IsNotExist(err) {
		t.Fatal("a host volume made a folder in the data folder")
	}
	checked := false
	for _, spec := range eng.specs {
		if strings.HasPrefix(spec.Name, "deskyard-mount-check-") && len(spec.Mounts) == 1 && spec.Mounts[0].Source == source && spec.Mounts[0].MustExist {
			checked = true
		}
	}
	if !checked {
		t.Fatalf("no mount check of %s: %+v", source, eng.specs)
	}
	gone := filepath.Join(t.TempDir(), "gone")
	if _, _, err := s.CreateVolume(context.Background(), "missing", gone); err == nil {
		t.Fatal("created a host volume of a missing folder")
	}
	if _, ok := s.Vols.Get("missing"); ok {
		t.Fatal("saved a host volume whose check failed")
	}
	if _, _, err := s.CreateVolume(context.Background(), "rel", "relative/path"); err == nil {
		t.Fatal("accepted a relative host path")
	}
}

func TestDeleteHostVolumeKeepsFolder(t *testing.T) {
	s, _ := volumeService(t)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	hostVolume(t, s, "photos", source, "", false)
	if err := s.DeleteVolume(context.Background(), "photos", true); err == nil {
		t.Fatal("deleted the files of a host volume")
	}
	if _, ok := s.Vols.Get("photos"); !ok {
		t.Fatal("a refused delete forgot the volume")
	}
	if err := s.DeleteVolume(context.Background(), "photos", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(source, "keep")); err != nil {
		t.Fatal("forgetting a host volume touched its folder")
	}
}

func TestDeleteVolumeRefusedWhileItemBusy(t *testing.T) {
	s, _ := volumeService(t)
	if _, _, err := s.CreateVolume(context.Background(), "photos", ""); err != nil {
		t.Fatal(err)
	}
	busy := errors.New("a backup of volume photos is running")
	s.ItemBusy = func(kind, name string) error {
		if kind == "volume" && name == "photos" {
			return busy
		}
		return nil
	}
	if err := s.DeleteVolume(context.Background(), "photos", true); !errors.Is(err, busy) {
		t.Fatalf("got %v", err)
	}
	if _, ok := s.Vols.Get("photos"); !ok {
		t.Fatal("deleted while busy")
	}
}

func TestRemoveRefusedWhileItemBusy(t *testing.T) {
	s, _ := mountService(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	busy := errors.New("a backup of alice is running")
	s.ItemBusy = func(kind, name string) error {
		if kind == "environment" && name == "alice" {
			return busy
		}
		return nil
	}
	if err := s.Remove(ctx, "alice", true); !errors.Is(err, busy) {
		t.Fatalf("got %v", err)
	}
	if _, ok := s.Defs.Get("alice"); !ok {
		t.Fatal("removed while busy")
	}
}

// CreateVolume refuses a host path inside the host data folder's entries.
func TestCreateHostVolumeRefusesHostDataEntries(t *testing.T) {
	s, _ := mountService(t)
	if err := s.Layout.Prepare(); err != nil {
		t.Fatal(err)
	}
	host := t.TempDir()
	if err := os.MkdirAll(filepath.Join(host, "home", "bob"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Layout = s.Layout.WithHost(host)
	if _, _, err := s.CreateVolume(context.Background(), "bob", filepath.Join(host, "home", "bob")); err == nil {
		t.Fatal("created a host volume of another environment's home")
	}
	if _, _, err := s.CreateVolume(context.Background(), "top", filepath.Join(host, "home")); err == nil {
		t.Fatal("created a host volume of the data folder's homes")
	}
}

// What the supervisor can't delete itself goes to the root helper, whose
// failure is reported with its message.
func TestRemoveTreeReportsHelperFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root deletes the read-only folder itself")
	}
	s, eng := volumeService(t)
	root := t.TempDir()
	stuck := filepath.Join(root, "x", "ro")
	if err := os.MkdirAll(stuck, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stuck, "f"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(stuck, 0o555)
	t.Cleanup(func() { _ = os.Chmod(stuck, 0o755) })
	eng.code, eng.stderr = 1, "rm: can't remove '/parent/x/ro/f': Permission denied\n"
	err := s.RemoveTree(context.Background(), root, "x")
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("got %v", err)
	}
	ran := eng.ran()
	if len(ran) != 1 || ran[0].Mounts[0].Source != root || ran[0].Network != "none" || ran[0].Labels[helper.LabelHelper] != helper.Folders {
		t.Fatalf("helpers %+v", ran)
	}
	for _, bad := range []string{"", ".", "..", "a/b"} {
		if err := s.RemoveTree(context.Background(), root, bad); err == nil {
			t.Errorf("removed %q", bad)
		}
	}
}

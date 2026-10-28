package environments

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// shellHelperEngine runs a helper's sh -c commands for real, with its one
// mount's target replaced by the source folder.
type shellHelperEngine struct {
	*fakeEngine
	mu    sync.Mutex
	specs map[string]engine.ContainerSpec
	runs  int
}

func (e *shellHelperEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	e.mu.Lock()
	e.specs[s.Name] = s
	e.mu.Unlock()
	return e.fakeEngine.Create(ctx, s)
}

func (e *shellHelperEngine) Exec(ctx context.Context, id string, x engine.ExecSpec) (*engine.Process, error) {
	e.mu.Lock()
	m := e.specs[id].Mounts[0]
	e.runs++
	e.mu.Unlock()
	args := append([]string(nil), x.Cmd...)
	args[2] = strings.ReplaceAll(args[2], m.Target, m.Source)
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr = &out, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, err
		}
		code = exit.ExitCode()
	}
	return &engine.Process{Stdout: &out, Stderr: &stderr, Wait: func() (int, error) { return code, nil }, Close: func() {}}, nil
}

func shellHelperService(t *testing.T) (*Service, *shellHelperEngine) {
	s, base := newFakeService(t)
	e := &shellHelperEngine{fakeEngine: base, specs: map[string]engine.ContainerSpec{}}
	s.Eng = e
	return s, e
}

// Each call takes a name nothing has, by a mkdir that fails on a taken one:
// restoring twice into a host volume never reuses the first one's folder.
func TestMakeFreeFolderTakesFreshNames(t *testing.T) {
	for _, onHost := range []bool{false, true} {
		t.Run(map[bool]string{false: "data folder", true: "engine host"}[onHost], func(t *testing.T) {
			s, e := shellHelperService(t)
			parent := Folder{Path: t.TempDir(), OnEngineHost: onHost}
			// A file holds the second name.
			if err := os.WriteFile(filepath.Join(parent.Path, "Restored x (2)"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			var got []string
			for range 3 {
				name, err := s.MakeFreeFolder(context.Background(), parent, "Restored x")
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, name)
				if st, err := os.Stat(filepath.Join(parent.Path, name)); err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
					t.Fatalf("%s: %v %v", name, st, err)
				}
			}
			if strings.Join(got, ",") != "Restored x,Restored x (3),Restored x (4)" {
				t.Fatalf("names %q", got)
			}
			if want := map[bool]int{false: 0, true: 3}[onHost]; e.runs != want {
				t.Fatalf("helpers %d, want %d", e.runs, want)
			}
			if onHost {
				for _, spec := range e.specs {
					if spec.Labels[helper.LabelHelper] != helper.Folders || spec.Network != "none" || spec.Mounts[0].Source != parent.Path || !spec.Mounts[0].MustExist {
						t.Fatalf("helper %+v", spec)
					}
				}
			}
		})
	}
}

func TestMakeFreeFolderConcurrent(t *testing.T) {
	s, _ := shellHelperService(t)
	parent := Folder{Path: t.TempDir()}
	names := make(chan string, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			name, err := s.MakeFreeFolder(context.Background(), parent, "Restored")
			if err != nil {
				t.Error(err)
			}
			names <- name
		})
	}
	wg.Wait()
	close(names)
	seen := map[string]bool{}
	for n := range names {
		if seen[n] {
			t.Fatalf("%s handed out twice", n)
		}
		seen[n] = true
	}
}

func TestMakeFreeFolderFailures(t *testing.T) {
	s, _ := shellHelperService(t)
	for _, bad := range []string{"", "..", "a/b"} {
		if _, err := s.MakeFreeFolder(context.Background(), Folder{Path: t.TempDir()}, bad); err == nil {
			t.Errorf("made %q", bad)
		}
	}
	if os.Geteuid() != 0 {
		ro := t.TempDir()
		_ = os.Chmod(ro, 0o555)
		t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
		for _, onHost := range []bool{false, true} {
			if _, err := s.MakeFreeFolder(context.Background(), Folder{Path: ro, OnEngineHost: onHost}, "x"); err == nil || !strings.Contains(err.Error(), "ermission denied") {
				t.Errorf("on host %v: %v", onHost, err)
			}
		}
	}
}

// The supervisor never deletes on the engine host, nor reads a host
// volume's path as if it were its own.
func TestEngineHostFoldersStayOutOfReach(t *testing.T) {
	s, _ := shellHelperService(t)
	host := t.TempDir()
	if err := os.WriteFile(filepath.Join(host, "a"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(host, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTree(context.Background(), Folder{Path: host, OnEngineHost: true}, "x"); !errors.Is(err, ErrConflict) {
		t.Fatalf("removed on the engine host: %v", err)
	}
	hostVolume(t, s, "photos", host, "/mnt/photos", false)
	if bytes, complete := s.VolumeSize("photos"); bytes != 0 || complete {
		t.Fatalf("sized a folder on the engine host: %d %v", bytes, complete)
	}
	// A link at the host path, as the supervisor sees it, says nothing of
	// the engine host: only a mount of the path itself is the volume's.
	other := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	hostVolume(t, s, "linked", link, "/mnt/linked", false)
	v, _ := s.Vols.Get("linked")
	for name, source := range map[string]string{"bob": other, "carol": link} {
		if _, err := s.Eng.Create(context.Background(), engine.ContainerSpec{Name: containerName(name), Labels: s.ownLabels(name), Mounts: []engine.Mount{{Source: source, Target: "/mnt"}}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.volumeMounters(context.Background(), v)
	if err != nil || strings.Join(got, ",") != "carol" {
		t.Fatalf("mounters %v %v", got, err)
	}
}

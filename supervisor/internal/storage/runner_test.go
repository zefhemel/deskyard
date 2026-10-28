package storage

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// helperFakeEngine records what an EngineRunner asks of the engine; exec
// prints out and exits with code.
type helperFakeEngine struct {
	engine.Engine // unexpected calls panic
	mu            sync.Mutex
	specs         []engine.ContainerSpec
	execs         []engine.ExecSpec
	copied        map[string]string // container → dir restic was copied into
	removed       []string
	pulled        []string
	out           string
	code          int
	execErr       error
}

func (e *helperFakeEngine) ImageInspect(_ context.Context, ref string) (engine.Image, error) {
	return engine.Image{}, engine.ErrNotFound
}
func (e *helperFakeEngine) Pull(_ context.Context, ref string, _ func(engine.PullProgress)) error {
	e.pulled = append(e.pulled, ref)
	return nil
}
func (e *helperFakeEngine) Create(_ context.Context, s engine.ContainerSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.specs = append(e.specs, s)
	return s.Name, nil
}
func (e *helperFakeEngine) CopyFromImage(_ context.Context, ref, path string) (io.ReadCloser, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "restic", Mode: 0o755, Size: 4})
	_, _ = tw.Write([]byte("ELF!"))
	_ = tw.Close()
	if ref != ResticImage || path != "/usr/bin/restic" {
		return nil, errors.New("wrong restic source")
	}
	return io.NopCloser(&buf), nil
}
func (e *helperFakeEngine) CopyToContainer(_ context.Context, id, dir string, _ io.Reader) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.copied == nil {
		e.copied = map[string]string{}
	}
	e.copied[id] = dir
	return nil
}
func (e *helperFakeEngine) Start(context.Context, string) error { return nil }
func (e *helperFakeEngine) List(_ context.Context, labels map[string]string) ([]engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []engine.Container
	for _, s := range e.specs {
		match := true
		for k, v := range labels {
			if s.Labels[k] != v {
				match = false
			}
		}
		if match {
			out = append(out, engine.Container{ID: s.Name, Name: s.Name, Labels: s.Labels})
		}
	}
	return out, nil
}
func (e *helperFakeEngine) Exec(_ context.Context, _ string, s engine.ExecSpec) (*engine.Process, error) {
	e.mu.Lock()
	e.execs = append(e.execs, s)
	e.mu.Unlock()
	if e.execErr != nil {
		return nil, e.execErr
	}
	return &engine.Process{Stdout: strings.NewReader(e.out), Stderr: strings.NewReader("restic: some warning\n"), Wait: func() (int, error) { return e.code, nil }, Close: func() {}}, nil
}
func (e *helperFakeEngine) Remove(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.removed = append(e.removed, id)
	return nil
}

func TestEngineRunnerSpecCarriesNoSecrets(t *testing.T) {
	e := &helperFakeEngine{out: "listing\n", code: 3}
	r := &EngineRunner{Helpers: &helper.Runner{Eng: e}}
	mounts := []engine.Mount{{Source: "/data/secret/remotes/nas", Target: RemoteMount, ReadOnly: true}}
	env := map[string]string{"RCLONE_CONFIG": RemoteMount + "/rclone.conf"}
	var out bytes.Buffer
	res, err := r.Run(context.Background(), Call{Mounts: mounts, Env: env, Args: []string{"rclone", "lsf", "nas:"}, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 3 || out.String() != "listing\n" || !strings.Contains(res.Stderr, "some warning") {
		t.Fatalf("result %+v out %q", res, out.String())
	}
	if len(e.specs) != 1 {
		t.Fatalf("specs %+v", e.specs)
	}
	s := e.specs[0]
	if s.Image != RcloneImage || !strings.HasPrefix(s.Name, "deskyard-storage-") || len(s.Env) != 0 || !s.NoHealthcheck ||
		len(s.Mounts) != 1 || s.Mounts[0] != mounts[0] || strings.Join(s.Entrypoint, " ") != "sleep" {
		t.Fatalf("spec %+v", s)
	}
	if e.copied[s.Name] != "/usr/local/bin" {
		t.Fatalf("restic not copied in: %v", e.copied)
	}
	if len(e.execs) != 1 || strings.Join(e.execs[0].Cmd, " ") != "rclone lsf nas:" || e.execs[0].User != "0" ||
		len(e.execs[0].Env) != 1 || e.execs[0].Env[0] != "RCLONE_CONFIG="+RemoteMount+"/rclone.conf" {
		t.Fatalf("exec %+v", e.execs)
	}
	if len(e.removed) != 1 || e.removed[0] != s.Name {
		t.Fatalf("helper not removed: %v", e.removed)
	}
	if strings.Join(e.pulled, ",") != ResticImage+","+RcloneImage {
		t.Fatalf("pulled %v", e.pulled)
	}
}

func TestEngineRunnerRemovesHelperWhenExecFails(t *testing.T) {
	e := &helperFakeEngine{execErr: errors.New("exec broke")}
	if _, err := (&EngineRunner{Helpers: &helper.Runner{Eng: e}}).Run(context.Background(), Call{Args: []string{"restic", "version"}}); err == nil {
		t.Fatal("no error")
	}
	if len(e.removed) != 1 {
		t.Fatalf("helper left behind: %v", e.removed)
	}
}

func TestEngineRunnerMountsCachedRestic(t *testing.T) {
	e := &helperFakeEngine{}
	bin := t.TempDir()
	r := &EngineRunner{Helpers: &helper.Runner{Eng: e}, BinDir: bin}
	for range 2 {
		if _, err := r.Run(context.Background(), Call{Args: []string{"restic", "version"}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.copied) != 0 {
		t.Fatalf("copied restic into a container: %v", e.copied)
	}
	cached := filepath.Join(bin, "restic-0.19.1")
	if b, err := os.ReadFile(cached); err != nil || string(b) != "ELF!" {
		t.Fatalf("cached binary %q %v", b, err)
	}
	if st, _ := os.Stat(cached); st.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", st.Mode())
	}
	for _, s := range e.specs {
		if len(s.Mounts) != 1 || s.Mounts[0] != (engine.Mount{Source: cached, Target: "/usr/local/bin/restic", ReadOnly: true, MustExist: true}) {
			t.Fatalf("mounts %+v", s.Mounts)
		}
	}
}

// Storage helpers are labelled as this supervisor's, so ones a crash left
// behind are removed before its first helper, and other supervisors' are
// left alone.
func TestEngineRunnerRemovesStaleHelpers(t *testing.T) {
	e := &helperFakeEngine{}
	e.specs = append(e.specs,
		engine.ContainerSpec{Name: "deskyard-storage-left", Labels: map[string]string{"deskyard.helper": "storage", "deskyard.instance": "aaa"}},
		engine.ContainerSpec{Name: "deskyard-storage-other", Labels: map[string]string{"deskyard.helper": "storage", "deskyard.instance": "bbb"}})
	mine := &EngineRunner{Helpers: &helper.Runner{Eng: e, Instance: "aaa"}}
	if _, err := mine.Run(context.Background(), Call{Args: []string{"restic", "version"}}); err != nil {
		t.Fatal(err)
	}
	run := e.specs[len(e.specs)-1]
	if l := run.Labels; l["deskyard.helper"] != "storage" || l["deskyard.instance"] != "aaa" {
		t.Fatalf("labels %v", l)
	}
	if strings.Join(e.removed, ",") != "deskyard-storage-left,"+run.Name {
		t.Fatalf("removed %v", e.removed)
	}
}

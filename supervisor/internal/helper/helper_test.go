package helper

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// fakeEngine records helpers; an exec prints out and exits with code.
type fakeEngine struct {
	engine.Engine // unexpected calls panic
	mu            sync.Mutex
	images        map[string]bool
	pulled        []string
	cs            map[string]engine.ContainerSpec
	order         []string
	execs         []engine.ExecSpec
	removed       []string
	out, stderr   string
	code          int
	removeErr     error
}

func newFake() *fakeEngine {
	return &fakeEngine{images: map[string]bool{}, cs: map[string]engine.ContainerSpec{}}
}

func (e *fakeEngine) ImageInspect(_ context.Context, ref string) (engine.Image, error) {
	if e.images[ref] {
		return engine.Image{ID: ref}, nil
	}
	return engine.Image{}, engine.ErrNotFound
}
func (e *fakeEngine) Pull(_ context.Context, ref string, _ func(engine.PullProgress)) error {
	e.pulled = append(e.pulled, ref)
	e.images[ref] = true
	return nil
}
func (e *fakeEngine) Create(_ context.Context, s engine.ContainerSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cs[s.Name] = s
	e.order = append(e.order, "create")
	return s.Name, nil
}
func (e *fakeEngine) Start(context.Context, string) error {
	e.order = append(e.order, "start")
	return nil
}
func (e *fakeEngine) List(_ context.Context, labels map[string]string) ([]engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []engine.Container
next:
	for _, s := range e.cs {
		for k, v := range labels {
			if s.Labels[k] != v {
				continue next
			}
		}
		out = append(out, engine.Container{ID: s.Name, Name: s.Name, Labels: s.Labels})
	}
	return out, nil
}
func (e *fakeEngine) Exec(_ context.Context, _ string, s engine.ExecSpec) (*engine.Process, error) {
	e.execs = append(e.execs, s)
	e.order = append(e.order, "exec")
	return &engine.Process{Stdout: strings.NewReader(e.out), Stderr: strings.NewReader(e.stderr), Wait: func() (int, error) { return e.code, nil }, Close: func() {}}, nil
}
func (e *fakeEngine) Remove(ctx context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	e.removed = append(e.removed, id)
	delete(e.cs, id)
	return e.removeErr
}

func TestRunLabelsExecsAndRemoves(t *testing.T) {
	e := newFake()
	e.out, e.stderr, e.code = "made\n", "first\nmkdir: denied\n", 3
	r := &Runner{Eng: e, Instance: "aaa"}
	var out bytes.Buffer
	prepared := ""
	res, err := r.Run(context.Background(), Spec{
		Purpose: Folders, Network: "none", Cmd: []string{"mkdir", "/x"}, Env: []string{"A=1"}, Stdout: &out,
		Mounts: []engine.Mount{{Source: "/data/home", Target: "/parent", MustExist: true}},
		Prepare: func(_ context.Context, id string) error {
			prepared = id
			e.order = append(e.order, "prepare")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 3 || out.String() != "made\n" {
		t.Fatalf("result %+v out %q", res, out.String())
	}
	if err := res.Err("making x"); err == nil || err.Error() != "making x: mkdir: denied" {
		t.Fatalf("err %v", err)
	}
	if (Result{}).Err("x") != nil {
		t.Fatal("exit 0 is an error")
	}
	if len(e.removed) != 1 || !strings.HasPrefix(e.removed[0], "deskyard-folders-") || prepared != e.removed[0] {
		t.Fatalf("removed %v, prepared %q", e.removed, prepared)
	}
	if strings.Join(e.order, ",") != "create,prepare,start,exec" {
		t.Fatalf("order %v", e.order)
	}
	if strings.Join(e.pulled, ",") != Image {
		t.Fatalf("pulled %v", e.pulled)
	}
	x := e.execs[0]
	if x.User != "0" || strings.Join(x.Cmd, " ") != "mkdir /x" || strings.Join(x.Env, " ") != "A=1" {
		t.Fatalf("exec %+v", x)
	}
}

func TestRunJoinsRemovalError(t *testing.T) {
	e := newFake()
	e.removeErr = errors.New("engine gone")
	_, err := (&Runner{Eng: e}).Run(context.Background(), Spec{Purpose: Storage, Cmd: []string{"true"}})
	if !errors.Is(err, e.removeErr) {
		t.Fatalf("got %v", err)
	}
}

// Removal never inherits the caller's cancellation, so a canceled command
// leaves no helper behind.
func TestRunRemovesAfterCancel(t *testing.T) {
	e := newFake()
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{Eng: e}
	_, _ = r.Run(ctx, Spec{Purpose: Storage, Cmd: []string{"true"}, Prepare: func(context.Context, string) error {
		cancel()
		return context.Canceled
	}})
	if len(e.removed) != 1 || len(e.cs) != 0 {
		t.Fatalf("removed %v, left %v", e.removed, e.cs)
	}
}

// Before its first helper a runner removes this instance's helpers a crash
// left behind, of every purpose, and no one else's containers.
func TestRemoveStaleOnFirstRun(t *testing.T) {
	e := newFake()
	e.images[Image] = true
	for name, labels := range map[string]map[string]string{
		"deskyard-storage-left":     {LabelHelper: Storage, LabelInstance: "aaa"},
		"deskyard-mount-check-left": {LabelHelper: MountCheck, LabelInstance: "aaa"},
		"deskyard-storage-other":    {LabelHelper: Storage, LabelInstance: "bbb"},
		"deskyard-reclaim":          {LabelHelper: "memory-reclaim", LabelInstance: "aaa"},
		"deskyard-alice":            {"deskyard.managed": "true", LabelInstance: "aaa"},
	} {
		e.cs[name] = engine.ContainerSpec{Name: name, Labels: labels}
	}
	r := &Runner{Eng: e, Instance: "aaa"}
	for range 2 {
		if _, err := r.Run(context.Background(), Spec{Purpose: Folders, Cmd: []string{"true"}}); err != nil {
			t.Fatal(err)
		}
	}
	stale := 0
	for _, id := range e.removed {
		if strings.HasSuffix(id, "-left") {
			stale++
		} else if !strings.HasPrefix(id, "deskyard-folders-") {
			t.Fatalf("removed %s", id)
		}
	}
	if stale != 2 || len(e.removed) != 4 || len(e.pulled) != 0 {
		t.Fatalf("removed %v, pulled %v", e.removed, e.pulled)
	}
}

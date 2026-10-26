package environments

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

func claim(e *dockerAccessEngine, container, name, instance, state string) {
	e.cs[container] = &engine.Container{ID: container, Name: container, State: state,
		Labels: map[string]string{LabelManaged: "true", LabelName: name, LabelRevision: "1", LabelInstance: instance}}
}

// Unknown and duplicate containers of this instance are reported and left as
// they are; another instance's are not this one's to report. A duplicate
// blocks the environment's lifecycle until it is resolved.
func TestReconcileReportsConflictsAndLeavesThem(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	claim(e, "deskyard-ghost", "ghost", "instance-a", "running")
	claim(e, "alice-copy", "alice", "instance-a", "exited")
	claim(e, "deskyard-carol", "carol", "instance-b", "exited")
	rep := s.ReconcileOnce(ctx)
	want := []Conflict{
		{Name: "alice", Reason: ConflictDuplicate, Containers: []string{"alice-copy", "deskyard-alice"}},
		{Name: "ghost", Reason: ConflictUnknown, Containers: []string{"deskyard-ghost"}},
	}
	if !slices.EqualFunc(rep.Conflicts, want, func(a, b Conflict) bool {
		return a.Name == b.Name && a.Reason == b.Reason && slices.Equal(a.Containers, b.Containers)
	}) {
		t.Fatalf("conflicts %+v, want %+v", rep.Conflicts, want)
	}
	for name, state := range map[string]string{"deskyard-ghost": "running", "alice-copy": "exited", "deskyard-carol": "exited", "deskyard-alice": "running"} {
		if c, err := e.Inspect(ctx, name); err != nil || c.State != state {
			t.Fatalf("%s was touched: %+v %v", name, c, err)
		}
	}
	got, err := s.Get(ctx, "alice")
	if err != nil || got.Conflict == "" {
		t.Fatalf("duplicate not shown: %+v %v", got, err)
	}
	if list, _ := s.List(ctx); len(list) != 1 || list[0].Conflict != got.Conflict {
		t.Fatalf("list: %+v", list)
	}
	for op, call := range map[string]func() error{
		"start":  func() error { return s.Start(ctx, "alice") },
		"stop":   func() error { return s.Stop(ctx, "alice") },
		"remove": func() error { return s.Remove(ctx, "alice", false) },
		"apply": func() error {
			_, err := s.Recreate(ctx, "alice", Settings{Image: "img", MemoryBytes: 1 << 30}, noProgress)
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s with a duplicate: %v", op, err)
		}
	}
	if d, ok := s.Defs.Get("alice"); !ok || d.Power != PowerRunning || d.Revision != 1 {
		t.Fatalf("blocked operations changed the definition: %+v", d)
	}
	// Resolved by removing the duplicate.
	delete(e.cs, "alice-copy")
	rep = s.ReconcileOnce(ctx)
	if len(rep.Conflicts) != 1 || rep.Conflicts[0].Name != "ghost" {
		t.Fatalf("after resolving: %+v", rep.Conflicts)
	}
	if err := s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileRestartsExitedRunningEnvironment(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	for _, name := range []string{"alice", "bob"} {
		if _, err := s.Create(ctx, name, Settings{Image: "img"}, noProgress); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Stop(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	e.cs["deskyard-alice"].State = "exited" // crashed
	if rep := s.ReconcileOnce(ctx); rep.Err() != nil {
		t.Fatal(rep.Err())
	}
	if c, _ := e.Inspect(ctx, "deskyard-alice"); !c.Running() {
		t.Fatal("a crashed environment that should run was not restarted")
	}
	if c, _ := e.Inspect(ctx, "deskyard-bob"); c.Running() {
		t.Fatal("a stopped environment was started")
	}
}

// A container the engine lost is recreated from the definition when the
// environment should run.
func TestReconcileRecreatesLostContainer(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	source := t.TempDir()
	created, err := s.Create(ctx, "alice", Settings{Image: "img", MemoryBytes: 1 << 30, Mounts: []HostMount{{Source: source, Target: "/mnt/data"}}}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	delete(e.cs, "deskyard-alice")
	fresh := reopen(t, s)
	if err := fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := e.Inspect(ctx, "deskyard-alice")
	if err != nil || !c.Running() || c.Env[TokenEnv] != created.Container.Env[TokenEnv] || c.Labels[LabelRevision] != "1" {
		t.Fatalf("not recreated: %+v %v", c, err)
	}
	if spec := e.specs["deskyard-alice"]; spec.Image != "sha256:img" || spec.MemoryBytes != 1<<30 || len(spec.Mounts) != 2 || spec.Mounts[1].Source != source {
		t.Fatalf("recreated with other settings: %+v", spec)
	}
}

// A definition whose home is gone is an error; recovery never makes a home,
// and doesn't retry by itself.
func TestReconcileMissingHomeIsAnError(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	delete(e.cs, "deskyard-alice")
	if err := os.RemoveAll(s.Layout.HomeOf("alice")); err != nil {
		t.Fatal(err)
	}
	delete(e.specs, "deskyard-alice")
	if rep := s.ReconcileOnce(ctx); rep.Err() == nil || !strings.Contains(rep.Err().Error(), "home") {
		t.Fatalf("missing home not reported: %v", rep.Err())
	}
	if got, _ := s.Get(ctx, "alice"); got.Error == "" || got.Container.ID != "" {
		t.Fatalf("environment: %+v", got)
	}
	s.ReconcileOnce(ctx)
	if _, err := os.Stat(s.Layout.HomeOf("alice")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reconcile made a home")
	}
	if _, created := e.specs["deskyard-alice"]; created {
		t.Fatal("a container was created without a home")
	}
}

// An existing container whose home is gone is not started: the engine would
// make an empty home. Any environment without its home shows the error, which
// clears once the folder is back.
func TestReconcileMissingHomeOfExistingContainer(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	for _, name := range []string{"alice", "bob"} {
		if _, err := s.Create(ctx, name, Settings{Image: "img"}, noProgress); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Stop(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	if m := e.specs["deskyard-alice"].Mounts[0]; m.Target != "/workspace" || !m.MustExist {
		t.Fatalf("home mount isn't strict: %+v", m)
	}
	e.cs["deskyard-alice"].State = "exited"
	for _, name := range []string{"alice", "bob"} {
		if err := os.RemoveAll(s.Layout.HomeOf(name)); err != nil {
			t.Fatal(err)
		}
	}
	rep := s.ReconcileOnce(ctx)
	if len(rep.Errors) != 2 || !strings.Contains(rep.Err().Error(), "home") {
		t.Fatalf("missing homes not reported: %v", rep.Err())
	}
	for _, name := range []string{"alice", "bob"} {
		if got, _ := s.Get(ctx, name); got.Error == "" || got.Container.Running() {
			t.Fatalf("%s: %+v", name, got)
		}
		if _, err := os.Stat(s.Layout.HomeOf(name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reconcile made %s's home", name)
		}
	}
	if err := s.Start(ctx, "alice"); err == nil || !strings.Contains(err.Error(), "home") {
		t.Fatalf("started without a home: %v", err)
	}
	if c, _ := e.Inspect(ctx, "deskyard-alice"); c.Running() {
		t.Fatal("the container was started without its home")
	}
	if err := os.MkdirAll(s.Layout.HomeOf("alice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rep := s.ReconcileOnce(ctx); len(rep.Errors) != 1 {
		t.Fatalf("after restoring alice's home: %v", rep.Err())
	}
	if got, _ := s.Get(ctx, "alice"); got.Error != "" || !got.Container.Running() {
		t.Fatalf("restored home: %+v", got)
	}
}

// An exited container behind its definition (a failed change, its error lost
// with a restart, or still recorded) is started as it is; only a start,
// restart or edit applies the definition.
func TestReconcileStartsStaleContainerWithoutApplying(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		s, e, _ := definitionService(t)
		ctx := context.Background()
		if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Defs.Update("alice", func(d *Definition) error {
			d.Limits.MemoryBytes, d.Revision = 1<<30, 2
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if recorded {
			s.setError("alice", "applying failed")
		}
		e.cs["deskyard-alice"].State = "exited"
		if rep := s.ReconcileOnce(ctx); rep.Err() != nil {
			t.Fatal(rep.Err())
		}
		c, _ := e.Inspect(ctx, "deskyard-alice")
		if !c.Running() || c.Labels[LabelRevision] != "1" || e.specs["deskyard-alice"].MemoryBytes != 0 {
			t.Fatalf("recorded %v: %+v", recorded, c)
		}
		if err := s.Restart(ctx, "alice"); err != nil {
			t.Fatal(err)
		}
		if c, _ := e.Inspect(ctx, "deskyard-alice"); c.Labels[LabelRevision] != "2" {
			t.Fatalf("restart did not apply: %+v", c)
		}
	}
}

// A container of an invalid name, or one claiming a defined environment
// under another container name, is a conflict; the latter blocks the
// environment. A busy environment is not judged again (a replacement's two
// containers are no duplicate): it keeps the conflict it had.
func TestReconcileReportsInvalidAndMisnamedClaims(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	delete(e.cs, "deskyard-alice")
	claim(e, "alice-old", "alice", "instance-a", "exited")
	claim(e, "deskyard-Bad_Name", "Bad_Name", "instance-a", "exited")
	rep := s.ReconcileOnce(ctx)
	if len(rep.Conflicts) != 2 || rep.Conflicts[0].Name != "Bad_Name" || rep.Conflicts[0].Reason != ConflictUnknown ||
		rep.Conflicts[1].Name != "alice" || rep.Conflicts[1].Reason != ConflictMisnamed {
		t.Fatalf("conflicts: %+v", rep.Conflicts)
	}
	if err := s.Start(ctx, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("start with a misnamed claim: %v", err)
	}
	if _, err := e.Inspect(ctx, "deskyard-alice"); err == nil {
		t.Fatal("a duplicate container was created")
	}
	release, err := s.Reserve("alice", "applying")
	if err != nil {
		t.Fatal(err)
	}
	claim(e, "deskyard-alice", "alice", "instance-a", "exited")
	if rep := s.ReconcileOnce(ctx); len(rep.Conflicts) != 2 || rep.Conflicts[1].Reason != ConflictMisnamed {
		t.Fatalf("busy environment judged again: %+v", rep.Conflicts)
	}
	release()
	if rep := s.ReconcileOnce(ctx); len(rep.Conflicts) != 2 || rep.Conflicts[1].Reason != ConflictDuplicate {
		t.Fatalf("after the operation: %+v", rep.Conflicts)
	}
}

// A pinned image gone from the engine is pulled again by its reference, as a
// new revision.
func TestReconcilePullsMissingPinnedImage(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	delete(e.cs, "deskyard-alice")
	de := s.Eng.(*definitionEngine)
	de.missing = map[string]bool{"sha256:img": true}
	de.ids = map[string]string{"img": "sha256:img2"}
	var heard [][]Repin
	s.Repinned = func(rs []Repin) { heard = append(heard, rs) }
	rep := s.ReconcileOnce(ctx)
	if rep.Err() != nil {
		t.Fatal(rep.Err())
	}
	def, _ := s.Defs.Get("alice")
	if def.Revision != 2 || def.Image.ID != "sha256:img2" || def.Image.Ref != "img" {
		t.Fatalf("definition: %+v", def)
	}
	if want := []Repin{{Name: "alice", Ref: "img", From: "sha256:img", To: "sha256:img2", Revision: 2}}; !slices.Equal(rep.Repins, want) {
		t.Fatalf("repins %+v, want %+v", rep.Repins, want)
	}
	if len(heard) != 1 || !slices.Equal(heard[0], rep.Repins) {
		t.Fatalf("repins not announced: %+v", heard)
	}
	if again := s.ReconcileOnce(ctx); len(again.Repins) != 0 {
		t.Fatalf("repin reported twice: %+v", again.Repins)
	}
	c, err := e.Inspect(ctx, "deskyard-alice")
	if err != nil || !c.Running() || c.Labels[LabelRevision] != "2" || e.specs["deskyard-alice"].Image != "sha256:img2" {
		t.Fatalf("container: %+v %v", c, err)
	}
}

// Containers of an older Deskyard that use this data folder stop startup;
// other supervisors' and unrelated containers don't.
func TestCheckLegacyContainers(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	data := s.Layout.Data
	mount := func(src string) []engine.Mount { return []engine.Mount{{Source: src, Target: "/workspace"}} }
	e.cs["deskyard-old"] = &engine.Container{ID: "deskyard-old", Name: "deskyard-old", State: "exited",
		Labels: map[string]string{LabelManaged: "true", LabelName: "old", "deskyard.id": "abc"}, Mounts: mount(filepath.Join(data, "old"))}
	e.cs["deskyard-elsewhere"] = &engine.Container{ID: "deskyard-elsewhere", Name: "deskyard-elsewhere",
		Labels: map[string]string{LabelManaged: "true", LabelName: "elsewhere"}, Mounts: mount(t.TempDir())}
	e.cs["deskyard-new"] = &engine.Container{ID: "deskyard-new", Name: "deskyard-new",
		Labels: map[string]string{LabelManaged: "true", LabelName: "new", LabelInstance: "instance-b"}, Mounts: mount(filepath.Join(data, "home", "new"))}
	e.cs["vpn-test"] = &engine.Container{ID: "vpn-test", Name: "vpn-test",
		Labels: map[string]string{vpn.LabelRole: "test"}, Mounts: mount(filepath.Join(data, "state"))}
	e.cs["helper"] = &engine.Container{ID: "helper", Name: "helper",
		Labels: map[string]string{"deskyard.helper": "memory-reclaim"}, Mounts: mount(data)}
	err := s.CheckLegacyContainers(ctx)
	if !errors.Is(err, config.ErrLegacyLayout) || !strings.Contains(err.Error(), "deskyard-old") {
		t.Fatalf("legacy container not refused: %v", err)
	}
	for _, other := range []string{"deskyard-elsewhere", "deskyard-new", "vpn-test", "helper"} {
		if strings.Contains(err.Error(), other) {
			t.Fatalf("%s reported: %v", other, err)
		}
	}
	delete(e.cs, "deskyard-old")
	if err := s.CheckLegacyContainers(ctx); err != nil {
		t.Fatal(err)
	}
}

// A container the engine lost is recreated whatever error the environment
// has, unless its build failed.
func TestReconcileRecreatesLostContainerWithAnError(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "bob", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	s.setError("bob", errNotReady)
	delete(e.cs, "deskyard-bob")
	if rep := s.ReconcileOnce(ctx); rep.Err() != nil {
		t.Fatal(rep.Err())
	}
	if c, err := e.Inspect(ctx, "deskyard-bob"); err != nil || !c.Running() {
		t.Fatalf("lost container not recreated: %+v %v", c, err)
	}
}

// A failed build stays failed, with its error, while the home folder goes
// missing and comes back: reconcile doesn't retry it.
func TestReconcileKeepsFailedBuildAcrossMissingHome(t *testing.T) {
	s, e, p := definitionService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	if err := e.Remove(ctx, "deskyard-alice"); err != nil {
		t.Fatal(err)
	}
	delete(p.nets, "deskyard-alice")
	p.failNetwork = true
	s.ReconcileOnce(ctx)
	failed, _ := s.Get(ctx, "alice")
	if failed.Error == "" {
		t.Fatal("build failure not recorded")
	}
	calls := p.networkCalls
	home := s.Layout.HomeOf("alice")
	if err := os.Rename(home, home+".away"); err != nil {
		t.Fatal(err)
	}
	s.ReconcileOnce(ctx)
	if got, _ := s.Get(ctx, "alice"); !strings.Contains(got.Error, "home") {
		t.Fatalf("missing home not shown: %q", got.Error)
	}
	if err := os.Rename(home+".away", home); err != nil {
		t.Fatal(err)
	}
	s.ReconcileOnce(ctx)
	if p.networkCalls != calls {
		t.Fatal("reconcile retried a failed build")
	}
	if got, _ := s.Get(ctx, "alice"); got.Error != failed.Error {
		t.Fatalf("build error %q lost: %q", failed.Error, got.Error)
	}
}

// A stray container of this instance claiming the name refuses a create,
// which would otherwise make a blocked duplicate.
func TestCreateRefusesClaimedName(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	claim(e, "old-ws", "alice", "instance-a", "exited")
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "old-ws") {
		t.Fatalf("create over a claim: %v", err)
	}
	if _, ok := s.Defs.Get("alice"); ok {
		t.Fatal("definition saved")
	}
	if _, err := e.Inspect(ctx, "deskyard-alice"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("container created: %v", err)
	}
}

// staleListEngine lists a container that is already gone, as a listing taken
// just before a removal finished.
type staleListEngine struct {
	engine.Engine
	stale engine.Container
}

func (e *staleListEngine) List(ctx context.Context, labels map[string]string) ([]engine.Container, error) {
	cs, err := e.Engine.List(ctx, labels)
	return append(cs, e.stale), err
}

func TestReconcileIgnoresClaimRemovedMeanwhile(t *testing.T) {
	s, _, _ := definitionService(t)
	ctx := context.Background()
	s.Eng = &staleListEngine{Engine: s.Eng, stale: engine.Container{ID: "deskyard-erin", Name: "deskyard-erin", State: "exited",
		Labels: map[string]string{LabelManaged: "true", LabelName: "erin", LabelRevision: "1", LabelInstance: "instance-a"}}}
	if rep := s.ReconcileOnce(ctx); len(rep.Conflicts) != 0 {
		t.Fatalf("a removed container reported: %+v", rep.Conflicts)
	}
}

// A pull that stalls doesn't stall reconciliation: the build is cut short and
// recorded as failed.
func TestReconcileBoundsStalledPull(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	delete(e.cs, "deskyard-alice")
	prev := reconcileTimeout
	reconcileTimeout = 50 * time.Millisecond
	t.Cleanup(func() { reconcileTimeout = prev })
	de := s.Eng.(*definitionEngine)
	de.missing = map[string]bool{"sha256:img": true, "img": true}
	de.pull = func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan Report, 1)
	go func() { done <- s.ReconcileOnce(ctx) }()
	select {
	case rep := <-done:
		if !errors.Is(rep.Err(), context.DeadlineExceeded) {
			t.Fatalf("stalled pull: %v", rep.Err())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reconcile stalled on a pull")
	}
	if !s.hasFailedBuild("alice") {
		t.Fatal("cut-short build not recorded as failed")
	}
}

package environments

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// definitionService runs environments on fakes that create networks, check
// host folders and answer readiness.
func definitionService(t *testing.T) (*Service, *dockerAccessEngine, *pairEngine) {
	t.Helper()
	s, p, _, _ := pairSetup(t)
	p.fakeEngine.port = p.port
	e := &dockerAccessEngine{mountEngine: &mountEngine{fakeEngine: p.fakeEngine, specs: map[string]engine.ContainerSpec{}}, socketAvailable: true}
	s.Eng = &definitionEngine{Engine: &vpnDockerAccessEngine{dockerAccessEngine: e, network: p}}
	s.Instance = "instance-a"
	return s, e, p
}

// definitionEngine gives images an ID other than their reference (ids, or
// sha256:<ref>), can lose images (missing) and can fail starts.
type definitionEngine struct {
	engine.Engine
	failStart  bool
	ids        map[string]string
	missing    map[string]bool
	afterStart func(id string)
	onRemove   func(id string)
	pull       func(ctx context.Context, ref string) error
}

func (e *definitionEngine) Pull(ctx context.Context, ref string, progress func(engine.PullProgress)) error {
	if e.pull != nil {
		return e.pull(ctx, ref)
	}
	return e.Engine.Pull(ctx, ref, progress)
}

func (e *definitionEngine) ImageInspect(ctx context.Context, ref string) (engine.Image, error) {
	if e.missing[ref] {
		return engine.Image{}, engine.ErrNotFound
	}
	img, err := e.Engine.ImageInspect(ctx, ref)
	img.ID = "sha256:" + ref
	if id, ok := e.ids[ref]; ok {
		img.ID = id
	}
	return img, err
}
func (e *definitionEngine) Start(ctx context.Context, id string) error {
	if e.failStart {
		return errors.New("start failed")
	}
	if err := e.Engine.Start(ctx, id); err != nil {
		return err
	}
	if e.afterStart != nil {
		e.afterStart(id)
	}
	return nil
}
func (e *definitionEngine) Remove(ctx context.Context, id string) error {
	if e.onRemove != nil {
		e.onRemove(id)
	}
	return e.Engine.Remove(ctx, id)
}

// foreign puts another supervisor's container and network of name on the engine.
func foreign(e *dockerAccessEngine, p *pairEngine, name string) map[string]string {
	labels := map[string]string{LabelManaged: "true", LabelName: name, LabelRevision: "1", LabelInstance: "instance-b"}
	e.cs[containerName(name)] = &engine.Container{ID: containerName(name), Name: containerName(name), State: "exited", Labels: labels}
	if p.nets == nil {
		p.nets = map[string]engine.Network{}
	}
	p.nets[containerName(name)] = engine.Network{ID: containerName(name), Name: containerName(name), Labels: labels}
	return labels
}

func TestStoppedEnvironmentWithoutContainerIsBuiltOnStart(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	source := t.TempDir()
	set := Settings{Image: "img", MemoryBytes: 1 << 30, NanoCPUs: 2e9, Mounts: []HostMount{{Source: source, Target: "/mnt/data", ReadOnly: true}}}
	created, err := s.Create(ctx, "alice", set, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	token := created.Container.Env[TokenEnv]
	if err := s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	// The engine loses the stopped container; a restarted supervisor leaves it
	// stopped and containerless.
	if err := e.Remove(ctx, "deskyard-alice"); err != nil {
		t.Fatal(err)
	}
	fresh := reopen(t, s)
	if err := fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := fresh.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "alice" || list[0].State != "stopped" || list[0].Power != PowerStopped || list[0].Container.ID != "" || list[0].Revision != 1 || list[0].AppliedRevision != 0 {
		t.Fatalf("containerless environment: %+v", list)
	}
	if _, err := e.Inspect(ctx, "deskyard-alice"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("a stopped environment's container was created before start")
	}
	if err := fresh.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	got, err := fresh.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Running() || got.Power != PowerRunning || got.AppliedRevision != got.Revision || got.Container.Env[TokenEnv] != token {
		t.Fatalf("started: %+v", got)
	}
	spec := e.specs["deskyard-alice"]
	if spec.Image != "sha256:img" || spec.MemoryBytes != 1<<30 || spec.NanoCPUs != 2e9 || len(spec.Mounts) != 2 || spec.Mounts[1].Source != source || !spec.Mounts[1].ReadOnly {
		t.Fatalf("rebuilt with other settings: %+v", spec)
	}
}

func TestContainerLabelsOnlyIdentify(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", MemoryBytes: 1 << 30, DockerAccess: true, Mounts: []HostMount{{Source: t.TempDir(), Target: "/mnt/data"}}}, noProgress); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{LabelManaged: "true", LabelName: "alice", LabelRevision: "1", LabelInstance: "instance-a"}
	if got := e.cs["deskyard-alice"].Labels; !maps.Equal(got, want) {
		t.Fatalf("labels %v, want %v", got, want)
	}
	// Another supervisor's container on the same engine is not this one's.
	e.cs["deskyard-bob"] = &engine.Container{ID: "deskyard-bob", Name: "deskyard-bob", State: "running",
		Labels: map[string]string{LabelManaged: "true", LabelName: "bob", LabelRevision: "1", LabelInstance: "instance-b"}}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "alice" || list[0].Conflict != "" {
		t.Fatalf("list: %+v %v", list, err)
	}
}

func TestMemoryReclaimKeepsRevisionAndContainer(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	before, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if !before.MemoryReclaim {
		t.Fatal("memory reclaim is on by default")
	}
	creates := len(e.specs)
	if err := s.SetMemoryReclaim("alice", false); err != nil {
		t.Fatal(err)
	}
	got, _ := reopen(t, s).Get(ctx, "alice")
	if got.MemoryReclaim || got.Revision != before.Revision || got.Container.ID != before.Container.ID || len(e.specs) != creates {
		t.Fatalf("memory reclaim: %+v", got)
	}
	if err := s.SetMemoryReclaim("nobody", true); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestFailedCreateLeavesNoDefinition(t *testing.T) {
	s, e, p := definitionService(t)
	ctx := context.Background()
	p.failNetwork = true
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err == nil {
		t.Fatal("create succeeded without a network")
	}
	if _, ok := s.Defs.Get("alice"); ok {
		t.Fatal("failed create left its definition")
	}
	if defs, err := LoadDefinitions(definitionsPath(s.Layout)); err != nil || len(defs.List()) != 0 {
		t.Fatalf("saved definitions: %v", err)
	}
	if _, err := os.Stat(s.tokenPath("alice")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed create left its proxy token")
	}
	if _, err := e.Inspect(ctx, "deskyard-alice"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("failed create left its container")
	}
	if s.FolderExists("alice") {
		t.Fatal("failed create left the folder it made")
	}
}

func TestFailedCreateRemovesWhatItMade(t *testing.T) {
	for _, fail := range []string{"create", "start"} {
		t.Run(fail, func(t *testing.T) {
			s, e, p := definitionService(t)
			ctx := context.Background()
			if fail == "create" {
				e.failImage = "sha256:img"
			} else {
				s.Eng.(*definitionEngine).failStart = true
			}
			if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err == nil {
				t.Fatal("create succeeded")
			}
			if _, ok := s.Defs.Get("alice"); ok {
				t.Fatal("failed create left its definition")
			}
			if _, err := os.Stat(s.tokenPath("alice")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed create left its proxy token")
			}
			if _, err := e.Inspect(ctx, "deskyard-alice"); !errors.Is(err, engine.ErrNotFound) {
				t.Fatal("failed create left its container")
			}
			if len(p.nets) != 0 {
				t.Fatalf("failed create left its network: %v", p.nets)
			}
		})
	}
}

// A create whose desktop never becomes ready removes what it made: the
// requested grants are never applied to it.
func TestCreateNotReadyRemovesWhatItMade(t *testing.T) {
	s, e, p := definitionService(t)
	ctx := context.Background()
	s.Eng.(*definitionEngine).afterStart = func(id string) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.cs[id].State = "exited"
	}
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err == nil {
		t.Fatal("create succeeded without becoming ready")
	}
	if _, ok := s.Defs.Get("alice"); ok {
		t.Fatal("create that never became ready left its definition")
	}
	if _, err := e.Inspect(ctx, "deskyard-alice"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("create that never became ready left its container")
	}
	if len(p.nets) != 0 || s.FolderExists("alice") {
		t.Fatalf("create that never became ready left its network or folder: %v", p.nets)
	}
}

// The home folder exists by the time the definition is saved: a crash
// between the two would leave a definition that can never start.
func TestCreateMakesHomeBeforeSavingDefinition(t *testing.T) {
	s, _, _ := definitionService(t)
	sync := syncDefinitionsDir
	t.Cleanup(func() { syncDefinitionsDir = sync })
	saved, homeless := false, false
	syncDefinitionsDir = func(dir string) error {
		if _, ok := s.Defs.defs["alice"]; ok { // s.Defs.mu is held
			saved = true
			homeless = homeless || !s.FolderExists("alice")
		}
		return sync(dir)
	}
	if _, err := s.Create(context.Background(), "alice", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	if !saved || homeless {
		t.Fatalf("definition saved %v, without its home %v", saved, homeless)
	}
}

// A removal interrupted after its container is gone, before the definition
// is deleted, never brings the environment back.
func TestInterruptedRemoveDoesNotRecreate(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	good := s.Defs.path
	s.Eng.(*definitionEngine).onRemove = func(string) { s.Defs.path = filepath.Join(blocker, "environments.json") }
	if err := s.Remove(ctx, "alice", false); err == nil {
		t.Fatal("remove succeeded without deleting the definition")
	}
	s.Defs.path = good
	s.Eng.(*definitionEngine).onRemove = nil
	fresh := reopen(t, s)
	fresh.ReconcileOnce(ctx)
	if _, err := e.Inspect(ctx, "deskyard-alice"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("reconcile recreated an environment being removed")
	}
	if d, ok := fresh.Defs.Get("alice"); !ok || d.Power != PowerStopped {
		t.Fatalf("interrupted removal: %+v %v", d, ok)
	}
	if err := fresh.Remove(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
}

// An edit of an environment without a container clears the error of its
// failed build, so reconcile builds it from the edited definition.
func TestEditWithoutContainerClearsBuildError(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	if err := e.Remove(ctx, "deskyard-alice"); err != nil {
		t.Fatal(err)
	}
	s.setError("alice", "building failed")
	if _, err := s.Recreate(ctx, "alice", Settings{Image: "fixed"}, noProgress); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, "alice"); got.Error != "" {
		t.Fatalf("edit kept the old error: %q", got.Error)
	}
	if rep := s.ReconcileOnce(ctx); rep.Err() != nil {
		t.Fatal(rep.Err())
	}
	if c, err := e.Inspect(ctx, "deskyard-alice"); err != nil || !c.Running() || e.specs["deskyard-alice"].Image != "sha256:fixed" {
		t.Fatalf("edited environment not built: %+v %v", c, err)
	}
}

// A build whose network can't be made records the error, so reconcile
// doesn't retry it in a loop.
func TestBuildNetworkFailureIsRecorded(t *testing.T) {
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
	if rep := s.ReconcileOnce(ctx); rep.Err() == nil {
		t.Fatal("network failure not reported")
	}
	if got, _ := s.Get(ctx, "alice"); got.Error == "" {
		t.Fatal("network failure not recorded")
	}
	calls := p.networkCalls
	s.ReconcileOnce(ctx)
	if p.networkCalls != calls {
		t.Fatal("reconcile retried a failed build")
	}
}

// Another supervisor's container and network of the same name are reported
// as a conflict and never removed.
func TestForeignContainerAndNetworkAreLeftAlone(t *testing.T) {
	s, e, p := definitionService(t)
	ctx := context.Background()
	foreign(e, p, "alice")
	// A create fails on the name and leaves the other's network.
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err == nil {
		t.Fatal("created over another supervisor's container")
	}
	delete(e.cs, "deskyard-alice")
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err == nil {
		t.Fatal("created over another supervisor's network")
	}
	if _, ok := p.nets["deskyard-alice"]; !ok {
		t.Fatal("failed create removed another supervisor's network")
	}
	// Defined here while the other supervisor owns the name.
	foreign(e, p, "alice")
	def := withSettings(Definition{Name: "alice", Created: time.Now().UTC(), Power: PowerStopped}, Settings{Image: "img"}, "sha256:img")
	def.Revision = 1
	if err := s.Defs.Put(def); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "alice")
	if err != nil || got.Conflict == "" || got.Container.ID != "" {
		t.Fatalf("get: %+v %v", got, err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].Conflict != got.Conflict || list[0].Container.ID != "" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := s.Start(ctx, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("start: %v", err)
	}
	if d, _ := s.Defs.Get("alice"); d.Power != PowerStopped {
		t.Fatal("a refused start saved power running")
	}
	if err := s.Remove(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Inspect(ctx, "deskyard-alice"); err != nil {
		t.Fatal("removed another supervisor's container")
	}
	if _, ok := p.nets["deskyard-alice"]; !ok {
		t.Fatal("removed another supervisor's network")
	}
}

func TestRemoveKeepingFilesThenCreateReusesHome(t *testing.T) {
	s, e, _ := definitionService(t)
	ctx := context.Background()
	first, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(s.Layout.HomeOf("alice"), "kept.txt")
	if err := os.WriteFile(kept, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed environment still defined", err)
	}
	if _, err := e.Inspect(ctx, "deskyard-alice"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("container kept")
	}
	if _, err := os.Stat(s.tokenPath("alice")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("proxy token kept")
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatal("home not kept", err)
	}
	second, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(kept); err != nil || second.Revision != 1 || !second.Created.After(first.Created) || second.Container.Env[TokenEnv] == first.Container.Env[TokenEnv] {
		t.Fatalf("re-created: %+v %v", second, err)
	}
}

package computers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

type replacementEngine struct {
	*fakeEngine
	created    []engine.ContainerSpec
	failCreate string
	failStart  string
	// exitOnStart: a container of this image exits as soon as it starts.
	exitOnStart string
	failRename  string // renames to this name fail
	afterStart  func()
	onStop      func()
}

func (e *replacementEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	e.created = append(e.created, s)
	if s.Image == e.failCreate {
		return "", errors.New("image creation failed")
	}
	return e.fakeEngine.Create(ctx, s)
}
func (e *replacementEngine) Start(ctx context.Context, id string) error {
	c, err := e.fakeEngine.Inspect(ctx, id)
	if err != nil {
		return err
	}
	if c.Image == e.failStart {
		return errors.New("image startup failed")
	}
	err = e.fakeEngine.Start(ctx, id)
	if c.Image == e.exitOnStart {
		e.mu.Lock()
		e.cs[id].State = "exited"
		e.mu.Unlock()
	}
	if e.afterStart != nil {
		e.afterStart()
	}
	return err
}
func (e *replacementEngine) Stop(ctx context.Context, id string, t time.Duration) error {
	if e.onStop != nil {
		e.onStop()
	}
	return e.fakeEngine.Stop(ctx, id, t)
}
func (e *replacementEngine) Rename(ctx context.Context, id, name string) error {
	if name == e.failRename {
		return errors.New("rename failed")
	}
	return e.fakeEngine.Rename(ctx, id, name)
}

func replacementService(t *testing.T, running bool) (*Service, *replacementEngine) {
	s, base := newFakeService(t)
	state := "exited"
	if running {
		state = "running"
	}
	// The production engine accepts names and IDs; this fake maps by name, so
	// the original's ID is its name.
	base.cs["deskyard-alice"] = &engine.Container{ID: "deskyard-alice", Name: "deskyard-alice", Image: "repo:latest", ImageID: "sha256:old", State: state, IP: base.desktopIP("deskyard-alice"), Labels: map[string]string{LabelManaged: "true", LabelName: "alice", LabelRevision: "1", LabelInstance: ""}, Env: map[string]string{TokenEnv: "original-token"}}
	power := PowerStopped
	if running {
		power = PowerRunning
	}
	def := Definition{Name: "alice", Created: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC), Revision: 1, Image: ImageRef{Ref: "repo:latest", ID: "sha256:old"}, Network: Network{Kind: NetworkIsolated}, MemoryReclaim: true, Power: power}
	if err := s.Defs.Put(def); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteFile(s.tokenPath("alice"), []byte("original-token\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.Layout.HomeOf("alice"), 0o755); err != nil {
		t.Fatal(err)
	}
	eng := &replacementEngine{fakeEngine: base}
	s.Eng = eng
	return s, eng
}

func recoveryFile(s *Service) string { return filepath.Join(s.Layout.State, "recovery", "alice.json") }

// The journal names the parked container and is on disk before the old
// container is stopped.
func TestApplyJournalBeforeStop(t *testing.T) {
	s, e := replacementService(t, true)
	var seen *replacement
	e.onStop = func() {
		if seen != nil {
			return
		}
		var rec replacement
		if err := store.ReadJSON(recoveryFile(s), &rec); err != nil {
			t.Error("no journal when the old container is stopped:", err)
			return
		}
		seen = &rec
	}
	if _, err := s.Recreate(context.Background(), "alice", Settings{Image: "new"}, noProgress); err != nil {
		t.Fatal(err)
	}
	if seen == nil || seen.Parked != parkedName("alice") || seen.Revision != 2 || !seen.WasRunning {
		t.Fatalf("journal at stop: %+v", seen)
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "new" || !c.Container.Running() || c.Revision != 2 || c.AppliedRevision != 2 || c.Error != "" {
		t.Fatalf("applied: %+v", c)
	}
	if _, err := e.Inspect(context.Background(), parkedName("alice")); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("parked container kept after a successful apply", err)
	}
	if _, err := os.Stat(recoveryFile(s)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("journal kept after a successful apply", err)
	}
}

// A new container that never gets ready is removed, and the parked one comes
// back under its name, running. The definition stays ahead, with the error.
func TestApplyFailureRestoresParked(t *testing.T) {
	s, e := replacementService(t, true)
	e.exitOnStart = "new"
	if _, err := s.Recreate(context.Background(), "alice", Settings{Image: "new"}, noProgress); err == nil {
		t.Fatal("apply of a container that never gets ready succeeded")
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "sha256:old" || !c.Container.Running() || c.Container.Env[TokenEnv] != "original-token" {
		t.Fatalf("old container not restored: %+v", c)
	}
	if c.Revision != 2 || c.AppliedRevision != 1 || c.Image != "new" || c.Error == "" {
		t.Fatalf("definition not kept ahead with the error: %+v", c)
	}
	if _, err := e.Inspect(context.Background(), parkedName("alice")); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("parked container left aside", err)
	}
	if _, err := os.Stat(recoveryFile(s)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("journal kept after the old container is back", err)
	}
}

// A failed apply is not retried by reconciliation, only by a start or another edit.
func TestApplyNoRetryLoop(t *testing.T) {
	s, e := replacementService(t, true)
	e.failStart = "new"
	if _, err := s.Recreate(context.Background(), "alice", Settings{Image: "new"}, noProgress); err == nil {
		t.Fatal("replacement should fail")
	}
	created := len(e.created)
	for range 3 {
		if err := s.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.created) != created {
		t.Fatalf("reconcile retried the failed apply: %d creates, was %d", len(e.created), created)
	}
	if c, _ := s.Get(context.Background(), "alice"); c.Revision <= c.AppliedRevision || c.Error == "" || !c.Container.Running() {
		t.Fatalf("after reconcile: %+v", c)
	}
	// A start retries it.
	e.failStart = ""
	if err := s.Start(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Get(context.Background(), "alice"); c.Revision != c.AppliedRevision || c.Error != "" || c.ImageID != "new" || !c.Container.Running() {
		t.Fatalf("start did not apply: %+v", c)
	}
}

func TestRecreateRollbackPinsPreviousImage(t *testing.T) {
	s, e := replacementService(t, false)
	e.failCreate = "new"
	_, err := s.Recreate(context.Background(), "alice", Settings{Image: "new"}, noProgress)
	if err == nil {
		t.Fatal("replacement should fail")
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "sha256:old" || c.AppliedRevision != 1 || c.Container.Running() {
		t.Fatalf("rollback changed the container: %+v", c)
	}
	if c.Container.Env[TokenEnv] != "original-token" || c.ID != "alice" {
		t.Fatal("rollback changed computer identity")
	}
}
func TestRecreateStartupFailureRestoresRunningComputer(t *testing.T) {
	s, e := replacementService(t, true)
	e.failStart = "new"
	_, err := s.Recreate(context.Background(), "alice", Settings{Image: "new"}, noProgress)
	if err == nil {
		t.Fatal("replacement should fail")
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "sha256:old" || !c.Container.Running() {
		t.Fatalf("previous runtime not restored: %+v", c)
	}
}
func TestRecreateCancellationAfterStartRestoresPreviousRuntime(t *testing.T) {
	s, e := replacementService(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.afterStart = cancel
	_, err := s.Recreate(ctx, "alice", Settings{Image: "new"}, noProgress)
	if err == nil {
		t.Fatal("cancelled readiness should fail")
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "sha256:old" || !c.Container.Running() {
		t.Fatalf("cancel prevented recovery: %+v", c)
	}
}
func TestRecreateJournalMustPersistBeforeStopping(t *testing.T) {
	s, e := replacementService(t, true)
	e.failStart = "new"
	if err := os.WriteFile(s.Layout.State, []byte("block state directory"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Recreate(context.Background(), "alice", Settings{Image: "new"}, noProgress)
	if err == nil {
		t.Fatal("journal write failure accepted")
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "sha256:old" || !c.Container.Running() || len(e.created) != 0 {
		t.Fatal("journal failure changed current computer")
	}
}

// writeInterruptedReplacement leaves the journal of a replacement of alice
// to revision 2, as a supervisor that stopped halfway through it would.
func writeInterruptedReplacement(t *testing.T, s *Service, wasRunning bool) {
	t.Helper()
	rec := replacement{Version: 1, Name: "alice", Revision: 2, Parked: parkedName("alice"), WasRunning: wasRunning}
	if err := store.WriteJSON(recoveryFile(s), rec); err != nil {
		t.Fatal(err)
	}
}

// park puts alice's container aside, as an interrupted replacement leaves it.
func park(t *testing.T, e *replacementEngine) {
	t.Helper()
	if err := e.Rename(context.Background(), "deskyard-alice", parkedName("alice")); err != nil {
		t.Fatal(err)
	}
}

// newContainer puts a container built from revision 2 under alice's name.
func newContainer(e *replacementEngine, state string) {
	e.cs["deskyard-alice"] = &engine.Container{ID: "deskyard-alice", Name: "deskyard-alice", Image: "new", ImageID: "new", State: state, IP: e.desktopIP("deskyard-alice"),
		Labels: map[string]string{LabelManaged: "true", LabelName: "alice", LabelRevision: "2", LabelInstance: ""}, Env: map[string]string{TokenEnv: "original-token"}}
}

func TestRecoverParkedAtStartup(t *testing.T) {
	s, e := replacementService(t, true)
	if err := e.Stop(context.Background(), "deskyard-alice", 0); err != nil {
		t.Fatal(err)
	}
	park(t, e)
	writeInterruptedReplacement(t, s, true)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal("interrupted computer missing:", err)
	}
	if c.AppliedRevision != 1 || c.ImageID != "sha256:old" || !c.Container.Running() || c.Container.Env[TokenEnv] != "original-token" {
		t.Fatalf("parked container not restored: %+v", c)
	}
	if _, err := e.Inspect(context.Background(), parkedName("alice")); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("parked container left aside", err)
	}
	if len(e.created) != 0 {
		t.Fatal("recovery created a container instead of restoring the parked one")
	}
	if _, err := os.Stat(recoveryFile(s)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful recovery kept journal", err)
	}
}

// A replacement that was up, its parked container not yet removed, is finished.
func TestStartupFinishesCommittedReplacement(t *testing.T) {
	s, e := replacementService(t, true)
	park(t, e)
	newContainer(e, "running")
	writeInterruptedReplacement(t, s, true)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "new" || c.AppliedRevision != 2 || !c.Container.Running() {
		t.Fatalf("finished replacement not kept: %+v", c)
	}
	if _, err := e.Inspect(context.Background(), parkedName("alice")); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("parked container kept", err)
	}
	if _, err := os.Stat(recoveryFile(s)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("journal kept", err)
	}
}

func TestStartupRemovesPartialReplacementAndRestoresOriginal(t *testing.T) {
	s, e := replacementService(t, true)
	park(t, e)
	newContainer(e, "created") // never started
	writeInterruptedReplacement(t, s, true)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "sha256:old" || c.AppliedRevision != 1 || !c.Container.Running() {
		t.Fatalf("partial replacement survived recovery: %+v", c)
	}
}

// A change rolled back at startup is reported: the definition stays ahead.
func TestStartupRollbackReportsError(t *testing.T) {
	s, e := replacementService(t, true)
	if _, err := s.Defs.Update("alice", func(d *Definition) error { d.Revision = 2; return nil }); err != nil {
		t.Fatal(err)
	}
	park(t, e)
	newContainer(e, "created") // never started
	writeInterruptedReplacement(t, s, true)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil || c.AppliedRevision != 1 || c.Revision != 2 || c.Error != errRolledBack {
		t.Fatalf("rollback not reported: %+v %v", c, err)
	}
}

// An interruption before the old container was parked leaves it where it was.
func TestStartupRecoveryBeforeParking(t *testing.T) {
	s, e := replacementService(t, false)
	writeInterruptedReplacement(t, s, false)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil || c.ImageID != "sha256:old" || c.Container.Running() || len(e.created) != 0 {
		t.Fatalf("%v %+v", err, c)
	}
	if _, err := os.Stat(recoveryFile(s)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("journal kept", err)
	}
}

func TestRecoveryNeverReplacesAnotherComputer(t *testing.T) {
	s, e := replacementService(t, false)
	park(t, e)
	writeInterruptedReplacement(t, s, false)
	e.cs["deskyard-alice"] = &engine.Container{ID: "deskyard-alice", Name: "deskyard-alice", State: "exited",
		Labels: map[string]string{LabelManaged: "true", LabelName: "alice", LabelRevision: "1", LabelInstance: "another-supervisor"}}
	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("identity collision accepted")
	}
	if len(e.created) != 0 || e.cs["deskyard-alice"].Labels[LabelInstance] != "another-supervisor" || e.cs[parkedName("alice")] == nil {
		t.Fatal("recovery touched unrelated computer")
	}
}

// A parked container that isn't this supervisor's is never renamed into place.
func TestRecoveryNeverRestoresAnotherParkedContainer(t *testing.T) {
	s, e := replacementService(t, false)
	park(t, e)
	e.cs[parkedName("alice")].Labels = map[string]string{LabelManaged: "true", LabelName: "alice", LabelInstance: "another-supervisor"}
	writeInterruptedReplacement(t, s, false)
	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("a foreign parked container was accepted")
	}
	if e.cs[parkedName("alice")] == nil || e.cs["deskyard-alice"] != nil {
		t.Fatal("recovery moved a foreign container")
	}
}

func TestFailedRollbackKeepsPrivateRecoveryIntent(t *testing.T) {
	s, e := replacementService(t, false)
	park(t, e)
	writeInterruptedReplacement(t, s, false)
	e.failRename = "deskyard-alice"
	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("failed restore accepted")
	}
	info, err := os.Stat(recoveryFile(s))
	if err != nil {
		t.Fatal("lost recovery intent", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("recovery intent is not private", info.Mode())
	}
	if _, err := s.Reserve("alice", ""); !errors.Is(err, ErrBusy) {
		t.Fatal("partial replacement can be operated on", err)
	}
	e.failRename = ""
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal("retry did not recover", err)
	}
	if c, err := s.Get(context.Background(), "alice"); err != nil || c.ImageID != "sha256:old" {
		t.Fatal(err, c)
	}
}

// Once the old container is back under its name the journal has done its
// job: a failed start is reported, and left to the next start.
func TestRecoveryStartFailureClearsIntent(t *testing.T) {
	s, e := replacementService(t, false)
	park(t, e)
	writeInterruptedReplacement(t, s, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.afterStart = cancel
	var rec replacement
	if err := store.ReadJSON(recoveryFile(s), &rec); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreReplacement(ctx, rec); err == nil {
		t.Fatal("readiness failure accepted")
	}
	if _, err := os.Stat(recoveryFile(s)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("intent kept after the old container is back", err)
	}
	if e.cs["deskyard-alice"] == nil || e.cs["deskyard-alice"].ImageID != "sha256:old" {
		t.Fatal("old container not back under its name")
	}
}

func TestReservationReleaseCannotClearNewOperation(t *testing.T) {
	s, _ := newFakeService(t)
	first, err := s.Reserve("alice", "")
	if err != nil {
		t.Fatal(err)
	}
	first()
	second, err := s.Reserve("alice", "")
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	first()
	if _, err := s.Reserve("alice", ""); !errors.Is(err, ErrBusy) {
		t.Fatal("old release cleared new reservation", err)
	}
}

type recoveryNetworkEngine struct {
	*replacementEngine
	order []string
}

func (e *recoveryNetworkEngine) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	return []engine.Network{{Name: "deskyard-alice", Labels: map[string]string{LabelManaged: "true", LabelName: "alice"}}}, nil
}
func (e *recoveryNetworkEngine) ConnectNetwork(context.Context, string, string, []string) error {
	e.order = append(e.order, "connect")
	return nil
}
func (e *recoveryNetworkEngine) Rename(ctx context.Context, id, name string) error {
	e.order = append(e.order, "rename")
	return e.replacementEngine.Rename(ctx, id, name)
}
func TestStartupReconnectsNetworksBeforeReplacementRecovery(t *testing.T) {
	s, base := replacementService(t, false)
	park(t, base)
	writeInterruptedReplacement(t, s, false)
	eng := &recoveryNetworkEngine{replacementEngine: base}
	s.Eng = eng
	s.Cfg.SelfID = "supervisor"
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(eng.order) < 2 || eng.order[0] != "connect" || eng.order[1] != "rename" {
		t.Fatal("recovery ran before supervisor network reconnection", eng.order)
	}
}

// A request can pause between the journal check and operation reservation while
// the preceding request finishes with a retained journal.
func TestReservationChecksRecoveryAfterAcquiringSlot(t *testing.T) {
	s := New(nil, Config{DataDir: t.TempDir()}, nil)
	s.mu.Lock()
	unlock := sync.OnceFunc(s.mu.Unlock)
	t.Cleanup(unlock)
	done := make(chan error, 1)
	go func() {
		release, err := s.Reserve("alice", "removing")
		if err == nil {
			release()
		}
		done <- err
	}()
	// Wait until the caller reaches Begin's locked mutex, rather than assuming
	// that scheduling the goroutine means it has completed the old stat check.
	deadline := time.Now().Add(time.Second)
	for {
		stack := make([]byte, 1<<20)
		stack = stack[:runtime.Stack(stack, true)]
		parked := false
		for _, trace := range strings.Split(string(stack), "\n\n") {
			if strings.Contains(trace, "TestReservationChecksRecoveryAfterAcquiringSlot.func1") && strings.Contains(trace, "(*Service).Begin(") {
				parked = true
				break
			}
		}
		if parked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reservation did not reach operation lock")
		}
		runtime.Gosched()
	}
	writeInterruptedReplacement(t, s, false)
	unlock()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("pending recovery was bypassed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reservation stayed blocked")
	}
	if _, ok := s.Busy()["alice"]; ok {
		t.Fatal("rejected reservation retained operation slot")
	}
}

func TestReservationStatErrorReleasesSlot(t *testing.T) {
	s := New(nil, Config{DataDir: t.TempDir()}, nil)
	blocked := s.Layout.State
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve("alice", ""); err == nil {
		t.Fatal("unreadable recovery directory accepted")
	}
	if _, ok := s.Busy()["alice"]; ok {
		t.Fatal("failed journal check kept operation reserved")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	release, err := s.Reserve("alice", "")
	if err != nil {
		t.Fatal("retry could not acquire released operation slot", err)
	}
	release()
}

// Grant changes recreate the computer; a failed recreation brings the old
// container back with its mounts, and the definition stays ahead. A grant of
// a volume whose folder is missing is refused before anything is touched.
func TestRecreateKeepsVolumesAndRollsBack(t *testing.T) {
	s, eng := mountService(t)
	ctx := context.Background()
	addVolume(t, s, "photos")
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(ctx, "deskyard-alice"); err != nil {
		t.Fatal(err)
	}
	set := Settings{Image: "img", Volumes: []VolumeGrant{{Name: "photos", ReadOnly: true}}}
	c, err := s.Recreate(ctx, "alice", set, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := volumeMount(eng.specs["deskyard-alice"], "/share/photos")
	if !ok || !m.ReadOnly || c.Revision != 2 || c.AppliedRevision != 2 || len(c.Settings.Volumes) != 1 {
		t.Fatalf("grant not applied: %+v %+v", m, c)
	}
	eng.failImage = "broken"
	set = Settings{Image: "broken", Volumes: []VolumeGrant{{Name: "photos"}}}
	if _, err := s.Recreate(ctx, "alice", set, noProgress); err == nil {
		t.Fatal("expected failure")
	}
	c, err = s.Get(ctx, "alice")
	if err != nil || c.Revision != 3 || c.AppliedRevision != 2 || c.Error == "" || c.Settings.Volumes[0].ReadOnly {
		t.Fatalf("definition not kept ahead: %+v %v", c, err)
	}
	if m, ok := volumeMount(eng.specs["deskyard-alice"], "/share/photos"); !ok || !m.ReadOnly || !c.Running() {
		t.Fatalf("old container not restored: %+v %+v", m, c)
	}
	eng.failImage = ""
	addVolume(t, s, "docs")
	if err := os.Remove(s.Layout.VolumeOf("docs")); err != nil {
		t.Fatal(err)
	}
	stops := eng.stops
	set = Settings{Image: "img", Volumes: []VolumeGrant{{Name: "photos"}, {Name: "docs"}}}
	if _, err := s.Recreate(ctx, "alice", set, noProgress); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("granted a volume without its folder: %v", err)
	}
	if c, _ := s.Get(ctx, "alice"); eng.stops != stops || !c.Running() || c.Revision != 3 || len(c.Settings.Volumes) != 1 {
		t.Fatalf("refused grant disrupted the computer: %+v", c)
	}
}

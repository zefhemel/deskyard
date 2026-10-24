package desktops

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
	afterStart func()
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
	if e.afterStart != nil {
		e.afterStart()
	}
	return err
}
func replacementService(t *testing.T, running bool) (*Service, *replacementEngine) {
	s, base, _, _ := newCameraService(t, true)
	state := "exited"
	if running {
		state = "running"
	}
	base.cs["deskyard-alice"] = &engine.Container{ID: "original", Name: "deskyard-alice", Image: "repo:latest", ImageID: "sha256:old", State: state, LocalPort: base.port, Labels: map[string]string{LabelManaged: "true", LabelName: "alice", LabelID: "stable-id"}, Env: map[string]string{TokenEnv: "original-token"}}
	// The production engine accepts names and IDs; this fake maps by name. Keep
	// its original ID equal to that name so removal has the same observable effect.
	base.cs["deskyard-alice"].ID = "deskyard-alice"
	eng := &replacementEngine{fakeEngine: base}
	s.Eng = eng
	return s, eng
}
func TestRecreateRollbackPinsPreviousImage(t *testing.T) {
	s, e := replacementService(t, false)
	e.failCreate = "new"
	_, err := s.Recreate(context.Background(), "alice", Settings{Image: "new"}, func(string, float64) {})
	if err == nil {
		t.Fatal("replacement should fail")
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "sha256:old" || c.Image != "repo:latest" {
		t.Fatalf("rollback changed image identity: %s %s", c.ImageID, c.Image)
	}
	if c.Container.Env[TokenEnv] != "original-token" || c.ID != "stable-id" {
		t.Fatal("rollback changed desktop identity")
	}
}
func TestRecreateStartupFailureRestoresRunningDesktop(t *testing.T) {
	s, e := replacementService(t, true)
	e.failStart = "new"
	_, err := s.Recreate(context.Background(), "alice", Settings{Image: "new"}, func(string, float64) {})
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
	_, err := s.Recreate(ctx, "alice", Settings{Image: "new"}, func(string, float64) {})
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
	if err := os.WriteFile(filepath.Join(s.Cfg.DataDir, ".deskyard"), []byte("block state directory"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Recreate(context.Background(), "alice", Settings{Image: "new"}, func(string, float64) {})
	if err == nil {
		t.Fatal("journal write failure accepted")
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "sha256:old" || !c.Container.Running() || len(e.created) != 0 {
		t.Fatal("journal failure changed current desktop")
	}
}
func writeInterruptedReplacement(t *testing.T, s *Service) {
	t.Helper()
	rec := map[string]any{"version": 1, "name": "alice", "id": "stable-id", "containerID": "old-container", "imageID": "sha256:old", "token": "original-token", "created": time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC), "wasRunning": false, "settings": Settings{Image: "repo:latest"}}
	if err := store.WriteJSON(filepath.Join(s.Cfg.DataDir, ".deskyard", "recovery", "alice.json"), rec); err != nil {
		t.Fatal(err)
	}
}
func TestReconcileRecoversMissingReplacementRecord(t *testing.T) {
	s, e := replacementService(t, false)
	delete(e.cs, "deskyard-alice")
	writeInterruptedReplacement(t, s)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal("interrupted desktop missing:", err)
	}
	if c.ID != "stable-id" || c.ImageID != "sha256:old" || c.Image != "repo:latest" || c.Container.Running() {
		t.Fatalf("wrong recovered desktop: %+v", c)
	}
	if _, err := os.Stat(filepath.Join(s.Cfg.DataDir, ".deskyard", "recovery", "alice.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful recovery kept journal", err)
	}
}
func TestRecoveryNeverReplacesAnotherDesktop(t *testing.T) {
	s, e := replacementService(t, false)
	writeInterruptedReplacement(t, s)
	e.cs["deskyard-alice"].Labels[LabelID] = "different-id"
	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("identity collision accepted")
	}
	if len(e.created) != 0 || e.cs["deskyard-alice"].Labels[LabelID] != "different-id" {
		t.Fatal("recovery touched unrelated desktop")
	}
}

func TestFailedRollbackKeepsPrivateRecoveryIntent(t *testing.T) {
	s, e := replacementService(t, false)
	e.failCreate = "new"
	// The first failed creation triggers rollback; make the old image unavailable too.
	delete(e.cs, "deskyard-alice")
	writeInterruptedReplacement(t, s)
	e.failCreate = "sha256:old"
	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("failed restore accepted")
	}
	path := filepath.Join(s.Cfg.DataDir, ".deskyard", "recovery", "alice.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal("lost recovery intent", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("recovery intent is not private", info.Mode())
	}
	if _, err := s.Reserve("alice", ""); !errors.Is(err, ErrBusy) {
		t.Fatal("partial replacement can be operated on", err)
	}
	e.failCreate = ""
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal("retry did not recover", err)
	}
	if _, err := s.Get(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
}
func TestReservationReleaseCannotClearNewOperation(t *testing.T) {
	s, _, _, _ := newCameraService(t, true)
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

func TestRecoveryReadinessFailureKeepsIntent(t *testing.T) {
	s, e := replacementService(t, false)
	delete(e.cs, "deskyard-alice")
	writeInterruptedReplacement(t, s)
	path := filepath.Join(s.Cfg.DataDir, ".deskyard", "recovery", "alice.json")
	var rec replacement
	if err := store.ReadJSON(path, &rec); err != nil {
		t.Fatal(err)
	}
	rec.WasRunning = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.afterStart = cancel
	if err := s.restoreReplacement(ctx, rec); err == nil {
		t.Fatal("readiness failure accepted")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("readiness failure discarded recovery intent", err)
	}
}

func TestStartupRemovesPartialReplacementAndRestoresOriginal(t *testing.T) {
	s, e := replacementService(t, false)
	writeInterruptedReplacement(t, s)
	e.cs["deskyard-alice"].Image = "new"
	e.cs["deskyard-alice"].ImageID = "sha256:new"
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageID != "sha256:old" || c.Image != "repo:latest" {
		t.Fatalf("partial replacement survived recovery: %+v", c)
	}
}

type recoveryNetworkEngine struct {
	*replacementEngine
	order []string
}

func (e *recoveryNetworkEngine) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	return []engine.Network{{Name: "deskyard-alice"}}, nil
}
func (e *recoveryNetworkEngine) ConnectNetwork(context.Context, string, string, []string) error {
	e.order = append(e.order, "connect")
	return nil
}
func (e *recoveryNetworkEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	e.order = append(e.order, "create")
	return e.replacementEngine.Create(ctx, s)
}
func TestStartupReconnectsNetworksBeforeReplacementRecovery(t *testing.T) {
	s, base := replacementService(t, false)
	delete(base.cs, "deskyard-alice")
	writeInterruptedReplacement(t, s)
	eng := &recoveryNetworkEngine{replacementEngine: base}
	s.Eng = eng
	s.Cfg.InContainer = true
	s.Cfg.SelfID = "supervisor"
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(eng.order) == 0 || eng.order[0] != "connect" {
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
	writeInterruptedReplacement(t, s)
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
	blocked := filepath.Join(s.Cfg.DataDir, ".deskyard")
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

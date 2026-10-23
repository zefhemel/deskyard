package vpn

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type ambiguousRuntimeEngine struct {
	runtimeEngine
	cancel                context.CancelFunc
	collision, failRemove bool
}

func (e *ambiguousRuntimeEngine) Create(_ context.Context, s engine.ContainerSpec) (string, error) {
	labels := map[string]string{}
	for k, v := range s.Labels {
		labels[k] = v
	}
	if e.collision {
		labels["deskyard.vpn.attempt"] = "unrelated-attempt"
	}
	e.containers = append(e.containers, engine.Container{ID: "recorded-before-error", Name: s.Name, Labels: labels})
	e.cancel()
	return "", context.Canceled
}
func (e *ambiguousRuntimeEngine) Remove(ctx context.Context, id string) error {
	if e.failRemove {
		return errors.New("secret engine cleanup error")
	}
	return e.runtimeEngine.Remove(ctx, id)
}
func TestEnsureReconcilesAmbiguousCreationByAttemptOwnership(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned-create-recorded-before-cancellation", true: "unrelated-name-collision"}[collision], func(t *testing.T) {
			m, _, c := managerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := &ambiguousRuntimeEngine{cancel: cancel, collision: collision}
			m.eng = e
			_, err := m.Ensure(ctx, strings.Repeat("9", 32), "alice", "desktop", c.ID, nil, false)
			if err == nil {
				t.Fatal("ambiguous creation succeeded")
			}
			if !collision && len(e.containers) != 0 {
				t.Fatal("creation response ambiguity leaked owned runtime")
			}
			if collision && (len(e.containers) != 1 || len(e.removed) != 0) {
				t.Fatal("cleanup deleted an unrelated name collision")
			}
		})
	}
}
func TestEnsureReportsSanitizedCleanupFailure(t *testing.T) {
	m, _, c := managerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &ambiguousRuntimeEngine{cancel: cancel, failRemove: true}
	m.eng = e
	_, err := m.Ensure(ctx, strings.Repeat("a", 32), "alice", "desktop", c.ID, nil, false)
	if err == nil || !strings.Contains(err.Error(), "cleanup") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("cleanup failure not sanitized/reported: %v", err)
	}
}

type ambiguousNetworkEngine struct {
	candidateEngine
	cancel    context.CancelFunc
	collision bool
}

func (e *ambiguousNetworkEngine) CreateNetwork(ctx context.Context, name, subnet string, labels map[string]string) error {
	copy := map[string]string{}
	for k, v := range labels {
		copy[k] = v
	}
	if e.collision {
		copy["deskyard.vpn.attempt"] = "unrelated-attempt"
	}
	_ = e.candidateEngine.CreateNetwork(ctx, name, subnet, copy)
	e.cancel()
	return context.Canceled
}
func TestCandidateReconcilesAmbiguousNetworkCreationByAttemptOwnership(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned-create-recorded-before-cancellation", true: "unrelated-name-collision"}[collision], func(t *testing.T) {
			m, _, c := managerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := &ambiguousNetworkEngine{cancel: cancel, collision: collision}
			m.eng = e
			_, err := m.TestSaved(ctx, c.ID, Input{Name: "candidate", Provider: "nordvpn", Country: "Netherlands"})
			if err == nil {
				t.Fatal("ambiguous creation succeeded")
			}
			if !collision && len(e.networks) != 0 {
				t.Fatal("creation response ambiguity leaked owned network")
			}
			if collision && (len(e.networks) != 1 || len(e.netRemoved) != 0) {
				t.Fatal("cleanup deleted unrelated network name collision")
			}
		})
	}
}

type failedPrivateCleanupEngine struct {
	candidateEngine
	parent string
}

func (e *failedPrivateCleanupEngine) Start(ctx context.Context, id string) error {
	if err := e.candidateEngine.Start(ctx, id); err != nil {
		return err
	}
	for i := range e.containers {
		e.containers[i].Health = "healthy"
	}
	return nil
}
func (e *failedPrivateCleanupEngine) RemoveNetwork(ctx context.Context, name string) error {
	if err := e.candidateEngine.RemoveNetwork(ctx, name); err != nil {
		return err
	}
	return os.Chmod(e.parent, 0o500)
}
func TestSuccessfulCandidateReportsPrivateFileCleanupFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses filesystem permission boundary")
	}
	m, _, c := managerFixture(t)
	parent := filepath.Join(m.stateDir, "vpn", "tests")
	e := &failedPrivateCleanupEngine{parent: parent}
	m.eng = e
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	s, err := m.TestSaved(context.Background(), c.ID, Input{Name: "candidate", Provider: "nordvpn", Country: "Netherlands"})
	if err == nil || !strings.Contains(err.Error(), "private") || s.State == "connected" {
		t.Fatalf("candidate succeeded despite private file cleanup failure: %+v %v", s, err)
	}
}

type stalledCandidateProbeEngine struct {
	candidateEngine
	entered, closed chan struct{}
	once            sync.Once
}

func (e *stalledCandidateProbeEngine) Exec(context.Context, string, engine.ExecSpec) (*engine.Process, error) {
	r, w := io.Pipe()
	close(e.entered)
	return &engine.Process{Stdout: r, Close: func() { e.once.Do(func() { _ = w.Close(); close(e.closed) }) }, Wait: func() (int, error) { <-e.closed; return -1, context.Canceled }}, nil
}
func TestCandidateCancellationDuringOpenProbeReachesCleanup(t *testing.T) {
	m, _, c := managerFixture(t)
	e := &stalledCandidateProbeEngine{entered: make(chan struct{}), closed: make(chan struct{})}
	m.eng = e
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.TestSaved(ctx, c.ID, Input{Name: "candidate", Provider: "nordvpn", Country: "Netherlands"})
		done <- err
	}()
	<-e.entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled open probe succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("open probe blocked candidate deferred cleanup")
	}
	select {
	case <-e.closed:
	default:
		t.Fatal("open probe attachment was not closed")
	}
	if len(e.containers) != 0 || len(e.networks) != 0 {
		t.Fatal("open probe cancellation leaked owned resources")
	}
	entries, err := os.ReadDir(filepath.Join(m.stateDir, "vpn", "tests"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("open probe cancellation leaked private files: %v", err)
	}
}

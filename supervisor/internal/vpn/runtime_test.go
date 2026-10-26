package vpn

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type runtimeEngine struct {
	engine.Engine
	specs        []engine.ContainerSpec
	containers   []engine.Container
	removed      []string
	failStart    bool
	failExec     bool
	failList     bool
	calls        int
	imageMissing bool
	failPull     bool
	pulls        int
	networks     []engine.Network
	egressOutput string
}

func (e *runtimeEngine) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	if e.networks != nil {
		return e.networks, nil
	}
	return []engine.Network{{Name: "desktop", Subnet: "10.213.0.0/28"}, {Name: "other", Subnet: "10.213.0.16/28"}}, nil
}
func (e *runtimeEngine) List(_ context.Context, labels map[string]string) ([]engine.Container, error) {
	if e.failList {
		return nil, errors.New("secret engine discovery error")
	}
	var out []engine.Container
	for _, c := range e.containers {
		ok := true
		for k, v := range labels {
			if c.Labels[k] != v {
				ok = false
			}
		}
		if ok {
			out = append(out, c)
		}
	}
	return out, nil
}
func (e *runtimeEngine) Create(_ context.Context, s engine.ContainerSpec) (string, error) {
	e.specs = append(e.specs, s)
	e.containers = append(e.containers, engine.Container{ID: s.Name, Name: s.Name, Labels: s.Labels, State: "created"})
	return s.Name, nil
}
func (e *runtimeEngine) Start(_ context.Context, id string) error {
	if e.failStart {
		return errors.New("secret-containing engine failure")
	}
	for i := range e.containers {
		if e.containers[i].ID == id {
			e.containers[i].State = "running"
		}
	}
	return nil
}
func (e *runtimeEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	for _, c := range e.containers {
		if c.ID == id {
			return c, nil
		}
	}
	return engine.Container{}, engine.ErrNotFound
}
func (e *runtimeEngine) Remove(_ context.Context, id string) error {
	e.removed = append(e.removed, id)
	for i, c := range e.containers {
		if c.ID == id {
			e.containers = append(e.containers[:i], e.containers[i+1:]...)
			break
		}
	}
	return nil
}
func (e *runtimeEngine) Exec(_ context.Context, _ string, s engine.ExecSpec) (*engine.Process, error) {
	e.calls++
	if e.failExec {
		return nil, errors.New("secret raw probe error")
	}
	out := "ready"
	if strings.Contains(strings.Join(s.Cmd, " "), "api.ipify.org") {
		out = "203.0.113.5"
		if e.egressOutput != "" {
			out = e.egressOutput
		}
	}
	return &engine.Process{Stdout: strings.NewReader(out), Stderr: strings.NewReader(""), Close: func() {}, Wait: func() (int, error) { return 0, nil }}, nil
}

func (e *runtimeEngine) ImageInspect(context.Context, string) (engine.Image, error) {
	if e.imageMissing {
		return engine.Image{}, engine.ErrNotFound
	}
	return engine.Image{ID: GluetunImage}, nil
}
func (e *runtimeEngine) Pull(ctx context.Context, _ string, _ func(engine.PullProgress)) error {
	e.pulls++
	if e.failPull {
		return errors.New("secret registry failure")
	}
	e.imageMissing = false
	return ctx.Err()
}
func TestEnsurePullsPinnedImageOnFreshEngineAndSanitizesPullFailures(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(strconv.FormatBool(fail), func(t *testing.T) {
			m, e, c := managerFixture(t)
			e.imageMissing = true
			e.failPull = fail
			_, err := m.Ensure(context.Background(), strings.Repeat("1", 32), "desktop", c.ID, nil, false)
			if e.pulls != 1 {
				t.Fatalf("fresh engine pulls=%d", e.pulls)
			}
			if fail {
				if err == nil || strings.Contains(err.Error(), "secret") || len(e.specs) != 0 {
					t.Fatalf("pull error not sanitized/bounded before creation: %v", err)
				}
			} else if err != nil || len(e.specs) != 1 {
				t.Fatalf("fresh engine not provisioned: %v", err)
			}
		})
	}
}
func managerFixture(t *testing.T) (*Manager, *runtimeEngine, Connection) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "networks.json"), filepath.Join(dir, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	u, p := "service-user", "service-password"
	c, err := s.Create(Input{Name: "test", Country: "Netherlands", Provider: "nordvpn", Username: &u, Password: &p})
	if err != nil {
		t.Fatal(err)
	}
	eng := &runtimeEngine{}
	return NewManager(eng, s, filepath.Join(dir, "state")), eng, c
}
func TestEnsureReadinessIsIndependentOfTunnelHealthAndReusesOnlyMatchingRuntime(t *testing.T) {
	m, e, c := managerFixture(t)
	ctx := context.Background()
	id := strings.Repeat("c", 32)
	runtime, err := m.Ensure(ctx, id, "desktop", c.ID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Health != "" {
		t.Fatal("test gateway unexpectedly healthy")
	}
	if len(e.specs) != 1 {
		t.Fatal(e.specs)
	}
	if _, err = m.Ensure(ctx, id, "desktop", c.ID, nil, true); err != nil || len(e.specs) != 1 {
		t.Fatalf("not idempotent: %v", err)
	}
	if _, err = m.Ensure(ctx, id, "desktop", c.ID, []Exception{{Destination: "10.213.0.18", Protocol: "tcp", Ports: []PortRange{{Start: 80}}}}, true); !errors.Is(err, ErrValidation) {
		t.Fatalf("reserved overlap accepted: %v", err)
	}
	if _, err = m.Ensure(ctx, id, "desktop", c.ID, nil, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale runtime replaced unsafely: %v", err)
	}
	for _, mount := range e.specs[0].Mounts {
		if mount.Target == "/gluetun/auth/config.toml" {
			data, err := os.ReadFile(mount.Source)
			if err != nil || !strings.Contains(string(data), "auth = \"apikey\"") || strings.Contains(string(data), "auth = \"none\"") {
				t.Fatalf("unsafe auth: %v", err)
			}
		}
	}
}

// A runtime stopped by a host or engine restart is started again, keeping
// its identity, so its consumer's namespace reference stays valid.
func TestEnsureStartsStoppedMatchingRuntimeInPlace(t *testing.T) {
	m, e, c := managerFixture(t)
	ctx := context.Background()
	id := strings.Repeat("c", 32)
	first, err := m.Ensure(ctx, id, "desktop", c.ID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	e.containers[0].State = "exited"
	again, err := m.Ensure(ctx, id, "desktop", c.ID, nil, true)
	if err != nil {
		t.Fatalf("stopped runtime not recovered: %v", err)
	}
	if again.ID != first.ID || again.State != "running" || len(e.specs) != 1 || len(e.removed) != 0 {
		t.Fatalf("stopped runtime replaced: %+v specs=%d removed=%v", again, len(e.specs), e.removed)
	}
	e.containers[0].State = "exited"
	e.failStart = true
	if _, err = m.Ensure(ctx, id, "desktop", c.ID, nil, true); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("failed start not reported safely: %v", err)
	}
}
func TestEnsureCleansFailedOwnedRuntimeAndSanitizesError(t *testing.T) {
	m, e, c := managerFixture(t)
	e.failStart = true
	_, err := m.Ensure(context.Background(), strings.Repeat("d", 32), "desktop", c.ID, nil, false)
	if err == nil || strings.Contains(err.Error(), "secret") || len(e.removed) != 1 {
		t.Fatalf("failed cleanup/redaction: %v %v", err, e.removed)
	}
}
func TestRemoveScopesOwnership(t *testing.T) {
	m, e, _ := managerFixture(t)
	id := strings.Repeat("e", 32)
	e.containers = []engine.Container{{ID: "owned", Labels: map[string]string{LabelRole: runtimeRole, LabelEnvironment: id}}, {ID: "unrelated", Labels: map[string]string{LabelEnvironment: id}}, {ID: "other-desktop", Labels: map[string]string{LabelRole: runtimeRole, LabelEnvironment: strings.Repeat("f", 32)}}}
	if err := m.Remove(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.removed, ",") != "owned" {
		t.Fatal(e.removed)
	}
}

// Runtimes are keyed by environment name, which two supervisors on one
// engine may share: an instance only finds, and removes, its own.
func TestInstanceScopesRuntimesByName(t *testing.T) {
	m, e, c := managerFixture(t)
	m.ForInstance("mine")
	e.containers = []engine.Container{{ID: "theirs", Labels: map[string]string{LabelRole: runtimeRole, LabelEnvironment: "alice", LabelInstance: "theirs"}}}
	if _, err := m.Ensure(context.Background(), "alice", "desktop", c.ID, nil, false); err != nil {
		t.Fatal(err)
	}
	spec := e.specs[len(e.specs)-1]
	if spec.Name != "deskyard-alice.vpn" || spec.Labels[LabelEnvironment] != "alice" || spec.Labels[LabelInstance] != "mine" {
		t.Fatalf("runtime identity: %s %v", spec.Name, spec.Labels)
	}
	if err := m.Remove(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.removed, ",") != spec.Name {
		t.Fatalf("removed %v", e.removed)
	}
	if _, err := m.Ensure(context.Background(), "Alice/../x", "desktop", c.ID, nil, false); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid identity accepted: %v", err)
	}
}
func TestStatusRequiresHealthAndVerifiedEgressCachesForFiveSeconds(t *testing.T) {
	m, e, c := managerFixture(t)
	id := strings.Repeat("a", 32)
	_, err := m.Ensure(context.Background(), id, "desktop", c.ID, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Status(context.Background(), id)
	if err != nil || s.State != "connecting" || s.CurrentExitIP != "" {
		t.Fatalf("bad initial state: %+v %v", s, err)
	}
	e.containers[0].Health = "healthy"
	m.observations = nil
	s, err = m.Status(context.Background(), id)
	if err != nil || s.State != "connected" || s.CurrentExitIP != "203.0.113.5" || s.ExitIPObservedAt.IsZero() {
		t.Fatalf("unverified: %+v %v", s, err)
	}
	calls := e.calls
	_, _ = m.Status(context.Background(), id)
	if e.calls != calls {
		t.Fatal("status performs per-viewer probes")
	}
	e.containers[0].Health = "unhealthy"
	m.observations[id].CheckedAt = time.Now().Add(-HealthInterval)
	s, err = m.Status(context.Background(), id)
	if err != nil || s.State != "reconnecting" || s.CurrentExitIP != "" || s.LastExitIP != "203.0.113.5" {
		t.Fatalf("stale current IP: %+v %v", s, err)
	}
	e.failExec = true
	m.observations[id].CheckedAt = time.Now().Add(-HealthInterval)
	s, err = m.Status(context.Background(), id)
	if err != nil || s.State != "blocked" || strings.Contains(s.Reason, "secret") {
		t.Fatalf("bad failure: %+v %v", s, err)
	}
}

func TestStatusDiscoveryFailureBecomesSanitizedBlockedObservation(t *testing.T) {
	m, e, c := managerFixture(t)
	id := strings.Repeat("9", 32)
	if _, err := m.Ensure(context.Background(), id, "desktop", c.ID, nil, false); err != nil {
		t.Fatal(err)
	}
	e.containers[0].Health = "healthy"
	if _, err := m.Status(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	m.observations[id].CheckedAt = time.Now().Add(-HealthInterval)
	e.failList = true
	s, err := m.Status(context.Background(), id)
	if err != nil || s.State != "blocked" || s.CurrentExitIP != "" || strings.Contains(s.Reason, "secret") {
		t.Fatalf("discovery failure left stale current state: %+v %v", s, err)
	}
}

func TestReservedNetworkConfigurationCopiesAndRejectsFutureDesktopOverlap(t *testing.T) {
	m, e, c := managerFixture(t)
	prefixes := []netip.Prefix{netip.MustParsePrefix("10.213.0.0/16")}
	if err := m.SetReservedNetworks(prefixes); err != nil {
		t.Fatal(err)
	}
	prefixes[0] = netip.MustParsePrefix("192.168.0.0/16")
	_, err := m.Ensure(context.Background(), strings.Repeat("2", 32), "desktop", c.ID, []Exception{{Destination: "10.213.240.10", Protocol: "tcp", Ports: []PortRange{{Start: 80}}}}, false)
	if !errors.Is(err, ErrValidation) || len(e.specs) != 0 {
		t.Fatalf("future desktop exception allowed: %v", err)
	}
	if err = m.SetReservedNetworks([]netip.Prefix{{}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid reserved prefix accepted: %v", err)
	}
}

type slowRuntimeEngine struct {
	runtimeEngine
	entered chan struct{}
	release chan struct{}
}

func (e *slowRuntimeEngine) Start(ctx context.Context, id string) error {
	close(e.entered)
	select {
	case <-e.release:
		return e.runtimeEngine.Start(ctx, id)
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestSlowEnsureDoesNotBlockOtherDesktopStatus(t *testing.T) {
	m, _, c := managerFixture(t)
	e := &slowRuntimeEngine{entered: make(chan struct{}), release: make(chan struct{})}
	m.eng = e
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.Ensure(ctx, strings.Repeat("3", 32), "desktop", c.ID, nil, false)
		done <- err
	}()
	<-e.entered
	probeCtx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	statusDone := make(chan error, 1)
	go func() { _, err := m.Status(probeCtx, strings.Repeat("4", 32)); statusDone <- err }()
	select {
	case err := <-statusDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-probeCtx.Done():
		close(e.release)
		<-done
		t.Fatal("another desktop's Ensure blocked status past its deadline")
	}
	close(e.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEnsureReusesRuntimeWhenNewNetworkIsAlreadyReservedByPool(t *testing.T) {
	m, e, c := managerFixture(t)
	if err := m.SetReservedNetworks([]netip.Prefix{netip.MustParsePrefix("10.213.0.0/16")}); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("5", 32)
	if _, err := m.Ensure(context.Background(), id, "desktop", c.ID, nil, false); err != nil {
		t.Fatal(err)
	}
	e.networks = []engine.Network{{Name: "desktop", Subnet: "10.213.0.0/28"}, {Name: "other", Subnet: "10.213.0.16/28"}, {Name: "new", Subnet: "10.213.1.0/28"}}
	if _, err := m.Ensure(context.Background(), id, "desktop", c.ID, nil, false); err != nil || len(e.specs) != 1 {
		t.Fatalf("covered network allocation forces unnecessary replacement: %v", err)
	}
}

func TestStatusDoesNotVerifyNonPublicOrMalformedEgressObservation(t *testing.T) {
	for _, value := range []string{"invalid", "10.0.0.1", "224.0.0.1", "0.0.0.0", "::1", "100.64.0.1"} {
		t.Run(value, func(t *testing.T) {
			m, e, c := managerFixture(t)
			id := strings.Repeat("6", 32)
			if _, err := m.Ensure(context.Background(), id, "desktop", c.ID, nil, false); err != nil {
				t.Fatal(err)
			}
			e.containers[0].Health = "healthy"
			e.egressOutput = value
			s, err := m.Status(context.Background(), id)
			if err != nil || s.State == "connected" || s.CurrentExitIP != "" {
				t.Fatalf("invalid egress observation verified: %+v %v", s, err)
			}
		})
	}
}

func TestStatusHonorsDeadlineWhileItsDesktopEnsureIsBusy(t *testing.T) {
	m, _, c := managerFixture(t)
	e := &slowRuntimeEngine{entered: make(chan struct{}), release: make(chan struct{})}
	m.eng = e
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id := strings.Repeat("7", 32)
	done := make(chan error, 1)
	go func() { _, err := m.Ensure(ctx, id, "desktop", c.ID, nil, false); done <- err }()
	<-e.entered
	statusCtx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	statusDone := make(chan error, 1)
	go func() { _, err := m.Status(statusCtx, id); statusDone <- err }()
	select {
	case err := <-statusDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("busy status deadline not reported: %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Error("same-desktop operation lock ignored status deadline")
	}
	close(e.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

package desktops

import (
	"context"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type pairEngine struct {
	networkCalls int
	failNetwork  bool
	*fakeEngine
	specs []engine.ContainerSpec
	calls []string
	port  int
}

func (e *pairEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	e.specs = append(e.specs, s)
	e.calls = append(e.calls, "create")
	id, err := e.fakeEngine.Create(ctx, s)
	if err == nil {
		e.cs[id].LocalPort = e.port
		e.cs[id].MemoryBytes = s.MemoryBytes
		e.cs[id].NanoCPUs = s.NanoCPUs
	}
	return id, err
}
func (e *pairEngine) Remove(ctx context.Context, id string) error {
	e.calls = append(e.calls, "remove-desktop")
	return e.fakeEngine.Remove(ctx, id)
}
func (e *pairEngine) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	return nil, nil
}
func (e *pairEngine) CreateNetwork(context.Context, string, string, map[string]string) error {
	e.networkCalls++
	if e.failNetwork {
		return errors.New("network unavailable")
	}
	return nil
}
func (e *pairEngine) RemoveNetwork(context.Context, string) error                    { return nil }
func (e *pairEngine) ConnectNetwork(context.Context, string, string, []string) error { return nil }
func (e *pairEngine) DisconnectNetwork(context.Context, string, string) error        { return nil }

type pairRuntime struct {
	e               *pairEngine
	fail            bool
	ensures, probes int
	gateway         engine.Container
}

func (r *pairRuntime) SetReservedNetworks([]netip.Prefix) error { return nil }
func (r *pairRuntime) Ensure(context.Context, string, string, string, string, []vpn.Exception, bool) (engine.Container, error) {
	r.ensures++
	r.e.calls = append(r.e.calls, "ensure")
	if r.fail {
		return engine.Container{}, errors.New("provider secret must not leak")
	}
	r.gateway.Labels = map[string]string{vpn.LabelRole: "gateway"}
	r.e.cs[r.gateway.ID] = &r.gateway
	return r.gateway, nil
}
func (r *pairRuntime) Remove(context.Context, string) error {
	r.e.calls = append(r.e.calls, "remove-runtime")
	return nil
}
func (r *pairRuntime) Status(context.Context, string) (vpn.Status, error) {
	r.probes++
	return vpn.Status{State: "reconnecting"}, nil
}
func pairSetup(t *testing.T) (*Service, *pairEngine, *pairRuntime, vpn.Connection) {
	t.Helper()
	e := &pairEngine{fakeEngine: newFakeEngine(t.TempDir())}
	dir := t.TempDir()
	s := New(e, Config{DataDir: dir, Subnet: "10.213.0.0/16"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	st, err := vpn.NewStore(filepath.Join(dir, ".deskyard"))
	if err != nil {
		t.Fatal(err)
	}
	u, p := "user", "password"
	conn, err := st.Create(vpn.Input{Name: "Home", Provider: "nordvpn", Country: "Netherlands", Username: &u, Password: &p})
	if err != nil {
		t.Fatal(err)
	}
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	t.Cleanup(ready.Close)
	parsed, _ := url.Parse(ready.URL)
	port, _ := strconv.Atoi(parsed.Port())
	e.port = port
	r := &pairRuntime{e: e, gateway: engine.Container{ID: "gateway", State: "running", LocalPort: port, IP: map[string]string{"deskyard-alice": "10.213.0.2"}}}
	if err := s.ConfigureNetworking(st, r, filepath.Join(dir, ".deskyard")); err != nil {
		t.Fatal(err)
	}
	return s, e, r, conn
}

var noProgress = func(string, float64) {}

func TestVPNEnableFailureRemainsVisibleAndRecovers(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	ctx := context.Background()
	created := time.Now().UTC().Truncate(time.Second)
	if err := s.createContainer(ctx, "alice", "012345678901234567890123456789ab", "token", created, Settings{Image: "img", MemoryBytes: 123, NanoCPUs: 456}); err != nil {
		t.Fatal(err)
	}
	r.fail = true
	_, err := s.Recreate(ctx, "alice", Settings{Image: "img", MemoryBytes: 123, NanoCPUs: 456, VPNConnectionID: conn.ID}, noProgress)
	if err == nil {
		t.Fatal("expected failed attachment")
	}
	got, err := s.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings.VPNConnectionID != conn.ID || got.VPN.State != "blocked" {
		t.Fatalf("lost blocked intent: %+v", got)
	}
	if _, err = e.Inspect(ctx, "deskyard-alice"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("consumer should be absent after failed runtime preparation")
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].ID != got.ID {
		t.Fatal("missing synthetic desktop")
	}
	fresh := New(e, s.Cfg, s.Log)
	r.fail = false
	if err = fresh.ConfigureNetworking(s.Networking.Connections, r, filepath.Join(s.Cfg.DataDir, ".deskyard")); err != nil {
		t.Fatal(err)
	}
	if err = fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = fresh.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "012345678901234567890123456789ab" || got.Container.Env[TokenEnv] != "token" || !got.Created.Equal(created) || got.Container.NetworkContainer != "gateway" {
		t.Fatalf("snapshot lost: %+v", got)
	}
	spec := e.specs[len(e.specs)-1]
	if spec.MemoryBytes != 123 || spec.NanoCPUs != 456 || spec.Restart != "no" || spec.Network != "" || spec.PublishLocal != nil {
		t.Fatalf("unsafe consumer: %+v", spec)
	}
	if got.State != "stopped" {
		t.Fatalf("power state changed: %s", got.State)
	}
}
func TestVPNPowerTargetAndCachedStatus(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	ctx := context.Background()
	got, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if got.Container.NetworkContainer != "gateway" {
		t.Fatal("not attached")
	}
	before := len(e.calls)
	if err = s.Restart(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if len(e.calls) != before+1 || e.calls[len(e.calls)-1] != "ensure" {
		t.Fatal("restart replaced tunnel")
	}
	if err = s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err = s.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	addr, token, err := s.Target(ctx, "alice")
	if err != nil || addr != "127.0.0.1:"+strconv.Itoa(r.gateway.LocalPort) || token == "" {
		t.Fatalf("target %s %v", addr, err)
	}
	s.Cfg.InContainer = true
	addr, _, err = s.Target(ctx, "alice")
	if err != nil || addr != "10.213.0.2:8080" {
		t.Fatalf("container target %s %v", addr, err)
	}
	s.Cfg.InContainer = false
	for range 5 {
		s.List(ctx)
		s.Get(ctx, "alice")
	}
	if r.probes != 0 {
		t.Fatal("viewer triggered health probe")
	}
	s.Networking.Poll(ctx, nil)
	after, _ := s.Get(ctx, "alice")
	if after.VPN.State != "reconnecting" || !after.Container.Running() {
		t.Fatalf("health changed desktop power: %+v", after)
	}
	set := after.Settings
	set.VPNConnectionID = ""
	if _, err = s.Recreate(ctx, "alice", set, noProgress); err != nil {
		t.Fatal(err)
	}
	spec := e.specs[len(e.specs)-1]
	if spec.NetworkContainer != "" || spec.Network != "deskyard-alice" || spec.Restart != "unless-stopped" {
		t.Fatalf("disable: %+v", spec)
	}
}
func TestVPNRoleNeverListedAsDesktop(t *testing.T) {
	s, e, _, _ := pairSetup(t)
	e.cs["deskyard-bogus"] = &engine.Container{ID: "bogus", Labels: map[string]string{LabelManaged: "true", LabelName: "bogus", vpn.LabelRole: "gateway"}}
	list, err := s.List(context.Background())
	if err != nil || len(list) != 0 {
		t.Fatalf("gateway listed: %+v %v", list, err)
	}
}

func TestVPNReferencesPreventDeletionAndRevisionRollout(t *testing.T) {
	s, _, r, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.Begin("alice", "creating"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	s.Done("alice")
	if err := s.Networking.DeleteConnection(conn.ID); !errors.Is(err, vpn.ErrConflict) {
		t.Fatalf("referenced delete: %v", err)
	}
	if err := s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	refs := s.Networking.References(conn.ID)
	if len(refs) != 1 || refs[0].Name != "alice" {
		t.Fatalf("references: %+v", refs)
	}
	r.fail = true
	updated, results, err := s.Networking.UpdateConnection(ctx, conn.ID, conn.Revision, []string{refs[0].ID}, vpn.Input{Name: "Changed", Provider: "nordvpn", Country: "Germany"}, noProgress, nil)
	if err != nil || updated.Revision != 2 || len(results) != 1 || results[0].Error == "" {
		t.Fatalf("rollout: %+v %+v %v", updated, results, err)
	}
	got, _ := s.Get(ctx, "alice")
	if got.Settings.VPNConnectionID != conn.ID || got.VPN.DesiredRevision != 2 || got.VPN.State != "blocked" {
		t.Fatalf("lost desired revision: %+v", got.VPN)
	}
	if err = s.Remove(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
	if err = s.Networking.DeleteConnection(conn.ID); err != nil {
		t.Fatal(err)
	}
}

func TestVPNChangeCannotRaceDirectStart(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.createContainer(ctx, "alice", "012345678901234567890123456789ab", "token", time.Now(), Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	// All service operations must honor the same lock even before assignment exists.
	unlock, err := s.Networking.lock(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	err = s.Start(deadline, "alice")
	unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("direct start bypassed transition lock: %v", err)
	}
	_ = e
	_ = r
	_ = conn
}
func TestVPNLabeledDesktopWithoutJournalNeverStartsDirect(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.createContainer(ctx, "alice", "012345678901234567890123456789ab", "token", time.Now(), Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	e.cs["deskyard-alice"].Labels[vpn.LabelConnection] = conn.ID
	got, _ := s.Get(ctx, "alice")
	if got.Settings.VPNConnectionID != conn.ID || got.VPN.State != "blocked" {
		t.Fatalf("unrecorded VPN selection lost: %+v", got)
	}
	if err := s.Start(ctx, "alice"); err == nil {
		t.Fatal("missing journal was treated as direct")
	}
}
func TestVPNDisableRetainsOmittedExceptions(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	ctx := context.Background()
	rules := []vpn.Exception{{Destination: "192.168.1.10", Protocol: "tcp", Ports: []vpn.PortRange{{Start: 631}}}}
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID, LANExceptions: rules}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	set := d.Settings
	set.VPNConnectionID = ""
	d, err = s.Recreate(ctx, "alice", set, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Settings.LANExceptions) != 1 {
		t.Fatal("disable discarded omitted LAN settings")
	}
}
func TestVPNConsumerCannotBeCreatedWithoutNamespace(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	err := s.createContainer(context.Background(), "alice", "id", "token", time.Now(), Settings{Image: "img", VPNConnectionID: conn.ID})
	if err == nil {
		t.Fatal("enabled consumer accepted independent networking")
	}
}

func TestVPNRemoveRefusesForeignReplacement(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	replacement := *e.cs["deskyard-alice"]
	replacement.Labels = map[string]string{LabelManaged: "true", LabelName: "alice", LabelID: "somebody-else"}
	e.cs["deskyard-alice"] = &replacement
	if err := s.Remove(ctx, "alice", false); err == nil {
		t.Fatal("removed foreign replacement")
	}
	if _, err := e.Inspect(ctx, "deskyard-alice"); err != nil {
		t.Fatal("foreign replacement destroyed")
	}
}
func TestVPNInterruptedCreateRecoversMissingNetwork(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	e.failNetwork = true
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err == nil {
		t.Fatal("expected network failure")
	}
	calls := e.networkCalls
	e.failNetwork = false
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if e.networkCalls == calls {
		t.Fatal("recovery never recreated missing desktop network")
	}
}

func TestVPNStatusRetainsHistoricalExitOnBlockedTransition(t *testing.T) {
	s, _, _, _ := pairSetup(t)
	n := s.Networking
	observed := time.Now().UTC()
	n.setStatus("id", vpn.Status{State: "connected", CurrentExitIP: "8.8.8.8", LastExitIP: "8.8.8.8", ExitIPObservedAt: observed})
	n.setStatus("id", vpn.Status{State: "blocked", Reason: "transition failed"})
	n.mu.Lock()
	status := n.statuses["id"]
	n.mu.Unlock()
	if status.CurrentExitIP != "" || status.LastExitIP != "8.8.8.8" || !status.ExitIPObservedAt.Equal(observed) {
		t.Fatalf("historical IP lost or mislabeled: %+v", status)
	}
}
func TestVPNOperationAndSnapshotConcurrency(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	n := s.Networking
	ctx := context.Background()
	r := networkIntent{ID: "012345678901234567890123456789ab", Name: "alice", Token: "token", Settings: Settings{Image: "img", VPNConnectionID: conn.ID}, Pending: true}
	if err := n.saveRecord(r); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var active atomic.Int32
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 12 {
				unlock, err := n.lock(ctx, "alice")
				if err != nil {
					t.Error(err)
					return
				}
				if active.Add(1) != 1 {
					t.Error("concurrent operations")
				}
				n.setStatus(r.ID, vpn.Status{State: "connecting"})
				n.References(conn.ID)
				n.synthetic(r)
				n.all()
				active.Add(-1)
				unlock()
			}
		}()
	}
	wg.Wait()
	if err := n.DeleteConnection(conn.ID); !errors.Is(err, vpn.ErrConflict) {
		t.Fatalf("reference disappeared: %v", err)
	}
}
func TestVPNDeleteWaitsForAssignmentCommit(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	n := s.Networking
	n.refs.Lock()
	done := make(chan error, 1)
	go func() { done <- n.DeleteConnection(conn.ID) }()
	select {
	case err := <-done:
		n.refs.Unlock()
		t.Fatalf("deletion bypassed assignment boundary: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	err := n.saveRecord(networkIntent{ID: "012345678901234567890123456789ab", Name: "alice", Token: "token", Settings: Settings{Image: "img", VPNConnectionID: conn.ID}})
	n.refs.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, vpn.ErrConflict) {
		t.Fatalf("assignment raced deletion: %v", err)
	}
}
func TestVPNReferencesIncludeOrphanedLabels(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.createContainer(ctx, "alice", "012345678901234567890123456789ab", "token", time.Now(), Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	e.cs["deskyard-alice"].Labels[vpn.LabelConnection] = conn.ID
	if err := s.Networking.DeleteConnection(conn.ID); !errors.Is(err, vpn.ErrConflict) {
		t.Fatalf("orphaned selected desktop did not prevent delete: %v", err)
	}
}
func TestVPNGatewayEventsWakeCentralObserverOnlyForOwnedRecords(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	n := s.Networking
	r := networkIntent{ID: "012345678901234567890123456789ab", Name: "alice", Token: "token", Settings: Settings{Image: "img", VPNConnectionID: conn.ID}}
	if err := n.saveRecord(r); err != nil {
		t.Fatal(err)
	}
	n.NotifyEngineEvent(engine.Event{Labels: map[string]string{vpn.LabelRole: "gateway", vpn.LabelDesktopID: "foreign"}})
	select {
	case <-n.wake:
		t.Fatal("foreign gateway triggered reconciliation")
	default:
	}
	n.NotifyEngineEvent(engine.Event{Labels: map[string]string{vpn.LabelRole: "gateway", vpn.LabelDesktopID: r.ID}})
	select {
	case <-n.wake:
	default:
		t.Fatal("owned gateway event ignored")
	}
}
func TestVPNGoneCameraRecoversWithoutLosingNamespacePolicy(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	s.Cfg.LocalEngine = true
	s.Cfg.SysDir = e.sys
	addCamera(t, e.sys, "video0")
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID, Camera: "/dev/video0"}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Webcam() {
		t.Fatal("camera not preserved")
	}
	if err = s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(filepath.Join(e.sys, "devices", "virtual", "video4linux", "video0")); err != nil {
		t.Fatal(err)
	}
	if err = s.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Get(ctx, "alice")
	if d.Settings.Camera != "" || d.Settings.VPNConnectionID != conn.ID || d.Container.NetworkContainer == "" {
		t.Fatalf("camera fallback lost policy: %+v", d)
	}
}
func TestVPNStoppedStateChangesTimestamp(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	before := d.VPN.ChangedAt
	time.Sleep(time.Millisecond)
	if err = s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Get(ctx, "alice")
	if d.VPN.State != "stopped" || !d.VPN.ChangedAt.After(before) {
		t.Fatalf("stopped state retained old timestamp: %+v", d.VPN)
	}
}

package computers

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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type pairEngine struct {
	networkCalls int
	failNetwork  bool
	nets         map[string]engine.Network
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
		if e.port != 0 && s.Network != "" {
			e.cs[id].IP = map[string]string{s.Network: "127.0.0.1"}
		}
		e.cs[id].MemoryBytes = s.MemoryBytes
		e.cs[id].NanoCPUs = s.NanoCPUs
	}
	return id, err
}
func (e *pairEngine) Remove(ctx context.Context, id string) error {
	e.calls = append(e.calls, "remove-desktop")
	return e.fakeEngine.Remove(ctx, id)
}
func (e *pairEngine) Networks(_ context.Context, l map[string]string) ([]engine.Network, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []engine.Network
next:
	for _, n := range e.nets {
		for k, v := range l {
			if n.Labels[k] != v {
				continue next
			}
		}
		out = append(out, n)
	}
	return out, nil
}
func (e *pairEngine) CreateNetwork(_ context.Context, name, subnet string, l map[string]string) error {
	e.networkCalls++
	if e.failNetwork {
		return errors.New("network unavailable")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.nets[name]; ok {
		return errors.New("network name in use")
	}
	if e.nets == nil {
		e.nets = map[string]engine.Network{}
	}
	e.nets[name] = engine.Network{ID: name, Name: name, Subnet: subnet, Labels: l}
	return nil
}
func (e *pairEngine) RemoveNetwork(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.nets[name]; !ok {
		return engine.ErrNotFound
	}
	delete(e.nets, name)
	return nil
}
func (e *pairEngine) ConnectNetwork(context.Context, string, string, []string) error { return nil }
func (e *pairEngine) DisconnectNetwork(context.Context, string, string) error        { return nil }

type pairRuntime struct {
	e               *pairEngine
	fail            bool
	ensures, probes int
	gateway         engine.Container
}

func (r *pairRuntime) SetReservedNetworks([]netip.Prefix) error { return nil }
func (r *pairRuntime) Ensure(_ context.Context, name, _, _ string, _ []vpn.Exception) (engine.Container, error) {
	r.ensures++
	r.e.calls = append(r.e.calls, "ensure")
	if r.fail {
		return engine.Container{}, errors.New("provider secret must not leak")
	}
	r.gateway.Labels = map[string]string{vpn.LabelRole: "gateway", vpn.LabelComputer: name}
	r.gateway.State = "running" // as vpn.Manager starts a stopped runtime
	// The gateway sits on the computer's network, served on the local test port.
	r.gateway.IP = map[string]string{containerName(name): "127.0.0.1"}
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
	e := &pairEngine{fakeEngine: newFakeEngine()}
	dir := t.TempDir()
	s := New(e, Config{DataDir: dir, Subnet: "10.213.0.0/16"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	st, err := vpn.NewStore(filepath.Join(dir, "networks.json"), s.Layout.Secret)
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
	s.Cfg.DesktopPort = port
	r := &pairRuntime{e: e, gateway: engine.Container{ID: "gateway", State: "running"}}
	if err := s.ConfigureNetworking(st, r, s.Layout.State); err != nil {
		t.Fatal(err)
	}
	return s, e, r, conn
}

var noProgress = func(string, float64) {}

// preRename gives alice's computer, running in the VPN runtime pairRuntime
// made, the runtime a supervisor from before the rename left: labelled under
// deskyard.vpn.environment, by instance. It returns the runtime's ID.
func preRename(t *testing.T, s *Service, e *pairEngine, instance string) string {
	t.Helper()
	const id = "deskyard-alice.vpn"
	e.mu.Lock()
	delete(e.cs, "gateway")
	e.cs[id] = &engine.Container{ID: id, Name: id, State: "running", Restart: "no", Labels: map[string]string{
		vpn.LabelRole: "gateway", "deskyard.vpn.environment": "alice", vpn.LabelInstance: instance,
		vpn.LabelConnection: "c", vpn.LabelRevision: "1"}}
	e.cs["deskyard-alice"].NetworkContainer = id
	e.mu.Unlock()
	r := s.Networking.transition("alice")
	r.RuntimeID = id
	if err := s.Networking.saveRecord(r); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUpgradeReplacesLegacyLabelledRuntime(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	s.Instance = "this"
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	legacy := preRename(t, s, e, "this")
	// Another supervisor's runtime, and VPN test networks from before the
	// rename: this instance's left over, the other's its own.
	e.cs["other.vpn"] = &engine.Container{ID: "other.vpn", Name: "other.vpn", State: "running", Labels: map[string]string{
		vpn.LabelRole: "gateway", "deskyard.vpn.environment": "alice", vpn.LabelInstance: "other"}}
	e.nets = map[string]engine.Network{
		"deskyard-vpn-test-1": {ID: "deskyard-vpn-test-1", Name: "deskyard-vpn-test-1", Labels: map[string]string{
			vpn.LabelRole: "test", "deskyard.vpn.environment": "1", vpn.LabelInstance: "this"}},
		"deskyard-vpn-test-2": {ID: "deskyard-vpn-test-2", Name: "deskyard-vpn-test-2", Labels: map[string]string{
			vpn.LabelRole: "test", "deskyard.vpn.environment": "2", vpn.LabelInstance: "other"}},
	}
	fresh := reopen(t, s)
	if err := fresh.ConfigureNetworking(s.Networking.Connections, r, s.Layout.State); err != nil {
		t.Fatal(err)
	}
	ensures := r.ensures
	if err := fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Inspect(ctx, legacy); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("the runtime from before the rename was left")
	}
	// Replaced outright: a runtime isn't prepared beside the old one, whose
	// stable name it would take.
	if r.ensures != ensures+1 {
		t.Fatalf("%d runtimes prepared, want 1", r.ensures-ensures)
	}
	if _, ok := e.nets["deskyard-vpn-test-1"]; ok {
		t.Fatal("the VPN test network from before the rename was left")
	}
	if _, err := e.Inspect(ctx, "other.vpn"); err != nil {
		t.Fatal("another supervisor's runtime was removed")
	}
	if _, ok := e.nets["deskyard-vpn-test-2"]; !ok {
		t.Fatal("another supervisor's VPN test network was removed")
	}
	g, err := e.Inspect(ctx, "gateway")
	if err != nil || g.Labels[vpn.LabelComputer] != "alice" || !g.Running() {
		t.Fatalf("no runtime labelled with its computer: %+v %v", g, err)
	}
	got, err := fresh.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Container.Running() || got.Container.NetworkContainer != "gateway" {
		t.Fatalf("alice doesn't run in the new runtime: %+v", got.Container)
	}
}

// A stopped computer's runtime from before the rename is left for its
// start, which replaces it.
func TestUpgradeReplacesStoppedComputersLegacyRuntimeOnStart(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	s.Instance = "this"
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	legacy := preRename(t, s, e, "this")
	e.mu.Lock()
	e.cs[legacy].State = "exited"
	e.mu.Unlock()
	fresh := reopen(t, s)
	if err := fresh.ConfigureNetworking(s.Networking.Connections, r, s.Layout.State); err != nil {
		t.Fatal(err)
	}
	ensures := r.ensures
	if err := fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Inspect(ctx, legacy); err != nil || r.ensures != ensures {
		t.Fatalf("startup touched a stopped computer's runtime: %v, %d ensures", err, r.ensures-ensures)
	}
	if err := fresh.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Inspect(ctx, legacy); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("the runtime from before the rename was left")
	}
	got, _ := fresh.Get(ctx, "alice")
	if !got.Container.Running() || got.Container.NetworkContainer != "gateway" {
		t.Fatalf("alice doesn't run in the new runtime: %+v", got.Container)
	}
}

// A replacement interrupted after the computer's container was removed is
// finished at the next startup, the runtime from before the rename removed.
func TestUpgradeFinishesInterruptedLegacyReplacement(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	s.Instance = "this"
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	legacy := preRename(t, s, e, "this")
	tr := s.Networking.transition("alice")
	tr.Pending, tr.Repair = true, true
	if err := s.Networking.saveRecord(tr); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	delete(e.cs, "deskyard-alice")
	e.mu.Unlock()
	fresh := reopen(t, s)
	if err := fresh.ConfigureNetworking(s.Networking.Connections, r, s.Layout.State); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Inspect(ctx, legacy); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("the runtime from before the rename was left")
	}
	got, _ := fresh.Get(ctx, "alice")
	if !got.Container.Running() || got.Container.NetworkContainer != "gateway" {
		t.Fatalf("alice doesn't run in the new runtime: %+v", got.Container)
	}
}

func TestVPNEnableFailureRemainsVisibleAndRecovers(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img", MemoryBytes: 123, NanoCPUs: 456}); err != nil {
		t.Fatal(err)
	}
	if err := s.setPower("alice", PowerStopped); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(ctx, "alice")
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
		t.Fatal("missing synthetic computer")
	}
	fresh := reopen(t, s)
	r.fail = false
	if err = fresh.ConfigureNetworking(s.Networking.Connections, r, s.Layout.State); err != nil {
		t.Fatal(err)
	}
	// Startup leaves the failed change, and the stopped computer, alone.
	specs := len(e.specs)
	if err = fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ = fresh.Get(ctx, "alice"); len(e.specs) != specs || got.VPN.State != "blocked" || got.Error == "" {
		t.Fatalf("startup retried a failed change: %+v", got)
	}
	if err = fresh.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	got, err = fresh.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "alice" || got.Container.Env[TokenEnv] != before.Container.Env[TokenEnv] || !got.Created.Equal(before.Created) || got.Container.NetworkContainer != "gateway" {
		t.Fatalf("snapshot lost: %+v", got)
	}
	spec := e.specs[len(e.specs)-1]
	if spec.MemoryBytes != 123 || spec.NanoCPUs != 456 || spec.Restart != "no" || spec.Network != "" {
		t.Fatalf("unsafe consumer: %+v", spec)
	}
	if got.State != "running" || got.Error != "" || got.VPN.State == "blocked" {
		t.Fatalf("start did not recover: %+v", got)
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
	if err != nil || addr != "127.0.0.1:"+strconv.Itoa(s.Cfg.DesktopPort) || token == "" {
		t.Fatalf("target %s %v", addr, err)
	}
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
		t.Fatalf("health changed computer power: %+v", after)
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
func TestVPNRoleNeverListedAsComputer(t *testing.T) {
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
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
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

// defineVPN saves name's definition as selecting connection, with no
// transition recorded.
func defineVPN(t *testing.T, s *Service, name, connection string) {
	t.Helper()
	if err := s.Defs.Put(Definition{Name: name, Created: time.Now().UTC(), Revision: 1, Image: ImageRef{Ref: "img", ID: "img"},
		Network: Network{Kind: NetworkVPN, Connection: connection}, MemoryReclaim: true, Power: PowerRunning}); err != nil {
		t.Fatal(err)
	}
}

// The definition is the authority: one that selects a VPN with no recorded
// transition (its direct container predates it) is applied fail closed.
func TestVPNDefinitionWithoutJournalAppliesFailClosed(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Defs.Update("alice", func(d *Definition) error {
		d.Network = Network{Kind: NetworkVPN, Connection: conn.ID}
		d.Revision++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !s.Networking.selected("alice") {
		t.Fatal("VPN definition not managed by the coordinator")
	}
	got, _ := s.Get(ctx, "alice")
	if got.Settings.VPNConnectionID != conn.ID {
		t.Fatalf("VPN selection lost: %+v", got)
	}
	if err := s.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	spec := e.specs[len(e.specs)-1]
	if spec.NetworkContainer != "gateway" || spec.Network != "" || spec.Restart != "no" || spec.Labels[LabelRevision] != "2" {
		t.Fatalf("started without its namespace: %+v", spec)
	}
	if r, ok := s.Networking.record("alice"); !ok || r.AppliedRevision != 2 || r.Pending {
		t.Fatalf("transition: %+v", r)
	}
}
func TestVPNDisableLeavesNoTransition(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	ctx := context.Background()
	rules := []vpn.Exception{{Destination: "192.168.1.10", Protocol: "tcp", Ports: []vpn.PortRange{{Start: 631}}}}
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID, LANExceptions: rules}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if def, _ := s.Defs.Get("alice"); len(def.Network.LANExceptions) != 1 {
		t.Fatalf("exceptions not in the definition: %+v", def.Network)
	}
	set := d.Settings
	set.VPNConnectionID = ""
	if _, err = s.Recreate(ctx, "alice", set, noProgress); err != nil {
		t.Fatal(err)
	}
	// The journal holds transitions only: a completed one is forgotten, and
	// an isolated definition has no exceptions.
	if r, ok := s.Networking.record("alice"); ok {
		t.Fatalf("completed transition kept: %+v", r)
	}
	if def, _ := s.Defs.Get("alice"); def.Network.Kind != NetworkIsolated || len(def.Network.LANExceptions) != 0 {
		t.Fatalf("isolated definition: %+v", def.Network)
	}
	if s.Networking.selected("alice") {
		t.Fatal("direct computer still managed by the coordinator")
	}
}
func TestVPNConsumerCannotBeCreatedWithoutNamespace(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	err := s.seed(context.Background(), "alice", Settings{Image: "img", VPNConnectionID: conn.ID})
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
	replacement.Labels = map[string]string{LabelManaged: "true", LabelName: "alice", LabelInstance: "somebody-else"}
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
	// A failed change waits for a start, restart or edit, not startup.
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if e.networkCalls != calls {
		t.Fatal("startup retried a failed creation")
	}
	if err := s.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if e.networkCalls == calls {
		t.Fatal("recovery never recreated missing computer network")
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
	defineVPN(t, s, "alice", conn.ID)
	r := networkTransition{Name: "alice", Pending: true}
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
				n.setStatus(r.Name, vpn.Status{State: "connecting"})
				n.References(conn.ID)
				n.decorate(Computer{Name: r.Name})
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
	// The assignment is the definition selecting the connection.
	defineVPN(t, s, "alice", conn.ID)
	n.refs.Unlock()
	if err := <-done; !errors.Is(err, vpn.ErrConflict) {
		t.Fatalf("assignment raced deletion: %v", err)
	}
}
func TestVPNReferencesIncludeOrphanedLabels(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	e.cs["deskyard-alice"].Labels[vpn.LabelConnection] = conn.ID
	if err := s.Networking.DeleteConnection(conn.ID); !errors.Is(err, vpn.ErrConflict) {
		t.Fatalf("orphaned selected computer did not prevent delete: %v", err)
	}
}
func TestVPNGatewayEventsWakeCentralObserverOnlyForOwnedRecords(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	n := s.Networking
	defineVPN(t, s, "alice", conn.ID)
	n.NotifyEngineEvent(engine.Event{Action: "die", Labels: map[string]string{vpn.LabelRole: "gateway", vpn.LabelComputer: "foreign"}})
	select {
	case <-n.wake:
		t.Fatal("foreign gateway triggered reconciliation")
	default:
	}
	n.NotifyEngineEvent(engine.Event{Action: "die", Labels: map[string]string{vpn.LabelRole: "gateway", vpn.LabelComputer: "alice", vpn.LabelInstance: "another"}})
	select {
	case <-n.wake:
		t.Fatal("another instance's gateway triggered reconciliation")
	default:
	}
	// Execs (the supervisor's own probes, healthchecks) change no state.
	for _, action := range []string{"exec_create: sh -ec probe", "exec_start: sh -ec probe", "exec_die", "health_status: healthy"} {
		n.NotifyEngineEvent(engine.Event{Action: action, Labels: map[string]string{vpn.LabelRole: "gateway", vpn.LabelComputer: "alice"}})
		select {
		case <-n.wake:
			t.Fatalf("%s event triggered reconciliation", action)
		default:
		}
	}
	n.NotifyEngineEvent(engine.Event{Action: "die", Labels: map[string]string{vpn.LabelRole: "gateway", vpn.LabelComputer: "alice"}})
	select {
	case <-n.wake:
	default:
		t.Fatal("owned gateway event ignored")
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

// A definition saved ahead of the applied transition (an interrupted change)
// wins: its revision is applied, fail closed, with the runtime prepared before
// the new container.
func TestDefinitionWinsOverStaleTransition(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Defs.Update("alice", func(d *Definition) error {
		d.Limits.MemoryBytes = 789
		d.Revision++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Networking.record("alice"); r.AppliedRevision != 1 || r.Pending {
		t.Fatalf("transition: %+v", r)
	}
	fresh := reopen(t, s)
	if err := fresh.ConfigureNetworking(s.Networking.Connections, s.Networking.runtime, s.Layout.State); err != nil {
		t.Fatal(err)
	}
	calls := len(e.calls)
	if err := fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	spec := e.specs[len(e.specs)-1]
	if spec.MemoryBytes != 789 || spec.Labels[LabelRevision] != "2" || spec.NetworkContainer != "gateway" || spec.Network != "" || spec.Restart != "no" {
		t.Fatalf("stale transition applied instead of the definition: %+v", spec)
	}
	if got := strings.Join(e.calls[calls:], ","); got != "remove-desktop,remove-runtime,ensure,create" {
		t.Fatalf("not fail-closed ordering: %s", got)
	}
	got, _ := fresh.Get(ctx, "alice")
	if r, _ := fresh.Networking.record("alice"); r.AppliedRevision != 2 || r.Pending || got.AppliedRevision != 2 || !got.Container.Running() {
		t.Fatalf("definition not applied: %+v %+v", r, got)
	}
}

// A definition whose connection was deleted keeps the computer stopped,
// with the error, and never starts it on direct networking.
func TestDeletedConnectionKeepsStopped(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Networking.Connections.Delete(conn.ID); err != nil {
		t.Fatal(err)
	}
	specs := len(e.specs)
	if err = s.Reconcile(ctx); err == nil || !strings.Contains(err.Error(), "connection-read") {
		t.Fatalf("deleted connection not reported: %v", err)
	}
	got, _ := s.Get(ctx, "alice")
	if got.Container.Running() || got.State != "stopped" || got.Error == "" || got.VPN.State != "blocked" || got.Settings.VPNConnectionID != conn.ID {
		t.Fatalf("not kept stopped with the error: %+v", got)
	}
	if err = s.Start(ctx, "alice"); err == nil {
		t.Fatal("started without its connection")
	}
	s.Networking.Poll(ctx, nil)
	got, _ = s.Get(ctx, "alice")
	if got.Container.Running() || got.Container.ID != d.Container.ID || len(e.specs) != specs {
		t.Fatalf("recreated or started without its connection: %+v", got)
	}
}

// No failure while moving to a VPN creates or starts the computer on direct
// networking: the old container is gone and nothing replaces it until the
// runtime is up.
func TestVPNNeverDirectOnFailure(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx, "deskyard-alice"); err != nil {
		t.Fatal(err)
	}
	specs := len(e.specs)
	r.fail = true
	if _, err := s.Recreate(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err == nil {
		t.Fatal("expected runtime failure")
	}
	direct := func(when string) {
		t.Helper()
		for _, spec := range e.specs[specs:] {
			if spec.NetworkContainer == "" {
				t.Fatalf("%s: created on direct networking: %+v", when, spec)
			}
		}
		if c, err := e.Inspect(ctx, "deskyard-alice"); err == nil && c.NetworkContainer == "" {
			t.Fatalf("%s: direct container left: %+v", when, c)
		}
	}
	direct("recreate")
	if err := s.Start(ctx, "alice"); err == nil {
		t.Fatal("started without its runtime")
	}
	direct("start")
	// Startup leaves the failed change for a start, restart or edit.
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	s.Networking.Poll(ctx, nil)
	direct("reconcile")
	got, _ := s.Get(ctx, "alice")
	if got.Settings.VPNConnectionID != conn.ID || got.VPN.State != "blocked" || got.Running() {
		t.Fatalf("selection or diagnostic lost: %+v", got)
	}
	r.fail = false
	if err := s.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	direct("recovery")
	if got, _ = s.Get(ctx, "alice"); !got.Container.Running() || got.Container.NetworkContainer != "gateway" {
		t.Fatalf("not recovered into the namespace: %+v", got)
	}
}

// Without the test seam the proxy dials the desktop's address on its own port:
// the container's IP, or its name when the engine reported none.
func TestTargetUsesContainerAddressAndDefaultPort(t *testing.T) {
	s, e, _, _ := pairSetup(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img"}, noProgress); err != nil {
		t.Fatal(err)
	}
	s.Cfg.DesktopPort = 0
	if addr, _, err := s.Target(ctx, "alice"); err != nil || addr != "127.0.0.1:8080" {
		t.Fatalf("with an address: %s %v", addr, err)
	}
	e.cs["deskyard-alice"].IP = map[string]string{}
	if addr, _, err := s.Target(ctx, "alice"); err != nil || addr != "deskyard-alice:8080" {
		t.Fatalf("without an address: %s %v", addr, err)
	}
}

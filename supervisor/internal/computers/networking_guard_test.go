package computers

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// A failed VPN apply is not retried by the poll; a start retries it.
func TestVPNPollDoesNotRetryFailedApply(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	r.fail = true
	if _, err := s.Recreate(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err == nil {
		t.Fatal("expected runtime failure")
	}
	r.fail = false
	calls := len(e.calls)
	for range 3 {
		s.Networking.Poll(ctx, nil)
	}
	for _, call := range e.calls[calls:] {
		if call == "ensure" || call == "create" {
			t.Fatalf("poll retried a failed apply: %v", e.calls[calls:])
		}
	}
	if err := s.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, "alice"); !got.Container.Running() || got.Container.NetworkContainer != "gateway" {
		t.Fatalf("start did not retry the apply: %+v", got)
	}
}

// A failed repair of the applied state (a lost gateway) is retried by the
// poll, unlike a failed apply of a change.
func TestVPNPollRetriesFailedRepair(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	if err := e.fakeEngine.Remove(ctx, "gateway"); err != nil {
		t.Fatal(err)
	}
	r.fail = true
	s.Networking.Poll(ctx, nil)
	if rec, _ := s.Networking.record("alice"); rec.Error == "" || !rec.Repair {
		t.Fatalf("failed repair not recorded as one: %+v", rec)
	}
	r.fail = false
	s.Networking.Poll(ctx, nil)
	got, _ := s.Get(ctx, "alice")
	if rec, _ := s.Networking.record("alice"); rec.Pending || rec.Error != "" || rec.Repair {
		t.Fatalf("repair not finished: %+v", rec)
	}
	if !got.Container.Running() || got.Container.NetworkContainer != "gateway" {
		t.Fatalf("poll did not repair the lost gateway: %+v", got.Container)
	}
}

// A connection edit leaves a stopped computer without a container
// alone; its start applies the new revision.
func TestVPNRolloutSkipsStoppedWithoutContainer(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	def := withSettings(Definition{Name: "bob", Created: time.Now().UTC(), Power: PowerStopped}, Settings{Image: "img", VPNConnectionID: conn.ID}, "img")
	if err := s.Defs.Put(def); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.Layout.HomeOf("bob"), 0o755); err != nil {
		t.Fatal(err)
	}
	updated, _, err := s.Networking.UpdateConnection(ctx, conn.ID, conn.Revision, []string{"bob"}, vpn.Input{Name: "Changed", Provider: "nordvpn", Country: "Germany"}, noProgress, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Inspect(ctx, containerName("bob")); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("rollout created a stopped computer's container: %v", err)
	}
	if rec, _ := s.Networking.record("bob"); rec.Pending || rec.ConnectionRevision != updated.Revision {
		t.Fatalf("rollout record: %+v", rec)
	}
	if err = s.Start(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "bob")
	if rec, _ := s.Networking.record("bob"); !got.Container.Running() || rec.AppliedConnectionRevision != updated.Revision {
		t.Fatalf("start did not apply the new revision: %+v %+v", got.Container, rec)
	}
}

// A replacement journal retained from a failed direct restore blocks a change
// to a VPN, and is never restored under a definition that selects one.
func TestVPNNeverRestoresParkedDirectContainer(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Rename(ctx, containerName("alice"), parkedName("alice")); err != nil {
		t.Fatal(err)
	}
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx, containerName("alice")); err != nil {
		t.Fatal(err)
	}
	if err := s.saveReplacement(replacement{Version: 1, Name: "alice", Revision: 7, Parked: parkedName("alice"), WasRunning: true}); err != nil {
		t.Fatal(err)
	}
	calls := len(e.calls)
	if _, err := s.Recreate(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err == nil {
		t.Fatal("change to a VPN accepted with a replacement pending")
	}
	if len(e.calls) != calls {
		t.Fatalf("refused change touched the engine: %v", e.calls[calls:])
	}
	// The definition moves to the VPN anyway (edited on disk).
	if _, err := s.Defs.Update("alice", func(d *Definition) error {
		d.Network = Network{Kind: NetworkVPN, Connection: conn.ID}
		d.Revision++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fresh := reopen(t, s)
	if err := fresh.ConfigureNetworking(s.Networking.Connections, s.Networking.runtime, s.Layout.State); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Inspect(ctx, parkedName("alice")); err == nil {
		t.Fatal("parked direct container kept")
	}
	if _, err := os.Stat(recoveryFile(s)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement journal kept: %v", err)
	}
	cs, _ := e.List(ctx, nil)
	for _, c := range cs {
		if c.Running() && c.NetworkContainer == "" && IsComputer(c.Labels) {
			t.Fatalf("running on direct networking: %+v", c)
		}
	}
	if got, _ := fresh.Get(ctx, "alice"); !got.Container.Running() || got.Container.NetworkContainer != "gateway" {
		t.Fatalf("VPN definition not applied: %+v", got)
	}
}

// A removal still being finished blocks a new computer of its name.
func TestCreateRefusesLeftoverTransition(t *testing.T) {
	s, _, _, _ := pairSetup(t)
	ctx := context.Background()
	if err := s.Networking.saveRecord(networkTransition{Name: "bob", Pending: true, Removing: true, DeleteFiles: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "bob", Settings{Image: "img"}, noProgress); !errors.Is(err, ErrExists) {
		t.Fatalf("create over a pending removal: %v", err)
	}
	if _, ok := s.Defs.Get("bob"); ok {
		t.Fatal("definition saved")
	}
}

// A refused VPN start leaves the saved power as it was.
func TestVPNRefusedStartKeepsPower(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := s.Networking.Connections.Delete(conn.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ctx, "alice"); err == nil {
		t.Fatal("started without its connection")
	}
	if def, _ := s.Defs.Get("alice"); def.Power != PowerStopped {
		t.Fatalf("refused start saved power %s", def.Power)
	}
}

// A VPN change that fails validation leaves the running computer alone.
func TestVPNInvalidChangeKeepsRunning(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Recreate(ctx, "alice", Settings{Image: "img", VPNConnectionID: "missing"}, noProgress); err == nil {
		t.Fatal("unknown connection accepted")
	}
	got, _ := s.Get(ctx, "alice")
	if !got.Container.Running() || got.Container.ID != d.Container.ID || got.Container.Restart != d.Container.Restart {
		t.Fatalf("rejected change touched the computer: %+v", got.Container)
	}
}

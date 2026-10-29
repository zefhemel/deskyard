package computers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// A container using the connection whose definition doesn't select it is not
// silently skipped or rolled out: it needs repair.
func TestVPNRolloutRejectsUnselectedReference(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	e.cs["deskyard-alice"].Labels[vpn.LabelConnection] = conn.ID
	refs := s.Networking.References(conn.ID)
	_, results, err := s.Networking.UpdateConnection(ctx, conn.ID, 1, []string{refs[0].ID}, vpn.Input{Name: "Changed", Provider: "nordvpn", Country: "Germany"}, noProgress, nil)
	saved, _ := s.Networking.Connections.Get(conn.ID)
	if err == nil || !strings.Contains(err.Error(), "repair") || saved.Revision != 1 {
		t.Fatalf("orphan silently applied: revision=%d results=%+v error=%v", saved.Revision, results, err)
	}
	got, _ := s.Get(ctx, "alice")
	if got.Settings.VPNConnectionID != "" || len(e.specs) != 1 {
		t.Fatalf("orphan changed policy: %+v", got)
	}
}

type phaseEngine struct {
	*pairEngine
	phase string
}

func (e *phaseEngine) Stop(ctx context.Context, id string, d time.Duration) error {
	if e.phase == "consumer-stop" {
		return errors.New("private-token /secret/path provider-password")
	}
	return e.pairEngine.Stop(ctx, id, d)
}
func (e *phaseEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	if e.phase == "consumer-create" {
		return "", errors.New("private-token /secret/path provider-password")
	}
	return e.pairEngine.Create(ctx, s)
}
func TestVPNRecoveryFailurePhases(t *testing.T) {
	for _, phase := range []string{"consumer-stop", "consumer-create", "runtime-ensure"} {
		t.Run(phase, func(t *testing.T) {
			s, e, r, conn := pairSetup(t)
			ctx := context.Background()
			d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
			if err != nil {
				t.Fatal(err)
			}
			s.Eng = &phaseEngine{e, phase}
			r.fail = phase == "runtime-ensure"
			intent, _ := s.Networking.record("alice")
			intent.Pending = true
			if err = s.Networking.saveRecord(intent); err != nil {
				t.Fatal(err)
			}
			err = s.Networking.Reconcile(ctx)
			if err == nil || !strings.Contains(err.Error(), phase) {
				t.Fatalf("missing safe phase %s: %v", phase, err)
			}
			persisted, _ := s.Networking.record("alice")
			got, _ := s.Get(ctx, "alice")
			if !strings.Contains(persisted.Error, phase) || got.VPN.State != "blocked" || got.Settings.VPNConnectionID != conn.ID || got.ID != d.ID {
				t.Fatalf("lost diagnostic or policy: %+v %+v", persisted, got.VPN)
			}
			for _, secret := range []string{"private-token", "/secret/path", "provider-password", "provider secret"} {
				if strings.Contains(err.Error()+persisted.Error+got.VPN.Reason, secret) {
					t.Fatal("raw cause leaked")
				}
			}
			// Reload the actual failed journal, retain its safe diagnostic, and
			// retry the same selected policy rather than discarding the state.
			stateDir := filepath.Dir(filepath.Dir(s.Networking.path))
			if err = s.ConfigureNetworking(s.Networking.Connections, r, stateDir); err != nil {
				t.Fatal(err)
			}
			got, _ = s.Get(ctx, "alice")
			if !strings.Contains(got.VPN.Reason, phase) {
				t.Fatalf("phase lost on restart: %+v", got.VPN)
			}
			s.Eng = e
			r.fail = false
			// Startup leaves the failed change; a start retries it.
			if err = s.Networking.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			if got, _ = s.Get(ctx, "alice"); got.VPN.State != "blocked" {
				t.Fatalf("startup retried a failed change: %+v", got.VPN)
			}
			if err = s.Start(ctx, "alice"); err != nil {
				t.Fatal(err)
			}
			got, _ = s.Get(ctx, "alice")
			if got.VPN.State == "blocked" || got.Settings.VPNConnectionID != conn.ID || got.Container.NetworkContainer == "" {
				t.Fatalf("retry did not recover selected policy: %+v", got)
			}

		})
	}
}

func TestVPNRolloutCancellationWhileWaitingForReferences(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	n := s.Networking
	n.refs.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := n.UpdateConnection(ctx, conn.ID, 1, nil, vpn.Input{Name: "Changed", Provider: "nordvpn", Country: "Germany"}, noProgress, nil)
		done <- err
	}()
	select {
	case err := <-done:
		n.refs.Unlock()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("reference wait cancellation: %v", err)
		}
	case <-time.After(time.Second):
		n.refs.Unlock()
		<-done
		t.Fatal("cancelled edit waited for reference lock")
	}
	saved, _ := n.Connections.Get(conn.ID)
	if saved.Revision != 1 {
		t.Fatalf("cancelled edit committed revision %d", saved.Revision)
	}
}

func TestVPNRolloutDetachesOnlyAfterCommit(t *testing.T) {
	s, _, _, conn := pairSetup(t)
	d, err := s.Create(context.Background(), "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updated, results, err := s.Networking.UpdateConnection(ctx, conn.ID, 1, []string{d.ID}, vpn.Input{Name: "Changed", Provider: "nordvpn", Country: "Germany"}, noProgress, func(save func() error) error {
		if err := save(); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if err != nil || updated.Revision != 2 || len(results) != 1 || results[0].Error != "" {
		t.Fatalf("committed rollout abandoned: %+v %+v %v", updated, results, err)
	}
	got, _ := s.Get(context.Background(), "alice")
	if got.VPN.AppliedRevision != 2 || got.Container.NetworkContainer == "" {
		t.Fatalf("revision not attached: %+v", got)
	}
}

// exitEngine lets the next starts of deskyard-alice exit at once, as a
// desktop that never becomes ready does, and counts power actions.
type exitEngine struct {
	*pairEngine
	exits            int
	starts, restarts int
}

func (e *exitEngine) Start(ctx context.Context, id string) error {
	e.starts++
	if err := e.pairEngine.Start(ctx, id); err != nil || e.exits == 0 {
		return err
	}
	e.exits--
	return e.pairEngine.Stop(ctx, id, 0)
}
func (e *exitEngine) Restart(ctx context.Context, id string, d time.Duration) error {
	e.restarts++
	return e.pairEngine.Restart(ctx, id, d)
}

// A change applied whose container never becomes ready stays reported after
// the poll restores its power, without the poll applying it again; a start
// clears it.
func TestVPNReadinessFailureAfterApplyStaysReported(t *testing.T) {
	for _, toDirect := range []bool{false, true} {
		name := "vpn-edit"
		if toDirect {
			name = "vpn-to-direct"
		}
		t.Run(name, func(t *testing.T) {
			s, e, _, conn := pairSetup(t)
			ctx := context.Background()
			d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
			if err != nil {
				t.Fatal(err)
			}
			x := &exitEngine{pairEngine: e, exits: 1}
			s.Eng = x
			set := d.Settings
			set.MemoryBytes = 1 << 30
			if toDirect {
				set.VPNConnectionID = ""
			}
			if _, err = s.Recreate(ctx, "alice", set, noProgress); err == nil {
				t.Fatal("expected readiness failure")
			}
			creates := len(e.specs)
			s.Networking.Poll(ctx, nil)
			got, _ := s.Get(ctx, "alice")
			if got.Error == "" || !strings.Contains(got.Error, "ready") {
				t.Fatalf("readiness failure not reported: %+v", got)
			}
			if !toDirect && !got.Container.Running() {
				t.Fatal("poll did not restore the exited container")
			}
			if len(e.specs) != creates {
				t.Fatal("poll applied the change again")
			}
			if err = s.Start(ctx, "alice"); err != nil {
				t.Fatal(err)
			}
			if got, _ = s.Get(ctx, "alice"); got.Error != "" || !got.Container.Running() {
				t.Fatalf("start did not clear the error: %+v", got)
			}
		})
	}
}

// A restart of an exited container boots it once: recover's start is the
// restart.
func TestVPNRestartOfExitedContainerBootsOnce(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	if err := e.Stop(ctx, "deskyard-alice", 0); err != nil {
		t.Fatal(err)
	}
	x := &exitEngine{pairEngine: e}
	s.Eng = x
	if err := s.Restart(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if x.starts+x.restarts != 1 {
		t.Fatalf("booted %d times", x.starts+x.restarts)
	}
	if got, _ := s.Get(ctx, "alice"); !got.Container.Running() {
		t.Fatal("not running")
	}
}

// A deleted connection stops the computer once and records it; the poll
// then leaves it, instead of rewriting the journal on every pass. A stop
// that fails is retried by the poll, never reported as stopped.
func TestVPNDeletedConnectionStopsOnceAndRetriesFailedStop(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	if err := s.Networking.Connections.Delete(conn.ID); err != nil {
		t.Fatal(err)
	}
	s.Eng = &phaseEngine{e, "consumer-stop"}
	s.Networking.Poll(ctx, nil)
	r, _ := s.Networking.record("alice")
	if !strings.Contains(r.Error, "consumer-stop") || !r.Repair {
		t.Fatalf("failed stop not recorded for retry: %+v", r)
	}
	s.Eng = e
	s.Networking.Poll(ctx, nil)
	got, _ := s.Get(ctx, "alice")
	r, _ = s.Networking.record("alice")
	if got.Container.Running() || !strings.Contains(r.Error, "connection-read") || r.Repair {
		t.Fatalf("stop not retried: %+v %+v", r, got.Container)
	}
	info, err := os.Stat(s.Networking.path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	s.Networking.Poll(ctx, nil)
	if again, _ := os.Stat(s.Networking.path); !again.ModTime().Equal(info.ModTime()) {
		t.Fatal("poll rewrote the journal for a gone connection")
	}
}

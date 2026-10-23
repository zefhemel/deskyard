package desktops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

func TestVPNRolloutRejectsUnjournaledReference(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if err := s.createContainer(ctx, "alice", "012345678901234567890123456789ab", "token", time.Now(), Settings{Image: "img"}); err != nil {
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
	if got.Settings.VPNConnectionID != conn.ID || got.VPN.State != "blocked" || len(e.specs) != 1 {
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
			if err = s.Networking.Reconcile(ctx); err != nil {
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

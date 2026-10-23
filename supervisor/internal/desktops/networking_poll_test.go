package desktops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestVPNPollRestoresDesiredPowerWithoutReplacingGateway(t *testing.T) {
	for _, stop := range []bool{false, true} {
		name := "unexpected-exit"
		if stop {
			name = "failed-stop"
		}
		t.Run(name, func(t *testing.T) {
			s, e, _, conn := pairSetup(t)
			ctx := context.Background()
			d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
			if err != nil {
				t.Fatal(err)
			}
			if stop {
				s.Eng = &phaseEngine{e, "consumer-stop"}
				if err = s.Stop(ctx, "alice"); err == nil {
					t.Fatal("stop must fail")
				}
				s.Eng = e
			} else if err = e.Stop(ctx, d.Container.ID, 0); err != nil {
				t.Fatal(err)
			}
			intent, _ := s.Networking.record("alice")
			if intent.Running == stop {
				t.Fatal("durable desired power lost")
			}
			calls := len(e.calls)
			s.Networking.Poll(ctx, nil)
			got, err := s.Get(ctx, "alice")
			if err != nil {
				t.Fatal(err)
			}
			if got.Container.Running() == stop {
				t.Errorf("power mismatch remains: %s", got.Container.State)
			}
			if got.Container.ID != d.Container.ID || got.Container.NetworkContainer != d.Container.NetworkContainer {
				t.Fatal("power repair replaced consumer or namespace")
			}
			for _, call := range e.calls[calls:] {
				if call == "remove-runtime" || call == "remove-desktop" || call == "create" {
					t.Fatalf("power repair replaced healthy pair: %s", call)
				}
			}
			// A subsequent ordinary health poll must preserve both desired stopped
			// state and applications running during an ordinary tunnel reconnect.
			s.Networking.Poll(ctx, nil)
			got, _ = s.Get(ctx, "alice")
			if got.Container.Running() == stop {
				t.Fatal("health observation changed desired power")
			}
		})
	}
}

func TestVPNPollRecoversExplicitDisableAndShowsDiagnostic(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	s.Eng = &phaseEngine{e, "consumer-create"}
	set := d.Settings
	set.VPNConnectionID = ""
	if _, err = s.Recreate(ctx, "alice", set, noProgress); err == nil {
		t.Fatal("disable must fail")
	}
	intent, _ := s.Networking.record("alice")
	if !intent.Pending || intent.Settings.VPNConnectionID != "" {
		t.Fatal("explicit disable intent lost")
	}
	got, err := s.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.VPN.State != "blocked" || !strings.Contains(got.VPN.Reason, "consumer-create") {
		t.Errorf("failed disable hidden: %+v", got.VPN)
	}
	if strings.Contains(got.VPN.Reason, "private-token") || strings.Contains(got.VPN.Reason, "/secret/path") {
		t.Fatal("raw transition cause exposed")
	}
	s.Eng = e
	changed := false
	s.Networking.Poll(ctx, func(name string) { changed = name == "alice" })
	got, err = s.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	intent, _ = s.Networking.record("alice")
	if !got.Container.Running() || got.ID != d.ID || got.Container.NetworkContainer != "" || got.Container.Restart != "unless-stopped" || got.Settings.VPNConnectionID != "" || intent.Pending || intent.Error != "" {
		t.Errorf("explicit direct intent not recovered: desktop=%+v intent=%+v", got, intent)
	}
	if got.VPN.State != "disabled" || got.VPN.Reason != "" || !changed {
		t.Errorf("completed disable not published: %+v changed=%v", got.VPN, changed)
	}
}

type removeFailureEngine struct{ *pairEngine }

func (e *removeFailureEngine) Remove(context.Context, string) error { return errors.New("unavailable") }

func TestVPNPollFinishesPendingDirectRemoval(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	set := d.Settings
	set.VPNConnectionID = ""
	if _, err = s.Recreate(ctx, "alice", set, noProgress); err != nil {
		t.Fatal(err)
	}
	s.Eng = &removeFailureEngine{e}
	if err = s.Remove(ctx, "alice", false); err == nil {
		t.Fatal("removal must fail")
	}
	intent, _ := s.Networking.record("alice")
	if !intent.Removing {
		t.Fatal("removal intent lost")
	}
	s.Eng = e
	s.Networking.Poll(ctx, nil)
	if _, err = e.Inspect(ctx, d.Container.ID); !errors.Is(err, engine.ErrNotFound) {
		t.Errorf("pending removal kept consumer: %v", err)
	}
	if _, ok := s.Networking.record("alice"); ok {
		t.Error("pending removal kept recovery record")
	}
}

// Fail only the final journal write, after resource removal has succeeded.
type removalPersistenceEngine struct {
	*pairEngine
	beforeNetworkRemoval func()
}

func (e *removalPersistenceEngine) RemoveNetwork(ctx context.Context, name string) error {
	e.beforeNetworkRemoval()
	return e.pairEngine.RemoveNetwork(ctx, name)
}
func TestVPNRemovalPersistenceFailureRetainsSafeRetry(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	journalPath := s.Networking.path
	blocked := filepath.Join(t.TempDir(), "private-state-sentinel")
	if err = os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	s.Eng = &removalPersistenceEngine{e, func() { s.Networking.path = filepath.Join(blocked, "desktops.json") }}
	err = s.Remove(ctx, "alice", false)
	if err == nil {
		t.Fatal("failed persistence reported successful removal")
	}
	for _, private := range []string{blocked, "desktops.json", "not a directory"} {
		if strings.Contains(err.Error(), private) {
			t.Errorf("removal exposed private persistence cause: %v", err)
		}
	}
	intent, ok := s.Networking.record("alice")
	if !ok || !intent.Removing {
		t.Fatal("failed final persistence forgot retry intent")
	}
	if _, err = e.Inspect(ctx, d.Container.ID); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("test did not reach final persistence")
	}
	s.Eng = e
	s.Networking.path = journalPath
	s.Networking.Poll(ctx, nil)
	if _, ok = s.Networking.record("alice"); ok {
		t.Fatal("final persistence retry retained removed desktop")
	}
}

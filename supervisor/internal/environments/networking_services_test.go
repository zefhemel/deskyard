package environments

import (
	"context"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailscaleServiceEditDoesNotRecreateStoppedComputer(t *testing.T) {
	s, e, _, _ := pairSetup(t)
	secret := "oauth-secret"
	c, err := s.Networking.Connections.Create(vpn.Input{Name: "Tailnet", Provider: "tailscale", Tailscale: &vpn.TailscaleInput{Tailnet: "home", ClientID: "id", ClientSecret: &secret, Tags: []string{"tag:deskyard"}}})
	if err != nil {
		t.Fatal(err)
	}
	def := Definition{Name: "alice", Created: time.Now(), Revision: 1, Image: ImageRef{Ref: "img", ID: "img"}, Network: Network{Kind: NetworkVPN, Connection: c.ID}, Power: PowerStopped}
	if err = s.Defs.Put(def); err != nil {
		t.Fatal(err)
	}
	before := len(e.specs)
	rules := []vpn.ServiceRule{{LocalPort: 3000, ListenPort: 443, Audience: "private"}}
	if err = s.Networking.UpdateServices(context.Background(), "alice", rules); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.Defs.Get("alice")
	if saved.Revision != 1 || saved.Power != PowerStopped || len(saved.Network.Services) != 1 || len(e.specs) != before {
		t.Fatalf("rule edit recreated computer: %+v", saved)
	}
	saved.Network.Services[0].LocalPort = 4000
	again, _ := s.Defs.Get("alice")
	if again.Network.Services[0].LocalPort != 3000 {
		t.Fatal("definition shares mutable rules")
	}
	if err = s.Networking.UpdateServices(context.Background(), "alice", nil); err != nil {
		t.Fatal(err)
	}
	saved, _ = s.Defs.Get("alice")
	if len(saved.Network.Services) != 0 {
		t.Fatal("explicit clear ignored")
	}
}

type servicePairRuntime struct {
	*pairRuntime
	stopped  bool
	failStop bool
}

func (r *servicePairRuntime) ApplyServices(context.Context, string, []vpn.ServiceRule) ([]vpn.ServiceStatus, error) {
	r.e.calls = append(r.e.calls, "services")
	return nil, nil
}
func (r *servicePairRuntime) Stop(context.Context, string) error {
	if r.failStop {
		return errors.New("transient stop")
	}
	r.stopped = true
	r.gateway.State = "exited"
	return nil
}

type orderedPairEngine struct{ *pairEngine }

func (e *orderedPairEngine) Start(ctx context.Context, id string) error {
	e.calls = append(e.calls, "start:"+id)
	return e.fakeEngine.Start(ctx, id)
}
func TestTailscaleServicesAppliedBeforeConsumerStart(t *testing.T) {
	s, e, r, _ := pairSetup(t)
	sr := &servicePairRuntime{pairRuntime: r}
	s.Networking.runtime = sr
	s.Eng = &orderedPairEngine{e}
	secret := "private"
	c, err := s.Networking.Connections.Create(vpn.Input{Name: "TS", Provider: "tailscale", Tailscale: &vpn.TailscaleInput{Tailnet: "home", ClientID: "id", ClientSecret: &secret, Tags: []string{"tag:desk"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: c.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	assertBefore := func() {
		t.Helper()
		service, start := -1, -1
		for i, call := range e.calls {
			if call == "services" && service < 0 {
				service = i
			}
			if call == "start:deskyard-alice" && start < 0 {
				start = i
			}
		}
		if service < 0 || start < 0 || service > start {
			t.Fatalf("publication reconciled after app started: %v", e.calls)
		}
	}
	assertBefore()
	if err = s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err = s.Networking.UpdateServices(ctx, "alice", []vpn.ServiceRule{{LocalPort: 3000, ListenPort: 443, Audience: "private"}}); err != nil {
		t.Fatal(err)
	}
	e.calls = nil
	if err = s.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	assertBefore()
}

func TestTailscaleConnectionDeletionKeepsUnretiredCredentials(t *testing.T) {
	s, e, _, _ := pairSetup(t)
	secret := "private"
	c, err := s.Networking.Connections.Create(vpn.Input{Name: "TS", Provider: "tailscale", Tailscale: &vpn.TailscaleInput{Tailnet: "home", ClientID: "id", ClientSecret: &secret, Tags: []string{"tag:desk"}}})
	if err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(s.Layout.Secret, "tailscale", "nodes", "alice", c.ID)
	if err = os.MkdirAll(node, 0700); err != nil {
		t.Fatal(err)
	}
	s.Networking.runtime = vpn.NewManager(e, s.Networking.Connections, s.Layout.State).ForInstance(s.Instance)
	if err = s.Networking.DeleteConnection(c.ID); !errors.Is(err, vpn.ErrConflict) {
		t.Fatalf("unretired connection deleted: %v", err)
	}
	if _, err = s.Networking.Connections.SecretFiles(c.ID); err != nil {
		t.Fatal("retirement credential lost")
	}
}
func TestTailscaleFailedSidecarStopIsRetried(t *testing.T) {
	s, e, r, _ := pairSetup(t)
	sr := &servicePairRuntime{pairRuntime: r}
	s.Networking.runtime = sr
	secret := "private"
	c, err := s.Networking.Connections.Create(vpn.Input{Name: "TS", Provider: "tailscale", Tailscale: &vpn.TailscaleInput{Tailnet: "home", ClientID: "id", ClientSecret: &secret, Tags: []string{"tag:desk"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: c.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	sr.failStop = true
	if err = s.Stop(ctx, "alice"); err == nil {
		t.Fatal("failed sidecar stop was hidden")
	}
	sr.failStop = false
	s.Networking.Poll(ctx, nil)
	g, _ := e.Inspect(ctx, r.gateway.ID)
	if g.Running() {
		t.Fatal("stopped computer's sidecar left online after retry")
	}
}

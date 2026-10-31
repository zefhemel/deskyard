package computers

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
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

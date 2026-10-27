package environments

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// vpnImageSetup is a running VPN environment alice, created from img
// (sha256:img), whose pinned image is then gone from the engine, and a
// pending repair of its pair, as after its gateway was lost.
func vpnImageSetup(t *testing.T, power Power) (*Service, *definitionEngine, *pairEngine) {
	t.Helper()
	s, _, p := definitionService(t)
	ctx := context.Background()
	conns, err := s.Networking.Connections.List()
	if err != nil || len(conns) != 1 {
		t.Fatal(conns, err)
	}
	if _, err = s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conns[0].ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	if power == PowerStopped {
		if err = s.Stop(ctx, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	de := s.Eng.(*definitionEngine)
	de.missing = map[string]bool{"sha256:img": true}
	r, _ := s.Networking.record("alice")
	r.Pending, r.Repair = true, true
	if err = s.Networking.saveRecord(r); err != nil {
		t.Fatal(err)
	}
	return s, de, p
}

// A pinned image gone from the engine is pulled again by its reference
// before the pair is replaced, as a new revision, as a direct environment's.
func TestVPNRecoveryRepinsMissingImage(t *testing.T) {
	s, de, _ := vpnImageSetup(t, PowerRunning)
	ctx := context.Background()
	de.ids = map[string]string{"img": "sha256:img2"}
	s.Networking.Poll(ctx, nil)
	got, err := s.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Networking.record("alice")
	if got.Error != "" || r.Pending || r.Error != "" {
		t.Fatalf("recovery failed: error=%q transition=%+v", got.Error, r)
	}
	def, _ := s.Defs.Get("alice")
	if def.Image.ID != "sha256:img2" || def.Revision != 2 || !got.Container.Running() || got.Container.Labels[LabelRevision] != "2" {
		t.Fatalf("not repinned: definition=%+v container=%+v", def, got.Container)
	}
}

// An image that is gone and can't be pulled again leaves the pair as it is,
// running or stopped: the poll doesn't tear it down and rebuild the gateway
// every pass. The error names the image and the remedy; an upgrade to an
// image that is there recovers it.
func TestVPNRecoveryWithUnavailableImageChangesNothing(t *testing.T) {
	for _, power := range []Power{PowerRunning, PowerStopped} {
		t.Run(string(power), func(t *testing.T) {
			s, de, p := vpnImageSetup(t, power)
			ctx := context.Background()
			de.missing["img"] = true
			de.pull = func(context.Context, string) error { return errors.New("pull access denied") }
			before, _ := s.Get(ctx, "alice")
			var removed []string
			de.onRemove = func(id string) { removed = append(removed, id) }
			calls := len(p.calls)
			for range 3 {
				s.Networking.Poll(ctx, nil)
			}
			for _, call := range p.calls[calls:] {
				if call == "ensure" || call == "remove-runtime" {
					t.Fatalf("poll churned the gateway: %v", p.calls[calls:])
				}
			}
			if len(removed) != 0 {
				t.Fatalf("poll removed %v", removed)
			}
			got, err := s.Get(ctx, "alice")
			if err != nil {
				t.Fatal(err)
			}
			if got.Container.ID != before.Container.ID || got.Container.Running() != before.Container.Running() {
				t.Fatalf("consumer changed: %+v", got.Container)
			}
			r, _ := s.Networking.record("alice")
			for _, msg := range []string{r.Error, got.Error, got.VPN.Reason} {
				if !strings.Contains(msg, "img") || !strings.Contains(msg, "sha256:img") || !strings.Contains(msg, "upgrade") {
					t.Fatalf("unclear error %q (transition %+v, VPN %+v)", msg, r, got.VPN)
				}
				if strings.Contains(msg, "pull access denied") {
					t.Fatalf("raw engine cause exposed: %q", msg)
				}
			}
			// The remedy: a current image under the reference, and an upgrade.
			delete(de.missing, "img")
			de.ids = map[string]string{"img": "sha256:img2"}
			de.pull = nil
			removed = nil
			set := got.Settings
			set.Image = got.Image
			if _, err = s.Recreate(ctx, "alice", set, noProgress); err != nil {
				t.Fatal(err)
			}
			got, _ = s.Get(ctx, "alice")
			r, _ = s.Networking.record("alice")
			def, _ := s.Defs.Get("alice")
			if got.Error != "" || r.Pending || r.Error != "" || def.Image.ID != "sha256:img2" || got.Container.ID == "" ||
				got.Container.Running() != (power == PowerRunning) || !slices.Contains(removed, before.Container.ID) {
				t.Fatalf("upgrade didn't recover: error=%q transition=%+v definition=%+v container=%+v", got.Error, r, def, got.Container)
			}
		})
	}
}

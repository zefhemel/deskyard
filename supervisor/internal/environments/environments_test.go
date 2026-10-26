package environments

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"
)

func TestValidName(t *testing.T) {
	for _, ok := range []string{"alice", "a1", "build-bot", "x23456789012345678901234567890ab"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a", "Alice", "1abc", "abc-", "a_b", "api", "login", "_x", "desktops", "environments", "x234567890123456789012345678901ab"} {
		if ValidName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFreeSubnetSkipsUsed(t *testing.T) {
	pool := netip.MustParsePrefix("10.213.0.0/16")
	got, err := freeSubnet(pool, []string{"10.213.0.0/28", "10.213.0.16/28", "172.17.0.0/16"})
	if err != nil || got.String() != "10.213.0.32/28" {
		t.Fatalf("%v %v", got, err)
	}
	// A wide network overlapping the start of the pool is skipped too.
	got, _ = freeSubnet(pool, []string{"10.213.0.0/24"})
	if got.String() != "10.213.1.0/28" {
		t.Fatal(got)
	}
	if _, err := freeSubnet(netip.MustParsePrefix("10.0.0.0/28"), []string{"10.0.0.0/28"}); err == nil {
		t.Fatal("full pool not reported")
	}
}

// reopen is s after a supervisor restart: a new service with the saved
// definitions.
func reopen(t *testing.T, s *Service) *Service {
	t.Helper()
	fresh, err := Open(s.Eng, s.Cfg, s.Instance, s.Log)
	if err != nil {
		t.Fatal(err)
	}
	return fresh
}

// seed defines name with set, unless it is defined, and creates its container
// (not started) from the definition, as Create does.
func (s *Service) seed(ctx context.Context, name string, set Settings) error {
	def, defined := s.Defs.Get(name)
	if !defined {
		def = withSettings(Definition{Name: name, Created: time.Now().UTC(), MemoryReclaim: true, Power: PowerRunning}, set, set.Image)
		if err := s.Defs.Put(def); err != nil {
			return err
		}
	}
	// As Create, the home is made with the definition.
	if err := os.MkdirAll(s.Layout.HomeOf(name), 0o755); err != nil {
		return err
	}
	err := s.createContainer(ctx, name, def.Revision, def.Settings(), pinned(def), "")
	if err != nil && !defined {
		_ = s.Defs.Delete(name)
	}
	return err
}

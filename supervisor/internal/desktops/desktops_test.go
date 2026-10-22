package desktops

import (
	"net/netip"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, ok := range []string{"alice", "a1", "build-bot", "x23456789012345678901234567890ab"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a", "Alice", "1abc", "abc-", "a_b", "api", "login", "_x", "desktops", "x234567890123456789012345678901ab"} {
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

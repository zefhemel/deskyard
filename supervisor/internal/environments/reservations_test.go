package environments

import (
	"context"
	"errors"
	"os"
	"testing"
)

// Claiming a name reserves it as being created and, under the reservation,
// has NameClaimed forget what an earlier environment of the name left.
func TestClaimName(t *testing.T) {
	s, _ := newFakeService(t)
	var during []string
	s.NameClaimed = func(name string) error {
		during = append(during, name+" "+s.Busy()[name])
		if name == "full" {
			return errors.New("disk full")
		}
		return nil
	}
	release, err := s.ClaimName("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(during) != 1 || during[0] != "alice creating" {
		t.Fatalf("NameClaimed heard %q", during)
	}
	if c, err := s.Get(context.Background(), "alice"); err != nil || c.State != "creating" {
		t.Fatalf("claimed name shows %+v %v", c, err)
	}
	if _, err := s.ClaimName("alice"); !errors.Is(err, ErrBusy) {
		t.Fatalf("claimed twice: %v", err)
	}
	release()
	if _, err := s.ClaimName("full"); err == nil || len(s.Busy()) != 0 {
		t.Fatalf("a failed NameClaimed kept the claim: %v %v", err, s.Busy())
	}
	if _, err := s.ClaimName("Not Valid"); err == nil {
		t.Fatal("claimed an invalid name")
	}
	if err := s.seed(context.Background(), "bob", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimName("bob"); !errors.Is(err, ErrExists) || len(s.Busy()) != 0 {
		t.Fatalf("claimed a defined name: %v", err)
	}
}

// An environment created stopped is only defined: it has its home, but no
// container or network until it is started.
func TestCreateStopped(t *testing.T) {
	s, e := newFakeService(t)
	ctx := context.Background()
	c, err := s.CreateStopped(ctx, "alice", Settings{Image: "img"}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != "stopped" || c.Container.ID != "" || c.Power != PowerStopped || c.Revision != 1 {
		t.Fatalf("%+v", c)
	}
	if len(e.cs) != 0 {
		t.Fatalf("containers %v", e.cs)
	}
	if st, err := os.Stat(s.Layout.HomeOf("alice")); err != nil || !st.IsDir() {
		t.Fatalf("no home: %v", err)
	}
	if _, err := s.CreateStopped(ctx, "bob", Settings{Image: "img", VPNConnectionID: "vpn"}, noProgress); err == nil {
		t.Fatal("created a stopped VPN environment")
	}
	if _, ok := s.Defs.Get("bob"); ok {
		t.Fatal("bob defined")
	}
}

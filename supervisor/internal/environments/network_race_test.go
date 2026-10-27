package environments

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// subnetEngine refuses a network whose subnet overlaps another, as Docker
// does ("Pool overlaps with other one on this address space"). steal makes
// that many creations lose their subnet to another supervisor on the engine
// between listing and creating.
type subnetEngine struct {
	*fakeEngine
	mu    sync.Mutex
	nets  []engine.Network
	steal int
}

func (e *subnetEngine) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	e.mu.Lock()
	out := append([]engine.Network(nil), e.nets...)
	e.mu.Unlock()
	time.Sleep(2 * time.Millisecond) // the gap between choosing and creating
	return out, nil
}

func (e *subnetEngine) CreateNetwork(_ context.Context, name, subnet string, l map[string]string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := netip.MustParsePrefix(subnet)
	if e.steal > 0 {
		e.steal--
		e.nets = append(e.nets, engine.Network{Name: "foreign-" + name, Subnet: subnet})
		return fmt.Errorf("pool overlaps: %w", engine.ErrSubnetTaken)
	}
	for _, n := range e.nets {
		if netip.MustParsePrefix(n.Subnet).Overlaps(p) {
			return fmt.Errorf("pool overlaps: %w", engine.ErrSubnetTaken)
		}
	}
	e.nets = append(e.nets, engine.Network{ID: name, Name: name, Subnet: subnet, Labels: l})
	return nil
}

func subnetService(t *testing.T, steal int) (*Service, *subnetEngine) {
	s, fake := newFakeService(t)
	eng := &subnetEngine{fakeEngine: fake, steal: steal}
	s.Eng = eng
	s.Cfg.Subnet = "10.213.0.0/16"
	return s, eng
}

// Environments created at the same time each get their own subnet.
func TestConcurrentNetworksGetDistinctSubnets(t *testing.T) {
	s, eng := subnetService(t, 0)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.createNetwork(context.Background(), fmt.Sprintf("env%d", i))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(eng.nets) != 8 {
		t.Fatalf("%d networks", len(eng.nets))
	}
}

// A subnet another supervisor took first is chosen again, not a failure.
func TestNetworkRetriesASubnetTakenMeanwhile(t *testing.T) {
	s, eng := subnetService(t, 2)
	if err := s.createNetwork(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if len(eng.nets) != 3 || eng.nets[2].Name != containerName("demo") {
		t.Fatalf("%+v", eng.nets)
	}
}

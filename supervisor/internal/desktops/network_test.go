package desktops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// The fake engine's networks behave like Docker's: a subnet that overlaps
// an existing network's is refused.

func (e *fakeEngine) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	e.mu.Lock()
	var out []engine.Network
	for _, n := range e.nets {
		if n.Labels["hidden"] == "" {
			out = append(out, n)
		}
	}
	e.mu.Unlock()
	// Leave room for another create to run between listing and creating.
	time.Sleep(5 * time.Millisecond)
	return out, nil
}

func (e *fakeEngine) CreateNetwork(_ context.Context, name, subnet string, labels map[string]string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.nets[name]; ok {
		return fmt.Errorf("network with name %s already exists", name)
	}
	p := netip.MustParsePrefix(subnet)
	for _, n := range e.nets {
		if netip.MustParsePrefix(n.Subnet).Overlaps(p) {
			e.refused++
			return errors.New("Error response from daemon: invalid pool request: Pool overlaps with other one on this address space")
		}
	}
	e.nets[name] = engine.Network{ID: name, Name: name, Subnet: subnet, Labels: labels}
	return nil
}

func (e *fakeEngine) RemoveNetwork(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.nets, name)
	return nil
}

// newNetworkService is a Service on the fake engine whose desktops answer
// as ready at once.
func newNetworkService(t *testing.T, pool string) (*Service, *fakeEngine) {
	ready := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(ready.Close)
	eng := newFakeEngine(t.TempDir())
	port, _ := strconv.Atoi(ready.URL[len("http://127.0.0.1:"):])
	eng.port = port
	return New(eng, Config{DataDir: t.TempDir(), Subnet: pool}, slog.New(slog.DiscardHandler)), eng
}

func TestConcurrentCreatesGetDistinctSubnets(t *testing.T) {
	s, eng := newNetworkService(t, "10.230.0.0/24")
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			_, errs[i] = s.Create(context.Background(), fmt.Sprintf("desk%d", i), Settings{Image: "img"}, func(string, float64) {})
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("desk%d: %v", i, err)
		}
	}
	seen := map[string]string{}
	for name, nw := range eng.nets {
		if other, ok := seen[nw.Subnet]; ok {
			t.Errorf("%s and %s share %s", name, other, nw.Subnet)
		}
		seen[nw.Subnet] = name
	}
	if len(seen) != n {
		t.Errorf("%d networks for %d desktops: %v", len(seen), n, eng.nets)
	}
	// One supervisor never asks for a subnet it has already given out.
	if eng.refused != 0 {
		t.Errorf("%d overlapping subnets asked for", eng.refused)
	}
}

func TestCreateSkipsSubnetTakenElsewhere(t *testing.T) {
	s, eng := newNetworkService(t, "10.230.0.0/24")
	// A network the listing doesn't show yet, as when another supervisor
	// on the same pool created it a moment ago.
	eng.nets["elsewhere"] = engine.Network{Name: "elsewhere", Subnet: "10.230.0.0/28", Labels: map[string]string{"hidden": "1"}}
	if _, err := s.Create(context.Background(), "alice", Settings{Image: "img"}, func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	if got := eng.nets["deskyard-alice"].Subnet; got != "10.230.0.16/28" {
		t.Fatalf("alice got %q", got)
	}
	// A full pool is an error, not a loop.
	eng.nets["rest"] = engine.Network{Name: "rest", Subnet: "10.230.0.0/24", Labels: map[string]string{"hidden": "1"}}
	if _, err := s.Create(context.Background(), "bob", Settings{Image: "img"}, func(string, float64) {}); err == nil {
		t.Fatal("bob was created in a full pool")
	}
}

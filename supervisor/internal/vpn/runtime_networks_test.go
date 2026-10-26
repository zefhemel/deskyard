package vpn

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type policyObservationEngine struct {
	*runtimeEngine
	readiness []string
}

func (e *policyObservationEngine) Exec(ctx context.Context, id string, s engine.ExecSpec) (*engine.Process, error) {
	e.readiness = append(e.readiness, strings.Join(s.Cmd, " "))
	return e.runtimeEngine.Exec(ctx, id, s)
}

func TestEnsureIgnoresRedundantPrivateInventoryChurn(t *testing.T) {
	for _, withException := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-exception", true: "disjoint-exception"}[withException], func(t *testing.T) {
			m, e, c := managerFixture(t)
			observed := &policyObservationEngine{runtimeEngine: e}
			m.eng = observed
			configured := []netip.Prefix{netip.MustParsePrefix("10.213.0.0/16"), netip.MustParsePrefix("192.168.99.0/24")}
			if err := m.SetReservedNetworks(configured); err != nil {
				t.Fatal(err)
			}
			var exceptions []Exception
			if withException {
				exceptions = []Exception{{Destination: "192.168.50.10", Protocol: "tcp", Ports: []PortRange{{Start: 631}}}}
			}
			ctx := context.Background()
			id := strings.Repeat("8", 32)
			original, err := m.Ensure(ctx, id, "desktop", c.ID, exceptions, false)
			if err != nil {
				t.Fatal(err)
			}
			originalProbe := observed.readiness[len(observed.readiness)-1]
			originalRules, err := os.ReadFile(filepath.Join(m.directory(id), "post-rules.txt"))
			if err != nil {
				t.Fatal(err)
			}
			// Configured reservations always retain explicit mandatory priority, even
			// when the baseline private deny would otherwise make them redundant.
			for _, prefix := range configured {
				needle := "-d " + prefix.String() + " -m comment"
				at := strings.Index(string(originalRules), needle)
				if at < 0 {
					t.Fatalf("configured reservation missing: %s", prefix)
				}
				if withException && at > strings.Index(string(originalRules), "-d 192.168.50.10/32") {
					t.Fatal("mandatory reservation follows exception")
				}
			}
			base := []engine.Network{{Name: "desktop", Subnet: "10.213.0.0/28"}, {Name: "other", Subnet: "10.213.0.16/28"}}
			churn := []engine.Network{{Name: "private-a", Subnet: "10.231.8.16/28"}, {Name: "private-b", Subnet: "172.20.0.0/24"}, {Name: "private-c", Subnet: "192.168.97.0/24"}}
			for _, inventory := range [][]engine.Network{append(slices.Clone(base), churn...), append(slices.Clone(base), churn[1:]...), base} {
				e.networks = inventory
				got, err := m.Ensure(ctx, id, "desktop", c.ID, exceptions, false)
				if err != nil || got.ID != original.ID || len(e.specs) != 1 || len(e.removed) != 0 {
					t.Fatalf("redundant private inventory replaced runtime: %v creates=%d removes=%d", err, len(e.specs), len(e.removed))
				}
				if observed.readiness[len(observed.readiness)-1] != originalProbe {
					t.Fatal("hash-only reuse changed actual readiness policy")
				}
				rules, _ := os.ReadFile(filepath.Join(m.directory(id), "post-rules.txt"))
				if string(rules) != string(originalRules) {
					t.Fatal("actual installed policy changed for redundant inventory")
				}
			}
			if withException {
				probes := len(observed.readiness)
				e.networks = append(slices.Clone(base), engine.Network{Name: "new-control", Subnet: "192.168.50.0/24"})
				if _, err := m.Ensure(ctx, id, "desktop", c.ID, exceptions, false); !errors.Is(err, ErrValidation) {
					t.Fatalf("full inventory overlap was ignored: %v", err)
				}
				if len(observed.readiness) != probes || len(e.specs) != 1 || len(e.removed) != 0 {
					t.Fatal("invalid policy touched existing runtime")
				}
			}
		})
	}
}

func TestEnsureRetainsNewMandatoryReservationChanges(t *testing.T) {
	for _, change := range []string{"public-inventory", "private-supernet", "configured-private-control"} {
		t.Run(change, func(t *testing.T) {
			m, e, c := managerFixture(t)
			ctx := context.Background()
			id := strings.Repeat("9", 32)
			configured := []netip.Prefix{netip.MustParsePrefix("10.213.0.0/16")}
			if err := m.SetReservedNetworks(configured); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Ensure(ctx, id, "desktop", c.ID, nil, false); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "public-inventory":
				e.networks = []engine.Network{{Name: "desktop", Subnet: "10.213.0.0/28"}, {Name: "public-control", Subnet: "203.0.113.0/24"}}
			case "private-supernet":
				e.networks = []engine.Network{{Name: "desktop", Subnet: "10.213.0.0/28"}, {Name: "mixed-private-public", Subnet: "10.0.0.0/7"}}
			case "configured-private-control":
				if err := m.SetReservedNetworks(append(configured, netip.MustParsePrefix("172.27.0.0/24"))); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.Ensure(ctx, id, "desktop", c.ID, nil, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("mandatory policy change did not require safe replacement: %v", err)
			}
			if len(e.specs) != 1 || len(e.removed) != 0 {
				t.Fatal("Ensure replaced a consumer dependency")
			}
		})
	}
}

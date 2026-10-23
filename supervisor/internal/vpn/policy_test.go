package vpn

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"
)

func TestNormalizeExceptions(t *testing.T) {
	in := []Exception{{Destination: "192.168.1.123/28", Protocol: " TCP ", Ports: []PortRange{{Start: 443}, {Start: 80, End: 81}, {Start: 81, End: 82}}}, {Destination: "10.1.2.3", Protocol: "udp", Ports: []PortRange{{Start: 53}}}}
	got, err := NormalizeExceptions(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []Exception{{Destination: "10.1.2.3/32", Protocol: "udp", Ports: []PortRange{{Start: 53, End: 53}}}, {Destination: "192.168.1.112/28", Protocol: "tcp", Ports: []PortRange{{Start: 80, End: 82}, {Start: 443, End: 443}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if in[0].Ports[0].Start != 443 {
		t.Fatal("mutated caller's rules")
	}
}
func TestRejectUnsafeExceptions(t *testing.T) {
	for _, dest := range []string{"0.0.0.0/0", "10.0.0.0/7", "192.168.0.0/15", "8.8.8.8", "169.254.169.254", "127.0.0.1", "100.100.100.100", "224.0.0.1", "::1", "fd00::1", "hostname"} {
		_, err := NormalizeExceptions([]Exception{{Destination: dest, Protocol: "tcp", Ports: []PortRange{{Start: 80}}}})
		if !errors.Is(err, ErrValidation) {
			t.Errorf("accepted %s: %v", dest, err)
		}
	}
	for _, rule := range []Exception{{Destination: "192.168.1.1", Protocol: "all", Ports: []PortRange{{Start: 80}}}, {Destination: "192.168.1.1", Protocol: "tcp"}, {Destination: "192.168.1.1", Protocol: "tcp", Ports: []PortRange{{Start: 0}}}, {Destination: "192.168.1.1", Protocol: "tcp", Ports: []PortRange{{Start: 99, End: 2}}}, {Destination: "192.168.1.1", Protocol: "tcp", Ports: []PortRange{{Start: 1, End: 65535}}}} {
		if _, err := NormalizeExceptions([]Exception{rule}); !errors.Is(err, ErrValidation) {
			t.Fatalf("accepted unsafe rule: %+v %v", rule, err)
		}
	}
	rules := []Exception{{Destination: "172.21.1.0/24", Protocol: "tcp", Ports: []PortRange{{Start: 80}}}}
	if err := ValidateExceptionDestinations(rules, []netip.Prefix{netip.MustParsePrefix("172.21.1.4/32")}); !errors.Is(err, ErrValidation) {
		t.Fatalf("reserved intersection: %v", err)
	}
}

func TestExceptionsMergeEquivalentRulesAndRejectAggregateCatchAll(t *testing.T) {
	rules := []Exception{{Destination: "192.168.0.0/16", Protocol: "tcp", Ports: []PortRange{{Start: 80}}}, {Destination: "192.168.1.1/16", Protocol: "tcp", Ports: []PortRange{{Start: 81}}}}
	got, err := NormalizeExceptions(rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Destination != "192.168.0.0/16" || !reflect.DeepEqual(got[0].Ports, []PortRange{{Start: 80, End: 81}}) {
		t.Fatalf("equivalent rules not canonicalized: %+v", got)
	}
	rules[0].Ports = []PortRange{{Start: 1, End: 32767}}
	rules[1].Ports = []PortRange{{Start: 32768, End: 65535}}
	if _, err := NormalizeExceptions(rules); !errors.Is(err, ErrValidation) {
		t.Fatalf("aggregate all-ports allowance accepted: %v", err)
	}
}

func TestExceptionsValidateEffectivePortsAcrossNestedPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rules   []Exception
		invalid bool
	}{
		{name: "nested aggregate catch-all", invalid: true, rules: []Exception{
			{Destination: "192.168.0.0/16", Protocol: "tcp", Ports: []PortRange{{Start: 1, End: 32767}}},
			{Destination: "192.168.1.0/24", Protocol: "tcp", Ports: []PortRange{{Start: 32768, End: 65535}}},
		}},
		{name: "nested aggregate catch-all reversed", invalid: true, rules: []Exception{
			{Destination: "192.168.1.0/24", Protocol: "tcp", Ports: []PortRange{{Start: 32768, End: 65535}}},
			{Destination: "192.168.0.0/16", Protocol: "tcp", Ports: []PortRange{{Start: 1, End: 32767}}},
		}},
		{name: "three nested ancestors", invalid: true, rules: []Exception{
			{Destination: "10.0.0.0/8", Protocol: "udp", Ports: []PortRange{{Start: 1, End: 20000}}},
			{Destination: "10.1.0.0/16", Protocol: "udp", Ports: []PortRange{{Start: 20001, End: 40000}}},
			{Destination: "10.1.2.3", Protocol: "udp", Ports: []PortRange{{Start: 40001, End: 65535}}},
		}},
		{name: "nested limited union", rules: []Exception{
			{Destination: "192.168.0.0/16", Protocol: "tcp", Ports: []PortRange{{Start: 80}}},
			{Destination: "192.168.1.0/24", Protocol: "tcp", Ports: []PortRange{{Start: 443}}},
		}},
		{name: "nested union retains a port gap", rules: []Exception{
			{Destination: "192.168.0.0/16", Protocol: "tcp", Ports: []PortRange{{Start: 1, End: 32767}}},
			{Destination: "192.168.1.0/24", Protocol: "tcp", Ports: []PortRange{{Start: 32769, End: 65535}}},
		}},
		{name: "disjoint destinations", rules: []Exception{
			{Destination: "192.168.1.0/24", Protocol: "tcp", Ports: []PortRange{{Start: 1, End: 32767}}},
			{Destination: "192.168.2.0/24", Protocol: "tcp", Ports: []PortRange{{Start: 32768, End: 65535}}},
		}},
		{name: "different protocols", rules: []Exception{
			{Destination: "192.168.0.0/16", Protocol: "tcp", Ports: []PortRange{{Start: 1, End: 32767}}},
			{Destination: "192.168.1.0/24", Protocol: "udp", Ports: []PortRange{{Start: 32768, End: 65535}}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeExceptions(tc.rules)
			if tc.invalid {
				if !errors.Is(err, ErrValidation) {
					t.Fatalf("effective all-ports allowance accepted: %v", err)
				}
			} else if err != nil {
				t.Fatalf("limited exception rejected: %v", err)
			}
		})
	}
}

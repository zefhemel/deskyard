package vpn

import (
	"net/netip"
	"slices"
	"strings"
)

// PortRange is inclusive. End zero in input means the single Start port.
type PortRange struct {
	Start uint16 `json:"start"`
	End   uint16 `json:"end"`
}

// Exception permits only these transport ports to an explicit private IPv4
// destination. It must never become a blanket Gluetun outbound-subnet setting.
type Exception struct {
	Destination string      `json:"destination"`
	Protocol    string      `json:"protocol"`
	Ports       []PortRange `json:"ports"`
}

var privateNetworks = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.168.0.0/16")}

// NormalizeExceptions canonicalizes destinations, protocols and inclusive port
// ranges. V1 permits only RFC1918 IPv4, never public, loopback, link-local,
// multicast, metadata or IPv6 destinations. Runtime control and other-desktop
// networks MUST additionally pass ValidateExceptionDestinations before use.
func NormalizeExceptions(in []Exception) ([]Exception, error) {
	if len(in) > 128 {
		return nil, invalid("lanExceptions", "at most 128 rules are supported")
	}
	out := make([]Exception, 0, len(in))
	for _, rule := range in {
		destination := strings.TrimSpace(rule.Destination)
		p, err := netip.ParsePrefix(destination)
		if err != nil {
			addr, e := netip.ParseAddr(destination)
			if e != nil {
				return nil, invalid("lanExceptions.destination", "must be a private IPv4 address or CIDR")
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		p = p.Masked()
		allowed := false
		for _, private := range privateNetworks {
			if p.Addr().Is4() && private.Contains(p.Addr()) && p.Bits() >= private.Bits() {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, invalid("lanExceptions.destination", "only RFC1918 IPv4 destinations are supported; IPv6 and metadata destinations remain blocked")
		}
		protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
		if protocol != "tcp" && protocol != "udp" {
			return nil, invalid("lanExceptions.protocol", "must be tcp or udp")
		}
		if len(rule.Ports) == 0 || len(rule.Ports) > 128 {
			return nil, invalid("lanExceptions.ports", "explicit ports or ranges are required (at most 128)")
		}
		merged, err := normalizePorts(rule.Ports)
		if err != nil {
			return nil, err
		}
		out = append(out, Exception{p.String(), protocol, merged})
	}
	slices.SortFunc(out, func(a, b Exception) int {
		if c := strings.Compare(a.Destination, b.Destination); c != 0 {
			return c
		}
		return strings.Compare(a.Protocol, b.Protocol)
	})
	combined := make([]Exception, 0, len(out))
	for _, rule := range out {
		if n := len(combined); n > 0 && combined[n-1].Destination == rule.Destination && combined[n-1].Protocol == rule.Protocol {
			ports, err := normalizePorts(append(combined[n-1].Ports, rule.Ports...))
			if err != nil {
				return nil, err
			}
			combined[n-1].Ports = ports
		} else {
			combined = append(combined, rule)
		}
	}
	return combined, nil
}

// ValidateExceptionDestinations applies mandatory dynamically discovered deny
// destinations (supervisor/control endpoints and other desktops' networks).
// Any overlap is rejected, even when the exception is the broader prefix.
func ValidateExceptionDestinations(rules []Exception, reserved []netip.Prefix) error {
	normalized, err := NormalizeExceptions(rules)
	if err != nil {
		return err
	}
	for _, rule := range normalized {
		p := netip.MustParsePrefix(rule.Destination)
		for _, deny := range reserved {
			if !deny.IsValid() {
				return invalid("lanExceptions.destination", "reserved network is invalid")
			}
			if p.Overlaps(deny) {
				return invalid("lanExceptions.destination", "overlaps a reserved control or desktop network")
			}
		}
	}
	return nil
}

func normalizePorts(in []PortRange) ([]PortRange, error) {
	ports := slices.Clone(in)
	for i := range ports {
		if ports[i].End == 0 {
			ports[i].End = ports[i].Start
		}
		if ports[i].Start == 0 || ports[i].End < ports[i].Start {
			return nil, invalid("lanExceptions.ports", "ports must be between 1 and 65535, with ascending ranges")
		}
	}
	slices.SortFunc(ports, func(a, b PortRange) int {
		if a.Start != b.Start {
			return int(a.Start) - int(b.Start)
		}
		return int(a.End) - int(b.End)
	})
	merged := make([]PortRange, 0, len(ports))
	for _, port := range ports {
		if n := len(merged); n > 0 && int(port.Start) <= int(merged[n-1].End)+1 {
			merged[n-1].End = max(merged[n-1].End, port.End)
		} else {
			merged = append(merged, port)
		}
	}
	if len(merged) == 1 && merged[0].Start == 1 && merged[0].End == 65535 {
		return nil, invalid("lanExceptions.ports", "an all-ports exception is not supported")
	}
	return merged, nil
}

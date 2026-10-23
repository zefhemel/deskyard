package vpn

import (
	"net/netip"
	"slices"
	"sort"
)

// Validate against every live network before simplifying the enforced policy.
// Configured pools/control destinations always remain mandatory denies. Only
// discovered prefixes fully inside RFC1918 can be redundant: overlap with an
// allowed LAN destination has already been rejected, and buildFirewall's
// baseline private deny blocks all their remaining traffic. Removing those
// redundant rules keeps actual firewall, readiness and fingerprint identical
// across unrelated engine network churn without hiding inventory changes.
func effectiveNetworkReservations(configured, discovered []netip.Prefix, exceptions []Exception) ([]netip.Prefix, error) {
	full := append(slices.Clone(configured), discovered...)
	if err := ValidateExceptionDestinations(exceptions, full); err != nil {
		return nil, err
	}
	mandatory := slices.Clone(configured)
	for _, prefix := range discovered {
		redundant := false
		for _, private := range privateNetworks {
			if prefix.Addr().Is4() && prefix.Bits() >= private.Bits() && private.Contains(prefix.Masked().Addr()) {
				redundant = true
				break
			}
		}
		if !redundant {
			mandatory = append(mandatory, prefix)
		}
	}
	return canonicalReserved(mandatory), nil
}

// Collapse covered prefixes so allocating another bridge inside the reserved
// desktop pool does not change an already enforced gateway policy.
func canonicalReserved(prefixes []netip.Prefix) []netip.Prefix {
	sort.Slice(prefixes, func(i, j int) bool {
		if prefixes[i].Bits() != prefixes[j].Bits() {
			return prefixes[i].Bits() < prefixes[j].Bits()
		}
		return prefixes[i].Addr().Compare(prefixes[j].Addr()) < 0
	})
	var out []netip.Prefix
	for _, prefix := range prefixes {
		covered := false
		for _, parent := range out {
			if parent.Overlaps(prefix) && parent.Bits() <= prefix.Bits() {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, prefix.Masked())
		}
	}
	return out
}

package vpn

import (
	"fmt"
	"net/netip"
	"strings"
)

// Policy runs before tailscaled or any desktop process. The outbound chain
// permits Tailscale peers and ordinary public egress, but keeps direct LAN and
// control-network access restricted. Tailscale's own policy governs peers.
func tailscalePolicy(exceptions []Exception, reserved []netip.Prefix) (string, error) {
	rules, err := NormalizeExceptions(exceptions)
	if err != nil {
		return "", err
	}
	reserved = append(append([]netip.Prefix{}, reserved...), netip.MustParsePrefix("10.213.0.0/16"))
	if err = ValidateExceptionDestinations(rules, reserved); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -eu\n")
	for _, family := range []string{"iptables-nft", "ip6tables-nft"} {
		fmt.Fprintf(&b, "%s -w -N DY_TS_OUT 2>/dev/null || true\n%s -w -F DY_TS_OUT\n%s -w -C OUTPUT -j DY_TS_OUT 2>/dev/null || %s -w -I OUTPUT 1 -j DY_TS_OUT\n", family, family, family, family)
		fmt.Fprintf(&b, "%s -w -A DY_TS_OUT -m conntrack --ctstate ESTABLISHED,RELATED --ctdir REPLY -j ACCEPT\n%s -w -A DY_TS_OUT -o lo -j ACCEPT\n", family, family)
		// Neither tailnet nor the desktop may reach cloud link-local metadata.
		if family == "iptables-nft" {
			for _, p := range reserved {
				if p.Addr().Is4() {
					fmt.Fprintf(&b, "iptables-nft -w -A DY_TS_OUT -d %s -j REJECT\n", p)
				}
			}
			b.WriteString("iptables-nft -w -A DY_TS_OUT -d 169.254.0.0/16 -j REJECT\niptables-nft -w -A DY_TS_OUT -d 100.64.0.0/10 ! -o tailscale0 -j REJECT\niptables-nft -w -A DY_TS_OUT -o tailscale0 -d 100.64.0.0/10 -j ACCEPT\n")
			for _, r := range rules {
				for _, p := range r.Ports {
					fmt.Fprintf(&b, "iptables-nft -w -A DY_TS_OUT -d %s -p %s --dport %d:%d -j ACCEPT\n", r.Destination, r.Protocol, p.Start, p.End)
				}
			}
			for _, p := range privateNetworks {
				fmt.Fprintf(&b, "iptables-nft -w -A DY_TS_OUT -d %s -j REJECT\n", p)
			}
		} else {
			b.WriteString("ip6tables-nft -w -A DY_TS_OUT -d fe80::/10 -j REJECT\nip6tables-nft -w -A DY_TS_OUT -o tailscale0 -d fd7a:115c:a1e0::/48 -j ACCEPT\nip6tables-nft -w -A DY_TS_OUT -d fc00::/7 -j REJECT\n")
		}
		fmt.Fprintf(&b, "%s -w -A DY_TS_OUT -j RETURN\n", family)
		fmt.Fprintf(&b, "%s -w -N DY_TS_IN 2>/dev/null || true\n%s -w -F DY_TS_IN\n%s -w -A DY_TS_IN -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT\n%s -w -A DY_TS_IN -j DROP\n%s -w -C INPUT -i tailscale0 -j DY_TS_IN 2>/dev/null || %s -w -I INPUT 1 -i tailscale0 -j DY_TS_IN\n", family, family, family, family, family, family)
	}
	// Docker's embedded resolver can reach host-private names. Only the
	// sidecar's root bootstrap may use it; applications use Quad100/public DNS.
	b.WriteString("iptables-nft -w -t raw -C OUTPUT -d 127.0.0.11 -m owner ! --uid-owner 0 -j DROP 2>/dev/null || iptables-nft -w -t raw -I OUTPUT 1 -d 127.0.0.11 -m owner ! --uid-owner 0 -j DROP\n")
	return b.String(), nil
}

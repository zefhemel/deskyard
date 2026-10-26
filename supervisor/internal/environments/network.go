package environments

import (
	"fmt"
	"net/netip"
	"strings"
)

// freeSubnet returns the first /28 inside pool that doesn't overlap any of
// the used subnets. Each environment's bridge network gets one, because Docker's
// default address pools run out after about 30 networks.
func freeSubnet(pool netip.Prefix, used []string) (netip.Prefix, error) {
	var taken []netip.Prefix
	for _, u := range used {
		if p, err := netip.ParsePrefix(u); err == nil {
			taken = append(taken, p.Masked())
		}
	}
	pool = pool.Masked()
	if !pool.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("DESKYARD_SUBNET must be IPv4")
	}
	base := pool.Addr().As4()
	start := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	count := uint32(1) << (28 - pool.Bits())
	for i := uint32(0); i < count; i++ {
		v := start + i*16
		cand := netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), 28)
		overlaps := false
		for _, t := range taken {
			if t.Overlaps(cand) {
				overlaps = true
				break
			}
		}
		if !overlaps {
			return cand, nil
		}
	}
	return netip.Prefix{}, fmt.Errorf("no free /28 left in %s", pool)
}

func netipParse(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return p, fmt.Errorf("DESKYARD_SUBNET: %w", err)
	}
	return p, nil
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

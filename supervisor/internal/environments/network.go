package environments

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
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

// createNetwork gives name's network the first free subnet of the pool.
// Choosing and creating are one step for this supervisor (subnetMu), so
// environments created at once don't choose the same subnet; one that another
// supervisor on the engine takes meanwhile is chosen again.
func (s *Service) createNetwork(ctx context.Context, name string) error {
	pool, err := netipParse(s.Cfg.Subnet)
	if err != nil {
		return err
	}
	s.subnetMu.Lock()
	defer s.subnetMu.Unlock()
	for attempt := 0; ; attempt++ {
		nets, err := s.Eng.Networks(ctx, nil)
		if err != nil {
			return err
		}
		var used []string
		for _, n := range nets {
			if n.Subnet != "" {
				used = append(used, n.Subnet)
			}
		}
		subnet, err := freeSubnet(pool, used)
		if err != nil {
			return err
		}
		err = s.Eng.CreateNetwork(ctx, containerName(name), subnet.String(), s.ownLabels(name))
		if !errors.Is(err, engine.ErrSubnetTaken) || attempt == 4 {
			return err
		}
	}
}

// network checks name's network: engine.ErrNotFound when there is none,
// ErrConflict when one of its name isn't this supervisor's.
func (s *Service) network(ctx context.Context, name string) error {
	nets, err := s.Eng.Networks(ctx, nil)
	if err != nil {
		return err
	}
	for _, n := range nets {
		if n.Name == containerName(name) {
			if !s.owns(n.Labels, name) {
				return fmt.Errorf("network %s: %w", n.Name, ErrConflict)
			}
			return nil
		}
	}
	return engine.ErrNotFound
}

// ensureNetwork creates name's network unless it is there.
func (s *Service) ensureNetwork(ctx context.Context, name string) error {
	if err := s.network(ctx, name); !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	return s.createNetwork(ctx, name)
}

// removeNetwork removes an environment's network. The supervisor joined it to
// reach the environment, and has to leave it first. A network of its name that
// isn't this supervisor's is left alone (ErrConflict).
func (s *Service) removeNetwork(ctx context.Context, name string) error {
	if err := s.network(ctx, name); err != nil {
		return err
	}
	if s.Cfg.SelfID != "" {
		_ = s.Eng.DisconnectNetwork(ctx, containerName(name), s.Cfg.SelfID)
	}
	return s.Eng.RemoveNetwork(ctx, containerName(name))
}

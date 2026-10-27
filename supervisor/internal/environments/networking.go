package environments

import (
	"cmp"
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// VPNRuntime is the provider boundary. Production uses vpn.Manager. Runtimes
// are keyed by environment name.
type VPNRuntime interface {
	SetReservedNetworks([]netip.Prefix) error
	// Ensure(ctx, environment, network, connection, exceptions)
	Ensure(context.Context, string, string, string, []vpn.Exception) (engine.Container, error)
	Remove(context.Context, string) error
	Status(context.Context, string) (vpn.Status, error)
}

// VPNStatus contains only public configuration and centrally cached observations.
type VPNStatus struct {
	vpn.Status
	ConnectionID    string `json:"connectionId,omitempty"`
	Name            string `json:"name,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Country         string `json:"country,omitempty"`
	DesiredRevision uint64 `json:"desiredRevision"`
	AppliedRevision uint64 `json:"appliedRevision"`
	Idle            bool   `json:"idle"`
}

// Networking coordinates reference changes, network transitions and paired
// operations of the environments whose definition selects a VPN, or that are
// leaving one. The definition is the authority for what is applied; the
// transitions journal only records how far it got. refs protects
// assignment/deletion/revision transactions; mu only protects snapshots.
// Neither is held during runtime preparation or health observations.
type Networking struct {
	Connections *vpn.Store
	runtime     VPNRuntime
	service     *Service
	path        string
	refs        contextMutex
	mu          sync.Mutex
	records     map[string]networkTransition // by name
	locks       map[string]chan struct{}
	statuses    map[string]vpn.Status
	gateways    map[string]engine.Container
	starts      map[string]autoStart // the poll's restarts of exited consumers
	now         func() time.Time
	wake        chan struct{}
	pollSlots   chan struct{}
}

// ConfigureNetworking must run before startup Reconcile, including pool reservation.
func (s *Service) ConfigureNetworking(connections *vpn.Store, runtime VPNRuntime, stateDir string) error {
	if s.Cfg.Subnet == "" {
		s.Cfg.Subnet = "10.213.0.0/16"
	}
	pool, err := netip.ParsePrefix(s.Cfg.Subnet)
	if err != nil {
		return err
	}
	if err = runtime.SetReservedNetworks([]netip.Prefix{pool}); err != nil {
		return err
	}
	n := &Networking{wake: make(chan struct{}, 1), pollSlots: make(chan struct{}, 8), Connections: connections, runtime: runtime, service: s, path: filepath.Join(stateDir, "network", "transitions.json"), records: map[string]networkTransition{}, locks: map[string]chan struct{}{}, statuses: map[string]vpn.Status{}, gateways: map[string]engine.Container{}, starts: map[string]autoStart{}, now: time.Now}
	if err = n.load(); err != nil {
		return err
	}
	s.Networking = n
	return nil
}
func (n *Networking) lock(ctx context.Context, name string) (func(), error) {
	n.mu.Lock()
	ch := n.locks[name]
	if ch == nil {
		ch = make(chan struct{}, 1)
		n.locks[name] = ch
	}
	n.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// tryLock takes name's operation lock only if it is free.
func (n *Networking) tryLock(name string) (func(), bool) {
	n.mu.Lock()
	ch := n.locks[name]
	if ch == nil {
		ch = make(chan struct{}, 1)
		n.locks[name] = ch
	}
	n.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	default:
		return nil, false
	}
}
func (n *Networking) ValidateSettings(set Settings) (Settings, error) {
	rules, err := vpn.NormalizeExceptions(set.LANExceptions)
	if err != nil {
		return set, err
	}
	pool, err := netip.ParsePrefix(n.service.Cfg.Subnet)
	if err != nil {
		return set, err
	}
	if err = vpn.ValidateExceptionDestinations(rules, []netip.Prefix{pool}); err != nil {
		return set, err
	}
	set.LANExceptions = rules
	if set.VPNConnectionID != "" {
		if _, err = n.Connections.Get(set.VPNConnectionID); err != nil {
			return set, err
		}
	}
	return set, nil
}
func (s *Service) ValidateNetworkSettings(set Settings) (Settings, error) {
	if s.Networking == nil {
		if set.VPNConnectionID != "" {
			return set, errVPNUnavailable
		}
		return set, nil
	}
	return s.Networking.ValidateSettings(set)
}

// selected reports whether the coordinator manages name's networking: its
// definition selects a VPN, or a transition (away from one) is recorded.
func (n *Networking) selected(name string) bool {
	if _, ok := n.record(name); ok {
		return true
	}
	def, ok := n.service.Defs.Get(name)
	return ok && def.Network.Kind == NetworkVPN
}
func (n *Networking) decorate(d Environment) Environment {
	d.VPN.State = "disabled"
	r, ok := n.record(d.Name)
	vpnSelected := d.Settings.VPNConnectionID != ""
	if !ok && !vpnSelected {
		return d
	}
	d.VPN = VPNStatus{ConnectionID: d.Settings.VPNConnectionID, DesiredRevision: r.ConnectionRevision, AppliedRevision: r.AppliedConnectionRevision}
	connectionGone := false
	if vpnSelected {
		if c, err := n.Connections.Get(d.Settings.VPNConnectionID); err == nil {
			d.VPN.Name = c.Name
			d.VPN.Provider = c.Provider
			d.VPN.Country = c.Country
			d.VPN.DesiredRevision = c.Revision
		} else {
			connectionGone = true
		}
	}
	n.mu.Lock()
	d.VPN.Status = n.statuses[d.Name]
	n.mu.Unlock()
	if !vpnSelected {
		d.VPN.Status = vpn.Status{State: "disabled"}
	} else if d.VPN.State == "" {
		d.VPN.State = "connecting"
	}
	if connectionGone {
		d.VPN.State = "blocked"
		d.VPN.Reason = "The selected VPN connection is unavailable; the environment stays stopped"
		d.VPN.CurrentExitIP = ""
		if d.Error == "" {
			d.Error = cmp.Or(r.Error, d.VPN.Reason)
		}
	} else if r.Pending || r.Removing || r.Error != "" {
		d.VPN.State = "blocked"
		d.VPN.Reason = r.Error
		if d.VPN.Reason == "" {
			d.VPN.Reason = "Network transition interrupted; retry to recover"
		}
		d.VPN.CurrentExitIP = ""
		if d.Error == "" {
			d.Error = r.Error
		}
	} else if vpnSelected && !d.Container.Running() {
		d.VPN.State = "stopped"
		d.VPN.Idle = true
		d.VPN.CurrentExitIP = ""
	} else if vpnSelected && r.AppliedRevision != d.Revision {
		// Its definition is ahead of what was applied: never started on it.
		// The poll applies it, fail closed.
		d.VPN.State = "blocked"
		d.VPN.Reason = "VPN network change not applied yet; it is being applied"
		d.VPN.CurrentExitIP = ""
	}
	return d
}
func (n *Networking) target(name string) (engine.Container, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	g, ok := n.gateways[name]
	return g, ok
}

// phase is an internal constant, never an engine/provider error or identifier.
func (n *Networking) fail(r networkTransition, phase recoveryPhase) error {
	msg := "VPN network transition failed at " + string(phase) + "; retry or repair the selected connection"
	if def, ok := n.service.Defs.Get(r.Name); ok && def.Network.Kind != NetworkVPN {
		msg = "Network transition failed at " + string(phase) + "; retry to recover"
	}
	return n.failWith(r, phase, msg)
}

// failWith is fail with msg as the error: one of the supervisor's own,
// never an engine/provider error.
func (n *Networking) failWith(r networkTransition, phase recoveryPhase, msg string) error {
	n.service.Log.Warn("environment network recovery failed", "environment", r.Name, "phase", string(phase))
	r.Pending = true
	r.Error = msg
	if err := n.saveRecord(r); err != nil {
		return errors.New("network recovery failed at " + string(phase) + "; recovery state could not be persisted")
	}
	n.setStatus(r.Name, vpn.Status{State: "blocked", Reason: r.Error, ChangedAt: time.Now().UTC()})
	return errors.New(r.Error)
}

package desktops

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// VPNRuntime is the provider boundary. Production uses vpn.Manager.
type VPNRuntime interface {
	SetReservedNetworks([]netip.Prefix) error
	Ensure(context.Context, string, string, string, string, []vpn.Exception, bool) (engine.Container, error)
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

// Networking coordinates reference changes, durable intent and paired operations.
// refs protects assignment/deletion/revision transactions; mu only protects
// snapshots. Neither is held during runtime preparation or health observations.
type Networking struct {
	Connections *vpn.Store
	runtime     VPNRuntime
	service     *Service
	path        string
	refs        contextMutex
	mu          sync.Mutex
	records     map[string]networkIntent
	locks       map[string]chan struct{}
	statuses    map[string]vpn.Status
	gateways    map[string]engine.Container
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
	n := &Networking{wake: make(chan struct{}, 1), pollSlots: make(chan struct{}, 8), Connections: connections, runtime: runtime, service: s, path: filepath.Join(stateDir, "vpn", "desktops.json"), records: map[string]networkIntent{}, locks: map[string]chan struct{}{}, statuses: map[string]vpn.Status{}, gateways: map[string]engine.Container{}}
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
			return set, errors.New("VPN networking unavailable")
		}
		return set, nil
	}
	return s.Networking.ValidateSettings(set)
}
func (n *Networking) selected(name string) bool { _, ok := n.record(name); return ok }
func (n *Networking) decorate(d Desktop) Desktop {
	d.VPN.State = "disabled"
	r, ok := n.record(d.Name)
	if !ok {
		if d.Settings.VPNConnectionID != "" {
			d.VPN.ConnectionID = d.Settings.VPNConnectionID
			d.VPN.State = "blocked"
			d.VPN.Reason = "VPN recovery record is missing; administrator repair required"
		}
		return d
	}
	d.Settings = cloneSettings(r.Settings)
	d.VPN = VPNStatus{ConnectionID: r.Settings.VPNConnectionID, DesiredRevision: r.Revision, AppliedRevision: r.AppliedRevision}
	if r.Settings.VPNConnectionID != "" {
		if c, err := n.Connections.Get(r.Settings.VPNConnectionID); err == nil {
			d.VPN.Name = c.Name
			d.VPN.Provider = c.Provider
			d.VPN.Country = c.Country
			d.VPN.DesiredRevision = c.Revision
		}
	}
	n.mu.Lock()
	d.VPN.Status = n.statuses[r.ID]
	n.mu.Unlock()
	if r.Settings.VPNConnectionID == "" {
		d.VPN.Status = vpn.Status{State: "disabled"}
	} else if d.VPN.State == "" {
		d.VPN.State = "connecting"
	}
	if r.Pending || r.Removing || r.Error != "" {
		d.VPN.State = "blocked"
		d.VPN.Reason = r.Error
		if d.VPN.Reason == "" {
			d.VPN.Reason = "Network transition interrupted; retry to recover"
		}
		d.VPN.CurrentExitIP = ""
	} else if r.Settings.VPNConnectionID != "" && !d.Container.Running() {
		d.VPN.State = "stopped"
		d.VPN.Idle = true
		d.VPN.CurrentExitIP = ""
	}
	return d
}
func (n *Networking) synthetic(r networkIntent) Desktop {
	return n.decorate(Desktop{ID: r.ID, Name: r.Name, State: "stopped", Image: r.Settings.Image, Created: r.Created, Folder: filepath.Join(n.service.Cfg.DataDir, r.Name), Settings: cloneSettings(r.Settings)})
}
func (n *Networking) target(name string) (engine.Container, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	g, ok := n.gateways[name]
	return g, ok
}

// phase is an internal constant, never an engine/provider error or identifier.
func (n *Networking) fail(r networkIntent, phase recoveryPhase) error {
	n.service.Log.Warn("desktop network recovery failed", "phase", string(phase))
	r.Pending = true
	r.Error = "VPN network transition failed at " + string(phase) + "; retry or repair the selected connection"
	if r.Settings.VPNConnectionID == "" {
		r.Error = "Network transition failed at " + string(phase) + "; retry to recover"
	}
	if err := n.saveRecord(r); err != nil {
		return errors.New("network recovery failed at " + string(phase) + "; recovery state could not be persisted")
	}
	n.setStatus(r.ID, vpn.Status{State: "blocked", Reason: r.Error, ChangedAt: time.Now().UTC()})
	return errors.New(r.Error)
}

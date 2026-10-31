package computers

import (
	"context"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

type serviceRuntime interface {
	ApplyServices(context.Context, string, []vpn.ServiceRule) ([]vpn.ServiceStatus, error)
}

func (n *Networking) UpdateServices(ctx context.Context, name string, rules []vpn.ServiceRule) error {
	unlock, err := n.lock(ctx, name)
	if err != nil {
		return err
	}
	defer unlock()
	rules, err = vpn.NormalizeServiceRules(rules)
	if err != nil {
		return err
	}
	def, ok := n.service.Defs.Get(name)
	if !ok {
		return ErrNotFound
	}
	c, err := n.Connections.Get(def.Network.Connection)
	if err != nil || c.Provider != "tailscale" {
		return errors.New("HTTP services require a Tailscale connection")
	}
	_, err = n.service.Defs.Update(name, func(d *Definition) error { d.Network.Services = rules; return nil })
	if err != nil {
		return err
	}
	def, _ = n.service.Defs.Get(name)
	if def.Power == PowerStopped {
		return nil
	}
	return n.syncServices(ctx, def)
}
func (n *Networking) syncServices(ctx context.Context, def Definition) error {
	c, err := n.Connections.Get(def.Network.Connection)
	if err != nil || c.Provider != "tailscale" || def.Power != PowerRunning {
		return nil
	}
	runtime, ok := n.runtime.(serviceRuntime)
	if !ok {
		return nil
	}
	_, err = runtime.ApplyServices(ctx, def.Name, def.Network.Services)
	r := n.transition(def.Name)
	old := r.ServiceError
	r.ServiceError = ""
	if err != nil {
		r.ServiceError = err.Error()
	}
	if r.ServiceError != old {
		if e := n.saveRecord(r); e != nil {
			return e
		}
	}
	return err
}
func (n *Networking) stopTailscale(ctx context.Context, name string) error {
	def, ok := n.service.Defs.Get(name)
	if !ok {
		return nil
	}
	c, err := n.Connections.Get(def.Network.Connection)
	if err != nil || c.Provider != "tailscale" {
		return nil
	}
	if runtime, ok := n.runtime.(interface {
		Stop(context.Context, string) error
	}); ok {
		return runtime.Stop(ctx, name)
	}
	return nil
}
func (n *Networking) retireOther(ctx context.Context, name, keep string) error {
	if runtime, ok := n.runtime.(interface {
		RetireOther(context.Context, string, string) error
	}); ok {
		return runtime.RetireOther(ctx, name, keep)
	}
	return nil
}

// Reconcile exposure before any app process can listen in this namespace.
// Missing publication prerequisites do not prevent normal desktop use once
// old exposure is confirmed withdrawn; a stopped unsafe sidecar does.
func (n *Networking) prepareTailscaleServices(ctx context.Context, def Definition, runtimeID string) error {
	c, err := n.Connections.Get(def.Network.Connection)
	if err != nil || c.Provider != "tailscale" {
		return nil
	}
	_ = n.syncServices(ctx, def)
	g, err := n.service.Eng.Inspect(ctx, runtimeID)
	if err != nil || !g.Running() {
		return errors.New("Tailscale sidecar is not safe to start the computer; retry network recovery")
	}
	return nil
}

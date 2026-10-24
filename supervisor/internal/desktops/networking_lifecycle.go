package desktops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

func (n *Networking) create(ctx context.Context, name string, set Settings, progress func(string, float64)) (Desktop, error) {
	var err error
	if err = ValidName(name); err != nil {
		return Desktop{}, err
	}
	if n.selected(name) {
		return Desktop{}, ErrExists
	}
	if _, err = n.service.Eng.Inspect(ctx, containerName(name)); !errors.Is(err, engine.ErrNotFound) {
		if err == nil {
			err = ErrExists
		}
		return Desktop{}, err
	}
	if err = n.service.checkHostSources(ctx, set.Mounts, set.DockerAccess); err != nil {
		return Desktop{}, err
	}
	if _, err = n.service.EnsureImage(ctx, set.Image, progress); err != nil {
		return Desktop{}, err
	}
	n.refs.Lock()
	set, err = n.ValidateSettings(set)
	r := networkIntent{ID: randomHex(16), Name: name, Token: randomHex(32), Created: time.Now().UTC(), Settings: set, Running: true, Pending: true}
	if err == nil {
		c, _ := n.Connections.Get(set.VPNConnectionID)
		r.Revision = c.Revision
		err = n.saveRecord(r)
	}
	n.refs.Unlock()
	if err != nil {
		return Desktop{}, err
	}
	if err = os.MkdirAll(filepath.Join(n.service.Cfg.DataDir, name), 0755); err != nil {
		return Desktop{}, n.fail(r, phaseFolder)
	}
	if err = n.service.createNetwork(ctx, name, r.ID); err != nil {
		return Desktop{}, n.fail(r, phaseBridgeCreate)
	}
	if err = n.apply(ctx, r, progress); err != nil {
		return Desktop{}, err
	}
	return n.service.Get(ctx, name)
}
func (n *Networking) recreate(ctx context.Context, name string, set Settings, progress func(string, float64)) (Desktop, error) {
	var err error
	d, err := n.service.Get(ctx, name)
	if err != nil {
		return Desktop{}, err
	}
	if _, err = hostMountsFromLabels(d.Container.Labels); err != nil {
		return Desktop{}, err
	}
	if err = n.service.checkHostSources(ctx, set.Mounts, set.DockerAccess); err != nil {
		return Desktop{}, err
	}
	if _, err = n.service.EnsureImage(ctx, set.Image, progress); err != nil {
		return Desktop{}, err
	}
	r, exists := n.record(name)
	if !exists {
		r = networkIntent{ID: d.ID, Name: d.Name, Token: d.Container.Env[TokenEnv], Created: d.Created, Running: d.Container.Running()}
	}
	// Disable automatic restart before stopping the old direct consumer.
	if d.Container.ID != "" {
		if err = n.service.Eng.UpdateRestartPolicy(ctx, d.Container.ID, "no"); err != nil {
			return Desktop{}, errors.New("could not disable independent desktop restart")
		}
		if d.Container.Running() {
			if err = n.service.Eng.Stop(ctx, d.Container.ID, stopTimeout); err != nil {
				return Desktop{}, errors.New("could not stop desktop for network change")
			}
		}
	}
	n.refs.Lock()
	set, err = n.ValidateSettings(set)
	if err == nil {
		r.Settings = set
		r.Pending = true
		r.Error = ""
		r.Revision = 0
		if set.VPNConnectionID != "" {
			c, _ := n.Connections.Get(set.VPNConnectionID)
			r.Revision = c.Revision
		}
		err = n.saveRecord(r)
	}
	n.refs.Unlock()
	if err != nil {
		return Desktop{}, err
	}
	if err = n.apply(ctx, r, progress); err != nil {
		return Desktop{}, err
	}
	return n.service.Get(ctx, name)
}

// apply is called under the desktop operation lock with durable pending intent.
// It never rolls back a committed selection to direct networking.
func (n *Networking) apply(ctx context.Context, r networkIntent, progress func(string, float64)) error {
	s := n.service
	progress("Preparing desktop networking", -1)
	if phase := n.ensureNetwork(ctx, r); phase != "" {
		return n.fail(r, phase)
	}
	if c, err := s.Eng.Inspect(ctx, containerName(r.Name)); err == nil {
		if c.Labels[LabelID] != r.ID || !IsDesktop(c.Labels) {
			return n.fail(r, phaseIdentity)
		}
		if err = s.Eng.UpdateRestartPolicy(ctx, c.ID, "no"); err != nil {
			return n.fail(r, phaseRestartPolicy)
		}
		if c.Running() {
			if err = s.Eng.Stop(ctx, c.ID, stopTimeout); err != nil {
				return n.fail(r, phaseStop)
			}
		}
		if err = s.Eng.Remove(ctx, c.ID); err != nil {
			return n.fail(r, phaseRemove)
		}
	} else if !errors.Is(err, engine.ErrNotFound) {
		return n.fail(r, phaseInspect)
	}
	if err := n.runtime.Remove(ctx, r.ID); err != nil {
		return n.fail(r, phaseRuntimeRemove)
	}
	r.RuntimeID = ""
	if r.Settings.VPNConnectionID != "" {
		g, err := n.runtime.Ensure(ctx, r.ID, r.Name, containerName(r.Name), r.Settings.VPNConnectionID, r.Settings.LANExceptions, !s.Cfg.InContainer)
		if err != nil {
			return n.fail(r, phaseEnsure)
		}
		r.RuntimeID = g.ID
		n.mu.Lock()
		n.gateways[r.Name] = g
		n.mu.Unlock()
		c, err := n.Connections.Get(r.Settings.VPNConnectionID)
		if err != nil {
			return n.fail(r, phaseConnection)
		}
		r.Revision = c.Revision
	}
	if r.Settings.Camera != "" && s.cameraGone(r.Settings.Camera) {
		r.Settings.Camera = ""
	}
	if err := n.saveRecord(r); err != nil {
		return n.fail(r, phaseSnapshot)
	}
	if err := s.createContainerIn(ctx, r.Name, r.ID, r.Token, r.Created, r.Settings, r.RuntimeID); err != nil {
		return n.fail(r, phaseCreate)
	}
	if r.Running {
		if err := s.Eng.Start(ctx, containerName(r.Name)); err != nil {
			return n.fail(r, phaseStart)
		}
	}
	r.AppliedRevision = r.Revision
	r.Pending = false
	r.Error = ""
	if err := n.saveRecord(r); err != nil {
		return n.fail(r, phaseSnapshot)
	}
	n.setStatus(r.ID, vpn.Status{State: "connecting", ChangedAt: time.Now().UTC()})
	if r.Running {
		if err := s.WaitReady(ctx, r.Name, 2*time.Minute); err != nil {
			return n.fail(r, phaseReady)
		}
	}
	return nil
}
func (n *Networking) power(ctx context.Context, name, action string) error {
	var err error
	r, ok := n.record(name)
	if !ok {
		return ErrNotFound
	}
	r.Running = action != "stop"
	if err = n.saveRecord(r); err != nil {
		return err
	}
	if action == "stop" {
		if c, e := n.service.Eng.Inspect(ctx, containerName(name)); e == nil {
			if err := n.service.Eng.Stop(ctx, c.ID, stopTimeout); err != nil {
				return errors.New("could not stop desktop")
			}
			n.setStatus(r.ID, vpn.Status{State: "stopped", ChangedAt: time.Now().UTC()})
			return nil
		} else if errors.Is(e, engine.ErrNotFound) {
			return nil
		} else {
			return errors.New("could not stop desktop")
		}
	}
	if err = n.recover(ctx, r); err != nil {
		return err
	}
	if action == "restart" {
		return n.service.Eng.Restart(ctx, containerName(name), stopTimeout)
	}
	return n.service.Eng.Start(ctx, containerName(name))
}
func (n *Networking) remove(ctx context.Context, name string, files bool) error {
	var err error
	r, ok := n.record(name)
	if !ok {
		return ErrNotFound
	}
	r.Removing = true
	r.Pending = true
	r.DeleteFiles = files
	if err = n.saveRecord(r); err != nil {
		return err
	}
	return n.finishRemove(ctx, r)
}
func (n *Networking) finishRemove(ctx context.Context, r networkIntent) error {
	s := n.service
	if c, err := s.Eng.Inspect(ctx, containerName(r.Name)); err == nil {
		if c.Labels[LabelID] != r.ID || !IsDesktop(c.Labels) {
			return errors.New("desktop identity changed; removal refused")
		}
	} else if !errors.Is(err, engine.ErrNotFound) {
		return errors.New("could not inspect desktop for removal")
	}
	if err := s.Eng.Remove(ctx, containerName(r.Name)); err != nil && !errors.Is(err, engine.ErrNotFound) {
		return errors.New("could not remove desktop")
	}
	if err := n.runtime.Remove(ctx, r.ID); err != nil {
		return errors.New("could not remove desktop VPN runtime")
	}
	if err := s.removeNetwork(ctx, r.Name); err != nil && !errors.Is(err, engine.ErrNotFound) {
		return errors.New("could not remove desktop network")
	}
	if r.DeleteFiles {
		if err := s.deleteFolder(ctx, r.Name); err != nil {
			return err
		}
	}
	n.refs.Lock()
	defer n.refs.Unlock()
	return n.forget(r.ID)
}

// A creation may have crashed after its durable snapshot but before its bridge.
func (n *Networking) ensureNetwork(ctx context.Context, r networkIntent) recoveryPhase {
	nets, err := n.service.Eng.Networks(ctx, map[string]string{LabelManaged: "true"})
	if err != nil {
		return phaseBridgeInspect
	}
	for _, network := range nets {
		if network.Name == containerName(r.Name) {
			if network.Labels[LabelID] != r.ID {
				return phaseBridgeIdentity
			}
			return ""
		}
	}
	if err := n.service.createNetwork(ctx, r.Name, r.ID); err != nil {
		return phaseBridgeCreate
	}
	return ""
}

package environments

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// errNotReady is the error of an environment whose network change was
// applied but whose container didn't become ready.
const errNotReady = "Environment did not become ready after its network change; retry to recover"

// create realises name's saved VPN definition. Its connection is checked
// again under the reference lock, so a deletion can't race the transition.
func (n *Networking) create(ctx context.Context, name string, progress func(string, float64)) (Environment, error) {
	var err error
	if err = ValidName(name); err != nil {
		return Environment{}, err
	}
	if _, ok := n.record(name); ok {
		return Environment{}, ErrExists
	}
	if err = n.service.noPendingReplacement(name); err != nil {
		return Environment{}, err
	}
	if _, err = n.service.Eng.Inspect(ctx, containerName(name)); !errors.Is(err, engine.ErrNotFound) {
		if err == nil {
			err = ErrExists
		}
		return Environment{}, err
	}
	// Service.Create has checked the host sources and the image.
	n.refs.Lock()
	r := networkTransition{Name: name, Pending: true}
	def, ok := n.service.Defs.Get(name)
	if !ok {
		err = ErrNotFound
	} else {
		_, err = n.ValidateSettings(def.Settings())
	}
	if err == nil {
		c, _ := n.Connections.Get(def.Network.Connection)
		r.ConnectionRevision = c.Revision
		err = n.saveRecord(r)
	}
	n.refs.Unlock()
	if err != nil {
		return Environment{}, err
	}
	if err = os.MkdirAll(n.service.Layout.HomeOf(name), 0755); err != nil {
		return Environment{}, n.fail(r, phaseFolder)
	}
	if err = n.service.createNetwork(ctx, name); err != nil {
		return Environment{}, n.fail(r, phaseBridgeCreate)
	}
	if err = n.apply(ctx, r, progress); err != nil {
		return Environment{}, err
	}
	return n.service.Get(ctx, name)
}

// recreate saves name's definition changed to set, as a pending transition,
// and applies it. The change is validated first. The old container is then
// stopped before the change is saved and replaced, not parked: a failure never
// brings back a container on the networking the definition has left. A
// change that can't be saved brings the old container back as it was.
func (n *Networking) recreate(ctx context.Context, name string, set Settings, progress func(string, float64)) (Environment, error) {
	var err error
	d, err := n.service.Get(ctx, name)
	if err != nil {
		return Environment{}, err
	}
	if d.Conflict != "" {
		return Environment{}, fmt.Errorf("%w: %s", ErrConflict, d.Conflict)
	}
	// A replacement still being recovered must finish first: it could
	// bring back a direct container under a VPN definition.
	if err = n.service.noPendingReplacement(name); err != nil {
		return Environment{}, err
	}
	if _, err = n.ValidateSettings(set); err != nil {
		return Environment{}, err
	}
	if err = n.service.checkHostSources(ctx, n.service.hostSources(set.Volumes), set.DockerAccess); err != nil {
		return Environment{}, err
	}
	// The old container is stopped below: refuse a change it couldn't be
	// replaced by while it is still running.
	if err = n.service.missingFolder(name, set.Volumes); err != nil {
		return Environment{}, err
	}
	img, err := n.service.EnsureImage(ctx, set.Image, progress)
	if err != nil {
		return Environment{}, err
	}
	r := n.transition(name)
	restore := func() {
		if d.Container.ID == "" {
			return
		}
		bg := context.WithoutCancel(ctx)
		if d.Container.Restart != "" {
			_ = n.service.Eng.UpdateRestartPolicy(bg, d.Container.ID, d.Container.Restart)
		}
		if d.Container.Running() {
			_ = n.service.Eng.Start(bg, d.Container.ID)
		}
	}
	// Disable automatic restart before stopping the old direct consumer.
	if d.Container.ID != "" {
		if err = n.service.Eng.UpdateRestartPolicy(ctx, d.Container.ID, "no"); err != nil {
			return Environment{}, errors.New("could not disable independent environment restart")
		}
		if d.Container.Running() {
			if err = n.service.Eng.Stop(ctx, d.Container.ID, stopTimeout); err != nil {
				restore()
				return Environment{}, errors.New("could not stop environment for network change")
			}
		}
	}
	n.refs.Lock()
	set, err = n.ValidateSettings(set)
	var prev Definition
	changed := false // the definition no longer matches the old container
	if err == nil {
		_, err = n.service.Defs.Update(name, func(def *Definition) error {
			prev = *def
			*def = withSettings(*def, set, img.ID)
			return nil
		})
		changed = err == nil
	}
	if err == nil {
		r.Pending = true
		r.Repair = false
		r.Error = ""
		r.ConnectionRevision = 0
		if set.VPNConnectionID != "" {
			c, _ := n.Connections.Get(set.VPNConnectionID)
			r.ConnectionRevision = c.Revision
		}
		// The definition follows the journal: without the journal's
		// transition, the change is taken back.
		if err = n.saveRecord(r); err != nil {
			if e := n.service.revertSettings(name, prev); e != nil {
				n.service.Log.Warn("restoring the definition after a failed network change", "environment", name, "err", e)
			} else {
				changed = false
			}
		}
	}
	n.refs.Unlock()
	if err != nil {
		// The old container is only brought back under its own definition;
		// otherwise it stays stopped, without automatic restart.
		if !changed {
			restore()
		}
		return Environment{}, err
	}
	// A folder error of the old grants goes with them.
	_ = n.service.checkFolders(name)
	if err = n.apply(ctx, r, progress); err != nil {
		return Environment{}, err
	}
	return n.service.Get(ctx, name)
}

// apply is called under the environment operation lock with a durable pending
// transition. It realises the current definition, fail closed: the old
// container is removed and, for a VPN, the runtime prepared before the new
// container is created in its namespace. It never rolls back a committed
// selection to direct networking. A transition to direct networking that
// completes is forgotten: the environment is then an ordinary one.
func (n *Networking) apply(ctx context.Context, r networkTransition, progress func(string, float64)) error {
	s := n.service
	progress("Preparing environment networking", -1)
	if phase := n.ensureNetwork(ctx, r); phase != "" {
		return n.fail(r, phase)
	}
	if err := n.repin(ctx, r); err != nil {
		return err
	}
	if c, err := s.Eng.Inspect(ctx, containerName(r.Name)); err == nil {
		if !s.owns(c.Labels, r.Name) {
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
	if err := n.runtime.Remove(ctx, r.Name); err != nil {
		return n.fail(r, phaseRuntimeRemove)
	}
	r.RuntimeID = ""
	n.mu.Lock()
	delete(n.gateways, r.Name)
	n.mu.Unlock()
	def, ok := s.Defs.Get(r.Name)
	if !ok {
		return n.fail(r, phaseDefinition)
	}
	r.ConnectionRevision = 0
	if def.Network.Kind == NetworkVPN {
		c, err := n.Connections.Get(def.Network.Connection)
		if err != nil {
			return n.fail(r, phaseConnection)
		}
		g, err := n.runtime.Ensure(ctx, r.Name, containerName(r.Name), c.ID, def.Network.LANExceptions)
		if err != nil {
			return n.fail(r, phaseEnsure)
		}
		r.RuntimeID = g.ID
		r.ConnectionRevision = c.Revision
		n.mu.Lock()
		n.gateways[r.Name] = g
		n.mu.Unlock()
	}
	if err := n.saveRecord(r); err != nil {
		return n.fail(r, phaseSnapshot)
	}
	if err := s.createContainer(ctx, r.Name, def.Revision, def.Settings(), pinned(def), r.RuntimeID); err != nil {
		// The cause is logged for the owner, never recorded: it may hold
		// private host paths.
		s.Log.Warn("creating the environment's container", "environment", r.Name, "err", err)
		return n.fail(r, phaseCreate)
	}
	running := def.Power == PowerRunning
	if running {
		if err := s.Eng.Start(ctx, containerName(r.Name)); err != nil {
			return n.fail(r, phaseStart)
		}
	}
	r.AppliedRevision = def.Revision
	r.AppliedConnectionRevision = r.ConnectionRevision
	r.Pending = false
	r.Repair = false
	r.Error = ""
	if def.Network.Kind == NetworkVPN {
		if err := n.saveRecord(r); err != nil {
			return n.fail(r, phaseSnapshot)
		}
		n.setStatus(r.Name, vpn.Status{State: "connecting", ChangedAt: time.Now().UTC()})
	} else if err := n.forget(r.Name); err != nil {
		return n.fail(r, phaseSnapshot)
	}
	if running {
		// The applied state stands: a container that isn't ready is
		// reported as the environment's error, not as a pending transition,
		// so the poll keeps restoring its power. A start, restart or edit
		// that succeeds clears it.
		if err := s.WaitReady(ctx, r.Name, 2*time.Minute); errors.Is(err, context.Canceled) {
			// The caller went away, not the container: it isn't reported
			// as not ready.
			return err
		} else if err != nil {
			n.service.Log.Warn("environment network recovery failed", "phase", string(phaseReady))
			s.setError(r.Name, errNotReady)
			if def.Network.Kind == NetworkVPN {
				n.setStatus(r.Name, vpn.Status{State: "blocked", Reason: errNotReady, ChangedAt: time.Now().UTC()})
			}
			return errors.New(errNotReady)
		}
	}
	s.setError(r.Name, "")
	return nil
}

// repin makes sure the image r's environment is pinned to is on the engine
// before apply tears anything down: one that is gone is pulled again by its
// reference, as a new revision (Service.repin). One that can't be fails the
// transition as a change, with the old pair left as it was; the poll leaves
// it for a start or an upgrade, rather than pulling again every pass.
func (n *Networking) repin(ctx context.Context, r networkTransition) error {
	s := n.service
	def, ok := s.Defs.Get(r.Name)
	if !ok {
		return n.fail(r, phaseDefinition)
	}
	if def.Image.ID == "" {
		return nil
	}
	if _, err := s.Eng.ImageInspect(ctx, def.Image.ID); err == nil {
		return nil
	} else if !errors.Is(err, engine.ErrNotFound) {
		return n.fail(r, phaseImage)
	}
	_, err := s.repin(ctx, r.Name)
	if err == nil {
		return nil
	}
	s.Log.Warn("pulling an environment's pinned image again", "environment", r.Name, "err", err)
	r.Repair = false
	return n.failWith(r, phaseImage, fmt.Sprintf("The image %s (%s) this environment is pinned to is gone from the engine and could not be pulled again; upgrade the environment or choose another image", def.Image.Ref, def.Image.ID))
}

// startable refuses a start of name that can't succeed, before its desired
// power is saved: a replacement still being recovered, or a VPN connection
// that is gone.
func (n *Networking) startable(name string) error {
	if err := n.service.noPendingReplacement(name); err != nil {
		return err
	}
	if def, ok := n.service.Defs.Get(name); !ok {
		return ErrNotFound
	} else if def.Network.Kind == NetworkVPN {
		if _, err := n.Connections.Get(def.Network.Connection); err != nil {
			return errors.New("the selected VPN connection is unavailable; the environment stays stopped")
		}
	}
	return nil
}

// power stops, or brings up and starts, an environment the coordinator
// manages. The caller has saved its desired power in the definition, after
// startable for a start or restart.
func (n *Networking) power(ctx context.Context, name, action string) error {
	r := n.transition(name)
	if action == "stop" {
		if c, e := n.service.Eng.Inspect(ctx, containerName(name)); e == nil {
			if !n.service.owns(c.Labels, name) {
				return fmt.Errorf("%w: the container %s isn't this environment's", ErrConflict, containerName(name))
			}
			if err := n.service.Eng.Stop(ctx, c.ID, stopTimeout); err != nil {
				return errors.New("could not stop environment")
			}
			n.setStatus(name, vpn.Status{State: "stopped", ChangedAt: time.Now().UTC()})
			return nil
		} else if errors.Is(e, engine.ErrNotFound) {
			return nil
		} else {
			return errors.New("could not stop environment")
		}
	}
	started, err := n.recover(ctx, r, true)
	if err != nil {
		return err
	}
	if !started { // recover didn't just boot it: apply's or its own start
		if action == "restart" {
			err = n.service.Eng.Restart(ctx, containerName(name), stopTimeout)
		} else {
			err = n.service.Eng.Start(ctx, containerName(name))
		}
		if err != nil {
			return err
		}
	}
	n.service.setError(name, "")
	return nil
}
func (n *Networking) remove(ctx context.Context, name string, files bool) error {
	r := n.transition(name)
	r.Removing = true
	r.Pending = true
	r.DeleteFiles = files
	if err := n.saveRecord(r); err != nil {
		return err
	}
	return n.finishRemove(ctx, r)
}

// finishRemove is idempotent. The definition is deleted before the
// transition, so an interrupted removal is finished, never applied.
func (n *Networking) finishRemove(ctx context.Context, r networkTransition) error {
	s := n.service
	if c, err := s.Eng.Inspect(ctx, containerName(r.Name)); err == nil {
		if !s.owns(c.Labels, r.Name) {
			return errors.New("environment identity changed; removal refused")
		}
	} else if !errors.Is(err, engine.ErrNotFound) {
		return errors.New("could not inspect environment for removal")
	}
	if err := s.Eng.Remove(ctx, containerName(r.Name)); err != nil && !errors.Is(err, engine.ErrNotFound) {
		return errors.New("could not remove environment")
	}
	if err := n.runtime.Remove(ctx, r.Name); err != nil {
		return errors.New("could not remove environment VPN runtime")
	}
	n.mu.Lock()
	delete(n.gateways, r.Name)
	delete(n.starts, r.Name)
	n.mu.Unlock()
	if err := s.removeNetwork(ctx, r.Name); errors.Is(err, ErrConflict) {
		s.Log.Warn("leaving a network that isn't this environment's", "environment", r.Name, "err", err)
	} else if err != nil && !errors.Is(err, engine.ErrNotFound) {
		return errors.New("could not remove environment network")
	}
	if r.DeleteFiles {
		if err := s.removeTree(ctx, s.Layout.Home, r.Name); err != nil {
			return err
		}
	}
	n.refs.Lock()
	if err := s.forget(r.Name); err != nil {
		n.refs.Unlock()
		return err
	}
	err := n.forget(r.Name)
	n.refs.Unlock()
	return errors.Join(err, s.removed(r.Name))
}

// A creation may have crashed after its durable snapshot but before its bridge.
func (n *Networking) ensureNetwork(ctx context.Context, r networkTransition) recoveryPhase {
	if err := n.service.ensureNetwork(ctx, r.Name); errors.Is(err, ErrConflict) {
		return phaseBridgeIdentity
	} else if err != nil {
		return phaseBridgeCreate
	}
	return ""
}

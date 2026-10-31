package computers

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// recover brings name's container and runtime in line with its definition,
// which wins over the transition: a definition ahead of the applied revision,
// a pending transition, a changed connection revision or a lost runtime is
// applied fail closed. A stopped computer without a container and with no
// transition in flight is left so until it is started. A definition whose connection is gone stays stopped,
// with the error. A failed apply of a change (its error recorded) is retried
// only with retry: a start, restart, edit or rollout, never by the poll or
// at startup. A failed repair of the applied state, a lost runtime or an
// exited container, is retried by the poll too; a definition or connection
// that has moved on makes it a change. Nothing is applied while a
// replacement is still being recovered. A stopped runtime of a running
// computer is started again, its consumer kept; a stopped computer's
// runtime is left for its start. It reports whether it started the
// container itself: an exited one, or a replacement that apply has started
// and waited for, as its power says.
func (n *Networking) recover(ctx context.Context, r networkTransition, retry bool) (bool, error) {
	if r.Removing {
		return false, n.finishRemove(ctx, r)
	}
	s := n.service
	def, defined := s.Defs.Get(r.Name)
	var conn vpn.Connection
	var connErr error
	if defined && def.Network.Kind == NetworkVPN {
		conn, connErr = n.Connections.Get(def.Network.Connection)
	}
	// Nothing in flight repairs the applied state, unless the definition or
	// the connection has moved on; a repair whose target moved is a change.
	moved := def.Revision != r.AppliedRevision ||
		(def.Network.Kind == NetworkVPN && connErr == nil && conn.Revision != r.AppliedConnectionRevision)
	if !r.Pending {
		r.Repair = !moved
	} else if moved {
		r.Repair = false
	}
	if r.Error != "" && r.Pending && !r.Repair && !retry {
		return false, nil
	}
	if err := s.noPendingReplacement(r.Name); err != nil {
		return false, err
	}
	if !defined {
		return false, n.fail(r, phaseDefinition)
	}
	c, ce := s.Eng.Inspect(ctx, containerName(r.Name))
	if ce != nil && !errors.Is(ce, engine.ErrNotFound) {
		return false, n.fail(r, phaseInspect)
	}
	exists := ce == nil
	if exists && !s.owns(c.Labels, r.Name) {
		return false, n.fail(r, phaseIdentity)
	}
	if connErr != nil {
		// The computer stays stopped. Stopping it is the poll's to
		// retry, until it holds; the gone connection then waits for an edit.
		r.Pending = true
		r.Repair = false
		if exists {
			if err := s.Eng.UpdateRestartPolicy(ctx, c.ID, "no"); err != nil {
				r.Repair = true
				return false, n.fail(r, phaseRestartPolicy)
			}
			if c.Running() {
				if err := s.Eng.Stop(ctx, c.ID, stopTimeout); err != nil {
					r.Repair = true
					return false, n.fail(r, phaseStop)
				}
			}
		}
		return false, n.fail(r, phaseConnection)
	}
	running := def.Power == PowerRunning
	if !exists && !running && !r.Pending {
		return false, nil // created when started; a transition in flight is finished
	}
	replace := r.Pending || !exists || def.Revision != r.AppliedRevision ||
		c.Labels[LabelRevision] != strconv.FormatUint(def.Revision, 10)
	if def.Network.Kind == NetworkVPN {
		if conn.Revision != r.AppliedConnectionRevision {
			replace = true
			r.ConnectionRevision = conn.Revision
		}
		if !replace && !running {
			// A stopped computer's runtime is prepared when it is
			// started: a lost or stopped one is no reason to recreate it.
			if c.Restart != "no" {
				if err := s.Eng.UpdateRestartPolicy(ctx, c.ID, "no"); err != nil {
					return false, n.fail(r, phaseRestartPolicy)
				}
			}
			if c.Running() {
				if err := s.Eng.Stop(ctx, c.ID, stopTimeout); err != nil {
					return false, n.fail(r, phaseStop)
				}
			}
			if err := n.stopTailscale(ctx, r.Name); err != nil {
				return false, err
			}
			return false, nil
		}
		if !replace && !retry && !c.Running() && !n.mayAutoStart(r.Name) {
			return false, nil // an exited container is started again after its backoff
		}
		if !replace && c.Running() {
			// A consumer must rejoin a gateway that is started again, and
			// must not run without it meanwhile.
			if g, err := s.Eng.Inspect(ctx, r.RuntimeID); err == nil && !g.Running() {
				if err = s.Eng.Stop(ctx, c.ID, stopTimeout); err != nil {
					return false, n.fail(r, phaseStop)
				}
				c.State = "exited"
			}
		}
		if !replace {
			// A runtime from before the rename is replaced, as a lost one is.
			// Compatibility code, removable with legacyRuntimes.
			legacy, err := s.legacyRuntimes(ctx, r.Name)
			if err != nil {
				return false, n.fail(r, phaseInspect)
			}
			replace = len(legacy) > 0
		}
		if !replace {
			g, err := n.runtime.Ensure(ctx, r.Name, containerName(r.Name), conn.ID, def.Network.LANExceptions)
			if err != nil || c.NetworkContainer != g.ID {
				replace = true
			} else {
				n.mu.Lock()
				n.gateways[r.Name] = g
				n.mu.Unlock()
			}
		}
	}
	if replace {
		r.Pending = true
		if err := n.saveRecord(r); err != nil {
			return false, n.fail(r, phaseSnapshot)
		}
		return true, n.apply(ctx, r, func(string, float64) {})
	}
	if c.Restart != "no" && def.Network.Kind == NetworkVPN {
		if err := s.Eng.UpdateRestartPolicy(ctx, c.ID, "no"); err != nil {
			return false, n.fail(r, phaseRestartPolicy)
		}
	}
	if running && !c.Running() {
		if def.Network.Kind == NetworkVPN {
			if err := n.prepareTailscaleServices(ctx, def, r.RuntimeID); err != nil {
				return false, err
			}
		}
		if def.Network.Kind == NetworkVPN {
			n.startedAt(r.Name, retry)
		}
		if err := s.Eng.Start(ctx, c.ID); err != nil {
			return false, n.fail(r, phaseStart)
		}
		return true, nil
	}
	if !running && c.Running() {
		if err := s.Eng.Stop(ctx, c.ID, stopTimeout); err != nil {
			return false, n.fail(r, phaseStop)
		}
	}
	return false, nil
}

// An exited VPN consumer has no engine restart policy; the poll starts it
// again, backing off while it keeps exiting: from one health interval,
// doubling to five minutes, and from the start again once a start has
// held for ten minutes. A start, restart or edit starts it at once.
const (
	maxRestartDelay    = 5 * time.Minute
	restartStableAfter = 10 * time.Minute
)

type autoStart struct {
	at    time.Time
	delay time.Duration
}

// mayAutoStart reports whether the poll may start name's exited container.
func (n *Networking) mayAutoStart(name string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	a := n.starts[name]
	return a.at.IsZero() || n.now().Sub(a.at) >= a.delay
}

// startedAt records a start of name's container, by the poll unless retry.
func (n *Networking) startedAt(name string, retry bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.now()
	a := n.starts[name]
	switch {
	case retry || a.at.IsZero() || now.Sub(a.at) >= restartStableAfter:
		a.delay = vpn.HealthInterval
	default:
		a.delay = min(2*a.delay, maxRestartDelay)
	}
	a.at = now
	n.starts[name] = a
}

// Reconcile runs once at startup. It finishes interrupted transitions and
// repairs the applied state; a failed apply of a change waits for a start,
// restart or edit, as it does for a direct computer. A running computer's
// VPN runtime from before the rename is replaced; a stopped one's is left
// for its start. Other VPN resources from before the rename are removed.
func (n *Networking) Reconcile(ctx context.Context) error {
	var errs []error
	names := n.names()
	// Compatibility code, removable with removeLegacyLeftovers.
	n.service.removeLegacyLeftovers(ctx, names)
	for _, name := range names {
		unlock, err := n.lock(ctx, name)
		if err == nil {
			_, err = n.recover(ctx, n.transition(name), false)
			unlock()
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Poll is the single central observation path. Viewer reads never probe. Work
// shares eight bounded slots across observation and repair. Saturated passes skip
// records; long repairs can delay unrelated health observations (no fleet fairness).
func (n *Networking) Poll(ctx context.Context, changed func(string)) {
	var wg sync.WaitGroup
	slots := n.pollSlots
	for _, item := range n.names() {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		default:
			continue
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			defer func() { <-slots }()
			check, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			// A computer whose operation is in progress is observed by
			// a later pass: waiting for it would hold a slot.
			unlock, ok := n.tryLock(name)
			if !ok {
				return
			}
			defer unlock()
			r := n.transition(name)
			def, defined := n.service.Defs.Get(name)
			viaVPN := defined && def.Network.Kind == NetworkVPN
			if !viaVPN && !r.Pending && !r.Removing {
				return
			}
			before, _ := n.service.Get(check, name)
			// Pending transitions/removal include explicit direct selections.
			// Otherwise reconcile durable power alongside dependency/revision loss.
			// A failed apply of a change waits for a start, restart or edit;
			// a failed repair of the applied state is retried.
			needsRepair := (r.Pending && (r.Error == "" || r.Repair)) || r.Removing || !viaVPN
			var c engine.Container
			if !needsRepair {
				g, ge := n.service.Eng.Inspect(check, r.RuntimeID)
				var ce error
				c, ce = n.service.Eng.Inspect(check, containerName(name))
				conn, ve := n.Connections.Get(def.Network.Connection)
				// A stopped computer's runtime is prepared when it starts.
				gatewayLost := def.Power == PowerRunning && (ge != nil || !g.Running())
				tailscaleStopPending := def.Power == PowerStopped && ve == nil && conn.Provider == "tailscale" && ge == nil && g.Running()
				needsRepair = tailscaleStopPending || gatewayLost || ce != nil || c.Running() != (def.Power == PowerRunning) || c.NetworkContainer != r.RuntimeID ||
					ve != nil || conn.Revision != r.AppliedConnectionRevision || def.Revision != r.AppliedRevision
			}
			if needsRepair {
				repair, cancelRepair := context.WithTimeout(ctx, 3*time.Minute)
				// recover persists a safe phase in the journal/status before returning.
				_, _ = n.recover(repair, r, false)
				cancelRepair()
			} else {
				_ = n.syncServices(check, def)
				status, err := n.runtime.Status(check, name)
				if err != nil {
					status = vpn.Status{State: "blocked", Reason: "VPN status unavailable"}
				}
				if !c.Running() {
					status.State = "stopped"
					status.CurrentExitIP = ""
				}
				n.setStatus(name, status)
			}
			after, _ := n.service.Get(ctx, name)
			if changed != nil && !reflect.DeepEqual(before.VPN, after.VPN) {
				changed(name)
			}
		}(item)
	}
	wg.Wait()
}
func (n *Networking) Run(ctx context.Context, changed func(string)) {
	ticker := time.NewTicker(vpn.HealthInterval)
	defer ticker.Stop()
	for {
		go n.Poll(ctx, changed)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-n.wake:
		}
	}
}

// Gateway events trigger recovery through the same coordinator, never a computer
// event of their own. Another instance's or an unmanaged computer's gateway
// cannot wake or mutate another pair.
// Only a change of a container's state wakes it: execs, the supervisor's own
// probes and healthchecks among them, and health reports don't.
func (n *Networking) NotifyEngineEvent(e engine.Event) {
	switch e.Action {
	case "start", "die", "stop", "destroy", "create", "restart", "kill", "oom", "pause", "unpause":
	default:
		return
	}
	var owned bool
	if e.Labels[vpn.LabelRole] != "" {
		name := e.Labels[vpn.LabelComputer]
		owned = e.Labels[vpn.LabelInstance] == n.service.Instance && ValidName(name) == nil && n.selected(name)
	} else if n.service.owns(e.Labels, e.Labels[LabelName]) {
		owned = n.selected(e.Labels[LabelName])
	}
	if owned {
		select {
		case n.wake <- struct{}{}:
		default:
		}
	}
}

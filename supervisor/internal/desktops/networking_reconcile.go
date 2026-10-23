package desktops

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

func (n *Networking) recover(ctx context.Context, r networkIntent) error {
	if r.Removing {
		return n.finishRemove(ctx, r)
	}
	s := n.service
	c, ce := s.Eng.Inspect(ctx, containerName(r.Name))
	replace := r.Pending || errors.Is(ce, engine.ErrNotFound)
	if r.Settings.Camera != "" && s.cameraGone(r.Settings.Camera) {
		r.Settings.Camera = ""
		replace = true
	}
	if ce != nil && !errors.Is(ce, engine.ErrNotFound) {
		return errors.New("could not inspect desktop for network recovery")
	}
	if r.Settings.VPNConnectionID != "" {
		conn, err := n.Connections.Get(r.Settings.VPNConnectionID)
		if err != nil {
			r.Pending = true
			if ce == nil {
				_ = s.Eng.UpdateRestartPolicy(ctx, c.ID, "no")
				_ = s.Eng.Stop(ctx, c.ID, stopTimeout)
			}
			return n.fail(r)
		}
		if conn.Revision != r.AppliedRevision {
			replace = true
			r.Revision = conn.Revision
		}
		if !replace {
			g, err := n.runtime.Ensure(ctx, r.ID, r.Name, containerName(r.Name), r.Settings.VPNConnectionID, r.Settings.LANExceptions, !s.Cfg.InContainer)
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
			return err
		}
		return n.apply(ctx, r, func(string, float64) {})
	}
	if c.Restart != "no" && r.Settings.VPNConnectionID != "" {
		if err := s.Eng.UpdateRestartPolicy(ctx, c.ID, "no"); err != nil {
			return errors.New("could not disable independent desktop restart")
		}
	}
	if r.Running && !c.Running() {
		if err := s.Eng.Start(ctx, c.ID); err != nil {
			return errors.New("could not start attached desktop")
		}
	}
	if !r.Running && c.Running() {
		return s.Eng.Stop(ctx, c.ID, stopTimeout)
	}
	return nil
}
func (n *Networking) Reconcile(ctx context.Context) error {
	var errs []error
	for _, r := range n.all() {
		unlock, err := n.lock(ctx, r.Name)
		if err == nil {
			latest, ok := n.record(r.Name)
			if ok {
				err = n.recover(ctx, latest)
			}
			unlock()
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Poll is the single central observation path. Viewer reads never probe. Work
// is bounded and parallel so an unavailable gateway cannot stall the whole list.
func (n *Networking) Poll(ctx context.Context, changed func(string)) {
	var wg sync.WaitGroup
	slots := n.pollSlots
	for _, item := range n.all() {
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
			unlock, err := n.lock(check, name)
			if err != nil {
				return
			}
			defer unlock()
			r, ok := n.record(name)
			if !ok || r.Settings.VPNConnectionID == "" {
				return
			}
			before, _ := n.service.Get(check, name)
			// Engine dependency loss and revision changes use the same durable repair.
			g, ge := n.service.Eng.Inspect(check, r.RuntimeID)
			c, ce := n.service.Eng.Inspect(check, containerName(name))
			conn, ve := n.Connections.Get(r.Settings.VPNConnectionID)
			if r.Pending || ge != nil || !g.Running() || ce != nil || c.NetworkContainer != r.RuntimeID || ve != nil || conn.Revision != r.AppliedRevision {
				repair, cancelRepair := context.WithTimeout(ctx, 3*time.Minute)
				_ = n.recover(repair, r)
				cancelRepair()
			} else {
				status, err := n.runtime.Status(check, r.ID)
				if err != nil {
					status = vpn.Status{State: "blocked", Reason: "VPN status unavailable"}
				}
				if !c.Running() {
					status.State = "stopped"
					status.CurrentExitIP = ""
				}
				n.setStatus(r.ID, status)
			}
			after, _ := n.service.Get(ctx, name)
			if changed != nil && !reflect.DeepEqual(before.VPN, after.VPN) {
				changed(name)
			}
		}(item.Name)
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

// Gateway events trigger recovery through the same coordinator, never a desktop
// event of their own. Unowned gateway IDs cannot wake or mutate another pair.
func (n *Networking) NotifyEngineEvent(e engine.Event) {
	id := e.Labels[LabelID]
	if e.Labels[vpn.LabelRole] != "" {
		id = e.Labels[vpn.LabelDesktopID]
	} else if !IsDesktop(e.Labels) {
		return
	}
	n.mu.Lock()
	_, owned := n.records[id]
	n.mu.Unlock()
	if owned {
		select {
		case n.wake <- struct{}{}:
		default:
		}
	}
}

package vpn

import (
	"context"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"os"
	"path/filepath"
	"time"
)

// Root tailscaled writes owner-only node keys. The supervisor runs as UID 1000,
// so an offline, networkless helper restores ownership before it can withdraw
// persisted publication. No desktop or daemon may be using dir during this call.
func (m *Manager) withdrawSavedServe(ctx context.Context, dir string) error {
	path := filepath.Join(dir, "tailscaled.state")
	if _, err := os.ReadFile(path); os.IsPermission(err) {
		if err = m.restoreStateOwner(ctx, dir); err != nil {
			return err
		}
	}
	return discardSavedServe(dir)
}
func (m *Manager) restoreStateOwner(ctx context.Context, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	attempt, err := newID()
	if err != nil {
		return errors.New("Tailscale state preparation identity unavailable")
	}
	name := "deskyard-ts-state-" + attempt
	labels := map[string]string{LabelRole: "state-owner", labelAttempt: attempt}
	if m.instance != "" {
		labels[LabelInstance] = m.instance
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		cs, e := m.eng.List(cleanup, labels)
		if e != nil {
			return
		}
		for _, c := range cs {
			if c.Name == name && ownsLabels(c.Labels, labels) {
				_ = m.eng.Remove(cleanup, c.ID)
			}
		}
	}()
	spec := engine.ContainerSpec{Name: name, Image: TailscaleImage, Network: "none", Labels: labels, Restart: "no", Mounts: []engine.Mount{{Source: dir, Target: "/var/lib/tailscale", MustExist: true}}, Entrypoint: []string{"chown"}, Cmd: []string{"-R", "1000:1000", "/var/lib/tailscale"}}
	id, err := m.eng.Create(ctx, spec)
	if err != nil {
		return errors.New("Tailscale offline state preparation failed")
	}
	if err = m.eng.Start(ctx, id); err != nil {
		return errors.New("Tailscale offline state preparation failed")
	}
	for {
		c, e := m.eng.Inspect(ctx, id)
		if e != nil {
			return errors.New("Tailscale offline state preparation unavailable")
		}
		if !c.Running() {
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("Tailscale offline state preparation timed out")
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
}

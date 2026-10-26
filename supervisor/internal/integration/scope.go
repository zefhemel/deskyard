//go:build vpnintegration

// Package integration contains opt-in shared-engine-safe verification helpers.
package integration

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"io"
	"time"
)

const Label = "deskyard.vpn.e2e-fixture"

type ScopedEngine struct {
	engine.Engine
	Scope        string
	BeforeRemove func(context.Context, engine.Container)
}

func (e *ScopedEngine) labels(labels map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range labels {
		out[k] = v
	}
	out[Label] = e.Scope
	return out
}
func (e *ScopedEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	if s.NetworkContainer != "" {
		if _, err := e.Inspect(ctx, s.NetworkContainer); err != nil {
			return "", err
		}
	}
	if s.Network != "" && s.Network != "none" {
		if _, err := e.network(ctx, s.Network); err != nil {
			return "", err
		}
	}
	s.Labels = e.labels(s.Labels)
	return e.Engine.Create(ctx, s)
}
func (e *ScopedEngine) Inspect(ctx context.Context, id string) (engine.Container, error) {
	c, err := e.Engine.Inspect(ctx, id)
	if err == nil && c.Labels[Label] != e.Scope {
		return engine.Container{}, engine.ErrNotFound
	}
	return c, err
}
func (e *ScopedEngine) List(ctx context.Context, l map[string]string) ([]engine.Container, error) {
	return e.Engine.List(ctx, e.labels(l))
}
func (e *ScopedEngine) Start(ctx context.Context, id string) error {
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return err
	}
	return e.Engine.Start(ctx, c.ID)
}
func (e *ScopedEngine) Stop(ctx context.Context, id string, d time.Duration) error {
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return err
	}
	return e.Engine.Stop(ctx, c.ID, d)
}
func (e *ScopedEngine) Restart(ctx context.Context, id string, d time.Duration) error {
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return err
	}
	return e.Engine.Restart(ctx, c.ID, d)
}
func (e *ScopedEngine) Rename(ctx context.Context, id, name string) error {
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return err
	}
	return e.Engine.Rename(ctx, c.ID, name)
}
func (e *ScopedEngine) UpdateRestartPolicy(ctx context.Context, id, p string) error {
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return err
	}
	return e.Engine.UpdateRestartPolicy(ctx, c.ID, p)
}
func (e *ScopedEngine) Remove(ctx context.Context, id string) error {
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return err
	}
	if e.BeforeRemove != nil {
		e.BeforeRemove(ctx, c)
	}
	return e.Engine.Remove(ctx, c.ID)
}
func (e *ScopedEngine) Exec(ctx context.Context, id string, s engine.ExecSpec) (*engine.Process, error) {
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	return e.Engine.Exec(ctx, c.ID, s)
}
func (e *ScopedEngine) CreateNetwork(ctx context.Context, name, subnet string, l map[string]string) error {
	return e.Engine.CreateNetwork(ctx, name, subnet, e.labels(l))
}
func (e *ScopedEngine) Networks(ctx context.Context, l map[string]string) ([]engine.Network, error) {
	if l != nil {
		l = e.labels(l)
	}
	return e.Engine.Networks(ctx, l)
}
func (e *ScopedEngine) network(ctx context.Context, name string) (string, error) {
	ns, err := e.Engine.Networks(ctx, e.labels(nil))
	if err != nil {
		return "", err
	}
	for _, n := range ns {
		if (n.Name == name || n.ID == name) && n.Labels[Label] == e.Scope {
			return n.ID, nil
		}
	}
	return "", engine.ErrNotFound
}
func (e *ScopedEngine) RemoveNetwork(ctx context.Context, name string) error {
	id, err := e.network(ctx, name)
	if err != nil {
		return err
	}
	return e.Engine.RemoveNetwork(ctx, id)
}
func (e *ScopedEngine) ConnectNetwork(ctx context.Context, network, id string, a []string) error {
	n, err := e.network(ctx, network)
	if err != nil {
		return err
	}
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return err
	}
	return e.Engine.ConnectNetwork(ctx, n, c.ID, a)
}
func (e *ScopedEngine) DisconnectNetwork(ctx context.Context, network, id string) error {
	n, err := e.network(ctx, network)
	if err != nil {
		return err
	}
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return err
	}
	return e.Engine.DisconnectNetwork(ctx, n, c.ID)
}
func (e *ScopedEngine) Events(ctx context.Context) (<-chan engine.Event, error) {
	source, err := e.Engine.Events(ctx)
	if err != nil {
		return nil, err
	}
	out := make(chan engine.Event)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-source:
				if !ok {
					return
				}
				if event.Labels[Label] == e.Scope {
					select {
					case out <- event:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return out, nil
}

func (e *ScopedEngine) Top(ctx context.Context, id, args string) ([]string, [][]string, error) {
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return e.Engine.Top(ctx, c.ID, args)
}
func (e *ScopedEngine) Stats(ctx context.Context, id string) (<-chan engine.Stats, error) {
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	return e.Engine.Stats(ctx, c.ID)
}
func (e *ScopedEngine) CopyToContainer(ctx context.Context, id, dir string, r io.Reader) error {
	c, err := e.Inspect(ctx, id)
	if err != nil {
		return err
	}
	return e.Engine.CopyToContainer(ctx, c.ID, dir, r)
}

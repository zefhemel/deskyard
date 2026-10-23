//go:build vpnintegration

package integration

import (
	"context"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"testing"
)

type scopeProbe struct {
	engine.Engine
	container engine.Container
	networks  []engine.Network
	removed   string
}

func (p *scopeProbe) Inspect(context.Context, string) (engine.Container, error) {
	return p.container, nil
}
func (p *scopeProbe) Remove(_ context.Context, id string) error { p.removed = id; return nil }
func (p *scopeProbe) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	return p.networks, nil
}
func (p *scopeProbe) RemoveNetwork(_ context.Context, id string) error { p.removed = id; return nil }
func TestScopedEngineRefusesForeignContainer(t *testing.T) {
	p := &scopeProbe{container: engine.Container{ID: "foreign", Labels: map[string]string{Label: "other"}}}
	e := &ScopedEngine{Engine: p, Scope: "ours"}
	if err := e.Remove(context.Background(), "name"); !errors.Is(err, engine.ErrNotFound) || p.removed != "" {
		t.Fatal("foreign mutation was not blocked")
	}
}
func TestScopedEngineMutatesCheckedIdentity(t *testing.T) {
	p := &scopeProbe{container: engine.Container{ID: "owned-id", Name: "reusable-name", Labels: map[string]string{Label: "ours"}}, networks: []engine.Network{{ID: "owned-network-id", Name: "reusable-network", Labels: map[string]string{Label: "ours"}}}}
	e := &ScopedEngine{Engine: p, Scope: "ours"}
	if err := e.Remove(context.Background(), "reusable-name"); err != nil || p.removed != "owned-id" {
		t.Fatal("removal followed mutable container name")
	}
	if err := e.RemoveNetwork(context.Background(), "reusable-network"); err != nil || p.removed != "owned-network-id" {
		t.Fatal("removal followed mutable network name")
	}
}

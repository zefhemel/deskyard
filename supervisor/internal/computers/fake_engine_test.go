package computers

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// fakeEngine keeps containers in memory.
type fakeEngine struct {
	engine.Engine
	port int
	mu   sync.Mutex
	cs   map[string]*engine.Container
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{cs: map[string]*engine.Container{}}
}

func (e *fakeEngine) Create(_ context.Context, s engine.ContainerSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.cs[s.Name]; ok {
		return "", errors.New("name in use")
	}
	e.cs[s.Name] = &engine.Container{ID: s.Name, Name: s.Name, Image: s.Image, ImageID: s.Image, Labels: s.Labels, Env: s.Env,
		State: "created", Devices: s.Devices, Mounts: s.Mounts, NetworkContainer: s.NetworkContainer, Restart: s.Restart, IP: e.desktopIP(s.Network)}
	return s.Name, nil
}

// desktopIP is the address map of a container on network when the engine
// serves desktops on a local port: the supervisor dials 127.0.0.1 there.
func (e *fakeEngine) desktopIP(network string) map[string]string {
	if e.port == 0 || network == "" {
		return nil
	}
	return map[string]string{network: "127.0.0.1"}
}

func (e *fakeEngine) Start(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cs[id]
	if !ok {
		return engine.ErrNotFound
	}
	c.State, c.Error = "running", ""
	return nil
}

func (e *fakeEngine) Restart(ctx context.Context, id string, t time.Duration) error {
	_ = e.Stop(ctx, id, t)
	return e.Start(ctx, id)
}

func (e *fakeEngine) Stop(_ context.Context, id string, _ time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.cs[id]; ok {
		c.State = "exited"
	}
	return nil
}

func (e *fakeEngine) UpdateRestartPolicy(_ context.Context, id, policy string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cs[id]
	if !ok {
		return engine.ErrNotFound
	}
	c.Restart = policy
	return nil
}

// Rename moves a container to another name, which is also its ID here.
func (e *fakeEngine) Rename(_ context.Context, id, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cs[id]
	if !ok {
		return engine.ErrNotFound
	}
	if _, taken := e.cs[name]; taken {
		return errors.New("name in use")
	}
	delete(e.cs, id)
	c.ID, c.Name = name, name
	e.cs[name] = c
	return nil
}

func (e *fakeEngine) Remove(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.cs, id)
	return nil
}

func (e *fakeEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.cs[id]; ok {
		return *c, nil
	}
	return engine.Container{}, engine.ErrNotFound
}

// List honours label filters, as the engine's does.
func (e *fakeEngine) List(_ context.Context, labels map[string]string) ([]engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []engine.Container
next:
	for _, c := range e.cs {
		for k, v := range labels {
			if c.Labels[k] != v {
				continue next
			}
		}
		out = append(out, *c)
	}
	return out, nil
}

func (e *fakeEngine) ImageInspect(_ context.Context, ref string) (engine.Image, error) {
	return engine.Image{ID: ref, Labels: map[string]string{LabelContract: Contract}}, nil
}

// newFakeService is a service on a fakeEngine whose containers' health
// checks pass.
func newFakeService(t *testing.T) (*Service, *fakeEngine) {
	eng := newFakeEngine()
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(health.Close)
	_, port, _ := net.SplitHostPort(health.Listener.Addr().String())
	eng.port, _ = strconv.Atoi(port)
	return New(eng, Config{DataDir: t.TempDir(), DesktopPort: eng.port}, slog.New(slog.DiscardHandler)), eng
}

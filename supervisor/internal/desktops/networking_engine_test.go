package desktops

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/engine/docker"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

const pairFixtureLabel = "deskyard.vpn.lifecycle-fixture"

// scopedPairEngine prevents reconciliation, events or cleanup from touching
// shared-engine desktops. Only Networks(nil) may read the global subnet inventory.
type scopedPairEngine struct {
	engine.Engine
	scope           string
	t               *testing.T
	observedUDP1194 bool
}

func (e *scopedPairEngine) labels(labels map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range labels {
		out[k] = v
	}
	out[pairFixtureLabel] = e.scope
	return out
}
func (e *scopedPairEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	s.Labels = e.labels(s.Labels)
	return e.Engine.Create(ctx, s)
}
func (e *scopedPairEngine) Inspect(ctx context.Context, id string) (engine.Container, error) {
	c, err := e.Engine.Inspect(ctx, id)
	if err == nil && c.Labels[pairFixtureLabel] != e.scope {
		return engine.Container{}, engine.ErrNotFound
	}
	return c, err
}
func (e *scopedPairEngine) List(ctx context.Context, l map[string]string) ([]engine.Container, error) {
	return e.Engine.List(ctx, e.labels(l))
}
func (e *scopedPairEngine) Start(ctx context.Context, id string) error {
	if _, err := e.Inspect(ctx, id); err != nil {
		return err
	}
	return e.Engine.Start(ctx, id)
}
func (e *scopedPairEngine) Stop(ctx context.Context, id string, d time.Duration) error {
	if _, err := e.Inspect(ctx, id); err != nil {
		return err
	}
	return e.Engine.Stop(ctx, id, d)
}
func (e *scopedPairEngine) Restart(ctx context.Context, id string, d time.Duration) error {
	if _, err := e.Inspect(ctx, id); err != nil {
		return err
	}
	return e.Engine.Restart(ctx, id, d)
}
func (e *scopedPairEngine) UpdateRestartPolicy(ctx context.Context, id, p string) error {
	if _, err := e.Inspect(ctx, id); err != nil {
		return err
	}
	return e.Engine.UpdateRestartPolicy(ctx, id, p)
}
func (e *scopedPairEngine) Remove(ctx context.Context, id string) error {
	if _, err := e.Inspect(ctx, id); err != nil {
		return err
	}
	if c, _ := e.Inspect(ctx, id); c.Labels[vpn.LabelRole] != "" && e.t != nil {
		b, _ := exec.CommandContext(ctx, "docker", "logs", id).CombinedOutput()
		seen := map[string]bool{}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "UDPv4 link remote") && strings.HasSuffix(strings.TrimSpace(line), ":1194") {
				e.observedUDP1194 = true
			}
			if strings.Contains(line, "AUTH_FAILED") && !seen["auth"] {
				e.t.Log("fixture runtime rejected disposable credentials: AUTH_FAILED")
				seen["auth"] = true
			}
			if strings.Contains(line, "custom endpoint port is not allowed") && !seen["port"] {
				e.t.Log("fixture runtime rejects unsupported NordVPN endpoint-port override")
				seen["port"] = true
			}
		}
	}
	return e.Engine.Remove(ctx, id)
}
func (e *scopedPairEngine) Exec(ctx context.Context, id string, s engine.ExecSpec) (*engine.Process, error) {
	if _, err := e.Inspect(ctx, id); err != nil {
		return nil, err
	}
	return e.Engine.Exec(ctx, id, s)
}
func (e *scopedPairEngine) CreateNetwork(ctx context.Context, name, subnet string, l map[string]string) error {
	return e.Engine.CreateNetwork(ctx, name, subnet, e.labels(l))
}
func (e *scopedPairEngine) Networks(ctx context.Context, l map[string]string) ([]engine.Network, error) {
	if l != nil {
		l = e.labels(l)
	}
	return e.Engine.Networks(ctx, l)
}
func (e *scopedPairEngine) network(ctx context.Context, name string) error {
	ns, err := e.Engine.Networks(ctx, e.labels(nil))
	if err != nil {
		return err
	}
	for _, n := range ns {
		if n.Name == name || n.ID == name {
			return nil
		}
	}
	return engine.ErrNotFound
}
func (e *scopedPairEngine) RemoveNetwork(ctx context.Context, name string) error {
	if err := e.network(ctx, name); err != nil {
		return err
	}
	return e.Engine.RemoveNetwork(ctx, name)
}
func (e *scopedPairEngine) ConnectNetwork(ctx context.Context, network, id string, a []string) error {
	if err := e.network(ctx, network); err != nil {
		return err
	}
	if _, err := e.Inspect(ctx, id); err != nil {
		return err
	}
	return e.Engine.ConnectNetwork(ctx, network, id, a)
}
func (e *scopedPairEngine) DisconnectNetwork(ctx context.Context, network, id string) error {
	if err := e.network(ctx, network); err != nil {
		return err
	}
	if _, err := e.Inspect(ctx, id); err != nil {
		return err
	}
	return e.Engine.DisconnectNetwork(ctx, network, id)
}
func (e *scopedPairEngine) Events(ctx context.Context) (<-chan engine.Event, error) {
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
				if event.Labels[pairFixtureLabel] == e.scope {
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

func TestVPNPairedEngineLifecycle(t *testing.T) {
	if os.Getenv("DESKYARD_VPN_LIFECYCLE_TEST") != "1" {
		t.Skip("set DESKYARD_VPN_LIFECYCLE_TEST=1 for scoped real desktop lifecycle")
	}
	raw, err := docker.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		t.Fatal(err)
	}
	e := &scopedPairEngine{Engine: raw, scope: randomHex(8), t: t}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		cs, err := e.List(cleanup, nil)
		if err != nil {
			t.Error(err)
		}
		for _, c := range cs {
			if err := e.Remove(cleanup, c.ID); err != nil {
				t.Error(err)
			}
		}
		ns, err := e.Networks(cleanup, map[string]string{})
		if err != nil {
			t.Error(err)
		}
		for _, n := range ns {
			if err := e.RemoveNetwork(cleanup, n.Name); err != nil {
				t.Error(err)
			}
		}
		cs, _ = e.List(cleanup, nil)
		ns, _ = e.Networks(cleanup, map[string]string{})
		if len(cs)+len(ns) != 0 {
			t.Error("fixture resources remain")
		}
	})
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	cfg := Config{DataDir: dir, Subnet: "10.217.0.0/16"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(e, cfg, log)
	state := filepath.Join(dir, ".deskyard")
	store, err := vpn.NewStore(state)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately invalid disposable credentials: this proves blocked-tunnel local
	// access/lifecycle, not provider authentication or healthy NordVPN egress.
	user, password := "fixture-invalid-user", "fixture-invalid-password"
	conn, err := store.Create(vpn.Input{Name: "Lifecycle fixture", Provider: "nordvpn", Country: "Netherlands", Username: &user, Password: &password})
	if err != nil {
		t.Fatal(err)
	}
	manager := &observedPairRuntime{VPNRuntime: vpn.NewManager(e, store, state), t: t}
	if err = s.ConfigureNetworking(store, manager, state); err != nil {
		t.Fatal(err)
	}
	name := "vpnlife-" + e.scope[:6]
	image := os.Getenv("CC_TEST_IMAGE")
	if image == "" {
		image = "deskyard-debian-xfce:selkies"
	}
	d, err := s.Create(ctx, name, Settings{Image: image, VPNConnectionID: conn.ID}, func(msg string, _ float64) { t.Log(msg) })
	if err != nil {
		t.Fatal(err)
	}
	if !d.Container.Running() || d.Container.NetworkContainer == "" || d.Container.Restart != "no" || d.Container.LocalPort != 0 {
		t.Fatalf("unsafe consumer %+v", d.Container)
	}
	token, id := d.Container.Env[TokenEnv], d.ID
	runtime := d.Container.NetworkContainer
	if err = s.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	gateway, err := e.Inspect(ctx, runtime)
	if err != nil || !gateway.Running() {
		t.Fatalf("stopped gateway: %v", err)
	}
	if err = s.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	if err = s.Restart(ctx, name); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Get(ctx, name)
	if d.Container.NetworkContainer != runtime {
		t.Fatal("ordinary restart replaced gateway")
	}
	// Runtime loss requires a new namespace and consumer; the journal survives a
	// supervisor replacement, keeping the same user identity and Caddy token.
	if err = e.Stop(ctx, runtime, time.Second); err != nil {
		t.Fatal(err)
	}
	fresh := New(e, cfg, log)
	if err = fresh.ConfigureNetworking(store, &observedPairRuntime{VPNRuntime: vpn.NewManager(e, store, state), t: t}, state); err != nil {
		t.Fatal(err)
	}
	if err = fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	d, err = fresh.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != id || d.Container.Env[TokenEnv] != token || d.Container.NetworkContainer == runtime || !d.Container.Running() {
		t.Fatal("recovery did not preserve identity and replace namespace")
	}
	set := d.Settings
	set.VPNConnectionID = ""
	d, err = fresh.Recreate(ctx, name, set, func(string, float64) {})
	if err != nil {
		t.Fatal(err)
	}
	if d.Container.NetworkContainer != "" || d.Container.LocalPort == 0 {
		t.Fatal("explicit disable did not restore direct networking")
	}
	if err = fresh.Remove(ctx, name, false); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Get(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove: %v", err)
	}
	if !e.observedUDP1194 {
		t.Fatal("did not observe provider-default UDP1194 in owned runtime logs")
	}
	t.Log("PASS provider-default NordVPN UDP1194 observed")
	t.Log("PASS real Selkies desktop, blocked-tunnel local access, idle runtime, restart, journal recovery, explicit disable and scoped cleanup")
}

type observedPairRuntime struct {
	VPNRuntime
	t *testing.T
}

func (r *observedPairRuntime) Ensure(ctx context.Context, id, name, network, connection string, exceptions []vpn.Exception, publish bool) (engine.Container, error) {
	g, err := r.VPNRuntime.Ensure(ctx, id, name, network, connection, exceptions, publish)
	if err != nil {
		r.t.Log("runtime Ensure diagnostic:", err)
	}
	return g, err
}
func (r *observedPairRuntime) Remove(ctx context.Context, id string) error {
	err := r.VPNRuntime.Remove(ctx, id)
	if err != nil {
		r.t.Log("runtime Remove diagnostic:", err)
	}
	return err
}

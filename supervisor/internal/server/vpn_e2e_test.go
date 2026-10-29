//go:build vpnintegration

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/engine/docker"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/integration"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
	"github.com/zefhemel/deskyard/supervisor/internal/web"
)

type e2eConfig struct {
	Scope, Endpoint, EndpointGateway, PublicKey, Listen, SelfID string
	Keys                                                        []string
	Production                                                  bool
}

// TestVPNEndToEnd is a scoped real supervisor. It runs only in a container, as
// the supervisor does: tests/vpn_e2e.py mounts the data folder at /data and its
// private directory (DESKYARD_VPN_E2E_DIR) beside it. The orchestration script
// supplies one owned endpoint and private fixture keys. A continue file releases
// lifecycle assertions after browser/agent API checks; stop releases the
// optional Nord UI.
func TestVPNEndToEnd(t *testing.T) {
	dir := os.Getenv("DESKYARD_VPN_E2E_DIR")
	if dir == "" {
		t.Skip("use tests/vpn_e2e.py")
	}
	b, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg e2eConfig
	if err = json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	duration := 45 * time.Minute
	if cfg.Production {
		duration = 12 * time.Hour
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	raw, err := docker.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		t.Fatal(err)
	}
	e := &integration.ScopedEngine{Engine: raw, Scope: cfg.Scope}
	var diagnosticMu sync.Mutex
	var outputSnapshots []map[string]any
	e.BeforeRemove = func(ctx context.Context, c engine.Container) {
		if c.Labels[vpn.LabelRole] == "" {
			return
		}
		probe, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		out, _, code, err := engine.Output(probe, e, c.ID, engine.ExecSpec{User: "root", Cmd: []string{"iptables", "-S", "OUTPUT"}})
		rules := strings.Split(strings.TrimSpace(string(out)), "\n")
		ready := err == nil && code == 0
		first := ready && len(rules) > 1 && rules[1] == "-A OUTPUT -j DYVPN"
		t.Logf("owned gateway before removal: OUTPUT available=%t managedHookFirst=%t", ready, first)
		diagnosticMu.Lock()
		defer diagnosticMu.Unlock()
		outputSnapshots = append(outputSnapshots, map[string]any{"available": ready, "rules": rules})
		b, _ := json.MarshalIndent(outputSnapshots, "", "  ")
		if err = os.WriteFile(filepath.Join(dir, "output-diagnostics.json"), b, 0600); err != nil {
			t.Error("cannot save private OUTPUT diagnostics")
		}
	}
	// As the supervisor does: find the data folder's host path on its own
	// container and translate mount sources at the engine.
	self, err := raw.Inspect(ctx, cfg.SelfID)
	if err != nil {
		t.Fatal("the fixture supervisor's own container", err)
	}
	dataHost := ""
	for _, m := range self.Mounts {
		if m.Target == config.DataDir {
			dataHost = m.Source
		}
	}
	if dataHost == "" {
		t.Fatal("no bind mount at " + config.DataDir)
	}
	layout := config.NewLayout(config.DataDir).WithHost(dataHost)
	eng := engine.WithDataFolder(e, layout.Data, layout.Host())
	state := layout.State
	if err = layout.Prepare(); err != nil {
		t.Fatal(err)
	}
	// Like the supervisor, the services below own only this instance's resources.
	instance, err := config.InstanceID(layout)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := vpn.NewStore(filepath.Join(layout.Data, "networks.json"), layout.Secret)
	if err != nil {
		t.Fatal(err)
	}
	var resolverMu sync.Mutex
	assignments := map[string]int{}
	resolver := func(id, network string) (vpn.FixturePeer, error) {
		resolverMu.Lock()
		defer resolverMu.Unlock()
		n, ok := assignments[id]
		if !ok {
			n = len(assignments)
			if n >= len(cfg.Keys) {
				return vpn.FixturePeer{}, errors.New("fixture peer capacity exceeded")
			}
			assignments[id] = n
		}
		endpoint, err := e.Inspect(ctx, cfg.Endpoint)
		if err != nil {
			return vpn.FixturePeer{}, err
		}
		if endpoint.IP[network] == "" {
			if err = e.ConnectNetwork(ctx, network, endpoint.ID, nil); err != nil {
				return vpn.FixturePeer{}, err
			}
			_, _, code, routeErr := engine.Output(ctx, e, endpoint.ID, engine.ExecSpec{User: "root", Cmd: []string{"ip", "route", "replace", "default", "via", cfg.EndpointGateway, "dev", "eth0"}})
			if routeErr != nil || code != 0 {
				return vpn.FixturePeer{}, errors.New("fixture endpoint uplink route failed")
			}
			endpoint, err = e.Inspect(ctx, endpoint.ID)
			if err != nil {
				return vpn.FixturePeer{}, err
			}
		}
		addr, err := netip.ParseAddr(endpoint.IP[network])
		if err != nil {
			return vpn.FixturePeer{}, err
		}
		return vpn.FixturePeer{Endpoint: addr, PublicKey: cfg.PublicKey, PrivateKeyFile: cfg.Keys[n], Address: fmt.Sprintf("10.77.0.%d/32", n+2)}, nil
	}
	makeService := func() *environments.Service {
		// Open reads the saved definitions, so a fresh service recovers from them.
		ds, err := environments.Open(eng, environments.Config{Layout: layout, Subnet: "10.219.0.0/16", SelfID: cfg.SelfID, DesktopPort: environments.DesktopPort}, instance, log)
		if err != nil {
			t.Fatal(err)
		}
		manager := vpn.NewManager(eng, store, state)
		if !cfg.Production {
			var release func()
			manager, release = vpn.NewFixtureManager(eng, store, state, resolver)
			t.Cleanup(release)
		}
		manager.ForInstance(instance)
		if err := ds.ConfigureNetworking(store, e2eObservedRuntime{VPNRuntime: manager, t: t}, state); err != nil {
			t.Fatal(err)
		}
		return ds
	}
	ds := makeService()
	accts, err := accounts.Load(layout.Data)
	if err != nil {
		t.Fatal(err)
	}
	password, err := os.ReadFile(filepath.Join(dir, "admin-password"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = accts.EnsureAdmin("admin", strings.TrimSpace(string(password))); err != nil {
		t.Fatal(err)
	}
	ui, _ := fs.Sub(web.Dist, "dist")
	s, err := New(Options{Log: log, Eng: eng, Environments: ds, Accounts: accts, Layout: layout, UI: ui, DefaultImage: os.Getenv("CC_TEST_IMAGE")})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: s}
	defer httpServer.Close()
	go httpServer.Serve(listener)
	// Keep observer/event recovery disabled during explicit dependency loss tests;
	// public API activity and all foreground paths remain production behavior.
	background, stopBackground := context.WithCancel(ctx)
	defer stopBackground()
	s.Run(background)
	write := func(name string, v any) {
		b, _ := json.MarshalIndent(v, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	waitFile := func(name string) {
		for {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("fixture coordination deadline")
			case <-time.After(time.Second):
			}
		}
	}
	if cfg.Production {
		write("ready.json", map[string]any{"listen": listener.Addr().String(), "production": true})
		waitFile("stop")
		return
	}
	u, p := "fixture-only", "fixture-only"
	conn, err := store.Create(vpn.Input{Name: "Controlled WireGuard fixture", Provider: "nordvpn", Country: "Netherlands", Username: &u, Password: &p})
	if err != nil {
		t.Fatal(err)
	}
	var dsList []environments.Environment
	for _, suffix := range []string{"a", "b"} {
		name := "vpe-" + cfg.Scope[:6] + "-" + suffix
		d, err := ds.Create(ctx, name, environments.Settings{Image: os.Getenv("CC_TEST_IMAGE"), VPNConnectionID: conn.ID}, func(msg string, _ float64) { t.Log(msg) })
		if err != nil {
			t.Fatal("initial create", err)
		}
		dsList = append(dsList, d)
	}
	if dsList[0].Container.NetworkContainer == dsList[1].Container.NetworkContainer {
		t.Fatal("shared namespace")
	}
	write("ready.json", map[string]any{"listen": listener.Addr().String(), "names": []string{dsList[0].Name, dsList[1].Name}, "containers": []string{dsList[0].Container.ID, dsList[1].Container.ID}, "gateways": []string{dsList[0].Container.NetworkContainer, dsList[1].Container.NetworkContainer}})
	t.Log("READY: two real Selkies environments with independent controlled VPN namespaces")
	waitFile("continue")
	stopBackground()
	// Application restart/idle/start preserve the runtime; its loss requires a new
	// consumer and gateway, retaining identity, token and workspace.
	d := dsList[0]
	gateway := d.Container.NetworkContainer
	if err = ds.Stop(ctx, d.Name); err != nil {
		t.Fatal(err)
	}
	g, err := e.Inspect(ctx, gateway)
	if err != nil || !g.Running() {
		t.Fatal("idle gateway lost", err)
	}
	if err = ds.Start(ctx, d.Name); err != nil {
		t.Fatal(err)
	}
	if err = ds.Restart(ctx, d.Name); err != nil {
		t.Fatal(err)
	}
	current, err := ds.Get(ctx, d.Name)
	if err != nil || current.Container.NetworkContainer != gateway {
		t.Fatal("application restart changed namespace", err)
	}
	if err = e.Stop(ctx, gateway, time.Second); err != nil {
		t.Fatal(err)
	}
	write("gateway-stopped", map[string]bool{"stopped": true})
	waitFile("replacement")
	fresh := makeService()
	if err = fresh.Reconcile(ctx); err != nil {
		t.Errorf("initial Reconcile failed: %v", err)
		failed, getErr := fresh.Get(ctx, d.Name)
		if getErr != nil || failed.Settings.VPNConnectionID != conn.ID || failed.VPN.State != "blocked" {
			t.Fatal("lost selected persisted failure", getErr)
		}
		t.Logf("persisted failure: state=%s reason=%s", failed.VPN.State, failed.VPN.Reason)
		if err = fresh.Reconcile(ctx); err != nil {
			t.Fatal("retry from persisted failure", err)
		}
	}
	s.Environments = fresh
	ds = fresh
	current, err = ds.Get(ctx, d.Name)
	if err != nil || current.ID != d.ID || current.Container.Env[environments.TokenEnv] != d.Container.Env[environments.TokenEnv] || current.Container.NetworkContainer == gateway {
		t.Fatal("replacement identity/namespace", err)
	}
	write("recovered.json", map[string]string{"container": current.Container.ID, "gateway": current.Container.NetworkContainer})
	waitFile("finish")
	// Same-image upgrade exercises the real recreate path and retained workspace.
	if _, err = ds.Recreate(ctx, d.Name, current.Settings, func(string, float64) {}); err != nil {
		t.Fatal("upgrade", err)
	}
	for _, original := range dsList {
		if err = e.DisconnectNetwork(ctx, "deskyard-"+original.Name, cfg.Endpoint); err != nil {
			t.Fatal("detach fixture endpoint", err)
		}
		if err = ds.Remove(ctx, original.Name, false); err != nil {
			t.Fatal("remove", err)
		}
	}
	if cs, err := ds.List(ctx); err != nil || len(cs) != 0 {
		t.Fatal("environment removal inventory", err)
	}
	t.Log("PASS idle/start/restart, gateway stop, persisted supervisor recovery, same-image recreate and pair removal")
}

// Preserve a safe manager boundary if an intermittent recovery failure recurs.
type e2eObservedRuntime struct {
	environments.VPNRuntime
	t *testing.T
}

func (r e2eObservedRuntime) Ensure(ctx context.Context, name, network, connection string, exceptions []vpn.Exception) (engine.Container, error) {
	c, err := r.VPNRuntime.Ensure(ctx, name, network, connection, exceptions)
	if err != nil {
		phase := "unclassified failure (private cause withheld)"
		switch err.Error() {
		case "VPN firewall/control readiness failed", "VPN runtime creation failed", "VPN runtime start failed", "VPN connection configuration is unavailable", "VPN pinned runtime image could not be prepared":
			phase = err.Error()
		}
		if errors.Is(err, vpn.ErrConflict) {
			phase = "VPN runtime configuration conflict"
		}
		r.t.Log("runtime Ensure diagnostic:", phase)
	}
	return c, err
}

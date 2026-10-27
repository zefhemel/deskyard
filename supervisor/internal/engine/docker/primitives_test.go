package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moby/moby/client"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func testAdapter(t *testing.T, handler http.HandlerFunc) *Docker {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := client.New(client.WithHost(srv.URL), client.WithAPIVersion("1.53"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &Docker{c: c}
}
func TestCreateNamespaceAndDefaults(t *testing.T) {
	for _, shared := range []bool{true, false} {
		t.Run(map[bool]string{true: "shared", false: "ordinary"}[shared], func(t *testing.T) {
			d := testAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/containers/create") {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var body struct {
					ExposedPorts map[string]any
					HostConfig   struct {
						NetworkMode  string
						CapAdd       []string
						PortBindings map[string]any
						Privileged   bool
						Devices      []struct{ PathOnHost string }
					}
					NetworkingConfig *struct{ EndpointsConfig map[string]any }
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if shared {
					if body.HostConfig.NetworkMode != "container:gateway" || len(body.ExposedPorts) != 0 || len(body.HostConfig.PortBindings) != 0 || body.NetworkingConfig != nil {
						t.Errorf("unsafe shared namespace request: %+v", body)
					}
				} else {
					if _, ok := body.ExposedPorts["8080/tcp"]; !ok {
						t.Error("ordinary port missing")
					}
					if body.HostConfig.NetworkMode != "desktop-net" || body.NetworkingConfig == nil {
						t.Error("ordinary network missing")
					}
				}
				if body.HostConfig.Privileged {
					t.Error("privileged container")
				}
				if len(body.HostConfig.CapAdd) != 1 || body.HostConfig.CapAdd[0] != "CAP_NET_ADMIN" || len(body.HostConfig.Devices) != 1 || body.HostConfig.Devices[0].PathOnHost != "/dev/net/tun" {
					t.Errorf("explicit capabilities/devices missing: %+v", body.HostConfig)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(201)
				_, _ = w.Write([]byte(`{"Id":"created"}`))
			})
			spec := engine.ContainerSpec{Name: "desktop", Image: "test", CapAdd: []string{"NET_ADMIN"}, Devices: []string{"/dev/net/tun"}}
			if shared {
				spec.NetworkContainer = "gateway"
			} else {
				spec.Network = "desktop-net"
			}
			if id, err := d.Create(context.Background(), spec); err != nil || id != "created" {
				t.Fatalf("create: %s %v", id, err)
			}
		})
	}
}
func TestRejectIncompatibleNamespaceSpec(t *testing.T) {
	d := testAdapter(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid spec reached Docker") })
	for _, s := range []engine.ContainerSpec{{NetworkContainer: "gateway", Network: "bridge"}, {NetworkContainer: "gateway", Aliases: []string{"desktop"}}} {
		if _, err := d.Create(context.Background(), s); err == nil {
			t.Errorf("accepted incompatible spec: %+v", s)
		}
	}
}
func TestInspectHealthAndUpdateRestartPolicy(t *testing.T) {
	d := testAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/containers/gateway/json"):
			_, _ = w.Write([]byte(`{"Id":"gateway","State":{"Status":"running","Health":{"Status":"unhealthy","Log":[{"Output":"private-secret"}]}},"HostConfig":{"NetworkMode":"container:vpn","RestartPolicy":{"Name":"no"}}}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/containers/gateway/update"):
			var body struct{ RestartPolicy struct{ Name string } }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.RestartPolicy.Name != "no" {
				t.Errorf("restart policy: %+v", body)
			}
			_, _ = w.Write([]byte(`{"Warnings":[]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	c, err := d.Inspect(context.Background(), "gateway")
	if err != nil || c.Health != "unhealthy" || c.NetworkContainer != "vpn" || c.Restart != "no" {
		t.Fatalf("inspection: %+v %v", c, err)
	}
	public, _ := json.Marshal(c)
	if strings.Contains(string(public), "private-secret") {
		t.Fatal("raw health log exposed")
	}
	if err := d.UpdateRestartPolicy(context.Background(), "gateway", "no"); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultDesktopHasNoAddedCapabilities(t *testing.T) {
	d := testAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			HostConfig struct {
				CapAdd     []string
				Privileged bool
				Devices    []any
			}
			NetworkingConfig struct {
				EndpointsConfig map[string]struct{ Aliases []string }
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.HostConfig.CapAdd) != 0 || body.HostConfig.Privileged || len(body.HostConfig.Devices) != 0 {
			t.Fatal("desktop network privileges changed")
		}
		if aliases := body.NetworkingConfig.EndpointsConfig["desktop-net"].Aliases; len(aliases) != 1 || aliases[0] != "desktop" {
			t.Errorf("network aliases changed: %v", aliases)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"Id":"created"}`))
	})
	if _, err := d.Create(context.Background(), engine.ContainerSpec{Name: "desktop", Image: "test", Network: "desktop-net", Aliases: []string{"desktop"}}); err != nil {
		t.Fatal(err)
	}
}

package computers

import (
	"bytes"
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"os"
	"strings"
	"testing"
)

type dockerAccessEngine struct {
	*mountEngine
	socketAvailable bool
	probes          int
}

func (e *dockerAccessEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	// The socket is on the daemon host; the fake mount boundary cannot stat it.
	for i := range s.Mounts {
		if s.Mounts[i].Source == "/var/run/docker.sock" {
			s.Mounts[i].MustExist = false
		}
	}
	id, err := e.mountEngine.Create(ctx, s)
	if err == nil {
		for i := range s.Mounts {
			if s.Mounts[i].Source == "/var/run/docker.sock" {
				s.Mounts[i].MustExist = true
			}
		}
		e.specs[id] = s
	}
	return id, err
}
func (e *dockerAccessEngine) Exec(ctx context.Context, id string, s engine.ExecSpec) (*engine.Process, error) {
	if strings.Contains(strings.Join(s.Cmd, " "), "-S /docker.sock") {
		e.probes++
		// The folder check runs first in the combined helper.
		for _, m := range e.specs[id].Mounts {
			if m.Source == dockerSocket {
				continue
			}
			st, err := os.Stat(m.Source)
			if err != nil || !st.IsDir() {
				return &engine.Process{Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(nil), Wait: func() (int, error) { return 1, nil }, Close: func() {}}, nil
			}
		}
		code := 0
		if !e.socketAvailable {
			code = 2
		}
		return &engine.Process{Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(nil), Wait: func() (int, error) { return code, nil }, Close: func() {}}, nil
	}
	return e.mountEngine.Exec(ctx, id, s)
}
func dockerAccessService(t *testing.T) (*Service, *dockerAccessEngine) {
	s, m := mountService(t)
	e := &dockerAccessEngine{mountEngine: m, socketAvailable: true}
	s.Eng = e
	return s, e
}
func TestDockerAccessOptInAndRecreation(t *testing.T) {
	s, e := dockerAccessService(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	initial := e.specs["deskyard-alice"]
	if len(initial.Mounts) != 1 || initial.Env["DESKYARD_DOCKER_ACCESS"] != "" {
		t.Fatal("default grants access")
	}
	if err := s.checkHostSources(ctx, nil, true); err != nil {
		t.Fatal(err)
	}
	if e.probes != 1 {
		t.Fatal("missing socket preflight")
	}
	set := Settings{Image: "img", DockerAccess: true}
	if _, err := s.Recreate(ctx, "alice", set, func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	spec := e.specs[d.Container.ID]
	if !d.Settings.DockerAccess || spec.Env["DESKYARD_DOCKER_ACCESS"] != "1" {
		t.Fatalf("permission lost: %+v", d.Settings)
	}
	found := false
	for _, m := range spec.Mounts {
		if m.Target == "/var/run/docker.sock" {
			found = m.Source == "/var/run/docker.sock" && m.MustExist && !m.ReadOnly
		}
	}
	if !found {
		t.Fatalf("missing strict socket bind: %+v", spec.Mounts)
	}
	e.socketAvailable = false
	set.DockerAccess = false
	if _, err = s.Recreate(ctx, "alice", set, func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Get(ctx, "alice")
	spec = e.specs[d.Container.ID]
	if d.Settings.DockerAccess || len(spec.Mounts) != 1 || spec.Env["DESKYARD_DOCKER_ACCESS"] != "" {
		t.Fatal("revocation retained socket")
	}
}
func TestDockerAccessUnavailablePreservesComputer(t *testing.T) {
	s, e := dockerAccessService(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx, "deskyard-alice"); err != nil {
		t.Fatal(err)
	}
	e.socketAvailable = false
	_, err := s.Recreate(ctx, "alice", Settings{Image: "img", DockerAccess: true}, func(string, float64) {})
	if err == nil || !strings.Contains(err.Error(), "Unix socket") {
		t.Fatalf("wrong socket failure: %v", err)
	}
	d, _ := s.Get(ctx, "alice")
	if !d.Container.Running() || e.stops != 0 || d.Settings.DockerAccess {
		t.Fatal("failed permission preflight changed computer")
	}
}

func TestDockerAccessRollbackRetainsPermission(t *testing.T) {
	s, e := dockerAccessService(t)
	ctx := context.Background()
	if err := s.seed(ctx, "alice", Settings{Image: "img", DockerAccess: true}); err != nil {
		t.Fatal(err)
	}
	e.failImage = "broken"
	if _, err := s.Recreate(ctx, "alice", Settings{Image: "broken"}, func(string, float64) {}); err == nil {
		t.Fatal("expected replacement failure")
	}
	// The old container is back with its permission; the definition stays ahead.
	d, err := s.Get(ctx, "alice")
	if err != nil || d.Container.Env["DESKYARD_DOCKER_ACCESS"] != "1" || d.Revision <= d.AppliedRevision || d.Error == "" {
		t.Fatalf("rollback lost permission: %+v %v", d, err)
	}
}

type vpnDockerAccessEngine struct {
	*dockerAccessEngine
	network *pairEngine
}

func (e *vpnDockerAccessEngine) Networks(ctx context.Context, l map[string]string) ([]engine.Network, error) {
	return e.network.Networks(ctx, l)
}
func (e *vpnDockerAccessEngine) CreateNetwork(ctx context.Context, name, subnet string, l map[string]string) error {
	return e.network.CreateNetwork(ctx, name, subnet, l)
}
func (e *vpnDockerAccessEngine) RemoveNetwork(ctx context.Context, name string) error {
	return e.network.RemoveNetwork(ctx, name)
}
func (e *vpnDockerAccessEngine) ConnectNetwork(ctx context.Context, n, id string, a []string) error {
	return e.network.ConnectNetwork(ctx, n, id, a)
}
func (e *vpnDockerAccessEngine) DisconnectNetwork(ctx context.Context, n, id string) error {
	return e.network.DisconnectNetwork(ctx, n, id)
}

func TestVPNDockerAccessPersistenceAndPreflight(t *testing.T) {
	s, p, _, conn := pairSetup(t)
	ctx := context.Background()
	m := &mountEngine{fakeEngine: p.fakeEngine, specs: map[string]engine.ContainerSpec{}}
	e := &vpnDockerAccessEngine{dockerAccessEngine: &dockerAccessEngine{mountEngine: m, socketAvailable: true}, network: p}
	s.Eng = e
	p.fakeEngine.port = p.port
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID, DockerAccess: true}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	for _, selection := range []string{"", conn.ID} {
		set := d.Settings
		set.VPNConnectionID = selection
		d, err = s.Recreate(ctx, "alice", set, noProgress)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Settings.DockerAccess || d.Container.Env["DESKYARD_DOCKER_ACCESS"] != "1" {
			t.Fatal("VPN transition lost Docker permission")
		}
	}
	e.socketAvailable = false
	stops := e.stops
	if _, err := s.Recreate(ctx, "alice", d.Settings, noProgress); err == nil {
		t.Fatal("unavailable socket accepted")
	}
	after, err := s.Get(ctx, "alice")
	if err != nil || after.Container.ID != d.Container.ID || !after.Container.Running() || e.stops != stops {
		t.Fatalf("socket preflight disturbed VPN computer: %+v %v", after, err)
	}
}

// Combined permissions must share one helper lifecycle.
func TestHostPreflightCombinesFoldersAndSocket(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "vpn"}[managed], func(t *testing.T) {
			s, p, _, conn := pairSetup(t)
			p.fakeEngine.port = p.port
			e := &dockerAccessEngine{mountEngine: &mountEngine{fakeEngine: p.fakeEngine, specs: map[string]engine.ContainerSpec{}}, socketAvailable: true}
			s.Eng = &vpnDockerAccessEngine{dockerAccessEngine: e, network: p}
			set := Settings{Image: "img", DockerAccess: true, Volumes: []VolumeGrant{hostVolume(t, s, "data", t.TempDir(), "/mnt/data", false)}}
			if managed {
				set.VPNConnectionID = conn.ID
			}
			for _, operation := range []string{"create", "recreate"} {
				t.Run(operation, func(t *testing.T) {
					before := len(e.specs)
					var err error
					if operation == "create" {
						_, err = s.Create(context.Background(), "alice", set, noProgress)
					} else {
						_, err = s.Recreate(context.Background(), "alice", set, noProgress)
					}
					if err != nil {
						t.Fatal(err)
					}
					// Recreate replaces the computer spec under the same fake ID.
					want := 2
					if operation == "recreate" {
						want = 1
					}
					if got := len(e.specs) - before; got != want {
						t.Fatalf("created %d new container specs, want %d (one shared preflight)", got, want)
					}
					for id, spec := range e.specs {
						if spec.Image != helperImage {
							continue
						}
						if len(spec.Mounts) != 2 {
							t.Fatalf("probe mounts: %+v", spec.Mounts)
						}
						for _, m := range spec.Mounts {
							if !m.ReadOnly || !m.MustExist {
								t.Fatalf("unsafe probe bind: %+v", m)
							}
						}
						if _, exists := e.cs[id]; exists {
							t.Fatalf("probe leaked: %s", id)
						}
					}
				})
			}
		})
	}
}

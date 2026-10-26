package environments

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"path/filepath"
	"reflect"
	"testing"
)

type vpnMountEngine struct {
	*mountEngine
	network *pairEngine
}

func (e *vpnMountEngine) Networks(ctx context.Context, labels map[string]string) ([]engine.Network, error) {
	return e.network.Networks(ctx, labels)
}
func (e *vpnMountEngine) CreateNetwork(ctx context.Context, name, subnet string, labels map[string]string) error {
	return e.network.CreateNetwork(ctx, name, subnet, labels)
}
func (e *vpnMountEngine) RemoveNetwork(ctx context.Context, name string) error {
	return e.network.RemoveNetwork(ctx, name)
}
func (e *vpnMountEngine) ConnectNetwork(ctx context.Context, name, id string, aliases []string) error {
	return e.network.ConnectNetwork(ctx, name, id, aliases)
}
func (e *vpnMountEngine) DisconnectNetwork(ctx context.Context, name, id string) error {
	return e.network.DisconnectNetwork(ctx, name, id)
}

func TestVPNHostMountPreflightPreservesRunningEnvironment(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(ctx, "alice")
	mounts := &vpnMountEngine{mountEngine: &mountEngine{fakeEngine: e.fakeEngine, specs: map[string]engine.ContainerSpec{}}, network: e}
	s.Eng = mounts
	e.fakeEngine.port = e.port
	set := before.Settings
	set.Mounts = []HostMount{{Source: filepath.Join(t.TempDir(), "missing"), Target: "/mnt/photos", ReadOnly: true}}
	if _, err := s.Recreate(ctx, "alice", set, noProgress); err == nil {
		t.Fatal("missing host source accepted")
	}
	after, err := s.Get(ctx, "alice")
	if err != nil || !after.Container.Running() || mounts.stops != 0 || len(after.Settings.Mounts) != 0 {
		t.Fatalf("mount preflight disturbed existing VPN environment: %+v err=%v stops=%d", after, err, mounts.stops)
	}
}

func TestVPNRecoverySettingsOwnHostMounts(t *testing.T) {
	original := Settings{Mounts: []HostMount{{Source: "/srv/photos", Target: "/mnt/photos", ReadOnly: true}}}
	cloned := cloneSettings(original)
	cloned.Mounts[0].Source = "/srv/other"
	if original.Mounts[0].Source != "/srv/photos" {
		t.Fatal("recovery snapshot shares mutable host mount settings")
	}
}

func TestVPNHostMountsSurviveDisableAndRecreate(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	mounts := &vpnMountEngine{mountEngine: &mountEngine{fakeEngine: e.fakeEngine, specs: map[string]engine.ContainerSpec{}}, network: e}
	s.Eng = mounts
	e.fakeEngine.port = e.port
	ctx := context.Background()
	want := []HostMount{{Source: t.TempDir(), Target: "/mnt/photos", ReadOnly: true}}
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID, Mounts: want}, noProgress)
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
		spec := mounts.specs["deskyard-alice"]
		if !reflect.DeepEqual(d.Settings.Mounts, want) || len(spec.Mounts) != 2 || !spec.Mounts[1].MustExist || !spec.NoNewPrivileges {
			t.Fatalf("lost host folder safety: settings=%+v spec=%+v", d.Settings, spec)
		}
	}
}

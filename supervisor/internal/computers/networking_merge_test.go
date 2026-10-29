package computers

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"os"
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

func TestVPNHostVolumePreflightPreservesRunningComputer(t *testing.T) {
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
	set.Volumes = []VolumeGrant{hostVolume(t, s, "photos", filepath.Join(t.TempDir(), "missing"), "/mnt/photos", true)}
	if _, err := s.Recreate(ctx, "alice", set, noProgress); err == nil {
		t.Fatal("missing host source accepted")
	}
	after, err := s.Get(ctx, "alice")
	if err != nil || !after.Container.Running() || mounts.stops != 0 || len(after.Settings.Volumes) != 0 {
		t.Fatalf("mount preflight disturbed existing VPN computer: %+v err=%v stops=%d", after, err, mounts.stops)
	}
}

func TestVPNRecoverySettingsOwnGrants(t *testing.T) {
	original := Settings{Volumes: []VolumeGrant{{Name: "photos", Path: "/mnt/photos", ReadOnly: true}}}
	cloned := cloneSettings(original)
	cloned.Volumes[0].Path = "/mnt/other"
	if original.Volumes[0].Path != "/mnt/photos" {
		t.Fatal("recovery snapshot shares mutable host mount settings")
	}
}

func TestVPNHostVolumesSurviveDisableAndRecreate(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	mounts := &vpnMountEngine{mountEngine: &mountEngine{fakeEngine: e.fakeEngine, specs: map[string]engine.ContainerSpec{}}, network: e}
	s.Eng = mounts
	e.fakeEngine.port = e.port
	ctx := context.Background()
	want := []VolumeGrant{hostVolume(t, s, "photos", t.TempDir(), "/mnt/photos", true)}
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID, Volumes: want}, noProgress)
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
		if !reflect.DeepEqual(d.Settings.Volumes, want) || len(spec.Mounts) != 2 || !spec.Mounts[1].MustExist || !spec.NoNewPrivileges {
			t.Fatalf("lost host folder safety: settings=%+v spec=%+v", d.Settings, spec)
		}
	}
}

// A VPN computer's settings are saved through withSettings: its grants
// must survive every change, on or off the VPN.
func TestVPNSettingsKeepVolumes(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	mounts := &vpnMountEngine{mountEngine: &mountEngine{fakeEngine: e.fakeEngine, specs: map[string]engine.ContainerSpec{}}, network: e}
	s.Eng = mounts
	e.fakeEngine.port = e.port
	ctx := context.Background()
	addVolume(t, s, "photos")
	want := []VolumeGrant{{Name: "photos", Path: "/share/photos", ReadOnly: true}}
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID, Volumes: want}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Settings){
		func(set *Settings) { set.MemoryBytes = 1 << 30 },
		func(set *Settings) { set.VPNConnectionID = "" },
		func(set *Settings) { set.VPNConnectionID = conn.ID },
	} {
		set := d.Settings
		change(&set)
		d, err = s.Recreate(ctx, "alice", set, noProgress)
		if err != nil {
			t.Fatal(err)
		}
		spec := mounts.specs["deskyard-alice"]
		m, ok := volumeMount(spec, "/share/photos")
		if !reflect.DeepEqual(d.Settings.Volumes, want) || !ok || !m.ReadOnly || !m.MustExist || !spec.NoNewPrivileges {
			t.Fatalf("lost the grant: settings=%+v spec=%+v", d.Settings, spec)
		}
	}
}

// Taking back a grant whose folder is missing, through a VPN settings change,
// clears its error at once.
func TestVPNDroppingGrantClearsFolderError(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	mounts := &vpnMountEngine{mountEngine: &mountEngine{fakeEngine: e.fakeEngine, specs: map[string]engine.ContainerSpec{}}, network: e}
	s.Eng = mounts
	e.fakeEngine.port = e.port
	ctx := context.Background()
	addVolume(t, s, "photos")
	if _, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID, Volumes: []VolumeGrant{{Name: "photos"}}}, noProgress); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.Layout.VolumeOf("photos")); err != nil {
		t.Fatal(err)
	}
	if err := s.checkFolders("alice"); err == nil {
		t.Fatal("no folder error")
	}
	d, err := s.Recreate(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if d.Error != "" {
		t.Fatalf("error kept: %q", d.Error)
	}
}

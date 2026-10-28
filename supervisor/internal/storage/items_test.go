package storage

import (
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

// Snapshots lay items out below one root per item.
func TestItemLayout(t *testing.T) {
	for it, want := range map[Item][3]string{
		{KindEnvironment, "alice"}: {"/deskyard/environment/alice", "/deskyard/environment/alice/home", "alice"},
		{KindVolume, "photos"}:     {"/deskyard/volume/photos", "/deskyard/volume/photos", ""},
	} {
		if got := [3]string{it.Root(), it.files(), it.Environment()}; got != want {
			t.Errorf("%s: %q, want %q", it, got, want)
		}
	}
	if _, err := ParseItem("network", "alice"); err == nil {
		t.Error("parsed an unknown kind")
	}
}

// Items of each kind are found where the environments service keeps them.
func TestItemKinds(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	env.addEnvironment(t, "alice", false, environments.Settings{Image: "img"})
	env.addVolume(t, "photos")
	env.addHostVolume(t, "media", t.TempDir())
	for it, want := range map[Item]string{
		{KindEnvironment, "alice"}: env.layout.HomeOf("alice"),
		{KindVolume, "photos"}:     env.layout.VolumeOf("photos"),
		{KindVolume, "media"}:      env.vols["media"].HostPath,
	} {
		if !s.exists(it) {
			t.Errorf("%s doesn't exist", it)
		}
		folder, err := s.itemFolder(it)
		if err != nil || folder.Path != want || folder.OnEngineHost != (it.Name == "media") {
			t.Errorf("%s: %+v %v", it, folder, err)
		}
		_, mounts, cleanup, err := s.itemSource(it)
		if err != nil || mounts[0].Source != want || !mounts[0].ReadOnly {
			t.Errorf("%s sources: %+v %v", it, mounts, err)
		}
		cleanup()
	}
	for _, it := range []Item{{KindEnvironment, "bob"}, {KindVolume, "bob"}} {
		if s.exists(it) {
			t.Errorf("%s exists", it)
		}
		if _, err := s.itemFolder(it); err == nil {
			t.Errorf("%s has a folder", it)
		}
	}
}

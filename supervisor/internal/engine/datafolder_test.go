package engine

import (
	"context"
	"testing"
)

// recordingEngine records the spec it is asked to create and returns canned containers.
type recordingEngine struct {
	Engine
	created    ContainerSpec
	containers []Container
}

func (r *recordingEngine) Create(_ context.Context, spec ContainerSpec) (string, error) {
	r.created = spec
	return "id", nil
}

func (r *recordingEngine) Inspect(context.Context, string) (Container, error) {
	return r.containers[0], nil
}

func (r *recordingEngine) List(context.Context, map[string]string) ([]Container, error) {
	return r.containers, nil
}

func TestWithDataFolderTranslatesCreate(t *testing.T) {
	inner := &recordingEngine{}
	e := WithDataFolder(inner, "/data", "/srv/d")
	_, err := e.Create(context.Background(), ContainerSpec{Mounts: []Mount{
		{Source: "/data/home/a", Target: "/home/a"},
		{Source: "/data/photos", Target: "/photos"},
		{Source: "/data", Target: "/all"},
		{Source: "/data/home/a", OnEngineHost: true, Target: "/host"},
		{Source: "/var/run/docker.sock", OnEngineHost: true, Target: "/var/run/docker.sock"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"/srv/d/home/a", "/srv/d/photos", "/srv/d", "/data/home/a", "/var/run/docker.sock"} {
		if m := inner.created.Mounts[i]; m.Source != want || !m.OnEngineHost {
			t.Errorf("mount %d = %+v, want engine-host source %s", i, m, want)
		}
	}
}

func TestWithDataFolderRefusesSourcesOutsideIt(t *testing.T) {
	inner := &recordingEngine{}
	e := WithDataFolder(inner, "/data", "/srv/d")
	for _, src := range []string{"/srv/photos", "/data/home/../../etc", "/database", "home/a"} {
		if _, err := e.Create(context.Background(), ContainerSpec{Mounts: []Mount{{Source: src, Target: "/x"}}}); err == nil {
			t.Errorf("data folder source %q accepted", src)
		}
	}
	if inner.created.Mounts != nil {
		t.Errorf("inner engine reached: %+v", inner.created)
	}
}

func TestWithDataFolderTranslatesInspectAndList(t *testing.T) {
	inner := &recordingEngine{containers: []Container{{Mounts: []Mount{
		{Source: "/srv/d/volume/v", OnEngineHost: true, Target: "/v"},
		{Source: "/srv/d/old", OnEngineHost: true, Target: "/old"},
		{Source: "/srv/photos", OnEngineHost: true, Target: "/photos"},
		{Source: "/srv/dd/x", OnEngineHost: true, Target: "/dd"},
		{Source: "/srv/d/docker/volumes/v/_data", OnEngineHost: true, Volume: "v", Target: "/named"},
	}}}}
	e := WithDataFolder(inner, "/data", "/srv/d")
	c, err := e.Inspect(context.Background(), "id")
	if err != nil {
		t.Fatal(err)
	}
	list, err := e.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Mount{
		{Source: "/data/volume/v", Target: "/v"},
		{Source: "/data/old", Target: "/old"},
		{Source: "/srv/photos", OnEngineHost: true, Target: "/photos"},
		{Source: "/srv/dd/x", OnEngineHost: true, Target: "/dd"},
		{Source: "/srv/d/docker/volumes/v/_data", OnEngineHost: true, Volume: "v", Target: "/named"},
	}
	for name, mounts := range map[string][]Mount{"inspect": c.Mounts, "list": list[0].Mounts} {
		for i, m := range mounts {
			if m != want[i] {
				t.Errorf("%s mount %d = %+v, want %+v", name, i, m, want[i])
			}
		}
	}
	if inner.containers[0].Mounts[0].Source != "/srv/d/volume/v" {
		t.Errorf("inner engine's containers were mutated: %+v", inner.containers[0].Mounts)
	}
}

func TestWithDataFolderCopiesSpec(t *testing.T) {
	inner := &recordingEngine{}
	e := WithDataFolder(inner, "/data", "/srv/d")
	spec := ContainerSpec{Mounts: []Mount{{Source: "/data/home/a", Target: "/home/a"}}}
	if _, err := e.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if spec.Mounts[0].Source != "/data/home/a" || spec.Mounts[0].OnEngineHost {
		t.Errorf("caller's mounts mutated: %+v", spec.Mounts[0])
	}
}

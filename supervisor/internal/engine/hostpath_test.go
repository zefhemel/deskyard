package engine

import (
	"context"
	"strings"
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

func testToHost(p string) string {
	if p == "/data" || strings.HasPrefix(p, "/data/home/") || strings.HasPrefix(p, "/data/volume/") {
		return "/srv/d" + strings.TrimPrefix(p, "/data")
	}
	return p
}

func testFromHost(p string) string {
	if p == "/srv/d" || strings.HasPrefix(p, "/srv/d/") {
		return "/data" + strings.TrimPrefix(p, "/srv/d")
	}
	return p
}

func TestWithHostPathsTranslatesCreate(t *testing.T) {
	inner := &recordingEngine{}
	e := WithHostPaths(inner, testToHost, testFromHost)
	_, err := e.Create(context.Background(), ContainerSpec{Mounts: []Mount{
		{Source: "/data/home/a", Target: "/home/a"},
		{Source: "/data/photos", Target: "/photos"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := inner.created.Mounts[0].Source; got != "/srv/d/home/a" {
		t.Errorf("home source = %q", got)
	}
	if got := inner.created.Mounts[1].Source; got != "/data/photos" {
		t.Errorf("host volume source = %q, want unchanged", got)
	}
}

func TestWithHostPathsTranslatesInspectAndList(t *testing.T) {
	inner := &recordingEngine{containers: []Container{{Mounts: []Mount{
		{Source: "/srv/d/volume/v", Target: "/v"},
		{Source: "/srv/photos", Target: "/photos"},
	}}}}
	e := WithHostPaths(inner, testToHost, testFromHost)
	c, err := e.Inspect(context.Background(), "id")
	if err != nil {
		t.Fatal(err)
	}
	list, err := e.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, mounts := range map[string][]Mount{"inspect": c.Mounts, "list": list[0].Mounts} {
		if mounts[0].Source != "/data/volume/v" || mounts[1].Source != "/srv/photos" {
			t.Errorf("%s mounts = %+v", name, mounts)
		}
	}
	if inner.containers[0].Mounts[0].Source != "/srv/d/volume/v" {
		t.Errorf("inner engine's containers were mutated: %+v", inner.containers[0].Mounts)
	}
}

func TestWithHostPathsCopiesSpec(t *testing.T) {
	inner := &recordingEngine{}
	e := WithHostPaths(inner, testToHost, testFromHost)
	spec := ContainerSpec{Mounts: []Mount{{Source: "/data/home/a", Target: "/home/a"}}}
	if _, err := e.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if spec.Mounts[0].Source != "/data/home/a" {
		t.Errorf("caller's mounts mutated: %q", spec.Mounts[0].Source)
	}
}

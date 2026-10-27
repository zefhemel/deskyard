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

// optionalEngine also implements the optional engine interfaces, as the Docker engine does.
type optionalEngine struct {
	recordingEngine
	reclaimed, removed string
}

func (o *optionalEngine) ProbeMemoryReclaim(context.Context) error { return nil }

func (o *optionalEngine) ReclaimMemory(_ context.Context, id string) (MemoryReclaim, error) {
	o.reclaimed = id
	return MemoryReclaim{AfterBytes: 1}, nil
}

func (o *optionalEngine) DanglingImages(context.Context, map[string]string) ([]Image, error) {
	return []Image{{ID: "img"}}, nil
}

func (o *optionalEngine) RemoveImage(_ context.Context, id string) error {
	o.removed = id
	return nil
}

func TestWithHostPathsKeepsOptionalInterfaces(t *testing.T) {
	inner := &optionalEngine{}
	e := WithHostPaths(inner, testToHost, testFromHost)
	reclaimer, ok := e.(MemoryReclaimer)
	if !ok {
		t.Fatal("wrapped engine lost MemoryReclaimer")
	}
	if got, err := reclaimer.ReclaimMemory(context.Background(), "c1"); err != nil || got.AfterBytes != 1 || inner.reclaimed != "c1" {
		t.Errorf("ReclaimMemory = %+v, %v; inner saw %q", got, err, inner.reclaimed)
	}
	pruner, ok := e.(ImagePruner)
	if !ok {
		t.Fatal("wrapped engine lost ImagePruner")
	}
	if err := pruner.RemoveImage(context.Background(), "img"); err != nil || inner.removed != "img" {
		t.Errorf("RemoveImage = %v; inner saw %q", err, inner.removed)
	}
	// Mount translation still applies on the combined wrapper.
	if _, err := e.Create(context.Background(), ContainerSpec{Mounts: []Mount{{Source: "/data/home/a"}}}); err != nil {
		t.Fatal(err)
	}
	if got := inner.created.Mounts[0].Source; got != "/srv/d/home/a" {
		t.Errorf("home source = %q", got)
	}
}

// reclaimOnlyEngine implements only MemoryReclaimer.
type reclaimOnlyEngine struct {
	recordingEngine
}

func (reclaimOnlyEngine) ProbeMemoryReclaim(context.Context) error { return nil }
func (reclaimOnlyEngine) ReclaimMemory(context.Context, string) (MemoryReclaim, error) {
	return MemoryReclaim{}, nil
}

func TestWithHostPathsAddsNoOptionalInterfaces(t *testing.T) {
	plain := WithHostPaths(&recordingEngine{}, testToHost, testFromHost)
	if _, ok := plain.(MemoryReclaimer); ok {
		t.Error("plain engine gained MemoryReclaimer")
	}
	if _, ok := plain.(ImagePruner); ok {
		t.Error("plain engine gained ImagePruner")
	}
	partial := WithHostPaths(&reclaimOnlyEngine{}, testToHost, testFromHost)
	if _, ok := partial.(MemoryReclaimer); !ok {
		t.Error("reclaim-only engine lost MemoryReclaimer")
	}
	if _, ok := partial.(ImagePruner); ok {
		t.Error("reclaim-only engine gained ImagePruner")
	}
}

package server

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// pruneEngine adds image pruning to the catalog engine. Like Docker, it
// refuses to remove an image a container uses.
type pruneEngine struct {
	*catalogEngine
	dangling []engine.Image
	removed  []string
}

func (e *pruneEngine) DanglingImages(_ context.Context, labels map[string]string) ([]engine.Image, error) {
	var out []engine.Image
	for _, img := range e.dangling {
		if img.Labels["deskyard.kind"] == labels["deskyard.kind"] {
			out = append(out, img)
		}
	}
	return out, nil
}

func (e *pruneEngine) RemoveImage(_ context.Context, id string) error {
	for _, c := range e.desktops {
		if c.ImageID == id {
			return errors.New("conflict: image is being used by a container")
		}
	}
	e.removed = append(e.removed, id)
	return nil
}

func oldImage(id, kind string) engine.Image {
	return engine.Image{ID: id, Labels: map[string]string{"deskyard.kind": kind}}
}

// Only versions this supervisor's catalog replaced are removed, and of them
// only those nothing uses: another supervisor on the same engine may pin an
// untagged version this one never had.
func TestCheckRemovesOnlyUnusedImagesThisCatalogReplaced(t *testing.T) {
	bt := newCatalogTest(t)
	e := &pruneEngine{catalogEngine: bt.e, dangling: []engine.Image{
		oldImage("sha256:unused", "desktop"),
		oldImage("sha256:stopped-container", "desktop"),
		oldImage("sha256:pinned", "desktop"),
		oldImage("sha256:foreign", "desktop"),
		oldImage("sha256:custom", "custom"),
	}}
	bt.s.Eng = e
	bt.s.Computers.Eng = e
	// The catalog's newest version of img moved away from these.
	for _, id := range []string{"sha256:unused", "sha256:stopped-container", "sha256:pinned", "sha256:custom", "sha256:base1"} {
		if err := bt.s.images.noteLocal("img", id); err != nil {
			t.Fatal(err)
		}
	}
	// A stopped container of another supervisor still uses one version...
	bt.e.desktops = append(bt.e.desktops, engine.Container{ID: "c1", Name: "deskyard-other", ImageID: "sha256:stopped-container", State: "exited"})
	// ...and a stopped computer without a container pins another.
	def := computers.Definition{Name: "alice", Created: time.Now().UTC(), Revision: 1,
		Image:   computers.ImageRef{Ref: "img", ID: "sha256:pinned"},
		Network: computers.Network{Kind: computers.NetworkIsolated}, Power: computers.PowerStopped}
	if err := bt.s.Computers.Defs.Put(def); err != nil {
		t.Fatal(err)
	}
	if j := bt.wait(bt.start("POST", "/api/images/check", nil)); j.Status != "done" {
		t.Fatalf("check: %+v", j)
	}
	if !slices.Equal(e.removed, []string{"sha256:unused"}) {
		t.Fatalf("removed %v, want only the unused version this catalog replaced", e.removed)
	}
	// A removed version is forgotten, as is one gone from the engine
	// (custom, which the fake engine doesn't hold); those in use are kept.
	entry, _ := bt.s.images.entry("img")
	if want := []string{"sha256:stopped-container", "sha256:pinned"}; !slices.Equal(entry.Previous, want) {
		t.Fatalf("previous %v, want %v", entry.Previous, want)
	}
}

// Every change of a reference's newest version records the one it replaced,
// once; a version that becomes the newest again is no longer previous. The
// record survives a restart.
func TestCatalogRecordsReplacedVersions(t *testing.T) {
	dir := t.TempDir()
	c, err := loadCatalog(dir, "img")
	if err != nil {
		t.Fatal(err)
	}
	steps := []func() error{
		func() error { return c.noteLocal("img", "sha256:a") },
		func() error { return c.setNewest("img", "sha256:b") },
		func() error { return c.noteLocal("img", "sha256:b") },
		func() error { return c.put(catalogEntry{Ref: "img", Newest: "sha256:c"}) },
		func() error { return c.setNewest("img", "sha256:a") },
		func() error { return c.noteLocal("img", "sha256:b") },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	c, err = loadCatalog(dir, "img")
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := c.entry("img")
	if want := []string{"sha256:c", "sha256:a"}; entry.Newest != "sha256:b" || !slices.Equal(entry.Previous, want) {
		t.Fatalf("newest %s previous %v, want sha256:b and %v", entry.Newest, entry.Previous, want)
	}
}

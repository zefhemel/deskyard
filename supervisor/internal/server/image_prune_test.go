package server

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
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

func TestCheckRemovesOnlyUnusedOldDesktopImages(t *testing.T) {
	bt := newCatalogTest(t)
	e := &pruneEngine{catalogEngine: bt.e, dangling: []engine.Image{
		oldImage("sha256:unused", "desktop"),
		oldImage("sha256:stopped-container", "desktop"),
		oldImage("sha256:pinned", "desktop"),
		oldImage("sha256:custom", "custom"),
	}}
	bt.s.Eng = e
	bt.s.Environments.Eng = e
	// A stopped container of another supervisor still uses one version...
	bt.e.desktops = append(bt.e.desktops, engine.Container{ID: "c1", Name: "deskyard-other", ImageID: "sha256:stopped-container", State: "exited"})
	// ...and a stopped environment without a container pins another.
	def := environments.Definition{Name: "alice", Created: time.Now().UTC(), Revision: 1,
		Image:   environments.ImageRef{Ref: "img", ID: "sha256:pinned"},
		Network: environments.Network{Kind: environments.NetworkIsolated}, Power: environments.PowerStopped}
	if err := bt.s.Environments.Defs.Put(def); err != nil {
		t.Fatal(err)
	}
	if j := bt.wait(bt.start("POST", "/api/images/check", nil)); j.Status != "done" {
		t.Fatalf("check: %+v", j)
	}
	if !slices.Equal(e.removed, []string{"sha256:unused"}) {
		t.Fatalf("removed %v, want only the unused desktop version", e.removed)
	}
}

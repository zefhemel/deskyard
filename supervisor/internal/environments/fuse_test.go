package environments

import (
	"context"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type fuseEngine struct {
	*fakeEngine
	enabled bool
	spec    engine.ContainerSpec
}

func (e *fuseEngine) ImageInspect(_ context.Context, ref string) (engine.Image, error) {
	labels := map[string]string{}
	if e.enabled {
		labels["deskyard.fuse"] = "userns-v1"
	}
	return engine.Image{ID: ref, Labels: labels}, nil
}
func (e *fuseEngine) Create(ctx context.Context, spec engine.ContainerSpec) (string, error) {
	e.spec = spec
	return e.fakeEngine.Create(ctx, spec)
}
func TestDesktopFUSECapability(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "userns"}[enabled], func(t *testing.T) {
			s, base := newFakeService(t)
			e := &fuseEngine{fakeEngine: base, enabled: enabled}
			s.Eng = e
			if err := s.createContainer(context.Background(), "alice", 1, Settings{}, "image", ""); err != nil {
				t.Fatal(err)
			}
			if enabled {
				if len(e.spec.Devices) != 1 || e.spec.Devices[0] != "/dev/fuse" {
					t.Fatalf("devices: %v", e.spec.Devices)
				}
			} else if len(e.spec.Devices) != 0 {
				t.Fatalf("legacy devices: %v", e.spec.Devices)
			}
			if len(e.spec.CapAdd) != 0 {
				t.Fatalf("added capabilities: %v", e.spec.CapAdd)
			}
		})
	}
}

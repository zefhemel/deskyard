package environments

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/desktopcli"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type cliEngine struct {
	*fakeEngine
	arch, capability string
	spec             engine.ContainerSpec
}

func (e *cliEngine) ImageInspect(_ context.Context, ref string) (engine.Image, error) {
	return engine.Image{ID: ref, Arch: e.arch, Labels: map[string]string{"deskyard.cli": e.capability}}, nil
}

func (e *cliEngine) Create(ctx context.Context, spec engine.ContainerSpec) (string, error) {
	e.spec = spec
	return e.fakeEngine.Create(ctx, spec)
}

func TestDesktopCLIArchitectureAndHostMount(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			s, base := newFakeService(t)
			e := &cliEngine{fakeEngine: base, arch: arch, capability: "runtime-v1"}
			layout := s.Layout.WithHost("/srv/deskyard")
			s.Eng = engine.WithHostPaths(e, layout.ToHost, layout.FromHost)
			dir := filepath.Join(s.Layout.State, "cli", "linux-"+arch)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "deskyard"), []byte("published CLI"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := s.createContainer(context.Background(), "alice", 1, Settings{}, "image", ""); err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, m := range e.spec.Mounts {
				if m.Target != "/opt/deskyard/cli" {
					continue
				}
				found = true
				if m.Source != "/srv/deskyard/state/cli/linux-"+arch || !m.ReadOnly || !m.MustExist {
					t.Fatalf("unsafe or wrong-architecture mount: %+v", m)
				}
			}
			if !found {
				t.Fatal("runtime CLI not mounted")
			}
		})
	}
}

func TestDesktopCLIRefusesUnusableRuntime(t *testing.T) {
	for _, tc := range []struct{ name, arch, capability string }{
		{"missing binary", "amd64", "runtime-v1"},
		{"unsupported architecture", "riscv64", "runtime-v1"},
		{"unknown architecture", "", "runtime-v1"},
		{"unknown capability", "amd64", "runtime-v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, base := newFakeService(t)
			e := &cliEngine{fakeEngine: base, arch: tc.arch, capability: tc.capability}
			s.Eng = e
			if err := s.createContainer(context.Background(), "alice", 1, Settings{}, "image", ""); err == nil {
				t.Fatal("unusable CLI accepted")
			}
			if e.spec.Name != "" {
				t.Fatal("created container despite unusable CLI")
			}
		})
	}
}

func TestLegacyDesktopKeepsBakedCLI(t *testing.T) {
	s, base := newFakeService(t)
	e := &cliEngine{fakeEngine: base, arch: "amd64"}
	s.Eng = e
	if err := s.createContainer(context.Background(), "alice", 1, Settings{}, "image", ""); err != nil {
		t.Fatal(err)
	}
	for _, m := range e.spec.Mounts {
		if m.Target == "/opt/deskyard/cli" {
			t.Fatal("legacy image received a runtime mount")
		}
	}
}

func publishTestCLI(t *testing.T, s *Service) {
	t.Helper()
	if err := desktopcli.Publish(filepath.Join("..", "..", "bin", "desktop-cli"), s.Layout.State); err != nil {
		t.Fatalf("publish test CLI (run make cli-linux first): %v", err)
	}
}

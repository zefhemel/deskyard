package docker

import (
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/system"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestMemoryReclaimPlatformRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		change       func(*system.Info)
	}{
		{"supported", "", func(*system.Info) {}},
		{"non-Linux", "Linux", func(i *system.Info) { i.OSType = "windows" }},
		{"rootless", "rootless", func(i *system.Info) { i.SecurityOptions = []string{"name=rootless"} }},
		{"cgroup-v1", "cgroup v2", func(i *system.Info) { i.CgroupVersion = "1" }},
		{"no-memory-controller", "memory controller", func(i *system.Info) { i.MemoryLimit = false }},
		{"no-swap-accounting", "swap accounting", func(i *system.Info) { i.SwapLimit = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := system.Info{OSType: "linux", CgroupVersion: "2", MemoryLimit: true, SwapLimit: true}
			tc.change(&info)
			err := checkReclaimPlatform(info)
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, engine.ErrReclaimUnsupported) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("expected %s diagnostic, got %v", tc.reason, err)
			}
		})
	}
}

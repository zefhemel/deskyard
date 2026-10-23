package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func checkReclaimPlatform(info system.Info) error {
	unsupported := func(reason string) error {
		return fmt.Errorf("%w: %s", engine.ErrReclaimUnsupported, reason)
	}
	if info.OSType != "linux" {
		return unsupported("the engine does not run Linux containers")
	}
	for _, opt := range info.SecurityOptions {
		if strings.Contains(opt, "rootless") {
			return unsupported("rootless Docker is not supported")
		}
	}
	if info.CgroupVersion != "2" {
		return unsupported("cgroup v2 is unavailable")
	}
	if !info.MemoryLimit {
		return unsupported("the memory controller is unavailable")
	}
	if !info.SwapLimit {
		return unsupported("swap accounting and limits are unavailable")
	}
	return nil
}

// memoryHelperImage checks the engine and resolves a fixed helper image before
// any host mounts or cgroup writes are attempted. Runtime calls recheck these
// prerequisites in case host configuration changed after the startup probe.
func (d *Docker) memoryHelperImage(ctx context.Context) (string, error) {
	info, err := d.c.Info(ctx, client.InfoOptions{})
	if err != nil {
		return "", fmt.Errorf("reading engine capabilities: %w", err)
	}
	if err = checkReclaimPlatform(info.Info); err != nil {
		return "", err
	}
	image, err := d.ImageInspect(ctx, reclaimImage)
	if errors.Is(err, engine.ErrNotFound) {
		if err = d.Pull(ctx, reclaimImage, nil); err != nil {
			return "", fmt.Errorf("preparing memory helper image: %w", err)
		}
		image, err = d.ImageInspect(ctx, reclaimImage)
	}
	if err != nil {
		return "", fmt.Errorf("preparing memory helper image: %w", err)
	}
	return image.ID, nil
}

// ProbeMemoryReclaim exercises the real request on a tiny disposable target.
// A partial/no-savings result is fine: this checks support, not capacity.
func (d *Docker) ProbeMemoryReclaim(ctx context.Context) (retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	image, err := d.memoryHelperImage(ctx)
	if err != nil {
		return err
	}
	id, err := d.createMemoryHelper(ctx, image, nil, "sleep 60")
	if err != nil {
		return fmt.Errorf("creating memory probe target: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, d.removeMemoryHelper(ctx, id)) }()
	if err = d.Start(ctx, id); err != nil {
		return fmt.Errorf("starting memory probe target: %w", err)
	}
	if _, err = d.ReclaimMemory(ctx, id); err != nil {
		return fmt.Errorf("probing memory reclamation: %w", err)
	}
	return nil
}

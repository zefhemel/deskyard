//go:build engine

package docker

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestProbeMemoryReclaim(t *testing.T) {
	d, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	probeErr := d.ProbeMemoryReclaim(ctx)
	if probeErr != nil && !errors.Is(probeErr, engine.ErrReclaimUnsupported) {
		t.Fatal(probeErr)
	}
	left, err := d.List(ctx, map[string]string{"deskyard.helper": "memory-reclaim"})
	if err != nil || len(left) != 0 {
		t.Fatalf("probe left helpers: %v %v", left, err)
	}
	if probeErr != nil {
		t.Skip(probeErr)
	}
}

// Regression: reclaim must target only this cgroup, actually swap cold pages,
// preserve processes and remove its host-access helpers.
func TestReclaimMemory(t *testing.T) {
	d, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// Container creation requires a local image; CI starts with an empty cache.
	if err := d.Pull(ctx, "python:3.13-alpine", nil); err != nil {
		t.Fatal(err)
	}
	id, err := d.Create(ctx, engine.ContainerSpec{
		Name: "deskyard-reclaim-engine-test-" + strings.ToLower(rand.Text()[:10]), Image: "python:3.13-alpine", Network: "none",
		Entrypoint: []string{"python", "-c"}, Cmd: []string{
			"import time; b=bytearray(b'x'*192*1024*1024); print('memory ready',flush=True); time.sleep(80)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Remove(context.Background(), id)
	if err := d.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	result, err := d.ReclaimMemory(ctx, id)
	if errors.Is(err, engine.ErrReclaimUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if result.AfterBytes >= result.BeforeBytes || result.SwapAfterBytes <= result.SwapBeforeBytes {
		t.Fatalf("did not reclaim and swap cold memory: %+v", result)
	}
	c, err := d.Inspect(ctx, id)
	if err != nil || !c.Running() {
		t.Fatalf("target stopped: %+v %v", c, err)
	}
	// Less than a full request remains resident. EAGAIN must still return the
	// measurements rather than treating a partial reclaim as a helper failure.
	partial, err := d.ReclaimMemory(ctx, id)
	if err != nil || !partial.Partial {
		t.Fatalf("partial reclaim: %+v %v", partial, err)
	}
	list, err := d.List(ctx, map[string]string{"deskyard.helper": "memory-reclaim"})
	if err != nil || len(list) != 0 {
		t.Fatalf("left helpers: %v %v", list, err)
	}
	if _, err := d.ReclaimMemory(ctx, "missing-reclaim-container"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing target: %v", err)
	}
	t.Logf("reclaimed: %+v", result)
}

func TestReclaimHelperCancellationRemovesContainer(t *testing.T) {
	d, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = d.memoryHelper(ctx, reclaimImage, nil, "sleep 60")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	list, err := d.List(context.Background(), map[string]string{"deskyard.helper": "memory-reclaim"})
	if err != nil || len(list) != 0 {
		t.Fatalf("left helpers after cancellation: %v %v", list, err)
	}
}

func TestReclaimMemorySwapDisabled(t *testing.T) {
	d, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c, err := d.c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     &container.Config{Image: reclaimImage, Entrypoint: []string{"sleep"}, Cmd: []string{"60"}},
		HostConfig: &container.HostConfig{NetworkMode: "none", Resources: container.Resources{Memory: 128 << 20, MemorySwap: 128 << 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Remove(context.Background(), c.ID)
	if err = d.Start(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.ReclaimMemory(ctx, c.ID); !errors.Is(err, engine.ErrReclaimUnsupported) {
		t.Fatalf("swap disabled: %v", err)
	}
	list, err := d.List(ctx, map[string]string{"deskyard.helper": "memory-reclaim"})
	if err != nil || len(list) != 0 {
		t.Fatalf("left helpers after unsupported target: %v %v", list, err)
	}
}

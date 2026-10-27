//go:build engine

package docker

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

func TestInspectReportsNamedVolume(t *testing.T) {
	d, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	suffix := strings.ToLower(rand.Text()[:8])
	volume := "deskyard-test-vol-" + suffix
	if _, err := d.c.VolumeCreate(ctx, client.VolumeCreateOptions{Name: volume}); err != nil {
		t.Fatal(err)
	}
	defer d.c.VolumeRemove(context.Background(), volume, client.VolumeRemoveOptions{Force: true})
	res, err := d.c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:   "deskyard-test-volume-" + suffix,
		Config: &container.Config{Image: "alpine:3.22", Cmd: []string{"sleep", "60"}},
		HostConfig: &container.HostConfig{Mounts: []mount.Mount{
			{Type: mount.TypeVolume, Source: volume, Target: "/v"},
			{Type: mount.TypeBind, Source: "/dev/shm", Target: "/b"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Remove(context.Background(), res.ID)
	c, err := d.Inspect(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range c.Mounts {
		got[m.Target] = m.Volume
	}
	if len(c.Mounts) != 2 || got["/v"] != volume || got["/b"] != "" {
		t.Fatalf("volume names by target: %v", got)
	}
}

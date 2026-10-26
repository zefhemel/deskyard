//go:build engine

package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// buildTestImage builds a desktop-like image (labelled, with VOLUME
// /workspace) tagged tag, and returns its ID.
func buildTestImage(t *testing.T, ctx context.Context, d *Docker, tag, test, version string) string {
	t.Helper()
	dockerfile := "FROM alpine:3.22\nLABEL deskyard.kind=desktop deskyard.test=" + test + " version=" + version + "\nVOLUME /workspace\n"
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(dockerfile))})
	_, _ = tw.Write([]byte(dockerfile))
	_ = tw.Close()
	res, err := d.c.ImageBuild(ctx, &buf, client.ImageBuildOptions{Tags: []string{tag}, Remove: true})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	img, err := d.ImageInspect(ctx, tag)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return img.ID
}

func anonymousVolumes(t *testing.T, ctx context.Context, d *Docker) []string {
	t.Helper()
	f := client.Filters{}
	f.Add("label", "com.docker.volume.anonymous")
	res, err := d.c.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range res.Items {
		names = append(names, v.Name)
	}
	return names
}

func TestOldDesktopImagesArePrunableAndCopiesLeaveNoVolumes(t *testing.T) {
	d, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	test := strings.ToLower(rand.Text()[:8])
	tag := "deskyard-prune-test:" + test
	old := buildTestImage(t, ctx, d, tag, test, "1")
	defer d.c.ImageRemove(context.Background(), old, client.ImageRemoveOptions{Force: true, PruneChildren: true})

	// Reading a file out of an image leaves no anonymous volume behind.
	before := anonymousVolumes(t, ctx, d)
	rc, err := d.CopyFromImage(ctx, tag, "/etc/alpine-release")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, rc)
	rc.Close()
	for _, v := range anonymousVolumes(t, ctx, d) {
		if !slices.Contains(before, v) {
			t.Errorf("copying from the image left volume %s", v)
		}
	}

	// A new build of the tag leaves the old version dangling.
	current := buildTestImage(t, ctx, d, tag, test, "2")
	defer d.c.ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	mine := map[string]string{"deskyard.kind": "desktop", "deskyard.test": test}
	ids := func() []string {
		imgs, err := d.DanglingImages(ctx, mine)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, img := range imgs {
			out = append(out, img.ID)
		}
		return out
	}
	if got := ids(); !slices.Equal(got, []string{old}) || slices.Contains(got, current) {
		t.Fatalf("dangling = %v, want only %s", got, old)
	}

	// Removal is refused while a container, even a stopped one, uses it.
	c, err := d.Create(ctx, engine.ContainerSpec{Name: "deskyard-prune-test-" + test, Image: old, Network: "none", Cmd: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveImage(ctx, old); err == nil {
		t.Fatal("removed an image a container uses")
	}
	if err := d.Remove(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveImage(ctx, old); err != nil {
		t.Fatal(err)
	}
	if got := ids(); len(got) != 0 {
		t.Fatalf("still dangling: %v", got)
	}
	if _, err := d.ImageInspect(ctx, tag); err != nil {
		t.Fatalf("the current version went too: %v", err)
	}
}

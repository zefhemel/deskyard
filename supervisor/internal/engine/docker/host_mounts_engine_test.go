//go:build engine

package docker

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestStrictHostMountReadOnlyIncludesSubmounts(t *testing.T) {
	d, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	name := "deskyard-host-mount-test-" + strings.ToLower(rand.Text()[:8])
	// /dev/shm is an existing nested tmpfs on the Linux engine host, including
	// Mac container engines. Use only our own unique file; do not change any
	// engine-host mounts or join its PID namespace.
	writer, err := d.Create(ctx, engine.ContainerSpec{Name: name + "-writer", Image: "alpine:3.22", Network: "none", Cmd: []string{"sleep", "60"},
		Mounts: []engine.Mount{{Source: "/dev/shm", Target: "/scratch", MustExist: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Remove(context.Background(), writer)
	if err := d.Start(ctx, writer); err != nil {
		t.Fatal(err)
	}
	filename := "/scratch/" + name
	defer engine.Output(context.Background(), d, writer, engine.ExecSpec{User: "1000", Cmd: []string{"rm", "-f", filename}})
	_, _, code, err := engine.Output(ctx, d, writer, engine.ExecSpec{User: "1000", Cmd: []string{"sh", "-c", `printf 'original\n' > "$1"`, "probe", filename}})
	if err != nil || code != 0 {
		t.Fatalf("source: %d %v", code, err)
	}
	target, err := d.Create(ctx, engine.ContainerSpec{Name: name + "-reader", Image: "alpine:3.22", Network: "none", Cmd: []string{"sleep", "60"},
		NoNewPrivileges: true, Mounts: []engine.Mount{{Source: "/dev", Target: "/data", ReadOnly: true, MustExist: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Remove(context.Background(), target)
	inspect, err := d.c.ContainerInspect(ctx, target, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inspect.Container.HostConfig.Mounts) != 1 || inspect.Container.HostConfig.Mounts[0].BindOptions == nil || !inspect.Container.HostConfig.Mounts[0].BindOptions.ReadOnlyForceRecursive {
		t.Fatal("read-only strict mount can silently fall back to writable submounts")
	}
	if err := d.Start(ctx, target); err != nil {
		t.Fatal(err)
	}
	nested := "/data/shm/" + name
	out, _, code, err := engine.Output(ctx, d, target, engine.ExecSpec{User: "1000", Cmd: []string{"cat", nested}})
	if err != nil || code != 0 || string(out) != "original\n" {
		t.Fatalf("read: %q %d %v", out, code, err)
	}
	_, errOut, code, err := engine.Output(ctx, d, target, engine.ExecSpec{User: "1000", Cmd: []string{"sh", "-c", `echo no >> "$1"`, "probe", nested}})
	if err != nil || code == 0 || !strings.Contains(string(errOut), "Read-only file system") {
		t.Fatalf("nested writable: %s %d %v", errOut, code, err)
	}
	out, _, code, err = engine.Output(ctx, d, target, engine.ExecSpec{User: "1000", Cmd: []string{"sh", "-c", "grep NoNewPrivs: /proc/self/status"}})
	if err != nil || code != 0 || !strings.Contains(string(out), "1") {
		t.Fatalf("new privilege acquisition allowed: %q %v", out, err)
	}
}

// Package enginetest is the behaviour every engine.Engine must show. Each
// implementation runs it against a real engine (build tag "engine").
package enginetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

const image = "alpine:3.22"

func Run(t *testing.T, e engine.Engine) {
	ctx := context.Background()
	if err := e.Pull(ctx, image, nil); err != nil {
		t.Fatalf("pull: %v", err)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := "deskyard-enginetest-" + hex.EncodeToString(b)
	labels := map[string]string{"deskyard.enginetest": name}

	if err := e.CreateNetwork(ctx, name, "", labels); err != nil {
		t.Fatalf("network: %v", err)
	}
	t.Cleanup(func() { _ = e.RemoveNetwork(context.Background(), name) })
	dir := t.TempDir()
	id, err := e.Create(ctx, engine.ContainerSpec{
		Name: name, Image: image, Labels: labels, Env: map[string]string{"HELLO": "world"},
		Network: name, Aliases: []string{"probe"},
		Cmd: []string{"sleep", "600"}, Devices: []string{"/dev/zero"},
		Mounts: []engine.Mount{{Source: dir, Target: "/mnt/probe", ReadOnly: true}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = e.Remove(context.Background(), id) })

	t.Run("labels, env, devices and mounts round-trip", func(t *testing.T) {
		c, err := e.Inspect(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if c.Labels["deskyard.enginetest"] != name || c.Env["HELLO"] != "world" || c.Name != name ||
			len(c.Devices) != 1 || c.Devices[0] != "/dev/zero" ||
			len(c.Mounts) != 1 || c.Mounts[0].Source != dir || c.Mounts[0].Target != "/mnt/probe" || !c.Mounts[0].ReadOnly {
			t.Fatalf("got %+v", c)
		}
		list, err := e.List(ctx, labels)
		if err != nil || len(list) != 1 || list[0].ID != id {
			t.Fatalf("list: %v %+v", err, list)
		}
	})
	t.Run("start, exec as a user, stop", func(t *testing.T) {
		if err := e.Start(ctx, id); err != nil {
			t.Fatal(err)
		}
		c, _ := e.Inspect(ctx, id)
		if !c.Running() || c.IP[name] == "" {
			t.Fatalf("not running with an address: %+v", c)
		}
		out, errOut, code, err := engine.Output(ctx, e, id, engine.ExecSpec{
			Cmd: []string{"sh", "-c", "id -u; echo oops >&2; exit 3"}, User: "1000",
		})
		if err != nil || strings.TrimSpace(string(out)) != "1000" || strings.TrimSpace(string(errOut)) != "oops" || code != 3 {
			t.Fatalf("exec: %q %q %d %v", out, errOut, code, err)
		}
		p, err := e.Exec(ctx, id, engine.ExecSpec{Cmd: []string{"cat"}, Stdin: true})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = p.Stdin.Write([]byte("piped"))
		_ = p.Stdin.Close()
		buf := make([]byte, 16)
		n, _ := p.Stdout.Read(buf)
		if string(buf[:n]) != "piped" {
			t.Fatalf("stdin: %q", buf[:n])
		}
		if code, _ := p.Wait(); code != 0 {
			t.Fatalf("cat exit %d", code)
		}
		p.Close()
		if err := e.Stop(ctx, id, time.Second); err != nil {
			t.Fatal(err)
		}
		if c, _ := e.Inspect(ctx, id); c.Running() {
			t.Fatal("still running")
		}
	})
	t.Run("networks", func(t *testing.T) {
		nets, err := e.Networks(ctx, labels)
		if err != nil || len(nets) != 1 || nets[0].Name != name {
			t.Fatalf("%v %+v", err, nets)
		}
	})
	t.Run("copy from image", func(t *testing.T) {
		r, err := e.CopyFromImage(ctx, image, "/etc/alpine-release")
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Close()
	})
	t.Run("rename keeps the container", func(t *testing.T) {
		renamed := name + ".renamed"
		if err := e.Rename(ctx, id, renamed); err != nil {
			t.Fatal(err)
		}
		c, err := e.Inspect(ctx, renamed)
		if err != nil || c.ID != id || c.Name != renamed || c.Labels["deskyard.enginetest"] != name {
			t.Fatalf("renamed: %v %+v", err, c)
		}
		if _, err := e.Inspect(ctx, name); !errors.Is(err, engine.ErrNotFound) {
			t.Fatalf("old name still answers: %v", err)
		}
		if err := e.Rename(ctx, id, name); err != nil {
			t.Fatal(err)
		}
		if c, err := e.Inspect(ctx, name); err != nil || c.ID != id {
			t.Fatalf("renamed back: %v %+v", err, c)
		}
	})
	t.Run("not found", func(t *testing.T) {
		if _, err := e.Inspect(ctx, "cc-does-not-exist"); !errors.Is(err, engine.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
}

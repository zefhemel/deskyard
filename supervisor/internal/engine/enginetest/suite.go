// Package enginetest is the behaviour every engine.Engine must show. Each
// implementation runs it against a real engine (build tag "engine").
package enginetest

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
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
	port := 0
	id, err := e.Create(ctx, engine.ContainerSpec{
		Name: name, Image: image, Labels: labels, Env: map[string]string{"HELLO": "world"},
		Network: name, Aliases: []string{"probe"}, PublishLocal: &port,
		Cmd: []string{"sleep", "600"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = e.Remove(context.Background(), id) })

	t.Run("labels and env round-trip", func(t *testing.T) {
		c, err := e.Inspect(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if c.Labels["deskyard.enginetest"] != name || c.Env["HELLO"] != "world" || c.Name != name {
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
		if !c.Running() || c.IP[name] == "" || c.LocalPort == 0 {
			t.Fatalf("not running with an address and a port: %+v", c)
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
	t.Run("not found", func(t *testing.T) {
		if _, err := e.Inspect(ctx, "cc-does-not-exist"); !errors.Is(err, engine.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
	t.Run("images", func(t *testing.T) { images(t, e) })
}

func uniqueRef(t *testing.T, e engine.Engine) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	ref := "deskyard-enginetest/img-" + hex.EncodeToString(b) + ":t"
	t.Cleanup(func() { _ = e.RemoveImage(context.Background(), ref) })
	return ref
}

func dockerfileTar(t *testing.T, dockerfile string) io.Reader {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(dockerfile))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte(dockerfile))
	_ = tw.Close()
	return &buf
}

func images(t *testing.T, e engine.Engine) {
	ctx := context.Background()

	t.Run("build and labels", func(t *testing.T) {
		ref := uniqueRef(t, e)
		var mu sync.Mutex
		var lines []string
		id, err := e.Build(ctx, engine.BuildSpec{
			Context:    dockerfileTar(t, "FROM "+image+"\nRUN echo hi > /x\n"),
			Dockerfile: "Dockerfile", Tags: []string{ref}, Labels: map[string]string{"deskyard.enginetest": "yes"},
		}, func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() })
		if err != nil {
			t.Fatal(err)
		}
		img, err := e.ImageInspect(ctx, id)
		if err != nil || img.Labels["deskyard.enginetest"] != "yes" {
			t.Fatalf("inspect %s: %v %+v", id, err, img)
		}
		if img2, err := e.ImageInspect(ctx, ref); err != nil || img2.ID != id {
			t.Fatalf("tag: %v %+v want %s", err, img2, id)
		}
		mu.Lock()
		all := strings.Join(lines, "\n")
		mu.Unlock()
		if !strings.Contains(all, "echo hi") && !strings.Contains(all, "hi") {
			t.Fatalf("log missed the step: %q", all)
		}
	})
	t.Run("build cancel", func(t *testing.T) {
		ids := func() map[string]string {
			list, err := e.List(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			m := map[string]string{}
			for _, c := range list {
				if !strings.HasPrefix(c.Name, "deskyard-") { // build containers have engine-generated names
					m[c.ID] = c.State
				}
			}
			return m
		}
		before := ids()
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		running := make(chan struct{})
		var once sync.Once
		done := make(chan error, 1)
		go func() {
			_, err := e.Build(cctx, engine.BuildSpec{
				Context:    dockerfileTar(t, "FROM "+image+"\nRUN sleep 600\n"),
				Dockerfile: "Dockerfile", NoCache: true,
			}, func(l string) {
				if strings.Contains(l, "sleep 600") {
					once.Do(func() { close(running) })
				}
			})
			done <- err
		}()
		select {
		case <-running:
		case err := <-done:
			t.Fatalf("build ended before the sleep step: %v", err)
		case <-time.After(60 * time.Second):
			t.Fatal("build never reached the sleep step")
		}
		time.Sleep(time.Second) // let the step's container start
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("cancelled build returned no error")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("build did not return within 10s of cancel")
		}
		// The daemon must stop the step too, leaving no build container.
		var left map[string]string
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
			left = map[string]string{}
			for id, st := range ids() {
				if _, old := before[id]; !old {
					left[id] = st
				}
			}
			if len(left) == 0 {
				return
			}
		}
		t.Fatalf("build containers remain after cancel: %v", left)
	})
	t.Run("build failure", func(t *testing.T) {
		_, err := e.Build(ctx, engine.BuildSpec{
			Context:    dockerfileTar(t, "FROM "+image+"\nRUN false\n"),
			Dockerfile: "Dockerfile",
		}, func(string) {})
		if err == nil || !strings.Contains(err.Error(), "false") {
			t.Fatalf("want an error naming the failing step, got %v", err)
		}
	})
	t.Run("commit", func(t *testing.T) {
		ref := uniqueRef(t, e)
		cid, err := e.Create(ctx, engine.ContainerSpec{Image: image, Cmd: []string{"sh", "-c", "echo committed > /marker"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.Remove(context.Background(), cid) })
		if err := e.Start(ctx, cid); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			c, err := e.Inspect(ctx, cid)
			if err != nil {
				t.Fatal(err)
			}
			if !c.Running() && c.State != "created" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("container still %s", c.State)
			}
		}
		id, err := e.Commit(ctx, cid, ref, nil, map[string]string{"deskyard.enginetest": "commit"})
		if err != nil {
			t.Fatal(err)
		}
		img, err := e.ImageInspect(ctx, ref)
		if err != nil || img.ID != id || img.Labels["deskyard.enginetest"] != "commit" {
			t.Fatalf("inspect: %v %+v want %s", err, img, id)
		}
		r, err := e.CopyFromImage(ctx, ref, "/marker")
		if err != nil {
			t.Fatalf("committed image lacks the file: %v", err)
		}
		_ = r.Close()
	})
	t.Run("tag and remove image", func(t *testing.T) {
		ref, ref2 := uniqueRef(t, e), uniqueRef(t, e)
		id, err := e.Build(ctx, engine.BuildSpec{
			Context: dockerfileTar(t, "FROM "+image+"\nRUN echo tagme > /t\n"), Dockerfile: "Dockerfile", Tags: []string{ref},
		}, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Tag(ctx, id, ref2); err != nil {
			t.Fatal(err)
		}
		if img, err := e.ImageInspect(ctx, ref2); err != nil || img.ID != id {
			t.Fatalf("tag: %v %+v", err, img)
		}
		cid, err := e.Create(ctx, engine.ContainerSpec{Image: ref, Cmd: []string{"true"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.RemoveImage(ctx, ref); err != nil {
			t.Fatalf("untagging one of two tags: %v", err)
		}
		// The last tag is in use by a container.
		if err := e.RemoveImage(ctx, ref2); err == nil || !strings.Contains(err.Error(), "in use") {
			t.Fatalf("want an in-use error, got %v", err)
		}
		_ = e.Remove(ctx, cid)
		if err := e.RemoveImage(ctx, ref2); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ImageInspect(ctx, ref2); !errors.Is(err, engine.ErrNotFound) {
			t.Fatalf("still there: %v", err)
		}
		if err := e.RemoveImage(ctx, ref2); !errors.Is(err, engine.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
	t.Run("image tags, user, entrypoint and copy in", func(t *testing.T) {
		ref := uniqueRef(t, e)
		repo := ref[:strings.LastIndex(ref, ":")]
		ref2 := repo + ":other"
		t.Cleanup(func() { _ = e.RemoveImage(context.Background(), ref2) })
		id, err := e.Build(ctx, engine.BuildSpec{
			Context: dockerfileTar(t, "FROM "+image+"\nRUN echo tags > /t\nUSER nobody\n"), Dockerfile: "Dockerfile", Tags: []string{ref},
		}, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Tag(ctx, id, ref2); err != nil {
			t.Fatal(err)
		}
		tags, err := e.ImageTags(ctx, repo)
		if err != nil || len(tags) != 2 || tags[ref] != id || tags[ref2] != id {
			t.Fatalf("tags of %s: %v %v (want both → %s)", repo, err, tags, id)
		}
		if img, err := e.ImageInspect(ctx, id); err != nil || img.User != "nobody" {
			t.Fatalf("user: %v %+v", err, img)
		}
		cid, err := e.Create(ctx, engine.ContainerSpec{Image: ref, Entrypoint: []string{"sleep", "30"}})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = e.Remove(context.Background(), cid) }()
		if err := e.Start(ctx, cid); err != nil {
			t.Fatal(err)
		}
		out, _, code, err := engine.Output(ctx, e, cid, engine.ExecSpec{Cmd: []string{"cat", "/proc/1/cmdline"}, User: "root"})
		if err != nil || code != 0 || !strings.HasPrefix(string(out), "sleep") {
			t.Fatalf("entrypoint: %v %d %q", err, code, out)
		}
		if img, _ := e.ImageInspect(ctx, id); img.Arch == "" {
			t.Errorf("no architecture: %+v", img)
		}
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{Name: "copied", Mode: 0o755, Size: 2})
		_, _ = tw.Write([]byte("hi"))
		_ = tw.Close()
		if err := e.CopyToContainer(ctx, cid, "/", &buf); err != nil {
			t.Fatal(err)
		}
		out, _, code, err = engine.Output(ctx, e, cid, engine.ExecSpec{Cmd: []string{"cat", "/copied"}, User: "0:0"})
		if err != nil || code != 0 || string(out) != "hi" {
			t.Fatalf("copied file: %v %d %q", err, code, out)
		}
	})
	t.Run("no healthcheck", func(t *testing.T) {
		ref := uniqueRef(t, e)
		// The healthcheck leaves a mark each time it runs.
		if _, err := e.Build(ctx, engine.BuildSpec{
			Context:    dockerfileTar(t, "FROM "+image+"\nHEALTHCHECK --interval=1s --start-period=0s CMD touch /checked\n"),
			Dockerfile: "Dockerfile", Tags: []string{ref},
		}, func(string) {}); err != nil {
			t.Fatal(err)
		}
		run := func(off bool) bool {
			cid, err := e.Create(ctx, engine.ContainerSpec{Image: ref, Cmd: []string{"sleep", "30"}, NoHealthcheck: off})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = e.Remove(context.Background(), cid) }()
			if err := e.Start(ctx, cid); err != nil {
				t.Fatal(err)
			}
			time.Sleep(3 * time.Second)
			_, _, code, err := engine.Output(ctx, e, cid, engine.ExecSpec{Cmd: []string{"test", "-e", "/checked"}})
			if err != nil {
				t.Fatal(err)
			}
			return code == 0
		}
		if !run(false) {
			t.Fatal("the image's healthcheck never ran (the test proves nothing)")
		}
		if run(true) {
			t.Fatal("the healthcheck ran with NoHealthcheck")
		}
	})
}

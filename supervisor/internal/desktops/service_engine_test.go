//go:build engine

package desktops

import (
	"archive/tar"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine/docker"
)

// The desktop image to test with (build it from images/debian-xfce first).
func testImage() string {
	if v := os.Getenv("CC_TEST_IMAGE"); v != "" {
		return v
	}
	return "deskyard-debian-xfce:test"
}

// The desktops' subnet pool: DESKYARD_TEST_SUBNET, as for the pytest suites,
// so checkouts testing at the same time stay apart.
func testSubnet() string {
	if v := os.Getenv("DESKYARD_TEST_SUBNET"); v != "" {
		return v
	}
	return "10.214.0.0/20"
}

func newService(t *testing.T) *Service {
	d, err := docker.New("")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// Docker Desktop/OrbStack share /Users and /private; t.TempDir lives there on macOS.
	dir, _ = filepath.EvalSymlinks(dir)
	return New(d, Config{DataDir: dir, Subnet: testSubnet()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestLifecycle(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	name := "cct-" + randomHex(3)
	t.Cleanup(func() { _ = s.Remove(context.Background(), name, true) })

	// An existing folder with a file is reused.
	folder := filepath.Join(s.Cfg.DataDir, name)
	_ = os.MkdirAll(folder, 0o755)
	_ = os.WriteFile(filepath.Join(folder, "kept.txt"), []byte("hi"), 0o644)

	c, err := s.Create(ctx, name, Settings{Image: testImage()}, func(string, float64) {})
	if err != nil {
		t.Fatal(err)
	}
	if c.State != "running" || c.ID == "" {
		t.Fatalf("%+v", c)
	}
	if _, err := s.Create(ctx, name, Settings{Image: testImage()}, func(string, float64) {}); err != ErrExists {
		t.Fatalf("duplicate: %v", err)
	}
	addr, token, err := s.Target(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	// Caddy answers with the token, refuses without.
	waitFor(t, func() bool {
		req, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
		req.Header.Set("X-Deskyard-Token", token)
		r, err := http.DefaultClient.Do(req)
		return err == nil && r.StatusCode == 200
	})
	r, _ := http.Get("http://" + addr + "/")
	if r.StatusCode != 401 {
		t.Fatalf("without token: %d", r.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(folder, "kept.txt")); err != nil {
		t.Fatal("existing folder not reused")
	}

	if err := s.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Get(ctx, name); c.State != "stopped" {
		t.Fatal(c.State)
	}
	if _, _, err := s.Target(ctx, name); err != ErrStopped {
		t.Fatal(err)
	}
	if err := s.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, name, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, name); err != ErrNotFound {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(folder, "kept.txt")); err != nil {
		t.Fatal("folder not kept on remove")
	}
	// Create again on the same folder, then remove with its files.
	if _, err := s.Create(ctx, name, Settings{Image: testImage()}, func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, name, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatal("folder not deleted")
	}
}

func TestFailedCreateLeavesNothing(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	name := "cct-" + randomHex(3)
	_, err := s.Create(ctx, name, Settings{Image: "alpine:3.22"}, func(string, float64) {})
	if err == nil {
		t.Fatal("an image without the contract label was accepted")
	}
	if _, err := s.Eng.Inspect(ctx, containerName(name)); err == nil {
		t.Fatal("container left behind")
	}
	if s.FolderExists(name) {
		t.Fatal("folder left behind")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 120; i++ {
		if ok() {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("timed out")
}

func TestFolderTar(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	name := "cct-" + randomHex(3)
	folder := filepath.Join(s.Cfg.DataDir, name)
	_ = os.MkdirAll(filepath.Join(folder, ".config/app"), 0o755)
	_ = os.WriteFile(filepath.Join(folder, ".config/app/settings.json"), []byte("hi"), 0o644)
	_ = os.WriteFile(filepath.Join(folder, "private"), []byte("secret"), 0o600)
	_ = os.Symlink("/etc/passwd", filepath.Join(folder, "link"))

	rc, err := s.FolderTar(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		got[h.Name] = string(b) + h.Linkname
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if got["./.config/app/settings.json"] != "hi" || got["./private"] != "secret" || got["./link"] != "/etc/passwd" {
		t.Errorf("tar: %v", got)
	}
	// The helper is gone, and the folder is as it was (mounted read-only).
	cs, _ := s.Eng.List(ctx, nil)
	for _, c := range cs {
		if strings.HasPrefix(c.Name, "deskyard-read-") {
			t.Errorf("helper %s left behind", c.Name)
		}
	}
	if _, err := s.FolderTar(ctx, "cct-missing"); err == nil {
		t.Error("a missing folder read without error")
	}
}

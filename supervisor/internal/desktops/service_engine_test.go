//go:build engine

package desktops

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine/docker"
)

// The desktop image to test with (build the debian-xfce target in images/Dockerfile first).
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

	// Some paths only, in that order; a second name is a hard link.
	if err := os.Link(filepath.Join(folder, "private"), filepath.Join(folder, "again")); err != nil {
		t.Fatal(err)
	}
	rc, err = s.FolderTar(ctx, name, "private", "./.config/app/settings.json", "again")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr = tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		names = append(names, fmt.Sprintf("%s %c %s%s", h.Name, h.Typeflag, b, h.Linkname))
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"./private 0 secret", "./.config/app/settings.json 0 hi", "./again 1 ./private"}; !slices.Equal(names, want) {
		t.Errorf("paths: %q, want %q", names, want)
	}
}

// Recovery uses only persisted intent and the engine after the supervisor dies
// between removing the old container and creating its replacement.
func TestRecoverInterruptedReplacement(t *testing.T) {
	s := newService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	name := "cct-" + randomHex(3)
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		if err := s.Remove(cleanup, name, true); err != nil {
			// The crash window may leave only the network if recovery itself fails.
			_ = s.Eng.Remove(cleanup, containerName(name))
			_ = s.Eng.RemoveNetwork(cleanup, containerName(name))
		}
	})
	folder := filepath.Join(s.Cfg.DataDir, name)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(folder, "recovery-marker.txt")
	if err := os.WriteFile(marker, []byte("workspace survives interrupted replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prior, err := s.Create(ctx, name, Settings{Image: testImage(), MemoryBytes: 2 << 30, NanoCPUs: 2e9}, func(string, float64) {})
	if err != nil {
		t.Fatal(err)
	}
	if prior.ID == "" || prior.ImageID == "" || prior.Created.IsZero() || !prior.Container.Running() {
		t.Fatalf("incomplete original desktop: %+v", prior)
	}
	rec := replacement{Version: 1, Name: name, ID: prior.ID, ContainerID: prior.Container.ID, ImageID: prior.ImageID, Token: prior.Container.Env[TokenEnv], Created: prior.Created, WasRunning: prior.Container.Running(), Settings: prior.Settings}
	if rec.Token == "" {
		t.Fatal("original desktop has no proxy token")
	}
	if err := s.saveReplacement(rec); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(s.recoveryPath(name)); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("recovery record permissions: %o", info.Mode().Perm())
	}
	if err := s.Eng.Remove(ctx, prior.Container.ID); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.Eng, s.Cfg, s.Log)
	if err := restarted.Reconcile(ctx); err != nil {
		t.Fatalf("recover interrupted replacement: %v", err)
	}
	restored, err := restarted.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Container.ID == prior.Container.ID {
		t.Fatal("recovery did not create a fresh runtime")
	}
	if restored.ID != prior.ID || restored.ImageID != prior.ImageID || restored.Image != prior.Image || !restored.Created.Equal(prior.Created) || !reflect.DeepEqual(restored.Settings, prior.Settings) {
		t.Fatalf("recovery changed identity or settings:\noriginal: %+v\nrestored: %+v", prior, restored)
	}
	if !restored.Container.Running() {
		t.Fatal("running intent was not restored")
	}
	if restored.Container.Env[TokenEnv] != rec.Token {
		t.Fatal("proxy token changed during recovery")
	}
	if err := restarted.WaitReady(ctx, name, 20*time.Second); err != nil {
		t.Fatalf("restored runtime not ready: %v", err)
	}
	addr, token, err := restarted.Target(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if token != rec.Token {
		t.Fatal("recovered target changed proxy token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Deskyard-Token", rec.Token)
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("preserved proxy token rejected: %s", response.Status)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "workspace survives interrupted replacement\n" {
		t.Fatalf("workspace marker lost: %q %v", data, err)
	}
	if _, err := os.Stat(restarted.recoveryPath(name)); !os.IsNotExist(err) {
		t.Fatalf("successful recovery retained journal: %v", err)
	}
}

//go:build engine

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/engine/docker"
)

// engineDirs makes a temporary folder the engine can mount (under /tmp, which
// Docker Desktop and OrbStack share) with the given subfolders.
func engineDirs(t *testing.T, subs ...string) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "deskyard-storage-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// The helper wrote some of it as root.
		d, _ := docker.New("")
		r := &EngineRunner{Eng: d}
		_, _ = r.Run(context.Background(), Call{Mounts: []engine.Mount{{Source: root, Target: "/clean"}}, Args: []string{"sh", "-c", "rm -rf /clean/*"}})
		os.RemoveAll(root)
	})
	for _, s := range subs {
		if err := os.MkdirAll(filepath.Join(root, s), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func engineRunner(t *testing.T) *EngineRunner {
	t.Helper()
	d, err := docker.New("")
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.MkdirTemp("/tmp", "deskyard-storage-bin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(bin) })
	return &EngineRunner{Eng: d, BinDir: bin}
}

func TestEngineRunnerResticLocal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	r := engineRunner(t)
	root := engineDirs(t, "remote", "repo-secret", "data", "repo")
	if err := os.WriteFile(filepath.Join(root, "remote", "rclone.conf"), []byte("[t]\ntype = local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "repo-secret", "restic-password"), []byte("pw"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	mounts := []engine.Mount{
		{Source: filepath.Join(root, "remote"), Target: RemoteMount, ReadOnly: true},
		{Source: filepath.Join(root, "repo-secret"), Target: "/secret/repo", ReadOnly: true},
		{Source: filepath.Join(root, "repo"), Target: "/repo"},
		{Source: filepath.Join(root, "data"), Target: "/deskyard/volume/v", ReadOnly: true},
	}
	env := map[string]string{"RCLONE_CONFIG": RemoteMount + "/rclone.conf", "RESTIC_PASSWORD_FILE": "/secret/repo/restic-password", "RESTIC_REPOSITORY": "rclone:t:/repo/restic"}
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		res, err := r.Run(ctx, Call{Mounts: mounts, Env: env, Args: args, Stdout: &out})
		if err != nil || res.Code != 0 {
			t.Fatalf("%v: %+v %v\n%s", args, res, err, out.String())
		}
		return out.String()
	}
	run("restic", "init")
	run("restic", "backup", "--host", "deskyard-test", "--tag", "volume:v", "/deskyard/volume/v")
	var snaps []map[string]any
	if err := json.Unmarshal([]byte(run("restic", "snapshots", "--json")), &snaps); err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots %v %v", snaps, err)
	}
	ob, err := Obscure("secret pass")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run("rclone", "reveal", ob)); got != "secret pass" {
		t.Fatalf("rclone reveals %q", got)
	}
}

// TestResticRoundTrip runs Tool against real restic, with a local rclone
// remote kept in the (writable) cache folder.
func TestResticRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	root := engineDirs(t, "remote", "key", "cache", "data/sub", "restore", "folder")
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	write("remote/rclone.conf", "[t]\ntype = local\n")
	write("key/restic-password", "pw")
	write("data/a.txt", "alpha")
	write("data/sub/b.txt", "beta")
	tool := Tool{Run: engineRunner(t), Host: "deskyard-test", CacheDir: filepath.Join(root, "cache")}
	repo := Repo{Remote: "t", Path: "/cache/repo", ConfigDir: filepath.Join(root, "remote"), KeyDir: filepath.Join(root, "key")}
	if err := tool.CheckRepo(ctx, repo); !errors.Is(err, ErrNoRepo) {
		t.Fatalf("before init: %v", err)
	}
	if err := tool.Init(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := tool.CheckRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	src := []engine.Mount{{Source: filepath.Join(root, "data"), Target: "/deskyard/volume/v", ReadOnly: true}}
	id, _, err := tool.Backup(ctx, repo, src, []string{"/deskyard/volume/v"}, []string{"deskyard", "volume:v", "manual"}, nil)
	if err != nil || id == "" {
		t.Fatalf("backup %q %v", id, err)
	}
	snaps, err := tool.Snapshots(ctx, repo, "volume:v")
	if err != nil || len(snaps) != 1 || snaps[0].ID != id || snaps[0].Host != "deskyard-test" {
		t.Fatalf("snapshots %+v %v", snaps, err)
	}
	entries, err := tool.Ls(ctx, repo, id, "/deskyard/volume/v")
	if err != nil || len(entries) != 2 {
		t.Fatalf("ls %+v %v", entries, err)
	}
	var out bytes.Buffer
	if err := tool.Dump(ctx, repo, id, "/deskyard/volume/v/a.txt", false, &out); err != nil || out.String() != "alpha" {
		t.Fatalf("dump %q %v", out.String(), err)
	}
	out.Reset()
	if err := tool.Dump(ctx, repo, id, "/deskyard/volume/v/sub", true, &out); err != nil || !bytes.HasPrefix(out.Bytes(), []byte("PK")) {
		t.Fatalf("zip dump %v (%d bytes)", err, out.Len())
	}
	// Restore here: newer files go, deleted ones come back.
	write("restore/newer.txt", "newer")
	if err := tool.Restore(ctx, repo, id, "/deskyard/volume/v", engine.Mount{Source: filepath.Join(root, "restore")}, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "restore/sub/b.txt")); err != nil || string(b) != "beta" {
		t.Fatalf("restored b.txt %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "restore/newer.txt")); !os.IsNotExist(err) {
		t.Fatalf("restore --delete kept a newer file: %v", err)
	}
	// Restore to a folder: only the included path.
	if err := tool.Restore(ctx, repo, id, "/deskyard/volume/v", engine.Mount{Source: filepath.Join(root, "folder")}, []string{"/sub/b.txt"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "folder/sub/b.txt")); err != nil || string(b) != "beta" {
		t.Fatalf("included file %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "folder/a.txt")); !os.IsNotExist(err) {
		t.Fatalf("include restored more: %v", err)
	}
	if err := tool.Forget(ctx, repo, "volume:v", Retention{Last: 1}); err != nil {
		t.Fatal(err)
	}
	if err := tool.Prune(ctx, repo); err != nil {
		t.Fatal(err)
	}
	wrong := repo
	wrong.KeyDir = filepath.Join(root, "remote") // no restic-password there
	if err := tool.CheckRepo(ctx, wrong); err == nil {
		t.Fatal("opened without a key")
	}
}

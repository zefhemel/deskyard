//go:build engine

package storage

import (
	"bytes"
	"context"
	"encoding/json"
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
	return &EngineRunner{Eng: d}
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

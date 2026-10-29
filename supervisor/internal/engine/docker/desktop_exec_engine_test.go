//go:build engine

package docker

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// Opt in against an already built image; every mutation is confined to the
// fresh container created here, without supervisor reconciliation or host binds.
func TestDesktopSessionExecFUSE(t *testing.T) {
	image := os.Getenv("CC_FUSE_TEST_IMAGE")
	if image == "" {
		t.Skip("set CC_FUSE_TEST_IMAGE to a userns-v1 desktop image")
	}
	for _, nnp := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "no-new-privileges"}[nnp], func(t *testing.T) {
			d, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			id, err := d.Create(ctx, engine.ContainerSpec{
				Name: "deskyard-fuse-test-" + strings.ToLower(rand.Text()[:8]), Image: image,
				Network: "none", Flatpak: true, Devices: []string{"/dev/fuse"}, NoNewPrivileges: nnp,
				Env: map[string]string{"WORKSPACE_PROXY_TOKEN": "isolated-fuse-test"},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer d.Remove(context.Background(), id)
			if err = d.Start(ctx, id); err != nil {
				t.Fatal(err)
			}
			run := func(cwd string, argv ...string) (string, int, error) {
				out, stderr, code, err := engine.Output(ctx, d, id, engine.ExecSpec{User: "user", DesktopSession: true, WorkDir: cwd, Cmd: argv})
				if err != nil || code != 0 {
					return string(out) + string(stderr), code, err
				}
				return string(out), code, nil
			}
			for {
				_, code, err := run("/workspace", "desktop-ready")
				if code == 0 && err == nil {
					break
				}
				if ctx.Err() != nil {
					t.Fatal("desktop readiness timed out")
				}
				time.Sleep(200 * time.Millisecond)
			}
			path, code, err := run("/workspace", "sh", "-c", "mkdir -p granted; printf portal-proof > granted/proof; flatpak document-export --app=org.deskyard.Probe --allow-read --allow-write /workspace/granted")
			if err != nil || code != 0 {
				t.Fatalf("export: %d %v %s", code, err, path)
			}
			path = strings.TrimSpace(path)
			if !strings.HasPrefix(path, "/run/workspace/doc/") {
				t.Fatalf("export path: %q", path)
			}
			out, code, err := run(path, "sh", "-c", "cat proof; printf updated > proof; grep CapEff: /proc/self/status")
			if err != nil || code != 0 || !strings.Contains(out, "portal-proof") || !strings.Contains(out, "0000000000000000") {
				t.Fatalf("portal cwd exec: %d %v %s", code, err, out)
			}
			out, code, err = run("/workspace", "cat", "granted/proof")
			if err != nil || code != 0 || out != "updated" {
				t.Fatalf("write-through: %d %v %q", code, err, out)
			}
		})
	}
}

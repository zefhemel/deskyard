//go:build engine

package vpn

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	dockerengine "github.com/zefhemel/deskyard/supervisor/internal/engine/docker"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This characterization requires no tailnet credentials. Live acceptance
// checks are separate; see tests/TAILSCALE.md.
func TestTailscaleKernelEngine(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker CLI required")
	}
	name := fmt.Sprintf("deskyard-ts-contract-%d", time.Now().UnixNano())
	image := "tailscale/tailscale@sha256:ed6480b33c6eb67c9cbd203ed42b9fb57e361986725b6856def71b7a3d9e276c"
	cmd := exec.Command("docker", "run", "--name", name, "--label", "deskyard.test=tailscale-contract", "--cap-add=NET_ADMIN", "--device=/dev/net/tun", "--entrypoint=/bin/sh", image, "-c", `set -eu
 tailscaled --state=/tmp/state --socket=/tmp/tailscaled.sock --tun=tailscale0 >/tmp/daemon.log 2>&1 &
 for i in $(seq 1 30); do
  if tailscale --socket=/tmp/tailscaled.sock status --json >/tmp/status.json 2>/dev/null; then break; fi
  sleep .1
 done
 test -S /tmp/tailscaled.sock
 grep 'NeedsLogin' /tmp/status.json
 iptables -N DESKYARD_CONTRACT
 iptables -A DESKYARD_CONTRACT -i tailscale0 -p tcp --dport 8080 -j DROP
 iptables -C DESKYARD_CONTRACT -i tailscale0 -p tcp --dport 8080 -j DROP
 tailscale --socket=/tmp/tailscaled.sock serve status --json
 `)
	policy, policyErr := tailscalePolicy(nil, nil)
	if policyErr != nil {
		t.Fatal(policyErr)
	}
	cmd.Args[len(cmd.Args)-1] = policy + "\n" + cmd.Args[len(cmd.Args)-1]
	t.Cleanup(func() {
		out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput()
		if err != nil {
			t.Logf("fixture cleanup: %v %s", err, out)
		}
	})
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kernel/control contract: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "NeedsLogin") {
		t.Fatal("daemon not observed before authentication")
	}
}

func TestTailscaleOfflineStateOwnerEngine(t *testing.T) {
	hostRoot := os.Getenv("DESKYARD_TEST_HOST_WORKSPACE")
	if hostRoot == "" {
		t.Skip("set DESKYARD_TEST_HOST_WORKSPACE to the engine-host path of /workspace")
	}
	dir, err := os.MkdirTemp("/workspace/.cache", "deskyard-ts-state-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	hostDir := hostRoot + strings.TrimPrefix(dir, "/workspace")
	out, err := exec.Command("docker", "run", "--rm", "--mount", "type=bind,src="+hostDir+",dst=/state", "--entrypoint=/bin/sh", TailscaleImage, "-c", `printf '%s' '{"_serve/profile":"public","_machinekey":"key"}' > /state/tailscaled.state; chmod 600 /state/tailscaled.state; mkdir /state/certs; chmod 700 /state/certs; printf private-test-certificate > /state/certs/test; chmod 600 /state/certs/test`).CombinedOutput()
	if err != nil {
		t.Fatalf("root-owned state fixture: %v %s", err, out)
	}
	if os.Getuid() != 0 {
		if _, err = os.ReadFile(filepath.Join(dir, "tailscaled.state")); !os.IsPermission(err) {
			t.Fatal("fixture should be unreadable to supervisor before ownership recovery", err)
		}
	}
	docker, err := dockerengine.New("")
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.WithHostPaths(docker, func(p string) string {
		if strings.HasPrefix(p, "/workspace/") {
			return hostRoot + strings.TrimPrefix(p, "/workspace")
		}
		return p
	}, func(p string) string {
		if strings.HasPrefix(p, hostRoot+"/") {
			return "/workspace" + strings.TrimPrefix(p, hostRoot)
		}
		return p
	})
	m := NewManager(eng, nil, dir).ForInstance("state-owner-fixture")
	if err = m.withdrawSavedServe(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err = os.ReadFile(filepath.Join(dir, "certs", "test")); err != nil {
		t.Fatal("private certificate state ownership was not recovered", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "tailscaled.state"))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if json.Unmarshal(b, &values) != nil || values["_machinekey"] != "key" {
		t.Fatal("node keys changed")
	}
	if _, ok := values["_serve/profile"]; ok {
		t.Fatal("public rule retained")
	}
}

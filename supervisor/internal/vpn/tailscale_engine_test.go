//go:build engine

package vpn

import (
	"fmt"
	"os/exec"
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

package vpn

import (
	"context"
	"encoding/json"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailscaleRuntimeIdentity(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(&runtimeEngine{}, nil, dir).ForInstance("instance-one")
	c := Connection{ID: strings.Repeat("a", 32), Provider: "tailscale", Revision: 1, Tailscale: &TailscaleConnection{Tailnet: "home", ClientID: "id", Tags: []string{"tag:deskyard"}}}
	spec, err := m.tailscaleSpec("alice", "desktop", c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Network != "desktop" || len(spec.CapAdd) != 1 || spec.CapAdd[0] != "NET_ADMIN" || spec.Restart != "no" {
		t.Fatalf("unsafe namespace: %+v", spec)
	}
	for _, v := range spec.Env {
		if strings.Contains(v, "secret") {
			t.Fatal("credential in env")
		}
	}
	other, err := m.tailscaleSpec("bob", "desktop", c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Mounts[0].Source == other.Mounts[0].Source {
		t.Fatal("two computers share node state")
	}
	before := spec.Labels[labelPolicy]
	c.Revision++
	rotated, err := m.tailscaleSpec("alice", "desktop", c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if before != rotated.Labels[labelPolicy] {
		t.Fatal("credential revision changes runtime identity")
	}
}
func TestTailscalePolicyIsolation(t *testing.T) {
	script, err := tailscalePolicy(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the generated policy in the engine fixture separately. Here the
	// parser must reject an exception overlapping a reserved computer subnet.
	_, err = tailscalePolicy([]Exception{{Destination: "10.213.0.0/16", Protocol: "tcp", Ports: []PortRange{{Start: 443, End: 443}}}}, nil)
	if err == nil {
		t.Fatal("allowed computer subnet exception")
	}
	if script == "" {
		t.Fatal("no policy")
	}
}
func TestTailscaleStatusParsing(t *testing.T) {
	var v tailscaleDaemonStatus
	if err := json.Unmarshal([]byte(`{"BackendState":"Running","TailscaleIPs":["100.80.1.2"],"Self":{"ID":"node123","DNSName":"alice.home.ts.net."},"CurrentTailnet":{"MagicDNSSuffix":"home.ts.net"}}`), &v); err != nil {
		t.Fatal(err)
	}
	got := v.publicStatus()
	if got.State != "connected" || got.CurrentExitIP != "" || got.Tailscale.DNSName != "alice.home.ts.net" {
		t.Fatalf("wrong status: %+v", got)
	}
	v.BackendState = "NeedsMachineAuth"
	got = v.publicStatus()
	if got.State == "connected" || got.Reason == "" {
		t.Fatal("approval falsely connected")
	}
}
func TestTailscaleRetirementRecovery(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(&runtimeEngine{}, nil, dir)
	if err := m.Retire(context.Background(), "../escape", ""); err == nil {
		t.Fatal("unsafe retirement path")
	}
	// Replacing a runtime never removes independently persisted node state.
	node := m.tailscaleNodeDir("alice", strings.Repeat("a", 32))
	os.MkdirAll(node, 0700)
	os.WriteFile(filepath.Join(node, "tailscaled.state"), []byte("identity"), 0600)
	if err := m.Remove(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(node, "tailscaled.state")); err != nil {
		t.Fatal("runtime removal lost identity")
	}
}

var _ engine.Engine = (*runtimeEngine)(nil)

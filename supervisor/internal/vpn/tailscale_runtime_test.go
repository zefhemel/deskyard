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

func TestTailscaleUnknownIdentityIsQuarantinedForRetirement(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "networks.json"), filepath.Join(dir, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	secret := "private"
	c, err := s.Create(Input{Name: "TS", Provider: "tailscale", Tailscale: &TailscaleInput{Tailnet: "home", ClientID: "id", ClientSecret: &secret, Tags: []string{"tag:desk"}}})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(&runtimeEngine{}, s, filepath.Join(dir, "state"))
	node := m.tailscaleNodeDir("alice", c.ID)
	os.MkdirAll(node, 0700)
	os.WriteFile(filepath.Join(node, "tailscaled.state"), []byte("unrecorded-device-state"), 0600)
	refs, err := m.RetainedReferences(context.Background(), c.ID)
	if err != nil || len(refs) != 1 || refs[0] != "alice" {
		t.Fatal("retained node not referenced", refs, err)
	}
	if err = m.Retire(context.Background(), "alice", c.ID); err != nil {
		t.Fatal("local retirement blocked by missing observed ID:", err)
	}
	if _, err = os.Stat(node); !os.IsNotExist(err) {
		t.Fatal("old state can be reused by fresh enrollment")
	}
	entries, err := os.ReadDir(m.retirementQueue())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			b, _ := os.ReadFile(filepath.Join(m.retirementQueue(), entry.Name()))
			var record tailscaleRetirement
			if json.Unmarshal(b, &record) != nil || !record.Discover {
				t.Fatal("unknown node lost discovery intent")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no durable retirement record")
	}
	refs, err = m.RetainedReferences(context.Background(), c.ID)
	if err != nil || len(refs) != 0 {
		t.Fatal("queued credentials should permit connection deletion", refs, err)
	}
}

func TestOfflineServeWithdrawalPreservesNodeKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailscaled.state")
	os.WriteFile(path, []byte(`{"_serve/profile-one":"public-config","_machinekey":"node-key","profile-one":"profile-data"}`), 0600)
	if err := discardSavedServe(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var v map[string]string
	if json.Unmarshal(b, &v) != nil || v["_machinekey"] != "node-key" || v["profile-one"] != "profile-data" {
		t.Fatal("node keys changed")
	}
	if _, exists := v["_serve/profile-one"]; exists {
		t.Fatal("public config can restore before intent reconciliation")
	}
}

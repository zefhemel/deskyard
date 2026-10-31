package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type tsDaemonEngine struct {
	*runtimeEngine
	config   serveConfig
	failSet  bool
	stopped  bool
	response string
}

func (e *tsDaemonEngine) Exec(_ context.Context, _ string, s engine.ExecSpec) (*engine.Process, error) {
	args := strings.Join(s.Cmd, " ")
	var b []byte
	switch {
	case strings.Contains(args, "serve status"):
		b, _ = json.Marshal(e.config)
	case strings.Contains(args, "debug localapi"):
		if e.failSet {
			return nil, errors.New("private engine detail")
		}
		e.config = serveConfig{}
		if err := json.Unmarshal([]byte(s.Cmd[len(s.Cmd)-1]), &e.config); err != nil {
			return nil, err
		}
		b = []byte("{}")
	default:
		b = []byte(e.response)
	}
	return &engine.Process{Stdout: strings.NewReader(string(b)), Stderr: strings.NewReader(""), Close: func() {}, Wait: func() (int, error) { return 0, nil }}, nil
}
func (e *tsDaemonEngine) Stop(context.Context, string, time.Duration) error {
	e.stopped = true
	return nil
}
func serviceManager(t *testing.T) (*Manager, *tsDaemonEngine) {
	t.Helper()
	dir := t.TempDir()
	e := &tsDaemonEngine{runtimeEngine: &runtimeEngine{}}
	e.response = `{"BackendState":"Running","Self":{"ID":"node123","DNSName":"alice.home.ts.net.","CapMap":{"funnel":[]}},"CertDomains":["alice.home.ts.net"]}`
	m := NewManager(e, nil, dir).ForInstance("instance")
	labels := m.runtimeLabels("alice")
	labels[labelProvider] = "tailscale"
	e.containers = []engine.Container{{ID: "gateway", Name: runtimeName("alice"), Labels: labels, State: "running"}}
	if err := os.MkdirAll(m.directory("alice"), 0700); err != nil {
		t.Fatal(err)
	}
	return m, e
}
func TestServeWithdrawalRecovery(t *testing.T) {
	m, e := serviceManager(t)
	ctx := context.Background()
	pub := []ServiceRule{{LocalPort: 3000, ListenPort: 443, Audience: "public"}}
	status, err := m.ApplyServices(ctx, "alice", pub)
	if err != nil || len(status) != 1 || status[0].State != "applied" {
		t.Fatalf("public apply: %+v %v", status, err)
	}
	e.response = `{"BackendState":"Running","Self":{"ID":"node123","DNSName":"alice.home.ts.net."}}`
	_, err = m.ApplyServices(ctx, "alice", []ServiceRule{{LocalPort: 3000, ListenPort: 443, Audience: "private"}})
	if err == nil {
		t.Fatal("missing HTTPS prerequisite accepted")
	}
	observed, _ := m.observedServe(ctx, "gateway")
	if len(observed.AllowFunnel) != 0 || len(observed.Web) != 0 {
		t.Fatal("old public config retained after failed private edit")
	}
	// A process restart reads saved pending intent and does not report success.
	other := NewManager(e, nil, m.stateDir).ForInstance("instance")
	statuses := other.serviceStatuses(ctx, "alice", "gateway", "alice.home.ts.net")
	if len(statuses) != 1 || statuses[0].State != "pending" || statuses[0].URL != "" {
		t.Fatal("pending intent falsely applied", statuses)
	}
	e.config, _ = buildServeConfig("alice.home.ts.net", pub)
	e.failSet = true
	if _, err = m.ApplyServices(ctx, "alice", nil); err == nil || !e.stopped {
		t.Fatal("unwithdrawable listener did not stop sidecar")
	}
}

func (e *tsDaemonEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	if len(s.Mounts) > 0 && s.Image == TailscaleImage {
		if err := os.WriteFile(filepath.Join(s.Mounts[0].Source, "tailscaled.state"), []byte(`{"_machinekey":"persisted-node"}`), 0600); err != nil {
			return "", err
		}
	}
	return e.runtimeEngine.Create(ctx, s)
}
func TestTailscaleEnsurePreservesIdentityAcrossReplacement(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "networks.json"), filepath.Join(dir, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	secret := "private"
	c, err := s.Create(Input{Name: "Tailnet", Provider: "tailscale", Tailscale: &TailscaleInput{Tailnet: "home", ClientID: "id", ClientSecret: &secret, Tags: []string{"tag:desk"}}})
	if err != nil {
		t.Fatal(err)
	}
	e := &tsDaemonEngine{runtimeEngine: &runtimeEngine{}, response: `{"BackendState":"Running","Self":{"ID":"node123","DNSName":"alice.home.ts.net."},"CurrentTailnet":{"MagicDNSSuffix":"home.ts.net"}}`}
	m := NewManager(e, s, filepath.Join(dir, "state")).ForInstance("instance")
	ctx := context.Background()
	if _, err = m.Ensure(ctx, "alice", "desktop", c.ID, nil); err != nil {
		t.Fatal(err)
	}
	identity, err := readNodeIdentity(m.tailscaleNodeDir("alice", c.ID))
	if err != nil || identity.NodeID != "node123" {
		t.Fatal("identity not persisted", identity, err)
	}
	if err = m.Remove(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	other := NewManager(e, s, filepath.Join(dir, "state")).ForInstance("instance")
	if _, err = other.Ensure(ctx, "alice", "desktop", c.ID, nil); err != nil {
		t.Fatal("replacement:", err)
	}
	identity, _ = readNodeIdentity(other.tailscaleNodeDir("alice", c.ID))
	if identity.NodeID != "node123" {
		t.Fatal("replacement lost node")
	}
	dns, _ := os.ReadFile(filepath.Join(other.directory("alice"), "resolv.conf"))
	if !strings.Contains(string(dns), "search home.ts.net") {
		t.Fatal("MagicDNS suffix missing")
	}
	os.Remove(filepath.Join(other.tailscaleNodeDir("alice", c.ID), "tailscaled.state"))
	if _, err = other.Ensure(ctx, "alice", "desktop", c.ID, nil); err == nil {
		t.Fatal("missing node keys silently replaced")
	}
}

func TestUnauthenticatedEmptyServeDoesNotStopSidecar(t *testing.T) {
	m, e := serviceManager(t)
	e.response = `{"BackendState":"NeedsMachineAuth","Self":{}}`
	e.failSet = true
	_, err := m.ApplyServices(context.Background(), "alice", []ServiceRule{{LocalPort: 3000, ListenPort: 443, Audience: "private"}})
	if err == nil {
		t.Fatal("unapproved publication accepted")
	}
	if e.stopped {
		t.Fatal("empty unpublished node was stopped during approval")
	}
}

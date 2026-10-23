package vpn

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"os"
	"strings"
	"testing"
	"time"
)

type candidateEngine struct {
	runtimeEngine
	netCreated, netRemoved []string
	cancelOnStart          context.CancelFunc
	networks               map[string]engine.Network
}

func (e *candidateEngine) CreateNetwork(_ context.Context, name, subnet string, labels map[string]string) error {
	if e.networks == nil {
		e.networks = map[string]engine.Network{}
	}
	e.networks[name] = engine.Network{ID: name, Name: name, Subnet: "10.212.0.0/28", Labels: labels}
	e.netCreated = append(e.netCreated, name)
	return nil
}
func (e *candidateEngine) RemoveNetwork(_ context.Context, name string) error {
	e.netRemoved = append(e.netRemoved, name)
	delete(e.networks, name)
	return nil
}
func (e *candidateEngine) Networks(_ context.Context, labels map[string]string) ([]engine.Network, error) {
	var ns []engine.Network
	for _, n := range e.networks {
		match := true
		for k, v := range labels {
			if n.Labels[k] != v {
				match = false
			}
		}
		if match {
			ns = append(ns, n)
		}
	}
	return ns, nil
}
func (e *candidateEngine) Start(ctx context.Context, id string) error {
	if e.cancelOnStart != nil {
		e.cancelOnStart()
		return ctx.Err()
	}
	return e.runtimeEngine.Start(ctx, id)
}
func TestCandidateCancellationAfterCreateCleansOwnedResources(t *testing.T) {
	m, _, c := managerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &candidateEngine{cancelOnStart: cancel}
	m.eng = e
	_, err := m.TestSaved(ctx, c.ID, Input{Name: "candidate", Country: "Netherlands", Provider: "nordvpn"})
	if err == nil {
		t.Fatal("canceled candidate succeeded")
	}
	if len(e.specs) != 1 || len(e.removed) != 1 || len(e.containers) != 0 || len(e.netCreated) != 1 || len(e.netRemoved) != 1 {
		t.Fatalf("incomplete cancellation cleanup: created=%d removed=%v remaining=%v nets=%v/%v", len(e.specs), e.removed, e.containers, e.netCreated, e.netRemoved)
	}
}
func TestCandidateCancellationAlwaysCleansPrivateFilesAndOwnedResources(t *testing.T) {
	m, _, c := managerFixture(t)
	e := &candidateEngine{}
	m.eng = e
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := m.TestSaved(ctx, c.ID, Input{Name: "candidate", Country: "Netherlands", Provider: "nordvpn"})
	if err == nil {
		t.Fatal("canceled test succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation ignored")
	}
	if len(e.netCreated) != len(e.netRemoved) || len(e.containers) != 0 {
		t.Fatalf("leaked resources: %+v", e)
	}
	entries, err := os.ReadDir(m.stateDir + "/vpn/tests")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("candidate private files leaked")
	}
}
func TestCandidateInputUsesSavedCredentialsOnlyWhenOmitted(t *testing.T) {
	m, _, c := managerFixture(t)
	u := "candidate-user"
	in, err := m.candidateInput(c.ID, Input{Name: "edit", Provider: "nordvpn", Country: "Germany", Username: &u})
	if err != nil {
		t.Fatal(err)
	}
	if in.Username == nil || *in.Username != u || in.Password == nil || *in.Password != "service-password" {
		t.Fatal("omitted credential not retained")
	}
	if _, err = m.candidateInput(strings.Repeat("a", 32), Input{}); err == nil {
		t.Fatal("missing saved connection accepted")
	}
}

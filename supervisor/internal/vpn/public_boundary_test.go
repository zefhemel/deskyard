package vpn

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Discovery may race a same-name replacement; removal must use the checked ID.
type replacedCandidateNetwork struct{ candidateEngine }

func (e *replacedCandidateNetwork) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	return []engine.Network{{ID: "original-id", Name: "candidate", Labels: map[string]string{labelAttempt: "owned"}}}, nil
}
func (e *replacedCandidateNetwork) RemoveNetwork(_ context.Context, id string) error {
	delete(e.networks, id)
	return nil
}
func TestCandidateNetworkCleanupKeepsSameNameReplacement(t *testing.T) {
	m, _, _ := managerFixture(t)
	e := &replacedCandidateNetwork{candidateEngine: candidateEngine{networks: map[string]engine.Network{"candidate": {ID: "replacement-id", Name: "candidate"}, "original-id": {ID: "original-id"}}}}
	m.eng = e
	if err := m.removeCandidateNetwork(context.Background(), "candidate", map[string]string{labelAttempt: "owned"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.networks["candidate"]; !ok {
		t.Fatal("cleanup removed same-name replacement instead of the checked network ID")
	}
	if _, ok := e.networks["original-id"]; ok {
		t.Fatal("checked network was not removed")
	}
}
func TestEnsureRedactsStoreFilesystemFailure(t *testing.T) {
	m, _, c := managerFixture(t)
	sentinel := filepath.Join(t.TempDir(), "private-path-sentinel")
	if err := os.WriteFile(sentinel, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	m.store.metadata = filepath.Join(sentinel, "networks.json") // Get now fails with ENOTDIR and the private path.
	_, err := m.Ensure(context.Background(), strings.Repeat("a", 32), "desktop", c.ID, nil)
	if err == nil {
		t.Fatal("unreadable store accepted")
	}
	if strings.Contains(err.Error(), sentinel) || strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("private store cause escaped Manager: %v", err)
	}
}

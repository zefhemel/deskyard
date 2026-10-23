package vpn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const CandidateTimeout = 2 * time.Minute

func (m *Manager) candidateInput(connectionID string, in Input) (Input, error) {
	if connectionID == "" {
		return in, nil
	}
	files, err := m.store.SecretFiles(connectionID)
	if err != nil {
		return Input{}, errors.New("saved VPN credentials are unavailable")
	}
	for _, field := range []struct {
		value **string
		path  string
	}{{&in.Username, files.Username}, {&in.Password, files.Password}} {
		if *field.value == nil {
			b, e := os.ReadFile(field.path)
			if e != nil {
				return Input{}, errors.New("saved VPN credentials are unavailable")
			}
			value := string(b)
			*field.value = &value
		}
	}
	return in, nil
}
func (m *Manager) Test(ctx context.Context, in Input) (Status, error) { return m.test(ctx, "", in) }
func (m *Manager) TestSaved(ctx context.Context, connectionID string, in Input) (Status, error) {
	return m.test(ctx, connectionID, in)
}
func (m *Manager) test(ctx context.Context, connectionID string, in Input) (result Status, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, CandidateTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return Status{}, errors.New("VPN test canceled")
	}
	in, err := m.candidateInput(connectionID, in)
	if err != nil {
		return Status{}, err
	}
	root := filepath.Join(m.stateDir, "vpn", "tests")
	if err = os.MkdirAll(root, 0o700); err != nil {
		return Status{}, errors.New("VPN test private directory is unavailable")
	}
	dir, err := os.MkdirTemp(root, "candidate-")
	if err != nil {
		return Status{}, errors.New("VPN test private directory is unavailable")
	}
	defer os.RemoveAll(dir)
	candidateStore, err := NewStore(dir)
	if err != nil {
		return Status{}, errors.New("VPN test store could not be prepared")
	}
	connection, err := candidateStore.Create(in)
	if err != nil {
		return Status{}, err
	}
	bytes := make([]byte, 16)
	if _, err = rand.Read(bytes); err != nil {
		return Status{}, errors.New("VPN test identity could not be generated")
	}
	id := hex.EncodeToString(bytes)
	network := "deskyard-vpn-test-" + id
	manager := NewManager(m.eng, candidateStore, dir)
	if err = m.eng.CreateNetwork(ctx, network, "", map[string]string{LabelRole: "test", LabelDesktopID: id}); err != nil {
		return Status{}, errors.New("VPN test network creation failed")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		runtimeErr := manager.Remove(cleanup, id)
		networkErr := m.eng.RemoveNetwork(cleanup, network)
		if runtimeErr != nil || networkErr != nil {
			result = Status{}
			resultErr = errors.New("VPN test resource cleanup failed")
		}
	}()
	if _, err = manager.Ensure(ctx, id, "candidate", network, connection.ID, nil, false); err != nil {
		return Status{}, err
	}
	ticker := time.NewTicker(HealthInterval)
	defer ticker.Stop()
	for {
		status, e := manager.Status(ctx, id)
		if e != nil {
			return Status{}, e
		}
		if status.State == "connected" {
			return status, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return Status{}, errors.New("VPN test canceled")
			}
			return Status{}, errors.New("VPN test did not establish verified egress within two minutes; check NordVPN service credentials (not account login), selected country/server availability and host internet access")
		case <-ticker.C:
		}
	}
}

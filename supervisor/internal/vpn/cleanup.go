package vpn

import (
	"context"
	"errors"
	"os"
)

const labelAttempt = "deskyard.vpn.attempt"

func ownsLabels(actual, expected map[string]string) bool {
	for k, v := range expected {
		if actual[k] != v {
			return false
		}
	}
	return true
}

// These helpers deliberately do not acquire the computer lock: Ensure already
// holds it. A unique attempt prevents ambiguous creation cleanup from deleting
// a pre-existing resource that merely collided on its stable name.
func (m *Manager) removeRuntimeAttempt(ctx context.Context, computer, name, attempt string) error {
	labels := m.runtimeLabels(computer)
	labels[labelAttempt] = attempt
	containers, err := m.eng.List(ctx, labels)
	if err != nil {
		return errors.New("VPN runtime cleanup discovery failed")
	}
	for _, c := range containers {
		if c.Name == name && ownsLabels(c.Labels, labels) {
			if err = m.eng.Remove(ctx, c.ID); err != nil {
				return errors.New("VPN runtime cleanup removal failed")
			}
		}
	}
	if err = os.RemoveAll(m.directory(computer)); err != nil {
		return errors.New("VPN runtime private file cleanup failed")
	}
	return nil
}
func (m *Manager) removeCandidateNetwork(ctx context.Context, name string, labels map[string]string) error {
	networks, err := m.eng.Networks(ctx, labels)
	if err != nil {
		return errors.New("VPN test network cleanup discovery failed")
	}
	for _, n := range networks {
		if n.Name == name && ownsLabels(n.Labels, labels) {
			if err = m.eng.RemoveNetwork(ctx, n.ID); err != nil {
				return errors.New("VPN test network cleanup removal failed")
			}
		}
	}
	return nil
}

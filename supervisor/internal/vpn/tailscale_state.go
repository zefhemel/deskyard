package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var dnsNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$`)

type TailscaleStatus struct {
	BackendState string          `json:"backendState"`
	NodeID       string          `json:"nodeId,omitempty"`
	DNSName      string          `json:"dnsName,omitempty"`
	Addresses    []string        `json:"addresses,omitempty"`
	Services     []ServiceStatus `json:"services,omitempty"`
}
type tailscaleDaemonStatus struct {
	BackendState string
	TailscaleIPs []string
	CertDomains  []string
	Self         struct {
		ID, DNSName  string
		Online       bool
		CapMap       map[string][]json.RawMessage
		Capabilities []string
	}
	CurrentTailnet struct{ MagicDNSSuffix string }
}

func (v tailscaleDaemonStatus) publicStatus() Status {
	state, reason := "connecting", "Tailscale is connecting"
	switch v.BackendState {
	case "Running":
		state, reason = "connected", ""
	case "NeedsLogin":
		state, reason = "blocked", "Tailscale authentication is required; disconnect and re-enroll if the device was revoked"
	case "NeedsMachineAuth":
		state, reason = "connecting", "Approve this device in the Tailscale admin console"
	case "Stopped":
		state, reason = "stopped", "Tailscale is stopped"
	}
	dns := strings.TrimSuffix(v.Self.DNSName, ".")
	if !dnsNameRE.MatchString(dns) {
		dns = ""
	}
	return Status{State: state, Reason: reason, ChangedAt: time.Now().UTC(), Tailscale: &TailscaleStatus{BackendState: v.BackendState, NodeID: v.Self.ID, DNSName: dns, Addresses: v.TailscaleIPs}}
}

type tailscaleNodeIdentity struct {
	NodeID            string `json:"nodeId"`
	Retiring          bool   `json:"retiring,omitempty"`
	RetirementID      string `json:"retirementId,omitempty"`
	EnrollmentStarted bool   `json:"enrollmentStarted,omitempty"`
}

func readNodeIdentity(dir string) (tailscaleNodeIdentity, error) {
	var v tailscaleNodeIdentity
	err := store.ReadJSON(filepath.Join(dir, "identity.json"), &v)
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, errors.New("Tailscale node identity unavailable")
	}
	return v, nil
}
func (m *Manager) rememberTailscaleNode(name, id string, status tailscaleDaemonStatus) error {
	dir := m.tailscaleNodeDir(name, id)
	old, err := readNodeIdentity(dir)
	if err != nil {
		return err
	}
	if old.Retiring {
		return errors.New("Tailscale device retirement is pending")
	}
	if old.NodeID != "" && old.NodeID != status.Self.ID {
		return errors.New("Tailscale device identity changed")
	}
	if old.NodeID == status.Self.ID {
		return nil
	}
	if err = store.WriteJSON(filepath.Join(dir, "identity.json"), tailscaleNodeIdentity{NodeID: status.Self.ID}); err != nil {
		return errors.New("Tailscale node identity could not be saved")
	}
	return nil
}
func (m *Manager) tailscaleStatus(ctx context.Context, name, id, connectionID string) (Status, error) {
	b, err := m.tsCommand(ctx, id, "status", "--json")
	if err != nil {
		return Status{State: "blocked", Reason: "Tailscale status unavailable", Tailscale: &TailscaleStatus{}}, nil
	}
	var v tailscaleDaemonStatus
	if json.Unmarshal(b, &v) != nil {
		return Status{}, errors.New("Tailscale status response invalid")
	}
	if v.Self.ID != "" {
		if err = m.rememberTailscaleNode(name, connectionID, v); err != nil {
			return Status{}, err
		}
	}
	status := v.publicStatus()
	if err = m.writeTailscaleDNS(name, v.CurrentTailnet.MagicDNSSuffix); err != nil {
		return Status{}, err
	}
	status.Tailscale.Services = m.serviceStatuses(ctx, name, id, status.Tailscale.DNSName)
	return status, nil
}

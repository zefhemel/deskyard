package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"net/http"
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
	NodeID   string `json:"nodeId"`
	Retiring bool   `json:"retiring,omitempty"`
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

type tailscaleRetirement struct{ NodeID, ClientID, Secret, Tailnet string }

// Retire queues an exact-ID deletion before local state goes away. The private
// queue holds the credential needed if the saved connection is then deleted.
// API outage does not block local computer removal; startup/poll retries it.
func (m *Manager) Retire(ctx context.Context, name, connectionID string) error {
	if !validRuntime(name) || (connectionID != "" && !validID(connectionID)) {
		return invalid("tailscale", "invalid retirement identity")
	}
	root := filepath.Dir(m.tailscaleNodeDir(name, strings.Repeat("a", 32)))
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("Tailscale node state unavailable")
	}
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || !validID(id) || (connectionID != "" && id != connectionID) {
			continue
		}
		dir := filepath.Join(root, id)
		identity, e := readNodeIdentity(dir)
		if e != nil {
			return e
		}
		if identity.NodeID == "" {
			if _, e = os.Stat(filepath.Join(dir, "tailscaled.state")); e == nil && !m.candidate {
				return errors.New("Tailscale device identity needs reconciliation before retirement")
			}
			if e = os.RemoveAll(dir); e != nil {
				return errors.New("Tailscale node cleanup failed")
			}
			continue
		}
		client, e := m.store.tailscaleClient(id)
		if e != nil {
			return e
		}
		record := tailscaleRetirement{NodeID: identity.NodeID, ClientID: client.clientID, Secret: client.secret, Tailnet: client.tailnet}
		queue := m.retirementQueue()
		if e = os.MkdirAll(queue, 0700); e != nil {
			return errors.New("Tailscale retirement queue unavailable")
		}
		key, e := newID()
		if e != nil {
			return errors.New("Tailscale retirement identity unavailable")
		}
		file := filepath.Join(queue, key+".json")
		if e = store.WriteJSON(file, record); e != nil {
			return errors.New("Tailscale retirement could not be saved")
		}
		identity.Retiring = true
		if e = store.WriteJSON(filepath.Join(dir, "identity.json"), identity); e != nil {
			return errors.New("Tailscale retirement could not be saved")
		}
		if e = os.RemoveAll(dir); e != nil {
			return errors.New("Tailscale node cleanup failed")
		}
	}
	m.RetryRetirements(ctx)
	return nil
}
func (m *Manager) RetryRetirements(ctx context.Context) {
	queue := m.retirementQueue()
	entries, err := os.ReadDir(queue)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		file := filepath.Join(queue, entry.Name())
		var v tailscaleRetirement
		if store.ReadJSON(file, &v) != nil {
			continue
		}
		client := tailscaleClient{baseURL: "https://api.tailscale.com", clientID: v.ClientID, secret: v.Secret, tailnet: v.Tailnet, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
		if client.DeleteDevice(ctx, v.NodeID) == nil {
			_ = os.Remove(file)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// RetireOther is idempotent recovery for switching connections, including a
// crash after the definition changed but before the old device was retired.
func (m *Manager) RetireOther(ctx context.Context, name, keep string) error {
	if !validRuntime(name) || (keep != "" && !validID(keep)) {
		return invalid("tailscale", "invalid retirement identity")
	}
	root := filepath.Dir(m.tailscaleNodeDir(name, strings.Repeat("a", 32)))
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("Tailscale identity inventory unavailable")
	}
	for _, entry := range entries {
		if entry.IsDir() && validID(entry.Name()) && entry.Name() != keep {
			if err = m.Retire(ctx, name, entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) retirementQueue() string {
	if m.retirementRoot != "" {
		return m.retirementRoot
	}
	return filepath.Join(m.stateDir, "tailscale", "retirements")
}
func (m *Manager) PendingRetirements() int {
	entries, _ := os.ReadDir(m.retirementQueue())
	return len(entries)
}

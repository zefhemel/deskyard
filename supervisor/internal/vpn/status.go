package vpn

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

const HealthInterval = 5 * time.Second

type Status struct {
	State            string    `json:"state"`
	Reason           string    `json:"reason,omitempty"`
	ChangedAt        time.Time `json:"changedAt"`
	CurrentExitIP    string    `json:"currentExitIp,omitempty"`
	LastExitIP       string    `json:"lastExitIp,omitempty"`
	ExitIPObservedAt time.Time `json:"exitIpObservedAt,omitempty"`
}
type observation struct {
	Status
	CheckedAt     time.Time
	EverConnected bool
}

// Status observes at most once per five seconds across all viewers. Callers
// can poll it on HealthInterval and publish changes on their event stream.
func (m *Manager) Status(ctx context.Context, computer string) (Status, error) {
	if !validRuntime(computer) {
		return Status{}, invalid("computer", "invalid identity")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	unlock, lockErr := m.lockComputer(checkCtx, computer)
	if lockErr != nil {
		return Status{}, lockErr
	}
	defer unlock()
	m.mu.Lock()
	if m.observations == nil {
		m.observations = map[string]*observation{}
	}
	o := m.observations[computer]
	if o == nil {
		o = &observation{}
		m.observations[computer] = o
	}
	m.mu.Unlock()
	now := time.Now().UTC()
	if now.Sub(o.CheckedAt) < HealthInterval {
		return o.Status, nil
	}
	next := o.Status
	next.CurrentExitIP = ""
	next.State = "blocked"
	next.Reason = "VPN runtime is unavailable"
	cs, err := m.eng.List(checkCtx, m.runtimeLabels(computer))
	if err != nil {
		next.Reason = "VPN runtime discovery failed"
	}
	if err == nil && len(cs) == 1 && cs[0].State == "running" {
		verify, readErr := os.ReadFile(filepath.Join(m.directory(computer), "verify.sh"))
		if readErr == nil && m.probe(checkCtx, cs[0].ID, string(verify)+" && ("+controlProbe+")") == nil {
			next.State = "connecting"
			if o.EverConnected {
				next.State = "reconnecting"
			}
			next.Reason = "VPN tunnel is unavailable"
			if cs[0].Health == "healthy" && m.probe(checkCtx, cs[0].ID, "wget -qO- -T 2 http://127.0.0.1:9999/ >/dev/null") == nil {
				// The control API's cached public IP is not proof of current egress. A
				// fresh, bounded HTTPS request must traverse the enforced tunnel policy.
				output, _, code, probeErr := engine.Output(checkCtx, m.eng, cs[0].ID, engine.ExecSpec{User: "root", Cmd: []string{"wget", "-qO-", "-T", "2", "https://api.ipify.org"}})
				ip, parseErr := netip.ParseAddr(strings.TrimSpace(string(output)))
				if probeErr == nil && code == 0 && parseErr == nil && ip.Is4() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
					next.State = "connected"
					next.Reason = ""
					next.CurrentExitIP = ip.String()
					next.LastExitIP = ip.String()
					next.ExitIPObservedAt = now
					o.EverConnected = true
				}
			}
		} else {
			next.Reason = "VPN firewall/control verification failed"
		}
	}
	if next.State != o.State || next.Reason != o.Reason {
		next.ChangedAt = now
	}
	o.Status = next
	o.CheckedAt = now
	return next, nil
}

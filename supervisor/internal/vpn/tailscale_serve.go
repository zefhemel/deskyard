package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"path/filepath"
	"slices"
	"strings"
)

type ServiceRule struct {
	Label      string `json:"label,omitempty"`
	LocalPort  int    `json:"localPort"`
	ListenPort int    `json:"listenPort"`
	Audience   string `json:"audience"`
}
type ServiceStatus struct {
	ServiceRule
	URL    string `json:"url,omitempty"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

var protectedServicePorts = map[int]bool{22: true, 6080: true, 6092: true, 8080: true, 9220: true, 9221: true, 9222: true}

func NormalizeServiceRules(in []ServiceRule) ([]ServiceRule, error) {
	if len(in) > 32 {
		return nil, invalid("services", "at most 32 HTTP services are supported")
	}
	out := slices.Clone(in)
	seen := map[int]bool{}
	for i, r := range out {
		r.Label = strings.TrimSpace(r.Label)
		if len(r.Label) > 100 || strings.ContainsAny(r.Label, "\r\n\x00") {
			return nil, invalid("services.label", "use a label of at most 100 bytes")
		}
		if r.LocalPort < 1 || r.LocalPort > 65535 || protectedServicePorts[r.LocalPort] {
			return nil, invalid("services.localPort", "choose an application port, excluding desktop control ports")
		}
		if r.ListenPort < 1 || r.ListenPort > 65535 || protectedServicePorts[r.ListenPort] || seen[r.ListenPort] {
			return nil, invalid("services.listenPort", "choose a unique HTTPS listener excluding desktop control ports")
		}
		if r.Audience != "private" && r.Audience != "public" {
			return nil, invalid("services.audience", "choose private Serve or public Funnel")
		}
		if r.Audience == "public" && r.ListenPort != 443 && r.ListenPort != 8443 && r.ListenPort != 10000 {
			return nil, invalid("services.listenPort", "Funnel uses 443, 8443 or 10000")
		}
		seen[r.ListenPort] = true
		out[i] = r
	}
	slices.SortFunc(out, func(a, b ServiceRule) int { return a.ListenPort - b.ListenPort })
	return out, nil
}

type serveTCP struct {
	HTTPS bool `json:",omitempty"`
}
type serveHandler struct{ Proxy string }
type serveWeb struct{ Handlers map[string]serveHandler }
type serveConfig struct {
	TCP         map[string]serveTCP        `json:",omitempty"`
	Web         map[string]serveWeb        `json:",omitempty"`
	AllowFunnel map[string]bool            `json:",omitempty"`
	Services    map[string]json.RawMessage `json:",omitempty"`
	Foreground  map[string]json.RawMessage `json:",omitempty"`
}

func buildServeConfig(dns string, in []ServiceRule) (serveConfig, error) {
	rules, err := NormalizeServiceRules(in)
	if err != nil {
		return serveConfig{}, err
	}
	if len(rules) == 0 {
		return serveConfig{}, nil
	}
	if !dnsNameRE.MatchString(dns) || !strings.HasSuffix(dns, ".ts.net") || strings.Contains(dns, "..") {
		return serveConfig{}, errors.New("Tailscale DNS name is not available")
	}
	out := serveConfig{TCP: map[string]serveTCP{}, Web: map[string]serveWeb{}, AllowFunnel: map[string]bool{}}
	for _, r := range rules {
		port := fmt.Sprint(r.ListenPort)
		hp := dns + ":" + port
		out.TCP[port] = serveTCP{HTTPS: true}
		out.Web[hp] = serveWeb{Handlers: map[string]serveHandler{"/": {Proxy: fmt.Sprintf("http://127.0.0.1:%d", r.LocalPort)}}}
		if r.Audience == "public" {
			out.AllowFunnel[hp] = true
		}
	}
	return out, nil
}
func sameServeConfig(a, b serveConfig) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}
func (m *Manager) observedServe(ctx context.Context, id string) (serveConfig, error) {
	b, err := m.tsCommand(ctx, id, "serve", "status", "--json")
	if err != nil {
		return serveConfig{}, err
	}
	var v serveConfig
	if json.Unmarshal(b, &v) != nil {
		return v, errors.New("Tailscale service status is invalid")
	}
	return v, nil
}
func (m *Manager) setServe(ctx context.Context, id string, c serveConfig) error {
	b, _ := json.Marshal(c)
	_, err := m.tsCommand(ctx, id, "debug", "localapi", "POST", "/localapi/v0/serve-config", string(b))
	return err
}
func (m *Manager) ApplyServices(ctx context.Context, name string, in []ServiceRule) ([]ServiceStatus, error) {
	if !validRuntime(name) {
		return nil, invalid("environment", "invalid identity")
	}
	rules, err := NormalizeServiceRules(in)
	if err != nil {
		return nil, err
	}
	unlock, err := m.lockComputer(ctx, name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	cs, err := m.eng.List(ctx, m.runtimeLabels(name))
	if err != nil || len(cs) != 1 || cs[0].Labels[labelProvider] != "tailscale" || !cs[0].Running() {
		return nil, errors.New("Tailscale sidecar is not running")
	}
	id := cs[0].ID
	if err = store.WriteJSON(filepath.Join(m.directory(name), "services.json"), rules); err != nil {
		return nil, errors.New("Tailscale service intent could not be saved")
	}
	b, err := m.tsCommand(ctx, id, "status", "--json")
	if err != nil {
		return nil, err
	}
	var status tailscaleDaemonStatus
	if json.Unmarshal(b, &status) != nil {
		return nil, errors.New("Tailscale device status invalid")
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	desired, err := buildServeConfig(dns, rules)
	// Withdraw first even if prerequisites for the replacement are not met.
	observed, observeErr := m.observedServe(ctx, id)
	if err == nil && observeErr == nil && sameServeConfig(observed, desired) {
		return m.serviceStatuses(ctx, name, id, dns), nil
	}
	if observeErr != nil || !sameServeConfig(observed, serveConfig{}) {
		if e := m.setServe(ctx, id, serveConfig{}); e != nil {
			_ = m.eng.Stop(context.WithoutCancel(ctx), id, 5e9)
			return nil, errors.New("Could not withdraw Tailscale services; sidecar stopped for recovery")
		}
	}
	if err != nil {
		return nil, err
	}
	if len(rules) > 0 {
		if status.BackendState != "Running" {
			return nil, errors.New("Tailscale must connect before publishing HTTP services")
		}
		if !slices.Contains(status.CertDomains, dns) {
			return nil, errors.New("Enable MagicDNS and HTTPS certificates in the Tailscale admin console")
		}
		for _, r := range rules {
			if r.Audience == "public" {
				_, allowed := status.Self.CapMap["funnel"]
				if !allowed && !slices.Contains(status.Self.Capabilities, "funnel") {
					return nil, errors.New("Allow Funnel for this device tag in the Tailscale access policy")
				}
			}
		}
		if err = m.setServe(ctx, id, desired); err != nil {
			return nil, errors.New("Tailscale HTTP configuration failed; check HTTPS and Funnel permissions")
		}
	}
	current, err := m.observedServe(ctx, id)
	if err != nil || !sameServeConfig(current, desired) {
		return nil, errors.New("Tailscale HTTP configuration has not been confirmed")
	}
	return m.serviceStatuses(ctx, name, id, dns), nil
}
func (m *Manager) serviceStatuses(ctx context.Context, name, id, dns string) []ServiceStatus {
	var rules []ServiceRule
	if store.ReadJSON(filepath.Join(m.directory(name), "services.json"), &rules) != nil {
		return nil
	}
	current, err := m.observedServe(ctx, id)
	out := make([]ServiceStatus, 0, len(rules))
	for _, r := range rules {
		v := ServiceStatus{ServiceRule: r, State: "pending", Reason: "Waiting for Tailscale HTTP configuration"}
		want, e := buildServeConfig(dns, []ServiceRule{r})
		if err == nil && e == nil {
			hp := fmt.Sprintf("%s:%d", dns, r.ListenPort)
			web, exists := current.Web[hp]
			tcp := current.TCP[fmt.Sprint(r.ListenPort)]
			if exists && tcp.HTTPS && web.Handlers["/"].Proxy == want.Web[hp].Handlers["/"].Proxy && current.AllowFunnel[hp] == (r.Audience == "public") {
				v.State = "applied"
				v.Reason = ""
				v.URL = "https://" + dns
				if r.ListenPort != 443 {
					v.URL += fmt.Sprint(":", r.ListenPort)
				}
				v.URL += "/"
			}
		}
		out = append(out, v)
	}
	return out
}

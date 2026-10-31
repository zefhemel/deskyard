package vpn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Official Tailscale 1.104.4 multi-architecture manifest (amd64 and arm64).
const TailscaleImage = "tailscale/tailscale@sha256:ed6480b33c6eb67c9cbd203ed42b9fb57e361986725b6856def71b7a3d9e276c"
const tailscaleSocket = "/var/run/tailscale/tailscaled.sock"
const labelProvider = "deskyard.vpn.provider"

func (m *Manager) tailscaleNodeDir(name, id string) string {
	root := filepath.Join(filepath.Dir(m.stateDir), "secret")
	if m.store != nil {
		root = filepath.Dir(m.store.secrets)
	}
	return filepath.Join(root, "tailscale", "nodes", name, id)
}
func (m *Manager) tailscaleSpec(name, network string, c Connection, exceptions []Exception, reserved []netip.Prefix) (engine.ContainerSpec, error) {
	if !validRuntime(name) || !validID(c.ID) || c.Tailscale == nil {
		return engine.ContainerSpec{}, invalid("tailscale", "invalid runtime identity")
	}
	policy, err := tailscalePolicy(exceptions, reserved)
	if err != nil {
		return engine.ContainerSpec{}, err
	}
	labels := m.runtimeLabels(name)
	labels[LabelConnection] = c.ID
	labels[labelProvider] = "tailscale"
	spec := engine.ContainerSpec{Name: runtimeName(name), Image: TailscaleImage, Network: network, Restart: "no", CapAdd: []string{"NET_ADMIN"}, Devices: []string{"/dev/net/tun"}, Labels: labels,
		Env:        map[string]string{"TS_DEBUG_FIREWALL_MODE": "iptables"},
		Mounts:     []engine.Mount{{Source: m.tailscaleNodeDir(name, c.ID), Target: "/var/lib/tailscale"}, {Source: m.directory(name), Target: "/run/deskyard", ReadOnly: true}},
		Entrypoint: []string{"/bin/sh", "-c"}, Cmd: []string{"/bin/sh /run/deskyard/policy.sh && exec tailscaled --state=/var/lib/tailscale/tailscaled.state --socket=" + tailscaleSocket + " --tun=tailscale0 --port=41641 --no-logs-no-support"}}
	identity, _ := json.Marshal(struct {
		Spec   engine.ContainerSpec
		Policy string
	}{spec, policy})
	sum := sha256.Sum256(identity)
	spec.Labels[labelPolicy] = hex.EncodeToString(sum[:])
	spec.Labels[LabelRevision] = strconv.FormatUint(c.Revision, 10)
	return spec, nil
}
func (m *Manager) tsCommand(ctx context.Context, id string, args ...string) ([]byte, error) {
	cmd := append([]string{"tailscale", "--socket=" + tailscaleSocket}, args...)
	result, err := engine.Run(ctx, m.eng, id, engine.ExecSpec{User: "root", Cmd: cmd}, engine.RunOptions{StdoutLimit: 1 << 20, StderrLimit: 4096})
	if err != nil || result.Code != 0 {
		return nil, errors.New("Tailscale command failed; check node status, permissions and tailnet settings")
	}
	return result.Stdout, nil
}
func (m *Manager) ensureTailscale(ctx context.Context, name, network string, c Connection, exceptions []Exception) (engine.Container, error) {
	nets, err := m.eng.Networks(ctx, nil)
	if err != nil {
		return engine.Container{}, errors.New("Tailscale networks unavailable")
	}
	m.mu.Lock()
	reserved := append([]netip.Prefix{}, m.reserved...)
	m.mu.Unlock()
	var discovered []netip.Prefix
	found := false
	for _, n := range nets {
		if n.Name == network {
			found = true
		}
		if n.Subnet != "" {
			p, e := netip.ParsePrefix(n.Subnet)
			if e != nil {
				return engine.Container{}, errors.New("Tailscale network inventory invalid")
			}
			discovered = append(discovered, p)
		}
	}
	if !found {
		return engine.Container{}, errors.New("Tailscale computer network unavailable")
	}
	reserved, err = effectiveNetworkReservations(reserved, discovered, exceptions)
	if err != nil {
		return engine.Container{}, err
	}
	spec, err := m.tailscaleSpec(name, network, c, exceptions, reserved)
	if err != nil {
		return engine.Container{}, err
	}
	nodeDir := m.tailscaleNodeDir(name, c.ID)
	saved, e := readNodeIdentity(nodeDir)
	if e != nil {
		return engine.Container{}, e
	}
	if saved.Retiring {
		return engine.Container{}, errors.New("Tailscale device retirement is pending")
	}
	if saved.NodeID != "" {
		if _, e = os.Stat(filepath.Join(nodeDir, "tailscaled.state")); e != nil {
			return engine.Container{}, errors.New("Tailscale node state is missing; disconnect and re-enroll explicitly")
		}
	}
	if err = os.MkdirAll(nodeDir, 0700); err != nil {
		return engine.Container{}, errors.New("Tailscale node storage unavailable")
	}
	if _, e := os.Stat(filepath.Join(nodeDir, "identity.json")); errors.Is(e, os.ErrNotExist) {
		if store.WriteJSON(filepath.Join(nodeDir, "identity.json"), saved) != nil {
			return engine.Container{}, errors.New("Tailscale node identity could not be initialized")
		}
	}
	if err = os.MkdirAll(m.directory(name), 0700); err != nil {
		return engine.Container{}, errors.New("Tailscale runtime storage unavailable")
	}
	policy, _ := tailscalePolicy(exceptions, reserved)
	if err = store.WriteFile(filepath.Join(m.directory(name), "policy.sh"), []byte(policy)); err != nil {
		return engine.Container{}, errors.New("Tailscale policy could not be stored")
	}
	if err = m.writeTailscaleDNS(name, ""); err != nil {
		return engine.Container{}, err
	}
	cs, err := m.eng.List(ctx, m.runtimeLabels(name))
	if err != nil {
		return engine.Container{}, errors.New("Tailscale runtime discovery failed")
	}
	if len(cs) > 1 {
		return engine.Container{}, fmt.Errorf("%w: multiple Tailscale runtimes", ErrConflict)
	}
	var id string
	if len(cs) == 1 {
		if cs[0].Labels[labelPolicy] != spec.Labels[labelPolicy] {
			return engine.Container{}, fmt.Errorf("%w: Tailscale runtime requires coordinated replacement", ErrConflict)
		}
		id = cs[0].ID
		if !cs[0].Running() {
			if err = m.withdrawSavedServe(ctx, nodeDir); err != nil {
				return engine.Container{}, err
			}
			if err = m.eng.Start(ctx, id); err != nil {
				return engine.Container{}, errors.New("Tailscale runtime start failed")
			}
		}
	} else {
		if _, err = m.eng.ImageInspect(ctx, TailscaleImage); errors.Is(err, engine.ErrNotFound) {
			err = m.eng.Pull(ctx, TailscaleImage, nil)
		}
		if err != nil {
			return engine.Container{}, errors.New("Tailscale pinned image unavailable")
		}
		attempt, e := newID()
		if e != nil {
			return engine.Container{}, errors.New("Tailscale runtime identity unavailable")
		}
		spec.Labels[labelAttempt] = attempt
		if err = m.withdrawSavedServe(ctx, nodeDir); err != nil {
			return engine.Container{}, err
		}
		id, err = m.eng.Create(ctx, spec)
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = m.removeRuntimeAttempt(cleanup, name, spec.Name, attempt)
			return engine.Container{}, errors.New("Tailscale runtime creation failed")
		}
		if err = m.eng.Start(ctx, id); err != nil {
			return engine.Container{}, errors.New("Tailscale runtime start failed")
		}
	}
	var status tailscaleDaemonStatus
	for {
		b, e := m.tsCommand(ctx, id, "status", "--json")
		if e == nil && json.Unmarshal(b, &status) == nil {
			break
		}
		select {
		case <-ctx.Done():
			return engine.Container{}, errors.New("Tailscale daemon did not become ready")
		case <-time.After(100 * time.Millisecond):
		}
	}
	// Existing state must never be silently replaced after revocation or loss.
	identity, err := readNodeIdentity(nodeDir)
	if err != nil {
		return engine.Container{}, err
	}
	if status.BackendState == "NeedsLogin" && identity.NodeID == "" {
		keyPath := filepath.Join(m.directory(name), "enroll.key")
		if _, err = os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
			client, e := m.store.tailscaleClient(c.ID)
			if e != nil {
				return engine.Container{}, e
			}
			key, e := client.enrollKey(ctx, c.Tailscale.Tags, m.candidate)
			if e != nil {
				return engine.Container{}, e
			}
			if e = store.WriteFile(keyPath, []byte(key)); e != nil {
				return engine.Container{}, errors.New("Tailscale enrollment material unavailable")
			}
		}
		hostname := name
		if m.instance != "" {
			suffix := m.instance
			if len(suffix) > 8 {
				suffix = suffix[:8]
			}
			hostname += "-" + suffix
		}
		if len(hostname) > 63 {
			hostname = hostname[:63]
		}
		identity.EnrollmentStarted = true
		if store.WriteJSON(filepath.Join(nodeDir, "identity.json"), identity) != nil {
			return engine.Container{}, errors.New("Tailscale enrollment intent could not be saved")
		}
		_, _ = m.tsCommand(ctx, id, "up", "--auth-key=file:/run/deskyard/enroll.key", "--hostname="+hostname, "--advertise-tags="+strings.Join(c.Tailscale.Tags, ","), "--accept-dns=false", "--accept-routes=false", "--netfilter-mode=off", "--ssh=false", "--timeout=10s")
		b, e := m.tsCommand(ctx, id, "status", "--json")
		if e == nil {
			_ = json.Unmarshal(b, &status)
		}
		if status.Self.ID != "" {
			if e = m.rememberTailscaleNode(name, c.ID, status); e != nil {
				return engine.Container{}, e
			}
			_ = os.Remove(keyPath)
		}
	}
	if identity.NodeID != "" && status.Self.ID != "" && identity.NodeID != status.Self.ID {
		return engine.Container{}, errors.New("Tailscale device identity changed; disconnect and re-enroll explicitly")
	}
	if status.Self.ID != "" {
		if err = m.rememberTailscaleNode(name, c.ID, status); err != nil {
			return engine.Container{}, err
		}
	}
	if err = m.writeTailscaleDNS(name, status.CurrentTailnet.MagicDNSSuffix); err != nil {
		return engine.Container{}, err
	}
	return m.eng.Inspect(ctx, id)
}

func (m *Manager) writeTailscaleDNS(name, suffix string) error {
	// Keep the inode: an existing desktop may bind this file. Quad100 resolves
	// tailnet names and public DNS; public fallback keeps host internet usable
	// while the node is awaiting approval or offline.
	value := "nameserver 100.100.100.100\nnameserver 1.1.1.1\noptions timeout:1 attempts:2\n"
	if suffix != "" && dnsNameRE.MatchString(suffix) {
		value += "search " + suffix + "\n"
	}
	p := filepath.Join(m.directory(name), "resolv.conf")
	old, _ := os.ReadFile(p)
	if string(old) == value {
		return nil
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return errors.New("Tailscale DNS configuration unavailable")
	}
	defer f.Close()
	if _, err = f.WriteString(value); err == nil {
		err = f.Sync()
	}
	if err != nil {
		return errors.New("Tailscale DNS configuration could not be saved")
	}
	return nil
}
func (m *Manager) DesktopMounts(name, connectionID string) ([]engine.Mount, error) {
	c, err := m.store.Get(connectionID)
	if err != nil {
		return nil, err
	}
	if c.Provider != "tailscale" {
		return nil, nil
	}
	return []engine.Mount{{Source: filepath.Join(m.directory(name), "resolv.conf"), Target: "/etc/resolv.conf", ReadOnly: true, MustExist: true}}, nil
}
func (m *Manager) Stop(ctx context.Context, name string) error {
	if !validRuntime(name) {
		return invalid("environment", "invalid identity")
	}
	unlock, err := m.lockComputer(ctx, name)
	if err != nil {
		return err
	}
	defer unlock()
	cs, err := m.eng.List(ctx, m.runtimeLabels(name))
	if err != nil {
		return errors.New("VPN runtime discovery failed")
	}
	for _, c := range cs {
		if c.Running() {
			if err = m.eng.Stop(ctx, c.ID, 10*time.Second); err != nil {
				return errors.New("VPN runtime stop failed")
			}
		}
	}
	return nil
}

// Call only while no daemon has this state open. Serve configuration is a
// separate Tailscale state-store key, so withdrawing it preserves node keys.
// Removing persisted exposure before daemon start prevents stale Funnel rules
// becoming active before the coordinator applies current desired intent.
func discardSavedServe(dir string) error {
	path := filepath.Join(dir, "tailscaled.state")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("Tailscale persisted state unavailable")
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(b, &values) != nil {
		return errors.New("Tailscale persisted state is invalid")
	}
	changed := false
	for key := range values {
		if strings.HasPrefix(key, "_serve/") {
			delete(values, key)
			changed = true
		}
	}
	if changed {
		if store.WriteJSON(path, values) != nil {
			return errors.New("Tailscale persisted exposure could not be withdrawn")
		}
	}
	return nil
}

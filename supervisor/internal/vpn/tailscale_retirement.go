package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type tailscaleRetirement struct {
	NodeID, ClientID, Secret, Tailnet, ConnectionID, Computer string
	Discover                                                  bool
}

var retirementMu sync.Mutex

func (m *Manager) retirementQueue() string {
	if m.retirementRoot != "" {
		return m.retirementRoot
	}
	return filepath.Join(m.stateDir, "tailscale", "retirements")
}
func (m *Manager) PendingRetirements() int {
	entries, _ := os.ReadDir(m.retirementQueue())
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

// RetainedReferences protects credentials until durable retirement owns a copy,
// even after the desired definition no longer names the connection.
func (m *Manager) RetainedReferences(ctx context.Context, id string) ([]string, error) {
	if !validID(id) {
		return nil, invalid("connection", "invalid identity")
	}
	root := filepath.Dir(filepath.Dir(m.tailscaleNodeDir("placeholder", id)))
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("Tailscale retained identities could not be inspected")
	}
	names := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || !validRuntime(e.Name()) {
			continue
		}
		if _, err = os.Stat(filepath.Join(root, e.Name(), id)); err == nil {
			names[e.Name()] = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("Tailscale retained identity unavailable")
		}
	}
	labels := map[string]string{LabelRole: runtimeRole, LabelConnection: id, labelProvider: "tailscale"}
	if m.instance != "" {
		labels[LabelInstance] = m.instance
	}
	cs, err := m.eng.List(ctx, labels)
	if err != nil {
		return nil, errors.New("Tailscale runtime references unavailable")
	}
	for _, c := range cs {
		if name := c.Labels[LabelEnvironment]; validRuntime(name) {
			names[name] = true
		}
	}
	out := []string{}
	for name := range names {
		out = append(out, name)
	}
	return out, nil
}

// Retire never discards unknown device keys. Unknown identities are quarantined
// with a credential snapshot, so local removal and fresh enrollment can proceed
// while a bounded background discovery finishes remote deletion.
func (m *Manager) Retire(ctx context.Context, name, connectionID string) error {
	if !validRuntime(name) || (connectionID != "" && !validID(connectionID)) {
		return invalid("tailscale", "invalid retirement identity")
	}
	retirementMu.Lock()
	defer retirementMu.Unlock()
	root := filepath.Dir(m.tailscaleNodeDir(name, strings.Repeat("a", 32)))
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("Tailscale identity inventory unavailable")
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
		_, stateErr := os.Stat(filepath.Join(dir, "tailscaled.state"))
		if identity.NodeID == "" && (errors.Is(stateErr, os.ErrNotExist) || m.candidate || (!identity.EnrollmentStarted && nodeIdentityExists(dir))) {
			if e = os.RemoveAll(dir); e != nil {
				return errors.New("Tailscale node cleanup failed")
			}
			continue
		}
		client, e := m.store.tailscaleClient(id)
		if e != nil {
			return e
		}
		queue := m.retirementQueue()
		if e = os.MkdirAll(queue, 0700); e != nil {
			return errors.New("Tailscale retirement queue unavailable")
		}
		key := identity.RetirementID
		if key == "" {
			key, e = newID()
			if e != nil {
				return errors.New("Tailscale retirement identity unavailable")
			}
			identity.RetirementID = key
			identity.Retiring = true
			if store.WriteJSON(filepath.Join(dir, "identity.json"), identity) != nil {
				return errors.New("Tailscale retirement identity could not be saved")
			}
		}
		record := tailscaleRetirement{NodeID: identity.NodeID, ClientID: client.clientID, Secret: client.secret, Tailnet: client.tailnet, ConnectionID: id, Computer: name, Discover: identity.NodeID == ""}
		file := filepath.Join(queue, key+".json")
		if e = store.WriteJSON(file, record); e != nil {
			return errors.New("Tailscale retirement could not be saved")
		}
		{
			// A normal transition removes the old runtime before this call. If a
			// candidate/caller still has it, stop it before moving its state.
			cs, listErr := m.eng.List(ctx, m.runtimeLabels(name))
			if listErr != nil {
				return errors.New("Tailscale old runtime inventory unavailable")
			}
			for _, c := range cs {
				if c.Labels[LabelConnection] == id && c.Running() {
					if e = m.eng.Stop(ctx, c.ID, 5*time.Second); e != nil {
						return errors.New("Tailscale old runtime could not be stopped")
					}
				}
			}
			parent := filepath.Join(queue, "nodes")
			if e = os.MkdirAll(parent, 0700); e != nil {
				return errors.New("Tailscale quarantine storage unavailable")
			}
			if e = os.Rename(dir, filepath.Join(parent, key)); e != nil {
				return errors.New("Tailscale node quarantine failed")
			}
		}
	}
	return nil
}
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
	for _, e := range entries {
		if e.IsDir() && validID(e.Name()) && e.Name() != keep {
			if err = m.Retire(ctx, name, e.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}
func (m *Manager) RetryRetirements(ctx context.Context) {
	// Candidate and runtime managers in the same supervisor share this queue.
	if !retirementMu.TryLock() {
		return
	}
	defer retirementMu.Unlock()
	queue := m.retirementQueue()
	entries, err := os.ReadDir(queue)
	if err != nil {
		return
	}
	for _, entry := range entries {
		key := strings.TrimSuffix(entry.Name(), ".json")
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || !validID(key) {
			continue
		}
		file := filepath.Join(queue, entry.Name())
		var v tailscaleRetirement
		if store.ReadJSON(file, &v) != nil {
			continue
		}
		nodeDir := filepath.Join(queue, "nodes", key)
		if _, e := os.Stat(nodeDir); errors.Is(e, os.ErrNotExist) && validRuntime(v.Computer) && validID(v.ConnectionID) {
			source := m.tailscaleNodeDir(v.Computer, v.ConnectionID)
			identity, e := readNodeIdentity(source)
			if e == nil && identity.RetirementID == key {
				cs, e := m.eng.List(ctx, m.runtimeLabels(v.Computer))
				if e != nil {
					continue
				}
				busy := false
				for _, c := range cs {
					if c.Labels[LabelConnection] == v.ConnectionID && c.Running() {
						busy = true
					}
				}
				if busy {
					continue
				}
				if os.MkdirAll(filepath.Join(queue, "nodes"), 0700) != nil || os.Rename(source, nodeDir) != nil {
					continue
				}
			}
		}
		if v.Discover {
			nodeID, e := m.discoverRetiredNode(ctx, key)
			if e != nil {
				continue
			}
			v.NodeID = nodeID
			v.Discover = false
			if store.WriteJSON(file, v) != nil {
				continue
			}
		}
		if v.NodeID != "" {
			client := &tailscaleClient{baseURL: "https://api.tailscale.com", clientID: v.ClientID, secret: v.Secret, tailnet: v.Tailnet, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
			// Credential rotation can repair pending deletions while the old
			// connection exists; the snapshot survives deletion of that connection.
			if m.store != nil && v.ConnectionID != "" {
				if current, e := m.store.tailscaleClient(v.ConnectionID); e == nil && current.tailnet == v.Tailnet {
					client = current
				}
			}
			if client.DeleteDevice(ctx, v.NodeID) != nil {
				continue
			}
		}
		statePath := filepath.Join(queue, "nodes", key)
		cleanupErr := os.RemoveAll(statePath)
		if cleanupErr != nil && m.restoreStateOwner(ctx, statePath) == nil {
			cleanupErr = os.RemoveAll(statePath)
		}
		if cleanupErr == nil {
			_ = os.Remove(file)
		}
		if ctx.Err() != nil {
			return
		}
	}
}
func (m *Manager) discoverRetiredNode(ctx context.Context, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	dir := filepath.Join(m.retirementQueue(), "nodes", key)
	if _, err := os.Stat(dir); err != nil {
		return "", errors.New("Tailscale quarantined state unavailable")
	}
	labels := map[string]string{LabelRole: "retirement", labelAttempt: key}
	if m.instance != "" {
		labels[LabelInstance] = m.instance
	}
	// Reap only this exact queue entry's discovery attempt after an interrupted
	// previous process, before mounting the same keys again.
	cs, err := m.eng.List(ctx, labels)
	if err != nil {
		return "", err
	}
	for _, c := range cs {
		if !ownsLabels(c.Labels, labels) {
			return "", errors.New("Tailscale discovery ownership changed")
		}
		if err = m.eng.Remove(ctx, c.ID); err != nil {
			return "", err
		}
	}
	if err = m.withdrawSavedServe(ctx, dir); err != nil {
		return "", err
	}
	spec := engine.ContainerSpec{Name: "deskyard-ts-retire-" + key, Image: TailscaleImage, Network: "bridge", Restart: "no", Labels: labels, Mounts: []engine.Mount{{Source: dir, Target: "/var/lib/tailscale", MustExist: true}}, Entrypoint: []string{"tailscaled"}, Cmd: []string{"--tun=userspace-networking", "--state=/var/lib/tailscale/tailscaled.state", "--socket=" + tailscaleSocket, "--no-logs-no-support"}}
	id, err := m.eng.Create(ctx, spec)
	if err != nil {
		return "", err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = m.eng.Remove(cleanup, id)
	}()
	if err = m.eng.Start(ctx, id); err != nil {
		return "", err
	}
	for {
		b, e := m.tsCommand(ctx, id, "status", "--json")
		var status tailscaleDaemonStatus
		if e == nil && json.Unmarshal(b, &status) == nil {
			if status.Self.ID != "" {
				return status.Self.ID, nil
			}
			if status.BackendState == "NeedsLogin" {
				return "", errors.New("Tailscale device identity discovery requires authentication; state retained")
			}
		}
		select {
		case <-ctx.Done():
			return "", errors.New("Tailscale retirement discovery pending")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func nodeIdentityExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "identity.json"))
	return err == nil
}

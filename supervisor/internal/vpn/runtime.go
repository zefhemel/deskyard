package vpn

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

const SetupTimeout = 2 * time.Minute

// Manager owns gateway containers only. The lifecycle caller holds its desktop
// operation lock, stops/recreates namespace consumers, and never replaces a
// live dependency merely because Ensure reports a conflicting configuration.
type Manager struct {
	eng          engine.Engine
	store        *Store
	stateDir     string
	mu           sync.Mutex
	observations map[string]*observation
	locks        map[string]chan struct{}
	reserved     []netip.Prefix
	setupTimeout time.Duration
}

func NewManager(eng engine.Engine, store *Store, stateDir string) *Manager {
	return &Manager{eng: eng, store: store, stateDir: stateDir, setupTimeout: SetupTimeout}
}

// A slow gateway must not prevent five-second observations of other desktops.
func (m *Manager) lockDesktop(ctx context.Context, id string) (func(), error) {
	m.mu.Lock()
	if m.locks == nil {
		m.locks = map[string]chan struct{}{}
	}
	lock := m.locks[id]
	if lock == nil {
		lock = make(chan struct{}, 1)
		m.locks[id] = lock
	}
	m.mu.Unlock()
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SetReservedNetworks configures additional mandatory destinations. Lifecycle
// must reserve its full desktop subnet pool before recovery/Ensure, including
// future allocations, plus any supervisor/control address prefixes. Engine
// networks are discovered independently on each Ensure. Inputs are copied.
func (m *Manager) SetReservedNetworks(prefixes []netip.Prefix) error {
	copy := make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		if !p.IsValid() {
			return invalid("reservedNetworks", "invalid prefix")
		}
		copy = append(copy, p.Masked())
	}
	m.mu.Lock()
	m.reserved = copy
	m.mu.Unlock()
	return nil
}

func (m *Manager) directory(id string) string {
	return filepath.Join(m.stateDir, "vpn", "runtimes", id)
}
func runtimeLabels(id string) map[string]string {
	return map[string]string{LabelRole: runtimeRole, LabelDesktopID: id}
}

func (m *Manager) Ensure(ctx context.Context, desktopID, name, network, connectionID string, exceptions []Exception, publishLocal bool) (result engine.Container, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, m.setupTimeout)
	defer cancel()
	if !validID(desktopID) {
		return engine.Container{}, invalid("desktopID", "invalid identity")
	}
	unlock, lockErr := m.lockDesktop(ctx, desktopID)
	if lockErr != nil {
		return engine.Container{}, lockErr
	}
	defer unlock()
	if ctx.Err() != nil {
		return engine.Container{}, errors.New("VPN runtime operation canceled")
	}
	connection, err := m.store.Get(connectionID)
	if err != nil {
		return engine.Container{}, err
	}
	credentials, err := m.store.SecretFiles(connectionID)
	if err != nil {
		return engine.Container{}, errors.New("VPN credentials are unavailable")
	}
	current, err := m.store.Get(connectionID)
	if err != nil || current.Revision != connection.Revision {
		return engine.Container{}, fmt.Errorf("%w: VPN connection changed during runtime preparation", ErrConflict)
	}
	networks, err := m.eng.Networks(ctx, nil)
	if err != nil {
		return engine.Container{}, errors.New("VPN reserved networks could not be inspected")
	}
	m.mu.Lock()
	configured := append([]netip.Prefix(nil), m.reserved...)
	m.mu.Unlock()
	var discovered []netip.Prefix
	found := false
	for _, n := range networks {
		if n.Name == network {
			found = true
		}
		if n.Subnet != "" {
			p, e := netip.ParsePrefix(n.Subnet)
			if e != nil {
				return engine.Container{}, errors.New("VPN reserved network is invalid")
			}
			discovered = append(discovered, p)
		}
	}
	if !found {
		return engine.Container{}, errors.New("VPN desktop network is unavailable")
	}
	reserved, err := effectiveNetworkReservations(configured, discovered, exceptions)
	if err != nil {
		return engine.Container{}, err
	}
	config, err := buildRuntime(runtimeInput{DesktopID: desktopID, Name: name, Network: network, Connection: connection, Credentials: credentials, Directory: m.directory(desktopID), Exceptions: exceptions, Reserved: reserved, PublishLocal: publishLocal})
	if err != nil {
		return engine.Container{}, err
	}
	existing, err := m.eng.List(ctx, runtimeLabels(desktopID))
	if err != nil {
		return engine.Container{}, errors.New("VPN runtime discovery failed")
	}
	if len(existing) > 1 {
		return engine.Container{}, fmt.Errorf("%w: multiple VPN runtimes require reconciliation", ErrConflict)
	}
	if len(existing) == 1 {
		c := existing[0]
		if c.Labels[labelPolicy] != config.Spec.Labels[labelPolicy] {
			return engine.Container{}, fmt.Errorf("%w: VPN runtime configuration requires coordinated replacement", ErrConflict)
		}
		if c.State != "running" {
			return engine.Container{}, errors.New("VPN runtime is stopped; coordinated recovery is required")
		}
		if err = m.ready(ctx, c.ID, config.Firewall); err != nil {
			return engine.Container{}, err
		}
		ready, inspectErr := m.eng.Inspect(ctx, c.ID)
		if inspectErr != nil {
			return engine.Container{}, errors.New("VPN ready runtime could not be inspected")
		}
		return ready, nil
	}
	imageCtx, stopPull := context.WithTimeout(ctx, 2*time.Minute)
	_, imageErr := m.eng.ImageInspect(imageCtx, GluetunImage)
	if errors.Is(imageErr, engine.ErrNotFound) {
		imageErr = m.eng.Pull(imageCtx, GluetunImage, nil)
	}
	stopPull()
	if imageErr != nil {
		return engine.Container{}, errors.New("VPN pinned runtime image could not be prepared")
	}
	if err = prepareRuntime(config, m.directory(desktopID)); err != nil {
		return engine.Container{}, errors.New("VPN private runtime configuration could not be prepared")
	}
	attempt, err := newID()
	if err != nil {
		return engine.Container{}, errors.New("VPN runtime creation identity failed")
	}
	config.Spec.Labels[labelAttempt] = attempt
	success := false
	// Arm ownership reconciliation before issuing the mutation: Docker may
	// commit creation and lose the response when its request context is canceled.
	defer func() {
		if !success {
			cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if cleanupErr := m.removeRuntimeAttempt(cleanup, desktopID, config.Spec.Name, attempt); cleanupErr != nil {
				result = engine.Container{}
				resultErr = errors.Join(resultErr, errors.New("VPN runtime cleanup failed; reconciliation is required"))
			}
		}
	}()
	created, err := m.eng.Create(ctx, config.Spec)
	if err != nil {
		return engine.Container{}, errors.New("VPN runtime creation failed")
	}
	if err = m.eng.Start(ctx, created); err != nil {
		return engine.Container{}, errors.New("VPN runtime start failed")
	}
	if err = m.ready(ctx, created, config.Firewall); err != nil {
		return engine.Container{}, err
	}
	c, err := m.eng.Inspect(ctx, created)
	if err != nil {
		return engine.Container{}, errors.New("VPN ready runtime could not be inspected")
	}
	success = true
	return c, nil
}

func (m *Manager) Remove(ctx context.Context, desktopID string) error {
	if !validID(desktopID) {
		return invalid("desktopID", "invalid identity")
	}
	unlock, lockErr := m.lockDesktop(ctx, desktopID)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	cs, err := m.eng.List(ctx, runtimeLabels(desktopID))
	if err != nil {
		return errors.New("VPN runtime discovery failed")
	}
	for _, c := range cs {
		if err = m.eng.Remove(ctx, c.ID); err != nil && !errors.Is(err, engine.ErrNotFound) {
			return errors.New("VPN runtime removal failed")
		}
	}
	m.mu.Lock()
	delete(m.observations, desktopID)
	m.mu.Unlock()
	if err = os.RemoveAll(m.directory(desktopID)); err != nil {
		return errors.New("VPN private runtime cleanup failed")
	}
	return nil
}

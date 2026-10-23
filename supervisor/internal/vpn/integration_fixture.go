//go:build vpnintegration

package vpn

import (
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"net/netip"
	"sync"
)

// FixturePeer exists only in opt-in integration builds. It supplies the existing
// controlled endpoint input; the real Manager still owns all policy, preparation,
// readiness, observations, cleanup and lifecycle behavior.
type FixturePeer struct {
	Endpoint                           netip.Addr
	PublicKey, PrivateKeyFile, Address string
}
type FixtureResolver func(desktopID, network string) (FixturePeer, error)

var fixtureManagers = struct {
	sync.RWMutex
	resolvers map[*Manager]FixtureResolver
}{resolvers: map[*Manager]FixtureResolver{}}

func NewFixtureManager(eng engine.Engine, store *Store, stateDir string, resolve FixtureResolver) (*Manager, func()) {
	m := NewManager(eng, store, stateDir)
	fixtureManagers.Lock()
	fixtureManagers.resolvers[m] = resolve
	fixtureManagers.Unlock()
	return m, func() { fixtureManagers.Lock(); delete(fixtureManagers.resolvers, m); fixtureManagers.Unlock() }
}
func (m *Manager) buildRuntime(in runtimeInput) (runtimeConfig, error) {
	fixtureManagers.RLock()
	resolve := fixtureManagers.resolvers[m]
	fixtureManagers.RUnlock()
	if resolve == nil {
		return buildRuntime(in)
	}
	peer, err := resolve(in.DesktopID, in.Network)
	if err != nil {
		return runtimeConfig{}, err
	}
	in.Fixture = &wireguardFixture{Endpoint: peer.Endpoint, Port: 51820, PublicKey: peer.PublicKey, PrivateKeyFile: peer.PrivateKeyFile, Address: peer.Address}
	cfg, err := buildRuntime(in)
	if err == nil {
		cfg.Spec.Entrypoint = []string{"sh", "-ec", "ip route replace default via " + peer.Endpoint.String() + " dev eth0; exec /gluetun-entrypoint"}
	}
	return cfg, err
}

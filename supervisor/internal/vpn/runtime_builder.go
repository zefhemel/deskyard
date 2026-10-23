//go:build !vpnintegration

package vpn

func (m *Manager) buildRuntime(in runtimeInput) (runtimeConfig, error) {
	return buildRuntime(in)
}

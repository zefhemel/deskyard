package vpn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"path/filepath"
	"strconv"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

const GluetunImage = "qmcgaw/gluetun@sha256:c2ee4191762fca6b6a3c3359a731198a95d9c9af4db32d466e223bf3a84e779b"
const (
	LabelRole       = "deskyard.vpn.role"
	LabelDesktopID  = "deskyard.vpn.desktop-id"
	LabelConnection = "deskyard.vpn.connection-id"
	LabelRevision   = "deskyard.vpn.revision"
	labelPolicy     = "deskyard.vpn.policy"
	runtimeRole     = "gateway"
)

// The fixture-only provider is typed and unexported. User input cannot select
// it or provide arbitrary Gluetun environment variables.
type wireguardFixture struct {
	Endpoint                           netip.Addr
	Port                               uint16
	PublicKey, PrivateKeyFile, Address string
}

// resolverPolicy is an explicit networking input for the later custom-DNS
// design. V1 supports only public resolution through the tunnel.
type resolverPolicy uint8

const publicDNSOverTunnel resolverPolicy = 0

type runtimeInput struct {
	DesktopID, Name, Network, Directory string
	Connection                          Connection
	Credentials                         CredentialFiles
	Exceptions                          []Exception
	Reserved                            []netip.Prefix
	PublishLocal                        bool
	Fixture                             *wireguardFixture
	Resolver                            resolverPolicy
}
type runtimeConfig struct {
	Spec     engine.ContainerSpec
	Firewall firewallPolicy
}

func buildRuntime(in runtimeInput) (runtimeConfig, error) {
	if !validID(in.DesktopID) || in.Network == "" {
		return runtimeConfig{}, invalid("runtime", "desktop identity and network are required")
	}
	if in.Resolver != publicDNSOverTunnel {
		return runtimeConfig{}, invalid("resolverPolicy", "unsupported resolver policy")
	}
	b := bootstrap{Protocol: "udp", Port: 1194}
	if in.Fixture != nil {
		b.Port = in.Fixture.Port
		b.Destination = in.Fixture.Endpoint
	}
	policy, err := buildFirewall(in.Exceptions, in.Reserved, b)
	if err != nil {
		return runtimeConfig{}, err
	}
	spec := engine.ContainerSpec{Name: "deskyard-vpn-" + in.DesktopID, Image: GluetunImage, Network: in.Network, Restart: "no", CapAdd: []string{"NET_ADMIN"}, Devices: []string{"/dev/net/tun"}, Env: map[string]string{
		"VPN_SERVICE_PROVIDER": "nordvpn", "VPN_TYPE": "openvpn", "OPENVPN_PROTOCOL": "udp", "OPENVPN_PROCESS_USER": "root", "SERVER_COUNTRIES": in.Connection.Country,
		"OPENVPN_USER_SECRETFILE": "/run/secrets/openvpn_user", "OPENVPN_PASSWORD_SECRETFILE": "/run/secrets/openvpn_password",
		"HTTP_CONTROL_SERVER_ADDRESS": "127.0.0.1:8000", "HTTP_CONTROL_SERVER_AUTH_CONFIG_FILEPATH": "/gluetun/auth/config.toml",
		"FIREWALL_INPUT_PORTS": "8080", "DNS_SERVER": "on", "DNS_UPSTREAM_RESOLVER_TYPE": "plain", "DNS_UPSTREAM_PLAIN_ADDRESSES": "1.1.1.1:53", "DNS_UPSTREAM_IPV6": "off", "BLOCK_MALICIOUS": "off", "DNS_UPDATE_PERIOD": "0", "VERSION_INFORMATION": "off", "HEALTH_RESTART_VPN": "on",
	}, Labels: map[string]string{LabelRole: runtimeRole, LabelDesktopID: in.DesktopID, LabelConnection: in.Connection.ID, LabelRevision: strconv.FormatUint(in.Connection.Revision, 10)}}
	spec.Mounts = []engine.Mount{{Source: in.Credentials.Username, Target: "/run/secrets/openvpn_user", ReadOnly: true}, {Source: in.Credentials.Password, Target: "/run/secrets/openvpn_password", ReadOnly: true}, {Source: filepath.Join(in.Directory, "auth.toml"), Target: "/gluetun/auth/config.toml", ReadOnly: true}, {Source: filepath.Join(in.Directory, "post-rules.txt"), Target: "/iptables/post-rules.txt", ReadOnly: true}}
	if in.Fixture != nil {
		f := in.Fixture
		spec.Env["VPN_SERVICE_PROVIDER"] = "custom"
		spec.Env["VPN_TYPE"] = "wireguard"
		spec.Env["WIREGUARD_ENDPOINT_IP"] = f.Endpoint.String()
		spec.Env["WIREGUARD_ENDPOINT_PORT"] = strconv.Itoa(int(f.Port))
		spec.Env["WIREGUARD_PUBLIC_KEY"] = f.PublicKey
		spec.Env["WIREGUARD_ADDRESSES"] = f.Address
		spec.Env["WIREGUARD_PRIVATE_KEY_SECRETFILE"] = "/run/secrets/wireguard_private_key"
		spec.Mounts = append(spec.Mounts[2:], engine.Mount{Source: f.PrivateKeyFile, Target: "/run/secrets/wireguard_private_key", ReadOnly: true})
		delete(spec.Env, "SERVER_COUNTRIES")
	}
	if in.PublishLocal {
		port := 0
		spec.PublishLocal = &port
	}
	// Configuration identity includes only
	// versioned mount paths, public metadata and the exact enforced policy.
	encoded, _ := json.Marshal(struct {
		Spec   engine.ContainerSpec
		Policy firewallPolicy
	}{spec, policy})
	sum := sha256.Sum256(encoded)
	spec.Labels[labelPolicy] = hex.EncodeToString(sum[:])
	return runtimeConfig{spec, policy}, nil
}

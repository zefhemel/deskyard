package vpn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

const GluetunImage = "qmcgaw/gluetun@sha256:c2ee4191762fca6b6a3c3359a731198a95d9c9af4db32d466e223bf3a84e779b"
const (
	LabelRole        = "deskyard.vpn.role"
	LabelEnvironment = "deskyard.vpn.environment" // the name of the environment a runtime serves
	LabelConnection  = "deskyard.vpn.connection-id"
	LabelRevision    = "deskyard.vpn.revision"
	// LabelInstance is the supervisor instance owning a runtime, as on environments.
	LabelInstance = "deskyard.instance"
	labelPolicy   = "deskyard.vpn.policy"
	runtimeRole   = "gateway"
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
	Environment, Instance, Network, Directory string
	Connection                                Connection
	Credentials                               CredentialFiles
	Exceptions                                []Exception
	Reserved                                  []netip.Prefix
	Fixture                                   *wireguardFixture
	Resolver                                  resolverPolicy
}
type runtimeConfig struct {
	Spec     engine.ContainerSpec
	Firewall firewallPolicy
}

func buildRuntime(in runtimeInput) (runtimeConfig, error) {
	if !validRuntime(in.Environment) || in.Network == "" {
		return runtimeConfig{}, invalid("runtime", "environment identity and network are required")
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
	spec := engine.ContainerSpec{Name: runtimeName(in.Environment), Image: GluetunImage, Network: in.Network, Restart: "no", CapAdd: []string{"NET_ADMIN"}, Devices: []string{"/dev/net/tun"}, Env: map[string]string{
		"VPN_SERVICE_PROVIDER": "nordvpn", "VPN_TYPE": "openvpn", "OPENVPN_PROTOCOL": "udp", "OPENVPN_PROCESS_USER": "root", "SERVER_COUNTRIES": in.Connection.Country,
		"OPENVPN_USER_SECRETFILE": "/run/secrets/openvpn_user", "OPENVPN_PASSWORD_SECRETFILE": "/run/secrets/openvpn_password",
		"HTTP_CONTROL_SERVER_ADDRESS": "127.0.0.1:8000", "HTTP_CONTROL_SERVER_AUTH_CONFIG_FILEPATH": "/gluetun/auth/config.toml",
		"FIREWALL_INPUT_PORTS": "8080", "DNS_SERVER": "on", "DNS_UPSTREAM_RESOLVER_TYPE": "plain", "DNS_UPSTREAM_PLAIN_ADDRESSES": "1.1.1.1:53", "DNS_UPSTREAM_IPV6": "off", "BLOCK_MALICIOUS": "off", "DNS_UPDATE_PERIOD": "0", "VERSION_INFORMATION": "off", "HEALTH_RESTART_VPN": "on",
	}, Labels: map[string]string{LabelRole: runtimeRole, LabelEnvironment: in.Environment, LabelConnection: in.Connection.ID, LabelRevision: strconv.FormatUint(in.Connection.Revision, 10)}}
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
	if in.Instance != "" {
		spec.Labels[LabelInstance] = in.Instance
	}
	spec.Labels[labelPolicy] = policyLabel(spec, policy)
	return runtimeConfig{spec, policy}, nil
}

// policyIdentityVersion versions policyIdentity. Changing either replaces
// every running VPN runtime (and its desktop) on the next reconcile, so do it
// only when what defines a runtime changes on purpose.
const policyIdentityVersion = 1

// policyIdentity is what defines a VPN runtime: the container fields
// buildRuntime sets, with mount sources as the layout's paths (inside the
// supervisor's container, before the engine maps them to the host), and the
// exact enforced firewall policy. It is its own record rather than the engine
// structs, so a new engine field doesn't change existing runtimes' identity.
type policyIdentity struct {
	Version                       int
	Name, Image, Network, Restart string
	CapAdd, Devices               []string
	Env, Labels                   map[string]string
	Mounts                        []policyMount
	Firewall                      firewallPolicy
}

type policyMount struct {
	Source, Target string
	ReadOnly       bool
}

// policyLabel is the policy label's value for spec (without that label) and
// its firewall policy: a hash of their policyIdentity.
func policyLabel(spec engine.ContainerSpec, firewall firewallPolicy) string {
	id := policyIdentity{Version: policyIdentityVersion, Name: spec.Name, Image: spec.Image, Network: spec.Network, Restart: spec.Restart,
		CapAdd: spec.CapAdd, Devices: spec.Devices, Env: spec.Env, Labels: spec.Labels, Firewall: firewall}
	for _, m := range spec.Mounts {
		id.Mounts = append(id.Mounts, policyMount{Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly})
	}
	encoded, _ := json.Marshal(id)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

var runtimeRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// validRuntime checks a runtime's identity: an environment's name, or a
// connection test's random ID.
func validRuntime(id string) bool { return runtimeRE.MatchString(id) }

// runtimeName is the gateway container of the environment id. Environment
// names have no '.', so it is never an environment's container name.
func runtimeName(id string) string { return "deskyard-" + id + ".vpn" }

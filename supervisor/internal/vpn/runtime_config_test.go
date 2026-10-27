package vpn

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeConfigUsesPinnedImagePrivateSecretsAndAuthenticatedControl(t *testing.T) {
	cfg, err := buildRuntime(runtimeInput{Environment: strings.Repeat("a", 32), Network: "alice-net", Connection: Connection{ID: strings.Repeat("b", 32), Provider: "nordvpn", Country: "Netherlands", Revision: 2}, Credentials: CredentialFiles{Username: "/private/user", Password: "/private/password"}, Directory: "/private/runtime"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Spec.Image != GluetunImage || !strings.Contains(cfg.Spec.Image, "@sha256:c2ee4191762fca6b6a3c3359a731198a95d9c9af4db32d466e223bf3a84e779b") {
		t.Fatal(cfg.Spec.Image)
	}
	if strings.Join(cfg.Spec.CapAdd, ",") != "NET_ADMIN" || strings.Join(cfg.Spec.Devices, ",") != "/dev/net/tun" || cfg.Spec.NetworkContainer != "" || cfg.Spec.Restart != "no" {
		t.Fatalf("unsafe spec: %+v", cfg.Spec)
	}
	for _, key := range []string{"OPENVPN_USER", "OPENVPN_PASSWORD", "HTTP_CONTROL_SERVER_AUTH_DEFAULT_ROLE", "FIREWALL_OUTBOUND_SUBNETS"} {
		if cfg.Spec.Env[key] != "" {
			t.Fatalf("unexpected %s", key)
		}
	}
	for _, target := range []string{"/run/secrets/openvpn_user", "/run/secrets/openvpn_password", "/gluetun/auth/config.toml", "/iptables/post-rules.txt"} {
		found := false
		for _, mount := range cfg.Spec.Mounts {
			if mount.Target == target {
				found = mount.ReadOnly
			}
		}
		if !found {
			t.Fatal("missing read-only mount", target)
		}
	}
	if cfg.Spec.Env["OPENVPN_PROTOCOL"] != "udp" || cfg.Spec.Env["DNS_UPSTREAM_PLAIN_ADDRESSES"] != "1.1.1.1:53" {
		t.Fatal(cfg.Spec.Env)
	}
}

func TestFirewallSemanticPolicy(t *testing.T) {
	rules, err := buildFirewall([]Exception{{Destination: "192.168.50.10", Protocol: "tcp", Ports: []PortRange{{Start: 631}}}}, []netip.Prefix{netip.MustParsePrefix("10.213.0.0/16")}, bootstrap{Protocol: "udp", Port: 1194})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, dst, iface, proto string
		port                    uint16
		reply                   bool
		want                    string
	}{
		{"printer", "192.168.50.10", "eth0", "tcp", 631, false, "ACCEPT"},
		{"other port", "192.168.50.10", "eth0", "tcp", 80, false, "DROP"},
		{"other transport", "192.168.50.10", "eth0", "udp", 631, false, "DROP"},
		{"other desktop", "10.213.1.2", "tun0", "tcp", 443, false, "DROP"},
		{"metadata", "169.254.169.254", "tun0", "tcp", 80, false, "DROP"},
		{"direct public", "1.1.1.1", "eth0", "tcp", 443, false, "DROP"},
		{"tunnel public", "1.1.1.1", "tun0", "tcp", 443, false, "ACCEPT"},
		{"provider bootstrap", "1.2.3.4", "eth0", "udp", 1194, false, "RETURN"},
		{"desktop cannot bootstrap", "1.2.3.4", "eth0", "udp", 1194, false, "DROP"},
		{"local reply", "10.213.0.1", "eth0", "tcp", 45000, true, "ACCEPT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decide(rules.Output, netip.MustParseAddr(c.dst), c.iface, c.proto, c.port, c.reply, c.name == "provider bootstrap"); got != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
	if _, err := buildFirewall([]Exception{{Destination: "10.213.1.2", Protocol: "tcp", Ports: []PortRange{{Start: 631}}}}, []netip.Prefix{netip.MustParsePrefix("10.213.0.0/16")}, bootstrap{}); err == nil {
		t.Fatal("reserved exception accepted")
	}
	if len(rules.RawDNS) != 1 || rules.RawDNS[0].Destination.String() != "127.0.0.11/32" || rules.RawDNS[0].Action != "DROP" {
		t.Fatal("Docker DNS not denied before DNAT")
	}
	if len(rules.Routes) != 1 || rules.Routes[0].String() != "192.168.50.10/32" {
		t.Fatal("exception missing direct route", rules.Routes)
	}
	if !rules.BlockIPv6 {
		t.Fatal("IPv6 must be denied")
	}
}

func decide(rs []firewallRule, dst netip.Addr, iface, proto string, port uint16, reply, root bool) string {
	for _, r := range rs {
		if r.Destination.IsValid() && !r.Destination.Contains(dst) {
			continue
		}
		if r.Interface != "" && r.Interface != iface {
			continue
		}
		if r.Protocol != "" && r.Protocol != proto {
			continue
		}
		if r.Port.Start != 0 && (port < r.Port.Start || port > r.Port.End) {
			continue
		}
		if r.RootOnly && !root {
			continue
		}
		if r.ReplyOnly && !reply {
			continue
		}
		return r.Action
	}
	return "RETURN"
}

func TestRuntimeRejectsUnsupportedResolverPolicy(t *testing.T) {
	_, err := buildRuntime(runtimeInput{Environment: strings.Repeat("a", 32), Network: "desktop", Resolver: resolverPolicy(99)})
	if err == nil {
		t.Fatal("unsupported resolver policy accepted")
	}
}

func TestNordVPNUsesProviderDefaultPort(t *testing.T) {
	cfg, err := buildRuntime(runtimeInput{Environment: strings.Repeat("a", 32), Network: "desktop", Connection: Connection{Provider: "nordvpn", Country: "Netherlands"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, set := cfg.Spec.Env["OPENVPN_ENDPOINT_PORT"]; set {
		t.Fatal("pinned Gluetun NordVPN provider rejects a custom endpoint port before firewall/control startup")
	}
	if cfg.Spec.Env["OPENVPN_PROTOCOL"] != "udp" {
		t.Fatal("NordVPN default UDP port must remain selected")
	}
}

func policyTestInput() runtimeInput {
	return runtimeInput{Environment: "alice", Instance: strings.Repeat("c", 32), Network: "alice-net",
		Connection:  Connection{ID: strings.Repeat("b", 32), Provider: "nordvpn", Country: "Netherlands", Revision: 2},
		Credentials: CredentialFiles{Username: "/data/secret/vpn/user", Password: "/data/secret/vpn/password"},
		Directory:   "/data/state/vpn/alice", Reserved: []netip.Prefix{netip.MustParsePrefix("10.213.0.0/16")}}
}

// The policy label is the identity of a versioned record of the runtime's
// policy, not of engine structs: a new field in engine.ContainerSpec or
// engine.Mount must not change it, or every running VPN environment would be
// replaced on upgrade. Changing this value replaces them all, so only do it
// on purpose (and bump policyIdentityVersion).
func TestRuntimePolicyLabelIsStable(t *testing.T) {
	cfg, err := buildRuntime(policyTestInput())
	if err != nil {
		t.Fatal(err)
	}
	const want = "305213d84161315810b2539dc92f65efc02316a570d20fd711e5cad6f9ab1bff"
	if got := cfg.Spec.Labels[labelPolicy]; got != want {
		t.Fatalf("policy label = %s, want %s", got, want)
	}
}

// Every ContainerSpec field buildRuntime sets is part of the policy identity:
// a new one must be added to policyIdentity (and the version bumped).
func TestRuntimePolicyIdentityCoversSpec(t *testing.T) {
	in := policyTestInput()
	in.Fixture = &wireguardFixture{Endpoint: netip.MustParseAddr("192.0.2.1"), Port: 51820, PublicKey: "pub", PrivateKeyFile: "/data/secret/vpn/key", Address: "10.64.0.2/32"}
	for _, input := range []runtimeInput{policyTestInput(), in} {
		cfg, err := buildRuntime(input)
		if err != nil {
			t.Fatal(err)
		}
		covered := map[string]bool{"Name": true, "Image": true, "Network": true, "Restart": true, "CapAdd": true, "Devices": true, "Env": true, "Labels": true, "Mounts": true}
		v := reflect.ValueOf(cfg.Spec)
		for i := range v.NumField() {
			if name := v.Type().Field(i).Name; !v.Field(i).IsZero() && !covered[name] {
				t.Errorf("ContainerSpec.%s is set but not in the policy identity", name)
			}
		}
		for _, m := range cfg.Spec.Mounts {
			if m.Volume != "" || m.MustExist {
				t.Errorf("mount %+v sets a field not in the policy identity", m)
			}
		}
	}
}

// Each part of the runtime's policy changes the label.
func TestRuntimePolicyLabelChanges(t *testing.T) {
	base, err := buildRuntime(policyTestInput())
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*runtimeInput){
		"country":   func(in *runtimeInput) { in.Connection.Country = "Germany" },
		"revision":  func(in *runtimeInput) { in.Connection.Revision = 3 },
		"secret":    func(in *runtimeInput) { in.Credentials.Username = "/data/secret/vpn/other" },
		"directory": func(in *runtimeInput) { in.Directory = "/data/state/vpn/other" },
		"exception": func(in *runtimeInput) {
			in.Exceptions = []Exception{{Destination: "192.168.50.10", Protocol: "tcp", Ports: []PortRange{{Start: 631}}}}
		},
	} {
		in := policyTestInput()
		change(&in)
		cfg, err := buildRuntime(in)
		if err != nil {
			t.Fatal(name, err)
		}
		if cfg.Spec.Labels[labelPolicy] == base.Spec.Labels[labelPolicy] {
			t.Errorf("%s: policy label unchanged", name)
		}
	}
}

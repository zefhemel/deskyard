package vpn

import (
	"net/netip"
	"strings"
	"testing"
)

func TestRuntimeConfigUsesPinnedImagePrivateSecretsAndAuthenticatedControl(t *testing.T) {
	cfg, err := buildRuntime(runtimeInput{DesktopID: strings.Repeat("a", 32), Name: "alice", Network: "alice-net", Connection: Connection{ID: strings.Repeat("b", 32), Provider: "nordvpn", Country: "Netherlands", Revision: 2}, Credentials: CredentialFiles{Username: "/private/user", Password: "/private/password"}, Directory: "/private/runtime", PublishLocal: true})
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
	_, err := buildRuntime(runtimeInput{DesktopID: strings.Repeat("a", 32), Network: "desktop", Resolver: resolverPolicy(99)})
	if err == nil {
		t.Fatal("unsupported resolver policy accepted")
	}
}

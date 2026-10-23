package vpn

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

type bootstrap struct {
	Protocol    string
	Port        uint16
	Destination netip.Addr
}
type firewallRule struct {
	Destination                 netip.Prefix
	Interface, Protocol, Action string
	Port                        PortRange
	ReplyOnly                   bool
	RootOnly                    bool
}
type firewallPolicy struct {
	Output, RawDNS []firewallRule
	Routes         []netip.Prefix
	BlockIPv6      bool
}

// buildFirewall places mandatory denies before administrator exceptions. Only
// reply-direction conntrack entries bypass them; an established outgoing LAN
// connection cannot inherit the supervisor's reply allowance.
func buildFirewall(exceptions []Exception, reserved []netip.Prefix, b bootstrap) (firewallPolicy, error) {
	rules, err := NormalizeExceptions(exceptions)
	if err != nil {
		return firewallPolicy{}, err
	}
	if err = ValidateExceptionDestinations(rules, reserved); err != nil {
		return firewallPolicy{}, err
	}
	p := firewallPolicy{BlockIPv6: true, RawDNS: []firewallRule{{Destination: netip.MustParsePrefix("127.0.0.11/32"), Action: "DROP"}}}
	p.Output = append(p.Output, firewallRule{ReplyOnly: true, Action: "ACCEPT"}, firewallRule{Interface: "lo", Action: "ACCEPT"})
	// Only the fixture has a private VPN endpoint. Production bootstrap is UDP
	// 1194 to public endpoints, further restricted by Gluetun's endpoint firewall.
	if b.Destination.IsValid() {
		p.Output = append(p.Output, firewallRule{Destination: netip.PrefixFrom(b.Destination, 32), Interface: "eth0", Protocol: b.Protocol, Port: PortRange{b.Port, b.Port}, Action: "RETURN"})
	}
	for _, deny := range reserved {
		if !deny.Addr().Is4() {
			continue
		} // IPv6 is denied independently for the entire namespace.
		p.Output = append(p.Output, firewallRule{Destination: deny, Action: "DROP"})
	}
	for _, rule := range rules {
		p.Routes = append(p.Routes, netip.MustParsePrefix(rule.Destination))
		for _, port := range rule.Ports {
			p.Output = append(p.Output, firewallRule{Destination: netip.MustParsePrefix(rule.Destination), Interface: "eth0", Protocol: rule.Protocol, Port: port, Action: "ACCEPT"})
		}
	}
	for _, cidr := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4"} {
		p.Output = append(p.Output, firewallRule{Destination: netip.MustParsePrefix(cidr), Action: "DROP"})
	}
	p.Output = append(p.Output, firewallRule{Interface: "tun0", Action: "ACCEPT"})
	if !b.Destination.IsValid() && b.Port != 0 {
		p.Output = append(p.Output, firewallRule{Interface: "eth0", Protocol: b.Protocol, Port: PortRange{b.Port, b.Port}, Action: "RETURN", RootOnly: true})
	}
	p.Output = append(p.Output, firewallRule{Action: "DROP"})
	return p, nil
}
func (r firewallRule) args() string {
	var a []string
	if r.Destination.IsValid() {
		a = append(a, "-d", r.Destination.String())
	}
	if r.Interface != "" {
		a = append(a, "-o", r.Interface)
	}
	if r.Protocol != "" {
		a = append(a, "-p", r.Protocol)
	}
	if r.Port.Start != 0 {
		ports := strconv.Itoa(int(r.Port.Start))
		if r.Port.End != r.Port.Start {
			ports += ":" + strconv.Itoa(int(r.Port.End))
		}
		a = append(a, "--dport", ports)
	}
	if r.RootOnly {
		a = append(a, "-m", "owner", "--uid-owner", "0")
	}
	if r.ReplyOnly {
		a = append(a, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "--ctdir", "REPLY")
	}
	return strings.Join(append(a, "-j", r.Action), " ")
}
func (r firewallRule) taggedArgs(index int) string {
	return strings.Replace(r.args(), "-j "+r.Action, "-m comment --comment dyvpn-"+strconv.Itoa(index)+" -j "+r.Action, 1)
}
func (p firewallPolicy) commands() []string {
	commands := []string{"iptables -N DYVPN", "iptables -I OUTPUT 1 -j DYVPN"}
	for i, r := range p.Output {
		commands = append(commands, "iptables -A DYVPN "+r.taggedArgs(i))
	}
	// raw OUTPUT executes before Docker rewrites 127.0.0.11:53 to its proxy port.
	for _, r := range p.RawDNS {
		commands = append(commands, "iptables -t raw -I OUTPUT 1 "+r.args())
	}
	commands = append(commands, "ip6tables -I OUTPUT 1 -j DROP", "ip6tables -I INPUT 1 -j DROP", "ip6tables -I FORWARD 1 -j DROP")
	return commands
}
func (p firewallPolicy) text() string { return strings.Join(p.commands(), "\n") + "\n" }
func (p firewallPolicy) verification() string {
	checks := []string{"test \"$(iptables -S OUTPUT | sed -n '2p')\" = '-A OUTPUT -j DYVPN'", fmt.Sprintf("test \"$(iptables -S DYVPN | wc -l | tr -d ' ')\" = '%d'", len(p.Output)+1)}
	var order []string
	for i, r := range p.Output {
		order = append(order, strconv.Itoa(i))
		checks = append(checks, "iptables -C DYVPN "+r.taggedArgs(i))
	}
	checks = append(checks, `test "$(iptables -S DYVPN | sed -n 's/.*--comment dyvpn-\([0-9]*\).*/\1/p')" = '`+strings.Join(order, "\n")+`'`)
	for _, r := range p.RawDNS {
		checks = append(checks, "iptables -t raw -C OUTPUT "+r.args())
	}
	for _, chain := range []string{"OUTPUT", "INPUT", "FORWARD"} {
		checks = append(checks, "test \"$(ip6tables -S "+chain+" | sed -n '2p')\" = '-A "+chain+" -j DROP'")
	}
	return strings.Join(checks, " && ")
}

// A dedicated destination-only table precedes Gluetun's WireGuard lookup;
// main-table routes alone lose to that lookup. Unmatched destinations continue
// to Gluetun's normal tunnel routing. This table never contains a default route.
func (p firewallPolicy) installRoutes() string {
	if len(p.Routes) == 0 {
		return "true"
	}
	commands := []string{`gateway=$(ip -4 route show default dev eth0 | awk 'NR==1 {print $3}') && test -n "$gateway"`}
	for _, route := range p.Routes {
		commands = append(commands, "ip -4 route replace table 169 "+route.String()+` via "$gateway" dev eth0 onlink`)
	}
	commands = append(commands, `(ip -4 rule show | grep -q '^100:.*lookup 169$' || ip -4 rule add pref 100 lookup 169)`)
	return "(" + strings.Join(commands, " && ") + ")"
}
func (p firewallPolicy) verifyRoutes() string {
	if len(p.Routes) == 0 {
		return "true"
	}
	checks := []string{`ip -4 rule show | grep -q '^100:.*lookup 169$'`}
	for _, route := range p.Routes {
		checks = append(checks, "ip -4 route show table 169 exact "+route.String()+" | grep -q ' dev eth0'")
	}
	return strings.Join(checks, " && ")
}

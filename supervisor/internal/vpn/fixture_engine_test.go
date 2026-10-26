package vpn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	dockerengine "github.com/zefhemel/deskyard/supervisor/internal/engine/docker"
)

// TestControlledWireGuard is opt-in because it creates disposable Docker
// networks/containers. Every mutation is scoped to one random fixture identity.
func TestControlledWireGuard(t *testing.T) {
	if os.Getenv("DESKYARD_VPN_ENGINE_TEST") != "1" {
		t.Skip("set DESKYARD_VPN_ENGINE_TEST=1 with a Docker host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	id := hex.EncodeToString(random)
	prefix := "dy-vpn-fixture-" + id[:10]
	label := "deskyard.vpn.fixture=" + id
	command := func(input string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stdin = strings.NewReader(input)
		b, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(b)), err
	}
	must := func(input string, args ...string) string {
		t.Helper()
		b, err := command(input, args...)
		if err != nil {
			t.Fatalf("fixture docker %s failed: %v: %s", args[0], err, b)
		}
		return b
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		for _, kind := range []string{"container", "network"} {
			listArgs := []string{kind, "ls", "-q", "--filter", "label=" + label}
			if kind == "container" {
				listArgs = append(listArgs, "-a")
			}
			cmd := exec.CommandContext(cleanup, "docker", listArgs...)
			out, listErr := cmd.Output()
			if listErr != nil {
				t.Errorf("fixture cleanup discovery failed: %v", listErr)
				continue
			}
			for _, resource := range strings.Fields(string(out)) {
				args := []string{kind, "rm"}
				if kind == "container" {
					args = append(args, "-f")
				}
				args = append(args, resource)
				if output, removeErr := exec.CommandContext(cleanup, "docker", args...).CombinedOutput(); removeErr != nil {
					t.Errorf("fixture %s cleanup failed: %v %s", kind, removeErr, output)
				}
			}

			remaining, checkErr := exec.CommandContext(cleanup, "docker", listArgs...).Output()
			if checkErr != nil || len(strings.Fields(string(remaining))) != 0 {
				t.Errorf("fixture %s cleanup left resources: %s (%v)", kind, remaining, checkErr)
			}
		}
	})
	network, server, client := prefix+"-net", prefix+"-endpoint", prefix+"-client"
	must("", "network", "create", "--label", label, network)
	must("", "run", "-d", "--name", server, "--label", label, "--network", network, "--cap-add=NET_ADMIN", "--sysctl=net.ipv4.ip_forward=1", "alpine:3.22", "sh", "-ec", "apk add --no-cache wireguard-tools iptables iproute2 python3 >/tmp/setup.log 2>&1; touch /tmp/ready; sleep infinity")
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, e := command("", "exec", server, "test", "-f", "/tmp/ready"); e == nil {
			break
		}
		if time.Now().After(deadline) {
			logs, _ := command("", "exec", server, "cat", "/tmp/setup.log")
			t.Fatal("endpoint setup timeout", logs)
		}
		time.Sleep(time.Second)
	}
	serverKey := must("", "exec", server, "wg", "genkey")
	clientKey := must("", "exec", server, "wg", "genkey")
	serverPublic := must(serverKey+"\n", "exec", "-i", server, "wg", "pubkey")
	clientPublic := must(clientKey+"\n", "exec", "-i", server, "wg", "pubkey")
	must(serverKey+"\n", "exec", "-i", server, "sh", "-ec", "umask 077; cat > /tmp/server.key")
	must("", "exec", server, "sh", "-ec", "ip link add wg0 type wireguard; ip address add 10.77.0.1/24 dev wg0; wg set wg0 private-key /tmp/server.key listen-port 51820 peer "+clientPublic+" allowed-ips 10.77.0.2/32; ip link set wg0 up; iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE; iptables -I FORWARD 1 -i wg0 -p udp --dport 53 -j ACCEPT")
	endpoint := must("", "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", server)
	// A separately addressed LAN service on the endpoint's L2 network makes
	// direct LAN traffic distinguishable from traffic on the WireGuard tunnel.
	lan := "192.168.251.10"
	must("", "exec", server, "ip", "address", "add", lan+"/32", "dev", "eth0")
	must("", "exec", "-d", server, "python3", "-m", "http.server", "631", "--bind", lan)
	must("", "exec", "-d", server, "python3", "-m", "http.server", "632", "--bind", lan)
	eng, err := dockerengine.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyfile := filepath.Join(dir, "wireguard-key")
	if err = os.WriteFile(keyfile, []byte(clientKey), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &wireguardFixture{Endpoint: netip.MustParseAddr(endpoint), Port: 51820, PublicKey: serverPublic, PrivateKeyFile: keyfile, Address: "10.77.0.2/32"}
	// Exercise the same full-inventory validation/canonical policy boundary as
	// Manager, using this fixture's owned networks and configured endpoint deny.
	fixtureConfig := func(exceptions []Exception, publish bool) runtimeConfig {
		t.Helper()
		networks, err := eng.Networks(ctx, map[string]string{"deskyard.vpn.fixture": id})
		if err != nil {
			t.Fatal(err)
		}
		var discovered []netip.Prefix
		for _, n := range networks {
			if n.Subnet != "" {
				p, err := netip.ParsePrefix(n.Subnet)
				if err != nil {
					t.Fatal(err)
				}
				discovered = append(discovered, p)
			}
		}
		reserved, err := effectiveNetworkReservations([]netip.Prefix{netip.PrefixFrom(netip.MustParseAddr(endpoint), 32)}, discovered, exceptions)
		if err != nil {
			t.Fatal(err)
		}
		config, err := buildRuntime(runtimeInput{Environment: id, Network: network, Directory: dir, Fixture: f, Reserved: reserved, Exceptions: exceptions, PublishLocal: publish})
		if err != nil {
			t.Fatal(err)
		}
		return config
	}
	config := fixtureConfig(nil, true)
	config.Spec.Labels["deskyard.vpn.fixture"] = id
	config.Spec.Entrypoint = []string{"sh", "-ec", "ip route replace default via " + endpoint + " dev eth0; exec /gluetun-entrypoint"}
	if err = prepareRuntime(config, dir); err != nil {
		t.Fatal(err)
	}
	runtimeID, err := eng.Create(ctx, config.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = eng.Start(ctx, runtimeID); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(eng, nil, dir)
	if err = manager.ready(ctx, runtimeID, config.Firewall); err != nil {
		logs, _ := command("", "logs", runtimeID)
		t.Fatalf("readiness: %v\n%s", err, logs)
	}
	t.Log("PASS firewall and authenticated control ready independently of tunnel health")
	checkInventoryChurn := func(exceptions []Exception, original runtimeConfig, publish bool) {
		t.Helper()
		churn := prefix + "-churn"
		must("", "network", "create", "--label", label, churn)
		for _, added := range []bool{true, false} {
			if !added {
				must("", "network", "rm", churn)
			}
			current := fixtureConfig(exceptions, publish)
			if current.Spec.Labels[labelPolicy] != original.Spec.Labels[labelPolicy] || current.Firewall.verification() != original.Firewall.verification() {
				t.Fatal("unrelated private network changed actual policy identity")
			}
			if err := manager.ready(ctx, runtimeID, current.Firewall); err != nil {
				t.Fatal("live canonical policy readiness", err)
			}
		}
	}
	checkInventoryChurn(nil, config, true)
	t.Log("PASS live unrelated private network add/remove preserves actual ready policy without exceptions")

	// Moving an installed rule must invalidate readiness even if every rule is
	// still present and the rule count is unchanged.
	must("", "exec", runtimeID, "iptables", "-D", "DYVPN", "1")
	must("", "exec", runtimeID, "sh", "-ec", "iptables -A DYVPN "+config.Firewall.Output[0].taggedArgs(0))
	if manager.probe(ctx, runtimeID, config.Firewall.verification()) == nil {
		t.Fatal("firewall verification accepted reordered policy")
	}
	must("", "exec", runtimeID, "iptables", "-D", "DYVPN", strconv.Itoa(len(config.Firewall.Output)))
	must("", "exec", runtimeID, "sh", "-ec", "iptables -I DYVPN 1 "+config.Firewall.Output[0].taggedArgs(0))

	must("", "run", "-d", "--name", client, "--label", label, "--network", "container:"+runtimeID, "--user", "1000:1000", "--workdir", "/tmp", "nicolaka/netshoot:latest", "python3", "-m", "http.server", "8080")
	request := func(target string) bool {
		_, e := command("", "exec", client, "curl", "-kfsS", "--connect-timeout", "1", "--max-time", "3", target)
		return e == nil
	}
	eventually := func(what string, fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			if fn() {
				return
			}
			time.Sleep(time.Second)
		}
		t.Fatal("timeout: " + what)
	}
	public := "https://1.1.1.1/cdn-cgi/trace"
	eventually("tunneled request", func() bool { return request(public) })
	transfer := must("", "exec", server, "wg", "show", "wg0", "transfer")
	t.Log("PASS public HTTPS through controlled WireGuard; endpoint transfer:", transfer)
	runtime, err := eng.Inspect(ctx, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	hostURL := "http://127.0.0.1:" + strconv.Itoa(runtime.LocalPort)
	local := func() bool {
		client := http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(hostURL)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == 200
	}
	eventually("host HTTP", local)
	if request("http://" + lan + ":631") {
		t.Fatal("LAN allowed by default")
	}
	t.Log("PASS LAN denied by default")
	// An explicit route and the same production policy permit only one port.
	lanExceptions := []Exception{{Destination: lan, Protocol: "tcp", Ports: []PortRange{{Start: 631}}}}
	configWithLAN := fixtureConfig(lanExceptions, false)
	must("", "exec", runtimeID, "iptables", "-I", "OUTPUT", "1", "-j", "DROP")
	must("", "exec", runtimeID, "iptables", "-F", "DYVPN")
	for i, r := range configWithLAN.Firewall.Output {
		must("", "exec", runtimeID, "sh", "-ec", "iptables -A DYVPN "+r.taggedArgs(i))
	}
	must("", "exec", runtimeID, "iptables", "-D", "OUTPUT", "1")
	if err = manager.ready(ctx, runtimeID, configWithLAN.Firewall); err != nil {
		t.Fatal("managed LAN route readiness", err)
	}
	if !request("http://" + lan + ":631") {
		routes, _ := command("", "exec", runtimeID, "ip", "route", "get", lan)
		rules, _ := command("", "exec", runtimeID, "iptables", "-L", "DYVPN", "-n", "-v", "-x")
		detail, _ := command("", "exec", client, "curl", "-v", "--max-time", "3", "http://"+lan+":631")
		listeners, _ := command("", "exec", server, "ss", "-lnt")
		t.Fatalf("port-specific LAN exception failed: routes=%s rules=%s curl=%s listeners=%s", routes, rules, detail, listeners)
	}
	if request("http://" + lan + ":632") {
		t.Fatal("LAN exception broadened to another port")
	}
	t.Log("PASS LAN destination/port exception")
	checkInventoryChurn(lanExceptions, configWithLAN, false)
	if !request("http://"+lan+":631") || request("http://"+lan+":632") {
		t.Fatal("private inventory churn changed LAN reachability")
	}
	t.Log("PASS live unrelated private network add/remove preserves limited LAN access and ready policy")

	dnsAnswer, e := command("", "exec", client, "dig", "+short", "+time=1", "+tries=1", "@1.1.1.1", "example.com")
	if e != nil || len(strings.Fields(dnsAnswer)) == 0 {
		t.Fatal("public DNS through tunnel failed")
	}
	packets := must("", "exec", server, "sh", "-ec", `iptables -L FORWARD -n -v -x | awk '$3=="ACCEPT" && $4=="udp" {print $1; exit}'`)
	if count, e := strconv.Atoi(packets); e != nil || count == 0 {
		t.Fatal("public DNS did not traverse endpoint WireGuard interface", packets)
	}
	dnsCounter := must("", "exec", server, "iptables", "-L", "FORWARD", "-n", "-v", "-x")
	t.Log("Tunnel DNS evidence:", dnsCounter)
	if _, e := command("", "exec", client, "dig", "+time=1", "+tries=1", "@127.0.0.11", "example.com"); e == nil {
		t.Fatal("Docker resolver leaked")
	}
	if request("http://[2606:4700:4700::1111]") {
		t.Fatal("IPv6 unexpectedly available")
	}
	t.Log("PASS public DNS through tunnel, Docker DNS denied, IPv6 blocked")
	for _, route := range []string{"/v1/vpn/status", "/v1/vpn/settings", "/v1/dns/status", "/v1/publicip/ip", "/v1/portforward", "/v1/updater/status"} {
		out, _ := command("", "exec", client, "curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "http://127.0.0.1:8000"+route)
		if out != "401" {
			t.Fatalf("unauthenticated control %s returned %s", route, out)
		}
	}
	for _, route := range []string{"/v1/vpn/status", "/v1/dns/status", "/v1/portforward", "/v1/updater/status"} {
		out, _ := command("", "exec", client, "curl", "-s", "-X", "PUT", "-d", `{"status":"stopped"}`, "-o", "/dev/null", "-w", "%{http_code}", "http://127.0.0.1:8000"+route)
		if out != "401" {
			t.Fatalf("unauthenticated control mutation %s returned %s", route, out)
		}
	}
	t.Log("PASS every probed control route rejects unauthenticated desktop access")
	must("", "exec", server, "ip", "link", "set", "wg0", "down")
	if request(public) {
		t.Fatal("internet escaped during tunnel loss")
	}
	if !local() {
		t.Fatal("local service lost with tunnel")
	}
	t.Log("PASS tunnel loss blocks egress and preserves host access")
	must("", "exec", server, "ip", "link", "set", "wg0", "up")
	eventually("tunnel recovery", func() bool { return request(public) })
	t.Log("PASS tunnel recovery")
	// A second independent gateway must not share the first desktop's loopback.
	otherNet := prefix + "-other-net"
	otherClient := prefix + "-other-client"
	must("", "network", "create", "--label", label, otherNet)
	otherRandom := make([]byte, 16)
	_, _ = rand.Read(otherRandom)
	otherID := hex.EncodeToString(otherRandom)
	otherDir := filepath.Join(dir, "other")
	otherConfig, e := buildRuntime(runtimeInput{Environment: otherID, Network: otherNet, Directory: otherDir, Fixture: f})
	if e != nil {
		t.Fatal(e)
	}
	otherConfig.Spec.Labels["deskyard.vpn.fixture"] = id
	if e = prepareRuntime(otherConfig, otherDir); e != nil {
		t.Fatal(e)
	}
	otherRuntime, e := eng.Create(ctx, otherConfig.Spec)
	if e != nil {
		t.Fatal(e)
	}
	if e = eng.Start(ctx, otherRuntime); e != nil {
		t.Fatal(e)
	}
	if e = manager.ready(ctx, otherRuntime, otherConfig.Firewall); e != nil {
		t.Fatal(e)
	}
	otherState, e := eng.Inspect(ctx, otherRuntime)
	if e != nil || otherState.Health == "healthy" {
		t.Fatalf("fixture failed to prove readiness before tunnel health: %v %s", e, otherState.Health)
	}
	must("", "run", "-d", "--name", otherClient, "--label", label, "--network", "container:"+otherRuntime, "--user", "1000:1000", "--workdir", "/tmp", "nicolaka/netshoot:latest", "python3", "-m", "http.server", "8081")
	if request("http://127.0.0.1:8081") {
		t.Fatal("desktop namespaces shared loopback")
	}
	otherInspect, e := eng.Inspect(ctx, otherRuntime)
	if e != nil {
		t.Fatal(e)
	}
	for _, ip := range otherInspect.IP {
		if request("http://" + ip + ":8081") {
			t.Fatal("other desktop reachable")
		}
	}
	t.Log("PASS independent desktop namespaces and denied cross-desktop request")
	must("", "stop", "-t", "1", runtimeID)
	if request(public) {
		t.Fatal("gateway stop leaked direct egress")
	}
	t.Log("PASS gateway stop keeps client egress blocked")
	t.Log(fmt.Sprintf("Fixture %s cleanup is scoped by unique label", prefix))
}

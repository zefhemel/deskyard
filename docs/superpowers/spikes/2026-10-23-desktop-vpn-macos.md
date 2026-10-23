---
displayName: Desktop VPN on macOS spike
tags: spike
date: "2026-10-23"
---
Can an outbound VPN for an individual Deskyard desktop be tested on this Mac before deploying on Linux? **Yes: an isolated OrbStack probe passed with a real desktop container.** This is feasibility evidence for [[Use cases|the family networking use case]], not an implemented Deskyard feature or a complete enforcement audit.

# Scope

The VPN carries traffic originating inside a desktop. How a person connects to Deskyard is out of scope. Linux is the eventual deployment target; macOS is the development and testing target. The agreed failure behavior is to block internet access, reconnect automatically and preserve access to the desktop through Deskyard; only an administrator disabling VPN should restore direct access.

# Environment and topology

- macOS ARM64 with OrbStack's Docker context `orbstack`.
- Docker Engine 29.8.2, Linux ARM64 kernel `7.2.9-orbstack-00391-g2b298d05fbd4`.
- Gluetun image digest `sha256:c2ee4191762fca6b6a3c3359a731198a95d9c9af4db32d466e223bf3a84e779b`, reporting commit `156ccc2` built 2026-10-19.
- A temporary Alpine container acts as a WireGuard endpoint, with a generated test keypair, forwarding and NAT. No provider account or production credentials are involved.
- A Gluetun container connects to that endpoint using its custom WireGuard provider. It holds `NET_ADMIN` and `/dev/net/tun`; the desktop receives neither for this probe.
- A fresh `deskyard-debian-xfce:local` desktop shares Gluetun's network namespace. Its `/workspace` is temporary in-memory storage. None of the existing desktops or their folders are modified.
- Port 8080 is published on Gluetun to a dynamically assigned macOS localhost port. HTTP requests include the desktop's test Caddy token. Gluetun permits inbound port 8080; no broad outbound-LAN exception is configured.

The WireGuard endpoint and Gluetun have separate IPs on one temporary Docker network. The desktop has no separate bridge attachment or direct default route outside Gluetun's namespace. The test endpoint forwards tunneled requests to the internet through OrbStack. This proves use of a real encrypted tunnel, although it does not change the public exit to a commercial VPN location.

# Results

Both a minimal HTTP-service client and a real Deskyard desktop passed the following sequence:

| Check | Observed result |
| --- | --- |
| Request `https://1.1.1.1/cdn-cgi/trace` from the client | Succeeded; WireGuard endpoint counters showed encrypted tunnel traffic |
| Reach the service from macOS localhost | Succeeded; the real desktop's Caddy endpoint accepted the test token |
| Bring the endpoint's WireGuard interface down | Client internet request failed; local service remained reachable |
| Bring the interface back up | Client internet requests succeeded again without recreating the desktop |
| Stop the Gluetun container | Client internet request failed instead of falling back to a direct route |
| Cleanup | Temporary client, Gluetun, endpoint and Docker network removed |

The probe script is a throwaway diagnostic at `/tmp/deskyard-vpn-probe/probe.py`; `python3 /tmp/deskyard-vpn-probe/probe.py --desktop` ran the real-desktop variant. It is not production implementation or a permanent test harness. Run Docker commands outside the Codex sandbox to reach OrbStack's socket.

# Findings for the design

- A managed VPN container per enabled desktop is viable for local development on this machine. Reusing saved provider credentials does not require sharing a network namespace between different desktops.
- No macOS host-routing changes or system VPN client were needed.
- Configure forwarding for the test endpoint at container creation with `--sysctl=net.ipv4.ip_forward=1`; changing it later inside the container failed because the sysctl filesystem is read-only.
- Gluetun's plain-DNS mode requires `DNS_UPSTREAM_PLAIN_ADDRESSES`; a named resolver alone did not satisfy startup validation. The probe used `1.1.1.1:53`. This is test plumbing, not the design for enforced custom DNS.
- Deskyard will need explicit lifecycle and target-address handling for a desktop sharing its VPN container's network namespace. Its current per-desktop bridge attachment and port publication cannot simply be applied unchanged to the desktop container.

# Not yet verified

- A NordVPN connection, authentication, location selection or provider-specific reconnect behavior.
- A Linux production host, other macOS runtimes or architecture combinations.
- The supervisor's own proxy path, browser streaming, agent API and presence end to end. This probe tested direct token-authenticated HTTP reachability to Caddy.
- Replacing or restarting the VPN container, engine or supervisor while the desktop is running; startup and recovery ordering must be designed and tested.
- Startup leak races, DNS leaks, IPv6, malicious user bypass attempts, existing port-forwarding escape routes or network isolation between two VPN-enabled desktops.
- Enforced custom DNS, including a local Pi-hole or AdGuard Home combined with the tunnel.

# Recommendation

Continue the VPN design using a managed per-desktop Gluetun container as the leading approach. Build a reproducible automated test topology around a local WireGuard endpoint so routine tests need no commercial credentials. Add a separate opt-in provider test for NordVPN and test the deployment on Linux before claiming production support. Design custom DNS as the next feature on the same networking foundation.

References: [Gluetun container network sharing](https://github.com/qdm12/gluetun-wiki/blob/main/setup/connect-a-container-to-gluetun.md), [custom WireGuard provider](https://github.com/qdm12/gluetun-wiki/blob/main/setup/providers/custom.md), [firewall settings](https://github.com/qdm12/gluetun-wiki/blob/main/setup/options/firewall.md), [DNS settings](https://github.com/qdm12/gluetun-wiki/blob/main/setup/options/dns.md).

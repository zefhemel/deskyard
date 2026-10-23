# Controlled VPN runtime fixture

From `supervisor/` on this OrbStack development host:

```sh
DESKYARD_VPN_ENGINE_TEST=1 DOCKER_HOST=unix:///Users/zef/.orbstack/run/docker.sock go test ./internal/vpn -run TestControlledWireGuard -count=1 -v
```

On another Docker host, use its `DOCKER_HOST` or the normal Docker environment. The test needs Linux WireGuard kernel support, `/dev/net/tun`, NET_ADMIN, Docker CLI access, internet access for disposable Alpine tool installation and public HTTPS/DNS observations, and the pinned Gluetun image. `Ensure` automatically pulls that image on a fresh engine; this lower-level fixture expects the pinned image locally. Alpine 3.22 and netshoot images are disposable test tools, not shipped runtime dependencies.

The Go test uses the production typed runtime/firewall builder with a private custom-WireGuard input unavailable to user configuration. It creates an endpoint with ephemeral keys, a gateway and unprivileged HTTP client, and a second independent namespace. A LAN service has a separate private destination on the endpoint's L2 segment. The fixture uses that endpoint as its default router so production exception routes can be tested without altering the Docker host's routing or other desktops.

Assertions cover actual tunneled HTTPS and DNS (endpoint counters), exact ordered firewall readiness, unauthenticated control reads/mutations, host HTTP during tunnel loss, loss/recovery, gateway stop, default LAN denial, a destination/TCP-port exception with an adjacent denied port, direct Docker DNS denial, IPv6 denial, separate loopback namespaces and cross-desktop denial. Test policy replacement briefly installs a temporary output DROP guard before changing its rules. Every container/network carries a unique `deskyard.vpn.fixture` label; cleanup uses only that label on every exit, with its own bounded context. No production supervisor is launched and no existing desktop is changed.

This proves networking behavior with controlled WireGuard. It does not prove NordVPN/OpenVPN authentication, selected country availability, full desktop/proxy lifecycle behavior, or native Linux deployment support. Those remain separate release checks. Never put provider credentials in fixture files, flags, environment variables, reports or chat.

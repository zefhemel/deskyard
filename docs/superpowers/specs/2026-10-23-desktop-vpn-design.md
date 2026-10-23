---
displayName: Desktop VPN design
tags: spec
status: draft
date: "2026-10-23"
---
Administrators can route an individual desktop's internet traffic through a saved NordVPN connection. Deskyard manages a separate VPN container for each enabled desktop, blocks direct internet fallback and keeps the desktop accessible when the tunnel fails. This implements the VPN portion of [[Use cases|the family and small-company use cases]], before a separate custom DNS feature.

# Agreed scope

- This is an outbound VPN for applications inside a desktop. Connecting people or devices to Deskyard through a VPN is out of scope.
- Start with guided NordVPN configuration, service credentials and country selection. Keep provider-specific configuration behind an adapter so more providers can be added later; generic configuration imports and other provider UIs are not required for version one.
- Saved connections are reusable, but each enabled desktop gets its own tunnel and network namespace.
- On tunnel failure, block internet access and reconnect automatically. Only an administrator explicitly choosing "No VPN" restores direct internet access.
- Block desktop-initiated access to the home network by default, with administrator-configured exceptions per desktop. Preserve the narrow access needed for Deskyard to serve the desktop.
- Allow a desktop restart when enabling, disabling or changing its VPN settings. Ordinary tunnel reconnects do not restart the desktop.
- Keep existing agent port forwarding unchanged. The feature controls the ordinary network path; it does not claim to prevent deliberate tunnels or proxies through allowed applications or existing forwarding. Do not describe it as a complete parental-control boundary.
- Linux is the deployment target. OrbStack on macOS is the first development-test environment. [[superpowers/spikes/2026-10-23-desktop-vpn-macos|Desktop VPN on macOS spike]] establishes local tunnel and failure-blocking feasibility; production support still requires the tests below.

# Architecture choice

Use a Deskyard-managed Gluetun container per VPN-enabled desktop. The desktop shares that container's network namespace and has no independent bridge attachment or internet route. The VPN container owns the bridge endpoint, published port, tunnel and firewall. The desktop keeps its existing filesystem, services and resource limits and receives no additional network privileges.

Host-based policy routing would require platform-specific setup on Linux and the Linux VM under macOS. Running a VPN client inside the desktop would mix enforcement, credentials and networking privileges with the user's environment. The separate container is the preferred balance and is the approach tested in the spike.

The supervisor reaches desktop Caddy on port 8080 through the VPN container's network endpoint. When the supervisor runs in Docker, it joins the existing per-desktop bridge as today and uses the gateway endpoint for the desktop target. When run directly, it uses a loopback-only published port on the VPN container. Exec, statistics, image inspection, file access and activity monitoring continue to target the desktop container itself.

No shared tunnel containers across desktops: overlapping listeners, localhost access and application identity must remain isolated. Multiple tunnels using the same NordVPN account count toward that account's connection limit; explain provider-limit errors rather than silently sharing namespaces.

# Components and boundaries

- A new `supervisor/internal/vpn/` package owns saved connections, credential storage, provider configuration, VPN runtime specifications, status and validation. The NordVPN adapter generates a fixed supported configuration, not arbitrary user-supplied container flags.
- A separate networking/lifecycle collaborator integrates VPN runtimes with `supervisor/internal/desktops/`. Desktop lifecycle coordinates the pair; HTTP handlers must not call the Docker SDK directly. Keep provider logic and reconciliation out of the existing large desktop service file.
- Extend `supervisor/internal/engine/` and its Docker adapter with explicit network-namespace sharing, VPN capabilities/devices, container health and restart-policy update support. Network sharing is mutually exclusive with attaching networks or publishing ports on the desktop container.
- `supervisor/internal/server/` exposes administrator configuration and sanitized per-desktop status using the existing session authorization, same-origin protection, jobs and events.
- `supervisor/web/` adds saved connections and desktop network settings using existing forms and job progress.

Use distinct runtime labels, including a role such as `deskyard.role=vpn` and the owning stable desktop ID. Existing containers with no role remain desktops. Every list, event consumer, reconciliation and cleanup path must distinguish VPN runtimes from desktops so a gateway cannot appear as an extra desktop or receive desktop operations.

# Saved connections and secrets

A connection has an immutable ID, unique display name, provider (`nordvpn`), country, configuration revision and credential reference. Version one uses NordVPN's documented OpenVPN service-credential setup over UDP; protocol selection is not a UI option. A local WireGuard endpoint remains useful for provider-independent networking tests, but does not substitute for a live OpenVPN/NordVPN test.

Store connection metadata atomically under `.deskyard/vpn/connections.json`, and secrets in separate owner-only files under `.deskyard/vpn/secrets/` (directories mode 0700, files mode 0600). Follow the repository's store conventions. This protects secrets with filesystem permissions, not a claim of encryption against the host administrator. Include these files in administrator backup guidance; never put them in `/workspace`, desktop images or container labels.

Supply OpenVPN credentials to Gluetun through read-only secret files using its documented `OPENVPN_USER_SECRETFILE` and `OPENVPN_PASSWORD_SECRETFILE` settings. Mount only the credentials needed for that connection into its VPN runtime. Desktop containers share networking only, not these mounts, process namespaces or the Docker socket. Check the host-path requirements in both direct and containerized supervisor modes.

API reads return `credentialsConfigured`, never credential values. Write payloads may set new credentials; omitted credentials on an edit retain the existing ones. Redact secrets from errors, logs and job output, including rejected provider responses. Raw Gluetun logs are not a public API.

Use a pinned, tested Gluetun image digest rather than a moving `latest` tag. Image upgrades are explicit implementation/release changes, with the network and lifecycle suite rerun. Do not add proxy services that Gluetun can optionally expose.

The shared network namespace makes Gluetun's localhost services reachable from desktop applications. Require authenticated access to its control API with a generated secret held outside the desktop; expose only the status routes the supervisor needs and no unauthenticated settings or mutation routes. Do not publish that API to the LAN. Docker-host administrators remain trusted.

# Administrator experience

A "VPN connections" section lets an administrator add a named connection, choose NordVPN and a country, enter service credentials and select "Test connection". Link to NordVPN's instructions for obtaining service credentials and explain that they are different from the normal account login. The test uses a disposable runtime with the candidate settings, reports authentication, tunnel and egress results, and removes the runtime afterward. It never changes an existing desktop.

Connection tests are jobs with cancellation and a two-minute overall deadline. Provide a useful sanitized error for rejected credentials, an unavailable country/server, missing host capabilities, image-pull failure and timeout. A passing test is evidence for the settings at that time, not a guarantee of future availability. A failed candidate may be edited or discarded without replacing a working saved revision.

On desktop creation and its Settings tab, offer "No VPN" or a saved connection. Show the chosen provider/country, current VPN state and local-network exceptions. Changing a running desktop uses the existing "Save and restart" pattern and warns that sessions briefly disconnect while files remain intact.

An exception contains an IP address or CIDR, transport (`tcp` or `udp`) and explicit ports or port ranges. Support multiple rules; validate and normalize addresses and ranges, reject default-route-sized catch-all rules, and explain that exceptions intentionally permit direct local traffic outside the VPN. LAN exceptions apply to that desktop, not every desktop using the connection. Do not turn an IP-and-port exception into an unrestricted subnet allowance.

Editing credentials or country on a shared connection lists affected desktops before "Save and restart affected desktops". Validate the candidate first, then serialize changes per desktop; do not stop every desktop merely to test credentials. A connection with any desktop reference cannot be deleted. A failed rollout keeps the desired selected revision and shows each affected desktop's result; retry uses that revision and never silently selects "No VPN".

Only administrators can manage connections, choose VPN settings or edit exceptions. Users with desktop access can see that desktop's sanitized connection status and errors, but cannot list secret configuration or change its network policy.

# Routing and firewall

For a VPN-enabled desktop, ordinary internet traffic and public DNS resolution must use the tunnel. Explicit local exceptions and the provider transport/bootstrap needed to establish the tunnel are the only direct egress allowances. Keep bootstrap limited to the VPN runtime's needs; do not allow desktop processes to use Docker's embedded DNS as an unrestricted direct resolver. Verify this rather than assuming Gluetun's default resolver configuration covers Docker's local DNS forwarding.

Block direct access to the home LAN, host services, link-local destinations and other desktop networks. Cover dynamically allocated Docker subnets as well as the user's LAN. The supervisor may initiate the token-authenticated connection to Caddy; allow its replies without granting the desktop general outbound access to the supervisor. Use port-specific exceptions and stateful replies, not a blanket `FIREWALL_OUTBOUND_SUBNETS` entry for the control network. This requires managed firewall rules in addition to Gluetun's broad subnet convenience setting.

Version one does not route public IPv6 through the VPN. Disable or block external IPv6 in the shared namespace and test it cannot become a direct fallback. Do not assume an IPv4-only Docker bridge eliminates all IPv6 escape paths.

Exceptions must not bypass the mandatory deny rules for other desktops or expose Gluetun management services. If an administrator needs a host/LAN service such as a printer, permit only its configured destination, protocol and ports. Link-local/metadata destinations remain denied in version one. Do not auto-detect and allow the entire home subnet.

Keep the current agent forwarding functionality and token permissions unchanged, as explicitly requested. Document this scope next to the feature's network protection explanation. Do not claim resistance to a user deliberately running another proxy or tunnel over otherwise permitted traffic.

# Lifecycle, persistence and recovery

Store per-desktop VPN assignment, LAN rules, desired power state, selected connection revision and pending network transition under `.deskyard/vpn/desktops.json`, keyed by stable desktop ID. This is authoritative network intent and a small recovery journal, not a replacement registry of every desktop. Container labels retain the stable desktop ID, selected connection ID/revision and runtime role for discovery. Preserve enough desktop settings in a pending transition to recover after its old container has been removed. List/get responses must surface interrupted transitions even while the desktop container is absent, so the administrator can see the error and retry rather than losing the desktop from the UI.

Enabling/disabling VPN and changing network configuration run under the existing per-desktop operation lock. Before committing a change, turn off independent automatic restart of the existing desktop, stop it cleanly and persist the transition. Commit the desired selection before starting a replacement. Failures after committing VPN selection must not restore or start the old direct-network container. Failed or interrupted transitions resume from their persisted intent; intentional disabling is a distinct administrator operation.

VPN-enabled desktop containers use no independent Docker restart policy. Deskyard starts them only after the VPN namespace's firewall is installed and its management path is ready. The desktop may then run and remain locally accessible while the tunnel is connecting or unavailable; connected status requires a healthy tunnel. Startup readiness requires checking that the expected managed firewall/routing policy has been installed and the authenticated runtime management endpoint responds; it must not rely on a guessed delay or merely "container running". Without the supervisor after an engine reboot, these desktops remain stopped rather than racing VPN initialization.

Keep the VPN runtime running when an administrator stops a desktop in version one. This preserves its namespace and avoids recreating the desktop solely to start it again; show it as idle and document that it may continue to consume a provider connection. Restarting a desktop restarts its applications, not the VPN runtime. Removing a desktop removes its VPN runtime, secret mounts and per-desktop intent; keep the saved reusable connection and preserve/delete its workspace according to the existing removal choice.

A transient tunnel failure leaves the namespace and firewall intact. Let Gluetun reconnect with bounded retry backoff; keep direct egress blocked and desktop service reachable. Health changes update VPN status independently of desktop power state.

If the VPN container exits or must be replaced, do not assume a desktop automatically joins the new namespace. Stop the desktop, recreate the runtime, wait for firewall/control readiness and recreate the desktop attached to that runtime, preserving its files and identity. Reapply the selected policy before restoring the desired running state. A missing or invalid saved connection is an error requiring repair, never permission to use direct networking.

At supervisor startup, reconcile saved intent, containers, namespaces and revisions before starting VPN-enabled desktops. At engine restart, detect dependency replacement and use the same ordered recovery. Keep cleanup ownership explicit so retry and cancellation remove only the current operation's resources and never another desktop's runtime. Report stopped/blocked errors when recovery cannot finish; do not delete user files during recovery.

Coordinating changes to a shared connection uses the same durable per-desktop mechanism. If one desktop fails, report partial results and keep its selected VPN policy; do not convert a partial operation into direct access or hide stale revisions.

# API and status contract

Use authenticated administrator endpoints under `/api/vpn/connections` for list/create, `/api/vpn/connections/{id}` for edit/delete, and `/api/vpn/test` for disposable candidate tests. Tests and affected-desktop updates use the existing job mechanism. API responses are allow-listed public types, separate from secret-bearing store/runtime types.

Extend desktop creation and `PATCH /api/desktops/{name}` with `vpnConnectionId` (null for "No VPN") and `lanExceptions`. Omission preserves existing settings. Existing clients and desktops retain direct networking unless VPN is selected. Invalid IDs, conflicting operations and references preventing deletion produce explicit validation/conflict responses.

Desktop JSON and events expose a `vpn` object with selected connection ID, public name/provider/country, desired/applied revision, state, last state-change time, sanitized reason and last verified exit IP when available. Use the existing events stream; keep the raw gateway address, credentials and control API token private.

| State | Meaning |
| --- | --- |
| disabled | Administrator selected "No VPN" |
| stopped | Desktop is stopped; no claim about active desktop egress |
| connecting | Initial tunnel connection is being established; internet unavailable |
| connected | Tunnel health and egress checks pass |
| reconnecting | A previously connected tunnel is recovering; internet unavailable |
| blocked | Configuration/runtime failure needs attention; no direct fallback |

Retain the last known exit IP only with its observation timestamp and never label it as current while disconnected. Poll managed runtime health on a bounded interval (five seconds initially), publish changes through existing events and bound any external IP checks to avoid per-viewer polling. A runtime's "running" process state alone is insufficient evidence of connectivity.

# Custom DNS follow-up

Custom DNS gets its own design and plan after VPN. Leave an explicit resolver-policy input at the networking boundary so a saved DNS policy can apply with or without VPN; do not expose arbitrary Gluetun environment configuration as a substitute. Version one uses a supported public resolver through the VPN. A later local Pi-hole/AdGuard Home resolver may require a narrowly scoped direct DNS exception, which must be explained as an intentional policy exception rather than silently leaking queries. DNS override enforcement and outage behavior are decisions for that design.

# Verification and release criteria

Use local disposable networks and credentials for routine integration tests; never depend on NordVPN secrets in ordinary CI. The automated fixture should create a controlled VPN endpoint and observable local/LAN services. Promote the throwaway spike into an isolated test fixture only during implementation, with bounded readiness, unique names, labels and cleanup on all exits.

- Unit/API checks: store permissions and atomic writes, revisions, redaction, administrator authorization, exception validation, legacy direct-network behavior and cancellation/conflict handling.
- Engine checks: valid/invalid namespace configurations, VPN capabilities and devices, private port publication, role-aware listing/events, and no extra desktop privileges.
- Runtime checks: requests travel through the tunnel; normal desktop networking cannot reach the host, another desktop or LAN targets by default; only allowed destination/port pairs work; local desktop access works during tunnel loss.
- Failure checks: failed authentication, unreachable server, startup before tunnel, tunnel drop, VPN process exit, gateway container replacement, supervisor restart, engine restart and cancellation at transition boundaries. Observe traffic during transitions to check for direct egress, including existing connections.
- DNS/IPv6 checks: prove public DNS follows the VPN and Docker DNS cannot leak direct queries; verify external IPv6 is unavailable rather than a fallback. These checks do not claim prevention of deliberate alternate application tunnels.
- Desktop checks: full supervisor-proxied screen connection, browser use, exec, files, activity, start/stop/restart, upgrades and removal. Run with both direct and containerized supervisor, and with two VPN-enabled desktops to check isolation.
- macOS checks: run the fixture and full desktop workflow under OrbStack on this machine.
- Linux checks: run the same runtime/lifecycle suite on a native Linux Docker host before declaring deployment support.
- Opt-in NordVPN check: use locally supplied service credentials to test OpenVPN authentication, country choice, egress, failure/recovery and credential edits. The user has an account available; no credentials belong in source, docs or chat.

Update `README.md`, `supervisor/API.md`, [[Architecture/VPN connections]], [[Architecture/VPN runtime]], [[Architecture/Lifecycle]], [[Architecture/Desktop proxy]] and [[Use cases]] as implementation lands. Mark planned components built and this spec implemented only after the release criteria pass; record unavailable provider/Linux checks as limitations, not successes.

# Sources

- [NordVPN service credentials](https://support.nordvpn.com/hc/en-us/articles/19685514639633-How-to-find-your-service-credentials-for-manual-NordVPN-setup) and [NordVPN's Gluetun/OpenVPN setup](https://support.nordvpn.com/hc/en-us/articles/47830508425745-How-to-set-up-an-OpenVPN-manual-connection-to-NordVPN-with-Gluetun).
- [Gluetun network sharing](https://github.com/qdm12/gluetun-wiki/blob/main/setup/connect-a-container-to-gluetun.md), [secret files](https://github.com/qdm12/gluetun-wiki/blob/main/setup/advanced/docker-secrets.md), [firewall settings](https://github.com/qdm12/gluetun-wiki/blob/main/setup/options/firewall.md), [authenticated control API](https://github.com/qdm12/gluetun-wiki/blob/main/setup/advanced/control-server.md) and [DNS settings](https://github.com/qdm12/gluetun-wiki/blob/main/setup/options/dns.md).
- [Docker networking](https://docs.docker.com/engine/network/) for container network sharing, DNS and bridge behavior.

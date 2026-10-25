---
displayName: Desktop VPN design
tags: spec
status: implemented
date: "2026-10-23"
features:
  - "[[Features/Outbound VPN]]"
---
An administrator can route each desktop's ordinary outbound internet traffic through a saved NordVPN connection. Each enabled desktop has its own managed Gluetun gateway/network namespace, preserving local desktop access while blocking direct fallback. This implements outbound VPN; custom DNS remains separate discovery scope.

# Enforcement and boundaries
The desktop shares its dedicated gateway namespace with no independent bridge/route or extra network privileges. Gateway containers own tunnel, firewall, bridge endpoint and publication; application/filesystem/resource ownership stays with the desktop. A native supervisor uses loopback publication and a containerized supervisor joins the control bridge. Exec/files/stats/activity still target the desktop container.

Separate gateways preserve localhost/listener isolation and avoid sharing credentials/firewall privileges with desktop processes. The gateway blocks ordinary direct internet, public DNS fallback, host/LAN/link-local/metadata/other-desktop access and external IPv6. Provider bootstrap is narrowly allowed. Public DNS follows the tunnel; Docker embedded DNS must not become an unrestricted direct resolver. Supervisor-initiated token-authenticated Caddy access has narrow stateful replies rather than unrestricted control-subnet egress.

Administrator exceptions are per desktop and contain normalized IP/CIDR, TCP/UDP and explicit ports/ranges. They permit deliberate direct local traffic without overriding mandatory deny rules or exposing gateway management. Catch-all routes and automatic blanket LAN allowances are rejected. Existing token forwarding remains unchanged; an allowed application or forwarding tunnel can deliberately bypass ordinary-network policy. This is not a complete parental-control boundary.

# Connections, secrets and UI
Reusable saved connections use NordVPN service credentials and country selection behind a provider adapter. Secret revisions and private runtime/control credentials live under `.deskyard/vpn/`, with owner-only directories/files and no secret-bearing API reads/logs. Blank edit fields retain credentials. Host administrators can read stored secrets. A disposable candidate test validates proposed credentials/configuration without stopping every referenced desktop.

Administrators manage connection CRUD/testing and desktop policy; regular users see only authorized sanitized status. Referenced connections cannot be deleted. Shared edits identify affected desktops and validate before serialized rollout. Partial rollout keeps desired selected revisions and per-desktop errors, never silent No VPN. Explicit No VPN is the administrator operation that restores direct networking.

Create/settings accept `vpnConnectionId` (null disables) and `lanExceptions`; omission preserves existing configuration. Running policy changes warn of recreation/disconnection while preserving files. Status distinguishes selected/applied revision, desktop power and tunnel health; a stale verified exit IP must not imply a current connection. API public types exclude secret/runtime details. See `supervisor/API.md` and [[Architecture/VPN connections]].

# Durable lifecycle
`.deskyard/vpn/desktops.json` stores stable-ID network intent, desired power, selected revision and complete pending-transition snapshots. It is authoritative for network recovery, not a replacement inventory of all desktops. Interrupted records remain visible even when their container is absent.

Under the desktop reservation, preflight host sources before stopping the old desktop, disable its independent restart, persist complete transition intent and commit selected policy before replacement. Once VPN is committed, failure cannot restart the old direct container. VPN desktops have no independent Docker restart policy: the supervisor establishes firewall/authenticated control readiness before starting them. They can remain locally accessible while the tunnel is unavailable. Without supervisor recovery after engine reboot, they remain stopped rather than racing initialization.

Stopping a desktop leaves its gateway idle/running and potentially consuming a provider connection. Application restart reuses the namespace. Tunnel loss retains blocking rules and lets Gluetun reconnect. Gateway replacement requires stopping/recreating its desktop consumer into the new namespace, preserving stable identity, workspace, settings and desired power. Missing credentials/connections or failed recovery produce retained blocked/error state, never direct fallback. Explicit disable/removal transitions are recoverable too. Removing a desktop cleans its owned runtime/intent while preserving reusable connections and respecting the workspace deletion choice.

Startup and background reconciliation order dependency readiness and desired-power restoration. Cleanup/cancellation owns only the current operation's resources. Managed-network intent stays separate from direct desktops' pinned-image rollback. Host-folder snapshots own independent mount slices; folder-only and combined network/folder settings use the same validated payload. See [[Architecture/VPN runtime]] and [[Architecture/Lifecycle]].

# Evidence and remaining checks
`supervisor/tests/VPN.md` describes the ownership-scoped fixture that avoids reconciling unrelated engine resources. OrbStack ARM64 controlled WireGuard checks exercised two independent tunnels, DNS/firewall/LAN/IPv6, loss/recovery, namespace isolation, native/containerized supervisors, real Selkies frames, exec/files/CDP/activity, shared terminal and persisted lifecycle recovery.

A subsequent production NordVPN preview authenticated using locally entered service credentials and provided ordinary egress and user-observed Chromium access to a MATE desktop. This establishes live authentication/access on OrbStack; exit country was not independently geolocated. Country switching, credential edits, provider concurrency limits, native Linux and actual host/engine reboot remain unverified. Fixture service replacement is not host-reboot evidence. Background observation timing is not a guaranteed polling deadline.

Earlier uncaptured forwarding packets and intermittent recovery failures remain individually unclassified. A later fully captured case proved late server replies; passing retries do not retroactively settle earlier observations. The transient MATE picture/input report has separate evidence in [[Testing/MATE stream startup 2026-10-24]], and does not establish a VPN enforcement defect. Implementation status does not remove these deployment-verification limits.

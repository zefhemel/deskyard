---
tags: component
status: planned
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Accounts]]"
  - "[[Architecture/VPN runtime]]"
spec: "[[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]]"
---
Planned administrator-managed saved outbound VPN configurations, initially NordVPN service credentials and country selection. Reuse configuration across desktops while keeping each desktop's tunnel separate. Connection tests run in disposable runtimes; public API types omit secrets. Proposed implementation: `supervisor/internal/vpn/`, administrator routes in `supervisor/internal/server/` and settings in `supervisor/web/`. Configuration and credentials live under the supervisor's private `.deskyard/vpn/`, never in a desktop's workspace or image.

The storage foundation is implemented in `supervisor/internal/vpn/connections.go`: generated stable IDs, case-insensitive unique names, canonical NordVPN country choices, incrementing revisions and secret-free connection DTOs. The store takes the private `.deskyard` directory and writes metadata atomically in `vpn/connections.json`; read-only credential mounts use separate 0600 username/password files in 0700 version directories below `vpn/secrets/<connection-id>/`. Credential edits preserve existing mount files until the connection is deleted. Omitted credentials retain their saved values; write-only input rejects JSON serialization. Administrator routes, reference-aware deletion, disposable tests and the guided UI remain planned.

`supervisor/internal/vpn/policy.go` validates and canonicalizes private IPv4 destinations, TCP/UDP and explicit inclusive port ranges. It rejects public, link-local/metadata and IPv6 exceptions, merges equivalent rules and rejects all-port allowances, including effective port unions across nested destination prefixes. Runtime integration must additionally validate overlapping control/other-desktop networks with `ValidateExceptionDestinations` and keep mandatory deny rules ahead of any exceptions. No firewall is installed by these validation helpers.

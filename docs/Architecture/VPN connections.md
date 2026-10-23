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

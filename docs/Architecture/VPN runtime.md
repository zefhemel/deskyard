---
tags: component
status: planned
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Desktop]]"
spec: "[[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]]"
---
Planned managed Gluetun container per VPN-enabled desktop. It owns the bridge endpoint, published desktop port, tunnel and firewall; the desktop shares only its network namespace and gets no additional network privileges. Blocks direct internet fallback and home-network access, with administrator-configured destination/port exceptions. Existing CLI port forwarding remains unchanged and is outside this feature's bypass-prevention scope.

The supervisor must coordinate startup, replacement and recovery and resolve desktop proxy targets through this runtime. Proposed implementation: `supervisor/internal/vpn/`, a desktop networking/lifecycle collaborator, and explicit namespace/capability/health/restart support in `supervisor/internal/engine/`. The [[superpowers/spikes/2026-10-23-desktop-vpn-macos|macOS spike]] demonstrated a tunnel and local Caddy reachability; complete supervisor integration and provider/Linux validation are still planned.

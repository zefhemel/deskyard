---
displayName: "Outbound VPN"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "Saved NordVPN connections select a per-desktop Gluetun gateway with fail-closed internet, explicit LAN exceptions and durable recovery."
outcome: "Choose an administrator-managed outbound route for a desktop."
acceptance:
  - "Tunnel loss blocks ordinary internet access while authenticated desktop control remains available."
  - "Explicit No VPN transitions, saved revisions and workspace-preserving recovery follow persisted intent."
limitations:
  - "Stopping a desktop leaves its gateway and possible provider connection cost."
  - "Custom DNS and general application-level bypass prevention are outside this implementation."
  - "Native Linux, full host reboot and provider-change coverage remain release checks."
components:
  - "[[Architecture/VPN connections]]"
  - "[[Architecture/VPN runtime]]"
  - "[[Architecture/Lifecycle]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/vpn/runtime_config.go`
- `supervisor/internal/desktops/networking.go`
- `supervisor/internal/server/vpn.go`
- `supervisor/tests/VPN.md`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.

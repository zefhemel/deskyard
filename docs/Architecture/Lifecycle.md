---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Accounts]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The lifecycle of desktops as jobs: create (reusing a folder that's already there), start, stop, restart, remove, change settings, upgrade. Desktops and their settings are container labels; the engine is the only list of them.

The [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]] plans paired desktop/VPN-runtime lifecycle and durable per-desktop network intent for transition recovery. VPN-enabled desktops will start only after firewall readiness and will need coordinated recreation when their shared network namespace is replaced. This is planned behavior, not the current lifecycle implementation.

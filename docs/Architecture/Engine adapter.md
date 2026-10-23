---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Container engine]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The supervisor's own interface to the container engine, with one implementation now (Docker, via its Go SDK) and room for Podman. Nothing else in the supervisor talks to the engine.

The [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]] plans explicit network-namespace sharing, VPN runtime capabilities/devices, health inspection and restart-policy control for [[Architecture/VPN runtime]]. These extensions are not implemented yet.

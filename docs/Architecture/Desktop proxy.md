---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Accounts]]"
  - "[[Architecture/Caddy]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Serves what's under `/<name>/vnc/`: the KasmVNC web client extracted from the desktop's image (never from the running desktop), and a WebSocket proxy to the desktop's port 8080 with the per-desktop token. Knows which accounts are connected to each desktop and streams that to the desktop pages.

For VPN-enabled desktops, the [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]] plans to resolve the Caddy target through [[Architecture/VPN runtime]] while continuing to authenticate with the desktop's token. Runtime target resolution and full proxy validation are not implemented yet.
